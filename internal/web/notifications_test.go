package web

import (
	"regexp"
	"testing"
	"time"

	"github.com/amir20/dozzle/internal/container"
	"github.com/amir20/dozzle/internal/notification"
	"github.com/stretchr/testify/assert"
)

func TestSubscriptionToResponseIncludesAdvancedAlertSettings(t *testing.T) {
	sub := &notification.Subscription{
		ID:                     42,
		Name:                   "api-errors",
		AlertGroup:             "EasyJukebox",
		Enabled:                true,
		DispatcherID:           7,
		ContainerExpression:    "true",
		LogExpression:          "message contains 'error'",
		NtfyTopic:              "api-alerts",
		NtfyPriority:           3,
		NtfyTags:               []string{"api", "error"},
		BypassQuietHours:       true,
		QuietPriority:          1,
		HoldDuringQuiet:        true,
		HoldClearWindow:        120,
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
		QuietStackThreshold:    4,
		QuietStackWindow:       600,
		PausedUntil:            ptrTime(time.Date(2026, time.May, 29, 13, 0, 0, 0, time.UTC)),
		DeliveryDays:           []string{"mon", "fri"},
	}

	resp := subscriptionToResponse(sub, nil, nil)

	assert.Equal(t, "api-alerts", resp.NtfyTopic)
	assert.Equal(t, "EasyJukebox", resp.AlertGroup)
	assert.Equal(t, 3, resp.NtfyPriority)
	assert.Equal(t, []string{"api", "error"}, resp.NtfyTags)
	assert.True(t, resp.BypassQuietHours)
	assert.Equal(t, 1, resp.QuietPriority)
	assert.True(t, resp.HoldDuringQuiet)
	assert.Equal(t, 120, resp.HoldClearWindow)
	assert.Equal(t, 4, resp.BurstCount)
	assert.Equal(t, 300, resp.BurstWindow)
	assert.Equal(t, 5, resp.BurstPriority)
	assert.Equal(t, "message contains 'ok'", resp.WatchdogPattern)
	assert.Equal(t, 60, resp.WatchdogWindow)
	assert.Equal(t, 600, resp.WatchdogCooldown)
	assert.Equal(t, "Service did not recover", resp.WatchdogTriggerMessage)
	assert.Equal(t, "Service recovered", resp.WatchdogClearMessage)
	assert.True(t, resp.AlertQuietEnabled)
	assert.Equal(t, "22:00", resp.AlertQuietStart)
	assert.Equal(t, "07:00", resp.AlertQuietEnd)
	assert.Equal(t, "Europe/Prague", resp.AlertQuietTimezone)
	assert.Equal(t, 4, resp.QuietStackThreshold)
	assert.Equal(t, 600, resp.QuietStackWindow)
	assert.Equal(t, sub.PausedUntil, resp.PausedUntil)
	assert.Equal(t, []string{"mon", "fri"}, resp.DeliveryDays)
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func TestPreviewUniqueKeyUsesFirstCaptureGroup(t *testing.T) {
	key, ok := previewUniqueKey(
		mustCompileRegex(t, `(\d{1,3}(?:\.\d{1,3}){3})`),
		container.LogEvent{RawMessage: "blocked client 192.168.1.20 from /login"},
	)

	assert.True(t, ok)
	assert.Equal(t, "192.168.1.20", key)
}

func TestPreviewUniqueKeyCanUseUnescapedRegexInput(t *testing.T) {
	key, ok := previewUniqueKey(
		mustCompileRegex(t, `timeout after (\d+)ms`),
		container.LogEvent{RawMessage: "request timeout after 2500ms"},
	)

	assert.True(t, ok)
	assert.Equal(t, "2500", key)
}

func mustCompileRegex(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(pattern)
	assert.NoError(t, err)
	return re
}
