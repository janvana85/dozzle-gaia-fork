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
			if m.logStoreCh != nil {
				select {
				case m.logStoreCh <- logEvent:
				default:
				}
			}
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
		if !m.isSubscriptionProcessable(sub) || !sub.IsLogAlert() {
			return true
		}
		if !sub.MatchesContainer(notificationContainer) {
			return true
		}

		// Watchdog mode: trigger starts/resets a timer; resolve pattern cancels it
		if sub.WatchdogWindow > 0 {
			// Resolve: cancel the watchdog timer if the resolve pattern matches, optionally send clear message
			if sub.WatchdogProgram != nil && sub.MatchesWatchdog(notificationLog) {
				wasActive := sub.CancelWatchdogTimer(logEvent.ContainerID)
				if wasActive && sub.WatchdogClearMessage != "" {
					if m.shouldAggregateQuiet(sub) {
						return true
					}
					if d, ok := m.getDispatcher(sub.DispatcherID); ok {
						clearNotif := types.Notification{
							ID:           fmt.Sprintf("%s-watchdog-clear-%d", c.ID, time.Now().UnixNano()),
							Type:         types.LogNotification,
							Detail:       sub.WatchdogClearMessage,
							Container:    notificationContainer,
							Log:          &notificationLog,
							NtfyTopic:    sub.NtfyTopic,
							NtfyPriority: sub.NtfyPriority,
							NtfyTags:     sub.NtfyTags,
							Subscription: types.SubscriptionConfig{
								ID:                  sub.ID,
								Name:                sub.Name,
								Enabled:             sub.Enabled,
								DispatcherID:        sub.DispatcherID,
								LogExpression:       sub.LogExpression,
								ContainerExpression: sub.ContainerExpression,
								NtfyTopic:           sub.NtfyTopic,
								NtfyPriority:        sub.NtfyPriority,
								NtfyTags:            sub.NtfyTags,
								BypassQuietHours:    sub.BypassQuietHours,
							},
							Timestamp: time.Now(),
						}
						m.sendOrQueue(d, clearNotif, sub)
					}
				}
			}
			// Trigger: reset the watchdog timer if the trigger pattern matches
			if sub.MatchesLog(notificationLog) {
				if d, ok := m.getDispatcher(sub.DispatcherID); ok {
					priority, burstEscalated, burstCount := sub.DetectBurst(logEvent.ContainerID, sub.NtfyPriority)
					detail := sub.WatchdogTriggerMessage
					if detail == "" {
						if sub.WatchdogPattern != "" {
							detail = fmt.Sprintf("Watchdog: no follow-up received within %d seconds", sub.WatchdogWindow)
						} else {
							detail = fmt.Sprintf("Watchdog: no heartbeat seen for %d seconds", sub.WatchdogWindow)
						}
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
					applyBurstModifier(&notif, sub, burstEscalated, burstCount)
					sub.ResetWatchdogTimer(logEvent.ContainerID, func() {
						if sub.IsWatchdogCooldownActive(logEvent.ContainerID) {
							return
						}
						sub.SetWatchdogCooldown(logEvent.ContainerID)
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

		unique := m.evaluateUnique(sub, logEvent.ContainerID, formatLogMessage(notificationLog.Message))
		if !unique.Allowed {
			return true
		}
		if m.shouldAggregateQuiet(sub) {
			notification := summaryNotification(sub, types.LogNotification, formatLogMessage(notificationLog.Message), notificationContainer)
			notification.Log = &notificationLog
			if err := m.aggregateQuietNotification(notification, sub); err != nil {
				log.Warn().Err(err).Msg("Failed to aggregate quiet-hours log hit")
			}
			sub.AddTriggeredContainer(notificationContainer.ID)
			sub.TriggerCount.Add(1)
			now := time.Now()
			sub.LastTriggeredAt.Store(&now)
			return true
		}

		// Per-container log cooldown
		if sub.Cooldown > 0 && sub.IsLogCooldownActive(logEvent.ContainerID) {
			log.Debug().
				Int("subscription_id", sub.ID).
				Str("subscription", sub.Name).
				Str("container_id", notificationContainer.ID).
				Str("container", notificationContainer.Name).
				Int("cooldown_seconds", sub.Cooldown).
				Str("reason", "cooldown-active").
				Msg("Skipping log alert due to cooldown")
			return true
		}
		if sub.Cooldown > 0 {
			sub.SetLogCooldown(logEvent.ContainerID)
			if m.queue != nil {
				expiresAt := time.Now().Add(time.Duration(sub.GetCooldownSeconds()) * time.Second)
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

		notificationID := fmt.Sprintf("%s-%d", c.ID, time.Now().UnixNano())

		log.Info().
			Str("alert_type", string(types.LogNotification)).
			Str("action", "hit").
			Int("subscription_id", sub.ID).
			Str("subscription", sub.Name).
			Str("container_id", notificationContainer.ID).
			Str("container", notificationContainer.Name).
			Str("notification_id", notificationID).
			Interface("log", notificationLog.Message).
			Msg("Alert hit")

		priority := sub.NtfyPriority
		priority, burstEscalated, burstCount := sub.DetectBurst(logEvent.ContainerID, priority)

		notification := types.Notification{
			ID:           notificationID,
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
		applyUniqueModifier(&notification, unique)
		applyBurstModifier(&notification, sub, burstEscalated, burstCount)

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
	// Normalize CPU by core count so alerts report overall load (0-100%),
	// matching the UI. Stat.CPUPercent is per-core (100% = one full core).
	cores := event.Container.CPULimit
	if cores <= 0 {
		cores = float64(event.Host.NCPU)
	}
	if cores <= 0 {
		cores = 1
	}

	notificationStat := types.NotificationStat{
		CPUPercent:    event.Stat.CPUPercent / cores,
		MemoryPercent: event.Stat.MemoryPercent,
		MemoryUsage:   event.Stat.MemoryUsage,
		Mounts:        FromContainerMounts(event.Container),
	}

	notificationContainer := FromContainerModel(event.Container, event.Host)

	m.subscriptions.Range(func(_ int, sub *Subscription) bool {
		// Skip disabled or non-metric subscriptions
		if !m.isSubscriptionProcessable(sub) || !sub.IsMetricAlert() {
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

		detail := fmt.Sprintf("CPU: %.1f%%, Memory: %.1f%%", notificationStat.CPUPercent, notificationStat.MemoryPercent)
		unique := m.evaluateUnique(sub, event.Stat.ID, detail)
		if !unique.Allowed {
			return true
		}
		if m.shouldAggregateQuiet(sub) {
			notification := summaryNotification(sub, types.MetricNotification, detail, notificationContainer)
			notification.Stat = &notificationStat
			if err := m.aggregateQuietNotification(notification, sub); err != nil {
				log.Warn().Err(err).Msg("Failed to aggregate quiet-hours metric hit")
			}
			sub.AddTriggeredContainer(event.Stat.ID)
			sub.TriggerCount.Add(1)
			now := time.Now()
			sub.LastTriggeredAt.Store(&now)
			return true
		}

		// Check per-container cooldown
		if sub.IsMetricCooldownActive(event.Stat.ID) {
			return true
		}

		// Set cooldown and update stats
		sub.SetMetricCooldown(event.Stat.ID)
		if m.queue != nil && sub.Cooldown > 0 {
			expiresAt := time.Now().Add(time.Duration(sub.GetCooldownSeconds()) * time.Second)
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
			Float64("cpu", notificationStat.CPUPercent).
			Float64("memory", notificationStat.MemoryPercent).
			Str("subscription", sub.Name).
			Msg("Metric alert triggered")

		priority, burstEscalated, burstCount := sub.DetectBurst(event.Stat.ID, sub.NtfyPriority)

		notification := types.Notification{
			ID:           fmt.Sprintf("%s-metric-%d", event.Stat.ID, time.Now().UnixNano()),
			Type:         types.MetricNotification,
			Detail:       detail,
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
		applyUniqueModifier(&notification, unique)
		applyBurstModifier(&notification, sub, burstEscalated, burstCount)

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
		if !m.isSubscriptionProcessable(sub) || !sub.IsEventAlert() {
			return true
		}

		if !sub.MatchesContainer(notificationContainer) {
			return true
		}

		if !sub.MatchesEvent(notificationEvent) {
			return true
		}

		if sub.WatchdogWindow > 0 {
			// Clear: cancel timer if clear pattern matches.
			if sub.WatchdogEventProgram != nil && sub.MatchesWatchdogEvent(notificationEvent) {
				wasActive := sub.CancelWatchdogTimer(event.Event.ActorID)
				if wasActive && sub.WatchdogClearMessage != "" {
					if m.shouldAggregateQuiet(sub) {
						return true
					}
					if d, ok := m.getDispatcher(sub.DispatcherID); ok {
						clearNotif := types.Notification{
							ID:           fmt.Sprintf("%s-watchdog-clear-%d", event.Event.ActorID, time.Now().UnixNano()),
							Type:         types.EventNotification,
							Detail:       sub.WatchdogClearMessage,
							Container:    notificationContainer,
							Event:        &notificationEvent,
							NtfyTopic:    sub.NtfyTopic,
							NtfyPriority: sub.NtfyPriority,
							NtfyTags:     sub.NtfyTags,
							Subscription: types.SubscriptionConfig{
								ID:                  sub.ID,
								Name:                sub.Name,
								Enabled:             sub.Enabled,
								DispatcherID:        sub.DispatcherID,
								EventExpression:     sub.EventExpression,
								ContainerExpression: sub.ContainerExpression,
								NtfyTopic:           sub.NtfyTopic,
								NtfyPriority:        sub.NtfyPriority,
								NtfyTags:            sub.NtfyTags,
								BypassQuietHours:    sub.BypassQuietHours,
							},
							Timestamp: time.Now(),
						}
						m.sendOrQueue(d, clearNotif, sub)
					}
				}
				return true
			}

			// Trigger: reset watchdog timer on matching event.
			if d, ok := m.getDispatcher(sub.DispatcherID); ok {
				triggerEvent := notificationEvent
				priority, burstEscalated, burstCount := sub.DetectBurst(event.Event.ActorID, sub.NtfyPriority)
				detail := sub.WatchdogTriggerMessage
				if detail == "" {
					detail = fmt.Sprintf("Watchdog: no follow-up received within %d seconds", sub.WatchdogWindow)
				}
				notif := types.Notification{
					ID:           fmt.Sprintf("%s-watchdog-%d", event.Event.ActorID, time.Now().UnixNano()),
					Type:         types.EventNotification,
					Detail:       detail,
					Container:    notificationContainer,
					Event:        &triggerEvent,
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
						NtfyTopic:           sub.NtfyTopic,
						NtfyPriority:        priority,
						NtfyTags:            sub.NtfyTags,
						BypassQuietHours:    sub.BypassQuietHours,
					},
					Timestamp: time.Now(),
				}
				applyBurstModifier(&notif, sub, burstEscalated, burstCount)
				sub.ResetWatchdogTimer(event.Event.ActorID, func() {
					if sub.IsWatchdogCooldownActive(event.Event.ActorID) {
						return
					}
					sub.SetWatchdogCooldown(event.Event.ActorID)
					sub.TriggerCount.Add(1)
					now := time.Now()
					sub.LastTriggeredAt.Store(&now)
					sub.AddTriggeredContainer(notificationContainer.ID)
					m.sendOrQueue(d, notif, sub)
				}, sub.WatchdogWindow)
				return true
			}
		}

		if sub.RestartLoopEnabled {
			m.processRestartLoop(sub, event, notificationContainer, notificationEvent)
			return true
		}

		detail := fmt.Sprintf("Container event: %s", event.Event.Name)
		if exitCode, ok := event.Event.ActorAttributes["exitCode"]; ok && event.Event.Name == "die" {
			detail = fmt.Sprintf("Container event: %s (exit code %s)", event.Event.Name, exitCode)
		}

		unique := m.evaluateUnique(sub, event.Event.ActorID, detail)
		if !unique.Allowed {
			return true
		}
		if m.shouldAggregateQuiet(sub) {
			notification := summaryNotification(sub, types.EventNotification, detail, notificationContainer)
			notification.Event = &notificationEvent
			if err := m.aggregateQuietNotification(notification, sub); err != nil {
				log.Warn().Err(err).Msg("Failed to aggregate quiet-hours event hit")
			}
			sub.AddTriggeredContainer(event.Event.ActorID)
			sub.TriggerCount.Add(1)
			now := time.Now()
			sub.LastTriggeredAt.Store(&now)
			return true
		}

		if sub.IsEventCooldownActive(event.Event.ActorID) {
			return true
		}

		if sub.Cooldown > 0 {
			sub.SetEventCooldown(event.Event.ActorID)
			if m.queue != nil {
				expiresAt := time.Now().Add(time.Duration(sub.GetCooldownSeconds()) * time.Second)
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

		notificationID := fmt.Sprintf("%s-event-%d", event.Event.ActorID, time.Now().UnixNano())

		log.Info().
			Str("alert_type", string(types.EventNotification)).
			Str("action", "hit").
			Int("subscription_id", sub.ID).
			Str("subscription", sub.Name).
			Str("container_id", event.Event.ActorID).
			Str("container", notificationContainer.Name).
			Str("notification_id", notificationID).
			Str("event", event.Event.Name).
			Msg("Alert hit")

		priority, burstEscalated, burstCount := sub.DetectBurst(event.Event.ActorID, sub.NtfyPriority)

		notification := types.Notification{
			ID:           notificationID,
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
		applyUniqueModifier(&notification, unique)
		applyBurstModifier(&notification, sub, burstEscalated, burstCount)

		if d, ok := m.getDispatcher(sub.DispatcherID); ok {
			m.sendOrQueue(d, notification, sub)
		}
		return true
	})
}

func (m *Manager) processRestartLoop(sub *Subscription, event *ContainerEventEntry, notificationContainer types.NotificationContainer, notificationEvent types.NotificationEvent) {
	containerID := event.Event.ActorID
	now := time.Now()

	if sub.IsRestartLoopCooldownActive(containerID) {
		return
	}

	if sub.RestartLoopStateWindow > 0 {
		if event.Container.State == "restarting" {
			if _, ok := sub.RestartLoopFirstSeenAt.Load(containerID); !ok {
				firstSeen := now
				sub.RestartLoopFirstSeenAt.Store(containerID, firstSeen)
				timer := time.AfterFunc(time.Duration(sub.RestartLoopStateWindow)*time.Second, func() {
					sub.RestartLoopTimers.Delete(containerID)
					first, ok := sub.RestartLoopFirstSeenAt.Load(containerID)
					if !ok || time.Since(first) < time.Duration(sub.RestartLoopStateWindow)*time.Second {
						return
					}
					if sub.IsRestartLoopCooldownActive(containerID) {
						return
					}
					if current, ok := sub.RestartLoopFirstSeenAt.Load(containerID); !ok || current != first {
						return
					}
					m.fireRestartLoopAlert(sub, containerID, notificationContainer, notificationEvent, "restarting state persisted")
				})
				sub.RestartLoopTimers.Store(containerID, timer)
			}
		} else {
			if sub.CancelRestartLoopTimer(containerID) {
				return
			}
		}
	}

	if sub.RestartLoopEventCount > 0 && sub.RestartLoopEventWindow > 0 && event.Event.Name == "restart" {
		window := time.Duration(sub.RestartLoopEventWindow) * time.Second
		cutoff := now.Add(-window)
		streaks, _ := sub.RestartLoopRestartStreaks.LoadOrCompute(containerID, func() ([]time.Time, bool) {
			return []time.Time{}, false
		})
		pruned := streaks[:0]
		for _, ts := range streaks {
			if ts.After(cutoff) {
				pruned = append(pruned, ts)
			}
		}
		pruned = append(pruned, now)
		sub.RestartLoopRestartStreaks.Store(containerID, pruned)
		if len(pruned) >= sub.RestartLoopEventCount {
			m.fireRestartLoopAlert(sub, containerID, notificationContainer, notificationEvent, "restart events exceeded threshold")
		}
	}
}

func (m *Manager) fireRestartLoopAlert(sub *Subscription, containerID string, notificationContainer types.NotificationContainer, notificationEvent types.NotificationEvent, detail string) {
	if !m.isSubscriptionProcessable(sub) {
		return
	}
	if sub.IsRestartLoopCooldownActive(containerID) {
		return
	}
	sub.SetRestartLoopCooldown(containerID)
	sub.AddTriggeredContainer(containerID)
	sub.TriggerCount.Add(1)
	now := time.Now()
	sub.LastTriggeredAt.Store(&now)

	priority, burstEscalated, burstCount := sub.DetectBurst(containerID, sub.NtfyPriority)
	notificationDetail := detail
	if sub.RestartLoopTriggerMessage != "" {
		notificationDetail = sub.RestartLoopTriggerMessage
	}
	notification := types.Notification{
		ID:           fmt.Sprintf("%s-restart-loop-%d", containerID, time.Now().UnixNano()),
		Type:         types.EventNotification,
		Detail:       notificationDetail,
		Container:    notificationContainer,
		Event:        &notificationEvent,
		NtfyTopic:    sub.NtfyTopic,
		NtfyPriority: priority,
		NtfyTags:     sub.NtfyTags,
		Subscription: types.SubscriptionConfig{
			ID:                        sub.ID,
			Name:                      sub.Name,
			Enabled:                   sub.Enabled,
			DispatcherID:              sub.DispatcherID,
			EventExpression:           sub.EventExpression,
			ContainerExpression:       sub.ContainerExpression,
			Cooldown:                  sub.Cooldown,
			NtfyTopic:                 sub.NtfyTopic,
			NtfyPriority:              priority,
			NtfyTags:                  sub.NtfyTags,
			BypassQuietHours:          sub.BypassQuietHours,
			RestartLoopEnabled:        sub.RestartLoopEnabled,
			RestartLoopStateWindow:    sub.RestartLoopStateWindow,
			RestartLoopEventCount:     sub.RestartLoopEventCount,
			RestartLoopEventWindow:    sub.RestartLoopEventWindow,
			RestartLoopCooldown:       sub.RestartLoopCooldown,
			RestartLoopTriggerMessage: sub.RestartLoopTriggerMessage,
		},
		Timestamp: time.Now(),
	}
	applyBurstModifier(&notification, sub, burstEscalated, burstCount)

	log.Info().
		Str("alert_type", string(types.EventNotification)).
		Str("action", "hit").
		Int("subscription_id", sub.ID).
		Str("subscription", sub.Name).
		Str("container_id", containerID).
		Str("container", notificationContainer.Name).
		Str("notification_id", notification.ID).
		Str("reason", detail).
		Msg("Alert hit")

	if d, ok := m.getDispatcher(sub.DispatcherID); ok {
		m.sendOrQueue(d, notification, sub)
	}
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

func appendUniqueTag(tags []string, tag string) []string {
	for _, existing := range tags {
		if existing == tag {
			return tags
		}
	}
	return append(append([]string(nil), tags...), tag)
}

// effectiveQuietHours returns whether we're currently in quiet hours for this subscription,
// and the time when quiet hours end. Per-alert config takes precedence over global.
func (m *Manager) effectiveQuietHours(sub *Subscription) (inQuiet bool, quietEnd time.Time) {
	if sub.BypassQuietHours {
		return false, time.Time{}
	}
	if inQ, effective := m.isAlertQuietHours(sub); effective {
		return inQ, m.nextAlertQuietEnd(sub)
	}
	return m.isQuietHours(), m.nextQuietEnd()
}

// sendOrQueue decides whether to send immediately, queue for quiet-hours/hold-window, or suppress.
func (m *Manager) sendOrQueue(d dispatcher.Dispatcher, notification types.Notification, sub *Subscription, burstEscalated ...bool) {
	inQuiet, quietEnd := m.effectiveQuietHours(sub)

	if sub.BypassQuietHours {
		log.Info().
			Str("alert_type", string(types.LogNotification)).
			Str("action", "send_now").
			Int("subscription_id", sub.ID).
			Str("subscription", sub.Name).
			Str("container_id", notification.Container.ID).
			Str("container", notification.Container.Name).
			Str("notification_id", notification.ID).
			Str("reason", "bypass-quiet-hours").
			Msg("Notification send requested")
	}

	if inQuiet {
		addModifierTag(&notification, "quiet-hours")

		if m.queue != nil {
			if m.shouldAggregateQuiet(sub) {
				if err := m.aggregateQuietNotification(notification, sub); err != nil {
					log.Warn().Err(err).Msg("Failed to aggregate quiet-hours notification")
				}
				return
			}
			if err := m.applyQuietHoursBurstEscalation(&notification, sub); err != nil {
				log.Warn().Err(err).Msg("Failed to evaluate quiet-hours burst")
			}
			pendingCount, err := m.queue.PendingCountForSubscription(sub.ID)
			if err != nil {
				log.Debug().Err(err).Msg("Failed to count pending quiet-hours notifications")
				pendingCount = 0
			}
			deliverAt := quietEnd.Add(time.Duration(pendingCount) * 2 * time.Second)
			addContext(&notification, "Delivery", "held during quiet hours")
			addContext(&notification, "Queued until", deliverAt.Format(time.RFC3339))
			log.Info().
				Str("alert_type", string(notification.Type)).
				Str("action", "queued").
				Int("subscription_id", sub.ID).
				Str("subscription", sub.Name).
				Str("container_id", notification.Container.ID).
				Str("container", notification.Container.Name).
				Str("notification_id", notification.ID).
				Time("deliver_at", deliverAt).
				Str("reason", "quiet-hours").
				Msg("Notification queued")
			if err := m.queue.Enqueue(notification, deliverAt); err != nil {
				log.Warn().Err(err).Msg("Failed to enqueue quiet-hours notification")
			}
			return
		}

		// Fallback: no queue, downgrade priority
		if sub.QuietPriority > 0 {
			notification.NtfyPriority = sub.QuietPriority
		}
		addContext(&notification, "Delivery", "quiet hours active; sent without queue")
		log.Info().
			Str("alert_type", string(types.EventNotification)).
			Str("action", "send_now").
			Int("subscription_id", sub.ID).
			Str("subscription", sub.Name).
			Str("container_id", notification.Container.ID).
			Str("container", notification.Container.Name).
			Str("notification_id", notification.ID).
			Int("priority", notification.NtfyPriority).
			Str("reason", "quiet-hours-no-queue").
			Msg("Notification send requested")
	}

	// Hold window only applies after quiet-hours policy has allowed the alert.
	if sub.HoldClearWindow > 0 && m.queue != nil {
		deliverAt := time.Now().Add(time.Duration(sub.HoldClearWindow) * time.Second)
		addContext(&notification, "Delivery", "legacy hold window")
		addContext(&notification, "Queued until", deliverAt.Format(time.RFC3339))
		log.Info().
			Str("alert_type", string(types.LogNotification)).
			Str("action", "queued").
			Int("subscription_id", sub.ID).
			Str("subscription", sub.Name).
			Str("container_id", notification.Container.ID).
			Str("container", notification.Container.Name).
			Str("notification_id", notification.ID).
			Time("deliver_at", deliverAt).
			Str("reason", "hold-window").
			Msg("Notification queued")
		if err := m.queue.Enqueue(notification, deliverAt); err != nil {
			log.Warn().Err(err).Msg("Failed to enqueue hold-window notification")
		}
		return
	}

	go m.sendWithRetry(d, notification, sub.DispatcherID)
}

func (m *Manager) applyQuietHoursBurstEscalation(notification *types.Notification, sub *Subscription) error {
	qh := m.GetQuietHours()

	threshold := qh.StackThreshold
	if threshold <= 0 {
		threshold = 3
	}
	if sub.QuietStackThreshold > 0 {
		threshold = sub.QuietStackThreshold
	}

	stackWindowSec := qh.StackWindow
	if stackWindowSec <= 0 {
		stackWindowSec = 900
	}
	if sub.QuietStackWindow > 0 {
		stackWindowSec = sub.QuietStackWindow
	}

	stackedPriority := qh.StackedPriority
	if stackedPriority <= 0 {
		stackedPriority = 4
	}

	fp := alertFingerprint(
		sub.Name,
		notification.Container.HostName,
		notification.Container.Name,
		string(notification.Type),
		notification.Detail,
	)

	state, err := m.queue.GetOrCreateAlertState(fp, sub.ID,
		notification.Container.HostName, notification.Container.Name, string(notification.Type))
	if err != nil {
		return err
	}

	now := time.Now()
	windowExpired := now.After(state.WindowStart.Add(time.Duration(stackWindowSec) * time.Second))
	if windowExpired {
		state.Count = 0
		state.StackedSent = false
		state.WindowStart = now
	}

	state.Count++
	if state.Count >= threshold && !state.StackedSent {
		state.StackedSent = true
		notification.NtfyPriority = stackedPriority
		if qh.StackedUsesQuietTopic && qh.QuietTopic != "" {
			notification.NtfyTopic = qh.QuietTopic
		}
		addContext(notification, "Quiet-hours stack", fmt.Sprintf("%d held alerts / %s", state.Count, formatDurationSeconds(stackWindowSec)))
	}

	if err := m.queue.UpsertAlertState(state); err != nil {
		return err
	}

	log.Debug().
		Str("fingerprint", fp).
		Int("count", state.Count).
		Int("threshold", threshold).
		Str("subscription", sub.Name).
		Msg("Alert held during quiet hours")
	return nil
}

// sendWithRetry sends a notification with up to 3 attempts and quadratic backoff (30s, 120s).
func (m *Manager) sendWithRetry(d dispatcher.Dispatcher, notification types.Notification, dispatcherID int) {
	if !m.isSubscriptionActive(notification.Subscription.ID) {
		log.Info().
			Str("alert_type", string(types.EventNotification)).
			Str("action", "drop").
			Str("reason", "subscription-inactive").
			Str("notification_id", notification.ID).
			Int("subscription_id", notification.Subscription.ID).
			Msg("Notification dropped")
		return
	}

	acquireCtx, acquireCancel := context.WithTimeout(m.ctx, time.Minute)
	defer acquireCancel()
	if err := m.sendSem.Acquire(acquireCtx, 1); err != nil {
		log.Warn().Err(err).Int("dispatcher", dispatcherID).Msg("Notification dropped: too many pending")
		return
	}
	defer m.sendSem.Release(1)

	for attempt := 1; attempt <= 3; attempt++ {
		if !m.isSubscriptionActive(notification.Subscription.ID) {
			log.Info().
				Str("alert_type", string(notification.Type)).
				Str("action", "drop").
				Str("reason", "subscription-inactive").
				Str("notification_id", notification.ID).
				Int("subscription_id", notification.Subscription.ID).
				Msg("Notification dropped")
			return
		}
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		err := d.Send(ctx, notification)
		cancel()
		if err == nil {
			log.Info().
				Str("alert_type", string(notification.Type)).
				Str("action", "sent").
				Int("dispatcher_id", dispatcherID).
				Str("notification_id", notification.ID).
				Int("subscription_id", notification.Subscription.ID).
				Str("subscription", notification.Subscription.Name).
				Str("container_id", notification.Container.ID).
				Str("container", notification.Container.Name).
				Msg("Notification sent")
			return
		}
		if attempt < 3 {
			backoff := time.Duration(attempt*attempt) * 30 * time.Second
			log.Warn().Err(err).Str("action", "retry").Int("attempt", attempt).Int("dispatcher_id", dispatcherID).
				Str("notification_id", notification.ID).
				Int("subscription_id", notification.Subscription.ID).
				Dur("backoff", backoff).Msg("Notification send failed, retrying")
			select {
			case <-m.ctx.Done():
				return
			case <-time.After(backoff):
			}
		} else {
			log.Error().Err(err).Str("action", "failed").Int("dispatcher_id", dispatcherID).
				Str("notification_id", notification.ID).
				Int("subscription_id", notification.Subscription.ID).
				Msg("Notification failed after 3 attempts")
		}
	}
}
