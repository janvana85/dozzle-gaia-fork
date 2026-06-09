package notification

import (
	"strings"
	"testing"
	"time"

	"github.com/amir20/dozzle/internal/notification/queue"
	"github.com/amir20/dozzle/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeQuietSummaryTextRemovesDynamicValues(t *testing.T) {
	first := `WARNING 2026-06-09 03:56:50,287 pid 14474 {"detail":"Expected available in 108 seconds.","status":429}`
	last := `WARNING 2026-06-09 05:10:11,999 pid 17890 {"detail":"Expected available in 42 seconds.","status":429}`

	assert.Equal(t, normalizeQuietSummaryText(first), normalizeQuietSummaryText(last))
	assert.Contains(t, normalizeQuietSummaryText(first), "429")
}

func TestRenderQuietSummaryShowsCountsTimesAndDistinctExamples(t *testing.T) {
	loc := time.FixedZone("CEST", 2*60*60)
	start := time.Date(2026, 6, 8, 22, 0, 0, 0, loc)
	end := time.Date(2026, 6, 9, 6, 30, 0, 0, loc)
	groups := []quietSummaryGroup{
		{
			Fingerprint: "a", Count: 100,
			FirstSeen: start.Add(2 * time.Hour), LastSeen: end.Add(-time.Minute),
			FirstExample: "first throttled request", LastExample: "last throttled request",
		},
		{
			Fingerprint: "b", Count: 2,
			FirstSeen: start.Add(3 * time.Hour), LastSeen: start.Add(4 * time.Hour),
			FirstExample: "timeout", LastExample: "timeout",
		},
	}
	summary := queue.QuietSummary{
		Key: "summary", PeriodStart: start, PeriodEnd: end, Timezone: "Europe/Prague",
		TotalCount: 102, GroupsJSON: mustJSON(groups),
		Notification: types.Notification{
			Subscription: types.SubscriptionConfig{ID: 1, Name: "EasyJukebox - WARNING"},
			Container:    types.NotificationContainer{Name: "easyjukebox-api", HostName: "Saturn"},
		},
	}

	notification := renderQuietSummary(summary, 1)

	assert.Contains(t, notification.Detail, "Quiet-hours summary: 102 hits")
	assert.Contains(t, notification.Detail, "100x")
	assert.Contains(t, notification.Detail, "First: first throttled request")
	assert.Contains(t, notification.Detail, "Last: last throttled request")
	assert.Contains(t, notification.Detail, "Other: 1 message groups, 2 hits")
	assert.NotContains(t, notification.Detail, "Last: timeout")
}

func TestTruncateQuietExampleAndSummaryLength(t *testing.T) {
	assert.Len(t, truncateQuietExample(strings.Repeat("x", 2000)), maxQuietSummaryExample)

	groups := make([]quietSummaryGroup, 20)
	for i := range groups {
		groups[i] = quietSummaryGroup{
			Fingerprint: string(rune('a' + i)), Count: 1,
			FirstSeen: time.Now(), LastSeen: time.Now(),
			FirstExample: strings.Repeat("x", maxQuietSummaryExample),
			LastExample: strings.Repeat("y", maxQuietSummaryExample),
		}
	}
	notification := renderQuietSummary(queue.QuietSummary{
		Key: "large", PeriodStart: time.Now().Add(-time.Hour), PeriodEnd: time.Now(),
		TotalCount: len(groups), GroupsJSON: mustJSON(groups),
	}, 10)
	require.NotEmpty(t, notification.Detail)
	assert.LessOrEqual(t, len(notification.Detail), maxQuietSummaryLength+100)
}
