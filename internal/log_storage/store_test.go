package log_storage

import (
	"testing"
	"time"

	"github.com/amir20/dozzle/internal/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogsBetweenDatesForContainerUsesComposeIdentity(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()

	oldContainer := container.Container{
		ID:     "old123456789",
		Name:   "app-api-1",
		Host:   "earth",
		Labels: map[string]string{"com.docker.compose.project": "app", "com.docker.compose.service": "api"},
	}
	newContainer := container.Container{
		ID:     "new123456789",
		Name:   "app-api-1",
		Host:   "earth",
		Labels: map[string]string{"com.docker.compose.project": "app", "com.docker.compose.service": "api"},
	}

	require.NoError(t, store.RecordContainer(oldContainer))
	require.NoError(t, store.AppendForContainer(oldContainer, &container.LogEvent{
		ContainerID: oldContainer.ID,
		Timestamp:   now.Add(-time.Minute).UnixMilli(),
		Message:     "old deploy",
	}))
	require.NoError(t, store.RecordContainer(newContainer))
	require.NoError(t, store.AppendForContainer(newContainer, &container.LogEvent{
		ContainerID: newContainer.ID,
		Timestamp:   now.UnixMilli(),
		Message:     "new deploy",
	}))
	store.flush()

	assert.True(t, store.HasLogsForContainer(newContainer))

	events, err := store.LogsBetweenDatesForContainer(t.Context(), newContainer, now.Add(-time.Hour), now.Add(time.Hour))
	require.NoError(t, err)

	messages := make([]string, 0)
	for event := range events {
		messages = append(messages, event.Message.(string))
	}
	assert.Equal(t, []string{"old deploy", "new deploy"}, messages)
}

func TestSearchBeforeForContainerReturnsNewestMatchesWithMore(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	c := container.Container{ID: "abc123456789", Name: "api", Host: "earth"}

	require.NoError(t, store.RecordContainer(c))
	for i, message := range []string{"ignore", "needle old", "needle middle", "needle newest"} {
		require.NoError(t, store.AppendForContainer(c, &container.LogEvent{
			ContainerID: c.ID,
			Timestamp:   now.Add(time.Duration(i) * time.Minute).UnixMilli(),
			Message:     message,
			RawMessage:  message,
			Stream:      "stdout",
			Level:       "info",
		}))
	}
	store.flush()

	events, hasMore, err := store.SearchBeforeForContainer(t.Context(), c, now.Add(10*time.Minute), 2, func(ev *container.LogEvent) bool {
		message, _ := ev.Message.(string)
		return message != "" && message != "ignore"
	})
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Len(t, events, 2)
	assert.Equal(t, "needle newest", events[0].Message)
	assert.Equal(t, "needle middle", events[1].Message)

	older, hasMore, err := store.SearchBeforeForContainer(t.Context(), c, time.UnixMilli(events[1].Timestamp), 2, func(ev *container.LogEvent) bool {
		message, _ := ev.Message.(string)
		return message != "" && message != "ignore"
	})
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Len(t, older, 1)
	assert.Equal(t, "needle old", older[0].Message)
}
