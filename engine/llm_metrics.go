package engine

import (
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// M5-14 可观测性（docs 10 §6，定案 A + C）。
//
// A：指标以**结构化日志**输出（JSON 快照经 log seam 落盘，外部 loki/promtail
// 采集，看长期趋势）；C：`LLMGetMetrics` RPC 即时拉取（单机诊断）。
//
// 指标注册表是并发安全的现值存储：counter 累加、gauge 覆写、histogram 记
// sum/count/max（p99 的完整分位留给外部采集，这里给出诊断所需的三元）。
// §6.1 的表逐行落名——每个名字一个常量，拼错=编译错。
//
// 告警阈值不在这里判定：这里只产出数值，判定归采集与告警层（记录于文档）。

// Metric names (10 §6.1, verbatim).
const (
	MetricDataAgeSeconds              = "llm_data_age_seconds"
	MetricSnapshotBuildDurationMS     = "llm_snapshot_build_duration_ms"
	MetricRequestDurationMS           = "llm_request_duration_ms"
	MetricRequestErrorsTotal          = "llm_request_errors_total"
	MetricToolCallsTotal              = "llm_tool_calls_total"
	MetricToolResultRejectedTotal     = "llm_tool_result_rejected_total"
	MetricTokensTotal                 = "llm_tokens_total" //nolint:gosec // G101 false positive: a metric name, not a credential
	MetricJevRequestDurationMS        = "jev_request_duration_ms"
	MetricJevValidationFailuresTotal  = "jev_validation_failures_total"
	MetricJevModelDriftTotal          = "jev_model_drift_total"
	MetricActivePairReadiness         = "active_pair_readiness"
	MetricPairEpoch                   = "pair_epoch"
	MetricProviderProbeAgeSeconds     = "provider_probe_age_seconds"
	MetricDecisionTotal               = "decision_total"
	MetricDecisionReasonTotal         = "decision_reason_total"
	MetricRiskRejectionTotal          = "risk_rejection_total"
	MetricNewRiskIntentsInWindow      = "new_risk_intents_in_window"
	MetricBudgetReservationsRemaining = "budget_reservations_remaining"
	MetricUnknownOrders               = "unknown_orders"
	MetricAccountReconciled           = "account_reconciled"
	MetricReconcileMismatchTotal      = "reconcile_mismatch_total"
	MetricReconcileAgeSeconds         = "reconcile_age_seconds"
	MetricHaltLevel                   = "halt_level"
	MetricClockDriftMS                = "clock_drift_ms"
	MetricRateLimitUsagePct           = "binance_rate_limit_usage_pct"
	MetricDBWriteErrorsTotal          = "db_write_errors_total"
	MetricProtectiveExitFailuresTotal = "protective_exit_failures_total"
	MetricStrategyStates              = "strategy_states"
	MetricApprovalsExpiringSoon       = "approvals_expiring_soon"
)

// LLMMetrics is the concurrency-safe metric store.
type LLMMetrics struct {
	mu         sync.Mutex
	counters   map[string]float64
	gauges     map[string]float64
	histograms map[string]*metricHistogram
	// Log receives the structured snapshot (A); nil disables the log channel.
	Log func(line string)
	Now func() time.Time
}

type metricHistogram struct {
	Sum   float64 `json:"sum"`
	Count int64   `json:"count"`
	Max   float64 `json:"max"`
}

// NewLLMMetrics builds the registry.
func NewLLMMetrics() *LLMMetrics {
	return &LLMMetrics{
		counters:   map[string]float64{},
		gauges:     map[string]float64{},
		histograms: map[string]*metricHistogram{},
	}
}

// metricKey folds labels deterministically (sorted, name first).
func metricKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	for _, k := range keys {
		b.WriteString("|")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(labels[k])
	}
	return b.String()
}

// Add increments a counter (labels optional).
func (m *LLMMetrics) Add(name string, delta float64, labels map[string]string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[metricKey(name, labels)] += delta
}

// Set writes a gauge.
func (m *LLMMetrics) Set(name string, value float64, labels map[string]string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauges[metricKey(name, labels)] = value
}

// Observe records one histogram sample.
func (m *LLMMetrics) Observe(name string, value float64, labels map[string]string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := metricKey(name, labels)
	h := m.histograms[key]
	if h == nil {
		h = &metricHistogram{}
		m.histograms[key] = h
	}
	h.Sum += value
	h.Count++
	if value > h.Max {
		h.Max = value
	}
}

// MetricSnapshot is the JSON shape the RPC and the log line share.
type MetricSnapshot struct {
	CollectedAt string                      `json:"collectedAt"`
	Counters    map[string]float64          `json:"counters"`
	Gauges      map[string]float64          `json:"gauges"`
	Histograms  map[string]*metricHistogram `json:"histograms"`
}

// Snapshot returns every current value (copied).
func (m *LLMMetrics) Snapshot() *MetricSnapshot {
	if m == nil {
		return &MetricSnapshot{CollectedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := &MetricSnapshot{
		CollectedAt: m.now().UTC().Format(time.RFC3339),
		Counters:    make(map[string]float64, len(m.counters)),
		Gauges:      make(map[string]float64, len(m.gauges)),
		Histograms:  make(map[string]*metricHistogram, len(m.histograms)),
	}
	maps.Copy(out.Counters, m.counters)
	maps.Copy(out.Gauges, m.gauges)
	for k, v := range m.histograms {
		copied := *v
		out.Histograms[k] = &copied
	}
	return out
}

// SnapshotJSON renders the snapshot for the RPC and the log line.
func (m *LLMMetrics) SnapshotJSON() (string, error) {
	raw, err := json.Marshal(m.Snapshot())
	if err != nil {
		return "", fmt.Errorf("llm metrics: marshalling the snapshot: %w", err)
	}
	return string(raw), nil
}

// LogSnapshot emits the structured snapshot through the log seam (mode A).
func (m *LLMMetrics) LogSnapshot() {
	if m == nil || m.Log == nil {
		return
	}
	line, err := m.SnapshotJSON()
	if err != nil {
		return
	}
	m.Log(line)
}

func (m *LLMMetrics) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}

// Semantic helpers: each call site records through one of these so the label
// vocabulary stays closed.

// RecordDecision counts a decision verdict and its reason.
func (m *LLMMetrics) RecordDecision(verdict, reason string) {
	m.Add(MetricDecisionTotal, 1, map[string]string{"decision": verdict})
	m.Add(MetricDecisionReasonTotal, 1, map[string]string{jsonKeyReason: reason})
}

// RecordRiskRejection counts one refusal by check code.
func (m *LLMMetrics) RecordRiskRejection(code string) {
	m.Add(MetricRiskRejectionTotal, 1, map[string]string{"code": code})
}

// RecordToolCall counts one tool invocation.
func (m *LLMMetrics) RecordToolCall(tool string) {
	m.Add(MetricToolCallsTotal, 1, map[string]string{"tool": tool})
}

// RecordToolResultRejected counts one rejected tool result.
func (m *LLMMetrics) RecordToolResultRejected(reason string) {
	m.Add(MetricToolResultRejectedTotal, 1, map[string]string{jsonKeyReason: reason})
}

// RecordTokens counts input and output tokens separately.
func (m *LLMMetrics) RecordTokens(input, output int64) {
	m.Add(MetricTokensTotal, float64(input), map[string]string{"direction": "input"})
	m.Add(MetricTokensTotal, float64(output), map[string]string{"direction": "output"})
}

// RecordRequestError counts one provider error by code.
func (m *LLMMetrics) RecordRequestError(code string) {
	m.Add(MetricRequestErrorsTotal, 1, map[string]string{"code": code})
}

// RecordJEVValidationFailure counts a judge answer rejected by validation.
func (m *LLMMetrics) RecordJEVValidationFailure(reason string) {
	m.Add(MetricJevValidationFailuresTotal, 1, map[string]string{jsonKeyReason: reason})
}

// RecordJEVModelDrift counts a served-model mismatch (immediate-alert class).
func (m *LLMMetrics) RecordJEVModelDrift() {
	m.Add(MetricJevModelDriftTotal, 1, nil)
}

// RecordReconcileMismatch counts a reconciliation divergence.
func (m *LLMMetrics) RecordReconcileMismatch() {
	m.Add(MetricReconcileMismatchTotal, 1, nil)
}

// RecordDBWriteError counts an audit write failure.
func (m *LLMMetrics) RecordDBWriteError() {
	m.Add(MetricDBWriteErrorsTotal, 1, nil)
}

// RecordProtectiveExitFailure counts a stop that could not be placed.
func (m *LLMMetrics) RecordProtectiveExitFailure() {
	m.Add(MetricProtectiveExitFailuresTotal, 1, nil)
}
