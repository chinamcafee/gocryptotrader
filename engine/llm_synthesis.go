package engine

import (
	"errors"
	"fmt"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/jev"
)

// M4-10 决策合成（docs 06 §7）。
//
// 本层是一张纯函数分诊表：把 M4-08 的分析结论与 M4-09 的裁判结论摊在一起，
// 按 §7 的文档序逐行判定，产出 HOLD 或 REVIEW。它**永不产出 ALLOW**——
// 即使两个模型完全一致、证据充分、对齐策略，出口也只有
// REVIEW / AWAITING_HARD_RISK_AND_APPROVAL，ALLOW 只能由硬风控引擎（M4-11）
// 产出，这是"模型不能授权订单"这条底线的具体体现。
//
// 核心原则：分歧时取保守方。宁可错过机会，不可错误开仓。

// Reason codes for the synthesis stage (06 §7 vocabulary, plus the extensions
// marked below).
const (
	// ReasonModelHold marks the analyst's own HOLD (§7 row 1).
	ReasonModelHold = "MODEL_HOLD"
	// ReasonJevHold marks the judge choosing hold (§7 row 3).
	ReasonJevHold = "JEV_HOLD"
	// ReasonJevUnavailable marks a judge that could not answer (§7 row 4).
	ReasonJevUnavailable = "JEV_UNAVAILABLE"
	// ReasonJevSchemaInvalid marks an answer that failed validation (§7 row 4).
	ReasonJevSchemaInvalid = "JEV_SCHEMA_INVALID"
	// ReasonJevModelDrift marks a served model that differs from the pinned one
	// (§7 row 4).
	ReasonJevModelDrift = "JEV_MODEL_DRIFT"
	// ReasonModelDirectionDisagreement marks materially different directions
	// (§7.1).
	ReasonModelDirectionDisagreement = "MODEL_DIRECTION_DISAGREEMENT"
	// ReasonJevEvidenceInsufficient marks an evidence score below the bar (§7).
	ReasonJevEvidenceInsufficient = "JEV_EVIDENCE_INSUFFICIENT"
	// ReasonJevStrategyMisalignment marks an alignment value below the bar (§7).
	ReasonJevStrategyMisalignment = "JEV_STRATEGY_MISALIGNMENT"
	// ReasonJevLowSeparation marks a choice distribution whose top two buckets
	// are too close for the judge to be said to be decided (§7.2).
	ReasonJevLowSeparation = "JEV_LOW_SEPARATION"
	// ReasonJevRequestsReview marks the judge asking for a human (§7.1).
	ReasonJevRequestsReview = "JEV_REQUESTS_REVIEW"
	// ReasonModelRequestsReview marks the analyst asking for a human. The docs
	// name only the judge's review row; this is the symmetric extension for the
	// analyst's REVIEW action.
	ReasonModelRequestsReview = "MODEL_REQUESTS_REVIEW"
	// ReasonAwaitingHardRiskAndApproval is the only exit for two agreeing
	// models: it is explicitly not ALLOW (§7 final note).
	ReasonAwaitingHardRiskAndApproval = "AWAITING_HARD_RISK_AND_APPROVAL"
)

// Synthesis threshold defaults.
//
// 06 §7 writes the two comparison thresholds as "阈值" without a number; the
// defaults below are the documented read: doc 01 §7's end-to-end success row
// carries evidence_quality = Adequate, so the evidence bar is the top of the
// three-point scale; alignment is a yes-probability, so the bar is its midpoint;
// the separation floor is the one number §7.2 does pin.
const (
	// DefaultMinEvidenceQualityScore requires the judge's "Adequate" bucket.
	DefaultMinEvidenceQualityScore = 2.0
	// DefaultMinStrategyAlignment requires the judge to lean yes.
	DefaultMinStrategyAlignment = 0.5
	// DefaultMinChoiceSeparation is the §7.2 "概率分布接近" quantity.
	DefaultMinChoiceSeparation = 0.10
)

// SynthesisThresholds are the tunable bars. A zero or negative field falls back
// to its default rather than accepting everything: a zero threshold would read
// as "no bar", which is the one interpretation no other rule in this package
// would agree with.
type SynthesisThresholds struct {
	MinEvidenceQualityScore float64
	MinStrategyAlignment    float64
	MinChoiceSeparation     float64
}

// DefaultSynthesisThresholds returns the documented bars.
func DefaultSynthesisThresholds() SynthesisThresholds {
	return SynthesisThresholds{
		MinEvidenceQualityScore: DefaultMinEvidenceQualityScore,
		MinStrategyAlignment:    DefaultMinStrategyAlignment,
		MinChoiceSeparation:     DefaultMinChoiceSeparation,
	}
}

// withDefaults replaces unset fields.
func (t SynthesisThresholds) withDefaults() SynthesisThresholds {
	if t.MinEvidenceQualityScore <= 0 {
		t.MinEvidenceQualityScore = DefaultMinEvidenceQualityScore
	}
	if t.MinStrategyAlignment <= 0 {
		t.MinStrategyAlignment = DefaultMinStrategyAlignment
	}
	if t.MinChoiceSeparation <= 0 {
		t.MinChoiceSeparation = DefaultMinChoiceSeparation
	}
	return t
}

// SynthesisInput is the two stage outcomes plus any judge-stage error.
//
// JevError is the pipeline error from JevPipeline.Review (transport failure,
// model drift, schema failure); a stage refusal is instead a JevReviewOutcome
// with a ReasonCode and a nil Assessment, which is why both channels exist.
type SynthesisInput struct {
	Analysis *AnalysisOutcome
	Jev      *JevReviewOutcome
	JevError error
}

// SynthesisOutcome is the triage answer. Verdict is VerdictHold or
// VerdictReview, never anything else.
type SynthesisOutcome struct {
	Verdict    string               `json:"verdict"`
	ReasonCode string               `json:"reasonCode"`
	Detail     string               `json:"detail,omitempty"`
	Candidate  *candidate.Candidate `json:"candidate,omitempty"`
	Assessment *jev.Assessment      `json:"assessment,omitempty"`
}

// Synthesize runs the §7 triage table in documented order.
func Synthesize(in SynthesisInput, th SynthesisThresholds) SynthesisOutcome {
	th = th.withDefaults()

	// Row 0 (defensive): no candidate means there is nothing to act on, even if
	// a verdict says otherwise.
	if in.Analysis == nil || in.Analysis.Candidate == nil {
		code := ReasonModelHold
		if in.Analysis != nil && in.Analysis.ReasonCode != "" {
			code = in.Analysis.ReasonCode
		}
		return SynthesisOutcome{Verdict: VerdictHold, ReasonCode: code}
	}
	cand := in.Analysis.Candidate

	// Rows 1-2: an analyst HOLD (any gate reason: stale snapshot, candidate
	// contract, budget, loop abort) ends the run; so does a safety REVIEW --
	// in both cases the judge was never consulted or no longer matters.
	switch in.Analysis.Verdict {
	case VerdictHold:
		return SynthesisOutcome{Verdict: VerdictHold, ReasonCode: in.Analysis.ReasonCode}
	case VerdictReview:
		return SynthesisOutcome{Verdict: VerdictReview, ReasonCode: in.Analysis.ReasonCode, Candidate: cand}
	}

	// Row 1 exact form: a HOLD candidate stops before the judge row (§7 row 1
	// precedes the Jev rows). The orchestrator may skip the judge call for a
	// holding candidate, but the table is authoritative either way.
	if cand.Action.IsHold() {
		return SynthesisOutcome{Verdict: VerdictHold, ReasonCode: ReasonModelHold, Candidate: cand}
	}
	// The analyst asking for a human outranks any judge answer.
	if cand.Action == candidate.ActionReview {
		return SynthesisOutcome{Verdict: VerdictReview, ReasonCode: ReasonModelRequestsReview, Candidate: cand}
	}

	// Row 3-4: the judge side. Every failure to produce a valid assessment is a
	// HOLD with the most specific reason available, never a silent pass.
	if in.JevError != nil {
		return SynthesisOutcome{
			Verdict:    VerdictHold,
			ReasonCode: classifyJevFailure(in.JevError),
			Detail:     in.JevError.Error(),
			Candidate:  cand,
		}
	}
	if in.Jev == nil {
		return SynthesisOutcome{Verdict: VerdictHold, ReasonCode: ReasonJevUnavailable, Candidate: cand}
	}
	out := SynthesisOutcome{Candidate: cand, Assessment: in.Jev.Assessment}
	if in.Jev.Assessment == nil {
		code := in.Jev.ReasonCode
		if code == "" {
			code = ReasonJevUnavailable
		}
		out.Verdict = VerdictHold
		out.ReasonCode = code
		out.Detail = in.Jev.Detail
		return out
	}

	// Row 5-6: hold and review choices (§7 row 3 and §7.1's review row).
	choice := in.Jev.Assessment.CandidateAction
	switch choice.Choice {
	case jev.CriteriaHold:
		out.Verdict = VerdictHold
		out.ReasonCode = ReasonJevHold
		return out
	case jev.CriteriaReview:
		out.Verdict = VerdictReview
		out.ReasonCode = ReasonJevRequestsReview
		return out
	}

	// Row 7: direction agreement (§7.1). An unrecognised choice is treated as a
	// disagreement, not as agreement: validation should make it impossible, and
	// the only safe reading of an unknown answer is the conservative one.
	if !directionsAgree(cand.Action, choice.Choice) {
		out.Verdict = VerdictReview
		out.ReasonCode = ReasonModelDirectionDisagreement
		out.Detail = fmt.Sprintf("llm %s vs jev %s", cand.Action, choice.Choice)
		return out
	}

	// Rows 8-10: both models agree in direction, so the remaining questions are
	// about how well the judge's own answers hold up.
	if score := in.Jev.Assessment.EvidenceQuality.Score; score < th.MinEvidenceQualityScore {
		out.Verdict = VerdictReview
		out.ReasonCode = ReasonJevEvidenceInsufficient
		out.Detail = fmt.Sprintf("evidence score %.2f below %.2f", score, th.MinEvidenceQualityScore)
		return out
	}
	if noul := in.Jev.Assessment.StrategyAlignment.Noul; noul < th.MinStrategyAlignment {
		out.Verdict = VerdictReview
		out.ReasonCode = ReasonJevStrategyMisalignment
		out.Detail = fmt.Sprintf("alignment %.2f below %.2f", noul, th.MinStrategyAlignment)
		return out
	}
	if separation := choiceSeparation(choice.Probabilities); separation < th.MinChoiceSeparation {
		out.Verdict = VerdictReview
		out.ReasonCode = ReasonJevLowSeparation
		out.Detail = fmt.Sprintf("choice separation %.4f below %.2f", separation, th.MinChoiceSeparation)
		return out
	}

	// Row 11: total agreement is still not ALLOW. The hard risk engine (M4-11)
	// is the only producer of ALLOW.
	out.Verdict = VerdictReview
	out.ReasonCode = ReasonAwaitingHardRiskAndApproval
	return out
}

// directionsAgree reports whether an analyst action and a judge choice mean the
// same thing per the §7.1 table (hold and review rows are handled before this
// is consulted).
func directionsAgree(action candidate.Action, choice string) bool {
	switch action {
	case candidate.ActionOpenLong:
		return choice == jev.CriteriaOpenLong
	case candidate.ActionOpenShort:
		return choice == jev.CriteriaOpenShort
	case candidate.ActionReduce:
		return choice == jev.CriteriaReduce
	}
	return false
}

// choiceSeparation is max(probabilities) - secondMax(probabilities), the §7.2
// uncertainty quantity. An empty or single-bucket map yields 0, which falls
// below any positive floor and therefore reads as undecided.
func choiceSeparation(probabilities map[string]float64) float64 {
	var top, second float64
	for _, p := range probabilities {
		if p > top {
			second = top
			top = p
			continue
		}
		if p > second {
			second = p
		}
	}
	return top - second
}

// classifyJevFailure maps a judge-stage error onto the §7 row-4 vocabulary.
func classifyJevFailure(err error) string {
	switch {
	case errors.Is(err, jev.ErrModelDrift), errors.Is(err, jev.ErrModelInvalid):
		return ReasonJevModelDrift
	case errors.Is(err, jev.ErrSchemaInvalid),
		errors.Is(err, jev.ErrChoiceInvalid),
		errors.Is(err, jev.ErrDistributionInvalid),
		errors.Is(err, jev.ErrProbabilityInvalid),
		errors.Is(err, jev.ErrScoreInvalid),
		errors.Is(err, jev.ErrNoulInvalid),
		errors.Is(err, jev.ErrAnswerMissing):
		return ReasonJevSchemaInvalid
	default:
		return ReasonJevUnavailable
	}
}
