package notification

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amir20/dozzle/internal/container"
	container_support "github.com/amir20/dozzle/internal/support/container"
	"github.com/amir20/dozzle/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

type recordingDispatcher struct {
	count atomic.Int32
	mu    sync.Mutex
	sent  []types.Notification
}

func (d *recordingDispatcher) Send(_ context.Context, notification types.Notification) error {
	d.count.Add(1)
	d.mu.Lock()
	d.sent = append(d.sent, notification)
	d.mu.Unlock()
	return nil
}

func (d *recordingDispatcher) notifications() []types.Notification {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]types.Notification(nil), d.sent...)
}

type notificationTestClient struct {
	host      container.Host
	container container.Container
}

func TestUpdateSubscriptionPreservesAdvancedFields(t *testing.T) {
	manager, _, cancel := newNotificationTestManager(t)
	defer cancel()

	sub := &Subscription{
		Name:                   "api errors",
		Enabled:                true,
		DispatcherID:           7,
		ContainerExpression:    "name == 'app'",
		LogExpression:          "message contains 'error'",
		NtfyTopic:              "api-alerts",
		NtfyPriority:           5,
		NtfyTags:               []string{"api", "quiet"},
		BypassQuietHours:       true,
		QuietPriority:          1,
		HoldDuringQuiet:        true,
		HoldClearWindow:        30,
		BurstCount:             4,
		BurstWindow:            300,
		BurstPriority:          5,
		WatchdogPattern:        "message contains 'ok'",
		WatchdogWindow:         60,
		WatchdogCooldown:       600,
		WatchdogTriggerMessage: "Service did not recover",
		WatchdogClearMessage:   "Service recovered",
		AlertQuietEnabled:      true,
		AlertQuietStart:        "22:00",
		AlertQuietEnd:          "07:00",
		AlertQuietTimezone:     "Europe/Prague",
		QuietStackThreshold:    3,
		QuietStackWindow:       900,
	}
	require.NoError(t, manager.AddSubscription(sub))

	require.NoError(t, manager.UpdateSubscription(sub.ID, map[string]any{"enabled": false}))

	updated, ok := manager.subscriptions.Load(sub.ID)
	require.True(t, ok)
	assert.False(t, updated.Enabled)
	assert.Equal(t, "api-alerts", updated.NtfyTopic)
	assert.Equal(t, 5, updated.NtfyPriority)
	assert.Equal(t, []string{"api", "quiet"}, updated.NtfyTags)
	assert.True(t, updated.BypassQuietHours)
	assert.Equal(t, 1, updated.QuietPriority)
	assert.True(t, updated.HoldDuringQuiet)
	assert.Equal(t, 30, updated.HoldClearWindow)
	assert.Equal(t, 4, updated.BurstCount)
	assert.Equal(t, 300, updated.BurstWindow)
	assert.Equal(t, 5, updated.BurstPriority)
	assert.Equal(t, "message contains 'ok'", updated.WatchdogPattern)
	assert.Equal(t, 60, updated.WatchdogWindow)
	assert.Equal(t, 600, updated.WatchdogCooldown)
	assert.Equal(t, "Service did not recover", updated.WatchdogTriggerMessage)
	assert.Equal(t, "Service recovered", updated.WatchdogClearMessage)
	assert.True(t, updated.AlertQuietEnabled)
	assert.Equal(t, "22:00", updated.AlertQuietStart)
	assert.Equal(t, "07:00", updated.AlertQuietEnd)
	assert.Equal(t, "Europe/Prague", updated.AlertQuietTimezone)
	assert.Equal(t, 3, updated.QuietStackThreshold)
	assert.Equal(t, 900, updated.QuietStackWindow)
}

func TestLoadConfigPreservesAdvancedSubscriptionFields(t *testing.T) {
	manager, _, cancel := newNotificationTestManager(t)
	defer cancel()

	config := `
subscriptions:
  - id: 12
    name: api errors
    enabled: true
    dispatcherId: 0
    containerExpression: name == 'app'
    logExpression: message contains 'error'
    ntfyTopic: api-alerts
    ntfyPriority: 5
    ntfyTags:
      - api
      - quiet
    bypassQuietHours: true
    quietPriority: 1
    holdDuringQuiet: true
    holdClearWindow: 30
    burstCount: 4
    burstWindow: 300
    burstPriority: 5
    watchdogPattern: message contains 'ok'
    watchdogWindow: 60
    watchdogCooldown: 600
    watchdogTriggerMessage: Service did not recover
    watchdogClearMessage: Service recovered
    alertQuietEnabled: true
    alertQuietStart: "22:00"
    alertQuietEnd: "07:00"
    alertQuietTimezone: Europe/Prague
`
	require.NoError(t, manager.LoadConfig(strings.NewReader(config)))

	loaded, ok := manager.subscriptions.Load(12)
	require.True(t, ok)
	assert.Equal(t, "api-alerts", loaded.NtfyTopic)
	assert.Equal(t, 5, loaded.NtfyPriority)
	assert.Equal(t, []string{"api", "quiet"}, loaded.NtfyTags)
	assert.True(t, loaded.BypassQuietHours)
	assert.Equal(t, 1, loaded.QuietPriority)
	assert.True(t, loaded.HoldDuringQuiet)
	assert.Equal(t, 30, loaded.HoldClearWindow)
	assert.Equal(t, 4, loaded.BurstCount)
	assert.Equal(t, 300, loaded.BurstWindow)
	assert.Equal(t, 5, loaded.BurstPriority)
	assert.Equal(t, "message contains 'ok'", loaded.WatchdogPattern)
	assert.Equal(t, 60, loaded.WatchdogWindow)
	assert.Equal(t, 600, loaded.WatchdogCooldown)
	assert.Equal(t, "Service did not recover", loaded.WatchdogTriggerMessage)
	assert.Equal(t, "Service recovered", loaded.WatchdogClearMessage)
	assert.True(t, loaded.AlertQuietEnabled)
	assert.Equal(t, "22:00", loaded.AlertQuietStart)
	assert.Equal(t, "07:00", loaded.AlertQuietEnd)
	assert.Equal(t, "Europe/Prague", loaded.AlertQuietTimezone)
}

func (c *notificationTestClient) FindContainer(context.Context, string, container.ContainerLabels) (container.Container, error) {
	return c.container, nil
}

func (c *notificationTestClient) ListContainers(context.Context, container.ContainerLabels) ([]container.Container, error) {
	return []container.Container{c.container}, nil
}

func (c *notificationTestClient) Host(context.Context) (container.Host, error) {
	return c.host, nil
}

func (c *notificationTestClient) ContainerAction(context.Context, container.Container, container.ContainerAction) error {
	return nil
}

func (c *notificationTestClient) UpdateContainer(context.Context, container.Container, chan<- container.UpdateProgress) (bool, error) {
	return false, nil
}

func (c *notificationTestClient) LogsBetweenDates(context.Context, container.Container, time.Time, time.Time, container.StdType) (<-chan *container.LogEvent, error) {
	return nil, nil
}

func (c *notificationTestClient) RawLogs(context.Context, container.Container, time.Time, time.Time, container.StdType) (io.ReadCloser, error) {
	return nil, nil
}

func (c *notificationTestClient) SubscribeStats(context.Context, chan<- container.ContainerStat) {}

func (c *notificationTestClient) SubscribeEvents(context.Context, chan<- container.ContainerEvent) {}

func (c *notificationTestClient) SubscribeContainersStarted(context.Context, chan<- container.Container) {
}

func (c *notificationTestClient) StreamLogs(ctx context.Context, _ container.Container, _ time.Time, _ container.StdType, _ chan<- *container.LogEvent) error {
	<-ctx.Done()
	return ctx.Err()
}

func (c *notificationTestClient) Attach(context.Context, container.Container, container.ExecEventReader, io.Writer) error {
	return nil
}

func (c *notificationTestClient) Exec(context.Context, container.Container, []string, container.ExecEventReader, io.Writer) error {
	return nil
}

func TestRemoveSubscriptionCancelsPendingWatchdogNotification(t *testing.T) {
	manager, listener, cancel := newNotificationTestManager(t)
	defer cancel()

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)

	sub := &Subscription{
		Name:                "watchdog",
		Enabled:             true,
		DispatcherID:        dispatcherID,
		ContainerExpression: "true",
		LogExpression:       "message == 'start'",
		WatchdogPattern:     "message == 'done'",
		WatchdogWindow:      1,
	}
	require.NoError(t, manager.AddSubscription(sub))
	stored, ok := manager.subscriptions.Load(sub.ID)
	require.True(t, ok)

	listener.logChannel <- &container.LogEvent{
		ContainerID: "container-1",
		Message:     "start",
		Timestamp:   time.Now().UnixMilli(),
	}

	require.Eventually(t, func() bool {
		return stored.WatchdogTimers.Size() == 1
	}, time.Second, 10*time.Millisecond)

	manager.RemoveSubscription(sub.ID)

	time.Sleep(1200 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())
}

func TestWatchdogPairAlertDoesNotSendWhenResolveLogArrives(t *testing.T) {
	manager, listener, cancel := newNotificationTestManager(t)
	defer cancel()

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)

	sub := &Subscription{
		Name:                "paired-alert",
		Enabled:             true,
		DispatcherID:        dispatcherID,
		ContainerExpression: "true",
		LogExpression:       "message == 'start'",
		WatchdogPattern:     "message == 'done'",
		WatchdogWindow:      1,
	}
	require.NoError(t, manager.AddSubscription(sub))
	stored, ok := manager.subscriptions.Load(sub.ID)
	require.True(t, ok)

	listener.logChannel <- &container.LogEvent{
		ContainerID: "container-1",
		Message:     "start",
		Timestamp:   time.Now().UnixMilli(),
	}

	require.Eventually(t, func() bool {
		return stored.WatchdogTimers.Size() == 1
	}, time.Second, 10*time.Millisecond)

	listener.logChannel <- &container.LogEvent{
		ContainerID: "container-1",
		Message:     "done",
		Timestamp:   time.Now().UnixMilli(),
	}

	require.Eventually(t, func() bool {
		return stored.WatchdogTimers.Size() == 0
	}, time.Second, 10*time.Millisecond)

	time.Sleep(1200 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())
}

func TestWatchdogPairAlertSendsWhenResolveLogDoesNotArrive(t *testing.T) {
	manager, listener, cancel := newNotificationTestManager(t)
	defer cancel()

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)

	sub := &Subscription{
		Name:                   "paired-alert",
		Enabled:                true,
		DispatcherID:           dispatcherID,
		ContainerExpression:    "true",
		LogExpression:          "message == 'start'",
		WatchdogPattern:        "message == 'done'",
		WatchdogWindow:         1,
		WatchdogTriggerMessage: "Service did not recover within 1 second",
	}
	require.NoError(t, manager.AddSubscription(sub))

	listener.logChannel <- &container.LogEvent{
		ContainerID: "container-1",
		Message:     "start",
		Timestamp:   time.Now().UnixMilli(),
	}

	require.Eventually(t, func() bool {
		return dispatcher.count.Load() == 1
	}, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, "Service did not recover within 1 second", dispatcher.notifications()[0].Detail)
}

func TestWatchdogClearMessageUsesQuietHoursQueuePolicy(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notifications.db")
	manager, listener, cancel := newNotificationTestManagerWithDB(t, dbPath)
	defer cancel()
	defer manager.queue.Close()
	manager.SetQuietHours(QuietHoursConfig{
		Enabled: true,
		Start:   "00:00",
		End:     "23:59",
	})

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)

	sub := &Subscription{
		Name:                 "paired-alert",
		Enabled:              true,
		DispatcherID:         dispatcherID,
		ContainerExpression:  "true",
		LogExpression:        "message == 'start'",
		WatchdogPattern:      "message == 'done'",
		WatchdogWindow:       60,
		WatchdogClearMessage: "Service recovered",
		NtfyPriority:         5,
		QuietPriority:        1,
	}
	require.NoError(t, manager.AddSubscription(sub))
	stored, ok := manager.subscriptions.Load(sub.ID)
	require.True(t, ok)

	listener.logChannel <- &container.LogEvent{
		ContainerID: "container-1",
		Message:     "start",
		Timestamp:   time.Now().UnixMilli(),
	}
	require.Eventually(t, func() bool {
		return stored.WatchdogTimers.Size() == 1
	}, time.Second, 10*time.Millisecond)

	listener.logChannel <- &container.LogEvent{
		ContainerID: "container-1",
		Message:     "done",
		Timestamp:   time.Now().UnixMilli(),
	}

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())
	assert.Equal(t, 1, countPendingNotifications(t, dbPath))

	markPendingNotificationsReady(t, dbPath)
	manager.flushQueue()

	require.Eventually(t, func() bool {
		return dispatcher.count.Load() == 1
	}, time.Second, 10*time.Millisecond)
	sent := dispatcher.notifications()[0]
	assert.Equal(t, "Service recovered", sent.Detail)
	assert.Contains(t, sent.NtfyTags, "quiet-hours")
}

func TestGlobalQuietHoursAppliesQuietPriority(t *testing.T) {
	manager, _, cancel := newNotificationTestManager(t)
	defer cancel()
	manager.SetQuietHours(QuietHoursConfig{
		Enabled: true,
		Start:   "00:00",
		End:     "23:59",
	})

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)
	sub := &Subscription{
		ID:            1,
		Enabled:       true,
		DispatcherID:  dispatcherID,
		NtfyPriority:  5,
		QuietPriority: 1,
	}
	manager.subscriptions.Store(sub.ID, sub)

	manager.sendOrQueue(dispatcher, notificationForTest(sub), sub)

	require.Eventually(t, func() bool {
		return dispatcher.count.Load() == 1
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, 1, dispatcher.notifications()[0].NtfyPriority)
}

func TestBypassQuietHoursUsesNormalPriorityDuringGlobalQuietHours(t *testing.T) {
	manager, _, cancel := newNotificationTestManager(t)
	defer cancel()
	manager.SetQuietHours(QuietHoursConfig{
		Enabled: true,
		Start:   "00:00",
		End:     "23:59",
	})

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)
	sub := &Subscription{
		ID:               1,
		Enabled:          true,
		DispatcherID:     dispatcherID,
		NtfyPriority:     5,
		QuietPriority:    1,
		BypassQuietHours: true,
	}
	manager.subscriptions.Store(sub.ID, sub)

	manager.sendOrQueue(dispatcher, notificationForTest(sub), sub)

	require.Eventually(t, func() bool {
		return dispatcher.count.Load() == 1
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, 5, dispatcher.notifications()[0].NtfyPriority)
}

func TestPerAlertQuietHoursOverrideGlobalQuietHours(t *testing.T) {
	manager, _, cancel := newNotificationTestManager(t)
	defer cancel()
	manager.SetQuietHours(QuietHoursConfig{
		Enabled: true,
		Start:   "00:00",
		End:     "23:59",
	})

	start, end := quietWindowOutsideNow()
	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)
	sub := &Subscription{
		ID:                 1,
		Enabled:            true,
		DispatcherID:       dispatcherID,
		NtfyPriority:       5,
		QuietPriority:      1,
		AlertQuietEnabled:  true,
		AlertQuietStart:    start,
		AlertQuietEnd:      end,
		AlertQuietTimezone: "UTC",
	}
	manager.subscriptions.Store(sub.ID, sub)

	manager.sendOrQueue(dispatcher, notificationForTest(sub), sub)

	require.Eventually(t, func() bool {
		return dispatcher.count.Load() == 1
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, 5, dispatcher.notifications()[0].NtfyPriority)
}

func TestPerAlertQuietHoursQueuesToSQLiteWhenActive(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notifications.db")
	manager, _, cancel := newNotificationTestManagerWithDB(t, dbPath)
	defer cancel()
	defer manager.queue.Close()

	start, end := quietWindowContainingNow()
	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)
	sub := &Subscription{
		ID:                 1,
		Name:               "per-alert-quiet-hours",
		Enabled:            true,
		DispatcherID:       dispatcherID,
		NtfyPriority:       5,
		QuietPriority:      1,
		AlertQuietEnabled:  true,
		AlertQuietStart:    start,
		AlertQuietEnd:      end,
		AlertQuietTimezone: "UTC",
	}
	manager.subscriptions.Store(sub.ID, sub)

	manager.sendOrQueue(dispatcher, notificationForTest(sub), sub)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())
	assert.Equal(t, 1, countPendingNotifications(t, dbPath))
	queued := pendingNotifications(t, dbPath)
	require.Len(t, queued, 1)
	assert.Contains(t, queued[0].NtfyTags, "quiet-hours")
}

func TestBypassQuietHoursSendsImmediatelyEvenWhenPerAlertQuietHoursActive(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notifications.db")
	manager, _, cancel := newNotificationTestManagerWithDB(t, dbPath)
	defer cancel()
	defer manager.queue.Close()

	start, end := quietWindowContainingNow()
	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)
	sub := &Subscription{
		ID:                 1,
		Name:               "bypass-per-alert-quiet-hours",
		Enabled:            true,
		DispatcherID:       dispatcherID,
		NtfyPriority:       5,
		QuietPriority:      1,
		BypassQuietHours:   true,
		AlertQuietEnabled:  true,
		AlertQuietStart:    start,
		AlertQuietEnd:      end,
		AlertQuietTimezone: "UTC",
	}
	manager.subscriptions.Store(sub.ID, sub)

	manager.sendOrQueue(dispatcher, notificationForTest(sub), sub)

	require.Eventually(t, func() bool {
		return dispatcher.count.Load() == 1
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, 0, countPendingNotifications(t, dbPath))
	assert.Equal(t, 5, dispatcher.notifications()[0].NtfyPriority)
}

func TestQuietHoursQueuesRepeatedAlertWithEscalatedPriority(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notifications.db")
	manager, _, cancel := newNotificationTestManagerWithDB(t, dbPath)
	defer cancel()
	defer manager.queue.Close()
	manager.SetQuietHours(QuietHoursConfig{
		Enabled:         true,
		Start:           "00:00",
		End:             "23:59",
		StackThreshold:  3,
		StackWindow:     60,
		StackedPriority: 5,
	})

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)
	sub := &Subscription{
		ID:            1,
		Name:          "repeated-api-error",
		Enabled:       true,
		DispatcherID:  dispatcherID,
		NtfyPriority:  4,
		QuietPriority: 1,
	}
	manager.subscriptions.Store(sub.ID, sub)

	manager.sendOrQueue(dispatcher, notificationForTest(sub), sub)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())
	assert.Equal(t, 1, countPendingNotifications(t, dbPath))

	manager.sendOrQueue(dispatcher, notificationForTest(sub), sub)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())
	assert.Equal(t, 2, countPendingNotifications(t, dbPath))

	manager.sendOrQueue(dispatcher, notificationForTest(sub), sub)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())
	assert.Equal(t, 3, countPendingNotifications(t, dbPath))

	queued := pendingNotifications(t, dbPath)
	require.Len(t, queued, 3)
	assert.Equal(t, 4, queued[0].NtfyPriority)
	assert.Equal(t, 4, queued[1].NtfyPriority)
	assert.Equal(t, 5, queued[2].NtfyPriority)
	for _, notification := range queued {
		assert.Contains(t, notification.NtfyTags, "quiet-hours")
	}
}

func TestQuietHoursTakesPrecedenceOverHoldWindow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notifications.db")
	manager, _, cancel := newNotificationTestManagerWithDB(t, dbPath)
	defer cancel()
	defer manager.queue.Close()
	manager.SetQuietHours(QuietHoursConfig{
		Enabled: true,
		Start:   "00:00",
		End:     "23:59",
	})

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)
	sub := &Subscription{
		ID:              1,
		Name:            "quiet-with-hold-window",
		Enabled:         true,
		DispatcherID:    dispatcherID,
		NtfyPriority:    5,
		QuietPriority:   1,
		HoldClearWindow: 60,
	}
	manager.subscriptions.Store(sub.ID, sub)

	manager.sendOrQueue(dispatcher, notificationForTest(sub), sub)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())
	assert.Equal(t, 1, countPendingNotifications(t, dbPath))
}

func TestQuietHoursQueuesEveryAlertWithQuietHoursTagAndSpacing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notifications.db")
	manager, _, cancel := newNotificationTestManagerWithDB(t, dbPath)
	defer cancel()
	defer manager.queue.Close()
	manager.SetQuietHours(QuietHoursConfig{
		Enabled:         true,
		Start:           "00:00",
		End:             "23:59",
		StackThreshold:  100,
		StackWindow:     60,
		StackedPriority: 5,
	})

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)
	sub := &Subscription{
		ID:           1,
		Name:         "held-repeated-api-error",
		Enabled:      true,
		DispatcherID: dispatcherID,
		NtfyPriority: 3,
	}
	manager.subscriptions.Store(sub.ID, sub)

	for range 10 {
		manager.sendOrQueue(dispatcher, notificationForTest(sub), sub)
	}

	assert.Equal(t, int32(0), dispatcher.count.Load())
	assert.Equal(t, 10, countPendingNotifications(t, dbPath))
	assertQuietHoursTagsAndSpacing(t, dbPath)
	markPendingNotificationsReady(t, dbPath)

	manager.flushQueue()

	require.Eventually(t, func() bool {
		return dispatcher.count.Load() == 1
	}, time.Second, 10*time.Millisecond)
	for _, notification := range dispatcher.notifications() {
		assert.Contains(t, notification.NtfyTags, "quiet-hours")
	}
	assert.Equal(t, 9, countPendingNotifications(t, dbPath))
}

func TestQuietHoursHoldsPriorityFiveNotificationsAndDrainsQueueSequentially(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notifications.db")
	manager, _, cancel := newNotificationTestManagerWithDB(t, dbPath)
	defer cancel()
	defer manager.queue.Close()
	manager.SetQuietHours(QuietHoursConfig{
		Enabled:        true,
		Start:          "00:00",
		End:            "23:59",
		StackThreshold: 100,
		StackWindow:    60,
	})

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)
	sub := &Subscription{
		ID:           1,
		Name:         "priority-five-during-quiet-hours",
		Enabled:      true,
		DispatcherID: dispatcherID,
		NtfyPriority: 5,
	}
	manager.subscriptions.Store(sub.ID, sub)

	for range 3 {
		manager.sendOrQueue(dispatcher, notificationForTest(sub), sub)
	}

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())
	assert.Equal(t, 3, countPendingNotifications(t, dbPath))
	queued := pendingNotifications(t, dbPath)
	require.Len(t, queued, 3)
	for _, notification := range queued {
		assert.Equal(t, 5, notification.NtfyPriority)
		assert.Contains(t, notification.NtfyTags, "quiet-hours")
	}

	markPendingNotificationsReady(t, dbPath)

	manager.flushQueue()
	assert.Equal(t, int32(1), dispatcher.count.Load())
	assert.Equal(t, 2, countPendingNotifications(t, dbPath))

	manager.flushQueue()
	assert.Equal(t, int32(2), dispatcher.count.Load())
	assert.Equal(t, 1, countPendingNotifications(t, dbPath))

	manager.flushQueue()
	assert.Equal(t, int32(3), dispatcher.count.Load())
	assert.Equal(t, 0, countPendingNotifications(t, dbPath))

	for _, notification := range dispatcher.notifications() {
		assert.Equal(t, 5, notification.NtfyPriority)
		assert.Contains(t, notification.NtfyTags, "quiet-hours")
	}
}

func TestBurstEscalationRespectsQuietHours(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notifications.db")
	manager, _, cancel := newNotificationTestManagerWithDB(t, dbPath)
	defer cancel()
	defer manager.queue.Close()
	manager.SetQuietHours(QuietHoursConfig{
		Enabled: true,
		Start:   "00:00",
		End:     "23:59",
	})

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)
	sub := &Subscription{
		ID:           1,
		Name:         "burst-api-error",
		Enabled:      true,
		DispatcherID: dispatcherID,
		NtfyPriority: 5,
	}
	manager.subscriptions.Store(sub.ID, sub)

	manager.sendOrQueue(dispatcher, notificationForTest(sub), sub, true)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())
	assert.Equal(t, 1, countPendingNotifications(t, dbPath))
	queued := pendingNotifications(t, dbPath)
	require.Len(t, queued, 1)
	assert.Equal(t, 5, queued[0].NtfyPriority)
	assert.Contains(t, queued[0].NtfyTags, "quiet-hours")
}

func TestQueuedNotificationSurvivesManagerRestartAndFlushesWhenReady(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notifications.db")
	manager, _, cancel := newNotificationTestManagerWithDB(t, dbPath)
	dispatcherID := manager.AddDispatcher(&recordingDispatcher{})
	sub := &Subscription{
		ID:           1,
		Name:         "queued-after-quiet-hours",
		Enabled:      true,
		DispatcherID: dispatcherID,
	}
	manager.subscriptions.Store(sub.ID, sub)
	require.NoError(t, manager.queue.Enqueue(notificationForTest(sub), time.Now().Add(-time.Second)))
	cancel()
	require.NoError(t, manager.queue.Close())

	restarted, _, restartedCancel := newNotificationTestManagerWithDB(t, dbPath)
	defer restartedCancel()
	defer restarted.queue.Close()
	dispatcher := &recordingDispatcher{}
	restartedDispatcherID := restarted.AddDispatcher(dispatcher)
	require.Equal(t, dispatcherID, restartedDispatcherID)
	restarted.subscriptions.Store(sub.ID, sub)

	restarted.flushQueue()

	require.Eventually(t, func() bool {
		return dispatcher.count.Load() == 1
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, "container-1", dispatcher.notifications()[0].Container.ID)
}

func TestQueuedNotificationIsDroppedAfterRestartWhenSubscriptionInactive(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "notifications.db")
	manager, _, cancel := newNotificationTestManagerWithDB(t, dbPath)
	dispatcherID := manager.AddDispatcher(&recordingDispatcher{})
	sub := &Subscription{
		ID:           1,
		Name:         "queued-after-quiet-hours",
		Enabled:      true,
		DispatcherID: dispatcherID,
	}
	manager.subscriptions.Store(sub.ID, sub)
	require.NoError(t, manager.queue.Enqueue(notificationForTest(sub), time.Now().Add(-time.Second)))
	cancel()
	require.NoError(t, manager.queue.Close())

	restarted, _, restartedCancel := newNotificationTestManagerWithDB(t, dbPath)
	defer restartedCancel()
	defer restarted.queue.Close()
	dispatcher := &recordingDispatcher{}
	restarted.AddDispatcher(dispatcher)

	restarted.flushQueue()
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(0), dispatcher.count.Load())
	ready, err := restarted.queue.DrainReady(t.Context())
	require.NoError(t, err)
	assert.Empty(t, ready)
}

func newNotificationTestManager(t *testing.T) (*Manager, *ContainerLogListener, context.CancelFunc) {
	return newNotificationTestManagerWithDB(t, "")
}

func newNotificationTestManagerWithDB(t *testing.T, dbPath string) (*Manager, *ContainerLogListener, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	client := &notificationTestClient{
		host: container.Host{ID: "host-1", Name: "host"},
		container: container.Container{
			ID:     "container-1",
			Name:   "app",
			Image:  "example/app",
			State:  "running",
			Labels: map[string]string{},
		},
	}
	listener := NewContainerLogListener(ctx, []container_support.ClientService{client})
	statsListener := NewContainerStatsListener(ctx, []container_support.ClientService{client})
	eventListener := NewContainerEventListener(ctx, []container_support.ClientService{client})
	manager := NewManager(listener, statsListener, eventListener, dbPath)
	require.NoError(t, manager.Start())
	return manager, listener, cancel
}

func notificationForTest(sub *Subscription) types.Notification {
	return types.Notification{
		ID:           "notification-1",
		Type:         types.LogNotification,
		Detail:       "detail",
		NtfyPriority: sub.NtfyPriority,
		Container: types.NotificationContainer{
			ID:       "container-1",
			Name:     "app",
			HostName: "host",
		},
		Subscription: types.SubscriptionConfig{
			ID:           sub.ID,
			Name:         sub.Name,
			Enabled:      sub.Enabled,
			DispatcherID: sub.DispatcherID,
		},
		Timestamp: time.Now(),
	}
}

func quietWindowOutsideNow() (string, string) {
	now := time.Now().UTC()
	start := now.Add(2 * time.Hour)
	end := start.Add(time.Hour)
	return start.Format("15:04"), end.Format("15:04")
}

func quietWindowContainingNow() (string, string) {
	now := time.Now().UTC()
	start := now.Add(-time.Hour)
	end := now.Add(time.Hour)
	return start.Format("15:04"), end.Format("15:04")
}

func countPendingNotifications(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM notification_queue WHERE status = 'pending'`).Scan(&count))
	return count
}

func pendingNotifications(t *testing.T, dbPath string) []types.Notification {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

	rows, err := db.Query(`SELECT payload FROM notification_queue WHERE status = 'pending' ORDER BY deliver_at ASC`)
	require.NoError(t, err)
	defer rows.Close()

	var notifications []types.Notification
	for rows.Next() {
		var payload string
		require.NoError(t, rows.Scan(&payload))
		var notification types.Notification
		require.NoError(t, json.Unmarshal([]byte(payload), &notification))
		notifications = append(notifications, notification)
	}
	require.NoError(t, rows.Err())
	return notifications
}

func markPendingNotificationsReady(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`UPDATE notification_queue SET deliver_at = ? WHERE status = 'pending'`, time.Now().Add(-time.Second).UTC())
	require.NoError(t, err)
}

func assertQuietHoursTagsAndSpacing(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

	rows, err := db.Query(`SELECT payload, deliver_at FROM notification_queue WHERE status = 'pending' ORDER BY deliver_at ASC`)
	require.NoError(t, err)
	defer rows.Close()

	var previous time.Time
	count := 0
	for rows.Next() {
		var payload string
		var deliverAtText string
		require.NoError(t, rows.Scan(&payload, &deliverAtText))
		var notification types.Notification
		require.NoError(t, json.Unmarshal([]byte(payload), &notification))
		assert.Contains(t, notification.NtfyTags, "quiet-hours")
		deliverAt, err := time.Parse(time.RFC3339Nano, deliverAtText)
		require.NoError(t, err)
		if count > 0 {
			assert.Equal(t, 2*time.Second, deliverAt.Sub(previous))
		}
		previous = deliverAt
		count++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, 10, count)
}
