package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// M5-14 测试：标签键规范化、三种类型的数学、语义帮助函数、日志 seam、RPC fail-closed。

func TestMetricStoreMath(t *testing.T) {
	t.Parallel()

	metrics := NewLLMMetrics()
	metrics.Now = func() time.Time { return time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC) }
	metrics.Add(MetricDecisionTotal, 1, map[string]string{"decision": "HOLD"})
	metrics.Add(MetricDecisionTotal, 2, map[string]string{"decision": "HOLD"})
	metrics.Set(MetricUnknownOrders, 3, nil)
	metrics.Observe(MetricRequestDurationMS, 100, nil)
	metrics.Observe(MetricRequestDurationMS, 300, nil)

	snapshot := metrics.Snapshot()
	assert.Equal(t, 3.0, snapshot.Counters[MetricDecisionTotal+"|decision=HOLD"])
	assert.Equal(t, 3.0, snapshot.Gauges[MetricUnknownOrders])
	h := snapshot.Histograms[MetricRequestDurationMS]
	require.NotNil(t, h)
	assert.Equal(t, 400.0, h.Sum)
	assert.Equal(t, int64(2), h.Count)
	assert.Equal(t, 300.0, h.Max)
	assert.Equal(t, "2026-10-11T20:00:00Z", snapshot.CollectedAt)

	// Label order must not create a second series.
	metrics.Add(MetricDecisionTotal, 1, map[string]string{"decision": "HOLD"})
	assert.Equal(t, 4.0, metrics.Snapshot().Counters[MetricDecisionTotal+"|decision=HOLD"])
}

func TestSemanticRecorders(t *testing.T) {
	t.Parallel()

	metrics := NewLLMMetrics()
	metrics.RecordDecision("REVIEW", ReasonAwaitingHardRiskAndApproval)
	metrics.RecordRiskRejection(RiskHalted)
	metrics.RecordToolCall("getCandleWindow")
	metrics.RecordToolResultRejected("sensitive_key")
	metrics.RecordTokens(120, 45)
	metrics.RecordRequestError("TIMEOUT")
	metrics.RecordJEVValidationFailure("probability_sum")
	metrics.RecordJEVModelDrift()
	metrics.RecordReconcileMismatch()
	metrics.RecordDBWriteError()
	metrics.RecordProtectiveExitFailure()

	snapshot := metrics.Snapshot()
	assert.Equal(t, 1.0, snapshot.Counters[MetricDecisionTotal+"|decision=REVIEW"])
	assert.Equal(t, 1.0, snapshot.Counters[MetricDecisionReasonTotal+"|reason="+ReasonAwaitingHardRiskAndApproval])
	assert.Equal(t, 1.0, snapshot.Counters[MetricRiskRejectionTotal+"|code="+RiskHalted])
	assert.Equal(t, 1.0, snapshot.Counters[MetricToolCallsTotal+"|tool=getCandleWindow"])
	assert.Equal(t, 120.0, snapshot.Counters[MetricTokensTotal+"|direction=input"])
	assert.Equal(t, 45.0, snapshot.Counters[MetricTokensTotal+"|direction=output"])
	for _, immediate := range []string{MetricJevModelDriftTotal, MetricReconcileMismatchTotal, MetricDBWriteErrorsTotal, MetricProtectiveExitFailuresTotal} {
		assert.Equalf(t, 1.0, snapshot.Counters[immediate], "%s is an immediate-alert counter", immediate)
	}
}

func TestSnapshotJSONAndLogSeam(t *testing.T) {
	t.Parallel()

	metrics := NewLLMMetrics()
	metrics.Set(MetricHaltLevel, 1, nil)
	var logged []string
	metrics.Log = func(line string) { logged = append(logged, line) }
	metrics.LogSnapshot()
	require.Len(t, logged, 1)
	var decoded MetricSnapshot
	require.NoError(t, json.Unmarshal([]byte(logged[0]), &decoded))
	assert.Equal(t, 1.0, decoded.Gauges[MetricHaltLevel])

	// Nil-safety across the board.
	var nilMetrics *LLMMetrics
	nilMetrics.Add(MetricHaltLevel, 1, nil)
	nilMetrics.Set(MetricHaltLevel, 1, nil)
	nilMetrics.Observe(MetricHaltLevel, 1, nil)
	nilMetrics.LogSnapshot()
	assert.NotNil(t, nilMetrics.Snapshot())
}

func TestLLMGetMetricsRPCFailsClosedAndServes(t *testing.T) {
	t.Parallel()

	server := &RPCServer{}
	_, err := server.LLMGetMetrics(context.Background(), &gctrpc.LLMGetMetricsRequest{})
	assert.ErrorIs(t, err, errLLMMetricsUnavailable, "a missing registry should not look like an empty one")

	metrics := NewLLMMetrics()
	metrics.Add(MetricUnknownOrders, 2, nil)
	server = &RPCServer{Engine: &Engine{llmMetrics: metrics}}
	response, err := server.LLMGetMetrics(context.Background(), &gctrpc.LLMGetMetricsRequest{})
	require.NoError(t, err)
	assert.Contains(t, response.GetMetricsJson(), MetricUnknownOrders)
	assert.NotEmpty(t, response.GetCollectedAt())
}
