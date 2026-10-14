package jev

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// Validation tolerances. The contract returns floating point probabilities, so
// exact equality is unusable; the tolerances below are the ones the reference
// implementation converged on and they are deliberately tight.
const (
	// ProbabilitySumTolerance bounds how far a distribution may stray from 1.
	ProbabilitySumTolerance = 0.03
	// ChoiceMarginTolerance bounds how far the reported choice may sit below the
	// highest probability in its own distribution.
	ChoiceMarginTolerance = 0.03
	// WeightedScoreTolerance bounds the difference between a reported score and
	// the score implied by its distribution.
	WeightedScoreTolerance = 0.06
)

var (
	// ErrModelDrift marks a response naming a different model than requested.
	ErrModelDrift = errors.New("response model differs from the requested model")
	// ErrSchemaInvalid marks a malformed answer envelope.
	ErrSchemaInvalid = errors.New("answer schema is invalid")
	// ErrChoiceInvalid marks a choice answer outside the offered criteria or
	// inconsistent with its own distribution.
	ErrChoiceInvalid = errors.New("choice answer is invalid")
	// ErrDistributionInvalid marks probabilities that do not cover exactly the
	// offered criteria.
	ErrDistributionInvalid = errors.New("probability distribution is invalid")
	// ErrProbabilityInvalid marks probabilities that do not sum to one.
	ErrProbabilityInvalid = errors.New("probabilities do not sum to one")
	// ErrScoreInvalid marks a score answer inconsistent with its scale, legend or
	// distribution.
	ErrScoreInvalid = errors.New("score answer is invalid")
	// ErrNoulInvalid marks a noul answer outside the unit interval.
	ErrNoulInvalid = errors.New("noul answer is invalid")
	// ErrAnswerMissing marks an expected answer that the response omitted.
	ErrAnswerMissing = errors.New("expected answer is missing")
)

// Assessment is a validated review result.
//
// Every field is populated only after its answer passed validation, so a caller
// never has to re-check whether an answer is well formed.
type Assessment struct {
	CandidateAction   *ChoiceAnswer
	EvidenceQuality   *ScoreAnswer
	StrategyAlignment *NoulAnswer
	Usage             Usage
	// ResponseModel is the model the endpoint actually served.
	ResponseModel string
}

// ValidateResponse checks a response against the request that produced it.
//
// The request is required rather than optional because two of the checks are
// relational: the served model must match the requested one, and every
// distribution must cover exactly the criteria that were offered. A response
// cannot be judged in isolation.
func ValidateResponse(req *SystemOneRequest, resp *SystemOneResponse) (*Assessment, error) {
	if req == nil || resp == nil {
		return nil, fmt.Errorf("%w: nil request or response", ErrSchemaInvalid)
	}
	if !IsPinnedModel(req.Model) {
		return nil, fmt.Errorf("%w: %q", ErrModelInvalid, req.Model)
	}
	served := strings.TrimSpace(resp.Model)
	if served != req.Model {
		return nil, fmt.Errorf("%w: requested %q, served %q", ErrModelDrift, req.Model, served)
	}
	if len(resp.Answers) == 0 {
		return nil, fmt.Errorf("%w: empty answers", ErrSchemaInvalid)
	}

	var envelope struct {
		CandidateAction   *ChoiceAnswer `json:"candidate_action"`
		EvidenceQuality   *ScoreAnswer  `json:"evidence_quality"`
		StrategyAlignment *NoulAnswer   `json:"strategy_alignment"`
	}
	if err := json.Unmarshal(resp.Answers, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSchemaInvalid, err)
	}
	if envelope.CandidateAction == nil {
		return nil, fmt.Errorf("%w: %s", ErrAnswerMissing, KeyCandidateAction)
	}
	if envelope.EvidenceQuality == nil {
		return nil, fmt.Errorf("%w: %s", ErrAnswerMissing, KeyEvidenceQuality)
	}
	if envelope.StrategyAlignment == nil {
		return nil, fmt.Errorf("%w: %s", ErrAnswerMissing, KeyStrategyAlignment)
	}

	choiceQuestion, err := DecodeChoice(req.Questions[KeyCandidateAction])
	if err != nil {
		return nil, fmt.Errorf("%w: choice question: %w", ErrQuestionsInvalid, err)
	}
	scoreQuestion, err := DecodeScore(req.Questions[KeyEvidenceQuality])
	if err != nil {
		return nil, fmt.Errorf("%w: score question: %w", ErrQuestionsInvalid, err)
	}

	if err := validateChoice(choiceQuestion, envelope.CandidateAction); err != nil {
		return nil, err
	}
	if err := validateScore(scoreQuestion, envelope.EvidenceQuality); err != nil {
		return nil, err
	}
	if envelope.StrategyAlignment.Type != AnswerNoul {
		return nil, fmt.Errorf("%w: strategy_alignment type %q", ErrSchemaInvalid, envelope.StrategyAlignment.Type)
	}
	if math.IsNaN(envelope.StrategyAlignment.Noul) || envelope.StrategyAlignment.Noul < 0 || envelope.StrategyAlignment.Noul > 1 {
		return nil, fmt.Errorf("%w: %v", ErrNoulInvalid, envelope.StrategyAlignment.Noul)
	}

	return &Assessment{
		CandidateAction:   envelope.CandidateAction,
		EvidenceQuality:   envelope.EvidenceQuality,
		StrategyAlignment: envelope.StrategyAlignment,
		Usage:             resp.Usage,
		ResponseModel:     served,
	}, nil
}

func validateChoice(q *ChoiceQuestion, a *ChoiceAnswer) error {
	if a.Type != AnswerChoice {
		return fmt.Errorf("%w: candidate_action type %q", ErrSchemaInvalid, a.Type)
	}
	if len(q.Criteria) < 2 {
		return fmt.Errorf("%w: choice question offered %d criteria", ErrQuestionsInvalid, len(q.Criteria))
	}
	if _, ok := q.Criteria[a.Choice]; !ok {
		return fmt.Errorf("%w: %q is not one of the offered criteria", ErrChoiceInvalid, a.Choice)
	}
	if len(a.Probabilities) != len(q.Criteria) {
		return fmt.Errorf("%w: %d probabilities for %d criteria", ErrDistributionInvalid, len(a.Probabilities), len(q.Criteria))
	}
	var sum, highest float64
	for key, p := range a.Probabilities {
		if _, ok := q.Criteria[key]; !ok {
			return fmt.Errorf("%w: unexpected key %q", ErrDistributionInvalid, key)
		}
		if math.IsNaN(p) || p < 0 || p > 1 {
			return fmt.Errorf("%w: %q = %v", ErrProbabilityInvalid, key, p)
		}
		sum += p
		if p > highest {
			highest = p
		}
	}
	if math.Abs(sum-1) > ProbabilitySumTolerance {
		return fmt.Errorf("%w: sum %v", ErrProbabilityInvalid, sum)
	}
	if a.Probabilities[a.Choice] < highest-ChoiceMarginTolerance {
		return fmt.Errorf("%w: %q scored %v while the maximum is %v",
			ErrChoiceInvalid, a.Choice, a.Probabilities[a.Choice], highest)
	}
	if math.IsNaN(a.Confidence) || a.Confidence < 0 || a.Confidence > 1 {
		return fmt.Errorf("%w: confidence %v", ErrChoiceInvalid, a.Confidence)
	}
	return nil
}

func validateScore(q *ScoreQuestion, a *ScoreAnswer) error {
	if a.Type != AnswerScore {
		return fmt.Errorf("%w: evidence_quality type %q", ErrSchemaInvalid, a.Type)
	}
	if len(q.Criteria) < 2 {
		return fmt.Errorf("%w: score question offered %d criteria", ErrQuestionsInvalid, len(q.Criteria))
	}
	if len(a.Legend) != len(q.Criteria) {
		return fmt.Errorf("%w: legend holds %d entries for a %d point scale", ErrScoreInvalid, len(a.Legend), len(q.Criteria))
	}
	if len(a.Probabilities) != len(q.Criteria) {
		return fmt.Errorf("%w: %d probabilities for a %d point scale", ErrDistributionInvalid, len(a.Probabilities), len(q.Criteria))
	}
	var sum, weighted float64
	for i := range len(q.Criteria) {
		key := strconv.Itoa(i)
		p, ok := a.Probabilities[key]
		if !ok {
			return fmt.Errorf("%w: missing bucket %q", ErrDistributionInvalid, key)
		}
		if math.IsNaN(p) || p < 0 || p > 1 {
			return fmt.Errorf("%w: bucket %q = %v", ErrProbabilityInvalid, key, p)
		}
		sum += p
		weighted += float64(i) * p
	}
	if math.Abs(sum-1) > ProbabilitySumTolerance {
		return fmt.Errorf("%w: sum %v", ErrProbabilityInvalid, sum)
	}
	maxScore := float64(len(q.Criteria) - 1)
	if math.IsNaN(a.Score) || a.Score < 0 || a.Score > maxScore {
		return fmt.Errorf("%w: score %v must be 0..%v", ErrScoreInvalid, a.Score, maxScore)
	}
	if math.Abs(a.Score-weighted) > WeightedScoreTolerance {
		return fmt.Errorf("%w: score %v disagrees with its distribution (%v)", ErrScoreInvalid, a.Score, weighted)
	}
	if math.IsNaN(a.Confidence) || a.Confidence < 0 || a.Confidence > 1 {
		return fmt.Errorf("%w: confidence %v", ErrScoreInvalid, a.Confidence)
	}
	return nil
}

// ValidateChoiceAnswer exposes the choice rules so a probe can validate a
// synthetic reply without going through the full review envelope.
func ValidateChoiceAnswer(q *ChoiceQuestion, a *ChoiceAnswer) error { return validateChoice(q, a) }

// ValidateScoreAnswer exposes the score rules for the same reason.
func ValidateScoreAnswer(q *ScoreQuestion, a *ScoreAnswer) error { return validateScore(q, a) }

// ValidateNoulAnswer checks a noul reply in isolation.
func ValidateNoulAnswer(a *NoulAnswer) error {
	if a == nil {
		return fmt.Errorf("%w: nil answer", ErrSchemaInvalid)
	}
	if a.Type != AnswerNoul {
		return fmt.Errorf("%w: strategy_alignment type %q", ErrSchemaInvalid, a.Type)
	}
	if math.IsNaN(a.Noul) || a.Noul < 0 || a.Noul > 1 {
		return fmt.Errorf("%w: %v", ErrNoulInvalid, a.Noul)
	}
	return nil
}
