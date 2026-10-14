// Package llm holds the vendor-neutral building blocks for the LLM/Jev decision
// subsystem.
//
// Design: docs/llm-dev/03-总体架构设计.md §4. The package deliberately does not
// import the engine: keeping the model-facing code free of engine dependencies
// is what allows this tree to be extracted into a separate worker process later
// without touching the decision pipeline (see OPEN-QUESTIONS Q22).
//
// Safety posture (docs/llm-dev/01-需求与产品目标.md §3):
//
//   - Model output is untrusted data, never an instruction.
//   - A model can only ever propose; deterministic code decides.
//   - Every failure converges on HOLD rather than a speculative order.
package llm

import "errors"

// Shared sentinel errors. Packages in this tree wrap these with %w so callers
// can rely on errors.Is while still receiving context.
var (
	// ErrNotImplemented is returned by adapter surfaces that exist to pin the
	// contract but have no backend yet.
	ErrNotImplemented = errors.New("not implemented")
	// ErrInvalidResponse marks a response that could not be parsed or that
	// violates its schema.
	ErrInvalidResponse = errors.New("invalid response")
	// ErrMissingData marks absent inputs that a caller must supply.
	ErrMissingData = errors.New("missing data")
	// ErrUnverified marks evidence that cannot be trusted for a decision.
	ErrUnverified = errors.New("unverified")
)
