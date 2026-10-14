package engine

import (
	"context"
	"fmt"
	"time"
)

// M4-16 SHADOW 模式调度（docs 11 §6：定时自动分析，只记录不执行）。
//
// 本层是 M4 链路的组装位：调度去抖 → 前置门禁（零成本）→ 分析流水线 →
// Jev 评估 → 决策合成 →（如到风控位）硬风控 + 数量重算 → 审计链落账。
// 每一段构件都已单独就位，这里把它们按 07 的文档序串成一次"决策"。
//
// SHADOW 保证不是一个开关，而是一个结构事实：本类型**没有订单提交接缝**，
// 物理上无法下单——ALLOW 在这里的含义是"若在可执行模式下，这一单会走到
// 订单落地"（M5 才执行）。shadow 运行只产出记录。
//
// 组装位也在这里收束两条等待已久的语义：
//   - **门禁拒绝也落账**：前置门禁是决策的一部分（"为什么今天没跑这个市场"
//     必须可回答），理由码随 decision_run 落库。
//   - **Jev 失败不是错误是事实**：BuildJev 的基建故障与 Review 的返回错误
//     一律走 JevError 通道，由分诊层映射为 JEV_UNAVAILABLE 族。

// ShadowRunRequest is one scheduled analysis.
type ShadowRunRequest struct {
	StrategyVersionID   string
	AccountID           string
	MarketID            string
	StrategyInstruction string
	ProfileRevisionID   string
	JevRevisionID       string
	LLMModel            string
	JevModel            string
	RunMode             string
	PairEpoch           int64
	EnvelopeHash        string
	// PreGates are the zero-cost gate facts; they are evaluated before any
	// model is consulted.
	PreGates PreGateInput
	// Assemble builds the market snapshot.
	Assemble func(ctx context.Context) (*SnapshotResult, error)
	// BuildJev assembles the judge request from the analyst's outcome (evidence,
	// manifest, reference price...). A nil builder means the run stops after
	// synthesis (analysis-only shadow).
	BuildJev func(ctx context.Context, analysis *AnalysisOutcome) (JevReviewRequest, error)
	// RiskFacts supplies the hard-risk and sizing inputs once synthesis asks for
	// them. Nil means the run records the awaiting-verdict without evaluating.
	RiskFacts func(ctx context.Context, analysis *AnalysisOutcome, jev *JevReviewOutcome) (*RiskInput, *SizingInput, error)
	// Execute is the production execution bridge (M5): the trigger consumer
	// calls it after a run completes with a full ALLOW chain, and it is a
	// no-op for modes that do not execute. The ShadowRunner itself never
	// touches it -- shadow mode keeps its structural promise.
	Execute func(ctx context.Context, run *ShadowRunResult) (*DispatchOutcome, error)
}

// ShadowRunResult is one run's recorded outcome.
type ShadowRunResult struct {
	// Skipped marks a debounce skip: nothing was recorded.
	Skipped    bool          `json:"skipped"`
	SkipReason string        `json:"skipReason,omitempty"`
	RetryAfter time.Duration `json:"retryAfter,omitempty"`

	Decision      string                `json:"decision,omitempty"`
	Reason        string                `json:"reason,omitempty"`
	DecisionRunID string                `json:"decisionRunId,omitempty"`
	Record        *DecisionRecordResult `json:"record,omitempty"`
	Synthesis     *SynthesisOutcome     `json:"synthesis,omitempty"`
	Risk          *RiskResult           `json:"risk,omitempty"`
	Sizing        *SizingResult         `json:"sizing,omitempty"`
}

// ShadowRunner assembles and records one shadow decision at a time.
type ShadowRunner struct {
	Scheduler *EvaluationScheduler
	Analysis  *AnalysisPipeline
	Jev       *JevPipeline
	Recorder  *DecisionRecorder
	Now       func() time.Time
}

// RunOnce executes the documented order for one market and records the result.
func (r *ShadowRunner) RunOnce(ctx context.Context, req *ShadowRunRequest) (*ShadowRunResult, error) {
	if r == nil || r.Scheduler == nil || r.Analysis == nil || r.Recorder == nil {
		return nil, ErrChatNotConfigured
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}

	// 1. Debounce (M4-06): a skip does not extend the window and records nothing
	// -- nothing happened, and the next admitted run is the one worth auditing.
	admission, err := r.Scheduler.Admit(EvaluationRequest{
		StrategyVersionID: req.StrategyVersionID,
		MarketID:          req.MarketID,
		TriggerKind:       DecisionTriggerAutoShadow,
		At:                now,
	})
	if err != nil {
		return nil, fmt.Errorf("llm shadow: admission: %w", err)
	}
	if !admission.Allowed {
		return &ShadowRunResult{Skipped: true, SkipReason: admission.ReasonCode, RetryAfter: admission.RetryAfter}, nil
	}

	// 2. Zero-cost gates: a refusal is still a decision and is recorded with the
	// gate's reason -- but no model is consulted and no budget is touched.
	if verdict := EvaluatePreGates(&req.PreGates); !verdict.Allowed {
		synthesis := &SynthesisOutcome{Verdict: VerdictHold, ReasonCode: verdict.ReasonCode}
		record, err := r.record(ctx, req, &DecisionRecordInput{Synthesis: synthesis})
		if err != nil {
			return nil, err
		}
		return &ShadowRunResult{Decision: record.Decision, Reason: record.Reason, DecisionRunID: record.DecisionRunID, Record: record, Synthesis: synthesis}, nil
	}

	// 3. Analyst.
	analysis, err := r.Analysis.Run(ctx, &AnalysisRequest{
		PreGates:            req.PreGates,
		Assemble:            req.Assemble,
		ProfileRevisionID:   req.ProfileRevisionID,
		StrategyVersionID:   req.StrategyVersionID,
		AccountID:           req.AccountID,
		MarketID:            req.MarketID,
		StrategyInstruction: req.StrategyInstruction,
	})
	if err != nil {
		return nil, fmt.Errorf("llm shadow: analysis: %w", err)
	}

	result := &ShadowRunResult{}
	var jevOutcome *JevReviewOutcome
	var jevErr error

	// 4. Judge, only when a candidate advanced to it.
	if analysis.Verdict == VerdictProceed && r.Jev != nil && req.BuildJev != nil {
		jevRequest, buildErr := req.BuildJev(ctx, analysis)
		if buildErr != nil {
			jevErr = buildErr
		} else {
			jevOutcome, jevErr = r.Jev.Review(ctx, &jevRequest)
			if jevErr != nil {
				jevErr = fmt.Errorf("llm shadow: judge: %w", jevErr)
			}
		}
	}

	// 5. Triage.
	synthesis := Synthesize(SynthesisInput{Analysis: analysis, Jev: jevOutcome, JevError: jevErr}, SynthesisThresholds{})

	// 6. Hard risk, only at the awaiting gate.
	recordInput := &DecisionRecordInput{Analysis: analysis, Jev: jevOutcome, Synthesis: &synthesis}
	if synthesis.ReasonCode == ReasonAwaitingHardRiskAndApproval && req.RiskFacts != nil {
		riskInput, sizingInput, factsErr := req.RiskFacts(ctx, analysis, jevOutcome)
		if factsErr != nil {
			return nil, fmt.Errorf("llm shadow: risk facts: %w", factsErr)
		}
		if riskInput != nil {
			risk := Evaluate(riskInput)
			recordInput.Risk = &risk
			result.Risk = &risk
			if sizingInput != nil {
				sizing := ComputeQuantity(sizingInput)
				recordInput.Sizing = &sizing
				result.Sizing = &sizing
			}
		}
	}

	record, err := r.record(ctx, req, recordInput)
	if err != nil {
		return nil, err
	}
	result.Decision = record.Decision
	result.Reason = record.Reason
	result.DecisionRunID = record.DecisionRunID
	result.Record = record
	result.Synthesis = &synthesis
	return result, nil
}

// record fills the audit input from the request and writes the chain.
func (r *ShadowRunner) record(ctx context.Context, req *ShadowRunRequest, in *DecisionRecordInput) (*DecisionRecordResult, error) {
	in.Trigger = DecisionTriggerAutoShadow
	in.RunMode = req.RunMode
	in.StrategyVersionID = req.StrategyVersionID
	in.LLMProfileRevisionID = req.ProfileRevisionID
	in.JevProfileRevisionID = req.JevRevisionID
	in.PairEpoch = req.PairEpoch
	in.EnvelopeHash = req.EnvelopeHash
	in.LLMModel = req.LLMModel
	in.JevModel = req.JevModel
	if in.Jev != nil && in.Jev.Manifest != nil {
		in.JevManifestState = JevManifestStateBound
	}
	return r.Recorder.Record(ctx, in)
}
