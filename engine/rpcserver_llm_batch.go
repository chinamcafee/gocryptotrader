package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// 批量决策工作台的两个 RPC（docs/llm-dev/13 §3.2）：起批返回 job id，
// 轮询取进度与逐行结果。作业只产决策记录——与 trigger/preview 同级，
// 绝不下单。

// LLMStartBatchDecision starts one serial batch job over the selected markets.
func (s *RPCServer) LLMStartBatchDecision(ctx context.Context, r *gctrpc.LLMStartBatchDecisionRequest) (*gctrpc.LLMStartBatchDecisionResponse, error) {
	if s == nil || s.Engine == nil {
		return nil, errLLMDecisionRunner
	}
	batch := s.Engine.currentBatchRunner()
	if batch == nil {
		return nil, errLLMDecisionRunner
	}
	job, err := batch.Start(ctx, r.GetKind(), r.GetStrategyVersionId(), r.GetMarketIds(), r.GetActor())
	if err != nil {
		return nil, err
	}
	return &gctrpc.LLMStartBatchDecisionResponse{JobId: job.ID}, nil
}

// LLMGetBatchDecision returns one job's progress and per-row outcomes.
func (s *RPCServer) LLMGetBatchDecision(_ context.Context, r *gctrpc.LLMGetBatchDecisionRequest) (*gctrpc.LLMGetBatchDecisionResponse, error) {
	if s == nil || s.Engine == nil {
		return nil, errLLMDecisionRunner
	}
	batch := s.Engine.currentBatchRunner()
	if batch == nil {
		return nil, errLLMDecisionRunner
	}
	if r.GetJobId() == "" {
		return nil, fmt.Errorf("%w: jobId", errLLMStrategyInput)
	}
	view, ok := batch.Get(r.GetJobId())
	if !ok {
		return nil, fmt.Errorf("%w: no such batch job %q", errLLMStrategyInput, r.GetJobId())
	}
	out := &gctrpc.LLMGetBatchDecisionResponse{
		JobId:             view.ID,
		Kind:              view.Kind,
		StrategyVersionId: view.StrategyVersionID,
		Actor:             view.Actor,
		CreatedAt:         view.CreatedAt.UTC().Format(time.RFC3339),
		Done:              view.Done,
		Total:             int32(len(view.Rows)), //nolint:gosec // bounded by the batch market cap (<=50)
	}
	for i := range view.Rows {
		row := &view.Rows[i]
		if row.Status != BatchRowPending && row.Status != BatchRowRunning {
			out.Finished++
		}
		out.Rows = append(out.Rows, &gctrpc.LLMBatchDecisionRow{
			MarketId:      row.MarketID,
			Status:        row.Status,
			Decision:      row.Decision,
			Reason:        row.Reason,
			DecisionRunId: row.DecisionRunID,
			Error:         row.Error,
		})
	}
	return out, nil
}

// LLMListActiveApprovals lists the live approvals the GUI's batch workbench
// binds to (A5: the default version is the active one). Filters apply when
// provided; the batch Start enforces the same rule server-side regardless of
// what this listing ever returns.
func (s *RPCServer) LLMListActiveApprovals(ctx context.Context, r *gctrpc.LLMListActiveApprovalsRequest) (*gctrpc.LLMListActiveApprovalsResponse, error) {
	approvals, err := llmstore.ListActiveStrategyApprovals(ctx)
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMListActiveApprovalsResponse{}
	account := strings.TrimSpace(r.GetAccountId())
	network := strings.TrimSpace(r.GetNetwork())
	for i := range approvals {
		approval := &approvals[i]
		if account != "" && approval.AccountID != account {
			continue
		}
		if network != "" && approval.Network != network {
			continue
		}
		rendered, err := approvalToProto(approval)
		if err != nil {
			return nil, err
		}
		out.Approvals = append(out.Approvals, rendered)
	}
	return out, nil
}
