package log_storage

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/amir20/dozzle/internal/container"
	"github.com/rs/zerolog/log"
)

const retentionDays = 4

// Store persists log events to NDJSON files on disk and serves them back.
// Files are organized as {dataDir}/{containerID[:12]}/{YYYY-MM-DD}.ndjson.
type Store struct {
	dataDir string
	mu      sync.Mutex
	files   map[string]*os.File
	writers map[string]*bufio.Writer
}

func NewStore(dataDir string) *Store {
	return &Store{
		dataDir: dataDir,
		files:   make(map[string]*os.File),
		writers: make(map[string]*bufio.Writer),
	}
}

// Start begins consuming events from ch, writing them to disk.
// It also flushes every 5 seconds and runs daily cleanup/rotation.
func (s *Store) Start(ctx context.Context, ch <-chan *container.LogEvent) {
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
				if err := s.write(ev); err != nil {
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

func (s *Store) containerDir(containerID string) string {
	short := containerID
	if len(short) > 12 {
		short = short[:12]
	}
	return filepath.Join(s.dataDir, short)
}

func (s *Store) filePath(containerID string, day time.Time) string {
	return filepath.Join(s.containerDir(containerID), day.UTC().Format("2006-01-02")+".ndjson")
}

func (s *Store) write(ev *container.LogEvent) error {
	if ev.ContainerID == "" {
		return nil
	}
	ts := time.UnixMilli(ev.Timestamp).UTC()
	path := s.filePath(ev.ContainerID, ts)

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

	return json.NewEncoder(w).Encode(ev)
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
	cutoff := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -retentionDays)
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(s.dataDir, entry.Name())
		files, _ := os.ReadDir(dir)
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
				_ = os.Remove(filepath.Join(dir, name))
				log.Debug().Str("file", name).Str("container", entry.Name()).Msg("log_storage: removed expired file")
			}
		}
		remaining, _ := os.ReadDir(dir)
		if len(remaining) == 0 {
			_ = os.Remove(dir)
		}
	}
}

// HasLogs returns true if any stored log files exist for the container.
func (s *Store) HasLogs(containerID string) bool {
	dir := s.containerDir(containerID)
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// LogsBetweenDates returns a channel of stored log events for the container
// between from and to (inclusive). The channel is closed when done.
func (s *Store) LogsBetweenDates(ctx context.Context, containerID string, from, to time.Time) (<-chan *container.LogEvent, error) {
	dir := s.containerDir(containerID)
	if _, err := os.Stat(dir); err != nil {
		short := containerID
		if len(short) > 12 {
			short = short[:12]
		}
		return nil, fmt.Errorf("no stored logs for container %s", short)
	}

	ch := make(chan *container.LogEvent, 100)
	go func() {
		defer close(ch)
		for d := from.UTC().Truncate(24 * time.Hour); !d.After(to.UTC()); d = d.AddDate(0, 0, 1) {
			path := filepath.Join(dir, d.Format("2006-01-02")+".ndjson")
			s.readFile(ctx, path, from, to, ch)
		}
	}()
	return ch, nil
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
