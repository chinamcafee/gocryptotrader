package jev

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/httpclient"
)

func staticSecret(string) SecretFn {
	return func(context.Context) (string, error) { return "test-token", nil }
}

// newLocalClient builds a client that may reach an httptest server.
func newLocalClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	c, err := NewClient(&Profile{
		BaseURL:               serverURL,
		Model:                 goodModel,
		TimeoutSeconds:        5,
		AllowInsecureLoopback: true,
		AllowPrivateNetwork:   true,
	}, staticSecret("test-token"))
	require.NoError(t, err, "a loopback profile with private access must be accepted")
	return c
}

func TestNewClientRejectsBadProfiles(t *testing.T) {
	t.Parallel()

	_, err := NewClient(&Profile{BaseURL: "https://api.example.com", Model: goodModel}, nil)
	require.ErrorIs(t, err, httpclient.ErrNilSecret, "a nil secret callback must be refused")

	_, err = NewClient(&Profile{BaseURL: "https://api.example.com", Model: "jev-latest"}, staticSecret("x"))
	require.ErrorIs(t, err, ErrModelInvalid, "an alias model must be refused so decisions stay replayable")

	_, err = NewClient(&Profile{BaseURL: "", Model: goodModel}, staticSecret("x"))
	require.Error(t, err, "an empty endpoint must be refused")

	_, err = NewClient(&Profile{BaseURL: "http://api.example.com", Model: goodModel}, staticSecret("x"))
	require.Error(t, err, "plain http to a public host must be refused")
}

func TestNewClientResolvesTheSystemOnePath(t *testing.T) {
	t.Parallel()

	c, err := NewClient(&Profile{BaseURL: "https://api.typesafe.ai/", Model: goodModel}, staticSecret("x"))
	require.NoError(t, err, "a well-formed profile must be accepted")
	assert.Equal(t, "https://api.typesafe.ai/v1/systemone", c.Endpoint(), "the contract path should be appended")
	assert.Equal(t, goodModel, c.Model(), "the pinned model should be exposed")
}

func TestSystemOneHappyPath(t *testing.T) {
	t.Parallel()

	var gotPath, gotAuth string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   goodModel,
			"answers": validAnswers(),
			"usage":   map[string]int{"input_tokens": 1081, "output_tokens": 89},
		})
	}))
	defer server.Close()

	client := newLocalClient(t, server.URL)
	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building must succeed")

	got, err := client.SystemOne(context.Background(), req)
	require.NoError(t, err, "a well-formed reply must be accepted")
	require.NotNil(t, got, "an assessment must be returned")
	assert.Equal(t, criteriaOpenLong, got.CandidateAction.Choice, "the chosen action should be surfaced")
	assert.Equal(t, 1081, got.Usage.InputTokens, "token usage should be surfaced")

	assert.Equal(t, "/v1/systemone", gotPath, "the contract path should be used")
	assert.Equal(t, "Bearer test-token", gotAuth, "the bearer token should be sent")
	assert.Contains(t, string(gotBody), EvidenceTreatment, "the untrusted-data framing should reach the endpoint")
	assert.Contains(t, string(gotBody), "RETAINED_LLM_TOOL_RESPONSES", "the evidence provenance should reach the endpoint")
}

func TestSystemOneRejectsModelMismatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := newLocalClient(t, server.URL)
	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building must succeed")
	req.Model = "jev-9.9.9"

	_, err = client.SystemOne(context.Background(), req)
	require.ErrorIs(t, err, ErrModelMismatch, "a request for another model must be refused before sending")
	assert.Equal(t, CodeModelInvalid, ErrorCode(err), "the failure should map to the pinned-model code")
}

func TestSystemOneErrorCodes(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		status   int
		body     string
		wantCode string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{}`, wantCode: CodeAuthFailed},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{}`, wantCode: CodeRateLimited},
		{name: "server error", status: http.StatusBadGateway, body: `{}`, wantCode: CodeUpstreamUnavailable},
		{name: "not json", status: http.StatusOK, body: `not json`, wantCode: CodeInvalidJSON},
		{name: "schema invalid", status: http.StatusOK, body: `{"model":"jev-1.13.0","answers":{"candidate_action":{"type":"choice"}}}`, wantCode: CodeSchemaInvalid},
		{
			name: "model drift", status: http.StatusOK, wantCode: CodeModelDrift,
			body: `{"model":"jev-1.14.0","answers":` + string(validAnswers()) + `}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client := newLocalClient(t, server.URL)
			req, err := BuildReviewRequest(testInput())
			require.NoError(t, err, "building must succeed")

			_, err = client.SystemOne(context.Background(), req)
			require.Error(t, err, "the reply must be rejected")
			assert.Equal(t, tc.wantCode, ErrorCode(err), "the failure should map to the stable vocabulary")
		})
	}
}

func TestSystemOneRefusesOversizedReply(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","pad":"` + strings.Repeat("x", 8192) + `"}`))
	}))
	defer server.Close()

	client, err := NewClient(&Profile{
		BaseURL:               server.URL,
		Model:                 goodModel,
		MaxResponseBytes:      512,
		AllowInsecureLoopback: true,
		AllowPrivateNetwork:   true,
	}, staticSecret("x"))
	require.NoError(t, err, "construction must succeed")

	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building must succeed")
	_, err = client.SystemOne(context.Background(), req)
	assert.Equal(t, CodeResponseTooLarge, ErrorCode(err), "an oversized reply should be refused")
}

func TestSystemOnePropagatesSecretFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	secretErr := errors.New("vault is locked")
	client, err := NewClient(&Profile{
		BaseURL: server.URL, Model: goodModel,
		AllowInsecureLoopback: true, AllowPrivateNetwork: true,
	}, func(context.Context) (string, error) { return "", secretErr })
	require.NoError(t, err, "construction must succeed")

	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building must succeed")
	_, err = client.SystemOne(context.Background(), req)
	require.ErrorIs(t, err, secretErr, "a secret failure must be surfaced rather than swallowed")
}

func TestSystemOneHonoursContextDeadline(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = w.Write([]byte(`{}`))
	}))
	defer func() { close(release); server.Close() }()

	client := newLocalClient(t, server.URL)
	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building must succeed")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = client.SystemOne(ctx, req)
	require.Error(t, err, "the call must fail when the deadline passes")
	assert.Equal(t, CodeTimeout, ErrorCode(err), "a deadline should map to the timeout code")
}

func TestSystemOneRejectsNilRequest(t *testing.T) {
	t.Parallel()

	client := newLocalClient(t, "http://127.0.0.1:1")
	_, err := client.SystemOne(context.Background(), nil)
	require.ErrorIs(t, err, ErrStateInvalid, "a nil request must be refused")

	var nilClient *Client
	_, err = nilClient.SystemOne(context.Background(), &SystemOneRequest{})
	require.Error(t, err, "a nil client must be refused rather than panicking")
}

func probeAnswers() json.RawMessage {
	return json.RawMessage(`{
	  "synthetic_choice": {
	    "type": "choice",
	    "choice": "expected",
	    "probabilities": {"expected": 0.8, "other": 0.2},
	    "confidence": 0.9
	  },
	  "synthetic_score": {
	    "type": "score",
	    "score": 1.0,
	    "probabilities": {"0": 0.0, "1": 1.0, "2": 0.0},
	    "legend": ["Insufficient", "Mixed", "Adequate"],
	    "confidence": 0.8
	  },
	  "synthetic_noul": { "type": "noul", "noul": 1.0 }
	}`)
}

func TestProbeHappyPath(t *testing.T) {
	t.Parallel()

	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   goodModel,
			"answers": probeAnswers(),
			"usage":   map[string]int{"input_tokens": 398, "output_tokens": 75},
		})
	}))
	defer server.Close()

	result := newLocalClient(t, server.URL).Probe(context.Background())
	require.Truef(t, result.Passed, "a well-formed probe reply must pass: %s", result.ErrorMessage)
	assert.Empty(t, result.ErrorCode, "a passing probe carries no error code")
	assert.Equal(t, ProbeLayerSystemOne, result.Layer, "the layer should be recorded")
	assert.Equal(t, 398, result.InputTokens, "token usage should be recorded")
	assert.Equal(t, 75, result.OutputTokens, "token usage should be recorded")
	assert.Equal(t, goodModel, result.ResponseModel, "the served model should be recorded")

	// A passing probe proves capability and nothing about the market: the request
	// must carry only the disclaimer.
	assert.Contains(t, string(body), "there is no real market", "the probe state should declare itself synthetic")
	for _, forbidden := range []string{"BTC", "USDT", "marketId", "price", "candle", "position"} {
		assert.NotContainsf(t, string(body), forbidden, "the probe should not carry market data (%s)", forbidden)
	}
}

func TestProbeReportsFailuresWithoutError(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		status    int
		body      string
		wantCode  string
		wantEmpty bool
	}{
		{name: "auth", status: http.StatusUnauthorized, body: `{}`, wantCode: CodeAuthFailed},
		{name: "invalid json", status: http.StatusOK, body: `nope`, wantCode: CodeInvalidJSON},
		{
			name: "model drift", status: http.StatusOK, wantCode: CodeModelDrift,
			body: `{"model":"jev-1.12.0","answers":` + string(probeAnswers()) + `}`,
		},
		{
			name: "schema invalid", status: http.StatusOK, wantEmpty: true,
			body: `{"model":"jev-1.13.0","answers":{"synthetic_choice":{"type":"choice"}}}`,
		},
		{
			name: "distribution invalid", status: http.StatusOK, wantEmpty: true,
			body: `{"model":"jev-1.13.0","answers":{"synthetic_choice":{"type":"choice","choice":"expected","probabilities":{"expected":0.4,"other":0.4},"confidence":0.5},"synthetic_score":{"type":"score","score":1.0,"probabilities":{"0":0.0,"1":1.0,"2":0.0},"legend":["a","b","c"],"confidence":0.5},"synthetic_noul":{"type":"noul","noul":1.0}}}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			result := newLocalClient(t, server.URL).Probe(context.Background())
			assert.False(t, result.Passed, "a broken reply should not pass")
			if tc.wantEmpty {
				assert.NotEmpty(t, result.ErrorCode, "a failure should always carry a code")
				return
			}
			assert.Equal(t, tc.wantCode, result.ErrorCode, "the failure should map to the stable vocabulary")
			assert.NotEmpty(t, result.ErrorMessage, "a failure should carry a readable message")
		})
	}
}

func TestProbeOnNilClient(t *testing.T) {
	t.Parallel()

	var nilClient *Client
	result := nilClient.Probe(context.Background())
	assert.False(t, result.Passed, "a nil client cannot pass a probe")
	assert.NotEmpty(t, result.ErrorCode, "a nil client should report a code rather than panicking")
}

func TestErrorCodeVocabulary(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		want string
	}{
		{err: nil, want: ""},
		{err: ErrRequestTooLarge, want: CodeRequestTooLarge},
		{err: ErrChoiceInvalid, want: CodeChoiceInvalid},
		{err: ErrDistributionInvalid, want: CodeChoiceInvalid},
		{err: ErrProbabilityInvalid, want: CodeChoiceInvalid},
		{err: ErrScoreInvalid, want: CodeScoreInvalid},
		{err: ErrNoulInvalid, want: CodeNoulInvalid},
		{err: ErrModelDrift, want: CodeModelDrift},
		{err: ErrAnswerMissing, want: CodeSchemaInvalid},
		{err: httpclient.ErrInsecureScheme, want: CodeInvalidBaseURL},
	} {
		assert.Equalf(t, tc.want, ErrorCode(tc.err), "the mapping should be stable for %v", tc.err)
	}
}
