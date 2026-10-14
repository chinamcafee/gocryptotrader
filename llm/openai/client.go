package openai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/httpclient"
)

// Profile is the connection part of an LLM provider profile
// (docs/llm-dev/05-LLM接入层-OpenAI兼容.md §2.2). It carries no secret: the key
// arrives through a SecretFn at call time.
type Profile struct {
	// BaseURL is the provider root, usually ending in /v1. The chat path is
	// appended by the client.
	BaseURL string
	// Model is the provider model identifier.
	Model string
	// TimeoutSeconds bounds one request, clamped to the accepted window.
	TimeoutSeconds int
	// MaxResponseBytes bounds a reply body.
	MaxResponseBytes int64
	// MaxTokens bounds the reply length. Zero selects DefaultMaxTokens.
	MaxTokens int
	// ProxyURL is an explicit proxy. Environment proxy variables are never read.
	ProxyURL string
	// AllowInsecureLoopback permits http to a loopback host for local endpoints.
	AllowInsecureLoopback bool
	// AllowPrivateNetwork permits connecting to private ranges, including
	// loopback, and is required for a local endpoint.
	AllowPrivateNetwork bool
	// Resolver overrides name resolution for tests. Production leaves it nil.
	Resolver func(ctx context.Context, host string) ([]net.IP, error)
}

// Client talks to an OpenAI-compatible endpoint. It holds no credentials.
type Client struct {
	http             *http.Client
	endpoint         string
	model            string
	maxTokens        int
	maxResponseBytes int64
	secretFn         httpclient.SecretFn
}

// NewClient builds a client from a profile and a secret callback.
func NewClient(p *Profile, secretFn httpclient.SecretFn) (*Client, error) {
	if secretFn == nil {
		return nil, httpclient.ErrNilSecret
	}
	if strings.TrimSpace(p.Model) == "" {
		return nil, ErrModelEmpty
	}
	timeout := defaultTimeout(p.TimeoutSeconds)
	hc, err := httpclient.New(&httpclient.Options{
		BaseURL:               p.BaseURL,
		Timeout:               timeout,
		MaxResponseBytes:      p.MaxResponseBytes,
		ProxyURL:              p.ProxyURL,
		AllowInsecureLoopback: p.AllowInsecureLoopback,
		AllowPrivateNetwork:   p.AllowPrivateNetwork,
		Resolver:              p.Resolver,
	})
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(strings.TrimSpace(p.BaseURL))
	if err != nil {
		return nil, fmt.Errorf("parsing endpoint: %w", err)
	}
	maxTokens := p.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	return &Client{
		http:             hc,
		endpoint:         httpclient.BasePath(base, ChatCompletionsPath),
		model:            p.Model,
		maxTokens:        maxTokens,
		maxResponseBytes: p.MaxResponseBytes,
		secretFn:         secretFn,
	}, nil
}

// Model returns the configured model identifier.
func (c *Client) Model() string { return c.model }

// Endpoint returns the resolved chat completions URL.
func (c *Client) Endpoint() string { return c.endpoint }

// MaxTokens returns the reply budget applied when a request does not set one.
func (c *Client) MaxTokens() int { return c.maxTokens }

// ChatCompletion sends one chat completion request and returns the raw reply.
//
// The reply is decoded and shape-checked but not interpreted: turning content
// into a candidate is the pipeline's job, and keeping that boundary means this
// client can be reused by the probe, the chat path and the decision path without
// any of them inheriting another's acceptance rules.
func (c *Client) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if c == nil {
		return nil, errors.New("nil OpenAI client")
	}
	if req == nil {
		return nil, ErrNilRequest
	}
	if err := c.normaliseRequest(req); err != nil {
		return nil, err
	}
	body, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	payload, err := c.post(ctx, body, c.maxResponseBytes)
	if err != nil {
		return nil, err
	}
	return ParseChatResponse(payload)
}

// Post sends an already-serialised body to the chat endpoint, so the probe can
// reuse the transport while applying its own response cap.
func (c *Client) Post(ctx context.Context, body []byte, maxBytes int64) ([]byte, error) {
	if c == nil {
		return nil, errors.New("nil OpenAI client")
	}
	return c.post(ctx, body, maxBytes)
}

// post performs the authenticated request.
func (c *Client) post(ctx context.Context, body []byte, maxBytes int64) ([]byte, error) {
	secret, err := c.secretFn(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving endpoint secret: %w", err)
	}
	if secret == "" {
		return nil, fmt.Errorf("%w: empty secret", httpclient.ErrNilSecret)
	}
	if maxBytes <= 0 {
		maxBytes = c.maxResponseBytes
	}
	return httpclient.PostJSON(ctx, c.http, c.endpoint, secret, body, maxBytes)
}

// ParseChatResponse decodes a reply body and checks its shape.
//
// It is exported so a recorded reply can be re-parsed during replay without
// re-sending the request, and so a caller can test the acceptance rules without
// a live endpoint.
func ParseChatResponse(payload []byte) (*ChatResponse, error) {
	var resp ChatResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidJSON, err)
	}
	if len(resp.Choices) == 0 {
		// Some providers put the reason in an error envelope with HTTP 200. The
		// envelope is surfaced instead of the empty choice list, because "why"
		// is what a caller needs to act.
		var envelope ErrorEnvelope
		if err := json.Unmarshal(payload, &envelope); err == nil && envelope.ErrorMessage() != "" {
			return nil, fmt.Errorf("%w: %s", ErrNoChoices, envelope.ErrorMessage())
		}
		return nil, ErrNoChoices
	}
	return &resp, nil
}

// normaliseRequest fills defaults and refuses a request that contradicts the
// profile.
func (c *Client) normaliseRequest(req *ChatRequest) error {
	switch {
	case strings.TrimSpace(req.Model) == "":
		req.Model = c.model
	case req.Model != c.model:
		return fmt.Errorf("%w: requested %q, configured %q", ErrModelMismatch, req.Model, c.model)
	}
	if len(req.Messages) == 0 {
		return ErrNoMessages
	}
	for i := range req.Messages {
		if !validRole(req.Messages[i].Role) {
			return fmt.Errorf("%w: %q", ErrInvalidRole, req.Messages[i].Role)
		}
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = c.maxTokens
	}
	return nil
}

func validRole(role string) bool {
	switch role {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		return true
	default:
		return false
	}
}

// defaultTimeout clamps a configured timeout to the accepted window.
func defaultTimeout(seconds int) time.Duration {
	if seconds <= 0 || seconds > maxTimeoutSeconds {
		return httpclient.DefaultTimeout
	}
	return time.Duration(seconds) * time.Second
}

// ContentOf returns the reply text of the first choice, refusing an empty body
// on a reply that was not a tool call.
//
// A finished reply with no content and no tool calls is a provider failure, not
// an empty answer, so it becomes an error rather than a blank string that a
// caller might mistake for "the model said nothing".
func ContentOf(resp *ChatResponse) (string, error) {
	if resp == nil {
		return "", ErrNoChoices
	}
	msg, err := resp.FirstMessage()
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(msg.Content)
	if text == "" && len(msg.ToolCalls) == 0 {
		return "", ErrContentMissing
	}
	return text, nil
}
