package docker_support

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/amir20/dozzle/internal/container"
	"github.com/amir20/dozzle/internal/notification"
	container_support "github.com/amir20/dozzle/internal/support/container"
	"github.com/amir20/dozzle/types"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubClientService struct {
	host container.Host
}

func (s stubClientService) FindContainer(ctx context.Context, id string, labels container.ContainerLabels) (container.Container, error) {
	return container.Container{}, nil
}

func (s stubClientService) ListContainers(ctx context.Context, labels container.ContainerLabels) ([]container.Container, error) {
	return nil, nil
}

func (s stubClientService) Host(ctx context.Context) (container.Host, error) {
	return s.host, nil
}

func (s stubClientService) ContainerAction(ctx context.Context, container container.Container, action container.ContainerAction) error {
	return nil
}

func (s stubClientService) UpdateContainer(ctx context.Context, container container.Container, progressCh chan<- container.UpdateProgress) (bool, error) {
	close(progressCh)
	return false, nil
}

func (s stubClientService) LogsBetweenDates(ctx context.Context, container container.Container, from time.Time, to time.Time, stdTypes container.StdType) (<-chan *container.LogEvent, error) {
	return nil, nil
}

func (s stubClientService) RawLogs(ctx context.Context, container container.Container, from time.Time, to time.Time, stdTypes container.StdType) (io.ReadCloser, error) {
	return nil, nil
}

func (s stubClientService) SubscribeStats(ctx context.Context, stats chan<- container.ContainerStat) {
}

func (s stubClientService) SubscribeEvents(ctx context.Context, events chan<- container.ContainerEvent) {
}

func (s stubClientService) SubscribeContainersStarted(ctx context.Context, containers chan<- container.Container) {
}

func (s stubClientService) StreamLogs(ctx context.Context, container container.Container, from time.Time, stdTypes container.StdType, events chan<- *container.LogEvent) error {
	return nil
}

func (s stubClientService) Attach(ctx context.Context, container container.Container, events container.ExecEventReader, stdout io.Writer) error {
	return nil
}

func (s stubClientService) Exec(ctx context.Context, container container.Container, cmd []string, events container.ExecEventReader, stdout io.Writer) error {
	return nil
}

var _ container_support.ClientService = stubClientService{}

func TestSubscriptionConfigsForAgentsCopiesAlertSettings(t *testing.T) {
	pausedUntil := time.Date(2026, time.June, 1, 10, 30, 0, 0, time.UTC)
	configs := subscriptionConfigsForAgents(
		[]*notification.Subscription{
			{
				ID:                        42,
				Name:                      "Agent alert",
				AlertGroup:                "Ops",
				Enabled:                   true,
				DispatcherID:              7,
				LogExpression:             "message contains 'error'",
				ContainerExpression:       "name == 'api'",
				MetricExpression:          "cpu > 90",
				EventExpression:           "event == 'die'",
				Cooldown:                  120,
				SampleWindow:              30,
				PausedUntil:               &pausedUntil,
				DeliveryDays:              []string{"mon", "fri"},
				NtfyTopic:                 "ops",
				NtfyPriority:              5,
				NtfyTags:                  []string{"docker", "agent"},
				BypassQuietHours:          true,
				QuietPriority:             1,
				HoldDuringQuiet:           true,
				HoldClearWindow:           60,
				BurstCount:                3,
				BurstWindow:               300,
				BurstPriority:             5,
				BurstNtfyTopic:            "ops-burst",
				UniqueKeyRegex:            `request_id=(\w+)`,
				UniqueWindow:              3600,
				UniqueThreshold:           2,
				WatchdogPattern:           "message contains 'ok'",
				WatchdogWindow:            600,
				WatchdogCooldown:          900,
				WatchdogTriggerMessage:    "service did not recover",
				WatchdogClearMessage:      "service recovered",
				RestartLoopEnabled:        true,
				RestartLoopStateWindow:    120,
				RestartLoopEventCount:     4,
				RestartLoopEventWindow:    300,
				RestartLoopCooldown:       600,
				RestartLoopTriggerMessage: "restart loop detected",
				QuietStackThreshold:       5,
				QuietStackWindow:          1800,
				AlertQuietEnabled:         true,
				AlertQuietStart:           "22:00",
				AlertQuietEnd:             "07:00",
				AlertQuietTimezone:        "Europe/Prague",
			},
		},
		notification.QuietHoursConfig{},
	)

	require.Len(t, configs, 1)
	assert.Equal(t, types.SubscriptionConfig{
		ID:                        42,
		Name:                      "Agent alert",
		AlertGroup:                "Ops",
		Enabled:                   true,
		DispatcherID:              7,
		LogExpression:             "message contains 'error'",
		ContainerExpression:       "name == 'api'",
		MetricExpression:          "cpu > 90",
		EventExpression:           "event == 'die'",
		Cooldown:                  120,
		SampleWindow:              30,
		PausedUntil:               &pausedUntil,
		DeliveryDays:              []string{"mon", "fri"},
		NtfyTopic:                 "ops",
		NtfyPriority:              5,
		NtfyTags:                  []string{"docker", "agent"},
		BypassQuietHours:          true,
		QuietPriority:             1,
		HoldDuringQuiet:           true,
		HoldClearWindow:           60,
		BurstCount:                3,
		BurstWindow:               300,
		BurstPriority:             5,
		BurstNtfyTopic:            "ops-burst",
		UniqueKeyRegex:            `request_id=(\w+)`,
		UniqueWindow:              3600,
		UniqueThreshold:           2,
		WatchdogPattern:           "message contains 'ok'",
		WatchdogWindow:            600,
		WatchdogCooldown:          900,
		WatchdogTriggerMessage:    "service did not recover",
		WatchdogClearMessage:      "service recovered",
		RestartLoopEnabled:        true,
		RestartLoopStateWindow:    120,
		RestartLoopEventCount:     4,
		RestartLoopEventWindow:    300,
		RestartLoopCooldown:       600,
		RestartLoopTriggerMessage: "restart loop detected",
		QuietStackThreshold:       5,
		QuietStackWindow:          1800,
		AlertQuietEnabled:         true,
		AlertQuietStart:           "22:00",
		AlertQuietEnd:             "07:00",
		AlertQuietTimezone:        "Europe/Prague",
	}, configs[0])
}

func TestSubscriptionConfigsForAgentsMaterializesGlobalQuietHours(t *testing.T) {
	configs := subscriptionConfigsForAgents(
		[]*notification.Subscription{
			{
				ID:                  7,
				Name:                "agent log alert",
				Enabled:             true,
				ContainerExpression: "name == 'api'",
				LogExpression:       "message contains 'error'",
			},
		},
		notification.QuietHoursConfig{
			Enabled:        true,
			Start:          "22:00",
			End:            "07:00",
			Timezone:       "Europe/Prague",
			StackThreshold: 5,
			StackWindow:    1800,
		},
	)

	require.Len(t, configs, 1)
	assert.True(t, configs[0].AlertQuietEnabled)
	assert.Equal(t, "22:00", configs[0].AlertQuietStart)
	assert.Equal(t, "07:00", configs[0].AlertQuietEnd)
	assert.Equal(t, "Europe/Prague", configs[0].AlertQuietTimezone)
	assert.Equal(t, 5, configs[0].QuietStackThreshold)
	assert.Equal(t, 1800, configs[0].QuietStackWindow)
}

func TestSubscriptionConfigsForAgentsPreservesPerAlertQuietHours(t *testing.T) {
	configs := subscriptionConfigsForAgents(
		[]*notification.Subscription{
			{
				ID:                  8,
				Name:                "custom quiet alert",
				Enabled:             true,
				ContainerExpression: "true",
				LogExpression:       "message contains 'warn'",
				AlertQuietEnabled:   true,
				AlertQuietStart:     "12:00",
				AlertQuietEnd:       "13:00",
				AlertQuietTimezone:  "UTC",
			},
		},
		notification.QuietHoursConfig{
			Enabled:  true,
			Start:    "22:00",
			End:      "07:00",
			Timezone: "Europe/Prague",
		},
	)

	require.Len(t, configs, 1)
	assert.True(t, configs[0].AlertQuietEnabled)
	assert.Equal(t, "12:00", configs[0].AlertQuietStart)
	assert.Equal(t, "13:00", configs[0].AlertQuietEnd)
	assert.Equal(t, "UTC", configs[0].AlertQuietTimezone)
}

func TestRetriableClientManagerSubscribeReplaysCurrentHosts(t *testing.T) {
	m := &RetriableClientManager{
		clients: map[string]container_support.ClientService{
			"host-1": stubClientService{
				host: container.Host{
					ID:       "host-1",
					Name:     "agent-1",
					Type:     "agent",
					Endpoint: "agent-1:7007",
				},
			},
		},
		subscribers: xsync.NewMap[context.Context, chan<- container.Host](),
		timeout:     time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan container.Host, 1)

	m.Subscribe(ctx, ch)

	select {
	case host := <-ch:
		assert.Equal(t, "host-1", host.ID)
		assert.True(t, host.Available)
		assert.Equal(t, "agent", host.Type)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replayed host")
	}
}
