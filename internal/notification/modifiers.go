package notification

import (
	"fmt"
	"time"

	"github.com/amir20/dozzle/types"
)

type uniqueDecision struct {
	Allowed          bool
	Key              string
	Count            int
	Threshold        int
	Window           int
	ThresholdReached bool
	Sent             bool
	WindowStart      time.Time
}

func addContext(notification *types.Notification, label, value string) {
	if value == "" {
		return
	}
	notification.Context = append(notification.Context, types.NotificationContext{Label: label, Value: value})
}

func addModifierTag(notification *types.Notification, tag string) {
	notification.NtfyTags = appendUniqueTag(notification.NtfyTags, tag)
}

func applyBurstModifier(notification *types.Notification, sub *Subscription, escalated bool, count int) {
	if !escalated {
		return
	}
	addModifierTag(notification, "burst")
	if sub.BurstNtfyTopic != "" {
		notification.NtfyTopic = sub.BurstNtfyTopic
	}
	addContext(notification, "Burst", fmt.Sprintf("%d hits / %s", count, formatDurationSeconds(sub.BurstWindow)))
	addContext(notification, "Delivery", "burst escalated")
}

func (m *Manager) evaluateUnique(sub *Subscription, containerID, message string) uniqueDecision {
	key, ok := sub.ExtractUniqueKey(message)
	if !ok {
		return uniqueDecision{Allowed: true}
	}

	threshold := sub.UniqueThreshold
	if threshold <= 1 {
		threshold = 1
	}

	fp := alertFingerprint(sub.Name, "", containerID, "unique", key)
	now := time.Now()
	window := time.Duration(sub.UniqueWindow) * time.Second

	if m.queue != nil {
		state, err := m.queue.GetOrCreateUniqueState(fp, sub.ID, key)
		if err != nil {
			return m.evaluateUniqueInMemory(sub, fp, key, threshold, now, window)
		}
		decision := advanceUniqueState(state.Count, state.WindowStart, state.Sent, key, threshold, sub.UniqueWindow, now, window)
		state.Count = decision.Count
		state.WindowStart = decision.WindowStart
		state.Sent = decision.Sent
		_ = m.queue.UpsertUniqueState(state)
		return decision
	}

	return m.evaluateUniqueInMemory(sub, fp, key, threshold, now, window)
}

func (m *Manager) evaluateUniqueInMemory(sub *Subscription, fp, key string, threshold int, now time.Time, window time.Duration) uniqueDecision {
	state, _ := sub.UniqueTrackers.LoadOrCompute(fp, func() (UniqueState, bool) {
		return UniqueState{WindowStart: now}, false
	})
	decision := advanceUniqueState(state.Count, state.WindowStart, state.Sent, key, threshold, sub.UniqueWindow, now, window)
	state.Count = decision.Count
	state.WindowStart = decision.WindowStart
	state.Sent = decision.Sent
	sub.UniqueTrackers.Store(fp, state)
	return decision
}

func advanceUniqueState(count int, windowStart time.Time, sent bool, key string, threshold, windowSeconds int, now time.Time, window time.Duration) uniqueDecision {
	if windowStart.IsZero() || now.After(windowStart.Add(window)) {
		count = 0
		sent = false
		windowStart = now
	}
	count++
	allowed := !sent && count >= threshold
	sent = sent || allowed
	return uniqueDecision{
		Allowed:          allowed,
		Key:              key,
		Count:            count,
		Threshold:        threshold,
		Window:           windowSeconds,
		ThresholdReached: allowed && threshold > 1,
		Sent:             sent,
		WindowStart:      windowStart,
	}
}

func applyUniqueModifier(notification *types.Notification, d uniqueDecision) {
	if d.Key == "" {
		return
	}
	addModifierTag(notification, "unique")
	if d.ThresholdReached {
		addModifierTag(notification, "threshold")
	}
	addContext(notification, "Unique key", d.Key)
	addContext(notification, "Hits", fmt.Sprintf("%d / %s", d.Count, formatDurationSeconds(d.Window)))
	if d.ThresholdReached {
		addContext(notification, "Delivery", fmt.Sprintf("threshold reached (%d hits)", d.Threshold))
	} else {
		addContext(notification, "Delivery", "first unique value in window")
	}
}

func formatDurationSeconds(seconds int) string {
	if seconds <= 0 {
		return "0s"
	}
	d := time.Duration(seconds) * time.Second
	if seconds%(24*60*60) == 0 {
		return fmt.Sprintf("%dd", seconds/(24*60*60))
	}
	if seconds%(60*60) == 0 {
		return fmt.Sprintf("%dh", seconds/(60*60))
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return d.String()
}
