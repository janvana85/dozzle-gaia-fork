package notification

import (
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/amir20/dozzle/internal/container"
	"github.com/amir20/dozzle/internal/utils"
	"github.com/amir20/dozzle/types"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/rs/zerolog/log"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

// isDozzleContainer returns true if the container is a Dozzle instance (to avoid feedback loops)
func isDozzleContainer(c container.Container) bool {
	return strings.Contains(c.Image, "amir20/dozzle")
}

// FromContainerMounts converts a container's MountStats map into the slice form
// exposed to metric expressions. Mounts whose free-space could not be measured
// (Available == false — e.g. Windows volumes or permission errors) are skipped
// so that `any(mounts, .usedPercent >= 85)` never fires on unmeasurable mounts.
func FromContainerMounts(c container.Container) []types.NotificationMount {
	if len(c.MountStats) == 0 {
		return nil
	}
	mounts := make([]types.NotificationMount, 0, len(c.MountStats))
	for _, ms := range c.MountStats {
		if !ms.Available || ms.Total == 0 {
			continue
		}
		used := ms.Used
		// Some fs implementations report Used as Total-Free; recompute to be safe.
		if used == 0 && ms.Free <= ms.Total {
			used = ms.Total - ms.Free
		}
		mounts = append(mounts, types.NotificationMount{
			Destination:    ms.Destination,
			TotalBytes:     ms.Total,
			FreeBytes:      ms.Free,
			UsedBytes:      used,
			UsedPercent:    float64(used) / float64(ms.Total) * 100.0,
			AvailableBytes: ms.Free,
		})
	}
	return mounts
}

// FromContainerModel converts internal container.Container to types.NotificationContainer
func FromContainerModel(c container.Container, host container.Host) types.NotificationContainer {
	return types.NotificationContainer{
		ID:        c.ID,
		Name:      c.Name,
		Image:     c.Image,
		State:     c.State,
		Health:    c.Health,
		HostID:    host.ID,
		HostName:  host.Name,
		HostGroup: host.Group,
		Labels:    c.Labels,
	}
}

// FromLogEvent converts container.LogEvent to types.NotificationLog
func FromLogEvent(l container.LogEvent) types.NotificationLog {
	message := extractMessage(l)

	return types.NotificationLog{
		ID:        l.Id,
		Message:   message,
		Timestamp: l.Timestamp,
		Level:     l.Level,
		Stream:    l.Stream,
		Type:      string(l.Type),
	}
}

// extractMessage extracts and joins message from LogEvent
// For grouped logs (fragments), joins them into a single string
// For complex logs (JSON/objects), converts to a regular map for expr compatibility
// For simple logs, returns the string as-is
func extractMessage(l container.LogEvent) any {
	switch v := l.Message.(type) {
	case string:
		return container.StripANSI(v)
	case []container.LogFragment:
		var sb strings.Builder
		for i, fragment := range v {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(container.StripANSI(fragment.Message))
		}
		return sb.String()
	case *orderedmap.OrderedMap[string, any]:
		// Convert OrderedMap to regular map for expr compatibility
		result := make(map[string]any)
		for pair := v.Oldest(); pair != nil; pair = pair.Next() {
			result[pair.Key] = pair.Value
		}
		return result
	case *orderedmap.OrderedMap[string, string]:
		// Convert OrderedMap[string, string] to regular map for expr compatibility
		result := make(map[string]any)
		for pair := v.Oldest(); pair != nil; pair = pair.Next() {
			result[pair.Key] = pair.Value
		}
		return result
	default:
		// For other complex objects, keep the original structure
		return v
	}
}

// Subscription represents a subscription to log streams with filtering
type Subscription struct {
	ID                  int        `json:"id" yaml:"id"`
	Name                string     `json:"name" yaml:"name"`
	AlertGroup          string     `json:"alertGroup,omitempty" yaml:"alertGroup,omitempty"`
	Enabled             bool       `json:"enabled" yaml:"enabled"`
	DispatcherID        int        `json:"dispatcherId" yaml:"dispatcherId"`
	LogExpression       string     `json:"logExpression" yaml:"logExpression"`
	ContainerExpression string     `json:"containerExpression" yaml:"containerExpression"`
	MetricExpression    string     `json:"metricExpression,omitempty" yaml:"metricExpression,omitempty"`
	EventExpression     string     `json:"eventExpression,omitempty" yaml:"eventExpression,omitempty"`
	Cooldown            int        `json:"cooldown,omitempty" yaml:"cooldown,omitempty"`
	SampleWindow        int        `json:"sampleWindow,omitempty" yaml:"sampleWindow,omitempty"`
	PausedUntil         *time.Time `json:"pausedUntil,omitempty" yaml:"pausedUntil,omitempty"`
	DeliveryDays        []string   `json:"deliveryDays,omitempty" yaml:"deliveryDays,omitempty"`

	// ntfy per-rule routing overrides
	NtfyTopic    string   `json:"ntfyTopic,omitempty" yaml:"ntfyTopic,omitempty"`
	NtfyPriority int      `json:"ntfyPriority,omitempty" yaml:"ntfyPriority,omitempty"` // 1-5; 0 = use dispatcher default
	NtfyTags     []string `json:"ntfyTags,omitempty" yaml:"ntfyTags,omitempty"`

	// Quiet hours behaviour for this rule
	BypassQuietHours bool `json:"bypassQuietHours,omitempty" yaml:"bypassQuietHours,omitempty"`
	QuietPriority    int  `json:"quietPriority,omitempty" yaml:"quietPriority,omitempty"` // priority used during quiet hours; 0 = hold
	HoldDuringQuiet  bool `json:"holdDuringQuiet,omitempty" yaml:"holdDuringQuiet,omitempty"`

	// Hold window: queue notification, deliver after N seconds
	HoldClearWindow int `json:"holdClearWindow,omitempty" yaml:"holdClearWindow,omitempty"`

	// Burst detection: escalate priority after N triggers in BurstWindow seconds
	BurstCount     int    `json:"burstCount,omitempty" yaml:"burstCount,omitempty"`
	BurstWindow    int    `json:"burstWindow,omitempty" yaml:"burstWindow,omitempty"`
	BurstPriority  int    `json:"burstPriority,omitempty" yaml:"burstPriority,omitempty"`
	BurstNtfyTopic string `json:"burstNtfyTopic,omitempty" yaml:"burstNtfyTopic,omitempty"`

	// Unique suppression: derive a key from the matched message and notify once per key/window.
	UniqueKeyRegex  string `json:"uniqueKeyRegex,omitempty" yaml:"uniqueKeyRegex,omitempty"`
	UniqueWindow    int    `json:"uniqueWindow,omitempty" yaml:"uniqueWindow,omitempty"`       // seconds; 0 = disabled
	UniqueThreshold int    `json:"uniqueThreshold,omitempty" yaml:"uniqueThreshold,omitempty"` // 0/1 = first hit per key

	// Watchdog / coupled-messages: fires if resolve pattern doesn't arrive within WatchdogWindow seconds
	WatchdogPattern        string `json:"watchdogPattern,omitempty" yaml:"watchdogPattern,omitempty"`
	WatchdogWindow         int    `json:"watchdogWindow,omitempty" yaml:"watchdogWindow,omitempty"`                 // seconds; 0 = disabled
	WatchdogCooldown       int    `json:"watchdogCooldown,omitempty" yaml:"watchdogCooldown,omitempty"`             // seconds between watchdog alerts; 0 = no cooldown
	WatchdogTriggerMessage string `json:"watchdogTriggerMessage,omitempty" yaml:"watchdogTriggerMessage,omitempty"` // custom alert message
	WatchdogClearMessage   string `json:"watchdogClearMessage,omitempty" yaml:"watchdogClearMessage,omitempty"`     // sent when watchdog resolves

	// Restart loop detection for event alerts.
	RestartLoopEnabled        bool   `json:"restartLoopEnabled,omitempty" yaml:"restartLoopEnabled,omitempty"`
	RestartLoopStateWindow    int    `json:"restartLoopStateWindow,omitempty" yaml:"restartLoopStateWindow,omitempty"`       // seconds; 0 = disabled
	RestartLoopEventCount     int    `json:"restartLoopEventCount,omitempty" yaml:"restartLoopEventCount,omitempty"`         // 0 = disabled
	RestartLoopEventWindow    int    `json:"restartLoopEventWindow,omitempty" yaml:"restartLoopEventWindow,omitempty"`       // seconds; 0 = disabled
	RestartLoopCooldown       int    `json:"restartLoopCooldown,omitempty" yaml:"restartLoopCooldown,omitempty"`             // seconds between alerts; 0 = no cooldown
	RestartLoopTriggerMessage string `json:"restartLoopTriggerMessage,omitempty" yaml:"restartLoopTriggerMessage,omitempty"` // custom alert message

	// Per-alert quiet hours override: if AlertQuietEnabled, these replace global quiet hours for this alert
	AlertQuietEnabled  bool   `json:"alertQuietEnabled,omitempty" yaml:"alertQuietEnabled,omitempty"`
	AlertQuietStart    string `json:"alertQuietStart,omitempty" yaml:"alertQuietStart,omitempty"`       // "22:00"
	AlertQuietEnd      string `json:"alertQuietEnd,omitempty" yaml:"alertQuietEnd,omitempty"`           // "07:00"
	AlertQuietTimezone string `json:"alertQuietTimezone,omitempty" yaml:"alertQuietTimezone,omitempty"` // "Europe/Prague"

	// Compiled filter expressions
	LogProgram           *vm.Program    `json:"-" yaml:"-"`
	ContainerProgram     *vm.Program    `json:"-" yaml:"-"`
	MetricProgram        *vm.Program    `json:"-" yaml:"-"`
	EventProgram         *vm.Program    `json:"-" yaml:"-"`
	WatchdogProgram      *vm.Program    `json:"-" yaml:"-"`
	WatchdogEventProgram *vm.Program    `json:"-" yaml:"-"`
	UniqueRegex          *regexp.Regexp `json:"-" yaml:"-"`

	// Runtime stats (not persisted)
	TriggerCount          atomic.Int64                 `json:"-" yaml:"-"`
	LastTriggeredAt       atomic.Pointer[time.Time]    `json:"-" yaml:"-"`
	TriggeredContainerIDs *xsync.Map[string, struct{}] `json:"-" yaml:"-"`

	// Per-container cooldown tracking
	MetricCooldowns *xsync.Map[string, time.Time] `json:"-" yaml:"-"`
	EventCooldowns  *xsync.Map[string, time.Time] `json:"-" yaml:"-"`
	LogCooldowns    *xsync.Map[string, time.Time] `json:"-" yaml:"-"`

	// Per-container sample buffers for windowed metric evaluation
	MetricSampleBuffers *xsync.Map[string, *utils.RingBuffer[bool]] `json:"-" yaml:"-"`

	// Per-container burst trigger timestamps (recent N triggers within BurstWindow)
	BurstTrackers *xsync.Map[string, []time.Time] `json:"-" yaml:"-"`

	// In-memory fallback for unique suppression when SQLite queue persistence is unavailable.
	UniqueTrackers *xsync.Map[string, UniqueState] `json:"-" yaml:"-"`

	// Per-container watchdog timers
	WatchdogTimers *xsync.Map[string, *time.Timer] `json:"-" yaml:"-"`

	// Per-container watchdog cooldown (last fired time)
	WatchdogCooldowns *xsync.Map[string, time.Time] `json:"-" yaml:"-"`

	// Per-container restart-loop timers and state
	RestartLoopTimers         *xsync.Map[string, *time.Timer] `json:"-" yaml:"-"`
	RestartLoopCooldowns      *xsync.Map[string, time.Time]   `json:"-" yaml:"-"`
	RestartLoopFirstSeenAt    *xsync.Map[string, time.Time]   `json:"-" yaml:"-"`
	RestartLoopRestartStreaks *xsync.Map[string, []time.Time] `json:"-" yaml:"-"`

	// Per-subscription quiet hours overrides (0 = use global)
	QuietStackThreshold int `json:"quietStackThreshold,omitempty" yaml:"quietStackThreshold,omitempty"`
	QuietStackWindow    int `json:"quietStackWindow,omitempty" yaml:"quietStackWindow,omitempty"`
}

type UniqueState struct {
	Count       int
	WindowStart time.Time
	Sent        bool
}

var validDeliveryDays = map[string]time.Weekday{
	"sun": time.Sunday,
	"mon": time.Monday,
	"tue": time.Tuesday,
	"wed": time.Wednesday,
	"thu": time.Thursday,
	"fri": time.Friday,
	"sat": time.Saturday,
}

func normalizeDeliveryDays(days []string) []string {
	if len(days) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(days))
	normalized := make([]string, 0, len(days))
	for _, day := range days {
		key := strings.ToLower(strings.TrimSpace(day))
		if _, ok := validDeliveryDays[key]; !ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, key)
	}
	return normalized
}

func (s *Subscription) NormalizeDeliverySchedule() {
	s.DeliveryDays = normalizeDeliveryDays(s.DeliveryDays)
}

func (s *Subscription) IsPaused(now time.Time) bool {
	if !s.Enabled {
		return true
	}
	return s.PausedUntil != nil && now.Before(*s.PausedUntil)
}

func (s *Subscription) IsDeliveryDay(now time.Time, loc *time.Location) bool {
	if len(s.DeliveryDays) == 0 {
		return true
	}
	weekday := now.In(loc).Weekday()
	for _, day := range s.DeliveryDays {
		if validDeliveryDays[day] == weekday {
			return true
		}
	}
	return false
}

// QuietHoursConfig defines a daily quiet window during which non-bypass alerts
// are either held (queued) or sent with a downgraded priority.
type QuietHoursConfig struct {
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	Start    string `json:"start" yaml:"start"`       // "22:00"
	End      string `json:"end" yaml:"end"`           // "07:00"
	Timezone string `json:"timezone" yaml:"timezone"` // e.g. "Europe/Prague"; empty = local

	// Summary grouping is enabled by default. A pointer preserves that default
	// for existing configuration files where the field is absent.
	GroupNotifications *bool `json:"groupNotifications,omitempty" yaml:"groupNotifications,omitempty"`
	SummaryMaxGroups    int   `json:"summaryMaxGroups,omitempty" yaml:"summaryMaxGroups,omitempty"`

	// Stacking
	StackThreshold  int `json:"stackThreshold" yaml:"stackThreshold"`   // 0 = default 3
	StackWindow     int `json:"stackWindow" yaml:"stackWindow"`         // seconds; 0 = default 900
	StackedPriority int `json:"stackedPriority" yaml:"stackedPriority"` // 0 = default 4

	// Topic routing
	QuietTopic            string `json:"quietTopic" yaml:"quietTopic"`
	StackedUsesQuietTopic bool   `json:"stackedUsesQuietTopic" yaml:"stackedUsesQuietTopic"`
}

func (q QuietHoursConfig) GroupingEnabled() bool {
	return q.GroupNotifications == nil || *q.GroupNotifications
}

func (q QuietHoursConfig) MaxSummaryGroups() int {
	if q.SummaryMaxGroups < 1 {
		return defaultQuietSummaryGroups
	}
	if q.SummaryMaxGroups > 50 {
		return 50
	}
	return q.SummaryMaxGroups
}

// TriggeredContainersCount returns the number of unique containers that triggered this subscription
func (s *Subscription) TriggeredContainersCount() int {
	if s.TriggeredContainerIDs == nil {
		return 0
	}
	return s.TriggeredContainerIDs.Size()
}

// AddTriggeredContainer adds a container ID to the triggered set
func (s *Subscription) AddTriggeredContainer(id string) {
	if s.TriggeredContainerIDs == nil {
		s.TriggeredContainerIDs = xsync.NewMap[string, struct{}]()
	}
	s.TriggeredContainerIDs.Store(id, struct{}{})
}

// CompileExpressions compiles all expression strings into executable programs.
// Returns an error describing which expression failed to compile.
func (s *Subscription) CompileExpressions() error {
	if s.ContainerExpression != "" {
		program, err := expr.Compile(s.ContainerExpression, expr.Env(types.NotificationContainer{}))
		if err != nil {
			return fmt.Errorf("failed to compile container expression: %w", err)
		}
		s.ContainerProgram = program
	}

	if s.LogExpression != "" {
		program, err := expr.Compile(s.LogExpression, expr.Env(types.NotificationLog{}))
		if err != nil {
			return fmt.Errorf("failed to compile log expression: %w", err)
		}
		s.LogProgram = program
	}

	if s.MetricExpression != "" {
		program, err := expr.Compile(s.MetricExpression, expr.Env(types.NotificationStat{}))
		if err != nil {
			return fmt.Errorf("failed to compile metric expression: %w", err)
		}
		s.MetricProgram = program
	}

	if s.EventExpression != "" {
		program, err := expr.Compile(s.EventExpression, expr.Env(types.NotificationEvent{}))
		if err != nil {
			return fmt.Errorf("failed to compile event expression: %w", err)
		}
		s.EventProgram = program
	}

	if s.WatchdogPattern != "" {
		var logErr error
		var eventErr error

		if s.LogExpression != "" || s.EventExpression == "" {
			program, err := expr.Compile(s.WatchdogPattern, expr.Env(types.NotificationLog{}))
			if err == nil {
				s.WatchdogProgram = program
			} else {
				logErr = err
			}
		}

		if s.EventExpression != "" {
			eventProgram, err := expr.Compile(s.WatchdogPattern, expr.Env(types.NotificationEvent{}))
			if err == nil {
				s.WatchdogEventProgram = eventProgram
			} else {
				eventErr = err
			}
		}

		if s.LogExpression != "" && s.WatchdogProgram == nil {
			return fmt.Errorf("failed to compile watchdog pattern: %w", logErr)
		}
		if s.EventExpression != "" && s.WatchdogEventProgram == nil {
			return fmt.Errorf("failed to compile watchdog pattern: %w", eventErr)
		}
		if s.LogExpression == "" && s.EventExpression == "" && s.WatchdogProgram == nil && s.WatchdogEventProgram == nil {
			return fmt.Errorf("failed to compile watchdog pattern: %w", logErr)
		}
	}

	if s.UniqueKeyRegex != "" {
		re, err := regexp.Compile(s.UniqueKeyRegex)
		if err != nil {
			return fmt.Errorf("failed to compile unique key regex: %w", err)
		}
		s.UniqueRegex = re
	}

	return nil
}

// DispatcherConfig represents a dispatcher configuration
type DispatcherConfig struct {
	ID       int               `json:"id" yaml:"id"`
	Name     string            `json:"name" yaml:"name"`
	Type     string            `json:"type" yaml:"type"` // "webhook", "ntfy", or "cloud"
	URL      string            `json:"url,omitempty" yaml:"url,omitempty"`
	Template string            `json:"template,omitempty" yaml:"template,omitempty"`
	Headers  map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Prefix   string            `json:"prefix,omitempty" yaml:"-"` // Cloud only, not persisted
	// ntfy-specific fields
	Topic           string   `json:"topic,omitempty" yaml:"topic,omitempty"`
	Priority        int      `json:"priority,omitempty" yaml:"priority,omitempty"` // 1-5
	Tags            []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Token           string   `json:"token,omitempty" yaml:"token,omitempty"`
	TitleTemplate   string   `json:"titleTemplate,omitempty" yaml:"titleTemplate,omitempty"`
	MessageTemplate string   `json:"messageTemplate,omitempty" yaml:"messageTemplate,omitempty"`
}

// MatchesWatchdog checks if a log matches the watchdog resolve pattern.
func (s *Subscription) MatchesWatchdog(l types.NotificationLog) bool {
	if s.WatchdogProgram == nil {
		return false
	}
	result, err := expr.Run(s.WatchdogProgram, l)
	if err != nil {
		return false
	}
	match, ok := result.(bool)
	return ok && match
}

func (s *Subscription) MatchesWatchdogEvent(event types.NotificationEvent) bool {
	if s.WatchdogEventProgram == nil {
		return false
	}
	result, err := expr.Run(s.WatchdogEventProgram, event)
	if err != nil {
		log.Debug().Err(err).Str("expression", s.WatchdogPattern).Msg("watchdog event expression evaluation error")
		return false
	}
	match, ok := result.(bool)
	return ok && match
}

// ResetWatchdogTimer cancels any existing watchdog timer and starts a new one for the container.
func (s *Subscription) ResetWatchdogTimer(containerID string, fire func(), window int) {
	if old, ok := s.WatchdogTimers.Load(containerID); ok {
		old.Stop()
	}
	t := time.AfterFunc(time.Duration(window)*time.Second, fire)
	s.WatchdogTimers.Store(containerID, t)
}

// CancelWatchdogTimer stops and removes the watchdog timer for a container.
// Returns true if a timer was active (useful for deciding whether to send a clear message).
func (s *Subscription) CancelWatchdogTimer(containerID string) bool {
	t, ok := s.WatchdogTimers.LoadAndDelete(containerID)
	if ok {
		t.Stop()
	}
	return ok
}

// StopRuntime cancels runtime-only work owned by this subscription.
func (s *Subscription) StopRuntime() {
	if s.WatchdogTimers == nil {
		return
	}
	var containerIDs []string
	s.WatchdogTimers.Range(func(containerID string, _ *time.Timer) bool {
		containerIDs = append(containerIDs, containerID)
		return true
	})
	for _, containerID := range containerIDs {
		s.CancelWatchdogTimer(containerID)
	}
}

// Config represents the persisted notification configuration
type Config struct {
	Subscriptions []*Subscription    `json:"subscriptions" yaml:"subscriptions"`
	Dispatchers   []DispatcherConfig `json:"dispatchers" yaml:"dispatchers"`
	QuietHours    QuietHoursConfig   `json:"quietHours,omitempty" yaml:"quietHours,omitempty"`
}

// MatchesContainer checks if a container matches this subscription's container filter
func (s *Subscription) MatchesContainer(c types.NotificationContainer) bool {
	if s.ContainerProgram == nil {
		return false
	}

	result, err := expr.Run(s.ContainerProgram, c)
	if err != nil {
		log.Warn().Err(err).Str("expression", s.ContainerExpression).Msg("container expression evaluation error")
		return false
	}

	match, ok := result.(bool)
	return ok && match
}

// MatchesLog checks if a log matches this subscription's log filter
func (s *Subscription) MatchesLog(l types.NotificationLog) bool {
	if s.LogProgram == nil {
		return false
	}

	result, err := expr.Run(s.LogProgram, l)
	if err != nil {
		return false
	}

	match, ok := result.(bool)
	return ok && match
}

// IsLogAlert returns true if this subscription is a log-based alert
func (s *Subscription) IsLogAlert() bool {
	return s.LogExpression != "" && s.LogProgram != nil
}

// IsMetricAlert returns true if this subscription is a metric-based alert
func (s *Subscription) IsMetricAlert() bool {
	return s.MetricExpression != "" && s.MetricProgram != nil
}

// IsEventAlert returns true if this subscription is an event-based alert
func (s *Subscription) IsEventAlert() bool {
	return s.EventExpression != "" && s.EventProgram != nil
}

// MatchesEvent checks if a Docker event matches this subscription's event filter
func (s *Subscription) MatchesEvent(event types.NotificationEvent) bool {
	if s.EventProgram == nil {
		return false
	}
	result, err := expr.Run(s.EventProgram, event)
	if err != nil {
		log.Debug().Err(err).Str("expression", s.EventExpression).Msg("event expression evaluation error")
		return false
	}
	match, ok := result.(bool)
	return ok && match
}

// IsEventCooldownActive checks if the cooldown is still active for a given container
func (s *Subscription) IsEventCooldownActive(containerID string) bool {
	if s.Cooldown == 0 {
		return false
	}
	lastTriggered, ok := s.EventCooldowns.Load(containerID)
	if !ok {
		return false
	}
	cooldown := time.Duration(s.GetCooldownSeconds()) * time.Second
	return time.Now().Before(lastTriggered.Add(cooldown))
}

// SetEventCooldown records the current time as the last triggered time for a container
func (s *Subscription) SetEventCooldown(containerID string) {
	s.EventCooldowns.Store(containerID, time.Now())
}

// MatchesMetric checks if a stat matches this subscription's metric filter
func (s *Subscription) MatchesMetric(stat types.NotificationStat) bool {
	if s.MetricProgram == nil {
		return false
	}

	result, err := expr.Run(s.MetricProgram, stat)
	if err != nil {
		log.Debug().Err(err).Str("expression", s.MetricExpression).Msg("metric expression evaluation error")
		return false
	}

	match, ok := result.(bool)
	return ok && match
}

const maxCooldownSeconds = 48 * 60 * 60

// GetCooldownSeconds returns the cooldown in seconds, clamped to [0, 48h].
func (s *Subscription) GetCooldownSeconds() int {
	return clampCooldownSeconds(s.Cooldown)
}

func clampCooldownSeconds(seconds int) int {
	if seconds <= 0 {
		return 0
	}
	if seconds > maxCooldownSeconds {
		return maxCooldownSeconds
	}
	return seconds
}

// IsMetricCooldownActive checks if the cooldown is still active for a given container
func (s *Subscription) IsMetricCooldownActive(containerID string) bool {
	if s.Cooldown == 0 {
		return false
	}
	lastTriggered, ok := s.MetricCooldowns.Load(containerID)
	if !ok {
		return false
	}
	cooldown := time.Duration(s.GetCooldownSeconds()) * time.Second
	return time.Now().Before(lastTriggered.Add(cooldown))
}

// SetMetricCooldown records the current time as the last triggered time for a container
func (s *Subscription) SetMetricCooldown(containerID string) {
	s.MetricCooldowns.Store(containerID, time.Now())
}

// GetSampleWindowSeconds returns the sample window in seconds, clamped to [1, 300], defaulting to 15
func (s *Subscription) GetSampleWindowSeconds() int {
	if s.SampleWindow <= 0 {
		return 15
	}
	if s.SampleWindow < 1 {
		return 1
	}
	if s.SampleWindow > 300 {
		return 300
	}
	return s.SampleWindow
}

// RecordMetricSample records a metric evaluation result and returns true if the window threshold is met.
// The alert fires when the buffer is full and >=80% of samples matched.
func (s *Subscription) RecordMetricSample(containerID string, matched bool) bool {
	windowSize := s.GetSampleWindowSeconds()

	// For window size of 1, just return the match result directly
	if windowSize <= 1 {
		return matched
	}

	buf, _ := s.MetricSampleBuffers.LoadOrCompute(containerID, func() (*utils.RingBuffer[bool], bool) {
		return utils.NewRingBuffer[bool](windowSize), false
	})

	buf.Push(matched)

	if buf.Len() < windowSize {
		return false
	}

	trueCount := 0
	for _, v := range buf.Data() {
		if v {
			trueCount++
		}
	}

	return float64(trueCount)/float64(buf.Len()) >= 0.8
}

// IsLogCooldownActive checks if the log cooldown is still active for a given container.
func (s *Subscription) IsLogCooldownActive(containerID string) bool {
	if s.Cooldown == 0 || s.LogCooldowns == nil {
		return false
	}
	lastTriggered, ok := s.LogCooldowns.Load(containerID)
	if !ok {
		return false
	}
	cooldown := time.Duration(s.GetCooldownSeconds()) * time.Second
	return time.Now().Before(lastTriggered.Add(cooldown))
}

// SetLogCooldown records the current time as the last log-triggered time for a container.
func (s *Subscription) SetLogCooldown(containerID string) {
	if s.LogCooldowns != nil {
		s.LogCooldowns.Store(containerID, time.Now())
	}
}

func (s *Subscription) ExtractUniqueKey(message string) (string, bool) {
	if s.UniqueWindow <= 0 || s.UniqueRegex == nil {
		return "", false
	}
	matches := s.UniqueRegex.FindStringSubmatch(message)
	if len(matches) == 0 {
		return "", false
	}
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1]), strings.TrimSpace(matches[1]) != ""
	}
	return strings.TrimSpace(matches[0]), strings.TrimSpace(matches[0]) != ""
}

// DetectBurst records the eligible notification send and returns the notification
// priority, whether the burst threshold has been reached, and the current count.
func (s *Subscription) DetectBurst(containerID string, basePriority int) (int, bool, int) {
	if s.BurstCount <= 0 || s.BurstWindow <= 0 || s.BurstTrackers == nil {
		return basePriority, false, 0
	}

	now := time.Now()
	window := time.Duration(s.BurstWindow) * time.Second
	cutoff := now.Add(-window)

	timestamps, _ := s.BurstTrackers.LoadOrCompute(containerID, func() ([]time.Time, bool) {
		return []time.Time{}, false
	})

	// Prune old entries then append current trigger
	pruned := timestamps[:0]
	for _, t := range timestamps {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	pruned = append(pruned, now)
	s.BurstTrackers.Store(containerID, pruned)

	if len(pruned) >= s.BurstCount && s.BurstPriority > 0 {
		return s.BurstPriority, true, len(pruned)
	}
	return basePriority, false, len(pruned)
}

// IsWatchdogCooldownActive returns true if the watchdog alert is on cooldown for a container.
func (s *Subscription) IsWatchdogCooldownActive(containerID string) bool {
	if s.WatchdogCooldown == 0 || s.WatchdogCooldowns == nil {
		return false
	}
	lastFired, ok := s.WatchdogCooldowns.Load(containerID)
	if !ok {
		return false
	}
	return time.Now().Before(lastFired.Add(time.Duration(clampCooldownSeconds(s.WatchdogCooldown)) * time.Second))
}

// SetWatchdogCooldown records the current time as the last watchdog-fired time for a container.
func (s *Subscription) SetWatchdogCooldown(containerID string) {
	if s.WatchdogCooldowns != nil {
		s.WatchdogCooldowns.Store(containerID, time.Now())
	}
}

// Restart loop detection helpers.
func (s *Subscription) IsRestartLoopCooldownActive(containerID string) bool {
	if s.RestartLoopCooldown == 0 || s.RestartLoopCooldowns == nil {
		return false
	}
	lastFired, ok := s.RestartLoopCooldowns.Load(containerID)
	if !ok {
		return false
	}
	return time.Now().Before(lastFired.Add(time.Duration(clampCooldownSeconds(s.RestartLoopCooldown)) * time.Second))
}

func (s *Subscription) SetRestartLoopCooldown(containerID string) {
	if s.RestartLoopCooldowns != nil {
		s.RestartLoopCooldowns.Store(containerID, time.Now())
	}
}

func (s *Subscription) CancelRestartLoopTimer(containerID string) bool {
	if s.RestartLoopTimers == nil {
		return false
	}
	t, ok := s.RestartLoopTimers.LoadAndDelete(containerID)
	if !ok {
		if s.RestartLoopFirstSeenAt != nil {
			s.RestartLoopFirstSeenAt.Delete(containerID)
		}
		return false
	}
	_ = t.Stop()
	if s.RestartLoopFirstSeenAt != nil {
		s.RestartLoopFirstSeenAt.Delete(containerID)
	}
	return true
}

func (s *Subscription) resetRestartLoopTimer(containerID string, d time.Duration) {
	if s.RestartLoopTimers == nil {
		return
	}
	if existing, ok := s.RestartLoopTimers.LoadAndDelete(containerID); ok {
		_ = existing.Stop()
	}
	timer := time.AfterFunc(d, func() {})
	s.RestartLoopTimers.Store(containerID, timer)
}

// Config represents the persisted notification configuration
