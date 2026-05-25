package dispatcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/amir20/dozzle/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNtfyDispatcher_RendersContentTemplates(t *testing.T) {
	var payload ntfyPayload
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		rw.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ntfy, err := NewNtfyDispatcher(
		"test",
		srv.URL,
		"alerts",
		5,
		"",
		"[{{ .Container.HostName }}] {{ .Subscription.Name }} - {{ .Container.Name }}",
		"Alert: {{ .Subscription.Name }}\nType: {{ .Type }}\nContainer: {{ .Container.Name }}\n\n{{ .Detail }}",
	)
	require.NoError(t, err)
	ntfy.client = srv.Client()

	err = ntfy.Send(context.Background(), types.Notification{
		Type:   types.EventNotification,
		Detail: "Container event: die (exit code 137)",
		Container: types.NotificationContainer{
			Name:     "api",
			HostName: "prod-host",
		},
		Subscription: types.SubscriptionConfig{
			Name: "Container died",
		},
		Timestamp: time.Now(),
	})
	require.NoError(t, err)

	assert.Equal(t, "alerts", payload.Topic)
	assert.Equal(t, "[prod-host] Container died - api", payload.Title)
	assert.Equal(t, "Alert: Container died\nType: event\nContainer: api\n\nContainer event: die (exit code 137)", payload.Message)
	assert.Equal(t, 5, payload.Priority)
}

func TestNtfyDispatcher_DefaultContentWhenTemplatesEmpty(t *testing.T) {
	var payload ntfyPayload
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		rw.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ntfy, err := NewNtfyDispatcher("test", srv.URL, "alerts", 0, "", "", "")
	require.NoError(t, err)
	ntfy.client = srv.Client()

	err = ntfy.Send(context.Background(), types.Notification{
		Detail: "plain detail",
		Container: types.NotificationContainer{
			Name: "api",
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "api", payload.Title)
	assert.Equal(t, "plain detail", payload.Message)
	assert.Equal(t, 3, payload.Priority)
}
