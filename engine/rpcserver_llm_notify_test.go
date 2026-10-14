package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/communications/base"
	"github.com/thrasher-corp/gocryptotrader/config"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// TestLLMGetNotifyStatus covers the W7-06 readiness surface: readiness flags
// report, and the credential values never leave the config.
func TestLLMGetNotifyStatus(t *testing.T) {
	cfg := &config.Config{}
	cfg.Communications.TelegramConfig.Enabled = true
	cfg.Communications.TelegramConfig.VerificationToken = "telegram-token-secret"
	cfg.Communications.TelegramConfig.AuthorisedClients = map[string]int64{"op": 42, "alt": 43}
	cfg.Communications.WebhookConfig.Enabled = true
	cfg.Communications.WebhookConfig.URL = "https://hooks.example.com/abc?secret=url-query-secret"
	cfg.Communications.WebhookConfig.Secret = "webhook-shared-secret"

	server := &RPCServer{Engine: &Engine{Config: cfg}}
	res, err := server.LLMGetNotifyStatus(context.Background(), &gctrpc.LLMGetNotifyStatusRequest{})
	require.NoError(t, err)
	require.Len(t, res.GetChannels(), 2)

	tg := res.GetChannels()[0]
	assert.Equal(t, "telegram", tg.GetName())
	assert.True(t, tg.GetEnabled())
	assert.True(t, tg.GetCredentialSet(), "a set token should be visible as a boolean")
	assert.Equal(t, int64(2), tg.GetTargets())

	wh := res.GetChannels()[1]
	assert.Equal(t, "webhook", wh.GetName())
	assert.True(t, wh.GetEnabled())
	assert.True(t, wh.GetCredentialSet())
	assert.Equal(t, int64(1), wh.GetTargets())
	assert.Equal(t, "https://hooks.example.com", wh.GetEndpointHost(), "only the host is echoed, never the path or query")

	// The machine proof: no credential value appears anywhere in the response.
	rendered := res.String()
	for _, secret := range []string{"telegram-token-secret", "webhook-shared-secret", "url-query-secret", "/abc"} {
		assert.NotContainsf(t, rendered, secret, "the response should not echo %q", secret)
	}

	// An unparsable endpoint is reported as set-but-unparsable, not hidden.
	cfg.Communications.WebhookConfig.URL = "not a url"
	res, err = server.LLMGetNotifyStatus(context.Background(), &gctrpc.LLMGetNotifyStatusRequest{})
	require.NoError(t, err)
	assert.Contains(t, res.GetChannels()[1].GetEndpointHost(), "无法解析", "an endpoint that cannot be parsed should be flagged")

	var empty *RPCServer
	_, err = empty.LLMGetNotifyStatus(context.Background(), &gctrpc.LLMGetNotifyStatusRequest{})
	require.ErrorIs(t, err, errLLMNotifyUnavailable)
}

// TestLLMTestNotify covers the operator probe: the production notifier path
// is exercised and the relay state is reported honestly.
func TestLLMTestNotify(t *testing.T) {
	var captured []base.Event
	engine := &Engine{}
	engine.llmNotifier = &LLMNotifier{Push: func(evt base.Event) { captured = append(captured, evt) }}
	server := &RPCServer{Engine: engine}

	_, err := server.LLMTestNotify(context.Background(), &gctrpc.LLMTestNotifyRequest{})
	require.ErrorIs(t, err, ErrNotifyInputInvalid, "a probe without an actor is refused")

	res, err := server.LLMTestNotify(context.Background(), &gctrpc.LLMTestNotifyRequest{Actor: "op-1"})
	require.NoError(t, err)
	assert.Equal(t, EventNotifyTest, res.GetEventType())
	assert.False(t, res.GetRelayRunning(), "no communications manager is assembled in this fixture")
	require.Len(t, captured, 1)
	assert.Equal(t, EventNotifyTest, captured[0].Type)
	assert.Contains(t, captured[0].Message, "[info]")
	assert.Contains(t, captured[0].Message, "op-1", "the message should name the operator that probed the channel")

	var empty *RPCServer
	_, err = empty.LLMTestNotify(context.Background(), &gctrpc.LLMTestNotifyRequest{Actor: "op-1"})
	require.ErrorIs(t, err, errLLMNotifyUnavailable)
}
