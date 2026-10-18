package candidate

// 对话配置提案（docs/llm-dev/16）：proposeAction 工具产出的**惰性**配置草稿。
// 它没有执行语义——真正生效必须由操作者在确认卡片上确认，调用与手动页面完全
// 相同的治理 RPC。因此本类型只承载参数：没有凭据、没有订单字段、没有候选动作。
const (
	// ActionStartOpenDecisionBatch drafts a batch open-decision job.
	ActionStartOpenDecisionBatch = "START_OPEN_DECISION_BATCH"
	// ActionStartPositionReview drafts a position review job.
	ActionStartPositionReview = "START_POSITION_REVIEW"
	// ActionSetMarketTracked drafts enabling or disabling one market.
	ActionSetMarketTracked = "SET_MARKET_TRACKED"
	// ActionCancelBatchJob drafts cancelling one batch job.
	ActionCancelBatchJob = "CANCEL_BATCH_JOB"
)

// ActionProposal is one inert configuration draft. The operator's confirmation
// card is the only path from this type to a write; the model never executes.
type ActionProposal struct {
	// Action is one of the Action* constants.
	Action string `json:"action"`
	// Summary is the model's display-only description of the draft.
	Summary string `json:"summary"`
	// MarketIDs are the run-vocabulary market ids a batch would cover.
	MarketIDs []string `json:"marketIds,omitempty"`
	// AllPositions marks a position review over the account's current
	// positions; the confirmation card resolves the ids at execution time.
	AllPositions bool `json:"allPositions,omitempty"`
	// StrategyVersionID is the approval-bound strategy version a batch runs.
	StrategyVersionID string `json:"strategyVersionId,omitempty"`
	// Actor is the operator identity the confirmation will record.
	Actor string `json:"actor,omitempty"`
	// MarketID is the single market a tracked-state change targets.
	MarketID string `json:"marketId,omitempty"`
	// Tracked is the requested tracked state.
	Tracked *bool `json:"tracked,omitempty"`
	// JobID names the batch job a cancel targets.
	JobID string `json:"jobId,omitempty"`
}
