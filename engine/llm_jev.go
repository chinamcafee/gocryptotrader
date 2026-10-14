package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/jev"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// M4-09 Jev 评估流水线（docs 06 §4/§5）。
//
// 裁判看到的事实必须与分析师逐字节一致——那是 M2-10 的构造器（纯函数）已经
// 钉死的事；本层是调用位：复验（构造器内建）→ 64KiB 体积闸 → 预算预约 →
// 单次 SystemOne → 结算 → **结束前重查 pair_epoch**（配对在评估途中被换掉，
// 这份评估就不再属于当前世界——STALE_PAIR，不是继续用）。

// Reason codes for the Jev stage.
const (
	// ReasonJevEvidenceInvalid marks evidence that failed §4.2 re-validation.
	ReasonJevEvidenceInvalid = "ANALYSIS_EVIDENCE_INVALID"
	// ReasonJevRequestTooLarge marks a request over the 64KiB cap.
	ReasonJevRequestTooLarge = "REQUEST_TOO_LARGE"
	// ReasonJevStalePair marks a pair change while the judge was running.
	ReasonJevStalePair = "STALE_PAIR"
	// JevManifestStateBound is the recorded manifest state when the review's
	// input manifest was bound (the A group's allowed value).
	JevManifestStateBound = "MANIFEST_BOUND"
)

// iJevJudge is the transport seam; *jev.Client satisfies it.
type iJevJudge interface {
	SystemOne(ctx context.Context, req *jev.SystemOneRequest) (*jev.Assessment, error)
}

// iJevJudgeManifest is the seam's manifest-capable form: the production client
// implements it, so the pipeline can bind the evidence manifest and the risk
// stage's A group can prove what the judge was shown.
type iJevJudgeManifest interface {
	SystemOneWithManifest(ctx context.Context, req *jev.SystemOneRequest) (*jev.Assessment, *jev.InputManifest, error)
}

// JevReviewRequest is one judge call.
type JevReviewRequest struct {
	Model               string
	Network             string
	StrategyVersionID   string
	StrategyInstruction string
	Snapshot            jev.SnapshotRef
	Candidate           *candidate.Candidate
	// Evidence is the analyst's retained tool evidence, byte for byte.
	Evidence       []candidate.ToolEvidence
	EvidencePolicy jev.EvidencePolicy
	Safety         jev.SafetyStatus
	Manifest       *jev.InputManifest
	// ProfileRevisionID is the Jev revision; ExpectedPairEpoch is the epoch the
	// run started under.
	ProfileRevisionID string
	ExpectedPairEpoch int64
}

// JevReviewOutcome is the stage's answer.
//
// A policy refusal is an outcome with a ReasonCode and a Detail, not an error:
// the pipeline wants it recorded in the decision run, and "the evidence was
// rejected because <detail>" is a fact about the decision, not a crash.
type JevReviewOutcome struct {
	Assessment    *jev.Assessment `json:"assessment,omitempty"`
	RequestSha256 string          `json:"requestSha256,omitempty"`
	ReasonCode    string          `json:"reasonCode,omitempty"`
	Detail        string          `json:"detail,omitempty"`
	InputTokens   int64           `json:"inputTokens"`
	OutputTokens  int64           `json:"outputTokens"`
	LatencyMS     int64           `json:"latencyMs"`
	// Manifest binds the exact request/response bytes the judge saw. Nil when
	// the judge seam could not produce one: the A group then refuses to
	// advance (RISK_JEV_MANIFEST_NOT_BOUND) rather than pretending.
	Manifest *jev.InputManifest `json:"manifest,omitempty"`
}

// JevPipeline runs judge evaluations.
type JevPipeline struct {
	Judge     iJevJudge
	Budget    iChatBudget
	Revisions iChatRevisions
	// PairEpoch reads the current epoch; it is consulted once more after the
	// judge answers.
	PairEpoch func(ctx context.Context) (int64, error)
	Now       func() time.Time
}

// Review executes one judge call.
func (p *JevPipeline) Review(ctx context.Context, req *JevReviewRequest) (*JevReviewOutcome, error) {
	if p == nil || p.Judge == nil || p.Budget == nil || p.Revisions == nil || p.PairEpoch == nil {
		return nil, ErrChatNotConfigured
	}
	now := p.now()
	revision, err := p.Revisions.Revision(ctx, req.ProfileRevisionID)
	if err != nil {
		return nil, fmt.Errorf("llm jev: resolving revision %q: %w", req.ProfileRevisionID, err)
	}
	if revision.Kind != ProviderKindJev {
		return nil, fmt.Errorf("%w: %q is %s", ErrChatProfileKind, req.ProfileRevisionID, revision.Kind)
	}

	// The pure constructor re-runs every §4.2 check (prompt version, ≤8
	// records, per-entry and total byte budgets, SHA-256 recomputation against
	// the manifest, schema, scope, sensitive keys, duplicate refs, candidate
	// validity) and the byte-cap Marshal below guards the 64KiB limit.
	built, err := jev.BuildReviewRequest(&jev.ReviewInput{
		Model:               req.Model,
		Network:             req.Network,
		StrategyVersionID:   req.StrategyVersionID,
		StrategyInstruction: req.StrategyInstruction,
		Snapshot:            req.Snapshot,
		Candidate:           req.Candidate,
		Evidence:            req.Evidence,
		EvidencePolicy:      req.EvidencePolicy,
		Safety:              req.Safety,
		PromptVersion:       openai.PromptVersionAnalysisReadOnly,
		Manifest:            req.Manifest,
		Now:                 now,
	})
	if err != nil {
		return &JevReviewOutcome{ReasonCode: ReasonJevEvidenceInvalid, Detail: err.Error()}, nil //nolint:nilerr // a refused review is an outcome (Detail carries the cause), not an error
	}
	body, err := built.Marshal()
	if err != nil {
		return &JevReviewOutcome{ReasonCode: ReasonJevRequestTooLarge, Detail: err.Error()}, nil //nolint:nilerr // a refused review is an outcome (Detail carries the cause), not an error
	}

	reservation, err := p.Budget.Reserve(ctx, BudgetReservationRequest{
		ProfileRevisionID: revision.ID,
		ProviderKind:      ProviderKindJev,
		Purpose:           "DECISION",
	}, now)
	if err != nil {
		if errorsIsBudgetGate(err) {
			return &JevReviewOutcome{ReasonCode: ReasonBudgetExhausted, RequestSha256: jev.HashBytes(body)}, nil
		}
		return nil, fmt.Errorf("llm jev: budget gate: %w", err)
	}

	var assessment *jev.Assessment
	var manifest *jev.InputManifest
	if withManifest, ok := p.Judge.(iJevJudgeManifest); ok {
		assessment, manifest, err = withManifest.SystemOneWithManifest(ctx, built)
	} else {
		assessment, err = p.Judge.SystemOne(ctx, built)
	}
	if err != nil {
		_ = p.Budget.Release(ctx, reservation.ID, p.now())
		return nil, fmt.Errorf("llm jev: judging: %w", err)
	}
	latency := p.now().Sub(now).Milliseconds()
	if assessment == nil {
		_ = p.Budget.Release(ctx, reservation.ID, p.now())
		return nil, errors.New("llm jev: nil assessment")
	}
	if settleErr := p.Budget.MarkUsed(ctx, reservation.ID, int64(assessment.Usage.InputTokens), int64(assessment.Usage.OutputTokens), latency, p.now()); settleErr != nil {
		return nil, fmt.Errorf("llm jev: settling the reservation: %w", settleErr)
	}
	outcome := &JevReviewOutcome{
		RequestSha256: jev.HashBytes(body),
		InputTokens:   int64(assessment.Usage.InputTokens),
		OutputTokens:  int64(assessment.Usage.OutputTokens),
		LatencyMS:     latency,
		Manifest:      manifest,
	}

	// The pair is re-read after the judge answered (06 §5): a verdict about a
	// pairing that no longer exists is not a verdict about this world.
	epoch, err := p.PairEpoch(ctx)
	if err != nil {
		return nil, fmt.Errorf("llm jev: re-reading the pair epoch: %w", err)
	}
	if epoch != req.ExpectedPairEpoch {
		outcome.ReasonCode = ReasonJevStalePair
		outcome.Detail = fmt.Sprintf("epoch moved from %d to %d while the judge ran", req.ExpectedPairEpoch, epoch)
		return outcome, nil
	}
	outcome.Assessment = assessment
	return outcome, nil
}

// now returns the pipeline clock, defaulting to the wall clock.
func (p *JevPipeline) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

// errorsIsBudgetGate reports whether a reserve failure is the budget refusing
// rather than infrastructure failing.
func errorsIsBudgetGate(err error) bool {
	return err != nil && (strings.Contains(err.Error(), ErrBudgetExhausted.Error()) ||
		strings.Contains(err.Error(), ErrBudgetDisabled.Error()))
}
