package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/amir20/dozzle/types"
	"github.com/rs/zerolog/log"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS notification_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	subscription_id INTEGER NOT NULL,
	dispatcher_id INTEGER NOT NULL,
	payload TEXT NOT NULL,
	ntfy_topic TEXT NOT NULL DEFAULT '',
	ntfy_priority INTEGER NOT NULL DEFAULT 3,
	created_at DATETIME NOT NULL,
	deliver_at DATETIME NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'pending'
);
CREATE INDEX IF NOT EXISTS idx_nq_status_deliver ON notification_queue(status, deliver_at);

CREATE TABLE IF NOT EXISTS alert_state (
    fingerprint      TEXT PRIMARY KEY,
    subscription_id  INTEGER NOT NULL,
    host_name        TEXT NOT NULL,
    container_name   TEXT NOT NULL,
    alert_type       TEXT NOT NULL,
    count            INTEGER NOT NULL DEFAULT 1,
    window_start     INTEGER NOT NULL,
    quiet_first_sent INTEGER NOT NULL DEFAULT 0,
    stacked_sent     INTEGER NOT NULL DEFAULT 0,
    updated_at       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS cooldown_state (
    key        TEXT PRIMARY KEY,
    expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS unique_state (
    fingerprint      TEXT PRIMARY KEY,
    subscription_id  INTEGER NOT NULL,
    unique_key       TEXT NOT NULL,
    count            INTEGER NOT NULL DEFAULT 0,
    window_start     INTEGER NOT NULL,
    sent             INTEGER NOT NULL DEFAULT 0,
    updated_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_unique_state_updated ON unique_state(updated_at);
`

// QueuedNotification is a row from the notification_queue table.
type QueuedNotification struct {
	ID             int64
	SubscriptionID int
	DispatcherID   int
	Notification   types.Notification
	DeliverAt      time.Time
	Attempts       int
}

// AlertState is a row from the alert_state table.
type AlertState struct {
	Fingerprint    string
	SubscriptionID int
	HostName       string
	ContainerName  string
	AlertType      string
	Count          int
	WindowStart    time.Time
	QuietFirstSent bool
	StackedSent    bool
	UpdatedAt      time.Time
}

type UniqueState struct {
	Fingerprint    string
	SubscriptionID int
	UniqueKey      string
	Count          int
	WindowStart    time.Time
	Sent           bool
	UpdatedAt      time.Time
}

// Queue is an SQLite-backed notification queue that survives container restarts.
type Queue struct {
	db *sql.DB
}

// NewQueue opens (or creates) the SQLite database at dbPath and applies the schema.
func NewQueue(dbPath string) (*Queue, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	return &Queue{db: db}, nil
}

// Close closes the underlying database connection.
func (q *Queue) Close() error {
	return q.db.Close()
}

// Healthcheck verifies the queue database can execute a write and read round-trip.
func (q *Queue) Healthcheck(ctx context.Context) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin healthcheck tx: %w", err)
	}
	defer tx.Rollback() // rollback is safe even after commit/no-op and keeps the probe side-effect free

	marker := fmt.Sprintf("healthcheck-%d", rand.Int63())
	now := time.Now().UTC()
	notification := types.Notification{
		ID:        marker,
		Type:      types.LogNotification,
		Detail:    "healthcheck",
		Log:       &types.NotificationLog{Message: "healthcheck"},
		Timestamp: now,
		Subscription: types.SubscriptionConfig{
			ID:           -1,
			DispatcherID: -1,
			Name:         "healthcheck",
			Enabled:      true,
		},
	}
	payload, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("marshal healthcheck notification: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO notification_queue
		 (subscription_id, dispatcher_id, payload, ntfy_topic, ntfy_priority, created_at, deliver_at, attempts, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, 'pending')`,
		notification.Subscription.ID,
		notification.Subscription.DispatcherID,
		string(payload),
		notification.NtfyTopic,
		notification.NtfyPriority,
		now,
		now,
	); err != nil {
		return fmt.Errorf("insert healthcheck row: %w", err)
	}

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_queue WHERE payload = ?`, string(payload)).Scan(&count); err != nil {
		return fmt.Errorf("verify healthcheck row: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("unexpected healthcheck row count %d", count)
	}

	return nil
}

// Enqueue stores a notification that should be delivered at deliverAt.
func (q *Queue) Enqueue(notification types.Notification, deliverAt time.Time) error {
	payload, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	_, err = q.db.Exec(
		`INSERT INTO notification_queue
		 (subscription_id, dispatcher_id, payload, ntfy_topic, ntfy_priority, created_at, deliver_at, attempts, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, 'pending')`,
		notification.Subscription.ID,
		notification.Subscription.DispatcherID,
		string(payload),
		notification.NtfyTopic,
		notification.NtfyPriority,
		time.Now().UTC(),
		deliverAt.UTC(),
	)
	if err == nil {
		log.Info().
			Str("alert_type", string(notification.Type)).
			Str("action", "persisted").
			Int("subscription_id", notification.Subscription.ID).
			Str("subscription", notification.Subscription.Name).
			Str("notification_id", notification.ID).
			Str("status", "pending").
			Time("deliver_at", deliverAt.UTC()).
			Msg("Notification persisted to SQLite queue")
	}
	return err
}

// PendingCountForSubscription returns how many queued notifications are still pending for a subscription.
func (q *Queue) PendingCountForSubscription(subscriptionID int) (int, error) {
	var count int
	err := q.db.QueryRow(
		`SELECT COUNT(*) FROM notification_queue WHERE status = 'pending' AND subscription_id = ?`,
		subscriptionID,
	).Scan(&count)
	return count, err
}

// DrainReady returns all pending notifications whose deliver_at time has passed.
func (q *Queue) DrainReady(ctx context.Context) ([]QueuedNotification, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, subscription_id, dispatcher_id, payload, deliver_at, attempts
		 FROM notification_queue
		 WHERE status = 'pending' AND deliver_at <= ?
		 ORDER BY deliver_at ASC`,
		time.Now().UTC(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []QueuedNotification
	for rows.Next() {
		var qn QueuedNotification
		var payloadStr string
		var deliverAt string
		if err := rows.Scan(&qn.ID, &qn.SubscriptionID, &qn.DispatcherID, &payloadStr, &deliverAt, &qn.Attempts); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payloadStr), &qn.Notification); err != nil {
			log.Warn().Err(err).Int64("id", qn.ID).Msg("Skipping malformed queued notification")
			q.drop(qn.ID) //nolint:errcheck
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, deliverAt); err == nil {
			qn.DeliverAt = t
		}
		result = append(result, qn)
	}
	if len(result) > 0 {
		log.Info().Str("action", "drain").Int("count", len(result)).Msg("Notification queue drained ready items")
	}
	return result, rows.Err()
}

// MarkSent marks a queued notification as successfully sent.
func (q *Queue) MarkSent(id int64) error {
	_, err := q.db.Exec(`UPDATE notification_queue SET status = 'sent' WHERE id = ?`, id)
	if err == nil {
		log.Info().
			Str("action", "sent").
			Int64("queue_id", id).
			Str("status", "sent").
			Msg("Queued notification delivered")
	}
	return err
}

// UpdateDeliverAt reschedules a pending queued notification.
func (q *Queue) UpdateDeliverAt(id int64, deliverAt time.Time) error {
	_, err := q.db.Exec(
		`UPDATE notification_queue SET deliver_at = ?, attempts = 0 WHERE id = ? AND status = 'pending'`,
		deliverAt.UTC(),
		id,
	)
	if err == nil {
		log.Info().
			Str("action", "rescheduled").
			Int64("queue_id", id).
			Time("deliver_at", deliverAt.UTC()).
			Msg("Queued notification rescheduled")
	}
	return err
}

// MarkFailed increments the attempt counter. When attempts reaches 3 the row is
// marked 'dropped' and will not be retried.
func (q *Queue) MarkFailed(id int64, attempts int) error {
	nextAttempts := attempts + 1
	status := "pending"
	if nextAttempts >= 3 {
		status = "dropped"
	}
	_, err := q.db.Exec(
		`UPDATE notification_queue SET attempts = ?, status = ?, deliver_at = ?
		 WHERE id = ?`,
		nextAttempts,
		status,
		time.Now().UTC().Add(time.Duration(nextAttempts*nextAttempts)*30*time.Second),
		id,
	)
	if err == nil {
		log.Warn().
			Str("action", "failed").
			Int64("queue_id", id).
			Int("attempts", nextAttempts).
			Str("status", status).
			Msg("Queued notification delivery failed")
	}
	return err
}

func (q *Queue) drop(id int64) error {
	_, err := q.db.Exec(`UPDATE notification_queue SET status = 'dropped' WHERE id = ?`, id)
	return err
}

// Cleanup removes sent and dropped rows older than maxAge.
func (q *Queue) Cleanup(maxAge time.Duration) {
	cutoff := time.Now().UTC().Add(-maxAge)
	if _, err := q.db.Exec(
		`DELETE FROM notification_queue WHERE status IN ('sent','dropped') AND created_at < ?`, cutoff,
	); err != nil {
		log.Warn().Err(err).Msg("Failed to clean notification queue")
	}
}

// GetOrCreateAlertState atomically loads the alert_state row for fp, creating
// it if absent.
func (q *Queue) GetOrCreateAlertState(fp string, subscriptionID int, hostName, containerName, alertType string) (*AlertState, error) {
	now := time.Now()
	_, err := q.db.Exec(`
		INSERT OR IGNORE INTO alert_state
			(fingerprint, subscription_id, host_name, container_name, alert_type, count, window_start, quiet_first_sent, stacked_sent, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, 0, 0, ?)`,
		fp, subscriptionID, hostName, containerName, alertType, now.Unix(), now.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("insert alert_state: %w", err)
	}

	row := q.db.QueryRow(`
		SELECT subscription_id, host_name, container_name, alert_type, count, window_start, quiet_first_sent, stacked_sent, updated_at
		FROM alert_state WHERE fingerprint = ?`, fp)

	var s AlertState
	s.Fingerprint = fp
	var windowStartUnix, updatedAtUnix int64
	var quietFirstSentInt, stackedSentInt int
	if err := row.Scan(&s.SubscriptionID, &s.HostName, &s.ContainerName, &s.AlertType,
		&s.Count, &windowStartUnix, &quietFirstSentInt, &stackedSentInt, &updatedAtUnix); err != nil {
		return nil, fmt.Errorf("scan alert_state: %w", err)
	}
	s.WindowStart = time.Unix(windowStartUnix, 0)
	s.UpdatedAt = time.Unix(updatedAtUnix, 0)
	s.QuietFirstSent = quietFirstSentInt != 0
	s.StackedSent = stackedSentInt != 0
	return &s, nil
}

// UpsertAlertState writes the alert state back to SQLite.
func (q *Queue) UpsertAlertState(s *AlertState) error {
	now := time.Now()
	s.UpdatedAt = now
	_, err := q.db.Exec(`
		INSERT INTO alert_state
			(fingerprint, subscription_id, host_name, container_name, alert_type, count, window_start, quiet_first_sent, stacked_sent, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(fingerprint) DO UPDATE SET
			count            = excluded.count,
			window_start     = excluded.window_start,
			quiet_first_sent = excluded.quiet_first_sent,
			stacked_sent     = excluded.stacked_sent,
			updated_at       = excluded.updated_at`,
		s.Fingerprint, s.SubscriptionID, s.HostName, s.ContainerName, s.AlertType,
		s.Count, s.WindowStart.Unix(), boolToInt(s.QuietFirstSent), boolToInt(s.StackedSent), now.Unix(),
	)
	return err
}

// CleanupAlertState removes alert_state rows not updated within retention.
func (q *Queue) CleanupAlertState(retention time.Duration) {
	cutoff := time.Now().Add(-retention).Unix()
	if _, err := q.db.Exec(`DELETE FROM alert_state WHERE updated_at < ?`, cutoff); err != nil {
		log.Warn().Err(err).Msg("Failed to clean alert_state")
	}
}

func (q *Queue) GetOrCreateUniqueState(fp string, subscriptionID int, uniqueKey string) (*UniqueState, error) {
	now := time.Now()
	_, err := q.db.Exec(`
		INSERT OR IGNORE INTO unique_state
			(fingerprint, subscription_id, unique_key, count, window_start, sent, updated_at)
		VALUES (?, ?, ?, 0, ?, 0, ?)`,
		fp, subscriptionID, uniqueKey, now.Unix(), now.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("insert unique_state: %w", err)
	}

	row := q.db.QueryRow(`
		SELECT subscription_id, unique_key, count, window_start, sent, updated_at
		FROM unique_state WHERE fingerprint = ?`, fp)

	var s UniqueState
	s.Fingerprint = fp
	var windowStartUnix, updatedAtUnix int64
	var sentInt int
	if err := row.Scan(&s.SubscriptionID, &s.UniqueKey, &s.Count, &windowStartUnix, &sentInt, &updatedAtUnix); err != nil {
		return nil, fmt.Errorf("scan unique_state: %w", err)
	}
	s.WindowStart = time.Unix(windowStartUnix, 0)
	s.UpdatedAt = time.Unix(updatedAtUnix, 0)
	s.Sent = sentInt != 0
	return &s, nil
}

func (q *Queue) UpsertUniqueState(s *UniqueState) error {
	now := time.Now()
	s.UpdatedAt = now
	_, err := q.db.Exec(`
		INSERT INTO unique_state
			(fingerprint, subscription_id, unique_key, count, window_start, sent, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(fingerprint) DO UPDATE SET
			count        = excluded.count,
			window_start = excluded.window_start,
			sent         = excluded.sent,
			updated_at   = excluded.updated_at`,
		s.Fingerprint, s.SubscriptionID, s.UniqueKey, s.Count, s.WindowStart.Unix(), boolToInt(s.Sent), now.Unix(),
	)
	return err
}

func (q *Queue) CleanupUniqueState(retention time.Duration) {
	cutoff := time.Now().Add(-retention).Unix()
	if _, err := q.db.Exec(`DELETE FROM unique_state WHERE updated_at < ?`, cutoff); err != nil {
		log.Warn().Err(err).Msg("Failed to clean unique_state")
	}
}

// SetCooldown writes or refreshes a cooldown entry. key is "<subscriptionID>:<containerID>:<type>".
func (q *Queue) SetCooldown(key string, expiresAt time.Time) error {
	_, err := q.db.Exec(`
		INSERT INTO cooldown_state (key, expires_at) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET expires_at = excluded.expires_at`,
		key, expiresAt.Unix(),
	)
	return err
}

// LoadCooldowns returns all non-expired cooldown entries.
func (q *Queue) LoadCooldowns() (map[string]time.Time, error) {
	now := time.Now().Unix()
	rows, err := q.db.Query(`SELECT key, expires_at FROM cooldown_state WHERE expires_at > ?`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]time.Time)
	for rows.Next() {
		var key string
		var expiresAtUnix int64
		if err := rows.Scan(&key, &expiresAtUnix); err != nil {
			return nil, err
		}
		result[key] = time.Unix(expiresAtUnix, 0)
	}
	return result, rows.Err()
}

// CleanupCooldowns removes expired cooldown entries.
func (q *Queue) CleanupCooldowns() {
	if _, err := q.db.Exec(`DELETE FROM cooldown_state WHERE expires_at <= ?`, time.Now().Unix()); err != nil {
		log.Warn().Err(err).Msg("Failed to clean cooldown_state")
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
