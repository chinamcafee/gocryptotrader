package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/httpclient"
)

const probeCandidateJSON = `{"strategyVersionId":"00000000-0000-4000-8000-00000000dead",` +
	`"snapshotId":"00000000-0000-4000-8000-00000000beef","marketId":"PROBE","action":"HOLD",` +
	`"thesis":"No data was supplied, so there is nothing to act on.",` +
	`"evidenceRefs":["00000000-0000-4000-8000-00000000beef"],` +
	`"invalidation":"A populated snapshot would change this answer.",` +
	`"suggestedEntry":"","suggestedExit":"","proposedMaxNotionalUsd":null,` +
	`"uncertainty":"HIGH","ttlSeconds":30}`

// scriptedServer answers each request in turn with the next scripted body.
func scriptedServer(t *testing.T, bodies ...string) (server *httptest.Server, seen *[]string) {
	t.Helper()
	recorded := make([]string, 0, len(bodies))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		recorded = append(recorded, string(raw))
		index := len(recorded) - 1
		body := ""
		if index < len(bodies) {
			body = bodies[index]
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &recorded
}

func toolCallReply() string {
	return `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"",` +
		`"tool_calls":[{"id":"call_abc","type":"function","function":{"name":"getProbeFacts","arguments":"{\"key\":\"probe\"}"}}]},` +
		`"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}}`
}

func TestProbeAuthPasses(t *testing.T) {
	t.Parallel()

	srv := replyServer(t, http.StatusOK, chatReply("OK", FinishStop))
	c := newLocalClient(t, srv.URL)

	result := c.Probe(context.Background(), ProbeLayerAuth)
	require.True(t, result.Passed, "a reachable endpoint must pass the auth layer")
	assert.Equal(t, ProbeLayerAuth, result.Layer, "the layer should be recorded")
	assert.Empty(t, result.ErrorCode, "a passing probe reports no code")
	assert.Equal(t, 18, result.InputTokens+result.OutputTokens, "token usage should be recorded")
	assert.Equal(t, testModel, result.ResponseModel, "the served model should be recorded for drift checks")
	assert.GreaterOrEqual(t, result.LatencyMS, int64(0), "latency should be measured")
}

func TestProbeAuthFailures(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"unauthorized": {http.StatusUnauthorized, `{"error":{"message":"bad key"}}`, CodeAuthFailed},
		"forbidden":    {http.StatusForbidden, `{"error":{"message":"no access"}}`, CodeAuthFailed},
		"rate limited": {http.StatusTooManyRequests, `{}`, CodeRateLimited},
		"upstream":     {http.StatusBadGateway, `{}`, CodeUpstreamUnavailable},
		"not found":    {http.StatusNotFound, `{}`, "HTTP_404"},
	} {
		srv := replyServer(t, tc.status, tc.body)
		c := newLocalClient(t, srv.URL)
		result := c.Probe(context.Background(), ProbeLayerAuth)
		assert.Falsef(t, result.Passed, "%s should not pass", name)
		assert.Equalf(t, tc.want, result.ErrorCode, "%s should map to its own code", name)
		assert.NotEmptyf(t, result.ErrorMessage, "%s should explain the failure", name)
	}
}

func TestProbeAuthRejectsUnusableReplies(t *testing.T) {
	t.Parallel()

	srv := replyServer(t, http.StatusOK, chatReply(strings.Repeat("x", 10), FinishLength))
	c := newLocalClient(t, srv.URL)
	result := c.Probe(context.Background(), ProbeLayerAuth)
	assert.False(t, result.Passed, "a truncated reply should not pass")
	assert.Equal(t, CodeTruncated, result.ErrorCode, "truncation has its own code")

	srv = replyServer(t, http.StatusOK, chatReply("", FinishContentFilter))
	c = newLocalClient(t, srv.URL)
	result = c.Probe(context.Background(), ProbeLayerAuth)
	assert.False(t, result.Passed, "a filtered reply should not pass")
	assert.Equal(t, CodeRefused, result.ErrorCode, "a withheld reply should be reported as a refusal")

	refused := `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"","refusal":"I cannot help with that"},"finish_reason":"stop"}]}`
	srv = replyServer(t, http.StatusOK, refused)
	c = newLocalClient(t, srv.URL)
	result = c.Probe(context.Background(), ProbeLayerAuth)
	assert.False(t, result.Passed, "an explicit refusal should not pass")
	assert.Equal(t, CodeRefused, result.ErrorCode, "the refusal field should be read")

	srv = replyServer(t, http.StatusOK, "")
	c = newLocalClient(t, srv.URL)
	result = c.Probe(context.Background(), ProbeLayerAuth)
	assert.False(t, result.Passed, "an empty body should not pass")
	assert.Equal(t, CodeInvalidJSON, result.ErrorCode, "an unreadable body has its own code") //nolint:testifylint // not JSON values; encoded-compare misfires on the constant name
}

func TestProbeAuthSurfacesNetworkFailures(t *testing.T) {
	t.Parallel()

	c := newLocalClient(t, "http://127.0.0.1:1")
	result := c.Probe(context.Background(), ProbeLayerAuth)
	assert.False(t, result.Passed, "an unreachable endpoint should not pass")
	assert.Contains(t, []string{CodeNetworkError, CodeTimeout}, result.ErrorCode, "the failure should be a transport code")
}

func TestProbeToolCallPasses(t *testing.T) {
	t.Parallel()

	srv, seen := scriptedServer(t, toolCallReply(), chatReply("OK", FinishStop))
	c := newLocalClient(t, srv.URL)

	result := c.Probe(context.Background(), ProbeLayerToolCall)
	require.Truef(t, result.Passed, "a provider that completes the round must pass: %s", result.ErrorMessage)
	require.Len(t, *seen, 2, "the layer must run two rounds")

	first := (*seen)[0]
	assert.Contains(t, first, `"tools":[`, "the tool should be offered")
	assert.Contains(t, first, `"tool_choice":"auto"`, "the first round should let the model choose")

	second := (*seen)[1]
	assert.Contains(t, second, `"tool_call_id":"call_abc"`, "the tool result should reference the call it answers")
	assert.Contains(t, second, `"role":"tool"`, "the second round should carry a tool message")
	assert.Contains(t, second, `"tool_choice":"none"`, "the final round should forbid further tool calls")
	assert.Contains(t, second, `"role":"assistant"`, "the assistant turn that asked for the tool should be replayed")
}

func TestProbeToolCallFailures(t *testing.T) {
	t.Parallel()

	// A provider that ignores the tool and answers directly.
	srv := replyServer(t, http.StatusOK, chatReply("OK", FinishStop))
	c := newLocalClient(t, srv.URL)
	result := c.Probe(context.Background(), ProbeLayerToolCall)
	assert.False(t, result.Passed, "a reply without tool calls should not pass")
	assert.Equal(t, CodeToolCallUnsupported, result.ErrorCode, "a missing tool call has its own code")

	// The wrong function.
	wrongName := strings.ReplaceAll(toolCallReply(), "getProbeFacts", "someOtherTool")
	srv = replyServer(t, http.StatusOK, wrongName)
	c = newLocalClient(t, srv.URL)
	result = c.Probe(context.Background(), ProbeLayerToolCall)
	assert.False(t, result.Passed, "calling a different function should not pass")
	assert.Equal(t, CodeToolCallUnsupported, result.ErrorCode, "an unexpected function is a support failure")

	// Arguments that do not decode.
	badArgs := strings.ReplaceAll(toolCallReply(), `{\"key\":\"probe\"}`, `{not json`)
	srv, _ = scriptedServer(t, badArgs)
	c = newLocalClient(t, srv.URL)
	result = c.Probe(context.Background(), ProbeLayerToolCall)
	assert.False(t, result.Passed, "unparsable arguments should not pass")
	assert.Equal(t, CodeToolArgumentsInvalid, result.ErrorCode, "malformed arguments have their own code")

	// Arguments that decode but do not round-trip the value asked for.
	wrongValue := strings.ReplaceAll(toolCallReply(), `{\"key\":\"probe\"}`, `{\"key\":\"other\"}`)
	srv, _ = scriptedServer(t, wrongValue)
	c = newLocalClient(t, srv.URL)
	result = c.Probe(context.Background(), ProbeLayerToolCall)
	assert.False(t, result.Passed, "a mangled argument value should not pass")
	assert.Equal(t, CodeToolArgumentsInvalid, result.ErrorCode, "a value mismatch is an argument failure")

	// A call with no id, which cannot be backfilled.
	noID := strings.ReplaceAll(toolCallReply(), `"id":"call_abc"`, `"id":""`)
	srv, _ = scriptedServer(t, noID)
	c = newLocalClient(t, srv.URL)
	result = c.Probe(context.Background(), ProbeLayerToolCall)
	assert.False(t, result.Passed, "a call without an id should not pass")
	assert.Equal(t, CodeToolRoundIncomplete, result.ErrorCode, "an unusable id breaks the round")
}

func TestProbeToolCallRoundIncomplete(t *testing.T) {
	t.Parallel()

	// A follow-up turn that arrives but carries nothing usable is the round's
	// own failure.
	srv, _ := scriptedServer(t, toolCallReply(), chatReply("   ", FinishStop))
	c := newLocalClient(t, srv.URL)
	result := c.Probe(context.Background(), ProbeLayerToolCall)
	assert.False(t, result.Passed, "an empty follow-up turn should not pass")
	assert.Equal(t, CodeToolRoundIncomplete, result.ErrorCode, "an unfinished round has its own code")

	// A follow-up turn that cannot be read at all keeps the more specific
	// protocol code, because that is what an operator has to fix.
	srv, _ = scriptedServer(t, toolCallReply(), "")
	c = newLocalClient(t, srv.URL)
	result = c.Probe(context.Background(), ProbeLayerToolCall)
	assert.False(t, result.Passed, "a broken follow-up turn should not pass")
	assert.Equal(t, CodeInvalidJSON, result.ErrorCode, "an unreadable body should name the fault, not the stage") //nolint:testifylint // not JSON values; encoded-compare misfires on the constant name
	assert.Contains(t, result.ErrorMessage, "tool round", "the stage should still be visible in the message")
}

func TestProbeToolCallRoundFailureOnBrokenTransport(t *testing.T) {
	t.Parallel()

	srv, _ := scriptedServer(t, toolCallReply())
	c := newLocalClient(t, srv.URL)

	// The scripted server runs out of bodies, so the follow-up gets an empty
	// reply over a live connection, which must still be attributed to the round
	// rather than crash the probe.
	result := c.Probe(context.Background(), ProbeLayerToolCall)
	assert.False(t, result.Passed, "a missing follow-up reply should not pass")
	assert.NotEmpty(t, result.ErrorCode, "a failure should carry a code")
}

func TestProbeSchemaPasses(t *testing.T) {
	t.Parallel()

	srv, seen := scriptedServer(t, chatReply(probeCandidateJSON, FinishStop))
	c := newLocalClient(t, srv.URL)

	result := c.Probe(context.Background(), ProbeLayerSchema)
	require.Truef(t, result.Passed, "a HOLD candidate with an explicit null notional must pass: %s", result.ErrorMessage)
	assert.Contains(t, (*seen)[0], `"response_format":{"type":"json_object"}`, "the JSON mode should be requested")
}

func TestProbeSchemaRejectsUnsafeReplies(t *testing.T) {
	t.Parallel()

	openLong := strings.Replace(probeCandidateJSON, `"action":"HOLD"`, `"action":"OPEN_LONG"`, 1)
	openLong = strings.Replace(openLong, `"proposedMaxNotionalUsd":null`, `"proposedMaxNotionalUsd":"50"`, 1)

	missingKey := strings.Replace(probeCandidateJSON, `"proposedMaxNotionalUsd":null,`, "", 1)

	zeroNotional := strings.Replace(probeCandidateJSON, `"proposedMaxNotionalUsd":null`, `"proposedMaxNotionalUsd":""`, 1)

	wrongSnapshot := strings.ReplaceAll(probeCandidateJSON, `"evidenceRefs":["00000000-0000-4000-8000-00000000beef"]`, `"evidenceRefs":["some-other-ref"]`)

	badTTL := strings.Replace(probeCandidateJSON, `"ttlSeconds":30`, `"ttlSeconds":99999`, 1)

	for name, tc := range map[string]struct {
		content string
		want    string
	}{
		"proposed exposure on an empty snapshot": {openLong, CodeCandidateSchemaInvalid},
		"notional key omitted":                   {missingKey, CodeCandidateSchemaInvalid},
		"notional present but empty":             {zeroNotional, CodeCandidateSchemaInvalid},
		"evidence does not lead with snapshot":   {wrongSnapshot, CodeCandidateSchemaInvalid},
		"ttl out of range":                       {badTTL, CodeCandidateSchemaInvalid},
		"not an object":                          {`[1,2,3]`, CodeCandidateSchemaInvalid},
		"not json at all":                        {`I think you should hold.`, CodeCandidateSchemaInvalid},
	} {
		srv, _ := scriptedServer(t, chatReply(tc.content, FinishStop))
		c := newLocalClient(t, srv.URL)
		result := c.Probe(context.Background(), ProbeLayerSchema)
		assert.Falsef(t, result.Passed, "%s should not pass", name)
		assert.Equalf(t, tc.want, result.ErrorCode, "%s should map to %s", name, tc.want)
	}
}

func TestProbeSchemaReportsMissingFields(t *testing.T) {
	t.Parallel()

	// Drop the thesis, which the candidate contract requires.
	noThesis := strings.Replace(probeCandidateJSON, `"thesis":"No data was supplied, so there is nothing to act on.",`, "", 1)
	srv, _ := scriptedServer(t, chatReply(noThesis, FinishStop))
	c := newLocalClient(t, srv.URL)

	result := c.Probe(context.Background(), ProbeLayerSchema)
	assert.False(t, result.Passed, "a candidate missing a required field should not pass")
	assert.Equal(t, CodeCandidateIncomplete, result.ErrorCode, "a missing field should be reported as incomplete, not as an unreadable reply")
}

func TestProbeStreamPasses(t *testing.T) {
	t.Parallel()

	stream := "data: {\"model\":\"" + testModel + "\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"O\"}}]}\n\n" +
		"data: {\"model\":\"" + testModel + "\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"K\"}}]}\n\n" +
		"data: {\"model\":\"" + testModel + "\",\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":2}}\n\n" +
		"data: [DONE]\n\n"
	srv, seen := scriptedServer(t, stream)
	c := newLocalClient(t, srv.URL)

	result := c.Probe(context.Background(), ProbeLayerStream)
	require.Truef(t, result.Passed, "an SSE reply must pass the stream layer: %s", result.ErrorMessage)
	assert.Contains(t, (*seen)[0], `"stream":true`, "the request should ask to stream")
	assert.Equal(t, testModel, result.ResponseModel, "the model should be read from a frame")
	assert.Equal(t, 9, result.InputTokens, "usage should be read from the final frame")
	assert.Equal(t, 2, result.OutputTokens, "usage should be read from the final frame")
}

func TestProbeStreamFailures(t *testing.T) {
	t.Parallel()

	// A provider that ignores the stream flag.
	srv := replyServer(t, http.StatusOK, chatReply("OK", FinishStop))
	c := newLocalClient(t, srv.URL)
	result := c.Probe(context.Background(), ProbeLayerStream)
	assert.False(t, result.Passed, "a non-streaming reply should not pass")
	assert.Equal(t, CodeStreamUnsupported, result.ErrorCode, "a missing stream has its own code")

	// A stream that is cut off before the terminator.
	srv, _ = scriptedServer(t, "data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"O\"}}]}\n\n")
	c = newLocalClient(t, srv.URL)
	result = c.Probe(context.Background(), ProbeLayerStream)
	assert.False(t, result.Passed, "a stream without a terminator should not pass")
	assert.Equal(t, CodeStreamUnsupported, result.ErrorCode, "an unfinished stream is unsupported")

	// A frame that is not JSON.
	srv, _ = scriptedServer(t, "data: {oops\n\ndata: [DONE]\n\n")
	c = newLocalClient(t, srv.URL)
	result = c.Probe(context.Background(), ProbeLayerStream)
	assert.False(t, result.Passed, "an unreadable frame should not pass")
	assert.Equal(t, CodeInvalidJSON, result.ErrorCode, "a malformed frame is a JSON failure") //nolint:testifylint // not JSON values; encoded-compare misfires on the constant name
}

func TestParseSSE(t *testing.T) {
	t.Parallel()

	frames, done := ParseSSE([]byte("data: {\"a\":1}\r\n\r\ndata: [DONE]\r\n"))
	assert.Equal(t, []string{`{"a":1}`}, frames, "frames should be returned without their prefix")
	assert.True(t, done, "the terminator should be detected")

	frames, done = ParseSSE([]byte(": keep-alive\n\nevent: ping\ndata: {\"b\":2}\n"))
	assert.Equal(t, []string{`{"b":2}`}, frames, "comments and non-data lines should be skipped")
	assert.False(t, done, "a body without the terminator is not done")

	frames, done = ParseSSE(nil)
	assert.Empty(t, frames, "an empty body has no frames")
	assert.False(t, done, "an empty body is not done")
}

func TestProbeUnknownLayer(t *testing.T) {
	t.Parallel()

	srv := replyServer(t, http.StatusOK, chatReply("OK", FinishStop))
	c := newLocalClient(t, srv.URL)

	result := c.Probe(context.Background(), "TELEPATHY")
	assert.False(t, result.Passed, "an unknown layer should not pass")
	assert.Equal(t, CodeChatSchemaInvalid, result.ErrorCode, "an unknown layer is a caller bug and should be visible")

	var nilClient *Client
	result = nilClient.Probe(context.Background(), ProbeLayerAuth)
	assert.False(t, result.Passed, "a nil client should refuse rather than panic")
	assert.NotEmpty(t, result.ErrorCode, "a nil client should report a code")
}

func TestProbeAllRunsEveryLayer(t *testing.T) {
	t.Parallel()

	srv, seen := scriptedServer(t,
		chatReply("OK", FinishStop),
		toolCallReply(),
		chatReply("OK", FinishStop),
		chatReply(probeCandidateJSON, FinishStop),
		"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n",
	)
	c := newLocalClient(t, srv.URL)

	results := c.ProbeAll(context.Background())
	require.Len(t, results, 4, "every layer must run")
	assert.Equal(t, []string{ProbeLayerAuth, ProbeLayerToolCall, ProbeLayerSchema, ProbeLayerStream},
		[]string{results[0].Layer, results[1].Layer, results[2].Layer, results[3].Layer},
		"the layers must run in a stable order so a stored row is comparable")
	for _, result := range results {
		assert.Truef(t, result.Passed, "%s should pass: %s", result.Layer, result.ErrorMessage)
	}
	assert.GreaterOrEqual(t, len(*seen), 5, "the four layers should send their requests")
}

func TestProbeCarriesNoMarketData(t *testing.T) {
	t.Parallel()

	srv, seen := scriptedServer(t,
		chatReply("OK", FinishStop),
		toolCallReply(),
		chatReply("OK", FinishStop),
		chatReply(probeCandidateJSON, FinishStop),
		"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n",
	)
	c := newLocalClient(t, srv.URL)
	c.ProbeAll(context.Background())

	// A probe may run against a misconfigured endpoint, so the only thing it may
	// reveal is that a request happened. Describing that data is absent is
	// allowed and in fact required; naming an instrument, an exchange or a
	// timeframe would leak what the operator is watching.
	for _, forbidden := range []string{
		"btc", "eth", "usdt", "binance", "hyperliquid",
		"100000", "1h", "1m", "\"bid\"", "\"ask\"",
	} {
		for i, body := range *seen {
			assert.NotContainsf(t, strings.ToLower(body), forbidden, "request %d should not mention %q", i, forbidden)
		}
	}

	// The SCHEMA layer's whole point is that it supplies nothing, so the prompt
	// must say so. Without this the layer would test nothing.
	require.GreaterOrEqual(t, len(*seen), 4, "the layers must have sent their requests")
	assert.Contains(t, (*seen)[3], "No market data is available",
		"the schema probe must state that it supplies no data")
}

func TestErrorCodeMapping(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"nil":                    {nil, ""},
		"auth":                   {&httpclient.StatusError{StatusCode: http.StatusUnauthorized}, CodeAuthFailed},
		"rate limit":             {&httpclient.StatusError{StatusCode: http.StatusTooManyRequests}, CodeRateLimited},
		"server":                 {&httpclient.StatusError{StatusCode: http.StatusInternalServerError}, CodeUpstreamUnavailable},
		"client":                 {&httpclient.StatusError{StatusCode: http.StatusBadRequest}, "HTTP_400"},
		"too large":              {httpclient.ErrResponseTooLarge, CodeResponseTooLarge},
		"request too large":      {ErrRequestTooLarge, CodeRequestTooLarge},
		"invalid json":           {ErrInvalidJSON, CodeInvalidJSON},
		"stream":                 {ErrStreamUnsupported, CodeStreamUnsupported},
		"tool unsupported":       {ErrToolCallUnsupported, CodeToolCallUnsupported},
		"arguments":              {ErrMalformedArguments, CodeToolArgumentsInvalid},
		"empty arguments":        {ErrEmptyArguments, CodeToolArgumentsInvalid},
		"round":                  {ErrToolRoundIncomplete, CodeToolRoundIncomplete},
		"candidate":              {ErrCandidateInvalid, CodeCandidateSchemaInvalid},
		"candidate not hold":     {ErrCandidateNotHold, CodeCandidateSchemaInvalid},
		"candidate notional":     {ErrCandidateNotionalNotExplicit, CodeCandidateSchemaInvalid},
		"candidate not object":   {ErrCandidateNotObject, CodeCandidateSchemaInvalid},
		"truncated":              {ErrTruncated, CodeTruncated},
		"refused":                {ErrRefused, CodeRefused},
		"content missing":        {ErrContentMissing, CodeChatContentMissing},
		"no choices":             {ErrNoChoices, CodeChatSchemaInvalid},
		"no messages":            {ErrNoMessages, CodeChatSchemaInvalid},
		"role":                   {ErrInvalidRole, CodeChatSchemaInvalid},
		"model empty":            {ErrModelEmpty, CodeChatSchemaInvalid},
		"model mismatch":         {ErrModelMismatch, CodeChatSchemaInvalid},
		"unsupported layer":      {ErrUnsupportedLayer, CodeChatSchemaInvalid},
		"blocked address":        {httpclient.ErrBlockedAddress, CodeInvalidBaseURL},
		"insecure scheme":        {httpclient.ErrInsecureScheme, CodeInvalidBaseURL},
		"embedded credentials":   {httpclient.ErrEmbeddedCredentials, CodeInvalidBaseURL},
		"nil endpoint":           {httpclient.ErrNilEndpoint, CodeInvalidBaseURL},
		"deadline":               {context.DeadlineExceeded, CodeTimeout},
		"unclassified transport": {assert.AnError, CodeNetworkError},
	} {
		assert.Equalf(t, tc.want, ErrorCode(tc.err), "%s should map to %s", name, tc.want)
	}
}

func TestErrorCodeUnwrapsWrappedSentinels(t *testing.T) {
	t.Parallel()

	// The mapping must survive the wrapping style the codebase uses in practice,
	// so a specific rule stays visible through the wrapper.
	var cand candidate.Candidate
	err := cand.Validate()
	require.Error(t, err, "an empty candidate must fail its contract")

	wrapped := fmt.Errorf("%w: %w", ErrCandidateInvalid, err)
	assert.Equal(t, CodeCandidateIncomplete, ErrorCode(wrapped),
		"a wrapped candidate error must keep its specific code rather than the generic one")
}
