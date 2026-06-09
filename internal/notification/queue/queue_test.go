package queue

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/amir20/dozzle/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueue_Healthcheck(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notifications.db")

	q, err := NewQueue(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, q.Close())
	})

	require.NoError(t, q.Healthcheck(context.Background()))
}

func TestQueue_EnqueueAndDrainRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notifications.db")

	q, err := NewQueue(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, q.Close())
	})

	notification := types.Notification{
		ID:        "notif-1",
		Type:      types.LogNotification,
		Detail:    "detail",
		Log:       &types.NotificationLog{Message: "hello"},
		Timestamp: time.Now().UTC(),
		Subscription: types.SubscriptionConfig{
			ID:           42,
			DispatcherID: 7,
			Name:         "quiet-hours",
			Enabled:      true,
		},
	}

	require.NoError(t, q.Enqueue(notification, time.Now().Add(-time.Second)))

	var count int
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM notification_queue WHERE status = 'pending'`).Scan(&count))
	assert.Equal(t, 1, count)

	items, err := q.DrainReady(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, notification.Subscription.ID, items[0].SubscriptionID)
	assert.Equal(t, notification.Subscription.DispatcherID, items[0].DispatcherID)
	assert.Equal(t, notification.Detail, items[0].Notification.Detail)
	assert.Equal(t, "hello", items[0].Notification.Log.Message)
}

func TestQueue_QuietSummaryRoundTrip(t *testing.T) {
	q, err := NewQueue(filepath.Join(t.TempDir(), "notifications.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, q.Close()) })

	end := time.Now().Add(-time.Minute).Truncate(time.Second)
	summary := QuietSummary{
		Key: "1:host:container:period", SubscriptionID: 1, DispatcherID: 2,
		PeriodStart: end.Add(-8 * time.Hour), PeriodEnd: end, Timezone: "Europe/Prague",
		Notification: types.Notification{
			ID: "example", Detail: "first", Subscription: types.SubscriptionConfig{ID: 1, DispatcherID: 2},
		},
		GroupsJSON: `[{"count":1}]`, TotalCount: 1,
	}
	require.NoError(t, q.UpsertQuietSummary(summary))

	loaded, err := q.GetQuietSummary(summary.Key)
	require.NoError(t, err)
	assert.Equal(t, 1, loaded.TotalCount)
	assert.Equal(t, "first", loaded.Notification.Detail)

	loaded.TotalCount = 2
	loaded.GroupsJSON = `[{"count":2}]`
	require.NoError(t, q.UpsertQuietSummary(*loaded))

	due, err := q.DueQuietSummaries(time.Now())
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, 2, due[0].TotalCount)

	require.NoError(t, q.DeleteQuietSummary(summary.Key))
	due, err = q.DueQuietSummaries(time.Now())
	require.NoError(t, err)
	assert.Empty(t, due)
}

func TestQueue_QuietPeriodModeStaysLocked(t *testing.T) {
	q, err := NewQueue(filepath.Join(t.TempDir(), "notifications.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, q.Close()) })

	end := time.Now().Add(time.Hour)
	grouping, err := q.QuietPeriodGrouping("1:period", end, true)
	require.NoError(t, err)
	assert.True(t, grouping)

	grouping, err = q.QuietPeriodGrouping("1:period", end, false)
	require.NoError(t, err)
	assert.True(t, grouping)
}
