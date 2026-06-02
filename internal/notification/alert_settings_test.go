package notification

import (
	"testing"
	"time"

	"github.com/amir20/dozzle/internal/container"
	"github.com/amir20/dozzle/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// emitEvent pushes a Docker event straight onto the event listener channel that
// processDockerEvents drains. The test client's SubscribeEvents is a no-op, so
// this is the only way events reach the manager in tests.
func emitEvent(manager *Manager, name, state string) {
	manager.eventListener.channel <- &ContainerEventEntry{
		Event:     container.ContainerEvent{Name: name, ActorID: "container-1", Time: time.Now()},
		Container: container.Container{ID: "container-1", Name: "app", State: state},
		Host:      container.Host{ID: "host-1", Name: "host"},
	}
}

func emitStat(manager *Manager, cpu float64) {
	manager.statsListener.channel <- &ContainerStatEvent{
		Stat:      container.ContainerStat{ID: "container-1", CPUPercent: cpu},
		Container: container.Container{ID: "container-1", Name: "app"},
		Host:      container.Host{ID: "host-1", Name: "host", NCPU: 1},
	}
}

func TestRestartLoopEventCountFiresOnceThenCooldownSuppresses(t *testing.T) {
	manager, _, cancel := newNotificationTestManager(t)
	defer cancel()

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)

	sub := &Subscription{
		Name:                      "restart-loop",
		Enabled:                   true,
		DispatcherID:              dispatcherID,
		ContainerExpression:       "true",
		EventExpression:           `name == "restart"`,
		RestartLoopEnabled:        true,
		RestartLoopEventCount:     3,
		RestartLoopEventWindow:    60,
		RestartLoopCooldown:       300,
		RestartLoopTriggerMessage: "container restarting too often",
	}
	require.NoError(t, manager.AddSubscription(sub))

	for i := 0; i < 3; i++ {
		emitEvent(manager, "restart", "restarting")
	}

	require.Eventually(t, func() bool {
		return dispatcher.count.Load() == 1
	}, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, "container restarting too often", dispatcher.notifications()[0].Detail)

	// Further restarts within the cooldown window must not produce more alerts.
	for i := 0; i < 3; i++ {
		emitEvent(manager, "restart", "restarting")
	}
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(1), dispatcher.count.Load())
}

func TestRestartLoopEventCountStaysSilentBelowThreshold(t *testing.T) {
	manager, _, cancel := newNotificationTestManager(t)
	defer cancel()

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)

	sub := &Subscription{
		Name:                   "restart-loop",
		Enabled:                true,
		DispatcherID:           dispatcherID,
		ContainerExpression:    "true",
		EventExpression:        `name == "restart"`,
		RestartLoopEnabled:     true,
		RestartLoopEventCount:  3,
		RestartLoopEventWindow: 60,
	}
	require.NoError(t, manager.AddSubscription(sub))

	emitEvent(manager, "restart", "restarting")
	emitEvent(manager, "restart", "restarting")

	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())
}

func TestRestartLoopStateWindowFiresWhenRestartingPersists(t *testing.T) {
	manager, _, cancel := newNotificationTestManager(t)
	defer cancel()

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)

	sub := &Subscription{
		Name:                   "restart-state",
		Enabled:                true,
		DispatcherID:           dispatcherID,
		ContainerExpression:    "true",
		EventExpression:        "true",
		RestartLoopEnabled:     true,
		RestartLoopStateWindow: 1,
		RestartLoopCooldown:    300,
	}
	require.NoError(t, manager.AddSubscription(sub))

	emitEvent(manager, "restart", "restarting")

	require.Eventually(t, func() bool {
		return dispatcher.count.Load() == 1
	}, 3*time.Second, 20*time.Millisecond)
	assert.Equal(t, "restarting state persisted", dispatcher.notifications()[0].Detail)
}

func TestRestartLoopStateWindowCancelledWhenStateRecovers(t *testing.T) {
	manager, _, cancel := newNotificationTestManager(t)
	defer cancel()

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)

	sub := &Subscription{
		Name:                   "restart-state",
		Enabled:                true,
		DispatcherID:           dispatcherID,
		ContainerExpression:    "true",
		EventExpression:        "true",
		RestartLoopEnabled:     true,
		RestartLoopStateWindow: 1,
		RestartLoopCooldown:    300,
	}
	require.NoError(t, manager.AddSubscription(sub))
	stored, ok := manager.subscriptions.Load(sub.ID)
	require.True(t, ok)

	emitEvent(manager, "restart", "restarting")
	require.Eventually(t, func() bool {
		return stored.RestartLoopTimers.Size() == 1
	}, time.Second, 10*time.Millisecond)

	// Container leaves the restarting state before the window elapses.
	emitEvent(manager, "start", "running")

	time.Sleep(1500 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())
}

func TestMetricSampleWindowFiresOnlyWhenBufferFull(t *testing.T) {
	manager, _, cancel := newNotificationTestManager(t)
	defer cancel()

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)

	sub := &Subscription{
		Name:                "metric-window",
		Enabled:             true,
		DispatcherID:        dispatcherID,
		ContainerExpression: "true",
		MetricExpression:    "cpu > 50",
		SampleWindow:        3,
	}
	require.NoError(t, manager.AddSubscription(sub))

	// Two matching samples are not enough to fill the window of 3.
	emitStat(manager, 60)
	emitStat(manager, 60)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())

	// Third matching sample fills the window with 100% matches and fires.
	emitStat(manager, 60)
	require.Eventually(t, func() bool {
		return dispatcher.count.Load() == 1
	}, 2*time.Second, 10*time.Millisecond)
}

func TestMetricSampleWindowStaysSilentBelowMatchRatio(t *testing.T) {
	manager, _, cancel := newNotificationTestManager(t)
	defer cancel()

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)

	sub := &Subscription{
		Name:                "metric-window",
		Enabled:             true,
		DispatcherID:        dispatcherID,
		ContainerExpression: "true",
		MetricExpression:    "cpu > 50",
		SampleWindow:        3,
	}
	require.NoError(t, manager.AddSubscription(sub))

	// Only 2 of 3 samples match (66%), below the 80% threshold.
	emitStat(manager, 60)
	emitStat(manager, 60)
	emitStat(manager, 10)

	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(0), dispatcher.count.Load())
}

func TestBurstEscalationRoutesToBurstTopic(t *testing.T) {
	manager, listener, cancel := newNotificationTestManager(t)
	defer cancel()

	dispatcher := &recordingDispatcher{}
	dispatcherID := manager.AddDispatcher(dispatcher)

	sub := &Subscription{
		Name:                "burst-topic",
		Enabled:             true,
		DispatcherID:        dispatcherID,
		ContainerExpression: "true",
		LogExpression:       `message == "boom"`,
		NtfyTopic:           "normal-topic",
		NtfyPriority:        3,
		BurstCount:          2,
		BurstWindow:         60,
		BurstPriority:       5,
		BurstNtfyTopic:      "burst-topic",
	}
	require.NoError(t, manager.AddSubscription(sub))

	for i := 0; i < 2; i++ {
		listener.logChannel <- &container.LogEvent{
			ContainerID: "container-1",
			Message:     "boom",
			Timestamp:   time.Now().UnixMilli(),
		}
	}

	require.Eventually(t, func() bool {
		return dispatcher.count.Load() == 2
	}, 2*time.Second, 10*time.Millisecond)

	// Delivery happens on async goroutines, so the recorded order is not
	// deterministic. Assert on the set: exactly one normal and one escalated.
	var normal, escalated *types.Notification
	for i := range dispatcher.notifications() {
		n := dispatcher.notifications()[i]
		if n.NtfyTopic == "burst-topic" {
			escalated = &n
		} else {
			normal = &n
		}
	}
	require.NotNil(t, normal, "expected one notification on the base topic")
	require.NotNil(t, escalated, "expected one notification escalated to the burst topic")

	assert.Equal(t, "normal-topic", normal.NtfyTopic)
	assert.Equal(t, 3, normal.NtfyPriority)
	assert.NotContains(t, normal.NtfyTags, "burst")

	assert.Equal(t, 5, escalated.NtfyPriority)
	assert.Contains(t, escalated.NtfyTags, "burst")
}
