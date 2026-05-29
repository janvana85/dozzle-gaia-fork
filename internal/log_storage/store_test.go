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
