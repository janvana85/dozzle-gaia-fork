package log_storage

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
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
}

func NewStore(dataDir string) *Store {
	return &Store{
		dataDir: dataDir,
		dbPath:  filepath.Join(dataDir, "index.sqlite"),
		files:   make(map[string]*os.File),
		writers: make(map[string]*bufio.Writer),
	}
}

func (s *Store) openDB() error {
	if s.db != nil {
		return nil
	}
	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", s.dbPath)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS log_chunks (
  host TEXT NOT NULL,
  container_id TEXT NOT NULL,
  day TEXT NOT NULL,
  path TEXT NOT NULL,
  first_ts INTEGER NOT NULL,
  last_ts INTEGER NOT NULL,
  lines INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (host, container_id, day)
);
CREATE INDEX IF NOT EXISTS idx_log_chunks_lookup ON log_chunks(host, container_id, day, first_ts, last_ts);

CREATE TABLE IF NOT EXISTS cached_containers (
  host TEXT NOT NULL,
  container_id TEXT NOT NULL,
  payload TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (host, container_id)
);
CREATE INDEX IF NOT EXISTS idx_cached_containers_host ON cached_containers(host, updated_at);
`); err != nil {
		_ = db.Close()
		return err
	}
	s.db = db
	return nil
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
		defer s.closeAll()

		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if err := s.write("unknown", ev); err != nil {
					log.Error().Err(err).Str("containerID", ev.ContainerID).Msg("log_storage: write failed")
				}
			case <-flushTicker.C:
				s.flush()
			case <-cleanupTicker.C:
				s.flush()
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

func (s *Store) write(host string, ev *container.LogEvent) error {
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
	}

	if err := json.NewEncoder(w).Encode(ev); err != nil {
		return err
	}
	return s.upsertIndex(host, ev.ContainerID, day, path, ev.Timestamp)
}

func (s *Store) upsertIndex(host, containerID, day, path string, ts int64) error {
	if err := s.openDB(); err != nil {
		return err
	}
	_, err := s.db.Exec(`
INSERT INTO log_chunks(host, container_id, day, path, first_ts, last_ts, lines, updated_at)
VALUES(?, ?, ?, ?, ?, ?, 1, ?)
ON CONFLICT(host, container_id, day) DO UPDATE SET
  path=excluded.path,
  first_ts=MIN(log_chunks.first_ts, excluded.first_ts),
  last_ts=MAX(log_chunks.last_ts, excluded.last_ts),
  lines=log_chunks.lines+1,
  updated_at=excluded.updated_at
`, host, containerID, day, path, ts, ts, time.Now().Unix())
	return err
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
	payload, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
INSERT INTO cached_containers(host, container_id, payload, updated_at)
VALUES(?, ?, ?, ?)
ON CONFLICT(host, container_id) DO UPDATE SET
  payload=excluded.payload,
  updated_at=excluded.updated_at
`, c.Host, c.ID, string(payload), time.Now().Unix())
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
    AND log_chunks.container_id = cached_containers.container_id
)
`)
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
	if err := s.openDB(); err != nil {
		return nil, err
	}
	query := `SELECT payload FROM cached_containers WHERE EXISTS (
  SELECT 1 FROM log_chunks
  WHERE log_chunks.host = cached_containers.host
    AND log_chunks.container_id = cached_containers.container_id
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
		c.State = "offline"
		c.Stats = nil
		result = append(result, c)
	}
	return result, rows.Err()
}

// HasLogs returns true if any stored log files exist for the container.
func (s *Store) HasLogs(host, containerID string) bool {
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

// LogsBetweenDates returns a channel of stored log events for the container
// between from and to (inclusive). The channel is closed when done.
func (s *Store) LogsBetweenDates(ctx context.Context, host, containerID string, from, to time.Time) (<-chan *container.LogEvent, error) {
	if err := s.openDB(); err != nil {
		return nil, err
	}
	ch := make(chan *container.LogEvent, 100)
	go func() {
		defer close(ch)
		rows, err := s.db.QueryContext(ctx, `
SELECT path FROM log_chunks
WHERE host = ? AND container_id = ? AND day >= ? AND day <= ?
ORDER BY day ASC`,
			host, containerID, from.UTC().Format("2006-01-02"), to.UTC().Format("2006-01-02"))
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				continue
			}
			s.readFile(ctx, path, from, to, ch)
		}
	}()
	return ch, nil
}

// Append stores one log event for the given host/container.
func (s *Store) Append(host string, ev *container.LogEvent) error {
	return s.write(host, ev)
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
