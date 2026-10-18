package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/thrasher-corp/gocryptotrader/llm/jev"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// errLLMProvidersUnavailable marks the provider RPCs before the runtime wiring
// creates the engine handle (the same fail-closed convention as the other LLM
// RPCs).
var errLLMProvidersUnavailable = errors.New("llm providers are not configured on this bot")

// LLMListProviderProfiles lists provider profiles with their newest revision
// (docs 08 §3.4).
func (s *RPCServer) LLMListProviderProfiles(ctx context.Context, r *gctrpc.LLMListProviderProfilesRequest) (*gctrpc.LLMListProviderProfilesResponse, error) {
	if s == nil || s.Engine == nil {
		return nil, errLLMProvidersUnavailable
	}
	profiles, err := llmstore.ListProfiles(ctx, r.GetIncludeArchived())
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMListProviderProfilesResponse{}
	for i := range profiles {
		converted, err := providerProfileToProto(ctx, &profiles[i])
		if err != nil {
			return nil, err
		}
		out.Profiles = append(out.Profiles, converted)
	}
	return out, nil
}

// LLMCreateProviderProfile creates a disabled profile; enabling happens by
// activating a pairing that uses one of its revisions.
func (s *RPCServer) LLMCreateProviderProfile(ctx context.Context, r *gctrpc.LLMCreateProviderProfileRequest) (*gctrpc.LLMCreateProviderProfileResponse, error) {
	if s == nil || s.Engine == nil {
		return nil, errLLMProvidersUnavailable
	}
	profile, err := CreateProviderProfile(ctx, r.GetKind(), r.GetDisplayName(), r.GetActor(), time.Now())
	if err != nil {
		return nil, err
	}
	converted, err := providerProfileToProto(ctx, profile)
	if err != nil {
		return nil, err
	}
	return &gctrpc.LLMCreateProviderProfileResponse{Profile: converted}, nil
}

// LLMUpdateProviderProfile appends a new immutable revision to a profile.
// Zero numerics and empty strings mean "unset"; the engine's per-kind shape
// rules then decide whether the revision is valid.
func (s *RPCServer) LLMUpdateProviderProfile(ctx context.Context, r *gctrpc.LLMUpdateProviderProfileRequest) (*gctrpc.LLMUpdateProviderProfileResponse, error) {
	if s == nil || s.Engine == nil {
		return nil, errLLMProvidersUnavailable
	}
	params := &ProviderRevisionParams{
		ProfileID:               r.GetId(),
		Actor:                   r.GetActor(),
		BaseURL:                 r.GetBaseUrl(),
		ModelID:                 r.GetModelId(),
		APIKey:                  r.GetApiKey(),
		TimeoutSeconds:          r.GetTimeoutSeconds(),
		MaxOutputTokens:         int64PtrOrNil(r.GetMaxOutputTokens()),
		MaxToolRounds:           int64PtrOrNil(r.GetMaxToolRounds()),
		MaxToolCalls:            int64PtrOrNil(r.GetMaxToolCalls()),
		MaxCumulativeTokens:     int64PtrOrNil(r.GetMaxCumulativeTokens()),
		PromptVersion:           stringPtrOrNil(r.GetPromptVersion()),
		QuestionTemplateVersion: stringPtrOrNil(r.GetQuestionTemplateVersion()),
		DailyBudgetUSD:          stringPtrOrNil(r.GetDailyBudgetUsd()),
		ReservationCap24H:       r.GetReservationCap_24H(),
		AllowInsecureLocalhost:  r.GetAllowInsecureLocalhost(),
		ProxyAddress:            stringPtrOrNil(r.GetProxyAddress()),
	}
	revision, err := AddProviderRevision(ctx, params, time.Now())
	if err != nil {
		return nil, err
	}
	return &gctrpc.LLMUpdateProviderProfileResponse{Revision: providerRevisionToProto(revision)}, nil
}

// LLMDeleteProviderProfile archives a profile. Deleting is always archival:
// revisions are immutable history and rows are never removed (09 §2.1).
func (s *RPCServer) LLMDeleteProviderProfile(ctx context.Context, r *gctrpc.LLMDeleteProviderProfileRequest) (*gctrpc.GenericResponse, error) {
	if s == nil || s.Engine == nil {
		return nil, errLLMProvidersUnavailable
	}
	if strings.TrimSpace(r.GetActor()) == "" {
		return nil, ErrInvalidActor
	}
	if _, err := llmstore.GetProfile(ctx, r.GetId()); err != nil {
		return nil, err
	}
	if err := llmstore.ArchiveProfile(ctx, r.GetId(), time.Now()); err != nil {
		return nil, err
	}
	return &gctrpc.GenericResponse{Status: "ok"}, nil
}

// LLMTestProviderProfile runs the layered capability probes against a
// revision and records every verdict. Probe failures are the answer, not an
// RPC error: the caller asked "what can this endpoint do", and a 200 with four
// verdicts is the truthful reply. Only infrastructure faults (unknown profile,
// vault unavailable) fail the call.
func (s *RPCServer) LLMTestProviderProfile(ctx context.Context, r *gctrpc.LLMTestProviderProfileRequest) (*gctrpc.LLMTestProviderProfileResponse, error) {
	if s == nil || s.Engine == nil || s.Config == nil {
		return nil, errLLMProvidersUnavailable
	}
	profile, err := llmstore.GetProfile(ctx, r.GetId())
	if err != nil {
		return nil, err
	}
	revision, err := resolveTestRevision(ctx, profile.ID, r.GetRevisionId())
	if err != nil {
		return nil, err
	}
	secretFn, err := revisionSecretFn(revision.APIKeyRef)
	if err != nil {
		return nil, err
	}

	out := &gctrpc.LLMTestProviderProfileResponse{RevisionId: revision.ID, Kind: revision.Kind}
	now := time.Now()
	switch revision.Kind {
	case ProviderKindLLM:
		client, err := openai.NewClient(llmProfileOf(revision), secretFn)
		if err != nil {
			return nil, err
		}
		for _, verdict := range client.ProbeAll(ctx) {
			if err := recordProbeVerdict(ctx, revision, verdict.Layer, verdict.Passed, verdict.ErrorCode, verdict.ResponseModel, verdict.LatencyMS, verdict.InputTokens, verdict.OutputTokens, now); err != nil {
				return nil, err
			}
			out.Results = append(out.Results, probeVerdictToProto(verdict.Layer, verdict.Passed, verdict.ErrorCode, verdict.ErrorMessage, verdict.ResponseModel, verdict.LatencyMS, verdict.InputTokens, verdict.OutputTokens))
		}
	case ProviderKindJev:
		client, err := jev.NewClient(jevProfileOf(revision), secretFn)
		if err != nil {
			return nil, err
		}
		verdict := client.Probe(ctx)
		if err := recordProbeVerdict(ctx, revision, verdict.Layer, verdict.Passed, verdict.ErrorCode, verdict.ResponseModel, verdict.LatencyMS, verdict.InputTokens, verdict.OutputTokens, now); err != nil {
			return nil, err
		}
		out.Results = append(out.Results, probeVerdictToProto(verdict.Layer, verdict.Passed, verdict.ErrorCode, verdict.ErrorMessage, verdict.ResponseModel, verdict.LatencyMS, verdict.InputTokens, verdict.OutputTokens))
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownProviderKind, revision.Kind)
	}
	return out, nil
}

// LLMGetActiveModelPair returns the pairing row; "never activated yet" is a
// state (found=false), not an error.
func (s *RPCServer) LLMGetActiveModelPair(ctx context.Context, _ *gctrpc.LLMGetActiveModelPairRequest) (*gctrpc.LLMGetActiveModelPairResponse, error) {
	if s == nil || s.Engine == nil {
		return nil, errLLMProvidersUnavailable
	}
	pair, err := llmstore.GetActiveModelPair(ctx)
	if err != nil {
		if errors.Is(err, llmstore.ErrNotFound) {
			return &gctrpc.LLMGetActiveModelPairResponse{Found: false}, nil
		}
		return nil, err
	}
	out := &gctrpc.LLMGetActiveModelPairResponse{
		Found:     true,
		PairEpoch: pair.PairEpoch,
		Readiness: pair.Readiness,
		UpdatedAt: pair.UpdatedAt.UTC().Format(time.RFC3339),
		UpdatedBy: pair.UpdatedBy,
	}
	if pair.LLMProfileRevisionID != nil {
		out.LlmProfileRevisionId = *pair.LLMProfileRevisionID
	}
	if pair.JevProfileRevisionID != nil {
		out.JevProfileRevisionId = *pair.JevProfileRevisionID
	}
	return out, nil
}

// LLMSwitchActiveModelPair performs the CAS activation (docs 05 §2.4): the
// caller must present the epoch it read plus operator re-authentication. An
// empty revision id clears that slot -- clearing produces readiness INCOMPLETE
// by construction, it is never a valid pairing for a READY claim.
func (s *RPCServer) LLMSwitchActiveModelPair(ctx context.Context, r *gctrpc.LLMSwitchActiveModelPairRequest) (*gctrpc.LLMSwitchActiveModelPairResponse, error) {
	if s == nil || s.Engine == nil {
		return nil, errLLMProvidersUnavailable
	}
	if err := s.verifyOperatorPassword(ctx, r.GetOperatorPassword()); err != nil {
		return nil, err
	}
	req := ActivatePairRequest{
		ExpectedEpoch:  r.GetExpectedPairEpoch(),
		IdempotencyKey: r.GetIdempotencyKey(),
		Actor:          r.GetActor(),
	}
	if id := strings.TrimSpace(r.GetLlmProfileRevisionId()); id != "" {
		req.LLMRevisionID = &id
	}
	if id := strings.TrimSpace(r.GetJevProfileRevisionId()); id != "" {
		req.JevRevisionID = &id
	}
	pair, err := ActivateModelPair(ctx, req, time.Now())
	if err != nil {
		return nil, err
	}
	// The activation re-points the global model pair: rebuild the chat and
	// decision corridors onto the new clients right away. Startup assembly is
	// one-shot, so without this they would stay unavailable (first activation)
	// or keep the retired pair's clients (subsequent switches).
	s.Engine.reassembleLLMRuntime(ctx, "pair activation by "+req.Actor)
	return &gctrpc.LLMSwitchActiveModelPairResponse{PairEpoch: pair.PairEpoch, Readiness: pair.Readiness}, nil
}

// resolveTestRevision picks the revision to probe: the requested one when
// given, otherwise the profile's live (non-superseded) revision.
func resolveTestRevision(ctx context.Context, profileID, revisionID string) (*llmstore.ProfileRevision, error) {
	if revisionID = strings.TrimSpace(revisionID); revisionID != "" {
		revision, err := llmstore.GetProfileRevision(ctx, revisionID)
		if err != nil {
			return nil, err
		}
		if revision.ProfileID != profileID {
			return nil, fmt.Errorf("llm providers: revision %s does not belong to profile %s", revisionID, profileID)
		}
		return revision, nil
	}
	revisions, err := llmstore.ListProfileRevisions(ctx, profileID)
	if err != nil {
		return nil, err
	}
	for i := range revisions {
		if revisions[i].SupersededAt == nil {
			return &revisions[i], nil
		}
	}
	return nil, fmt.Errorf("llm providers: profile %s has no live revision to test", profileID)
}

// recordProbeVerdict persists one layer's verdict. Every probe outcome is a
// row: an append-only history is what makes readiness claims auditable.
func recordProbeVerdict(ctx context.Context, revision *llmstore.ProfileRevision, layer string, passed bool, errorCode, responseModel string, latencyMS int64, inputTokens, outputTokens int, now time.Time) error {
	row := &llmstore.ProbeResult{
		ID:                newLLMIdentifier(),
		ProfileRevisionID: revision.ID,
		Kind:              revision.Kind,
		Layer:             layer,
		Passed:            passed,
		ProbedAt:          now,
	}
	if errorCode != "" {
		row.ErrorCode = &errorCode
	}
	if latencyMS > 0 {
		row.LatencyMS = &latencyMS
	}
	if inputTokens > 0 {
		tokens := int64(inputTokens)
		row.InputTokens = &tokens
	}
	if outputTokens > 0 {
		tokens := int64(outputTokens)
		row.OutputTokens = &tokens
	}
	if responseModel != "" {
		row.ResponseModel = &responseModel
	}
	return llmstore.InsertProbeResult(ctx, row)
}

// providerProfileToProto converts a stored profile plus its newest revision.
func providerProfileToProto(ctx context.Context, profile *llmstore.Profile) (*gctrpc.LLMProviderProfile, error) {
	revisions, err := llmstore.ListProfileRevisions(ctx, profile.ID)
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMProviderProfile{
		Id:            profile.ID,
		Kind:          profile.Kind,
		DisplayName:   profile.DisplayName,
		Enabled:       profile.Enabled,
		CreatedAt:     profile.CreatedAt.UTC().Format(time.RFC3339),
		CreatedBy:     profile.CreatedBy,
		RevisionCount: int64(len(revisions)),
	}
	if profile.ArchivedAt != nil {
		out.ArchivedAt = profile.ArchivedAt.UTC().Format(time.RFC3339)
	}
	for i := range revisions {
		if revisions[i].SupersededAt == nil {
			out.Revision = providerRevisionToProto(&revisions[i])
			break
		}
	}
	return out, nil
}

// providerRevisionToProto converts one revision row. The plaintext key never
// travels: only the derived fingerprint is exposed.
func providerRevisionToProto(r *llmstore.ProfileRevision) *gctrpc.LLMProviderRevision {
	out := &gctrpc.LLMProviderRevision{
		Id:                     r.ID,
		ProfileId:              r.ProfileID,
		RevisionNo:             r.RevisionNo,
		Kind:                   r.Kind,
		BaseUrl:                r.BaseURL,
		ModelId:                r.ModelID,
		ApiKeyFingerprint:      r.APIKeyFingerprint,
		TimeoutSeconds:         r.TimeoutSeconds,
		ReservationCap_24H:     r.ReservationCap24H,
		AllowInsecureLocalhost: r.AllowInsecureLocalhost,
		CreatedAt:              r.CreatedAt.UTC().Format(time.RFC3339),
		CreatedBy:              r.CreatedBy,
	}
	if r.MaxOutputTokens != nil {
		out.MaxOutputTokens = *r.MaxOutputTokens
	}
	if r.MaxToolRounds != nil {
		out.MaxToolRounds = *r.MaxToolRounds
	}
	if r.MaxToolCalls != nil {
		out.MaxToolCalls = *r.MaxToolCalls
	}
	if r.MaxCumulativeTokens != nil {
		out.MaxCumulativeTokens = *r.MaxCumulativeTokens
	}
	if r.PromptVersion != nil {
		out.PromptVersion = *r.PromptVersion
	}
	if r.QuestionTemplateVersion != nil {
		out.QuestionTemplateVersion = *r.QuestionTemplateVersion
	}
	if r.DailyBudgetUSD != nil {
		out.DailyBudgetUsd = *r.DailyBudgetUSD
	}
	if r.ProxyAddress != nil {
		out.ProxyAddress = *r.ProxyAddress
	}
	if r.SupersededAt != nil {
		out.SupersededAt = r.SupersededAt.UTC().Format(time.RFC3339)
	}
	return out
}

// probeVerdictToProto converts one probe verdict to its wire shape.
func probeVerdictToProto(layer string, passed bool, errorCode, errorMessage, responseModel string, latencyMS int64, inputTokens, outputTokens int) *gctrpc.LLMProbeLayerResult {
	return &gctrpc.LLMProbeLayerResult{
		Layer:         layer,
		Passed:        passed,
		ErrorCode:     errorCode,
		ErrorMessage:  errorMessage,
		LatencyMs:     latencyMS,
		InputTokens:   int64(inputTokens),
		OutputTokens:  int64(outputTokens),
		ResponseModel: responseModel,
	}
}

func int64PtrOrNil(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func stringPtrOrNil(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}
