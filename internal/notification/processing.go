package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/amir20/dozzle/internal/container"
	"github.com/amir20/dozzle/internal/notification/dispatcher"
	"github.com/amir20/dozzle/types"
	"github.com/rs/zerolog/log"
)

// processLogEvents processes log events from the listener channel
func (m *Manager) processLogEvents() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case logEvent := <-m.listener.LogChannel():
			if logEvent == nil {
				return
			}
			m.processLogEvent(logEvent)
		}
	}
}

// processLogEvent processes a single log event and sends notifications for matching subscriptions
func (m *Manager) processLogEvent(logEvent *container.LogEvent) {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	c, host, err := m.listener.FindContainerWithHost(ctx, logEvent.ContainerID, nil)
	if err != nil {
		log.Error().Err(err).Str("containerID", logEvent.ContainerID).Msg("Failed to find container")
		return
	}

	if isDozzleContainer(c) {
		return
	}

	notificationContainer := FromContainerModel(c, host)
	notificationLog := FromLogEvent(*logEvent)

	m.subscriptions.Range(func(_ int, sub *Subscription) bool {
		if !sub.Enabled || !sub.IsLogAlert() {
			return true
		}
		if !sub.MatchesContainer(notificationContainer) {
			return true
		}

		// Watchdog mode: trigger starts/resets a timer; resolve pattern cancels it
		if sub.WatchdogWindow > 0 {
			// Resolve: cancel the watchdog timer if the resolve pattern matches
			if sub.WatchdogProgram != nil && sub.MatchesWatchdog(notificationLog) {
				sub.CancelWatchdogTimer(logEvent.ContainerID)
			}
			// Trigger: reset the watchdog timer if the trigger pattern matches
			if sub.MatchesLog(notificationLog) {
				if d, ok := m.getDispatcher(sub.DispatcherID); ok {
					priority := sub.DetectBurst(logEvent.ContainerID, sub.NtfyPriority)
					detail := fmt.Sprintf("Watchdog: no follow-up received within %d seconds", sub.WatchdogWindow)
					if sub.WatchdogPattern == "" {
						detail = fmt.Sprintf("Watchdog: no heartbeat seen for %d seconds", sub.WatchdogWindow)
					}
					notif := types.Notification{
						ID:           fmt.Sprintf("%s-watchdog-%d", c.ID, time.Now().UnixNano()),
						Type:         types.LogNotification,
						Detail:       detail,
						Container:    notificationContainer,
						Log:          &notificationLog,
						NtfyTopic:    sub.NtfyTopic,
						NtfyPriority: priority,
						NtfyTags:     sub.NtfyTags,
						Subscription: types.SubscriptionConfig{
							ID:                  sub.ID,
							Name:                sub.Name,
							Enabled:             sub.Enabled,
							DispatcherID:        sub.DispatcherID,
							LogExpression:       sub.LogExpression,
							ContainerExpression: sub.ContainerExpression,
							NtfyTopic:           sub.NtfyTopic,
							NtfyPriority:        priority,
							NtfyTags:            sub.NtfyTags,
							BypassQuietHours:    sub.BypassQuietHours,
						},
						Timestamp: time.Now(),
					}
					sub.ResetWatchdogTimer(logEvent.ContainerID, func() {
						sub.TriggerCount.Add(1)
						now := time.Now()
						sub.LastTriggeredAt.Store(&now)
						sub.AddTriggeredContainer(notificationContainer.ID)
						m.sendOrQueue(d, notif, sub)
					}, sub.WatchdogWindow)
				}
			}
			return true
		}

		// Normal mode: send immediately when trigger matches
		if !sub.MatchesLog(notificationLog) {
			return true
		}

		// Per-container log cooldown
		if sub.Cooldown > 0 && sub.IsLogCooldownActive(logEvent.ContainerID) {
			return true
		}
		if sub.Cooldown > 0 {
			sub.SetLogCooldown(logEvent.ContainerID)
			if m.queue != nil {
				expiresAt := time.Now().Add(time.Duration(sub.Cooldown) * time.Second)
				key := fmt.Sprintf("%d:%s:log", sub.ID, logEvent.ContainerID)
				if err := m.queue.SetCooldown(key, expiresAt); err != nil {
					log.Debug().Err(err).Msg("Failed to persist log cooldown")
				}
			}
		}

		sub.AddTriggeredContainer(notificationContainer.ID)
		sub.TriggerCount.Add(1)
		now := time.Now()
		sub.LastTriggeredAt.Store(&now)

		log.Debug().Str("containerID", notificationContainer.ID).Interface("log", notificationLog.Message).Msg("Matched subscription")

		priority := sub.NtfyPriority
		priority = sub.DetectBurst(logEvent.ContainerID, priority)

		notification := types.Notification{
			ID:           fmt.Sprintf("%s-%d", c.ID, time.Now().UnixNano()),
			Type:         types.LogNotification,
			Detail:       formatLogMessage(notificationLog.Message),
			Container:    notificationContainer,
			Log:          &notificationLog,
			NtfyTopic:    sub.NtfyTopic,
			NtfyPriority: priority,
			NtfyTags:     sub.NtfyTags,
			Subscription: types.SubscriptionConfig{
				ID:                  sub.ID,
				Name:                sub.Name,
				Enabled:             sub.Enabled,
				DispatcherID:        sub.DispatcherID,
				LogExpression:       sub.LogExpression,
				ContainerExpression: sub.ContainerExpression,
				NtfyTopic:           sub.NtfyTopic,
				NtfyPriority:        priority,
				NtfyTags:            sub.NtfyTags,
				BypassQuietHours:    sub.BypassQuietHours,
			},
			Timestamp: time.Now(),
		}

		if d, ok := m.getDispatcher(sub.DispatcherID); ok {
			m.sendOrQueue(d, notification, sub)
		}
		return true
	})
}

// processStatEvents processes stat events from the stats listener channel
func (m *Manager) processStatEvents() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case event, ok := <-m.statsListener.Channel():
			if !ok || event == nil {
				return
			}
			m.processStatEvent(event)
		}
	}
}

// processStatEvent processes a single stat event and sends notifications for matching metric subscriptions
func (m *Manager) processStatEvent(event *ContainerStatEvent) {
	notificationStat := types.NotificationStat{
		CPUPercent:    event.Stat.CPUPercent,
		MemoryPercent: event.Stat.MemoryPercent,
		MemoryUsage:   event.Stat.MemoryUsage,
		Mounts:        FromContainerMounts(event.Container),
	}

	notificationContainer := FromContainerModel(event.Container, event.Host)

	m.subscriptions.Range(func(_ int, sub *Subscription) bool {
		// Skip disabled or non-metric subscriptions
		if !sub.Enabled || !sub.IsMetricAlert() {
			return true
		}

		// Check container filter first
		if !sub.MatchesContainer(notificationContainer) {
			return true
		}

		// Evaluate metric expression and record in sample window
		matched := sub.MatchesMetric(notificationStat)
		if !sub.RecordMetricSample(event.Stat.ID, matched) {
			return true
		}

		// Check per-container cooldown
		if sub.IsMetricCooldownActive(event.Stat.ID) {
			return true
		}

		// Set cooldown and update stats
		sub.SetMetricCooldown(event.Stat.ID)
		if m.queue != nil && sub.Cooldown > 0 {
			expiresAt := time.Now().Add(time.Duration(sub.Cooldown) * time.Second)
			key := fmt.Sprintf("%d:%s:metric", sub.ID, event.Stat.ID)
			if err := m.queue.SetCooldown(key, expiresAt); err != nil {
				log.Debug().Err(err).Msg("Failed to persist metric cooldown")
			}
		}
		sub.AddTriggeredContainer(event.Stat.ID)
		sub.TriggerCount.Add(1)
		now := time.Now()
		sub.LastTriggeredAt.Store(&now)

		log.Debug().
			Str("containerID", event.Stat.ID).
			Float64("cpu", event.Stat.CPUPercent).
			Float64("memory", event.Stat.MemoryPercent).
			Str("subscription", sub.Name).
			Msg("Metric alert triggered")

		priority := sub.DetectBurst(event.Stat.ID, sub.NtfyPriority)

		notification := types.Notification{
			ID:           fmt.Sprintf("%s-metric-%d", event.Stat.ID, time.Now().UnixNano()),
			Type:         types.MetricNotification,
			Detail:       fmt.Sprintf("CPU: %.1f%%, Memory: %.1f%%", notificationStat.CPUPercent, notificationStat.MemoryPercent),
			Container:    notificationContainer,
			Stat:         &notificationStat,
			NtfyTopic:    sub.NtfyTopic,
			NtfyPriority: priority,
			NtfyTags:     sub.NtfyTags,
			Subscription: types.SubscriptionConfig{
				ID:                  sub.ID,
				Name:                sub.Name,
				Enabled:             sub.Enabled,
				DispatcherID:        sub.DispatcherID,
				MetricExpression:    sub.MetricExpression,
				ContainerExpression: sub.ContainerExpression,
				Cooldown:            sub.Cooldown,
				SampleWindow:        sub.SampleWindow,
				NtfyTopic:           sub.NtfyTopic,
				NtfyPriority:        priority,
				NtfyTags:            sub.NtfyTags,
				BypassQuietHours:    sub.BypassQuietHours,
			},
			Timestamp: time.Now(),
		}

		if d, ok := m.getDispatcher(sub.DispatcherID); ok {
			m.sendOrQueue(d, notification, sub)
		}
		return true
	})
}

// processDockerEvents processes Docker events from the event listener channel
func (m *Manager) processDockerEvents() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case event, ok := <-m.eventListener.Channel():
			if !ok || event == nil {
				return
			}
			m.processDockerEvent(event)
		}
	}
}

// processDockerEvent processes a single Docker event and sends notifications for matching event subscriptions
func (m *Manager) processDockerEvent(event *ContainerEventEntry) {
	notificationContainer := FromContainerModel(event.Container, event.Host)
	notificationEvent := types.NotificationEvent{
		Name:       event.Event.Name,
		ActorID:    event.Event.ActorID,
		Attributes: event.Event.ActorAttributes,
		Timestamp:  event.Event.Time,
	}

	m.subscriptions.Range(func(_ int, sub *Subscription) bool {
		if !sub.Enabled || !sub.IsEventAlert() {
			return true
		}

		if !sub.MatchesContainer(notificationContainer) {
			return true
		}

		if !sub.MatchesEvent(notificationEvent) {
			return true
		}

		if sub.IsEventCooldownActive(event.Event.ActorID) {
			return true
		}

		if sub.Cooldown > 0 {
			sub.SetEventCooldown(event.Event.ActorID)
			if m.queue != nil {
				expiresAt := time.Now().Add(time.Duration(sub.Cooldown) * time.Second)
				key := fmt.Sprintf("%d:%s:event", sub.ID, event.Event.ActorID)
				if err := m.queue.SetCooldown(key, expiresAt); err != nil {
					log.Debug().Err(err).Msg("Failed to persist event cooldown")
				}
			}
		}

		sub.AddTriggeredContainer(event.Event.ActorID)
		sub.TriggerCount.Add(1)
		now := time.Now()
		sub.LastTriggeredAt.Store(&now)

		log.Debug().
			Str("containerID", event.Event.ActorID).
			Str("event", event.Event.Name).
			Str("subscription", sub.Name).
			Msg("Event alert triggered")

		detail := fmt.Sprintf("Container event: %s", event.Event.Name)
		if exitCode, ok := event.Event.ActorAttributes["exitCode"]; ok && event.Event.Name == "die" {
			detail = fmt.Sprintf("Container event: %s (exit code %s)", event.Event.Name, exitCode)
		}

		priority := sub.DetectBurst(event.Event.ActorID, sub.NtfyPriority)

		notification := types.Notification{
			ID:           fmt.Sprintf("%s-event-%d", event.Event.ActorID, time.Now().UnixNano()),
			Type:         types.EventNotification,
			Detail:       detail,
			Container:    notificationContainer,
			Event:        &notificationEvent,
			NtfyTopic:    sub.NtfyTopic,
			NtfyPriority: priority,
			NtfyTags:     sub.NtfyTags,
			Subscription: types.SubscriptionConfig{
				ID:                  sub.ID,
				Name:                sub.Name,
				Enabled:             sub.Enabled,
				DispatcherID:        sub.DispatcherID,
				EventExpression:     sub.EventExpression,
				ContainerExpression: sub.ContainerExpression,
				Cooldown:            sub.Cooldown,
				NtfyTopic:           sub.NtfyTopic,
				NtfyPriority:        priority,
				NtfyTags:            sub.NtfyTags,
				BypassQuietHours:    sub.BypassQuietHours,
			},
			Timestamp: time.Now(),
		}

		if d, ok := m.getDispatcher(sub.DispatcherID); ok {
			m.sendOrQueue(d, notification, sub)
		}
		return true
	})
}

func formatLogMessage(message any) string {
	switch v := message.(type) {
	case string:
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// sendOrQueue decides whether to send immediately, queue for quiet hours, or hold for the clear window.
func (m *Manager) sendOrQueue(d dispatcher.Dispatcher, notification types.Notification, sub *Subscription) {
	// Hold/clear window: delay delivery; deliver via queue after HoldClearWindow seconds
	if sub.HoldClearWindow > 0 && m.queue != nil {
		deliverAt := time.Now().Add(time.Duration(sub.HoldClearWindow) * time.Second)
		if err := m.queue.Enqueue(notification, deliverAt); err != nil {
			log.Warn().Err(err).Msg("Failed to enqueue hold/clear notification")
		}
		return
	}

	// Quiet hours handling
	if m.isQuietHours() && !sub.BypassQuietHours {
		if sub.HoldDuringQuiet && m.queue != nil {
			deliverAt := m.nextQuietEnd()
			if err := m.queue.Enqueue(notification, deliverAt); err != nil {
				log.Warn().Err(err).Msg("Failed to enqueue quiet-hours notification")
			}
			return
		}
		// Downgrade priority
		if sub.QuietPriority > 0 {
			notification.NtfyPriority = sub.QuietPriority
		}
	}

	go m.sendWithRetry(d, notification, sub.DispatcherID)
}

// sendWithRetry sends a notification with up to 3 attempts and quadratic backoff (30s, 120s).
func (m *Manager) sendWithRetry(d dispatcher.Dispatcher, notification types.Notification, dispatcherID int) {
	acquireCtx, acquireCancel := context.WithTimeout(m.ctx, time.Minute)
	defer acquireCancel()
	if err := m.sendSem.Acquire(acquireCtx, 1); err != nil {
		log.Warn().Err(err).Int("dispatcher", dispatcherID).Msg("Notification dropped: too many pending")
		return
	}
	defer m.sendSem.Release(1)

	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		err := d.Send(ctx, notification)
		cancel()
		if err == nil {
			return
		}
		if attempt < 3 {
			backoff := time.Duration(attempt*attempt) * 30 * time.Second
			log.Warn().Err(err).Int("attempt", attempt).Int("dispatcher", dispatcherID).
				Dur("backoff", backoff).Msg("Notification send failed, retrying")
			select {
			case <-m.ctx.Done():
				return
			case <-time.After(backoff):
			}
		} else {
			log.Error().Err(err).Int("dispatcher", dispatcherID).Msg("Notification failed after 3 attempts")
		}
	}
}
