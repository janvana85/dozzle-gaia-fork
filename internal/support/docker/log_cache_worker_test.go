package docker_support

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoadLogCacheConfigDefaultsToFourDayBackfill(t *testing.T) {
	t.Setenv("DOZZLE_LOG_CACHE_INITIAL_BACKFILL", "")
	t.Setenv("DOZZLE_LOG_CACHE_MAX_CATCHUP", "")

	cfg := loadLogCacheConfig()

	assert.Equal(t, 4*24*time.Hour, cfg.initialBackfill)
	assert.Equal(t, 4*24*time.Hour, cfg.maxCatchup)
}

func TestLoadLogCacheConfigCapsBackfillAtFourDays(t *testing.T) {
	t.Setenv("DOZZLE_LOG_CACHE_INITIAL_BACKFILL", "168h")
	t.Setenv("DOZZLE_LOG_CACHE_MAX_CATCHUP", "240h")

	cfg := loadLogCacheConfig()

	assert.Equal(t, 4*24*time.Hour, cfg.initialBackfill)
	assert.Equal(t, 4*24*time.Hour, cfg.maxCatchup)
}

func TestLoadLogCacheConfigAllowsShorterBackfill(t *testing.T) {
	t.Setenv("DOZZLE_LOG_CACHE_INITIAL_BACKFILL", "12h")
	t.Setenv("DOZZLE_LOG_CACHE_MAX_CATCHUP", "24h")

	cfg := loadLogCacheConfig()

	assert.Equal(t, 12*time.Hour, cfg.initialBackfill)
	assert.Equal(t, 24*time.Hour, cfg.maxCatchup)
}
