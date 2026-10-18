package engine

import "context"

// ToolProgress is one live progress update from a running tool. It travels to
// the chat stream only; the decision path has no progress sink and drops it.
type ToolProgress struct {
	Tool    string
	Phase   string
	Done    int
	Total   int
	Message string
}

type toolProgressKey struct{}

// withToolProgress attaches a per-call progress sink to the tool's context.
// The sink lives on the context rather than on the shared runner, so two
// concurrent turns can never see each other's progress.
func withToolProgress(ctx context.Context, sink func(ToolProgress)) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, toolProgressKey{}, sink)
}

// reportToolProgress emits one update when a sink is attached; callers without
// one (the decision path, tests) are a no-op.
func reportToolProgress(ctx context.Context, progress ToolProgress) {
	sink, ok := ctx.Value(toolProgressKey{}).(func(ToolProgress))
	if !ok || sink == nil {
		return
	}
	sink(progress)
}
