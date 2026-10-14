package webhook

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/communications/base"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// M5-16 测试：载荷成文（severity/summary/detail 拆解）、secret 头、退避重试、连接校验。

func TestParseMessageStructuresTheNotifierPhrasing(t *testing.T) {
	t.Parallel()

	payload := ParseMessage(base.Event{Type: "llm_order_unknown", Message: "[high] ORDER RESULT UNKNOWN on i1; account flagged for reconciliation | {\"intentId\":\"i1\"}"})
	assert.Equal(t, "llm_order_unknown", payload.Type)
	assert.Equal(t, "high", payload.Severity)
	assert.Equal(t, "ORDER RESULT UNKNOWN on i1; account flagged for reconciliation", payload.Summary)
	assert.JSONEq(t, `{"intentId":"i1"}`, payload.Detail)

	// A message without the marker keeps summary=whole, no severity.
	payload = ParseMessage(base.Event{Type: "order", Message: "plain message"})
	assert.Empty(t, payload.Severity)
	assert.Equal(t, "plain message", payload.Summary)

	assert.Equal(t, Payload{Type: "t"}, ParseMessage(base.Event{Type: "t"}))
}

func TestPushEventPostsThePayloadWithSecret(t *testing.T) {
	t.Parallel()

	var gotBody []byte
	var gotSecret string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecret = r.Header.Get("X-Webhook-Secret")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	relay := &Webhook{Client: server.Client(), Sleep: func(time.Duration) {}}
	relay.Setup(&base.CommunicationsConfig{WebhookConfig: base.WebhookConfig{Enabled: true, URL: server.URL, Secret: "s3cret"}})
	require.NoError(t, relay.Connect())
	assert.True(t, relay.IsConnected())
	assert.Equal(t, "webhook", relay.GetName())

	require.NoError(t, relay.PushEvent(base.Event{Type: "llm_halt_set", Message: "[high] HALT FULL: clock drift"}))
	assert.Equal(t, "s3cret", gotSecret)
	var payload Payload
	require.NoError(t, json.Unmarshal(gotBody, &payload))
	assert.Equal(t, "llm_halt_set", payload.Type)
	assert.Equal(t, "high", payload.Severity)
	assert.Equal(t, "HALT FULL: clock drift", payload.Summary)
}

func TestPushEventRetriesWithExponentialBackoff(t *testing.T) {
	t.Parallel()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var sleeps []time.Duration
	relay := &Webhook{Client: server.Client(), Sleep: func(d time.Duration) { sleeps = append(sleeps, d) }}
	relay.Setup(&base.CommunicationsConfig{WebhookConfig: base.WebhookConfig{Enabled: true, URL: server.URL}})
	require.NoError(t, relay.Connect())
	require.NoError(t, relay.PushEvent(base.Event{Type: "t", Message: "m"}))
	assert.Equal(t, 3, attempts, "the third attempt succeeds")
	assert.Equal(t, []time.Duration{WebhookRetryBaseDelay, 2 * WebhookRetryBaseDelay}, sleeps)

	// Giving up after three retries surfaces the last failure.
	alwaysFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer alwaysFail.Close()
	sleeps = nil
	relay = &Webhook{Client: alwaysFail.Client(), Sleep: func(d time.Duration) { sleeps = append(sleeps, d) }}
	relay.Setup(&base.CommunicationsConfig{WebhookConfig: base.WebhookConfig{Enabled: true, URL: alwaysFail.URL}})
	require.NoError(t, relay.Connect())
	err := relay.PushEvent(base.Event{Type: "t", Message: "m"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
	assert.Len(t, sleeps, WebhookMaxRetries, "initial attempt plus three retries")
	assert.Equal(t, []time.Duration{WebhookRetryBaseDelay, 2 * WebhookRetryBaseDelay, 4 * WebhookRetryBaseDelay}, sleeps)
}

func TestConnectValidationAndDisabledNoOp(t *testing.T) {
	t.Parallel()

	relay := &Webhook{}
	relay.Setup(&base.CommunicationsConfig{WebhookConfig: base.WebhookConfig{Enabled: true, URL: "not-a-url"}})
	assert.Error(t, relay.Connect())

	relay = &Webhook{}
	relay.Setup(&base.CommunicationsConfig{WebhookConfig: base.WebhookConfig{Enabled: true, URL: "ftp://example.com"}})
	assert.Error(t, relay.Connect())

	// Disabled: no connection, push is a silent no-op.
	disabled := &Webhook{}
	disabled.Setup(&base.CommunicationsConfig{})
	require.NoError(t, disabled.Connect())
	assert.False(t, disabled.IsConnected())
	require.NoError(t, disabled.PushEvent(base.Event{Type: "t"}))

	var nilRelay *Webhook
	assert.False(t, nilRelay.IsConnected())
	require.NoError(t, nilRelay.PushEvent(base.Event{Type: "t"}))
	assert.Error(t, nilRelay.Connect())
}
