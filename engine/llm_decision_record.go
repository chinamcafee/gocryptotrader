package engine

import (
	"context"
	"fmt"
	"strconv"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M4-13 审计链（docs 07 §5、09 §2.3）。
//
// 一次决策落三张表：`llm_decision_runs`（决策本身）→ 有硬风控则加一张
// `llm_risk_evaluations`（"为什么拒了"的唯一记录）→ 一条 `llm_audit_events`
// （可被 kind 过滤的治理事件流）。三张表都是不可变表：只插入，永不更新——
// 重放研究要能回答"当时输入是什么、输出了什么"，任何 in-place 修改都会毁掉
// 这个能力。
//
// `input_hash` 是全部输入身份（策略版本 / 快照 / 两个修订版 / pair_epoch /
// envelope 哈希 / 候选与裁决的原文哈希）的规范化 SHA-256：同样的输入再来一次、
// 对照重放实验、以及"这条 decision_run 到底喂了什么给模型"的追问都靠它。

// Decision trigger vocabulary (09 §2.3).
const (
	DecisionTriggerManualChat    = "MANUAL_CHAT"
	DecisionTriggerManualPreview = "MANUAL_PREVIEW"
	DecisionTriggerAutoShadow    = "AUTO_SHADOW"
	DecisionTriggerAutoCandidate = "AUTO_CANDIDATE"
	DecisionTriggerAutoExecution = "AUTO_EXECUTION"
	// DecisionTriggerPositionReview marks a manual hold/close review of an
	// open position (docs 13 §3.3).
	DecisionTriggerPositionReview = "MANUAL_POSITION_REVIEW"
)

// AuditKindDecisionRecorded is the audit event written for every decision run.
const AuditKindDecisionRecorded = "DECISION_RECORDED"

// Decision input-hash schema version.
const decisionInputHashVersion = "decision-run-input-v1"

// iDecisionRuns / iRiskEvaluations / iAuditEvents are the narrow write seams;
// LLMDecisionStore adapts them to the repository layer.
type iDecisionRuns interface {
	InsertDecisionRun(ctx context.Context, r *llmstore.DecisionRun) error
}

type iRiskEvaluations interface {
	InsertRiskEvaluation(ctx context.Context, e *llmstore.RiskEvaluation) error
}

type iAuditEvents interface {
	InsertAuditEvent(ctx context.Context, e *llmstore.AuditEvent) error
}

// LLMDecisionStore is the production adapter over database/repository/llm.
type LLMDecisionStore struct{}

// InsertDecisionRun appends one decision run.
func (LLMDecisionStore) InsertDecisionRun(ctx context.Context, r *llmstore.DecisionRun) error {
	return llmstore.InsertDecisionRun(ctx, r)
}

// InsertRiskEvaluation appends one risk evaluation.
func (LLMDecisionStore) InsertRiskEvaluation(ctx context.Context, e *llmstore.RiskEvaluation) error {
	return llmstore.InsertRiskEvaluation(ctx, e)
}

// InsertAuditEvent appends one audit event.
func (LLMDecisionStore) InsertAuditEvent(ctx context.Context, e *llmstore.AuditEvent) error {
	return llmstore.InsertAuditEvent(ctx, e)
}

// DecisionRecorder persists one pipeline pass.
type DecisionRecorder struct {
	Runs  iDecisionRuns
	Risk  iRiskEvaluations
	Audit iAuditEvents
	Now   func() time.Time
	NewID func() string
}

// DecisionRecordInput is everything one row set needs.
type DecisionRecordInput struct {
	Trigger                 string
	RunMode                 string
	StrategyVersionID       string
	SnapshotID              string
	LLMProfileRevisionID    string
	JevProfileRevisionID    string
	LLMModel                string
	JevModel                string
	PromptVersion           string
	QuestionTemplateVersion string
	JevManifestState        string
	PairEpoch               int64
	EnvelopeHash            string
	Deadline                time.Time
	LatencyMS               int64
	// Analysis, Jev and Synthesis are the stage outcomes; Synthesis is required,
	// the others are nil when their stage never ran.
	Analysis  *AnalysisOutcome
	Jev       *JevReviewOutcome
	Synthesis *SynthesisOutcome
	// Risk and Sizing are set only when the hard risk stage ran.
	Risk             *RiskResult
	RiskCheckVersion string
	Sizing           *SizingResult
}

// DecisionRecordResult is what the caller needs to continue (order intents
// reference the run; operations reference the audit trail).
type DecisionRecordResult struct {
	DecisionRunID    string `json:"decisionRunId"`
	RiskEvaluationID string `json:"riskEvaluationId,omitempty"`
	InputHash        string `json:"inputHash"`
	Decision         string `json:"decision"`
	Reason           string `json:"reason"`
}

// Record writes the decision run, the risk evaluation (when hard risk ran) and
// the audit event, in that order.
//
// An insert failure aborts the chain and is returned: a decision that cannot be
// persisted must not proceed to order placement, because the audit trail is
// what every later question ("why did we trade?") is answered from.
func (r *DecisionRecorder) Record(ctx context.Context, in *DecisionRecordInput) (*DecisionRecordResult, error) {
	if r == nil || r.Runs == nil || r.Risk == nil || r.Audit == nil {
		return nil, ErrChatNotConfigured
	}
	if in.Synthesis == nil {
		return nil, fmt.Errorf("%w: a synthesis outcome is required", ErrChatNotConfigured)
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	newID := func() string { return "" }
	if r.NewID != nil {
		newID = r.NewID
	}

	candidateJSON, err := marshalCandidate(firstCandidate(in))
	if err != nil {
		return nil, fmt.Errorf("llm decision record: candidate json: %w", err)
	}
	jevJSON, err := marshalJev(in.Jev)
	if err != nil {
		return nil, fmt.Errorf("llm decision record: jev json: %w", err)
	}

	decision, reason := decisionAndReason(in.Synthesis, in.Risk)
	inputHash := decisionInputHash(in, candidateJSON, jevJSON)

	run := llmstore.DecisionRun{
		ID:                   newID(),
		Trigger:              in.Trigger,
		RunMode:              in.RunMode,
		LLMProfileRevisionID: in.LLMProfileRevisionID,
		PairEpoch:            in.PairEpoch,
		LLMModel:             in.LLMModel,
		PromptVersion:        in.PromptVersion,
		CandidateJSON:        candidateJSON,
		JevJSON:              jevJSON,
		Decision:             decision,
		Reason:               reason,
		InputHash:            inputHash,
		CreatedAt:            now,
	}
	run.StrategyVersionID = optionalString(in.StrategyVersionID)
	run.SnapshotID = optionalString(in.SnapshotID)
	run.JevProfileRevisionID = optionalString(in.JevProfileRevisionID)
	run.JevModel = optionalString(in.JevModel)
	run.QuestionTemplateVersion = optionalString(in.QuestionTemplateVersion)
	run.JevManifestState = optionalString(in.JevManifestState)
	if in.Analysis != nil {
		run.ToolTranscriptJSON = optionalString(in.Analysis.TranscriptJSON)
		run.ToolCallCount = int64(in.Analysis.ToolCalls)
		run.LLMRoundCount = int64(in.Analysis.Rounds)
		run.UsageInputTokens = in.Analysis.UsageInputTokens
		run.UsageOutputTokens = in.Analysis.UsageOutputTokens
		run.LatencyMS = in.LatencyMS
	}
	if !in.Deadline.IsZero() {
		deadline := in.Deadline
		run.DeadlineAt = &deadline
	}
	if in.Jev != nil {
		run.JevInputTokens = &in.Jev.InputTokens
		run.JevOutputTokens = &in.Jev.OutputTokens
	}
	if err := r.Runs.InsertDecisionRun(ctx, &run); err != nil {
		return nil, fmt.Errorf("llm decision record: decision run: %w", err)
	}
	result := &DecisionRecordResult{
		DecisionRunID: run.ID,
		InputHash:     inputHash,
		Decision:      decision,
		Reason:        reason,
	}

	if in.Risk != nil {
		riskID := newID()
		checksJSON, err := json.Marshal(in.Risk.Checks)
		if err != nil {
			return nil, fmt.Errorf("llm decision record: checks json: %w", err)
		}
		riskRow := llmstore.RiskEvaluation{
			ID:            riskID,
			DecisionRunID: run.ID,
			CheckVersion:  in.RiskCheckVersion,
			Verdict:       in.Risk.Verdict,
			ChecksJSON:    string(checksJSON),
			EnvelopeHash:  in.EnvelopeHash,
			EvaluatedAt:   now,
		}
		if riskRow.CheckVersion == "" {
			riskRow.CheckVersion = RiskCheckVersion
		}
		if len(in.Risk.Rejections) > 0 {
			rejectionsJSON, err := json.Marshal(in.Risk.Rejections)
			if err != nil {
				return nil, fmt.Errorf("llm decision record: rejections json: %w", err)
			}
			text := string(rejectionsJSON)
			riskRow.RejectionsJSON = &text
		}
		if in.Sizing != nil {
			riskRow.ComputedQty = optionalString(in.Sizing.Quantity)
			riskRow.ComputedNotional = optionalString(in.Sizing.NotionalUSD)
			riskRow.ReferencePrice = optionalString(in.Sizing.ReferencePrice)
		}
		if err := r.Risk.InsertRiskEvaluation(ctx, &riskRow); err != nil {
			return nil, fmt.Errorf("llm decision record: risk evaluation: %w", err)
		}
		result.RiskEvaluationID = riskID
	}

	detail, err := json.Marshal(map[string]string{
		"decisionRunId":     run.ID,
		"decision":          decision,
		jsonKeyReason:       reason,
		"trigger":           in.Trigger,
		"runMode":           in.RunMode,
		"strategyVersionId": in.StrategyVersionID,
		"snapshotId":        in.SnapshotID,
		"marketId":          candidateMarketID(in),
		"envelopeHash":      in.EnvelopeHash,
	})
	if err != nil {
		return nil, fmt.Errorf("llm decision record: audit detail: %w", err)
	}
	subject := run.ID
	if err := r.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID:         newID(),
		Kind:       AuditKindDecisionRecorded,
		Actor:      auditActorSystem,
		Subject:    &subject,
		DetailJSON: string(detail),
		CreatedAt:  now,
	}); err != nil {
		return nil, fmt.Errorf("llm decision record: audit event: %w", err)
	}
	return result, nil
}

// decisionAndReason maps the stage outcomes onto the decision column.
//
// A hard-risk verdict supersedes the synthesis verdict (that is the whole point
// of the risk engine); its reason is the first refusal — the same stable first
// the evaluator reported. An ALLOW keeps the synthesis reason, which records
// which upstream gate advanced the run.
func decisionAndReason(s *SynthesisOutcome, risk *RiskResult) (decision, reason string) {
	if risk != nil {
		switch risk.Verdict {
		case RiskVerdictAllow:
			return RiskVerdictAllow, s.ReasonCode
		default:
			reason := risk.Verdict
			if len(risk.Rejections) > 0 {
				reason = risk.Rejections[0].Code
			}
			return risk.Verdict, reason
		}
	}
	return s.Verdict, s.ReasonCode
}

// candidateMarketID finds the market in whichever outcome carries it.
func candidateMarketID(in *DecisionRecordInput) string {
	if in.Synthesis != nil && in.Synthesis.Candidate != nil {
		return in.Synthesis.Candidate.MarketID
	}
	if in.Analysis != nil && in.Analysis.Candidate != nil {
		return in.Analysis.Candidate.MarketID
	}
	return ""
}

// decisionInputHash binds every input identity into one canonical digest.
func decisionInputHash(in *DecisionRecordInput, candidateJSON, jevJSON *string) string {
	var buf []byte
	appendField := func(name, value string) {
		buf = append(buf, name...)
		buf = append(buf, '=')
		buf = append(buf, value...)
		buf = append(buf, '\n')
	}
	appendField("version", decisionInputHashVersion)
	appendField("strategyVersionId", in.StrategyVersionID)
	appendField("snapshotId", in.SnapshotID)
	appendField("llmProfileRevisionId", in.LLMProfileRevisionID)
	appendField("jevProfileRevisionId", in.JevProfileRevisionID)
	appendField("pairEpoch", strconv.FormatInt(in.PairEpoch, 10))
	appendField("envelopeHash", in.EnvelopeHash)
	appendField("candidateSha256", sha256Hex([]byte(derefOrEmpty(candidateJSON))))
	appendField("jevSha256", sha256Hex([]byte(derefOrEmpty(jevJSON))))
	return sha256Hex(buf)
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// firstCandidate prefers the synthesis copy (it is the one that was reviewed)
// and falls back to the analysis copy.
func firstCandidate(in *DecisionRecordInput) *candidate.Candidate {
	if in.Synthesis != nil && in.Synthesis.Candidate != nil {
		return in.Synthesis.Candidate
	}
	if in.Analysis != nil {
		return in.Analysis.Candidate
	}
	return nil
}

// marshalCandidate serialises the candidate, or nil when absent.
func marshalCandidate(c *candidate.Candidate) (*string, error) {
	if c == nil {
		return nil, nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	text := string(raw)
	return &text, nil
}

// marshalJev serialises the judge outcome (assessment + usage + request hash),
// or nil when the stage never ran.
func marshalJev(j *JevReviewOutcome) (*string, error) {
	if j == nil {
		return nil, nil
	}
	raw, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	text := string(raw)
	return &text, nil
}
