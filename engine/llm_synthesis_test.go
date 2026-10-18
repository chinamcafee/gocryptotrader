package engine

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/jev"
)

// M4-10 测试：分诊表逐行覆盖 §7/§7.1/§7.2，外加"永不产出 ALLOW"的总括断言。

// synthProbs builds a decided choice distribution: the chosen bucket far above
// the rest, so the §7.2 separation floor passes unless a test says otherwise.
func synthProbs(choice string) map[string]float64 {
	probs := map[string]float64{
		jev.CriteriaOpenLong:  0.05,
		jev.CriteriaOpenShort: 0.05,
		jev.CriteriaReduce:    0.05,
		jev.CriteriaHold:      0.05,
		jev.CriteriaReview:    0.05,
	}
	probs[choice] = 0.8
	return probs
}

func synthAssessment(choice string, score, noul float64) *jev.Assessment {
	return &jev.Assessment{
		CandidateAction: &jev.ChoiceAnswer{
			Type:          jev.AnswerChoice,
			Choice:        choice,
			Probabilities: synthProbs(choice),
			Confidence:    0.8,
		},
		EvidenceQuality: &jev.ScoreAnswer{
			Type:       jev.AnswerScore,
			Score:      score,
			Confidence: 0.7,
			Legend:     map[string]any{"0": "Insufficient", "1": "Mixed", "2": "Adequate"},
		},
		StrategyAlignment: &jev.NoulAnswer{Type: jev.AnswerNoul, Noul: noul},
	}
}

func synthProceed(action candidate.Action) *AnalysisOutcome {
	return &AnalysisOutcome{Verdict: VerdictProceed, Candidate: &candidate.Candidate{Action: action}}
}

// synthAgreed builds the row-11 setup: analysis and judge agree, and every
// judge quality bar passes.
func synthAgreed(action candidate.Action, choice string) SynthesisInput {
	return SynthesisInput{
		Analysis: synthProceed(action),
		Jev:      &JevReviewOutcome{Assessment: synthAssessment(choice, 2, 0.9)},
	}
}

func TestSynthesizeCandidateHoldStopsBeforeTheJudge(t *testing.T) {
	t.Parallel()

	in := synthAgreed(candidate.ActionHold, jev.CriteriaHold)
	out := Synthesize(in, SynthesisThresholds{})
	assert.Equal(t, VerdictHold, out.Verdict)
	assert.Equal(t, ReasonModelHold, out.ReasonCode, "the analyst's own hold is the reason, not the judge's")
	require.NotNil(t, out.Candidate)
}

func TestSynthesizeAnalystHoldPassesItsReasonThrough(t *testing.T) {
	t.Parallel()

	// A hold produced by an earlier gate (stale snapshot, budget, candidate
	// contract) carries the gate's code, even though no candidate exists.
	out := Synthesize(SynthesisInput{Analysis: &AnalysisOutcome{Verdict: VerdictHold, ReasonCode: "MARKET_SNAPSHOT_STALE"}}, SynthesisThresholds{})
	assert.Equal(t, VerdictHold, out.Verdict)
	assert.Equal(t, "MARKET_SNAPSHOT_STALE", out.ReasonCode)

	// The defensive shape -- a hold verdict that still carries a candidate --
	// behaves the same rather than falling through to the judge rows.
	out = Synthesize(SynthesisInput{
		Analysis: &AnalysisOutcome{Verdict: VerdictHold, ReasonCode: "BUDGET_EXHAUSTED", Candidate: &candidate.Candidate{Action: candidate.ActionOpenLong}},
	}, SynthesisThresholds{})
	assert.Equal(t, VerdictHold, out.Verdict)
	assert.Equal(t, "BUDGET_EXHAUSTED", out.ReasonCode)
}

func TestSynthesizeMissingAnalysisIsHold(t *testing.T) {
	t.Parallel()

	out := Synthesize(SynthesisInput{}, SynthesisThresholds{})
	assert.Equal(t, VerdictHold, out.Verdict)
	assert.Equal(t, ReasonModelHold, out.ReasonCode)
}

func TestSynthesizeSafetyReviewPassesThroughWithoutTheJudge(t *testing.T) {
	t.Parallel()

	// M4-08's safety gate: a risk-bearing candidate on non-exchange evidence
	// stops before the Jev stage, so the synthesis never sees a judge outcome.
	out := Synthesize(SynthesisInput{
		Analysis: &AnalysisOutcome{
			Verdict:    VerdictReview,
			ReasonCode: ReasonReviewEvidenceQuality,
			Candidate:  &candidate.Candidate{Action: candidate.ActionOpenLong},
		},
	}, SynthesisThresholds{})
	assert.Equal(t, VerdictReview, out.Verdict)
	assert.Equal(t, ReasonReviewEvidenceQuality, out.ReasonCode)
	assert.Nil(t, out.Assessment)
}

func TestSynthesizeClassifiesJudgeFailures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"drift", fmt.Errorf("jeving: %w: requested jev-1.0.0, served jev-1.1.0", jev.ErrModelDrift), ReasonJevModelDrift},
		{"alias", fmt.Errorf("jeving: %w", jev.ErrModelInvalid), ReasonJevModelDrift},
		{"schema", fmt.Errorf("jeving: %w: empty answers", jev.ErrSchemaInvalid), ReasonJevSchemaInvalid},
		{"answer missing", fmt.Errorf("jeving: %w: candidate_action", jev.ErrAnswerMissing), ReasonJevSchemaInvalid},
		{"transport", errors.New("jeving: context deadline exceeded"), ReasonJevUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := Synthesize(SynthesisInput{Analysis: synthProceed(candidate.ActionOpenLong), JevError: tc.err}, SynthesisThresholds{})
			assert.Equal(t, VerdictHold, out.Verdict, "a judge that cannot answer stops automation")
			assert.Equal(t, tc.want, out.ReasonCode)
			assert.NotEmpty(t, out.Detail)
		})
	}
}

func TestSynthesizeStageRefusalsHoldWithTheirOwnReason(t *testing.T) {
	t.Parallel()

	// M4-09 refusals (evidence invalid, oversized, stale pair, budget) arrive as
	// an outcome with a nil assessment: the stage's code is the reason.
	out := Synthesize(SynthesisInput{
		Analysis: synthProceed(candidate.ActionOpenLong),
		Jev:      &JevReviewOutcome{ReasonCode: ReasonJevStalePair, Detail: "epoch moved from 3 to 4 while the judge ran"},
	}, SynthesisThresholds{})
	assert.Equal(t, VerdictHold, out.Verdict)
	assert.Equal(t, ReasonJevStalePair, out.ReasonCode)
	assert.Equal(t, "epoch moved from 3 to 4 while the judge ran", out.Detail)

	// A missing outcome and a zero-value outcome both read as unavailable.
	out = Synthesize(SynthesisInput{Analysis: synthProceed(candidate.ActionOpenLong)}, SynthesisThresholds{})
	assert.Equal(t, VerdictHold, out.Verdict)
	assert.Equal(t, ReasonJevUnavailable, out.ReasonCode)

	out = Synthesize(SynthesisInput{Analysis: synthProceed(candidate.ActionOpenLong), Jev: &JevReviewOutcome{}}, SynthesisThresholds{})
	assert.Equal(t, VerdictHold, out.Verdict)
	assert.Equal(t, ReasonJevUnavailable, out.ReasonCode)
}

func TestSynthesizeJudgeHoldAndReviewChoices(t *testing.T) {
	t.Parallel()

	out := Synthesize(SynthesisInput{
		Analysis: synthProceed(candidate.ActionOpenLong),
		Jev:      &JevReviewOutcome{Assessment: synthAssessment(jev.CriteriaHold, 2, 0.9)},
	}, SynthesisThresholds{})
	assert.Equal(t, VerdictHold, out.Verdict, "the judge choosing hold is more conservative than the analyst opening")
	assert.Equal(t, ReasonJevHold, out.ReasonCode)

	out = Synthesize(SynthesisInput{
		Analysis: synthProceed(candidate.ActionOpenLong),
		Jev:      &JevReviewOutcome{Assessment: synthAssessment(jev.CriteriaReview, 2, 0.9)},
	}, SynthesisThresholds{})
	assert.Equal(t, VerdictReview, out.Verdict)
	assert.Equal(t, ReasonJevRequestsReview, out.ReasonCode)
}

func TestSynthesizeDirectionDisagreements(t *testing.T) {
	t.Parallel()

	cases := []struct {
		action candidate.Action
		choice string
	}{
		{candidate.ActionOpenLong, jev.CriteriaOpenShort},
		{candidate.ActionOpenLong, jev.CriteriaReduce},
		{candidate.ActionOpenShort, jev.CriteriaOpenLong},
		{candidate.ActionOpenShort, jev.CriteriaReduce},
		{candidate.ActionReduce, jev.CriteriaOpenLong},
		{candidate.ActionReduce, jev.CriteriaOpenShort},
		{candidate.ActionOpenLong, "not_a_criterion"}, // unknown answers read conservatively
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s_vs_%s", tc.action, tc.choice), func(t *testing.T) {
			t.Parallel()
			out := Synthesize(SynthesisInput{
				Analysis: synthProceed(tc.action),
				Jev:      &JevReviewOutcome{Assessment: synthAssessment(tc.choice, 2, 0.9)},
			}, SynthesisThresholds{})
			assert.Equal(t, VerdictReview, out.Verdict)
			assert.Equal(t, ReasonModelDirectionDisagreement, out.ReasonCode)
			assert.Contains(t, out.Detail, string(tc.action))
			assert.Contains(t, out.Detail, tc.choice)
		})
	}
}

func TestSynthesizeModelReviewAction(t *testing.T) {
	t.Parallel()

	out := Synthesize(synthAgreed(candidate.ActionReview, jev.CriteriaReview), SynthesisThresholds{})
	assert.Equal(t, VerdictReview, out.Verdict)
	assert.Equal(t, ReasonModelRequestsReview, out.ReasonCode, "the analyst asking for a human outranks the judge")
}

func TestSynthesizeAgreementNeverAllows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		action candidate.Action
		choice string
	}{
		{candidate.ActionOpenLong, jev.CriteriaOpenLong},
		{candidate.ActionOpenShort, jev.CriteriaOpenShort},
		{candidate.ActionReduce, jev.CriteriaReduce},
	}
	for _, tc := range cases {
		t.Run(string(tc.action), func(t *testing.T) {
			t.Parallel()
			out := Synthesize(synthAgreed(tc.action, tc.choice), SynthesisThresholds{})
			assert.Equal(t, VerdictReview, out.Verdict, "even total agreement is not ALLOW")
			assert.Equal(t, ReasonAwaitingHardRiskAndApproval, out.ReasonCode)
			require.NotNil(t, out.Candidate, "the candidate must reach the hard risk engine")
			require.NotNil(t, out.Assessment, "the assessment must reach the audit trail")
		})
	}
}

func TestSynthesizeEvidenceBar(t *testing.T) {
	t.Parallel()

	in := synthAgreed(candidate.ActionOpenLong, jev.CriteriaOpenLong)
	in.Jev.Assessment = synthAssessment(jev.CriteriaOpenLong, 1.4, 0.9)
	out := Synthesize(in, SynthesisThresholds{})
	assert.Equal(t, VerdictReview, out.Verdict)
	assert.Equal(t, ReasonJevEvidenceInsufficient, out.ReasonCode)
	assert.Contains(t, out.Detail, "1.40")

	// The bar is inclusive: exactly Adequate passes.
	in.Jev.Assessment = synthAssessment(jev.CriteriaOpenLong, 2.0, 0.9)
	out = Synthesize(in, SynthesisThresholds{})
	assert.Equal(t, ReasonAwaitingHardRiskAndApproval, out.ReasonCode)
}

func TestSynthesizeAlignmentBar(t *testing.T) {
	t.Parallel()

	in := synthAgreed(candidate.ActionOpenLong, jev.CriteriaOpenLong)
	in.Jev.Assessment = synthAssessment(jev.CriteriaOpenLong, 2, 0.4)
	out := Synthesize(in, SynthesisThresholds{})
	assert.Equal(t, VerdictReview, out.Verdict)
	assert.Equal(t, ReasonJevStrategyMisalignment, out.ReasonCode)

	in.Jev.Assessment = synthAssessment(jev.CriteriaOpenLong, 2, 0.5)
	out = Synthesize(in, SynthesisThresholds{})
	assert.Equal(t, ReasonAwaitingHardRiskAndApproval, out.ReasonCode)
}

func TestSynthesizeSeparationFloor(t *testing.T) {
	t.Parallel()

	in := synthAgreed(candidate.ActionOpenLong, jev.CriteriaOpenLong)
	// max 0.40 - second 0.32 = 0.08: the judge's own distribution says it is
	// not decided, so the run goes to a human even though everything agrees.
	in.Jev.Assessment.CandidateAction.Probabilities = map[string]float64{
		jev.CriteriaOpenLong: 0.40, jev.CriteriaHold: 0.32, jev.CriteriaReview: 0.16,
		jev.CriteriaReduce: 0.06, jev.CriteriaOpenShort: 0.06,
	}
	out := Synthesize(in, SynthesisThresholds{})
	assert.Equal(t, VerdictReview, out.Verdict)
	assert.Equal(t, ReasonJevLowSeparation, out.ReasonCode)
	assert.Contains(t, out.Detail, "0.0800")

	// The floor is inclusive: exactly 0.10 passes.
	in.Jev.Assessment.CandidateAction.Probabilities = map[string]float64{
		jev.CriteriaOpenLong: 0.40, jev.CriteriaHold: 0.30, jev.CriteriaReview: 0.18,
		jev.CriteriaReduce: 0.06, jev.CriteriaOpenShort: 0.06,
	}
	out = Synthesize(in, SynthesisThresholds{})
	assert.Equal(t, ReasonAwaitingHardRiskAndApproval, out.ReasonCode)
}

func TestSynthesizeThresholdsFallBackToDefaults(t *testing.T) {
	t.Parallel()

	// A zero-value threshold struct must not read as "no bar": a 1.9 score is
	// below the default 2.0 even when the caller forgot to set anything.
	in := synthAgreed(candidate.ActionOpenLong, jev.CriteriaOpenLong)
	in.Jev.Assessment = synthAssessment(jev.CriteriaOpenLong, 1.9, 0.9)
	out := Synthesize(in, SynthesisThresholds{})
	assert.Equal(t, ReasonJevEvidenceInsufficient, out.ReasonCode)

	// Explicit thresholds still win where they are set.
	out = Synthesize(in, SynthesisThresholds{MinEvidenceQualityScore: 1.5})
	assert.Equal(t, ReasonAwaitingHardRiskAndApproval, out.ReasonCode)
}

func TestSynthesizeNeverProducesAllowOrProceed(t *testing.T) {
	t.Parallel()

	scenarios := []SynthesisInput{
		{},
		{Analysis: &AnalysisOutcome{Verdict: VerdictHold, ReasonCode: "MARKET_SNAPSHOT_STALE"}},
		{Analysis: &AnalysisOutcome{Verdict: VerdictReview, ReasonCode: ReasonReviewEvidenceQuality, Candidate: &candidate.Candidate{Action: candidate.ActionOpenLong}}},
		synthAgreed(candidate.ActionHold, jev.CriteriaHold),
		synthAgreed(candidate.ActionOpenLong, jev.CriteriaOpenShort),
		synthAgreed(candidate.ActionOpenLong, jev.CriteriaOpenLong),
		{Analysis: synthProceed(candidate.ActionOpenLong), JevError: errors.New("boom")},
		{Analysis: synthProceed(candidate.ActionOpenLong), Jev: &JevReviewOutcome{ReasonCode: ReasonJevStalePair}},
	}
	for i, in := range scenarios {
		out := Synthesize(in, SynthesisThresholds{})
		assert.Contains(t, []string{VerdictHold, VerdictReview}, out.Verdict,
			"scenario %d: synthesis decides between HOLD and REVIEW only", i)
		assert.NotEmptyf(t, out.ReasonCode, "scenario %d: every exit names a reason", i)
	}
}
