package docker_support

import (
	"testing"
	"time"

	"github.com/amir20/dozzle/internal/notification"
	"github.com/amir20/dozzle/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
