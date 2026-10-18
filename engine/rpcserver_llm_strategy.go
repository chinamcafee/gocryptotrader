package engine

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// LLM strategy and decision RPCs (docs/llm-dev 08 §3.2/§3.5).
//
// Same shape as the conversation RPCs: thin conversions over the engine
// services and the repository layer. Read paths are direct repository calls;
// write paths go through the same services the internal pipelines use, so an
// operator command and an automatic run cannot diverge.
//
// Trigger/preview run the shadow decision chain through the assembled decision
// runner (per-run facts: snapshot assembly and the Jev evidence manifest); they
// answer HOLD/REVIEW records and can never place an order. They fail closed
// while the runner is not assembled, or when a per-run fact cannot be sourced.

// errLLMDecisionRunner marks trigger/preview before the decision runner's
// production wiring lands (the same fail-closed convention as
// errLLMChatUnavailable).
var errLLMDecisionRunner = errors.New("llm decision runner is not configured on this bot")

// errLLMStrategyInput marks an operator command missing required fields.
var errLLMStrategyInput = errors.New("llm strategy command: required field missing")

// strategyToProto renders a strategy row.
func strategyToProto(s *llmstore.Strategy) *gctrpc.LLMStrategy {
	return &gctrpc.LLMStrategy{
		Id:        s.ID,
		Name:      s.Name,
		CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
		CreatedBy: s.CreatedBy,
		Archived:  s.ArchivedAt != nil,
	}
}

// versionToProto renders a version row.
func versionToProto(v *llmstore.StrategyVersion) *gctrpc.LLMStrategyVersion {
	out := &gctrpc.LLMStrategyVersion{
		Id:               v.ID,
		StrategyId:       v.StrategyID,
		State:            v.State,
		CreatedAt:        v.CreatedAt.UTC().Format(time.RFC3339),
		RiskEnvelopeHash: derefOrEmpty(v.RiskEnvelopeHash),
		StrategyHash:     derefOrEmpty(v.StrategyHash),
		ProposalState:    derefOrEmpty(v.ProposalState),
	}
	if v.ParentVersionID != nil {
		out.ParentVersionId = *v.ParentVersionID
	}
	if v.FrozenAt != nil {
		out.FrozenAt = v.FrozenAt.UTC().Format(time.RFC3339)
	}
	return out
}

// decisionRunToProto renders a decision run.
func decisionRunToProto(r *llmstore.DecisionRun) *gctrpc.LLMDecisionRun {
	out := &gctrpc.LLMDecisionRun{
		Id:            r.ID,
		Trigger:       r.Trigger,
		RunMode:       r.RunMode,
		Decision:      r.Decision,
		Reason:        r.Reason,
		InputHash:     r.InputHash,
		LlmModel:      r.LLMModel,
		CreatedAt:     r.CreatedAt.UTC().Format(time.RFC3339),
		LatencyMs:     r.LatencyMS,
		LlmRoundCount: r.LLMRoundCount,
		ToolCallCount: r.ToolCallCount,
	}
	if r.StrategyVersionID != nil {
		out.StrategyVersionId = *r.StrategyVersionID
	}
	if r.SnapshotID != nil {
		out.SnapshotId = *r.SnapshotID
	}
	if r.JevModel != nil {
		out.JevModel = *r.JevModel
	}
	return out
}

// riskEvidenceToProto renders a stored risk evaluation.
func riskEvidenceToProto(e *llmstore.RiskEvaluation) *gctrpc.LLMRiskEvidence {
	return &gctrpc.LLMRiskEvidence{
		RiskEvaluationId:    e.ID,
		CheckVersion:        e.CheckVersion,
		Verdict:             e.Verdict,
		ChecksJson:          e.ChecksJSON,
		RejectionsJson:      derefOrEmpty(e.RejectionsJSON),
		EnvelopeHash:        e.EnvelopeHash,
		ComputedQty:         derefOrEmpty(e.ComputedQty),
		ComputedNotionalUsd: derefOrEmpty(e.ComputedNotional),
		ReferencePrice:      derefOrEmpty(e.ReferencePrice),
		EvaluatedAt:         e.EvaluatedAt.UTC().Format(time.RFC3339),
	}
}

// auditEventToProto renders one audit event.
func auditEventToProto(e *llmstore.AuditEvent) *gctrpc.LLMAuditEvent {
	return &gctrpc.LLMAuditEvent{
		Id:         e.ID,
		Kind:       e.Kind,
		Actor:      e.Actor,
		Subject:    derefOrEmpty(e.Subject),
		DetailJson: e.DetailJSON,
		CreatedAt:  e.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// approvalToProto renders a stored strategy approval.
func approvalToProto(a *llmstore.StrategyApproval) (*gctrpc.LLMStrategyApproval, error) {
	markets, err := DecodeApprovedMarkets(a.ApprovedMarketIDs)
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMStrategyApproval{
		Id:                a.ID,
		StrategyVersionId: a.StrategyVersionID,
		AccountId:         a.AccountID,
		Network:           a.Network,
		ExecutionMode:     a.ExecutionMode,
		ApprovedMarketIds: markets,
		RiskEnvelopeHash:  a.RiskEnvelopeHash,
		PairEpoch:         a.PairEpoch,
		ApprovedBy:        a.ApprovedBy,
	}
	if !a.ValidFrom.IsZero() {
		out.ValidFrom = a.ValidFrom.UTC().Format(time.RFC3339)
	}
	if !a.ValidUntil.IsZero() {
		out.ValidUntil = a.ValidUntil.UTC().Format(time.RFC3339)
	}
	if !a.ApprovedAt.IsZero() {
		out.ApprovedAt = a.ApprovedAt.UTC().Format(time.RFC3339)
	}
	if a.RevokedAt != nil {
		out.RevokedAt = a.RevokedAt.UTC().Format(time.RFC3339)
	}
	if a.RevokedBy != nil {
		out.RevokedBy = *a.RevokedBy
	}
	if a.RevokeReason != nil {
		out.RevokeReason = *a.RevokeReason
	}
	if a.ExpiredAt != nil {
		out.ExpiredAt = a.ExpiredAt.UTC().Format(time.RFC3339)
	}
	return out, nil
}

// LLMListStrategies lists strategies.
func (s *RPCServer) LLMListStrategies(ctx context.Context, r *gctrpc.LLMListStrategiesRequest) (*gctrpc.LLMListStrategiesResponse, error) {
	strategies, err := llmstore.ListStrategies(ctx, r.GetIncludeArchived())
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMListStrategiesResponse{}
	for i := range strategies {
		strategy := &strategies[i]
		out.Strategies = append(out.Strategies, strategyToProto(strategy))
	}
	return out, nil
}

// LLMGetStrategyVersion returns one version with its tree and policy document.
func (s *RPCServer) LLMGetStrategyVersion(ctx context.Context, r *gctrpc.LLMGetStrategyVersionRequest) (*gctrpc.LLMGetStrategyVersionResponse, error) {
	if r.GetId() == "" {
		return nil, fmt.Errorf("%w: id", errLLMStrategyInput)
	}
	version, err := llmstore.GetStrategyVersion(ctx, r.GetId())
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMGetStrategyVersionResponse{Version: versionToProto(version)}
	if version.FrozenJSON != nil {
		out.PolicyJson = *version.FrozenJSON
	} else if version.ProposalJSON != nil {
		out.PolicyJson = *version.ProposalJSON
	}
	versions, err := llmstore.ListStrategyVersions(ctx, version.StrategyID)
	if err == nil {
		if tree, treeErr := BuildVersionTree(versions); treeErr == nil {
			for i := range tree {
				node := tree[i]
				out.Tree = append(out.Tree, versionToProto(&node.Version))
			}
		}
	}
	return out, nil
}

// LLMExtractPolicy runs a policy extraction on a conversation.
func (s *RPCServer) LLMExtractPolicy(ctx context.Context, r *gctrpc.LLMExtractPolicyRequest) (*gctrpc.LLMExtractPolicyResponse, error) {
	if r.GetConversationId() == "" || r.GetProfileRevisionId() == "" {
		return nil, fmt.Errorf("%w: conversation_id and profile_revision_id", errLLMStrategyInput)
	}
	chat, err := s.chatService()
	if err != nil {
		return nil, err
	}
	extractor := &PolicyExtractor{
		Transport: chat.Pipeline.Transport,
		History:   chat.History,
		Cipher:    chat.Cipher,
		Budget:    chat.Pipeline.Budget,
		Revisions: chat.Pipeline.Revisions,
		Now:       time.Now,
	}
	result, err := extractor.Extract(ctx, PolicyExtractionRequest{
		ConversationID:    r.GetConversationId(),
		ProfileRevisionID: r.GetProfileRevisionId(),
		Markets:           r.GetMarkets(),
	})
	if err != nil {
		return nil, err
	}
	proposalJSON, err := json.Marshal(result.Proposal)
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMExtractPolicyResponse{
		ProposalJson:        string(proposalJSON),
		State:               string(result.State),
		ClarifyingQuestions: result.Proposal.ClarifyingQuestions,
		InputTokens:         result.InputTokens,
		OutputTokens:        result.OutputTokens,
		LatencyMs:           result.LatencyMS,
	}
	return out, nil
}

// LLMFreezeStrategyVersion freezes exactly the document the operator reviewed.
func (s *RPCServer) LLMFreezeStrategyVersion(ctx context.Context, r *gctrpc.LLMFreezeStrategyVersionRequest) (*gctrpc.LLMFreezeStrategyVersionResponse, error) {
	if r.GetId() == "" || r.GetActor() == "" || r.GetPolicyJson() == "" {
		return nil, fmt.Errorf("%w: id, actor and policy_json", errLLMStrategyInput)
	}
	var policy candidate.ReviewablePolicy
	if err := json.Unmarshal([]byte(r.GetPolicyJson()), &policy); err != nil {
		return nil, fmt.Errorf("llm freeze: policy json: %w", err)
	}
	freezer := &StrategyFreezer{Versions: LLMStrategyVersions{}, Now: time.Now, NewID: newLLMIdentifier}
	result, err := freezer.Freeze(ctx, r.GetId(), &policy, r.GetActor())
	if err != nil {
		return nil, err
	}
	return &gctrpc.LLMFreezeStrategyVersionResponse{
		StrategyHash:     result.StrategyHash,
		RiskEnvelopeHash: result.RiskEnvelopeHash,
		FrozenJson:       result.FrozenJSON,
		FrozenAt:         result.FrozenAt.UTC().Format(time.RFC3339),
	}, nil
}

// LLMApproveStrategyVersion approves a frozen version after re-authentication.
func (s *RPCServer) LLMApproveStrategyVersion(ctx context.Context, r *gctrpc.LLMApproveStrategyVersionRequest) (*gctrpc.LLMApproveStrategyVersionResponse, error) {
	if r.GetStrategyVersionId() == "" || r.GetAccountId() == "" || r.GetNetwork() == "" ||
		r.GetExecutionMode() == "" || r.GetOperatorPassword() == "" || r.GetIdempotencyKey() == "" {
		return nil, fmt.Errorf("%w: approval fields", errLLMStrategyInput)
	}
	service := &ApprovalService{
		Approvals:      LLMApprovals{},
		VerifyPassword: s.verifyOperatorPassword,
		Now:            time.Now,
		NewID:          newLLMIdentifier,
	}
	approval, replay, err := service.Approve(ctx, &ApprovalRequest{
		Actor:             r.GetActor(),
		VersionID:         r.GetStrategyVersionId(),
		AccountID:         r.GetAccountId(),
		Network:           r.GetNetwork(),
		ExecutionMode:     r.GetExecutionMode(),
		Markets:           r.GetApprovedMarketIds(),
		ValidFor:          time.Duration(r.GetValidMinutes()) * time.Minute,
		OperatorPassword:  r.GetOperatorPassword(),
		IdempotencyKey:    r.GetIdempotencyKey(),
		ExpectedPairEpoch: r.GetExpectedPairEpoch(),
	})
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMApproveStrategyVersionResponse{
		ApprovalId:       approval.ID,
		IdempotentReplay: replay,
	}
	if !approval.ApprovedAt.IsZero() {
		out.ApprovedAt = approval.ApprovedAt.UTC().Format(time.RFC3339)
	}
	if !approval.ValidUntil.IsZero() {
		out.ValidUntil = approval.ValidUntil.UTC().Format(time.RFC3339)
	}
	return out, nil
}

// LLMRevokeApproval withdraws an approval.
func (s *RPCServer) LLMRevokeApproval(ctx context.Context, r *gctrpc.LLMRevokeApprovalRequest) (*gctrpc.GenericResponse, error) {
	if r.GetId() == "" || r.GetActor() == "" {
		return nil, fmt.Errorf("%w: id and actor", errLLMStrategyInput)
	}
	reason := r.GetReason()
	if reason == "" {
		reason = "operator revoke"
	}
	if err := llmstore.RevokeStrategyApproval(ctx, r.GetId(), time.Now(), r.GetActor(), reason); err != nil {
		return nil, err
	}
	return &gctrpc.GenericResponse{Status: "ok"}, nil
}

// LLMPauseStrategy suspends a version: the state machine's PAUSED transition
// stops it from being scheduled without touching the approval row.
func (s *RPCServer) LLMPauseStrategy(ctx context.Context, r *gctrpc.LLMPauseStrategyRequest) (*gctrpc.GenericResponse, error) {
	return s.setStrategyVersionState(ctx, r.GetStrategyVersionId(), r.GetActor(), r.GetReason(), StrategyStatePaused)
}

// LLMResumeStrategy returns a PAUSED version to FROZEN when it still holds a
// live approval; a version whose approval was revoked must be re-approved, not
// silently resumed.
func (s *RPCServer) LLMResumeStrategy(ctx context.Context, r *gctrpc.LLMResumeStrategyRequest) (*gctrpc.GenericResponse, error) {
	versionID := r.GetStrategyVersionId()
	if versionID == "" || r.GetActor() == "" {
		return nil, fmt.Errorf("%w: strategy_version_id and actor", errLLMStrategyInput)
	}
	approval, err := llmstore.GetStrategyApprovalByVersion(ctx, versionID)
	if err != nil || approval == nil {
		return nil, fmt.Errorf("%w: strategy version %s has no live approval", ErrApprovalRequired, versionID)
	}
	if approval.RevokedAt != nil || (!approval.ValidUntil.IsZero() && approval.ValidUntil.Before(time.Now())) {
		return nil, fmt.Errorf("%w: strategy version %s has no live approval", ErrApprovalRequired, versionID)
	}
	return s.setStrategyVersionState(ctx, versionID, r.GetActor(), r.GetReason(), "FROZEN")
}

// setStrategyVersionState advances one version and records the transition.
func (s *RPCServer) setStrategyVersionState(ctx context.Context, versionID, actor, reason, toState string) (*gctrpc.GenericResponse, error) {
	if versionID == "" || actor == "" {
		return nil, fmt.Errorf("%w: strategy_version_id and actor", errLLMStrategyInput)
	}
	version, err := llmstore.GetStrategyVersion(ctx, versionID)
	if err != nil {
		return nil, err
	}
	if version.State == toState {
		return &gctrpc.GenericResponse{Status: "ok"}, nil
	}
	if err := llmstore.UpdateStrategyVersionState(ctx, versionID, toState); err != nil {
		return nil, err
	}
	if reason == "" {
		reason = "operator command"
	}
	if err := llmstore.InsertStrategyStateTransition(ctx, &llmstore.StrategyStateTransition{
		ID: newLLMIdentifier(), StrategyVersionID: versionID,
		FromState: version.State, ToState: toState, Reason: reason,
		Actor: actor, CreatedAt: time.Now(),
	}); err != nil {
		return nil, err
	}
	return &gctrpc.GenericResponse{Status: "ok"}, nil
}

// LLMDiffStrategyVersions diffs two frozen policies field by field.
func (s *RPCServer) LLMDiffStrategyVersions(ctx context.Context, r *gctrpc.LLMDiffStrategyVersionsRequest) (*gctrpc.LLMDiffStrategyVersionsResponse, error) {
	if r.GetFromId() == "" || r.GetToId() == "" {
		return nil, fmt.Errorf("%w: from_id and to_id", errLLMStrategyInput)
	}
	load := func(id string) (*candidate.ReviewablePolicy, error) {
		version, err := llmstore.GetStrategyVersion(ctx, id)
		if err != nil {
			return nil, err
		}
		if version.FrozenJSON == nil {
			return nil, fmt.Errorf("%w: version %s is not frozen", ErrFreezeInputInvalid, id)
		}
		var policy candidate.ReviewablePolicy
		if err := json.Unmarshal([]byte(*version.FrozenJSON), &policy); err != nil {
			return nil, fmt.Errorf("llm diff: version %s: %w", id, err)
		}
		return &policy, nil
	}
	from, err := load(r.GetFromId())
	if err != nil {
		return nil, err
	}
	to, err := load(r.GetToId())
	if err != nil {
		return nil, err
	}
	diff, err := DiffPolicies(from, to)
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMDiffStrategyVersionsResponse{
		FromVersionId: diff.FromVersionID,
		ToVersionId:   diff.ToVersionID,
	}
	for i := range diff.Changes {
		change := &diff.Changes[i]
		out.Changes = append(out.Changes, &gctrpc.LLMPolicyChange{
			Field: change.Field, From: change.From, To: change.To,
		})
	}
	return out, nil
}

// LLMCreateStrategy creates a strategy row (W5 workspace bootstrap).
func (s *RPCServer) LLMCreateStrategy(ctx context.Context, r *gctrpc.LLMCreateStrategyRequest) (*gctrpc.LLMCreateStrategyResponse, error) {
	admin := &LLMStrategyAdmin{Store: LLMStrategyAdminStore{}, Now: time.Now, NewID: newLLMIdentifier}
	strategy, err := admin.CreateStrategy(ctx, r.GetName(), r.GetActor())
	if err != nil {
		return nil, err
	}
	return &gctrpc.LLMCreateStrategyResponse{Strategy: strategyToProto(strategy)}, nil
}

// LLMListStrategyVersions lists one strategy's versions.
func (s *RPCServer) LLMListStrategyVersions(ctx context.Context, r *gctrpc.LLMListStrategyVersionsRequest) (*gctrpc.LLMListStrategyVersionsResponse, error) {
	if r.GetStrategyId() == "" {
		return nil, fmt.Errorf("%w: strategy_id", errLLMStrategyInput)
	}
	versions, err := llmstore.ListStrategyVersions(ctx, r.GetStrategyId())
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMListStrategyVersionsResponse{}
	for i := range versions {
		out.Versions = append(out.Versions, versionToProto(&versions[i]))
	}
	return out, nil
}

// LLMSaveStrategyDraft persists an extraction proposal as a draft version.
func (s *RPCServer) LLMSaveStrategyDraft(ctx context.Context, r *gctrpc.LLMSaveStrategyDraftRequest) (*gctrpc.LLMSaveStrategyDraftResponse, error) {
	admin := &LLMStrategyAdmin{Store: LLMStrategyAdminStore{}, Now: time.Now, NewID: newLLMIdentifier}
	version, err := admin.SavePolicyDraft(ctx, r.GetStrategyId(), r.GetParentVersionId(), r.GetProposalJson(), r.GetActor())
	if err != nil {
		return nil, err
	}
	return &gctrpc.LLMSaveStrategyDraftResponse{Version: versionToProto(version)}, nil
}

// LLMGetStrategyApproval returns the approval bound to a version, if any.
func (s *RPCServer) LLMGetStrategyApproval(ctx context.Context, r *gctrpc.LLMGetStrategyApprovalRequest) (*gctrpc.LLMGetStrategyApprovalResponse, error) {
	if r.GetStrategyVersionId() == "" {
		return nil, fmt.Errorf("%w: strategy_version_id", errLLMStrategyInput)
	}
	approval, err := llmstore.GetStrategyApprovalByVersion(ctx, r.GetStrategyVersionId())
	if err != nil {
		if errors.Is(err, llmstore.ErrNotFound) {
			// No approval row is a state, not an error.
			return &gctrpc.LLMGetStrategyApprovalResponse{}, nil
		}
		return nil, err
	}
	rendered, err := approvalToProto(approval)
	if err != nil {
		return nil, err
	}
	return &gctrpc.LLMGetStrategyApprovalResponse{Found: true, Approval: rendered}, nil
}

// LLMListDecisionRuns lists one strategy's decision runs, newest first.
func (s *RPCServer) LLMListDecisionRuns(ctx context.Context, r *gctrpc.LLMListDecisionRunsRequest) (*gctrpc.LLMListDecisionRunsResponse, error) {
	runs, err := llmstore.ListDecisionRunsByStrategy(ctx, r.GetStrategyVersionId(), int(r.GetLimit()))
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMListDecisionRunsResponse{}
	for i := range runs {
		run := &runs[i]
		out.Runs = append(out.Runs, decisionRunToProto(run))
	}
	return out, nil
}

// LLMGetDecisionRun returns one run with its stored source documents.
func (s *RPCServer) LLMGetDecisionRun(ctx context.Context, r *gctrpc.LLMGetDecisionRunRequest) (*gctrpc.LLMGetDecisionRunResponse, error) {
	if r.GetId() == "" {
		return nil, fmt.Errorf("%w: id", errLLMStrategyInput)
	}
	run, err := llmstore.GetDecisionRun(ctx, r.GetId())
	if err != nil {
		return nil, err
	}
	return &gctrpc.LLMGetDecisionRunResponse{
		Run:                decisionRunToProto(run),
		CandidateJson:      derefOrEmpty(run.CandidateJSON),
		JevJson:            derefOrEmpty(run.JevJSON),
		ToolTranscriptJson: derefOrEmpty(run.ToolTranscriptJSON),
	}, nil
}

// LLMTriggerAnalysis triggers one automatic analysis.
//
// The run is the shadow decision chain (pre-gates -> analyst -> Jev ->
// synthesis -> record); it can never place an order. It fails closed while the
// decision runner is not assembled, or when any per-run fact cannot be sourced
// (no frozen version, no approval, unknown exchange): a trigger that cannot
// actually run must not report success.
func (s *RPCServer) LLMTriggerAnalysis(ctx context.Context, r *gctrpc.LLMTriggerAnalysisRequest) (*gctrpc.LLMDecisionRun, error) {
	return s.runShadowDecision(ctx, r.GetStrategyVersionId(), r.GetMarketId())
}

// LLMPreviewDecision is the same read-only preview: it produces HOLD/REVIEW
// records and never an order intent (docs 08 §3.5).
func (s *RPCServer) LLMPreviewDecision(ctx context.Context, r *gctrpc.LLMPreviewDecisionRequest) (*gctrpc.LLMDecisionRun, error) {
	return s.runShadowDecision(ctx, r.GetStrategyVersionId(), r.GetMarketId())
}

// runShadowDecision executes one trigger/preview and returns the recorded run.
func (s *RPCServer) runShadowDecision(ctx context.Context, strategyVersionID, marketID string) (*gctrpc.LLMDecisionRun, error) {
	if s.Engine == nil {
		return nil, errLLMDecisionRunner
	}
	runner := s.Engine.currentDecisionRunner()
	if runner == nil {
		return nil, errLLMDecisionRunner
	}
	if strings.TrimSpace(strategyVersionID) == "" || strings.TrimSpace(marketID) == "" {
		return nil, fmt.Errorf("%w: strategyVersionId", errLLMStrategyInput)
	}
	request, err := s.Engine.buildShadowRunRequest(ctx, strategyVersionID, marketID)
	if err != nil {
		return nil, err
	}
	result, err := runner.RunOnce(ctx, request)
	if err != nil {
		return nil, err
	}
	if result.Skipped {
		return &gctrpc.LLMDecisionRun{Reason: result.SkipReason}, nil
	}
	run, err := llmstore.GetDecisionRun(ctx, result.DecisionRunID)
	if err != nil {
		return nil, fmt.Errorf("llm trigger: reading the recorded run: %w", err)
	}
	return decisionRunToProto(run), nil
}

// LLMGetDecisionLineage returns the run, its risk evaluations and the audit
// events recorded for it (the A13 review surface).
func (s *RPCServer) LLMGetDecisionLineage(ctx context.Context, r *gctrpc.LLMGetDecisionLineageRequest) (*gctrpc.LLMGetDecisionLineageResponse, error) {
	if r.GetId() == "" {
		return nil, fmt.Errorf("%w: id", errLLMStrategyInput)
	}
	run, err := llmstore.GetDecisionRun(ctx, r.GetId())
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMGetDecisionLineageResponse{Run: decisionRunToProto(run)}
	riskRows, err := llmstore.ListRiskEvaluationsByRun(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	for i := range riskRows {
		row := &riskRows[i]
		out.RiskEvaluations = append(out.RiskEvaluations, riskEvidenceToProto(row))
	}
	events, err := llmstore.ListAuditEvents(ctx, AuditKindDecisionRecorded, 200)
	if err != nil {
		return nil, err
	}
	for i := range events {
		event := &events[i]
		if event.Subject != nil && *event.Subject == run.ID {
			out.AuditEvents = append(out.AuditEvents, auditEventToProto(event))
		}
	}
	return out, nil
}

// LLMGetRiskEvidence returns the "why was this rejected" evidence: every
// executed check with its actual vs allowed values.
func (s *RPCServer) LLMGetRiskEvidence(ctx context.Context, r *gctrpc.LLMGetRiskEvidenceRequest) (*gctrpc.LLMGetRiskEvidenceResponse, error) {
	if r.GetDecisionRunId() == "" {
		return nil, fmt.Errorf("%w: decision_run_id", errLLMStrategyInput)
	}
	rows, err := llmstore.ListRiskEvaluationsByRun(ctx, r.GetDecisionRunId())
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMGetRiskEvidenceResponse{}
	for i := range rows {
		row := &rows[i]
		out.RiskEvaluations = append(out.RiskEvaluations, riskEvidenceToProto(row))
	}
	return out, nil
}

// LLMListAuditEvents lists audit events, optionally filtered by kind.
func (s *RPCServer) LLMListAuditEvents(ctx context.Context, r *gctrpc.LLMListAuditEventsRequest) (*gctrpc.LLMListAuditEventsResponse, error) {
	events, err := llmstore.ListAuditEvents(ctx, r.GetKind(), int(r.GetLimit()))
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMListAuditEventsResponse{}
	for i := range events {
		event := &events[i]
		out.Events = append(out.Events, auditEventToProto(event))
	}
	return out, nil
}

// verifyOperatorPassword re-authenticates the operator for approval and other
// high-consequence commands, matching the RPC layer's own password check.
func (s *RPCServer) verifyOperatorPassword(_ context.Context, password string) error {
	if s == nil || s.Engine == nil || s.Config == nil || s.Config.RemoteControl.Password == "" {
		return errors.New("llm strategies: operator password is not configured")
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(s.Config.RemoteControl.Password)) != 1 {
		return errors.New("llm strategies: operator password rejected")
	}
	return nil
}

// newLLMIdentifier mints a UUID for rows created by operator commands.
func newLLMIdentifier() string {
	id, err := uuid.NewV4()
	if err != nil {
		return ""
	}
	return id.String()
}

// errLLMMetricsUnavailable marks the metrics RPC before the runtime wiring
// creates the registry (the same fail-closed convention as the chat service:
// an empty snapshot and a missing subsystem must not look the same).
var errLLMMetricsUnavailable = errors.New("llm metrics are not configured on this bot")

// LLMGetMetrics returns the metric snapshot for diagnostics (10 §6, mode C).
func (s *RPCServer) LLMGetMetrics(context.Context, *gctrpc.LLMGetMetricsRequest) (*gctrpc.LLMGetMetricsResponse, error) {
	if s.Engine == nil || s.Engine.llmMetrics == nil {
		return nil, errLLMMetricsUnavailable
	}
	snapshot := s.Engine.llmMetrics.Snapshot()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("llm metrics: marshalling the snapshot: %w", err)
	}
	return &gctrpc.LLMGetMetricsResponse{
		MetricsJson: string(raw),
		CollectedAt: snapshot.CollectedAt,
	}, nil
}

// errLLMRiskSwitchesUnavailable marks the risk-switch RPC before the runtime
// wiring creates the service (fail-closed, like the other LLM RPCs).
var errLLMRiskSwitchesUnavailable = errors.New("llm risk switches are not configured on this bot")

// LLMSetRiskSwitch flips one §5.6 switch and audits the change (M5-17).
func (s *RPCServer) LLMSetRiskSwitch(ctx context.Context, r *gctrpc.LLMSetRiskSwitchRequest) (*gctrpc.GenericResponse, error) {
	if s.Engine == nil || s.Engine.llmRiskSwitches == nil {
		return nil, errLLMRiskSwitchesUnavailable
	}
	if err := s.Engine.llmRiskSwitches.Set(ctx, r.GetName(), r.GetEnabled(), r.GetActor()); err != nil {
		return nil, err
	}
	return &gctrpc.GenericResponse{Status: "ok"}, nil
}

// LLMGetRiskSwitches returns the §5.6 catalogue with live states (W7-05 面板
// 的数据面): defaults applied, red-line locks declared, reason codes attached.
func (s *RPCServer) LLMGetRiskSwitches(_ context.Context, _ *gctrpc.LLMGetRiskSwitchesRequest) (*gctrpc.LLMGetRiskSwitchesResponse, error) {
	if s == nil || s.Engine == nil || s.Engine.llmRiskSwitches == nil {
		return nil, errLLMRiskSwitchesUnavailable
	}
	state := s.Engine.llmRiskSwitches.CurrentState()
	out := &gctrpc.LLMGetRiskSwitchesResponse{}
	for _, spec := range RiskSwitchCatalog {
		out.Switches = append(out.Switches, &gctrpc.LLMRiskSwitch{
			Name:       spec.Name,
			Enabled:    state[spec.Name],
			DefaultOn:  spec.DefaultOn,
			Locked:     spec.LockedFromRPC,
			ReasonCode: spec.ReasonCode,
		})
	}
	return out, nil
}

// errLLMReadinessUnavailable marks the readiness RPC before the runtime facts
// adapter is assembled (fail-closed, like the other LLM RPCs).
var errLLMReadinessUnavailable = errors.New("llm readiness is not configured on this bot")

// LLMGetReadiness returns the M6-01 admission report for a target network and
// mode (docs 11 §8: exit code 0 = READY, 2 = NOT_READY is the CLI's half;
// this handler answers the full gate list either way).
func (s *RPCServer) LLMGetReadiness(ctx context.Context, r *gctrpc.LLMReadinessRequest) (*gctrpc.LLMReadinessResponse, error) {
	if s.Engine == nil || s.Engine.llmReadiness == nil || s.Engine.llmReadiness.Facts == nil {
		return nil, errLLMReadinessUnavailable
	}
	report, err := s.Engine.llmReadiness.Evaluate(ctx, ReadinessRequest{Network: r.GetNetwork(), Mode: r.GetMode()})
	if err != nil {
		return nil, err
	}
	resp := &gctrpc.LLMReadinessResponse{
		Ready:     report.Ready,
		Network:   report.Network,
		Mode:      report.Mode,
		CheckedAt: report.CheckedAt.UTC().Format(time.RFC3339),
	}
	for i := range report.Gates {
		gate := &report.Gates[i]
		resp.Gates = append(resp.Gates, &gctrpc.LLMReadinessGate{
			Code: gate.Code, Status: gate.Status, Detail: gate.Detail,
		})
	}
	return resp, nil
}
