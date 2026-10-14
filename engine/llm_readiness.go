package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// M6-01 准入检查（docs 11 §8）。
//
// `gctcli llm readiness --network binance-testnet` 的引擎侧评估器：把「能不能
// 在目标网络上跑目标模式」拆成一组**可机械核对**的门（gate），每门报告
// 实际值 vs 期望值，全部 PASS 才算 READY（退出码 0），否则 NOT_READY
// （退出码 2）并列出每一项未通过的原因。
//
// 与 pre-gate（M4-07）同构：判定是纯函数、事实由调用方供给、零值
// fail-closed——**没有「没数据就算过」的路径**。区别在于 pre-gate 拦的是
// 单次决策花钱，本组门拦的是整机上测试网这件事本身。
//
// 评估的十条门与 A 项验收的对应（docs 01 §10）：
//   - MODE_PERMITTED / NETWORK_CONSISTENT → A11 的前置（TESTNET 不能误连主网）
//   - PAIR_READY / PROBE_FRESH            → A06–A09 的模型可用性前置
//   - NO_FULL_HALT                        → A14 停机状态不得带病上岗
//   - RISK_SWITCHES_INTACT                → 底线 4/5（三个锁定开关）
//   - ACCOUNT_RECONCILED                  → A13 链路起点
//   - DAY_START_SEALED / COVERAGE_PRESENT → A16/A17 的数据面前置
//   - APPROVAL_LIVE                       → A04 的闭环（无活审批不许上 TESTNET/LIVE）

// Gate verdicts.
const (
	ReadinessPass = "PASS"
	ReadinessFail = "FAIL"
)

// Gate codes (stable identifiers for scripts and dashboards).
const (
	ReadinessGateInput          = "INPUT_COMPLETE"
	ReadinessGateModePermitted  = "MODE_PERMITTED"
	ReadinessGatePairReady      = "PAIR_READY"
	ReadinessGateProbeFresh     = "PROBE_FRESH"
	ReadinessGateNetwork        = "NETWORK_CONSISTENT"
	ReadinessGateNoFullHalt     = "NO_FULL_HALT"
	ReadinessGateSwitchesIntact = "RISK_SWITCHES_INTACT"
	ReadinessGateReconciled     = "ACCOUNT_RECONCILED"
	ReadinessGateDaySealed      = "DAY_START_SEALED"
	ReadinessGateCoverage       = "COVERAGE_PRESENT"
	ReadinessGateApprovalLive   = "APPROVAL_LIVE"
)

// ErrReadinessUnavailable marks the evaluator before the runtime facts
// adapter is assembled (the same fail-closed convention as the chat,
// decision-runner, metrics and risk-switch seams).
var ErrReadinessUnavailable = errors.New("llm readiness: the fact source is not configured")

// ReadinessHalt is one active halt fact.
type ReadinessHalt struct {
	ID       string
	Level    string
	MarketID string
}

// ReadinessInput is the fact set the gates evaluate. Zero values fail closed.
type ReadinessInput struct {
	// Network and Mode are the admission target (e.g. binance-testnet, TESTNET).
	Network string
	Mode    string
	// Now is the evaluation instant.
	Now time.Time

	// PairReadiness is the active pairing's stored readiness.
	PairReadiness string
	// LLMProbePassed/At and JevProbePassed/At describe the newest probe per
	// side; ProbeMaxAge is the freshness TTL from config. Zero or negative TTL
	// means "no TTL configured", which fails closed as stale (M4-07 rule).
	LLMProbePassed bool
	LLMProbeAt     time.Time
	JevProbePassed bool
	JevProbeAt     time.Time
	ProbeMaxAge    time.Duration

	// Halts lists every active halt.
	Halts []ReadinessHalt
	// Switches is the live §5.6 switch state with defaults applied; nil means
	// the state was not supplied and fails closed.
	Switches map[string]bool

	// Reconciled/ReconciledUntil describe the last CLEAN reconciliation.
	Reconciled      bool
	ReconciledUntil time.Time

	// DayKeyUTC is the sealed baseline's day; DaySealed says whether it exists.
	DayKeyUTC string
	DaySealed bool

	// CoverageEnabled is the number of markets in the polling scope.
	CoverageEnabled int

	// LiveApprovals is the count of live approvals for the target network.
	LiveApprovals int

	// ConfiguredNetwork is the bot's configured trading network.
	ConfiguredNetwork string
}

// ReadinessGate is one admission row: the actual value vs the expectation.
type ReadinessGate struct {
	Code   string `json:"code"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// ReadinessReport is the admission verdict.
type ReadinessReport struct {
	Ready     bool            `json:"ready"`
	Network   string          `json:"network"`
	Mode      string          `json:"mode"`
	Gates     []ReadinessGate `json:"gates"`
	CheckedAt time.Time       `json:"checkedAt"`
}

// ReadinessRequest is one operator admission query.
type ReadinessRequest struct {
	Network string
	Mode    string
}

// iReadinessFacts gathers the live facts behind the gates.
type iReadinessFacts interface {
	GatherReadinessFacts(ctx context.Context, network, mode string) (ReadinessInput, error)
}

// LLMReadinessService evaluates the gates over gathered facts.
type LLMReadinessService struct {
	Facts iReadinessFacts
	Now   func() time.Time
}

// Evaluate gathers the facts and runs the gate set.
func (s *LLMReadinessService) Evaluate(ctx context.Context, req ReadinessRequest) (ReadinessReport, error) {
	if s == nil || s.Facts == nil {
		return ReadinessReport{}, ErrReadinessUnavailable
	}
	in, err := s.Facts.GatherReadinessFacts(ctx, req.Network, req.Mode)
	if err != nil {
		return ReadinessReport{}, fmt.Errorf("llm readiness: gathering facts: %w", err)
	}
	if in.Network == "" {
		in.Network = req.Network
	}
	if in.Mode == "" {
		in.Mode = req.Mode
	}
	if in.Now.IsZero() {
		in.Now = s.now()
	}
	return EvaluateReadiness(&in), nil
}

func (s *LLMReadinessService) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// EvaluateReadiness runs the admission gates in a fixed order so the report a
// reader sees is deterministic.
func EvaluateReadiness(in *ReadinessInput) ReadinessReport {
	report := ReadinessReport{
		Network:   in.Network,
		Mode:      in.Mode,
		CheckedAt: in.Now,
	}
	if strings.TrimSpace(in.Network) == "" || strings.TrimSpace(in.Mode) == "" || in.Now.IsZero() {
		report.Gates = []ReadinessGate{{
			Code: ReadinessGateInput, Status: ReadinessFail,
			Detail: fmt.Sprintf("network=%q mode=%q checkedAt=%s; all are required", in.Network, in.Mode, in.Now.UTC().Format(time.RFC3339)),
		}}
		return report
	}

	report.Gates = []ReadinessGate{
		modePermittedGate(in),
		pairReadyGate(in),
		probeFreshGate(in),
		networkGate(in),
		noFullHaltGate(in),
		switchesIntactGate(in),
		reconciledGate(in),
		daySealedGate(in),
		coverageGate(in),
		approvalLiveGate(in),
	}
	report.Ready = true
	for _, gate := range report.Gates {
		if gate.Status != ReadinessPass {
			report.Ready = false
			break
		}
	}
	return report
}

func gateRow(code string, pass bool, passDetail, failDetail string) ReadinessGate {
	if pass {
		return ReadinessGate{Code: code, Status: ReadinessPass, Detail: passDetail}
	}
	return ReadinessGate{Code: code, Status: ReadinessFail, Detail: failDetail}
}

func modePermittedGate(in *ReadinessInput) ReadinessGate {
	ok := modePermitsDecisions(in.Mode)
	return gateRow(ReadinessGateModePermitted, ok,
		fmt.Sprintf("mode %s permits automatic decisions", strings.ToUpper(in.Mode)),
		fmt.Sprintf("mode %s does not permit automatic decisions (allowed: SHADOW/PAPER/TESTNET/LIVE)", strings.ToUpper(in.Mode)))
}

func pairReadyGate(in *ReadinessInput) ReadinessGate {
	ok := in.PairReadiness == LLMPairReady
	return gateRow(ReadinessGatePairReady, ok,
		"pair readiness "+LLMPairReady,
		fmt.Sprintf("pair readiness %q, want %s (Q3: no degraded path)", in.PairReadiness, LLMPairReady))
}

func probeSideDetail(name string, passed bool, at, now time.Time, maxAge time.Duration) string {
	if !passed {
		return name + " probe did not pass its newest run"
	}
	if at.IsZero() {
		return name + " probe has no timestamp"
	}
	if maxAge <= 0 {
		return name + " probe passes but no freshness TTL is configured"
	}
	if at.After(now) {
		return name + " probe timestamp is in the future"
	}
	age := now.Sub(at)
	return fmt.Sprintf("%s probe passed %s ago", name, age.Round(time.Second))
}

func probeFreshGate(in *ReadinessInput) ReadinessGate {
	ok := in.LLMProbePassed && in.JevProbePassed && in.ProbeMaxAge > 0 &&
		!in.LLMProbeAt.IsZero() && !in.JevProbeAt.IsZero() &&
		!in.LLMProbeAt.After(in.Now) && !in.JevProbeAt.After(in.Now) &&
		in.Now.Sub(in.LLMProbeAt) <= in.ProbeMaxAge && in.Now.Sub(in.JevProbeAt) <= in.ProbeMaxAge
	llmDetail := probeSideDetail("llm", in.LLMProbePassed, in.LLMProbeAt, in.Now, in.ProbeMaxAge)
	jevDetail := probeSideDetail("jev", in.JevProbePassed, in.JevProbeAt, in.Now, in.ProbeMaxAge)
	if ok {
		return gateRow(ReadinessGateProbeFresh, true,
			fmt.Sprintf("%s, %s (ttl %s)", llmDetail, jevDetail, in.ProbeMaxAge), "")
	}
	return gateRow(ReadinessGateProbeFresh, false, "",
		fmt.Sprintf("%s; %s (ttl %s, want both fresh within it)", llmDetail, jevDetail, in.ProbeMaxAge))
}

func networkGate(in *ReadinessInput) ReadinessGate {
	switch strings.ToUpper(strings.TrimSpace(in.Mode)) {
	case LLMRunModeShadow, LLMRunModePaper:
		return gateRow(ReadinessGateNetwork, true,
			fmt.Sprintf("non-trading mode %s: venue consistency is enforced at submit time (M5-13)", strings.ToUpper(in.Mode)), "")
	}
	if in.ConfiguredNetwork == "" {
		return gateRow(ReadinessGateNetwork, false, "", "no configured trading network; cannot prove the venue matches the request")
	}
	ok := strings.EqualFold(strings.TrimSpace(in.ConfiguredNetwork), strings.TrimSpace(in.Network))
	return gateRow(ReadinessGateNetwork, ok,
		fmt.Sprintf("configured %s matches request %s", in.ConfiguredNetwork, in.Network),
		fmt.Sprintf("configured %s != requested %s", in.ConfiguredNetwork, in.Network))
}

func noFullHaltGate(in *ReadinessInput) ReadinessGate {
	softOrMarket := 0
	for _, halt := range in.Halts {
		if strings.EqualFold(strings.TrimSpace(halt.Level), HaltLevelFull) {
			detail := fmt.Sprintf("FULL halt %s is active (id %s)", halt.Level, halt.ID)
			if halt.MarketID != "" {
				detail = fmt.Sprintf("FULL halt is active (id %s, market %s)", halt.ID, halt.MarketID)
			}
			return gateRow(ReadinessGateNoFullHalt, false, "", detail)
		}
		softOrMarket++
	}
	return gateRow(ReadinessGateNoFullHalt, true,
		fmt.Sprintf("no active FULL halt (SOFT/MARKET halts active: %d)", softOrMarket), "")
}

func switchesIntactGate(in *ReadinessInput) ReadinessGate {
	if in.Switches == nil {
		return gateRow(ReadinessGateSwitchesIntact, false, "", "switch state was not supplied; cannot prove the locked switches are on")
	}
	var off []string
	for _, spec := range RiskSwitchCatalog {
		if spec.LockedFromRPC && !in.Switches[spec.Name] {
			off = append(off, spec.Name)
		}
	}
	if len(off) > 0 {
		return gateRow(ReadinessGateSwitchesIntact, false, "",
			fmt.Sprintf("locked switch(es) off: %s (red lines 4/5: only a config edit and restart may turn them off)", strings.Join(off, ", ")))
	}
	return gateRow(ReadinessGateSwitchesIntact, true,
		"locked switches on: haltOnDailyLoss, blockOnUnknownOrders, blockOnClockDrift", "")
}

func reconciledGate(in *ReadinessInput) ReadinessGate {
	if !in.Reconciled {
		return gateRow(ReadinessGateReconciled, false, "", "account is not reconciled (or the last pass found a mismatch)")
	}
	if in.ReconciledUntil.IsZero() || !in.Now.Before(in.ReconciledUntil) {
		return gateRow(ReadinessGateReconciled, false, "",
			fmt.Sprintf("reconciliation validity ended at %s; a fresh CLEAN pass is required", in.ReconciledUntil.UTC().Format(time.RFC3339)))
	}
	return gateRow(ReadinessGateReconciled, true,
		"reconciled, valid until "+in.ReconciledUntil.UTC().Format(time.RFC3339), "")
}

func daySealedGate(in *ReadinessInput) ReadinessGate {
	want := UtcDayKey(in.Now)
	ok := in.DaySealed && in.DayKeyUTC == want
	if ok {
		return gateRow(ReadinessGateDaySealed, true, fmt.Sprintf("day-start baseline for %s is sealed", want), "")
	}
	sealed := in.DayKeyUTC
	if !in.DaySealed || sealed == "" {
		sealed = "none"
	}
	return gateRow(ReadinessGateDaySealed, false, "",
		fmt.Sprintf("day-start baseline for %s is not sealed (sealed: %s); the daily-loss ledger is blind without it", want, sealed))
}

func coverageGate(in *ReadinessInput) ReadinessGate {
	ok := in.CoverageEnabled > 0
	return gateRow(ReadinessGateCoverage, ok,
		fmt.Sprintf("%d markets in the coverage scope", in.CoverageEnabled),
		"no markets in the coverage scope; the decision loop would have nothing to observe")
}

func approvalLiveGate(in *ReadinessInput) ReadinessGate {
	mode := strings.ToUpper(strings.TrimSpace(in.Mode))
	switch mode {
	case LLMRunModeTestnet, LLMRunModeLive:
	default:
		return gateRow(ReadinessGateApprovalLive, true,
			fmt.Sprintf("mode %s does not submit to a venue: no live approval required", mode), "")
	}
	ok := in.LiveApprovals > 0
	return gateRow(ReadinessGateApprovalLive, ok,
		fmt.Sprintf("%d live approval(s) for %s", in.LiveApprovals, in.Network),
		fmt.Sprintf("no live approval for %s: approvals are explicit and per-network (A04)", in.Network))
}
