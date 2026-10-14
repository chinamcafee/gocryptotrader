package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// M4-15 测试：fail-closed 的触发路径、输入校验、转换器与操作者重认证。
// 仓储支撑的读取路径由 `database/repository/llm` 的真库 e2e 测试覆盖，
// 这里覆盖不需要数据库的接缝。

func TestLLMStrategyRPCLinesFailClosed(t *testing.T) {
	t.Parallel()

	// No Engine at all: extraction cannot run and says so.
	server := &RPCServer{}
	_, err := server.LLMExtractPolicy(context.Background(), &gctrpc.LLMExtractPolicyRequest{ConversationId: "c1", ProfileRevisionId: "r1"})
	assert.ErrorIs(t, err, errLLMChatUnavailable)

	// The decision runner's production wiring is not part of this bot yet:
	// trigger and preview must refuse rather than report an empty success.
	_, err = server.LLMTriggerAnalysis(context.Background(), &gctrpc.LLMTriggerAnalysisRequest{StrategyVersionId: "v1", MarketId: "m1"})
	assert.ErrorIs(t, err, errLLMDecisionRunner)
	_, err = server.LLMPreviewDecision(context.Background(), &gctrpc.LLMPreviewDecisionRequest{StrategyVersionId: "v1", MarketId: "m1"})
	assert.ErrorIs(t, err, errLLMDecisionRunner)
}

func TestLLMStrategyRPCsValidateInput(t *testing.T) {
	t.Parallel()

	server := &RPCServer{}
	ctx := context.Background()
	cases := []struct {
		name string
		call func() error
	}{
		{"get version", func() error {
			_, err := server.LLMGetStrategyVersion(ctx, &gctrpc.LLMGetStrategyVersionRequest{})
			return err
		}},
		{"freeze", func() error {
			_, err := server.LLMFreezeStrategyVersion(ctx, &gctrpc.LLMFreezeStrategyVersionRequest{Id: "v1"})
			return err
		}},
		{"freeze bad json", func() error {
			_, err := server.LLMFreezeStrategyVersion(ctx, &gctrpc.LLMFreezeStrategyVersionRequest{Id: "v1", Actor: "op", PolicyJson: "not json"})
			return err
		}},
		{"approve", func() error {
			_, err := server.LLMApproveStrategyVersion(ctx, &gctrpc.LLMApproveStrategyVersionRequest{StrategyVersionId: "v1"})
			return err
		}},
		{"revoke", func() error {
			_, err := server.LLMRevokeApproval(ctx, &gctrpc.LLMRevokeApprovalRequest{})
			return err
		}},
		{"pause", func() error {
			_, err := server.LLMPauseStrategy(ctx, &gctrpc.LLMPauseStrategyRequest{})
			return err
		}},
		{"resume", func() error {
			_, err := server.LLMResumeStrategy(ctx, &gctrpc.LLMResumeStrategyRequest{})
			return err
		}},
		{"diff", func() error {
			_, err := server.LLMDiffStrategyVersions(ctx, &gctrpc.LLMDiffStrategyVersionsRequest{FromId: "a"})
			return err
		}},
		{"get run", func() error {
			_, err := server.LLMGetDecisionRun(ctx, &gctrpc.LLMGetDecisionRunRequest{})
			return err
		}},
		{"lineage", func() error {
			_, err := server.LLMGetDecisionLineage(ctx, &gctrpc.LLMGetDecisionLineageRequest{})
			return err
		}},
		{"risk evidence", func() error {
			_, err := server.LLMGetRiskEvidence(ctx, &gctrpc.LLMGetRiskEvidenceRequest{})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.call()
			require.Error(t, err, "a missing required field must refuse, never silently succeed")
		})
	}
}

func TestOperatorPasswordReAuthentication(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.RemoteControl.Password = "s3cret"
	server := &RPCServer{Engine: &Engine{Config: cfg}}
	assert.NoError(t, server.verifyOperatorPassword(context.Background(), "s3cret"))
	assert.Error(t, server.verifyOperatorPassword(context.Background(), "wrong"))

	unset := &RPCServer{}
	assert.Error(t, server.verifyOperatorPassword(context.Background(), ""))
	assert.Error(t, unset.verifyOperatorPassword(context.Background(), "s3cret"), "an unconfigured password fails closed")
}

func TestStrategyAndDecisionConverters(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	archived := now
	strategy := strategyToProto(&llmstore.Strategy{ID: "s1", Name: "BTC trend", CreatedAt: now, CreatedBy: "op", ArchivedAt: &archived})
	assert.Equal(t, "s1", strategy.GetId())
	assert.True(t, strategy.GetArchived())

	parent := "v0"
	frozenAt := now
	version := versionToProto(&llmstore.StrategyVersion{
		ID: "v1", StrategyID: "s1", ParentVersionID: &parent, State: "FROZEN",
		StrategyHash: new("sh"), RiskEnvelopeHash: new("rh"), ProposalState: new("READY_FOR_REVIEW"),
		CreatedAt: now, FrozenAt: &frozenAt,
	})
	assert.Equal(t, "v0", version.GetParentVersionId())
	assert.Equal(t, "sh", version.GetStrategyHash())
	assert.Equal(t, frozenAt.UTC().Format(time.RFC3339), version.GetFrozenAt())

	versionID := "v1"
	market := "Binance:usdtmarginedfutures:BTC_USDT"
	run := decisionRunToProto(&llmstore.DecisionRun{
		ID: "d1", StrategyVersionID: &versionID, SnapshotID: &market, Trigger: "AUTO_SHADOW",
		RunMode: "SHADOW", Decision: "HOLD", Reason: "MARKET_SNAPSHOT_STALE", InputHash: "ih",
		LLMModel: "gpt-test", JevModel: new("jev-1.0.0"), CreatedAt: now, LatencyMS: 12,
	})
	assert.Equal(t, "v1", run.GetStrategyVersionId())
	assert.Equal(t, "jev-1.0.0", run.GetJevModel())

	evidence := riskEvidenceToProto(&llmstore.RiskEvaluation{
		ID: "re1", CheckVersion: "new-risk-v1", Verdict: "REJECT", ChecksJSON: `[{"code":"RISK_HALTED","passed":false}]`,
		RejectionsJSON: new(`[{"code":"RISK_HALTED","actual":"halted","allowed":"no halt"}]`),
		EnvelopeHash:   "env", ComputedQty: new("0.0199"), EvaluatedAt: now,
	})
	assert.Equal(t, "REJECT", evidence.GetVerdict())
	assert.Contains(t, evidence.GetChecksJson(), "RISK_HALTED")
	assert.Equal(t, "0.0199", evidence.GetComputedQty())
	assert.Empty(t, evidence.GetComputedNotionalUsd(), "a nil column renders empty, never a stale value")

	subject := "d1"
	event := auditEventToProto(&llmstore.AuditEvent{
		ID: "a1", Kind: AuditKindDecisionRecorded, Actor: "system", Subject: &subject,
		DetailJSON: `{"decision":"HOLD"}`, CreatedAt: now,
	})
	assert.Equal(t, subject, event.GetSubject())
	assert.Equal(t, AuditKindDecisionRecorded, event.GetKind())
}
