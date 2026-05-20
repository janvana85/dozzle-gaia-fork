package dispatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/amir20/dozzle/types"
	"github.com/rs/zerolog/log"
)

// NtfyDispatcher sends notifications to an ntfy server (ntfy.sh or self-hosted).
type NtfyDispatcher struct {
	Name            string
	ServerURL       string
	DefaultTopic    string
	DefaultPriority int // 1-5; 0 treated as 3 (default)
	Token           string
	client          *http.Client
}

// NewNtfyDispatcher creates a new ntfy dispatcher.
// serverURL is the base URL (e.g. "https://ntfy.sh" or "https://ntfy.example.com").
// priority 0 is treated as 3 (ntfy default). Valid range is 1-5.
func NewNtfyDispatcher(name, serverURL, topic string, priority int, token string) (*NtfyDispatcher, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("invalid ntfy server URL: %w", err)
	}
	if scheme := strings.ToLower(parsed.Scheme); scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("invalid ntfy server URL scheme %q: only http and https are allowed", parsed.Scheme)
	}
	if topic == "" {
		return nil, fmt.Errorf("ntfy topic is required")
	}
	if priority < 0 || priority > 5 {
		return nil, fmt.Errorf("ntfy priority must be between 1 and 5 (or 0 for default)")
	}

	return &NtfyDispatcher{
		Name:            name,
		ServerURL:       strings.TrimRight(serverURL, "/"),
		DefaultTopic:    topic,
		DefaultPriority: priority,
		Token:           token,
		client: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				DialContext:           safeDialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
	}, nil
}

type ntfyPayload struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title"`
	Message  string   `json:"message"`
	Priority int      `json:"priority,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

// Send implements the Dispatcher interface.
func (n *NtfyDispatcher) Send(ctx context.Context, notification types.Notification) error {
	topic := notification.NtfyTopic
	if topic == "" {
		topic = n.DefaultTopic
	}

	priority := notification.NtfyPriority
	if priority == 0 {
		priority = n.DefaultPriority
	}
	if priority == 0 {
		priority = 3
	}

	tags := notification.NtfyTags

	title := notification.Container.Name
	if title == "" {
		title = "Dozzle Alert"
	}

	payload := ntfyPayload{
		Topic:    topic,
		Title:    title,
		Message:  notification.Detail,
		Priority: priority,
		Tags:     tags,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal ntfy payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.ServerURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create ntfy request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	if n.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.Token)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Debug().
			Str("ntfy", n.Name).
			Str("topic", topic).
			Int("status_code", resp.StatusCode).
			Msg("ntfy returned non-success status code")
		return fmt.Errorf("ntfy returned status code %d", resp.StatusCode)
	}

	return nil
}
