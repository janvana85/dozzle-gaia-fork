package web

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/amir20/dozzle/internal/auth"
	"github.com/amir20/dozzle/internal/container"
	container_support "github.com/amir20/dozzle/internal/support/container"
	support_web "github.com/amir20/dozzle/internal/support/web"
	"github.com/amir20/dozzle/internal/utils"
	"github.com/dustin/go-humanize"
	"github.com/go-chi/chi/v5"

	"github.com/rs/zerolog/log"
)

type cacheGapEvent struct {
	Type        string `json:"t"`
	Message     string `json:"m"`
	Timestamp   int64  `json:"ts"`
	Id          int64  `json:"id"`
	Level       string `json:"l"`
	Stream      string `json:"s"`
	ContainerID string `json:"c"`
	From        string `json:"from"`
	To          string `json:"to"`
}

func parseStdTypes(r *http.Request) container.StdType {
	var stdTypes container.StdType
	if r.URL.Query().Has("stdout") {
		stdTypes |= container.STDOUT
	}
	if r.URL.Query().Has("stderr") {
		stdTypes |= container.STDERR
	}
	return stdTypes
}

func matchesFilter(event *container.LogEvent, regex *regexp.Regexp, levels map[string]struct{}, inverse bool) bool {
	if regex != nil && inverse == support_web.Search(regex, event) {
		return false
	}
	if len(levels) == 0 {
		return true
	}
	_, ok := levels[event.Level]
	return ok
}

func matchesStdType(event *container.LogEvent, stdTypes container.StdType) bool {
	switch event.Stream {
	case "stdout":
		return stdTypes&container.STDOUT != 0
	case "stderr":
		return stdTypes&container.STDERR != 0
	default:
		return true
	}
}

func newCacheGapEvent(containerID string, from, to time.Time) cacheGapEvent {
	ts := from.UnixMilli()
	return cacheGapEvent{
		Type:        "cache-gap",
		Message:     "not found in cache, fetching from docker logs",
		Timestamp:   ts,
		Id:          -ts,
		Level:       "info",
		Stream:      "stderr",
		ContainerID: containerID,
		From:        from.Format(time.RFC3339Nano),
		To:          to.Format(time.RFC3339Nano),
	}
}

func (h *handler) scheduleCacheGapFill(containerService *container_support.ContainerService, from, to time.Time, stdTypes container.StdType) {
	if h.logStore == nil || containerService == nil || from.IsZero() || to.IsZero() || !from.Before(to) {
		return
	}

	c := containerService.Container
	key := fmt.Sprintf("%s:%s:%d:%d:%d", c.Host, c.ID, from.UnixNano(), to.UnixNano(), stdTypes)
	if _, loaded := h.gapFillJobs.LoadOrStore(key, struct{}{}); loaded {
		return
	}

	go func() {
		defer h.gapFillJobs.Delete(key)

		if err := h.logStore.RecordContainer(c); err != nil {
			log.Debug().Err(err).Str("container", c.ID).Msg("failed to record cached container metadata")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		events, err := containerService.LogsBetweenDates(ctx, from, to, stdTypes)
		if err != nil {
			log.Debug().Err(err).Str("container", c.ID).Time("from", from).Time("to", to).Msg("failed to fill log cache gap")
			return
		}

		count := 0
		for event := range events {
			if err := h.logStore.AppendForContainer(c, event); err != nil {
				log.Debug().Err(err).Str("container", c.ID).Msg("failed to append log cache gap event")
				continue
			}
			count++
		}
		log.Debug().Str("container", c.ID).Int("lines", count).Time("from", from).Time("to", to).Msg("filled log cache gap")
	}()
}

func (h *handler) resolveLabels(r *http.Request) container.ContainerLabels {
	labels := h.config.Labels
	if h.config.Authorization.Provider != NONE {
		user := auth.UserFromContext(r.Context())
		if user.ContainerLabels.Exists() {
			labels = user.ContainerLabels
		}
	}
	return labels
}

func (h *handler) fetchLogsBetweenDates(w http.ResponseWriter, r *http.Request) {
	plainText := strings.Contains(r.Header.Get("Accept"), "text/plain")
	if plainText {
		w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	} else {
		w.Header().Set("Content-Type", "application/x-jsonl; charset=UTF-8")
	}

	from, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("from"))
	to, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("to"))
	// Guard against an inverted range (from after to). The frontend can compute
	// from > to on merged/unsorted windows; left as-is it makes the backward
	// expansion loop below spin with empty fetches until from drifts past to.
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		from, to = to, from
	}
	id := chi.URLParam(r, "id")

	stdTypes := parseStdTypes(r)
	if stdTypes == 0 {
		http.Error(w, "stdout or stderr is required", http.StatusBadRequest)
		return
	}

	containerService, findErr := h.hostService.FindContainer(hostKey(r), id, h.resolveLabels(r))
	// Prefer the local log store: it is indexed by time, so historical scroll-back
	// is fast, whereas reading old logs from the agent/Docker scans the json-file
	// sequentially (seconds to minutes for old ranges).
	usingStore := false
	if h.logStore != nil {
		if containerService != nil {
			usingStore = true
		} else if findErr != nil {
			usingStore = h.logStore.HasLogs(hostKey(r), id)
		}
	}
	if findErr != nil && !usingStore {
		http.Error(w, findErr.Error(), http.StatusNotFound)
		return
	}

	delta := max(to.Sub(from), time.Second*3)

	var err error
	var regex *regexp.Regexp
	if r.URL.Query().Has("filter") {
		regex, err = support_web.ParseRegex(r.URL.Query().Get("filter"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	inverse := r.URL.Query().Get("inverse") == "true"

	onlyComplex := r.URL.Query().Has("jsonOnly")
	everything := r.URL.Query().Has("everything")
	if everything {
		from = time.Time{}
		to = time.Now()
	}

	minimum := 0
	buffer := utils.NewRingBuffer[*container.LogEvent](500)
	if r.URL.Query().Has("min") {
		minimum, err = strconv.Atoi(r.URL.Query().Get("min"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if minimum < 0 || minimum > buffer.Size {
			http.Error(w, "minimum must be between 0 and buffer size", http.StatusBadRequest)
			return
		}
		buffer = utils.NewRingBuffer[*container.LogEvent](minimum)
	}

	maxStart := math.MaxInt
	if r.URL.Query().Has("maxStart") {
		maxStart, err = strconv.Atoi(r.URL.Query().Get("maxStart"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if maxStart < 1 || maxStart > buffer.Size {
			http.Error(w, "invalid maxStart", http.StatusBadRequest)
			return
		}
	}

	levels := make(map[string]struct{})
	for _, level := range r.URL.Query()["levels"] {
		levels[level] = struct{}{}
	}

	lastSeenId := uint32(0)
	if r.URL.Query().Has("lastSeenId") {
		to = to.Add(50 * time.Millisecond)
		num, err := strconv.ParseUint(r.URL.Query().Get("lastSeenId"), 10, 32)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		lastSeenId = uint32(num)
	}

	startId := uint32(0)
	if r.URL.Query().Has("startId") {
		from = from.Add(-50 * time.Millisecond)
		num, err := strconv.ParseUint(r.URL.Query().Get("startId"), 10, 32)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		startId = uint32(num)
	}

	var writer io.Writer = w
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		gzWriter := gzip.NewWriter(w)
		defer gzWriter.Close()
		writer = gzWriter
	}
	encoder := json.NewEncoder(writer)

	if r.URL.Query().Has("cachedSearch") {
		if h.logStore == nil {
			http.Error(w, "log cache is not enabled", http.StatusNotFound)
			return
		}
		limit := 200
		if r.URL.Query().Has("limit") {
			limit, err = strconv.Atoi(r.URL.Query().Get("limit"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if limit < 1 || limit > 500 {
				http.Error(w, "limit must be between 1 and 500", http.StatusBadRequest)
				return
			}
		}
		before := to
		if before.IsZero() {
			before = time.Now()
		}
		match := func(event *container.LogEvent) bool {
			return matchesStdType(event, stdTypes) && matchesFilter(event, regex, levels, inverse)
		}
		var events []*container.LogEvent
		var hasMore bool
		if containerService != nil {
			events, hasMore, err = h.logStore.SearchBeforeForContainer(r.Context(), containerService.Container, before, limit, match)
		} else {
			events, hasMore, err = h.logStore.SearchBefore(r.Context(), hostKey(r), id, before, limit, match)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if hasMore {
			w.Header().Set("X-Has-More", "true")
		}
		for i := len(events) - 1; i >= 0; i-- {
			support_web.EscapeHTMLValues(events[i])
			if err := encoder.Encode(events[i]); err != nil {
				log.Error().Err(err).Msg("error encoding cached search event")
				return
			}
		}
		return
	}

	// Serve from the indexed local log store. It is fast for historical ranges.
	if usingStore {
		queryFrom := from
		if everything {
			queryFrom = time.Now().AddDate(0, 0, -4)
		}
		var events <-chan *container.LogEvent
		if containerService != nil {
			events, err = h.logStore.LogsBetweenDatesForContainer(r.Context(), containerService.Container, queryFrom, to)
		} else {
			events, err = h.logStore.LogsBetweenDates(r.Context(), hostKey(r), id, queryFrom, to)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeStored := func(event *container.LogEvent) {
			if everything {
				if _, ok := event.Message.(string); onlyComplex && ok {
					return
				}
				if regex != nil && inverse == support_web.Search(regex, event) {
					return
				}
				if len(levels) > 0 {
					if _, ok := levels[event.Level]; !ok {
						return
					}
				}
				if plainText {
					fmt.Fprintf(writer, "%s\n", event.RawMessage)
				} else if err := encoder.Encode(event); err != nil {
					log.Error().Err(err).Msg("error encoding stored log event")
				}
				return
			}
			if !matchesFilter(event, regex, levels, inverse) {
				return
			}
			support_web.EscapeHTMLValues(event)
			if err := encoder.Encode(event); err != nil {
				log.Error().Err(err).Msg("error encoding stored log event")
			}
		}
		// Peek the first event. If the store has data for this range, serve it.
		// If it is empty (a gap in the cache), return immediately and fill the
		// gap from Docker/agent in the background. The request path must stay
		// cache-only so historical scrolling never waits on Docker log scans.
		if first, ok := <-events; ok {
			if !everything && !plainText && containerService != nil && !from.IsZero() && !to.IsZero() && from.Before(to) {
				firstTime := time.UnixMilli(first.Timestamp)
				if from.Before(firstTime) {
					h.scheduleCacheGapFill(containerService, from, firstTime, stdTypes)
					if err := encoder.Encode(newCacheGapEvent(containerService.Container.ID, from, firstTime)); err != nil {
						log.Error().Err(err).Msg("error encoding leading log cache gap event")
						return
					}
				}
			}
			writeStored(first)
			for event := range events {
				writeStored(event)
			}
			return
		}
		h.scheduleCacheGapFill(containerService, from, to, stdTypes)
		if !plainText && containerService != nil && !from.IsZero() && !to.IsZero() && from.Before(to) {
			if err := encoder.Encode(newCacheGapEvent(containerService.Container.ID, from, to)); err != nil {
				log.Error().Err(err).Msg("error encoding log cache gap event")
			}
		}
		return
	}

	startIdFound := startId == 0
	if h.logStore != nil && containerService != nil {
		if err := h.logStore.RecordContainer(containerService.Container); err != nil {
			log.Debug().Err(err).Str("container", id).Msg("failed to record cached container metadata")
		}
	}
	// Reading old logs straight from the agent/Docker scans the json-file
	// sequentially and can be very slow for ranges far in the past. Time-bound it
	// so a slow range fails gracefully (returns what was gathered) instead of
	// hanging the request indefinitely.
	fetchCtx, cancelFetch := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancelFetch()
	for {
		if minimum > 0 && buffer.Len() >= minimum {
			break
		}
		if fetchCtx.Err() != nil {
			log.Warn().Str("container", id).Time("from", from).Time("to", to).Msg("timed out fetching historical logs from agent")
			break
		}

		buffer.Clear()

		events, err := containerService.LogsBetweenDates(fetchCtx, from, to, stdTypes)
		if err != nil {
			// A deadline/cancel just means the slow fetch ran out of time; return
			// whatever was already gathered rather than failing the whole request.
			if fetchCtx.Err() != nil {
				log.Warn().Str("container", id).Msg("timed out fetching historical logs from agent")
				break
			}
			log.Error().Err(err).Msg("error fetching logs")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for event := range events {
			if h.logStore != nil && containerService != nil {
				if err := h.logStore.AppendForContainer(containerService.Container, event); err != nil {
					log.Debug().Err(err).Str("container", id).Msg("failed to append log cache")
				}
			}
			if everything {
				if _, ok := event.Message.(string); onlyComplex && ok {
					continue
				}
				if regex != nil && inverse == support_web.Search(regex, event) {
					continue
				}
				if len(levels) > 0 {
					if _, ok := levels[event.Level]; !ok {
						continue
					}
				}
				if plainText {
					// Strip ANSI and control bytes; a NUL byte truncates clipboard text on Windows
					fmt.Fprintf(writer, "%s\n", container.SanitizeForPlainText(event.RawMessage))
				} else if err := encoder.Encode(event); err != nil {
					log.Error().Err(err).Msg("error encoding log event")
				}
				continue
			}

			if !matchesFilter(event, regex, levels, inverse) {
				continue
			}

			if !startIdFound {
				if event.Id == startId {
					log.Debug().Uint32("startId", startId).Msg("found start id, will include subsequent events")
					startIdFound = true
				}
				continue
			}

			if lastSeenId != 0 && event.Id == lastSeenId {
				log.Debug().Uint32("lastSeenId", lastSeenId).Msg("found last seen id")
				break
			}

			if buffer.Len() >= maxStart {
				break
			}

			support_web.EscapeHTMLValues(event)
			buffer.Push(event)
		}

		if everything || from.Before(containerService.Container.Created) || minimum == 0 {
			break
		}

		from = from.Add(-delta)
		delta = delta * 2
	}

	log.Debug().Int("buffer_size", buffer.Len()).Msg("sending logs to client")

	for _, event := range buffer.Data() {
		if err := encoder.Encode(event); err != nil {
			log.Error().Err(err).Msg("error encoding log event")
			return
		}
	}
}

func (h *handler) streamContainerLogs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	h.streamLogsForContainers(w, r, func(container *container.Container) bool {
		return container.ID == id && container.Host == hostKey(r)
	})
}

func (h *handler) streamLogsMerged(w http.ResponseWriter, r *http.Request) {
	ids := make(map[string]bool)
	for _, id := range strings.Split(chi.URLParam(r, "ids"), ",") {
		ids[id] = true
	}

	h.streamLogsForContainers(w, r, func(container *container.Container) bool {
		return ids[container.ID] && container.Host == hostKey(r)
	})
}

func (h *handler) streamLogsWithLabels(w http.ResponseWriter, r *http.Request) {
	// Parse label filters from URL path
	// Expected format: /labels/key1:value1,key2:value2/logs/stream
	labelsParam := chi.URLParam(r, "labels")
	labelFilters := make(map[string]string)

	if labelsParam != "" {
		for _, pair := range strings.Split(labelsParam, ",") {
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) == 2 {
				labelFilters[parts[0]] = parts[1]
			}
		}
	}

	h.streamLogsForContainers(w, r, func(container *container.Container) bool {
		if container.State != "running" {
			return false
		}

		// Check if all label filters match
		for key, value := range labelFilters {
			if container.Labels[key] != value {
				return false
			}
		}

		return len(labelFilters) > 0
	})
}

func (h *handler) streamGroupedLogs(w http.ResponseWriter, r *http.Request) {
	group := chi.URLParam(r, "group")

	h.streamLogsForContainers(w, r, func(container *container.Container) bool {
		return container.State == "running" && container.Group == group
	})
}

func (h *handler) streamHostGroupLogs(w http.ResponseWriter, r *http.Request) {
	group, err := url.PathUnescape(chi.URLParam(r, "group"))
	if err != nil || group == "" {
		http.Error(w, "invalid group", http.StatusBadRequest)
		return
	}

	hostIDs := make(map[string]struct{})
	for _, host := range h.hostService.Hosts() {
		if host.Group == group {
			hostIDs[host.ID] = struct{}{}
		}
	}

	h.streamLogsForContainers(w, r, func(c *container.Container) bool {
		_, ok := hostIDs[c.Host]
		return c.State == "running" && ok
	})
}

func (h *handler) streamHostLogs(w http.ResponseWriter, r *http.Request) {
	host := hostKey(r)
	h.streamLogsForContainers(w, r, func(container *container.Container) bool {
		return container.State == "running" && container.Host == host
	})
}

func (h *handler) streamLogsForContainers(w http.ResponseWriter, r *http.Request, containerFilter container_support.ContainerFilter) {
	stdTypes := parseStdTypes(r)
	if stdTypes == 0 {
		http.Error(w, "stdout or stderr is required", http.StatusBadRequest)
		return
	}

	sseWriter, err := support_web.NewSSEWriter(r.Context(), w, r)
	if err != nil {
		log.Error().Err(err).Msg("error creating sse writer")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer sseWriter.Close()

	userLabels := h.resolveLabels(r)

	existingContainers, errs := h.hostService.ListAllContainersFiltered(userLabels, containerFilter)
	if len(errs) > 0 {
		log.Warn().Err(errs[0]).Msg("error while listing containers")
	}

	absoluteTime := time.Time{}
	liveLogs := make(chan *container.LogEvent)
	events := make(chan *container.ContainerEvent, 1)
	backfill := make(chan []*container.LogEvent)

	levels := make(map[string]struct{})
	for _, level := range r.URL.Query()["levels"] {
		levels[level] = struct{}{}
	}

	allLogs := true
	for level := range container.SupportedLogLevels {
		if _, ok := levels[level]; !ok {
			allLogs = false
		}
	}

	var regex *regexp.Regexp
	if r.URL.Query().Has("filter") {
		var err error
		regex, err = support_web.ParseRegex(r.URL.Query().Get("filter"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	inverse := r.URL.Query().Get("inverse") == "true"

	if !allLogs || regex != nil || inverse {
		absoluteTime = time.Now()

		go func() {
			minimum := 50
			delta := -10 * time.Second
			to := absoluteTime
			for minimum > 0 {
				events := make([]*container.LogEvent, 0)
				stillRunning := false
				for _, container := range existingContainers {
					containerService, err := h.hostService.FindContainer(container.Host, container.ID, userLabels)

					if err != nil {
						log.Error().Err(err).Msg("error while finding container")
						return
					}

					if to.Before(containerService.Container.Created) {
						continue
					}

					logs, err := containerService.LogsBetweenDates(r.Context(), to.Add(delta), to, stdTypes)
					if err != nil {
						log.Error().Err(err).Msg("error while fetching logs")
						return
					}

					for log := range logs {
						if !matchesFilter(log, regex, levels, inverse) {
							continue
						}
						events = append(events, log)
					}

					stillRunning = true
				}

				if !stillRunning {
					return
				}

				to = to.Add(delta)
				delta *= 2
				minimum -= len(events)
				sort.Slice(events, func(i, j int) bool {
					return events[i].Timestamp < events[j].Timestamp
				})
				if len(events) > 0 {
					backfill <- events
				}
			}
		}()
	}

	streamLogs := func(c container.Container) {
		containerService, err := h.hostService.FindContainer(c.Host, c.ID, userLabels)
		if err != nil {
			log.Error().Err(err).Msg("error while finding container")
			return
		}
		c = containerService.Container
		if h.logStore != nil {
			if err := h.logStore.RecordContainer(c); err != nil {
				log.Debug().Err(err).Str("container", c.ID).Msg("failed to record cached container metadata")
			}
		}
		start := utils.Max(absoluteTime, c.StartedAt)
		streamStart := start

		if h.logStore != nil && h.logStore.HasLogs(c.Host, c.ID) {
			if cachedLogs, err := h.logStore.LogsBetweenDates(r.Context(), c.Host, c.ID, start, time.Now()); err == nil {
				cached := make([]*container.LogEvent, 0, 128)
				for logEvent := range cachedLogs {
					if !matchesFilter(logEvent, regex, levels, inverse) {
						continue
					}
					cached = append(cached, logEvent)
				}
				if len(cached) > 0 {
					select {
					case backfill <- cached:
					case <-r.Context().Done():
						return
					}
				}
			}
			if lastCached, ok := h.logStore.LastTimestampForContainer(c); ok && !lastCached.Before(streamStart) {
				streamStart = lastCached.Add(time.Millisecond)
			}
		}

		localLogs := make(chan *container.LogEvent, 100)
		go func() {
			defer close(localLogs)
			err = containerService.StreamLogs(r.Context(), streamStart, stdTypes, localLogs)
			if err != nil {
				if errors.Is(err, io.EOF) {
					log.Debug().Str("container", c.ID).Msg("streaming ended")
				} else if !errors.Is(err, context.Canceled) {
					log.Error().Err(err).Str("container", c.ID).Msg("unknown error while streaming logs")
				}
			}
		}()

		for logEvent := range localLogs {
			if h.logStore != nil {
				if err := h.logStore.AppendForContainer(c, logEvent); err != nil {
					log.Debug().Err(err).Str("container", c.ID).Msg("failed to append log cache")
				}
			}
			select {
			case liveLogs <- logEvent:
			case <-r.Context().Done():
				return
			}
		}
	}

	for _, container := range existingContainers {
		go streamLogs(container)
	}

	newContainers := make(chan container.Container)
	h.hostService.SubscribeContainersStarted(r.Context(), newContainers, containerFilter)

	ticker := time.NewTicker(5 * time.Second)
	sseWriter.Ping()
loop:
	for {
		select {
		case logEvent := <-liveLogs:
			if !matchesFilter(logEvent, regex, levels, inverse) {
				continue
			}

			support_web.EscapeHTMLValues(logEvent)
			sseWriter.Message(logEvent)
		case c := <-newContainers:
			if _, err := h.hostService.FindContainer(c.Host, c.ID, userLabels); err == nil {
				events <- &container.ContainerEvent{ActorID: c.ID, Name: "container-started", Host: c.Host, Time: time.Now()}
				go streamLogs(c)
			}

		case event := <-events:
			log.Debug().Str("event", event.Name).Str("container", event.ActorID).Msg("received event")
			if err := sseWriter.Event("container-event", event); err != nil {
				log.Error().Err(err).Msg("error encoding container event")
			}

		case backfillEvents := <-backfill:
			for _, event := range backfillEvents {
				support_web.EscapeHTMLValues(event)
			}
			if err := sseWriter.Event("logs-backfill", backfillEvents); err != nil {
				log.Error().Err(err).Msg("error encoding container event")
			}

		case <-ticker.C:
			sseWriter.Ping()

		case <-r.Context().Done():
			break loop
		}
	}

	if e := log.Debug(); e.Enabled() {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		e.Str("allocated", humanize.Bytes(m.Alloc)).
			Str("totalAllocated", humanize.Bytes(m.TotalAlloc)).
			Str("system", humanize.Bytes(m.Sys)).
			Int("routines", runtime.NumGoroutine()).
			Msg("runtime mem stats")
	}
}
