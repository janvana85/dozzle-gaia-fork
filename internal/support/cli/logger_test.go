package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlertLogWriterKeepsAlertEvents(t *testing.T) {
	var out bytes.Buffer
	writer := alertLogWriter{writer: &out}

	n, err := writer.Write([]byte(`{"level":"info","message":"Notification sent"}` + "\n"))

	require.NoError(t, err)
	assert.Positive(t, n)
	assert.Contains(t, out.String(), "Notification sent")
}

func TestAlertLogWriterDropsConnectionNoise(t *testing.T) {
	var out bytes.Buffer
	writer := alertLogWriter{writer: &out}

	n, err := writer.Write([]byte(`{"level":"warn","message":"error fetching host info for agent"}` + "\n"))

	require.NoError(t, err)
	assert.Positive(t, n)
	assert.Empty(t, out.String())
}

func TestAlertLogWriterHandlesConsoleOutput(t *testing.T) {
	var out bytes.Buffer
	writer := alertLogWriter{writer: &out}

	_, err := writer.Write([]byte("INF Notification queued subscription=api\nINF Connected to Docker\n"))

	require.NoError(t, err)
	assert.Contains(t, out.String(), "Notification queued")
	assert.NotContains(t, out.String(), "Connected to Docker")
}
