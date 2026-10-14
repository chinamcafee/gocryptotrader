package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// M4-11 装配件 · 快照 → 硬风控事实（B 组数据质量 + C 组账户/仓位）。
//
// 来源钉死（07 §5 条文 + 本仓侦察结论）：
//   - `SnapshotQuality` = 快照**全部已标注类别的折叠最坏值**——risk 检查只放行
//     `EXCHANGE_REST`/`DATABASE_CACHED`，所以给"最坏值"而不是"是否降级"，两者
//     判据不同（`PARTIAL` 不降级但也不在放行集）；无标注 = UNKNOWN（未评估的
//     事实不是可信事实）。
//   - `SnapshotAgeMs` = `FreshnessMS`（"最老必需事实的年龄"——即风险检查要问的
//     那个数）；`MaxSnapshotAgeMs`/`MaxAccountInventoryAgeMs` 为**政策输入**
//     （库中无配置字段，禁发明默认）。
//   - `CandleGap` = `Gaps` 非空；`OrderbookValid` = 盘口事实存在（构建期
//     `Validate` 已拒交叉/空盘——快照侧原注释）。
//   - 账户/仓位来自 `snapshot.Account`（由 M5-08 生产者 `buildSnapshotAccountFacts`
//     填充）：权益/可用直映射，仓位符号已在生产者落在 `Side`/`Size`；保证金模式/
//     单向持仓/日界基线/计数由**显式输入**带入。
//
// 纪律：**决策必须有账户**——快照不带账户就是读不到，拒；四个仓位字段不齐也拒。

// ErrRiskFactsInput marks assembler material a fact set cannot be built from.
var ErrRiskFactsInput = errors.New("llm risk facts: the input material is incomplete")

// RiskSnapshotFactsInput is the reviewed material the data and account groups
// are mapped from.
type RiskSnapshotFactsInput struct {
	// Result is the exact snapshot result the decision ran on.
	Result *SnapshotResult
	// Now is the instant ages are measured against (injected).
	Now time.Time
	// MaxSnapshotAgeMs and MaxAccountInventoryAgeMs are the policy limits; both
	// must be positive -- the checks refuse on non-positive limits, and a fact
	// set that cannot state its own limits is not a fact set.
	MaxSnapshotAgeMs         int64
	MaxAccountInventoryAgeMs int64
	// AccountReconciled is the run's reconciliation fact (persisted state, not
	// a snapshot read).
	AccountReconciled bool
}

// RiskAccountFactsInput is the venue-phase material the account facts carry
// beyond the snapshot's own read.
type RiskAccountFactsInput struct {
	AccountID               string
	TargetAccountID         string
	MarginType              string
	ExpectedMarginType      string
	HedgeMode               bool
	DayStartEquityUSD       *string
	ExternalFlowsUSD        *string
	UnresolvedUnknownOrders int
	UnreconciledPartials    int
}

// riskDataFactsFromSnapshot maps the snapshot onto the risk engine's data facts.
func riskDataFactsFromSnapshot(in *RiskSnapshotFactsInput) (*RiskDataFacts, error) {
	if in == nil || in.Result == nil || in.Result.Snapshot == nil {
		return nil, fmt.Errorf("%w: the snapshot result is required", ErrRiskFactsInput)
	}
	if in.Now.IsZero() {
		return nil, ErrSnapshotClockMissing
	}
	if in.MaxSnapshotAgeMs <= 0 || in.MaxAccountInventoryAgeMs <= 0 {
		return nil, fmt.Errorf("%w: the age limits must be positive", ErrRiskFactsInput)
	}
	snap := in.Result.Snapshot
	accountAge, err := accountInventoryAgeMs(snap, in.Now)
	if err != nil {
		return nil, err
	}
	return &RiskDataFacts{
		SnapshotQuality:          worstSnapshotQuality(snap),
		SnapshotAgeMs:            snap.FreshnessMS,
		MaxSnapshotAgeMs:         in.MaxSnapshotAgeMs,
		AccountInventoryAgeMs:    accountAge,
		MaxAccountInventoryAgeMs: in.MaxAccountInventoryAgeMs,
		CandleGap:                len(snap.Gaps) > 0,
		OrderbookValid:           snap.OrderBook != nil,
		AccountReconciled:        in.AccountReconciled,
	}, nil
}

// riskAccountFactsFromSnapshot maps the snapshot's account read plus the
// venue-phase material onto the risk engine's account facts and positions.
func riskAccountFactsFromSnapshot(snap *snapshot.MarketSnapshot, src *RiskAccountFactsInput, now time.Time) (RiskAccountFacts, []RiskPositionFact, error) {
	if snap == nil || snap.Account == nil {
		return RiskAccountFacts{}, nil, fmt.Errorf("%w: the snapshot carries no account facts", ErrRiskFactsInput)
	}
	if src == nil || strings.TrimSpace(src.AccountID) == "" || strings.TrimSpace(src.TargetAccountID) == "" ||
		strings.TrimSpace(src.MarginType) == "" || strings.TrimSpace(src.ExpectedMarginType) == "" {
		return RiskAccountFacts{}, nil, fmt.Errorf("%w: account id, target account and both margin types are required", ErrRiskFactsInput)
	}
	if now.IsZero() {
		return RiskAccountFacts{}, nil, ErrSnapshotClockMissing
	}
	if _, err := accountInventoryAgeMs(snap, now); err != nil {
		return RiskAccountFacts{}, nil, err
	}
	positions := make([]RiskPositionFact, 0, len(snap.Account.Positions))
	for i := range snap.Account.Positions {
		pos := snap.Account.Positions[i]
		side := strings.ToUpper(strings.TrimSpace(pos.Side))
		if side != PositionSideLong && side != PositionSideShort {
			return RiskAccountFacts{}, nil, fmt.Errorf("%w: position %s carries side %q", ErrRiskFactsInput, pos.Symbol, pos.Side)
		}
		if strings.TrimSpace(pos.Symbol) == "" || strings.TrimSpace(pos.Size) == "" {
			return RiskAccountFacts{}, nil, fmt.Errorf("%w: a position fact is incomplete", ErrRiskFactsInput)
		}
		positions = append(positions, RiskPositionFact{
			MarketID:   pos.Symbol,
			Side:       side,
			Quantity:   pos.Size,
			EntryPrice: pos.EntryPrice,
			MarkPrice:  pos.MarkPrice,
		})
	}
	return RiskAccountFacts{
		AccountID:               src.AccountID,
		TargetAccountID:         src.TargetAccountID,
		EquityUSD:               snap.Account.TotalEquityUSD,
		AvailableMarginUSD:      snap.Account.AvailableBalanceUSD,
		MarginType:              src.MarginType,
		ExpectedMarginType:      src.ExpectedMarginType,
		HedgeMode:               src.HedgeMode,
		DayStartEquityUSD:       src.DayStartEquityUSD,
		ExternalFlowsUSD:        src.ExternalFlowsUSD,
		UnresolvedUnknownOrders: src.UnresolvedUnknownOrders,
		UnreconciledPartials:    src.UnreconciledPartials,
	}, positions, nil
}

// accountInventoryAgeMs measures the account read's age, refusing an account
// nobody can age: a missing read time or a future stamp is unreadable, not
// fresh.
func accountInventoryAgeMs(snap *snapshot.MarketSnapshot, now time.Time) (int64, error) {
	if snap == nil || snap.Account == nil {
		return 0, fmt.Errorf("%w: the snapshot carries no account facts", ErrRiskFactsInput)
	}
	if snap.Account.ReadAt.IsZero() {
		return 0, fmt.Errorf("%w: %w", ErrRiskFactsInput, snapshot.ErrMissingReadTime)
	}
	age := now.Sub(snap.Account.ReadAt)
	if age < 0 {
		return 0, fmt.Errorf("%w: %w", ErrRiskFactsInput, snapshot.ErrFactFromTheFuture)
	}
	return age.Milliseconds(), nil
}

// worstSnapshotQuality folds the snapshot's **market-category** labels to the
// worst one. Account and funding are excluded on purpose:
//
//   - the builder labels a caller-supplied account LOCAL_TRACKED by
//     construction (llm_snapshot.go), so folding it in would reject every run;
//     the account has dedicated checks (RISK_ACCOUNT_INVENTORY_STALE,
//     RISK_ACCOUNT_NOT_RECONCILED);
//   - funding has its own check (RISK_FUNDING_UNAVAILABLE) and is
//     deliberately non-blocking on freshness: a slow funding read must not
//     stop trading for most of each settlement cycle.
//
// Unlabelled facts are UNKNOWN: an unassessed fact is not a trusted fact.
func worstSnapshotQuality(snap *snapshot.MarketSnapshot) candidate.DataQuality {
	if snap == nil || len(snap.Quality) == 0 {
		return candidate.QualityUnknown
	}
	var worst candidate.DataQuality
	first := true
	for category := range snap.Quality {
		switch category {
		case snapshot.CategoryAccount, snapshot.CategoryFunding:
			continue
		}
		label := snap.QualityOf(category)
		if first {
			worst, first = label, false
			continue
		}
		worst = worstQuality(worst, label)
	}
	if first {
		// Only excluded categories were labelled: nothing market-side was
		// assessed, which is UNKNOWN rather than trusted.
		return candidate.QualityUnknown
	}
	return worst
}

// worstQuality returns the lower-ranked of two labels. Downgrade refuses
// upgrades, which is exactly the pairwise minimum over the label order.
func worstQuality(a, b candidate.DataQuality) candidate.DataQuality {
	if lowered, err := a.Downgrade(b); err == nil {
		return lowered
	}
	return a
}
