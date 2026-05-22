package notification

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amir20/dozzle/internal/container"
	"github.com/amir20/dozzle/internal/notification/dispatcher"
	"github.com/amir20/dozzle/internal/notification/queue"
	"github.com/amir20/dozzle/internal/utils"
	"github.com/amir20/dozzle/types"
	"github.com/expr-lang/expr"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/semaphore"
)

// Manager manages notification subscriptions and dispatches notifications
type Manager struct {
	subscriptions       *xsync.Map[int, *Subscription]
	dispatchers         *xsync.Map[int, dispatcher.Dispatcher]
	cloudDispatcher     atomic.Pointer[dispatcher.Dispatcher]
	subscriptionCounter atomic.Int32
	dispatcherCounter   atomic.Int32
	listener            *ContainerLogListener
	statsListener       *ContainerStatsListener
	eventListener       *ContainerEventListener
	ctx                 context.Context
	cancel              context.CancelFunc
	sendSem             *semaphore.Weighted
	queue               *queue.Queue
	quietHours          QuietHoursConfig
	logStoreCh          chan<- *container.LogEvent
	quietHoursMu        sync.RWMutex
}

// NewManager creates a new notification manager.
// dbPath is the path to the SQLite database for the notification queue;
// pass an empty string to disable persistence (in-memory only).
func NewManager(listener *ContainerLogListener, statsListener *ContainerStatsListener, eventListener *ContainerEventListener, dbPath string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		subscriptions: xsync.NewMap[int, *Subscription](),
		dispatchers:   xsync.NewMap[int, dispatcher.Dispatcher](),
		listener:      listener,
		statsListener: statsListener,
		eventListener: eventListener,
		ctx:           ctx,
		cancel:        cancel,
		sendSem:       semaphore.NewWeighted(5),
	}

	if dbPath != "" {
		q, err := queue.NewQueue(dbPath)
		if err != nil {
			log.Warn().Err(err).Str("path", dbPath).Msg("Failed to open notification queue; persistence disabled")
		} else {
			m.queue = q
			go m.drainQueue()
		}
	}

	go m.processLogEvents()
	go m.processStatEvents()
	go m.processDockerEvents()

	return m
}

// Start initializes the manager and starts the log listener
func (m *Manager) Start() error {
	return m.listener.Start(m)
}

// SetLogStore registers a channel to receive a copy of every log event.
func (m *Manager) SetLogStore(ch chan<- *container.LogEvent) {
	m.logStoreCh = ch
}

// ShouldListenToContainer implements ContainerMatcher interface
// Only matches log-based subscriptions (metric-only subscriptions don't need log streaming)
func (m *Manager) ShouldListenToContainer(c container.Container, host container.Host) bool {
	notificationContainer := FromContainerModel(c, host)

	shouldListen := false
	m.subscriptions.Range(func(_ int, sub *Subscription) bool {
		if sub.Enabled && sub.LogExpression != "" && sub.MatchesContainer(notificationContainer) {
			shouldListen = true
			return false
		}
		return true
	})
	return shouldListen
}

// SetQuietHours updates the global quiet hours configuration.
func (m *Manager) SetQuietHours(cfg QuietHoursConfig) {
	m.quietHoursMu.Lock()
	m.quietHours = cfg
	m.quietHoursMu.Unlock()
}

// GetQuietHours returns the current quiet hours configuration.
func (m *Manager) GetQuietHours() QuietHoursConfig {
	m.quietHoursMu.RLock()
	defer m.quietHoursMu.RUnlock()
	return m.quietHours
}

// isQuietHours returns true if the current time (in the configured timezone) falls within the quiet window.
func (m *Manager) isQuietHours() bool {
	m.quietHoursMu.RLock()
	qh := m.quietHours
	m.quietHoursMu.RUnlock()

	if !qh.Enabled || qh.Start == "" || qh.End == "" {
		return false
	}

	loc := m.resolveLocation(qh.Timezone)
	now := time.Now().In(loc)
	startH, startM := parseHHMM(qh.Start)
	endH, endM := parseHHMM(qh.End)

	start := time.Date(now.Year(), now.Month(), now.Day(), startH, startM, 0, 0, loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), endH, endM, 0, 0, loc)

	if start.Before(end) {
		return !now.Before(start) && now.Before(end)
	}
	// overnight window (e.g., 22:00 – 08:00)
	return !now.Before(start) || now.Before(end)
}

// isAlertQuietHours returns true if the current time falls within the per-alert quiet window.
// Returns (inQuiet bool, effective bool) — effective is false when no per-alert override is set.
func (m *Manager) isAlertQuietHours(sub *Subscription) (inQuiet bool, effective bool) {
	if !sub.AlertQuietEnabled || sub.AlertQuietStart == "" || sub.AlertQuietEnd == "" {
		return false, false
	}
	loc := m.resolveLocation(sub.AlertQuietTimezone)
	now := time.Now().In(loc)
	startH, startM := parseHHMM(sub.AlertQuietStart)
	endH, endM := parseHHMM(sub.AlertQuietEnd)
	start := time.Date(now.Year(), now.Month(), now.Day(), startH, startM, 0, 0, loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), endH, endM, 0, 0, loc)
	var in bool
	if start.Before(end) {
		in = !now.Before(start) && now.Before(end)
	} else {
		in = !now.Before(start) || now.Before(end)
	}
	return in, true
}

// nextAlertQuietEnd returns when the per-alert quiet window ends.
func (m *Manager) nextAlertQuietEnd(sub *Subscription) time.Time {
	loc := m.resolveLocation(sub.AlertQuietTimezone)
	endH, endM := parseHHMM(sub.AlertQuietEnd)
	now := time.Now().In(loc)
	candidate := time.Date(now.Year(), now.Month(), now.Day(), endH, endM, 0, 0, loc)
	if candidate.Before(now) {
		candidate = candidate.Add(24 * time.Hour)
	}
	return candidate
}

// resolveLocation returns the time.Location for the given timezone string.
// Falls back to time.Local if the string is empty or invalid.
func (m *Manager) resolveLocation(tz string) *time.Location {
	if tz == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Warn().Str("timezone", tz).Msg("Invalid quiet hours timezone, using local time")
		return time.Local
	}
	return loc
}

// nextQuietEnd returns the next time quiet hours end (in the configured timezone).
func (m *Manager) nextQuietEnd() time.Time {
	m.quietHoursMu.RLock()
	qh := m.quietHours
	m.quietHoursMu.RUnlock()

	loc := m.resolveLocation(qh.Timezone)
	endH, endM := parseHHMM(qh.End)
	now := time.Now().In(loc)
	candidate := time.Date(now.Year(), now.Month(), now.Day(), endH, endM, 0, 0, loc)
	if candidate.Before(now) {
		candidate = candidate.Add(24 * time.Hour)
	}
	return candidate
}

// parseHHMM parses a "HH:MM" string, returns (0,0) on parse failure.
func parseHHMM(s string) (int, int) {
	var h, min int
	fmt.Sscanf(s, "%d:%d", &h, &min)
	return h, min
}

// drainQueue processes the SQLite queue every 60 seconds and delivers ready notifications.
func (m *Manager) drainQueue() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.flushQueue()
		}
	}
}

func (m *Manager) flushQueue() {
	if m.queue == nil {
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()

	items, err := m.queue.DrainReady(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to drain notification queue")
		return
	}

	for _, item := range items {
		d, ok := m.getDispatcher(item.DispatcherID)
		if !ok {
			log.Warn().Int("dispatcher_id", item.DispatcherID).Msg("Dispatcher not found for queued notification; dropping")
			_ = m.queue.MarkFailed(item.ID, 3)
			continue
		}
		id := item.ID
		dispID := item.DispatcherID
		notification := item.Notification
		attempts := item.Attempts
		if !m.isSubscriptionActive(notification.Subscription.ID) {
			log.Debug().Int64("queue_id", id).Int("subscription", notification.Subscription.ID).Msg("Dropping queued notification for inactive subscription")
			if qErr := m.queue.MarkSent(id); qErr != nil {
				log.Warn().Err(qErr).Msg("Failed to mark inactive queued notification sent")
			}
			continue
		}
		go func() {
			sendCtx, sendCancel := context.WithTimeout(m.ctx, 30*time.Second)
			defer sendCancel()
			if err := d.Send(sendCtx, notification); err != nil {
				log.Warn().Err(err).Int64("queue_id", id).Msg("Queued notification failed")
				if qErr := m.queue.MarkFailed(id, attempts); qErr != nil {
					log.Warn().Err(qErr).Msg("Failed to update queue status")
				}
			} else {
				log.Debug().Int64("queue_id", id).Int("dispatcher_id", dispID).Msg("Delivered queued notification")
				if qErr := m.queue.MarkSent(id); qErr != nil {
					log.Warn().Err(qErr).Msg("Failed to mark notification sent")
				}
			}
		}()
	}

	m.queue.Cleanup(7 * 24 * time.Hour)
	m.queue.CleanupAlertState(25 * time.Hour)
	m.queue.CleanupCooldowns()
}

// AddSubscription adds a new subscription with compiled expressions
func (m *Manager) AddSubscription(sub *Subscription) error {
	// Auto-increment ID using atomic counter
	sub.ID = int(m.subscriptionCounter.Add(1))
	sub.Enabled = true
	sub.MetricCooldowns = xsync.NewMap[string, time.Time]()
	sub.MetricSampleBuffers = xsync.NewMap[string, *utils.RingBuffer[bool]]()
	sub.EventCooldowns = xsync.NewMap[string, time.Time]()
	sub.LogCooldowns = xsync.NewMap[string, time.Time]()
	sub.BurstTrackers = xsync.NewMap[string, []time.Time]()
	sub.WatchdogTimers = xsync.NewMap[string, *time.Timer]()
	sub.WatchdogCooldowns = xsync.NewMap[string, time.Time]()

	if err := sub.CompileExpressions(); err != nil {
		return err
	}

	m.subscriptions.Store(sub.ID, sub)
	log.Debug().Str("name", sub.Name).Int("id", sub.ID).Msg("Added subscription")

	m.updateListeners()

	return nil
}

// RemoveSubscription removes a subscription by ID
func (m *Manager) RemoveSubscription(id int) {
	if sub, ok := m.subscriptions.LoadAndDelete(id); ok {
		sub.StopRuntime()
		log.Debug().Int("id", id).Str("name", sub.Name).Msg("Removed subscription")

		m.updateListeners()
	}
}

// ReplaceSubscription replaces a subscription with new data
func (m *Manager) ReplaceSubscription(sub *Subscription) error {
	sub.MetricCooldowns = xsync.NewMap[string, time.Time]()
	sub.MetricSampleBuffers = xsync.NewMap[string, *utils.RingBuffer[bool]]()
	sub.EventCooldowns = xsync.NewMap[string, time.Time]()
	sub.LogCooldowns = xsync.NewMap[string, time.Time]()
	sub.BurstTrackers = xsync.NewMap[string, []time.Time]()

	if err := sub.CompileExpressions(); err != nil {
		return err
	}

	// Preserve enabled state from existing subscription if it exists
	if existing, ok := m.subscriptions.Load(sub.ID); ok {
		sub.Enabled = existing.Enabled
		existing.StopRuntime()
	} else {
		sub.Enabled = true
	}

	m.subscriptions.Store(sub.ID, sub)
	log.Debug().Str("name", sub.Name).Int("id", sub.ID).Msg("Replaced subscription")

	m.updateListeners()

	return nil
}

// UpdateSubscription updates a subscription with the provided fields
func (m *Manager) UpdateSubscription(id int, updates map[string]any) error {
	var updateErr error
	_, ok := m.subscriptions.Compute(id, func(sub *Subscription, loaded bool) (*Subscription, xsync.ComputeOp) {
		if !loaded {
			updateErr = fmt.Errorf("subscription not found")
			return nil, xsync.CancelOp
		}

		// Clone the subscription
		updated := &Subscription{
			ID:                  sub.ID,
			Name:                sub.Name,
			Enabled:             sub.Enabled,
			DispatcherID:        sub.DispatcherID,
			ContainerExpression: sub.ContainerExpression,
			ContainerProgram:    sub.ContainerProgram,
			LogExpression:       sub.LogExpression,
			LogProgram:          sub.LogProgram,
			MetricExpression:    sub.MetricExpression,
			MetricProgram:       sub.MetricProgram,
			EventExpression:     sub.EventExpression,
			EventProgram:        sub.EventProgram,
			EventCooldowns:      sub.EventCooldowns,
			Cooldown:            sub.Cooldown,
			SampleWindow:        sub.SampleWindow,
			MetricCooldowns:     sub.MetricCooldowns,
			MetricSampleBuffers: sub.MetricSampleBuffers,
			TriggeredContainerIDs: sub.TriggeredContainerIDs,
		}

		// Preserve runtime stats (atomics can't be copied in struct literal)
		updated.TriggerCount.Store(sub.TriggerCount.Load())
		updated.LastTriggeredAt.Store(sub.LastTriggeredAt.Load())
		sub.StopRuntime()

		// Apply updates to the clone
		for key, value := range updates {
			switch key {
			case "name":
				if name, ok := value.(string); ok {
					updated.Name = name
				}
			case "enabled":
				if enabled, ok := value.(bool); ok {
					updated.Enabled = enabled
				}
			case "dispatcherId":
				if dispatcherID, ok := value.(int); ok {
					updated.DispatcherID = dispatcherID
				}
			case "containerExpression":
				if exprStr, ok := value.(string); ok {
					program, err := expr.Compile(exprStr, expr.Env(types.NotificationContainer{}))
					if err != nil {
						updateErr = fmt.Errorf("failed to compile container expression: %w", err)
						return nil, xsync.CancelOp
					}
					updated.ContainerExpression = exprStr
					updated.ContainerProgram = program
				}
			case "logExpression":
				if exprStr, ok := value.(string); ok {
					if exprStr != "" {
						program, err := expr.Compile(exprStr, expr.Env(types.NotificationLog{}))
						if err != nil {
							updateErr = fmt.Errorf("failed to compile log expression: %w", err)
							return nil, xsync.CancelOp
						}
						updated.LogExpression = exprStr
						updated.LogProgram = program
					} else {
						updated.LogExpression = ""
						updated.LogProgram = nil
					}
				}
			case "metricExpression":
				if exprStr, ok := value.(string); ok {
					if exprStr != "" {
						program, err := expr.Compile(exprStr, expr.Env(types.NotificationStat{}))
						if err != nil {
							updateErr = fmt.Errorf("failed to compile metric expression: %w", err)
							return nil, xsync.CancelOp
						}
						updated.MetricExpression = exprStr
						updated.MetricProgram = program
					} else {
						updated.MetricExpression = ""
						updated.MetricProgram = nil
					}
				}
			case "eventExpression":
				if exprStr, ok := value.(string); ok {
					if exprStr != "" {
						program, err := expr.Compile(exprStr, expr.Env(types.NotificationEvent{}))
						if err != nil {
							updateErr = fmt.Errorf("failed to compile event expression: %w", err)
							return nil, xsync.CancelOp
						}
						updated.EventExpression = exprStr
						updated.EventProgram = program
					} else {
						updated.EventExpression = ""
						updated.EventProgram = nil
					}
				}
			case "cooldown":
				if cd, ok := value.(int); ok {
					updated.Cooldown = cd
				}
			case "sampleWindow":
				if sw, ok := value.(int); ok {
					updated.SampleWindow = sw
					updated.MetricSampleBuffers = xsync.NewMap[string, *utils.RingBuffer[bool]]()
				}
			}
		}

		return updated, xsync.UpdateOp
	})

	if updateErr != nil {
		return updateErr
	}

	if !ok {
		return fmt.Errorf("subscription not found")
	}

	log.Debug().Int("id", id).Interface("updates", updates).Msg("Updated subscription")

	m.updateListeners()

	return nil
}

// updateListeners updates log and stats listeners based on current subscriptions
func (m *Manager) updateListeners() {
	m.listener.UpdateStreams()

	hasMetric := false
	hasEvent := false
	m.subscriptions.Range(func(_ int, sub *Subscription) bool {
		if sub.Enabled && sub.IsMetricAlert() {
			hasMetric = true
		}
		if sub.Enabled && sub.IsEventAlert() {
			hasEvent = true
		}
		if hasMetric && hasEvent {
			return false
		}
		return true
	})

	if hasMetric {
		m.statsListener.Start()
	} else {
		m.statsListener.Stop()
	}

	if hasEvent {
		m.eventListener.Start()
	} else {
		m.eventListener.Stop()
	}
}

// AddDispatcher adds a dispatcher and returns its auto-generated ID
func (m *Manager) AddDispatcher(d dispatcher.Dispatcher) int {
	id := int(m.dispatcherCounter.Add(1))
	m.dispatchers.Store(id, d)
	log.Debug().Int("id", id).Msg("Added dispatcher")
	return id
}

// UpdateDispatcher updates a dispatcher by ID
func (m *Manager) UpdateDispatcher(id int, d dispatcher.Dispatcher) {
	m.dispatchers.Store(id, d)
	log.Debug().Int("id", id).Msg("Updated dispatcher")
}

// RemoveDispatcher removes a dispatcher by ID
func (m *Manager) RemoveDispatcher(id int) {
	if _, ok := m.dispatchers.LoadAndDelete(id); ok {
		log.Debug().Int("id", id).Msg("Removed dispatcher")
	}
}

// SetCloudDispatcher sets the dedicated cloud dispatcher used for subscriptions with DispatcherID == 0.
func (m *Manager) SetCloudDispatcher(d dispatcher.Dispatcher) {
	m.cloudDispatcher.Store(&d)
	log.Debug().Msg("Set cloud dispatcher")
}

// ClearCloudDispatcher removes the cloud dispatcher.
func (m *Manager) ClearCloudDispatcher() {
	m.cloudDispatcher.Store(nil)
	log.Debug().Msg("Cleared cloud dispatcher")
}


// getDispatcher resolves a dispatcher by subscription's DispatcherID.
// DispatcherID == 0 means the cloud dispatcher; otherwise lookup in the dispatchers map.
func (m *Manager) getDispatcher(id int) (dispatcher.Dispatcher, bool) {
	if id == 0 {
		if p := m.cloudDispatcher.Load(); p != nil {
			return *p, true
		}
		return nil, false
	}
	return m.dispatchers.Load(id)
}

func (m *Manager) isSubscriptionActive(id int) bool {
	if id == 0 {
		return true
	}
	sub, ok := m.subscriptions.Load(id)
	return ok && sub.Enabled
}

// Subscriptions returns all subscriptions sorted by ID
func (m *Manager) Subscriptions() []*Subscription {
	result := make([]*Subscription, 0)
	m.subscriptions.Range(func(_ int, sub *Subscription) bool {
		result = append(result, sub)
		return true
	})
	slices.SortFunc(result, func(a, b *Subscription) int {
		return a.ID - b.ID
	})
	return result
}

// GetNotificationStats returns runtime stats for all subscriptions
func (m *Manager) GetNotificationStats() []types.SubscriptionStats {
	var stats []types.SubscriptionStats
	m.subscriptions.Range(func(_ int, sub *Subscription) bool {
		var containerIDs []string
		if sub.TriggeredContainerIDs != nil {
			sub.TriggeredContainerIDs.Range(func(id string, _ struct{}) bool {
				containerIDs = append(containerIDs, id)
				return true
			})
		}

		var lastTriggered *time.Time
		if t := sub.LastTriggeredAt.Load(); t != nil && !t.IsZero() {
			lastTriggered = t
		}

		stats = append(stats, types.SubscriptionStats{
			SubscriptionID:        sub.ID,
			TriggerCount:          sub.TriggerCount.Load(),
			LastTriggeredAt:       lastTriggered,
			TriggeredContainerIDs: containerIDs,
		})
		return true
	})
	return stats
}

// Dispatchers returns all dispatchers as DispatcherConfig sorted by ID.
// Includes the cloud dispatcher (ID 0) when configured.
func (m *Manager) Dispatchers() []DispatcherConfig {
	result := make([]DispatcherConfig, 0)

	// Include cloud dispatcher if configured
	if p := m.cloudDispatcher.Load(); p != nil {
		if cd, ok := (*p).(*dispatcher.CloudDispatcher); ok {
			result = append(result, DispatcherConfig{
				ID:     0,
				Name:   cd.Name,
				Type:   "cloud",
				Prefix: cd.Prefix,
			})
		}
	}

	m.dispatchers.Range(func(id int, d dispatcher.Dispatcher) bool {
		switch v := d.(type) {
		case *dispatcher.WebhookDispatcher:
			result = append(result, DispatcherConfig{
				ID:       id,
				Name:     v.Name,
				Type:     "webhook",
				URL:      v.URL,
				Template: v.TemplateText,
				Headers:  v.Headers,
			})
		case *dispatcher.NtfyDispatcher:
			result = append(result, DispatcherConfig{
				ID:       id,
				Name:     v.Name,
				Type:     "ntfy",
				URL:      v.ServerURL,
				Topic:    v.DefaultTopic,
				Priority: v.DefaultPriority,
				Token:    v.Token,
			})
		}
		return true
	})
	slices.SortFunc(result, func(a, b DispatcherConfig) int {
		return a.ID - b.ID
	})
	return result
}

// RestorePersistentState loads cooldowns from SQLite after subscriptions have been loaded.
func (m *Manager) RestorePersistentState() {
	if m.queue == nil {
		return
	}

	cooldowns, err := m.queue.LoadCooldowns()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load cooldowns from SQLite")
		return
	}

	restored := 0
	for key, expiresAt := range cooldowns {
		// key format: "<subID>:<containerID>:<type>"
		colonIdx := strings.Index(key, ":")
		if colonIdx < 0 {
			continue
		}
		subIDStr := key[:colonIdx]
		rest := key[colonIdx+1:]
		lastColon := strings.LastIndex(rest, ":")
		if lastColon < 0 {
			continue
		}
		containerID := rest[:lastColon]
		cooldownType := rest[lastColon+1:]

		subID, err := strconv.Atoi(subIDStr)
		if err != nil {
			continue
		}

		sub, ok := m.subscriptions.Load(subID)
		if !ok {
			continue
		}

		// Convert expiresAt back to the lastTriggered time the maps expect.
		// The maps store time.Now() at trigger time; IsXCooldownActive checks
		// time.Now().Before(lastTriggered.Add(cooldownDuration)).
		// So we restore lastTriggered = expiresAt - cooldownDuration.
		cooldownDur := time.Duration(sub.Cooldown) * time.Second
		lastTriggered := expiresAt.Add(-cooldownDur)

		switch cooldownType {
		case "log":
			if sub.LogCooldowns != nil {
				sub.LogCooldowns.Store(containerID, lastTriggered)
			}
		case "metric":
			if sub.MetricCooldowns != nil {
				sub.MetricCooldowns.Store(containerID, lastTriggered)
			}
		case "event":
			if sub.EventCooldowns != nil {
				sub.EventCooldowns.Store(containerID, lastTriggered)
			}
		}
		restored++
	}

	if restored > 0 {
		log.Info().Int("cooldowns", restored).Msg("Restored cooldown state from SQLite")
	}
}
