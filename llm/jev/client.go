package jev

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
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/httpclient"
)

// SecretFn resolves the bearer token at call time.
//
// The token is fetched per request rather than stored on the client so that a
// decrypted secret lives only for the duration of one call, and so the client
// itself never holds anything worth stealing from a heap dump taken later.
//
// It is an alias rather than a distinct type so a caller can share one secret
// source between the Jev and OpenAI adapters.
type SecretFn = httpclient.SecretFn

// Profile is the connection part of a Jev provider profile
// (docs/llm-dev/06-Jev接入层-SystemOne.md §8). The model must be a pinned
// version: an alias would drift and break replay.
type Profile struct {
	BaseURL               string
	Model                 string
	TimeoutSeconds        int
	MaxResponseBytes      int64
	ProxyURL              string
	AllowInsecureLoopback bool
	AllowPrivateNetwork   bool
	// Resolver overrides name resolution for tests. Production leaves it nil.
	Resolver func(ctx context.Context, host string) ([]net.IP, error)
}

// ErrModelMismatch marks a request whose model differs from the configured one.
var ErrModelMismatch = errors.New("request model differs from the configured model")

// ErrInvalidJSON marks a response body that is not valid JSON.
var ErrInvalidJSON = errors.New("response is not valid JSON")

// Client talks to a System One endpoint. It holds no credentials itself.
type Client struct {
	http             *http.Client
	endpoint         string
	model            string
	secretFn         SecretFn
	maxResponseBytes int64
}

// NewClient builds a Jev client from a profile and a secret callback.
func NewClient(p *Profile, secretFn SecretFn) (*Client, error) {
	if secretFn == nil {
		return nil, httpclient.ErrNilSecret
	}
	if !IsAcceptedModel(p.Model) {
		return nil, fmt.Errorf("%w: %q", ErrModelInvalid, p.Model)
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
	// Accept both spellings of the base URL: the bare site (the path is
	// appended here) and the full endpoint (a live 404 on 2026-10-16 showed an
	// operator pasting the complete /v1/systemone URL, which was then doubled).
	endpoint := strings.TrimRight(base.String(), "/")
	if !strings.HasSuffix(endpoint, SystemOnePath) {
		endpoint = httpclient.BasePath(base, SystemOnePath)
	}
	return &Client{
		http:             hc,
		endpoint:         endpoint,
		model:            p.Model,
		secretFn:         secretFn,
		maxResponseBytes: p.MaxResponseBytes,
	}, nil
}

// Model returns the pinned model the client was configured with.
func (c *Client) Model() string { return c.model }

// Endpoint returns the resolved System One URL.
func (c *Client) Endpoint() string { return c.endpoint }

// SystemOne sends a review request and returns a validated assessment.
//
// The returned assessment is only produced after ValidateResponse has accepted
// every answer, so a caller never receives a partially trusted reply.
func (c *Client) SystemOne(ctx context.Context, req *SystemOneRequest) (*Assessment, error) {
	assessment, _, err := c.SystemOneWithManifest(ctx, req)
	return assessment, err
}

// SystemOneWithManifest is SystemOne plus the input manifest binding the exact
// request and response bytes: the record that answers "what exactly was the
// judge given" without the original payloads, and the prerequisite for any
// order (docs 06 §6). The manifest is built only after the response validated,
// so a refused reply never yields one.
func (c *Client) SystemOneWithManifest(ctx context.Context, req *SystemOneRequest) (*Assessment, *InputManifest, error) {
	if c == nil {
		return nil, nil, errors.New("nil Jev client")
	}
	if req == nil {
		return nil, nil, fmt.Errorf("%w: nil request", ErrStateInvalid)
	}
	if req.Model != c.model {
		return nil, nil, fmt.Errorf("%w: requested %q, configured %q", ErrModelMismatch, req.Model, c.model)
	}
	body, err := req.Marshal()
	if err != nil {
		return nil, nil, err
	}

	payload, err := c.post(ctx, body)
	if err != nil {
		return nil, nil, err
	}

	var resp SystemOneResponse
	if err = json.Unmarshal(payload, &resp); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrInvalidJSON, err)
	}
	assessment, err := ValidateResponse(req, &resp)
	if err != nil {
		return nil, nil, err
	}
	state, ok := req.State.(ReviewState)
	if !ok {
		return nil, nil, fmt.Errorf("%w: the review state has an unexpected shape", ErrStateInvalid)
	}
	refs, err := manifestToolRefs(state.Evidence)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := BuildInputManifest(req, body, payload, state.Candidate, state.StrategyInstruction, refs)
	if err != nil {
		return nil, nil, err
	}
	return assessment, manifest, nil
}

// manifestToolRefs turns the retained evidence into the manifest's response
// references. The tool runner's refs follow tool:<name>:<callId> and
// decompose; any other shape is its own call identity (refs are unique by
// contract), because a name invented out of a non-conforming ref would be
// worse than an opaque one.
func manifestToolRefs(evidence []candidate.ToolEvidence) ([]ToolResponseRef, error) {
	refs := make([]ToolResponseRef, 0, len(evidence))
	for i := range evidence {
		name, callID := evidence[i].Ref, evidence[i].Ref
		if parts := strings.Split(evidence[i].Ref, ":"); len(parts) == 3 && parts[0] == "tool" && parts[1] != "" && parts[2] != "" {
			name, callID = parts[1], parts[2]
		}
		if strings.TrimSpace(name) == "" || strings.TrimSpace(callID) == "" {
			return nil, fmt.Errorf("%w: evidence ref %q cannot identify its call", ErrManifestIncomplete, evidence[i].Ref)
		}
		refs = append(refs, NewToolResponseRef(callID, name, &evidence[i]))
	}
	return refs, nil
}

// post performs the authenticated request.
func (c *Client) post(ctx context.Context, body []byte) ([]byte, error) {
	secret, err := c.secretFn(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving endpoint secret: %w", err)
	}
	if secret == "" {
		return nil, fmt.Errorf("%w: empty secret", httpclient.ErrNilSecret)
	}
	return httpclient.PostJSON(ctx, c.http, c.endpoint, secret, body, c.maxResponseBytes)
}

// defaultTimeout clamps a configured timeout to the accepted window.
func defaultTimeout(seconds int) time.Duration {
	if seconds <= 0 || seconds > maxTimeoutSeconds {
		return httpclient.DefaultTimeout
	}
	return time.Duration(seconds) * time.Second
}
