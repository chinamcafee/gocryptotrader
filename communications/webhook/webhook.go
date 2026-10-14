// Package webhook is the generic webhook relay (docs/llm-dev 08 §5, Q19):
// enabled events are POSTed as JSON to an operator-configured URL, with three
// exponential-backoff retries. It exists because GoCryptoTrader ships no
// generic webhook channel, and the LLM alerting story needs one ("webhook 接
// 自建告警系统" — Q19's ruling).
package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/thrasher-corp/gocryptotrader/communications/base"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// Retry policy: three retries after the first attempt, exponential from
// WebhookRetryBaseDelay. Fixed by Q19; not configuration.
const (
	// WebhookMaxRetries is the number of retries after the initial attempt.
	WebhookMaxRetries = 3
	// WebhookRetryBaseDelay is the first backoff; it doubles each retry.
	WebhookRetryBaseDelay = 250 * time.Millisecond
	// WebhookRequestTimeout bounds one POST attempt.
	WebhookRequestTimeout = 10 * time.Second
)

// Payload is the JSON body one webhook POST carries (Q19: 事件类型/严重级/
// 摘要/详情).
type Payload struct {
	Type     string `json:"type"`
	Severity string `json:"severity,omitempty"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail,omitempty"`
	PushedAt string `json:"pushedAt"`
}

// Webhook pushes events to a configured URL.
type Webhook struct {
	base.Base
	URL    string
	Secret string

	// Client performs the POSTs; tests substitute a client pointed at httptest.
	Client *http.Client
	// Sleep waits between retries; tests substitute a no-op.
	Sleep func(time.Duration)

	mu        sync.Mutex
	connected bool
}

// Setup loads the webhook settings.
func (w *Webhook) Setup(config *base.CommunicationsConfig) {
	if config == nil {
		return
	}
	w.Enabled = config.WebhookConfig.Enabled
	w.URL = strings.TrimSpace(config.WebhookConfig.URL)
	w.Secret = config.WebhookConfig.Secret
}

// Connect validates the endpoint and marks the relay connected.
func (w *Webhook) Connect() error {
	if w == nil {
		return errors.New("webhook: nil relay")
	}
	if !w.Enabled {
		return nil
	}
	parsed, err := url.Parse(w.URL)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("webhook: url %q is invalid", w.URL)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("webhook: url %q must be http(s)", w.URL)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.connected = true
	return nil
}

// IsConnected reports the relay state.
func (w *Webhook) IsConnected() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.connected
}

// GetName names the medium for the comms status surface.
func (w *Webhook) GetName() string { return "webhook" }

// PushEvent posts one event with retries. A non-2xx response is a failure; the
// last failure is returned so the comms wrapper can log it.
func (w *Webhook) PushEvent(evt base.Event) error {
	if w == nil || !w.Enabled || !w.IsConnected() {
		return nil
	}
	payload := ParseMessage(evt)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhook: marshalling: %w", err)
	}
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: WebhookRequestTimeout}
	}
	sleep := w.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	var lastErr error
	delay := WebhookRetryBaseDelay
	for attempt := 0; attempt <= WebhookMaxRetries; attempt++ {
		if attempt > 0 {
			sleep(delay)
			delay *= 2
		}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, w.URL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("webhook: building the request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if w.Secret != "" {
			req.Header.Set("X-Webhook-Secret", w.Secret)
		}
		response, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		_ = response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("webhook: %s returned %d", w.urlForError(), response.StatusCode)
	}
	return lastErr
}

func (w *Webhook) urlForError() string {
	parsed, err := url.Parse(w.URL)
	if err != nil {
		return w.URL
	}
	return parsed.Redacted()
}

// ParseMessage turns the notifier's "[severity] summary | detail" phrasing back
// into structured fields. A message without the marker keeps summary=whole.
func ParseMessage(evt base.Event) Payload {
	payload := Payload{Type: evt.Type, Summary: evt.Message}
	if evt.Message == "" {
		return payload
	}
	if evt.Message[0] == '[' {
		if end := strings.Index(evt.Message, "] "); end > 1 {
			payload.Severity = evt.Message[1:end]
			payload.Summary = evt.Message[end+2:]
		}
	}
	if idx := strings.Index(payload.Summary, " | "); idx >= 0 {
		payload.Detail = payload.Summary[idx+3:]
		payload.Summary = payload.Summary[:idx]
	}
	return payload
}
