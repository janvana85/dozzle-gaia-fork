package notification

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amir20/dozzle/internal/container"
	container_support "github.com/amir20/dozzle/internal/support/container"
	"github.com/amir20/dozzle/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func newNotificationTestManager(t *testing.T) (*Manager, *ContainerLogListener, context.CancelFunc) {
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
	manager := NewManager(listener, statsListener, eventListener, "")
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
