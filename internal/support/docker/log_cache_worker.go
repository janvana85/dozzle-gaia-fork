package docker_support

import (
	"context"
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/amir20/dozzle/internal/container"
	container_support "github.com/amir20/dozzle/internal/support/container"
	"github.com/rs/zerolog/log"
)

const (
	maxLogCacheBackfill            = 4 * 24 * time.Hour
	defaultLogCacheInitialBackfill = maxLogCacheBackfill
	defaultLogCachePollInterval    = time.Minute
	defaultLogCacheMaxCatchup      = maxLogCacheBackfill
	defaultLogCacheMaxBackfills    = 2
)

type logCacheConfig struct {
	initialBackfill time.Duration
	pollInterval    time.Duration
	maxCatchup      time.Duration
	maxBackfills    int
}

func (m *MultiHostService) startLogCacheWorker(ctx context.Context) {
	if m.logStore == nil {
		return
	}
	cfg := loadLogCacheConfig()
	sem := make(chan struct{}, cfg.maxBackfills)

	containers := make(chan container.Container, 100)
	m.SubscribeContainersStarted(ctx, containers, func(c *container.Container) bool {
		return c.State == "running"
	})

	go func() {
		m.scheduleCacheBackfills(ctx, cfg, sem)

		ticker := time.NewTicker(cfg.pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case c, ok := <-containers:
				if !ok {
					return
				}
				m.scheduleCacheBackfillForStartedContainer(ctx, cfg, sem, c)
			case <-ticker.C:
				m.scheduleCacheBackfills(ctx, cfg, sem)
			}
		}
	}()
}

func loadLogCacheConfig() logCacheConfig {
	return logCacheConfig{
		initialBackfill: boundedDurationEnv("DOZZLE_LOG_CACHE_INITIAL_BACKFILL", defaultLogCacheInitialBackfill, maxLogCacheBackfill),
		pollInterval:    durationEnv("DOZZLE_LOG_CACHE_POLL_INTERVAL", defaultLogCachePollInterval),
		maxCatchup:      boundedDurationEnv("DOZZLE_LOG_CACHE_MAX_CATCHUP", defaultLogCacheMaxCatchup, maxLogCacheBackfill),
		maxBackfills:    intEnv("DOZZLE_LOG_CACHE_MAX_BACKFILLS", defaultLogCacheMaxBackfills),
	}
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		log.Warn().Str("env", name).Str("value", value).Dur("fallback", fallback).Msg("invalid log cache duration")
		return fallback
	}
	return d
}

func boundedDurationEnv(name string, fallback time.Duration, maxValue time.Duration) time.Duration {
	d := durationEnv(name, fallback)
	if d > maxValue {
		log.Warn().Str("env", name).Dur("value", d).Dur("max", maxValue).Msg("log cache duration exceeds maximum")
		return maxValue
	}
	return d
}

func intEnv(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		log.Warn().Str("env", name).Str("value", value).Int("fallback", fallback).Msg("invalid log cache integer")
		return fallback
	}
	return n
}

func (m *MultiHostService) scheduleCacheBackfills(ctx context.Context, cfg logCacheConfig, sem chan struct{}) {
	for _, client := range m.manager.List() {
		host, err := client.Host(ctx)
		if err != nil {
			log.Debug().Err(err).Msg("log cache: host unavailable during cache scan")
			continue
		}
		containers, err := client.ListContainers(ctx, nil)
		if err != nil {
			log.Debug().Err(err).Str("host", host.Name).Msg("log cache: failed to list containers")
			continue
		}
		for _, c := range containers {
			if c.State != "running" {
				continue
			}
			m.scheduleCacheBackfill(ctx, cfg, sem, client, c)
		}
	}
}

func (m *MultiHostService) scheduleCacheBackfillForStartedContainer(ctx context.Context, cfg logCacheConfig, sem chan struct{}, c container.Container) {
	client, ok := m.manager.Find(c.Host)
	if !ok {
		return
	}
	found, err := client.FindContainer(ctx, c.ID, nil)
	if err != nil {
		log.Debug().Err(err).Str("container", c.ID).Msg("log cache: failed to resolve started container")
		return
	}
	if found.State != "running" {
		return
	}
	m.scheduleCacheBackfill(ctx, cfg, sem, client, found)
}

func (m *MultiHostService) scheduleCacheBackfill(ctx context.Context, cfg logCacheConfig, sem chan struct{}, client container_support.ClientService, c container.Container) {
	key := containerCacheKey(c)
	if _, loaded := m.cacheJobs.LoadOrStore(key, struct{}{}); loaded {
		return
	}

	go func() {
		defer m.cacheJobs.Delete(key)

		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			return
		}

		if err := m.backfillCacheWindow(ctx, cfg, client, c); err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
			log.Debug().Err(err).Str("container", c.ID).Msg("log cache: backfill failed")
		}
	}()
}

func (m *MultiHostService) backfillCacheWindow(ctx context.Context, cfg logCacheConfig, client container_support.ClientService, c container.Container) error {
	if err := m.logStore.RecordContainer(c); err != nil {
		log.Debug().Err(err).Str("container", c.ID).Msg("log cache: failed to record container metadata")
	}

	now := time.Now()
	from := now.Add(-cfg.initialBackfill)
	if c.StartedAt.After(from) {
		from = c.StartedAt
	}
	if last, ok := m.logStore.LastTimestampForContainer(c); ok {
		from = last.Add(time.Millisecond)
	}
	if cfg.maxCatchup > 0 {
		minFrom := now.Add(-cfg.maxCatchup)
		if from.Before(minFrom) {
			from = minFrom
		}
	}
	if !from.Before(now) {
		return nil
	}

	events, err := client.LogsBetweenDates(ctx, c, from, now, container.STDALL)
	if err != nil {
		return err
	}
	count := 0
	for event := range events {
		if err := m.logStore.AppendForContainer(c, event); err != nil {
			log.Debug().Err(err).Str("container", c.ID).Msg("log cache: failed to append backfilled log")
			continue
		}
		count++
	}
	if count > 0 {
		log.Debug().
			Str("container", c.ID).
			Str("identity", c.LogIdentity()).
			Int("lines", count).
			Time("from", from).
			Time("to", now).
			Msg("log cache: backfilled logs")
	}
	return nil
}
