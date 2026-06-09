package notification

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/amir20/dozzle/internal/notification/queue"
	"github.com/amir20/dozzle/types"
)

const (
	defaultQuietSummaryGroups = 10
	maxQuietSummaryExample    = 1000
	maxQuietSummaryLength     = 12000
)

type quietSummaryGroup struct {
	Fingerprint  string    `json:"fingerprint"`
	Count        int       `json:"count"`
	FirstSeen    time.Time `json:"firstSeen"`
	LastSeen     time.Time `json:"lastSeen"`
	FirstExample string    `json:"firstExample"`
	LastExample  string    `json:"lastExample"`
}

var (
	summaryTimestamp = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?\b`)
	summaryUUID      = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
	summaryIPv4      = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	summaryDuration  = regexp.MustCompile(`(?i)\b\d+(?:\.\d+)?\s*(?:ms|milliseconds?|s|secs?|seconds?|m|mins?|minutes?|h|hours?)\b`)
	summaryLongID    = regexp.MustCompile(`\b\d{5,}\b`)
	summaryJSONValue = regexp.MustCompile(`("[^"]+"\s*:\s*)(-?\d+(?:\.\d+)?)`)
)

func normalizeQuietSummaryText(text string) string {
	text = summaryTimestamp.ReplaceAllString(text, "<timestamp>")
	text = summaryUUID.ReplaceAllString(text, "<uuid>")
	text = summaryIPv4.ReplaceAllString(text, "<ip>")
	text = summaryDuration.ReplaceAllString(text, "<duration>")
	text = summaryJSONValue.ReplaceAllStringFunc(text, func(match string) string {
		parts := summaryJSONValue.FindStringSubmatch(match)
		key := strings.ToLower(parts[1])
		if strings.Contains(key, "status") {
			return match
		}
		return parts[1] + "<number>"
	})
	text = summaryLongID.ReplaceAllString(text, "<id>")
	return multiSpace.ReplaceAllString(strings.TrimSpace(text), " ")
}

func truncateQuietExample(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= maxQuietSummaryExample {
		return text
	}
	return text[:maxQuietSummaryExample-3] + "..."
}

func quietSummaryFingerprint(text string) string {
	sum := sha256.Sum256([]byte(normalizeQuietSummaryText(text)))
	return hex.EncodeToString(sum[:])
}

func (m *Manager) quietSummaryPeriod(sub *Subscription, now time.Time) (time.Time, time.Time, *time.Location) {
	start, end, timezone := m.GetQuietHours().Start, m.GetQuietHours().End, m.GetQuietHours().Timezone
	if sub.AlertQuietEnabled {
		start, end, timezone = sub.AlertQuietStart, sub.AlertQuietEnd, sub.AlertQuietTimezone
	}
	loc := m.resolveLocation(timezone)
	localNow := now.In(loc)
	startH, startM := parseHHMM(start)
	endH, endM := parseHHMM(end)
	periodEnd := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), endH, endM, 0, 0, loc)
	if !localNow.Before(periodEnd) {
		periodEnd = periodEnd.AddDate(0, 0, 1)
	}
	periodStart := time.Date(periodEnd.Year(), periodEnd.Month(), periodEnd.Day(), startH, startM, 0, 0, loc)
	if !periodStart.Before(periodEnd) {
		periodStart = periodStart.AddDate(0, 0, -1)
	}
	return periodStart, periodEnd, loc
}

func (m *Manager) aggregateQuietNotification(notification types.Notification, sub *Subscription) error {
	m.quietSummaryMu.Lock()
	defer m.quietSummaryMu.Unlock()

	now := notification.Timestamp
	if now.IsZero() {
		now = time.Now()
	}
	periodStart, periodEnd, loc := m.quietSummaryPeriod(sub, now)
	key := fmt.Sprintf("%d:%s:%s:%d", sub.ID, notification.Container.HostName, notification.Container.ID, periodStart.Unix())
	summary, err := m.queue.GetQuietSummary(key)
	if err == sql.ErrNoRows {
		summary = &queue.QuietSummary{
			Key: key, SubscriptionID: sub.ID, DispatcherID: sub.DispatcherID,
			PeriodStart: periodStart, PeriodEnd: periodEnd, Timezone: loc.String(),
			Notification: notification,
		}
	} else if err != nil {
		return err
	}
	var groups []quietSummaryGroup
	if summary.GroupsJSON != "" {
		_ = json.Unmarshal([]byte(summary.GroupsJSON), &groups)
	}
	example := truncateQuietExample(notification.Detail)
	fp := quietSummaryFingerprint(notification.Detail)
	found := false
	for i := range groups {
		if groups[i].Fingerprint != fp {
			continue
		}
		groups[i].Count++
		groups[i].LastSeen = now
		groups[i].LastExample = example
		found = true
		break
	}
	if !found {
		groups = append(groups, quietSummaryGroup{
			Fingerprint: fp, Count: 1, FirstSeen: now, LastSeen: now,
			FirstExample: example, LastExample: example,
		})
	}
	summary.TotalCount++
	summary.Notification = notification
	summary.Notification.NtfyTopic = sub.NtfyTopic
	summary.Notification.NtfyPriority = sub.NtfyPriority
	summary.Notification.NtfyTags = sub.NtfyTags
	summary.GroupsJSON = mustJSON(groups)
	return m.queue.UpsertQuietSummary(*summary)
}

func (m *Manager) shouldAggregateQuiet(sub *Subscription) bool {
	if m.queue == nil || sub.BypassQuietHours {
		return false
	}
	inQuiet, _ := m.effectiveQuietHours(sub)
	if !inQuiet {
		return false
	}
	periodStart, periodEnd, _ := m.quietSummaryPeriod(sub, time.Now())
	key := fmt.Sprintf("%d:%d", sub.ID, periodStart.Unix())
	grouping, err := m.queue.QuietPeriodGrouping(key, periodEnd, m.GetQuietHours().GroupingEnabled())
	if err != nil {
		return m.GetQuietHours().GroupingEnabled()
	}
	return grouping
}

func summaryNotification(sub *Subscription, notificationType types.NotificationType, detail string, container types.NotificationContainer) types.Notification {
	return types.Notification{
		ID:           fmt.Sprintf("%s-quiet-hit-%d", container.ID, time.Now().UnixNano()),
		Type:         notificationType,
		Detail:       detail,
		Container:    container,
		NtfyTopic:    sub.NtfyTopic,
		NtfyPriority: sub.NtfyPriority,
		NtfyTags:     sub.NtfyTags,
		Subscription: types.SubscriptionConfig{
			ID:             sub.ID,
			Name:           sub.Name,
			DispatcherID:   sub.DispatcherID,
			NtfyTopic:      sub.NtfyTopic,
			NtfyPriority:   sub.NtfyPriority,
			NtfyTags:       sub.NtfyTags,
			BypassQuietHours: sub.BypassQuietHours,
		},
		Timestamp: time.Now(),
	}
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func renderQuietSummary(summary queue.QuietSummary, maxGroups int) types.Notification {
	var groups []quietSummaryGroup
	_ = json.Unmarshal([]byte(summary.GroupsJSON), &groups)
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Count == groups[j].Count {
			return groups[i].FirstSeen.Before(groups[j].FirstSeen)
		}
		return groups[i].Count > groups[j].Count
	})
	if maxGroups < 1 {
		maxGroups = defaultQuietSummaryGroups
	}
	if maxGroups > 50 {
		maxGroups = 50
	}
	loc, err := time.LoadLocation(summary.Timezone)
	if err != nil {
		loc = time.Local
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Quiet-hours summary: %d hits\nPeriod: %s-%s\n",
		summary.TotalCount, summary.PeriodStart.In(loc).Format("2006-01-02 15:04"), summary.PeriodEnd.In(loc).Format("2006-01-02 15:04"))
	shown, shownHits := 0, 0
	for _, group := range groups {
		if shown >= maxGroups {
			break
		}
		var section strings.Builder
		fmt.Fprintf(&section, "\n%d. %dx (%s-%s)\nFirst: %s\n",
			shown+1, group.Count, group.FirstSeen.In(loc).Format("15:04:05"), group.LastSeen.In(loc).Format("15:04:05"), group.FirstExample)
		if group.LastExample != group.FirstExample {
			fmt.Fprintf(&section, "Last: %s\n", group.LastExample)
		}
		if b.Len()+section.Len() > maxQuietSummaryLength {
			break
		}
		b.WriteString(section.String())
		shown++
		shownHits += group.Count
	}
	if shown < len(groups) {
		fmt.Fprintf(&b, "\nOther: %d message groups, %d hits", len(groups)-shown, summary.TotalCount-shownHits)
	}
	notification := summary.Notification
	notification.ID = fmt.Sprintf("quiet-summary-%s", summary.Key)
	notification.Detail = strings.TrimSpace(b.String())
	notification.Log = nil
	notification.Stat = nil
	notification.Event = nil
	notification.Timestamp = summary.PeriodEnd
	notification.Context = nil
	return notification
}
