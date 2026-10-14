package openai

import "errors"

// Sentinel errors. The pipeline maps these to the stable error-code vocabulary
// in docs/llm-dev/05-LLM接入层-OpenAI兼容.md §9, so each one must be
// distinguishable with errors.Is rather than by matching messages.
var (
	// ErrModelMismatch marks a request whose model differs from the profile's.
	// A silent override would make the audit trail name a model that never ran.
	ErrModelMismatch = errors.New("request model differs from the configured model")
	// ErrModelEmpty marks a profile without a model identifier.
	ErrModelEmpty = errors.New("model is empty")
	// ErrNilRequest marks a nil chat request.
	ErrNilRequest = errors.New("chat request is nil")
	// ErrNoMessages marks a request with nothing to say.
	ErrNoMessages = errors.New("chat request has no messages")
	// ErrRequestTooLarge marks a body over MaxRequestBytes.
	ErrRequestTooLarge = errors.New("chat request exceeds the byte limit")
	// ErrInvalidRole marks a message whose role is not in the contract.
	ErrInvalidRole = errors.New("chat request has an unsupported role")
	// ErrUnknownPromptVersion marks a prompt version with no text behind it.
	// Serving a fallback would record a version that never ran, which is worse
	// than refusing: a decision record must name the instructions it used.
	ErrUnknownPromptVersion = errors.New("unknown prompt version")
	// ErrInvalidJSON marks a response body that is not valid JSON.
	ErrInvalidJSON = errors.New("response is not valid JSON")
	// ErrNoChoices marks a well-formed reply with no completion in it.
	ErrNoChoices = errors.New("response contains no choices")
	// ErrContentMissing marks a non-tool reply with an empty body. An empty
	// answer cannot be a decision, so it is a failure rather than a HOLD.
	ErrContentMissing = errors.New("response content is empty")
	// ErrEmptyArguments marks a tool call with no arguments document.
	ErrEmptyArguments = errors.New("tool call has empty arguments")
	// ErrMalformedArguments marks tool arguments that are not valid JSON.
	ErrMalformedArguments = errors.New("tool call arguments are not valid JSON")
	// ErrUnsupportedLayer marks a probe layer this adapter cannot exercise.
	ErrUnsupportedLayer = errors.New("unsupported probe layer")
	// ErrTruncated marks a reply cut off by the token budget. The reference
	// project hit this with a 512 token budget, which is why the default is
	// higher and why the condition is a hard failure: a truncated reply cannot
	// be parsed into a candidate.
	ErrTruncated = errors.New("response was truncated")
	// ErrRefused marks a reply the provider withheld or the model declined.
	ErrRefused = errors.New("model refused the request")
	// ErrToolCallUnsupported marks an endpoint that answered without calling the
	// tool it was asked to call.
	ErrToolCallUnsupported = errors.New("endpoint did not call the requested tool")
	// ErrToolRoundIncomplete marks a tool round whose follow-up turn did not
	// complete, which makes the backfill path unusable.
	ErrToolRoundIncomplete = errors.New("tool round did not complete")
	// ErrStreamUnsupported marks an endpoint that did not answer with a stream.
	ErrStreamUnsupported = errors.New("endpoint did not stream the response")
	// ErrCandidateInvalid marks a reply whose candidate fails the contract in
	// llm/candidate. It wraps rather than restates, so the specific rule is
	// still visible with errors.Is.
	ErrCandidateInvalid = errors.New("candidate violates the contract")
	// ErrCandidateNotHold marks a probe reply that proposed exposure despite
	// having no data. A model that does this cannot be trusted with real facts.
	ErrCandidateNotHold = errors.New("candidate proposed action on an empty snapshot")
	// ErrCandidateNotionalNotExplicit marks a reply that omitted the notional
	// key instead of setting it to null. The distinction matters: an omitted key
	// means the model did not answer the question.
	ErrCandidateNotionalNotExplicit = errors.New("proposedMaxNotionalUsd must be present and null")
	// ErrCandidateNotObject marks candidate content that is not a JSON object.
	ErrCandidateNotObject = errors.New("candidate content is not a JSON object")
)
