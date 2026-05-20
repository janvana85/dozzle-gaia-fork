package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
`

// QueuedNotification is a row from the notification_queue table.
type QueuedNotification struct {
	ID           int64
	SubscriptionID int
	DispatcherID   int
	Notification   types.Notification
	DeliverAt      time.Time
	Attempts       int
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
	return err
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
	return result, rows.Err()
}

// MarkSent marks a queued notification as successfully sent.
func (q *Queue) MarkSent(id int64) error {
	_, err := q.db.Exec(`UPDATE notification_queue SET status = 'sent' WHERE id = ?`, id)
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
