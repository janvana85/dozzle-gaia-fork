package log_storage

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/amir20/dozzle/internal/container"
	"github.com/rs/zerolog/log"
	_ "modernc.org/sqlite"
)

const retentionDays = 4

// Store persists log events to NDJSON files on disk and serves them back.
// Files are organized as {dataDir}/{host}/{containerID[:12]}/{YYYY-MM-DD}.ndjson.
type Store struct {
	dataDir string
	dbPath  string
	db      *sql.DB
	mu      sync.Mutex
	files   map[string]*os.File
	writers map[string]*bufio.Writer
	recent  map[string]logEventKey
	pending map[chunkKey]*chunkDelta
}

// chunkKey mirrors the log_chunks primary key.
type chunkKey struct {
	host        string
	containerID string
	day         string
}

// chunkDelta accumulates index updates in memory so the SQLite index is
// written once per flush instead of once per log line.
type chunkDelta struct {
	identity string
	path     string
	firstTS  int64
	lastTS   int64
	lines    int64
}

func NewStore(dataDir string) *Store {
	return &Store{
		dataDir: dataDir,
		dbPath:  filepath.Join(dataDir, "index.sqlite"),
		files:   make(map[string]*os.File),
		writers: make(map[string]*bufio.Writer),
		recent:  make(map[string]logEventKey),
		pending: make(map[chunkKey]*chunkDelta),
	}
}

type logEventKey struct {
	valid     bool
	timestamp int64
	id        uint32
	raw       string
}

func eventKey(ev *container.LogEvent) logEventKey {
	return logEventKey{valid: true, timestamp: ev.Timestamp, id: ev.Id, raw: ev.RawMessage}
}

func (s *Store) openDB() error {
	if s.db != nil {
		return nil
	}
	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		return err
	}
	// WAL lets historical-scroll reads run while the live stream is writing;
	// without it every cache read queues behind index writes. The pragmas are
	// per-connection, so they go through the DSN to cover the whole pool.
	dsn := "file:" + s.dbPath + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS log_chunks (
  host TEXT NOT NULL,
  container_id TEXT NOT NULL,
  identity TEXT NOT NULL DEFAULT '',
  day TEXT NOT NULL,
  path TEXT NOT NULL,
  first_ts INTEGER NOT NULL,
  last_ts INTEGER NOT NULL,
  lines INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (host, container_id, day)
);

CREATE TABLE IF NOT EXISTS cached_containers (
  host TEXT NOT NULL,
  container_id TEXT NOT NULL,
  identity TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT '',
  finished_at INTEGER NOT NULL DEFAULT 0,
  payload TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (host, container_id)
);
`); err != nil {
		_ = db.Close()
		return err
	}
	for _, col := range []struct {
		table string
		name  string
		def   string
	}{
		{"log_chunks", "identity", "TEXT NOT NULL DEFAULT ''"},
		{"cached_containers", "identity", "TEXT NOT NULL DEFAULT ''"},
		{"cached_containers", "state", "TEXT NOT NULL DEFAULT ''"},
		{"cached_containers", "finished_at", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := ensureColumn(db, col.table, col.name, col.def); err != nil {
			_ = db.Close()
			return err
		}
	}
	if err := migrateCachedIdentities(db); err != nil {
		_ = db.Close()
		return err
	}
	if _, err := db.Exec(`
CREATE INDEX IF NOT EXISTS idx_log_chunks_lookup ON log_chunks(host, container_id, day, first_ts, last_ts);
CREATE INDEX IF NOT EXISTS idx_log_chunks_identity_lookup ON log_chunks(host, identity, day, first_ts, last_ts);
CREATE INDEX IF NOT EXISTS idx_cached_containers_host ON cached_containers(host, updated_at);
CREATE INDEX IF NOT EXISTS idx_cached_containers_identity ON cached_containers(host, identity);
`); err != nil {
		_ = db.Close()
		return err
	}
	s.db = db
	return nil
}

func migrateCachedIdentities(db *sql.DB) error {
	rows, err := db.Query(`SELECT host, container_id, payload FROM cached_containers WHERE identity = ''`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type update struct {
		host        string
		containerID string
		identity    string
	}
	updates := make([]update, 0)
	for rows.Next() {
		var host, containerID, payload string
		if err := rows.Scan(&host, &containerID, &payload); err != nil {
			return err
		}
		var c container.Container
		if err := json.Unmarshal([]byte(payload), &c); err != nil {
			continue
		}
		identity := c.LogIdentity()
		if identity == "" {
			continue
		}
		updates = append(updates, update{host: host, containerID: containerID, identity: identity})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, u := range updates {
		if _, err := db.Exec(`UPDATE cached_containers SET identity = ? WHERE host = ? AND container_id = ?`, u.identity, u.host, u.containerID); err != nil {
			return err
		}
		if _, err := db.Exec(`UPDATE log_chunks SET identity = ? WHERE host = ? AND container_id = ? AND identity = ''`, u.identity, u.host, u.containerID); err != nil {
			return err
		}
	}
	return nil
}

func ensureColumn(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	return err
}

// Start begins consuming events from ch, writing them to disk.
// It also flushes every 5 seconds and runs daily cleanup/rotation.
func (s *Store) Start(ctx context.Context, ch <-chan *container.LogEvent) {
	if err := s.openDB(); err != nil {
		log.Error().Err(err).Msg("log_storage: failed to open sqlite index")
	}
	s.cleanup()

	go func() {
		flushTicker := time.NewTicker(5 * time.Second)
		cleanupTicker := time.NewTicker(24 * time.Hour)
		defer flushTicker.Stop()
		defer cleanupTicker.Stop()
		defer func() {
			s.closeAll()
			s.flushIndex()
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if err := s.write("unknown", ev, ""); err != nil {
					log.Error().Err(err).Str("containerID", ev.ContainerID).Msg("log_storage: write failed")
				}
			case <-flushTicker.C:
				s.flush()
				s.flushIndex()
			case <-cleanupTicker.C:
				s.flush()
				s.flushIndex()
				s.closeAll() // close handles so day rollover opens new files
				s.cleanup()
			}
		}
	}()
}

func (s *Store) containerDir(host, containerID string) string {
	if host == "" {
		host = "unknown"
	}
	short := containerID
	if len(short) > 12 {
		short = short[:12]
	}
	return filepath.Join(s.dataDir, host, short)
}

func (s *Store) filePath(host, containerID string, day time.Time) string {
	return filepath.Join(s.containerDir(host, containerID), day.UTC().Format("2006-01-02")+".ndjson")
}

func (s *Store) write(host string, ev *container.LogEvent, identity string) error {
	if ev.ContainerID == "" {
		return nil
	}
	ts := time.UnixMilli(ev.Timestamp).UTC()
	path := s.filePath(host, ev.ContainerID, ts)
	day := ts.Format("2006-01-02")

	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.writers[path]
	if !ok {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		s.files[path] = f
		w = bufio.NewWriterSize(f, 64*1024)
		s.writers[path] = w
		s.recent[path] = s.lastEventKey(path)
	}

	key := eventKey(ev)
	if key == s.recent[path] {
		return nil
	}

	if err := json.NewEncoder(w).Encode(ev); err != nil {
		return err
	}
	s.recent[path] = key

	ck := chunkKey{host: host, containerID: ev.ContainerID, day: day}
	delta, ok := s.pending[ck]
	if !ok {
		delta = &chunkDelta{firstTS: ev.Timestamp, lastTS: ev.Timestamp}
		s.pending[ck] = delta
	}
	delta.identity = identity
	delta.path = path
	delta.firstTS = min(delta.firstTS, ev.Timestamp)
	delta.lastTS = max(delta.lastTS, ev.Timestamp)
	delta.lines++
	return nil
}

func (s *Store) lastEventKey(path string) logEventKey {
	data, err := os.ReadFile(path)
	if err != nil {
		return logEventKey{}
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return logEventKey{}
	}
	if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
		data = data[i+1:]
	}
	var ev container.LogEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return logEventKey{}
	}
	return eventKey(&ev)
}

// flushIndex applies accumulated chunk deltas to SQLite in one transaction.
// On failure the deltas are merged back so the next flush retries them.
func (s *Store) flushIndex() {
	s.mu.Lock()
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return
	}
	pending := s.pending
	s.pending = make(map[chunkKey]*chunkDelta)
	s.mu.Unlock()

	restore := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for key, delta := range pending {
			if newer, ok := s.pending[key]; ok {
				newer.firstTS = min(newer.firstTS, delta.firstTS)
				newer.lastTS = max(newer.lastTS, delta.lastTS)
				newer.lines += delta.lines
			} else {
				s.pending[key] = delta
			}
		}
	}

	if err := s.openDB(); err != nil {
		restore()
		log.Error().Err(err).Msg("log_storage: failed to open sqlite index for flush")
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		restore()
		log.Error().Err(err).Msg("log_storage: failed to begin index flush")
		return
	}
	now := time.Now().Unix()
	for key, delta := range pending {
		if _, err := tx.Exec(`
INSERT INTO log_chunks(host, container_id, identity, day, path, first_ts, last_ts, lines, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(host, container_id, day) DO UPDATE SET
  identity=excluded.identity,
  path=excluded.path,
  first_ts=MIN(log_chunks.first_ts, excluded.first_ts),
  last_ts=MAX(log_chunks.last_ts, excluded.last_ts),
  lines=log_chunks.lines+excluded.lines,
  updated_at=excluded.updated_at
`, key.host, key.containerID, delta.identity, key.day, delta.path, delta.firstTS, delta.lastTS, delta.lines, now); err != nil {
			_ = tx.Rollback()
			restore()
			log.Error().Err(err).Msg("log_storage: failed to flush index delta")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		restore()
		log.Error().Err(err).Msg("log_storage: failed to commit index flush")
	}
}

// syncForRead makes buffered writes visible to a reader: file buffers are
// flushed to disk and pending index deltas are applied. Reads are rare
// compared to writes, so this keeps read-your-writes without paying a SQLite
// transaction per log line.
func (s *Store) syncForRead() {
	s.flush()
	s.flushIndex()
}

// RecordContainer stores lightweight metadata so cached containers can remain visible
// even after the live agent or Docker container disappears.
func (s *Store) RecordContainer(c container.Container) error {
	if c.ID == "" {
		return nil
	}
	if err := s.openDB(); err != nil {
		return err
	}
	c.Stats = nil
	c.Env = nil
	c.Ports = nil
	finishedAt := int64(0)
	if !c.FinishedAt.IsZero() {
		finishedAt = c.FinishedAt.UTC().Unix()
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return err
	}
	identity := c.LogIdentity()
	_, err = s.db.Exec(`
INSERT INTO cached_containers(host, container_id, identity, state, finished_at, payload, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(host, container_id) DO UPDATE SET
  identity=excluded.identity,
  state=excluded.state,
  finished_at=excluded.finished_at,
  payload=excluded.payload,
  updated_at=excluded.updated_at
`, c.Host, c.ID, identity, c.State, finishedAt, string(payload), time.Now().Unix())
	return err
}

func (s *Store) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.writers {
		_ = w.Flush()
	}
}

func (s *Store) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.writers {
		_ = w.Flush()
	}
	for _, f := range s.files {
		_ = f.Close()
	}
	s.files = make(map[string]*os.File)
	s.writers = make(map[string]*bufio.Writer)
}

func (s *Store) cleanup() {
	s.syncForRead()
	if err := s.openDB(); err == nil {
		cutoffTS := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -retentionDays).Unix()
		rows, err := s.db.Query(`SELECT path FROM log_chunks WHERE last_ts < ?`, cutoffTS*1000)
		if err == nil {
			var path string
			for rows.Next() {
				if err := rows.Scan(&path); err == nil {
					_ = os.Remove(path)
				}
			}
			_ = rows.Close()
			_, _ = s.db.Exec(`DELETE FROM log_chunks WHERE last_ts < ?`, cutoffTS*1000)
			_, _ = s.db.Exec(`
DELETE FROM cached_containers
WHERE NOT EXISTS (
  SELECT 1 FROM log_chunks
  WHERE log_chunks.host = cached_containers.host
    AND (
      log_chunks.container_id = cached_containers.container_id
      OR (cached_containers.identity != '' AND log_chunks.identity = cached_containers.identity)
    )
)
`)
			exitedCutoff := time.Now().UTC().Add(-24 * time.Hour).Unix()
			_, _ = s.db.Exec(`DELETE FROM cached_containers WHERE state = 'exited' AND finished_at > 0 AND finished_at < ?`, exitedCutoff)
		}
	}
	cutoff := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -retentionDays)
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return
	}
	for _, hostEntry := range entries {
		if !hostEntry.IsDir() {
			continue
		}
		hostDir := filepath.Join(s.dataDir, hostEntry.Name())
		containerEntries, _ := os.ReadDir(hostDir)
		for _, containerEntry := range containerEntries {
			if !containerEntry.IsDir() {
				continue
			}
			containerDir := filepath.Join(hostDir, containerEntry.Name())
			files, _ := os.ReadDir(containerDir)
			for _, f := range files {
				if !f.Type().IsRegular() {
					continue
				}
				name := f.Name()
				if len(name) < 10 {
					continue
				}
				t, err := time.Parse("2006-01-02", name[:10])
				if err != nil {
					continue
				}
				if t.Before(cutoff) {
					_ = os.Remove(filepath.Join(containerDir, name))
					log.Debug().Str("file", name).Str("container", containerEntry.Name()).Str("host", hostEntry.Name()).Msg("log_storage: removed expired file")
				}
			}
			remaining, _ := os.ReadDir(containerDir)
			if len(remaining) == 0 {
				_ = os.Remove(containerDir)
			}
		}
		remainingHosts, _ := os.ReadDir(hostDir)
		if len(remainingHosts) == 0 {
			_ = os.Remove(hostDir)
		}
	}
}

// CachedContainers returns cached container metadata. Returned containers are
// marked offline because they only represent locally cached logs, not a live
// Docker object.
func (s *Store) CachedContainers(host string) ([]container.Container, error) {
	s.syncForRead()
	if err := s.openDB(); err != nil {
		return nil, err
	}
	query := `SELECT payload FROM cached_containers WHERE EXISTS (
  SELECT 1 FROM log_chunks
  WHERE log_chunks.host = cached_containers.host
    AND (
      log_chunks.container_id = cached_containers.container_id
      OR (cached_containers.identity != '' AND log_chunks.identity = cached_containers.identity)
    )
)`
	args := []any{}
	if host != "" {
		query += ` AND cached_containers.host = ?`
		args = append(args, host)
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]container.Container, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			continue
		}
		var c container.Container
		if err := json.Unmarshal([]byte(payload), &c); err != nil {
			continue
		}
		if c.State == "exited" && !c.FinishedAt.IsZero() {
			if time.Since(c.FinishedAt.UTC()) > 24*time.Hour {
				continue
			}
		}
		c.State = "offline"
		c.Stats = nil
		result = append(result, c)
	}
	return result, rows.Err()
}

// HasLogs returns true if any stored log files exist for the container.
func (s *Store) HasLogs(host, containerID string) bool {
	s.syncForRead()
	if err := s.openDB(); err != nil {
		dir := s.containerDir(host, containerID)
		entries, err := os.ReadDir(dir)
		return err == nil && len(entries) > 0
	}
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM log_chunks WHERE host = ? AND container_id = ? LIMIT 1`, host, containerID).Scan(&exists); err != nil {
		return false
	}
	return true
}

// HasLogsForContainer returns true if cached logs exist for the concrete
// container ID or for the stable Compose/Coolify service identity.
func (s *Store) HasLogsForContainer(c container.Container) bool {
	s.syncForRead()
	if c.ID == "" {
		return false
	}
	identity := c.LogIdentity()
	if identity == "" {
		return s.HasLogs(c.Host, c.ID)
	}
	if err := s.openDB(); err != nil {
		return s.HasLogs(c.Host, c.ID)
	}
	var exists int
	err := s.db.QueryRow(`
SELECT 1 FROM log_chunks
WHERE host = ? AND (container_id = ? OR identity = ?)
LIMIT 1`, c.Host, c.ID, identity).Scan(&exists)
	return err == nil
}

// LastTimestampForContainer returns the newest cached log timestamp for the
// concrete container ID or stable service identity.
func (s *Store) LastTimestampForContainer(c container.Container) (time.Time, bool) {
	s.syncForRead()
	if c.ID == "" {
		return time.Time{}, false
	}
	if err := s.openDB(); err != nil {
		return time.Time{}, false
	}
	identity := c.LogIdentity()
	query := `SELECT MAX(last_ts) FROM log_chunks WHERE host = ? AND container_id = ?`
	args := []any{c.Host, c.ID}
	if identity != "" {
		query += ` OR (host = ? AND identity = ?)`
		args = append(args, c.Host, identity)
	}
	var ts sql.NullInt64
	if err := s.db.QueryRow(query, args...).Scan(&ts); err != nil || !ts.Valid || ts.Int64 <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(ts.Int64).UTC(), true
}

// LogsBetweenDates returns a channel of stored log events for the container
// between from and to (inclusive). The channel is closed when done.
func (s *Store) LogsBetweenDates(ctx context.Context, host, containerID string, from, to time.Time) (<-chan *container.LogEvent, error) {
	return s.logsBetweenDates(ctx, host, containerID, "", from, to)
}

// LogsBetweenDatesForContainer includes logs from previous container IDs that
// belonged to the same stable Compose/Coolify service identity.
func (s *Store) LogsBetweenDatesForContainer(ctx context.Context, c container.Container, from, to time.Time) (<-chan *container.LogEvent, error) {
	return s.logsBetweenDates(ctx, c.Host, c.ID, c.LogIdentity(), from, to)
}

// SearchBeforeForContainer returns matching cached log events before the given
// time, newest first. It reads files backwards so recent searches can stop as
// soon as the bounded result set is full.
func (s *Store) SearchBeforeForContainer(ctx context.Context, c container.Container, before time.Time, limit int, match func(*container.LogEvent) bool) ([]*container.LogEvent, bool, error) {
	return s.searchBefore(ctx, c.Host, c.ID, c.LogIdentity(), before, limit, match)
}

// SearchBefore returns matching cached log events for a concrete host and
// container ID before the given time, newest first.
func (s *Store) SearchBefore(ctx context.Context, host, containerID string, before time.Time, limit int, match func(*container.LogEvent) bool) ([]*container.LogEvent, bool, error) {
	return s.searchBefore(ctx, host, containerID, "", before, limit, match)
}

// PreviousChunkRangeForContainer returns the nearest cached chunk that ends
// before the given time for the concrete container ID or stable service identity.
func (s *Store) PreviousChunkRangeForContainer(c container.Container, before time.Time) (time.Time, time.Time, bool, error) {
	return s.previousChunkRange(c.Host, c.ID, c.LogIdentity(), before)
}

// PreviousChunkRange returns the nearest cached chunk that ends before the given
// time for the concrete host and container ID.
func (s *Store) PreviousChunkRange(host, containerID string, before time.Time) (time.Time, time.Time, bool, error) {
	return s.previousChunkRange(host, containerID, "", before)
}

func (s *Store) logsBetweenDates(ctx context.Context, host, containerID, identity string, from, to time.Time) (<-chan *container.LogEvent, error) {
	s.syncForRead()
	if err := s.openDB(); err != nil {
		return nil, err
	}
	ch := make(chan *container.LogEvent, 100)
	go func() {
		defer close(ch)
		dayFrom := from.UTC().Format("2006-01-02")
		dayTo := to.UTC().Format("2006-01-02")
		query := `
SELECT path, MIN(first_ts) AS first_ts FROM log_chunks
WHERE (host = ? AND container_id = ? AND day >= ? AND day <= ?)`
		args := []any{host, containerID, dayFrom, dayTo}
		if identity != "" {
			query += ` OR (host = ? AND identity = ? AND day >= ? AND day <= ?)`
			args = append(args, host, identity, dayFrom, dayTo)
		}
		query += ` GROUP BY path ORDER BY first_ts ASC`
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var path string
			var firstTS int64
			if err := rows.Scan(&path, &firstTS); err != nil {
				continue
			}
			s.readFile(ctx, path, from, to, ch)
		}
	}()
	return ch, nil
}

func (s *Store) searchBefore(ctx context.Context, host, containerID, identity string, before time.Time, limit int, match func(*container.LogEvent) bool) ([]*container.LogEvent, bool, error) {
	s.syncForRead()
	if limit <= 0 {
		limit = 200
	}
	if before.IsZero() {
		before = time.Now()
	}
	if err := s.openDB(); err != nil {
		return nil, false, err
	}
	from := before.UTC().AddDate(0, 0, -retentionDays)
	dayFrom := from.Format("2006-01-02")
	dayTo := before.UTC().Format("2006-01-02")
	query := `
SELECT path FROM log_chunks
WHERE ((host = ? AND container_id = ?)`
	args := []any{host, containerID}
	if identity != "" {
		query += ` OR (host = ? AND identity = ?)`
		args = append(args, host, identity)
	}
	query += `) AND day >= ? AND day <= ? AND first_ts < ?
GROUP BY path
ORDER BY MAX(last_ts) DESC`
	args = append(args, dayFrom, dayTo, before.UnixMilli())

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	result := make([]*container.LogEvent, 0, limit)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			continue
		}
		more, err := s.readFileReverse(ctx, path, from, before, limit+1, match, &result)
		if err != nil {
			continue
		}
		if more || len(result) > limit {
			return result[:limit], true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return result, false, nil
}

func (s *Store) previousChunkRange(host, containerID, identity string, before time.Time) (time.Time, time.Time, bool, error) {
	s.syncForRead()
	if before.IsZero() {
		before = time.Now()
	}
	if err := s.openDB(); err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	from := before.UTC().AddDate(0, 0, -retentionDays)
	dayFrom := from.Format("2006-01-02")
	dayTo := before.UTC().Format("2006-01-02")
	query := `
SELECT first_ts, last_ts FROM log_chunks
WHERE ((host = ? AND container_id = ?)`
	args := []any{host, containerID}
	if identity != "" {
		query += ` OR (host = ? AND identity = ?)`
		args = append(args, host, identity)
	}
	query += `) AND day >= ? AND day <= ? AND last_ts < ?
ORDER BY last_ts DESC
LIMIT 1`
	args = append(args, dayFrom, dayTo, before.UnixMilli())

	var firstTS, lastTS int64
	if err := s.db.QueryRow(query, args...).Scan(&firstTS, &lastTS); err != nil {
		if err == sql.ErrNoRows {
			return time.Time{}, time.Time{}, false, nil
		}
		return time.Time{}, time.Time{}, false, err
	}
	return time.UnixMilli(firstTS).UTC(), time.UnixMilli(lastTS).UTC(), true, nil
}

// Append stores one log event for the given host/container.
func (s *Store) Append(host string, ev *container.LogEvent) error {
	return s.write(host, ev, "")
}

// AppendForContainer stores one log event and indexes it under a stable
// service identity when the container comes from Compose/Coolify.
func (s *Store) AppendForContainer(c container.Container, ev *container.LogEvent) error {
	return s.write(c.Host, ev, c.LogIdentity())
}

func (s *Store) readFile(ctx context.Context, path string, from, to time.Time, ch chan<- *container.LogEvent) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	dec := json.NewDecoder(bufio.NewReaderSize(f, 64*1024))
	for dec.More() {
		var ev container.LogEvent
		if err := dec.Decode(&ev); err != nil {
			break
		}
		ts := time.UnixMilli(ev.Timestamp)
		if ts.Before(from) || ts.After(to) {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case ch <- &ev:
		}
	}
}

func (s *Store) readFileReverse(ctx context.Context, path string, from, to time.Time, stopAfter int, match func(*container.LogEvent) bool, result *[]*container.LogEvent) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return false, err
	}

	const chunkSize int64 = 64 * 1024
	pos := info.Size()
	pending := make([]byte, 0)
	for pos > 0 {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
		}

		n := chunkSize
		if pos < n {
			n = pos
		}
		pos -= n
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, pos); err != nil {
			return false, err
		}
		data := append(buf, pending...)
		parts := bytes.Split(data, []byte{'\n'})
		pending = append(pending[:0], parts[0]...)
		for i := len(parts) - 1; i >= 1; i-- {
			if s.decodeReverseLine(parts[i], from, to, match, result) && len(*result) >= stopAfter {
				return true, nil
			}
		}
	}
	if len(pending) > 0 && s.decodeReverseLine(pending, from, to, match, result) && len(*result) >= stopAfter {
		return true, nil
	}
	return false, nil
}

func (s *Store) decodeReverseLine(line []byte, from, to time.Time, match func(*container.LogEvent) bool, result *[]*container.LogEvent) bool {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return false
	}
	var ev container.LogEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return false
	}
	ts := time.UnixMilli(ev.Timestamp)
	if ts.Before(from) || !ts.Before(to) {
		return false
	}
	if match != nil && !match(&ev) {
		return false
	}
	*result = append(*result, &ev)
	return true
}
