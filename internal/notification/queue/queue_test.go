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
