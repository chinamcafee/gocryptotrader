package engine

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/config"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/llm/coverage"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
	"github.com/thrasher-corp/gocryptotrader/llm/trigger"
	"github.com/thrasher-corp/gocryptotrader/log"
)

// Config mapping for the rotation.
//
// This exists as its own step because a config field and a plan field that look
// alike are not always the same thing: the config carries numbers a human
// typed, and the plan carries the arithmetic those numbers imply. Mapping them
// in one place, with a test, is what keeps a config edit from silently changing
// the weight budget.

// planFromCoverage maps the coverage config onto a coverage plan.
func planFromCoverage(c *config.LLMCoverageConfig) coverage.Plan {
	plan := coverage.DefaultPlan()
	if c == nil {
		return plan
	}
	plan.Enabled = c.Enabled
	if strings.TrimSpace(c.Scope) != "" {
		plan.Scope = c.Scope
	}
	if c.RotationCycleMinutes > 0 {
		plan.RotationCycleMinutes = c.RotationCycleMinutes
	}
	if c.KlineIncrementLimit > 0 {
		// Saturate before narrowing: an absurd config value must not wrap into
		// a negative limit.
		v := min(c.KlineIncrementLimit, math.MaxInt)
		plan.KlineIncrementLimit = int(v)
	}
	if c.KlineFullLimit > 0 {
		v := min(c.KlineFullLimit, math.MaxInt)
		plan.KlineFullLimit = int(v)
	}
	if c.FullReloadSpreadMinutes > 0 {
		plan.FullReloadSpreadMinutes = c.FullReloadSpreadMinutes
	}
	if c.WeightBudgetFraction > 0 {
		plan.WeightBudgetFraction = c.WeightBudgetFraction
	}
	if len(c.PersistIntervals) > 0 {
		plan.PersistIntervals = append([]string(nil), c.PersistIntervals...)
	}
	return plan
}

// intervalFromConfig maps the configured interval spelling onto the engine's.
//
// An unknown spelling is an error rather than a default: the freshness limit and
// the fetch window are both derived from the interval, so quietly substituting
// one would apply arithmetic the operator did not ask for.
func intervalFromConfig(spelling string) (kline.Interval, error) {
	switch strings.ToLower(strings.TrimSpace(spelling)) {
	case "", "1h":
		return kline.OneHour, nil
	case "1m":
		return kline.OneMin, nil
	case "5m":
		return kline.FiveMin, nil
	case "15m":
		return kline.FifteenMin, nil
	case "4h":
		return kline.FourHour, nil
	case "1d":
		return kline.OneDay, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnsupportedSnapshotInterval, spelling)
	}
}

// indicatorParamsFromConfig maps the indicator config onto the contract's
// parameters, falling back to the documented defaults field by field.
func indicatorParamsFromConfig(c *config.LLMIndicatorConfig) snapshot.IndicatorParams {
	params := snapshot.DefaultIndicatorParams()
	if c == nil {
		return params
	}
	if c.MACDFast > 0 {
		params.MACDFast = c.MACDFast
	}
	if c.MACDSlow > 0 {
		params.MACDSlow = c.MACDSlow
	}
	if c.MACDSignal > 0 {
		params.MACDSignal = c.MACDSignal
	}
	if len(c.SMAPeriods) > 0 {
		params.SMAPeriods = append([]int64(nil), c.SMAPeriods...)
	}
	if len(c.EMAPeriods) > 0 {
		params.EMAPeriods = append([]int64(nil), c.EMAPeriods...)
	}
	if c.RSIPeriod > 0 {
		params.RSIPeriod = c.RSIPeriod
	}
	if c.ATRPeriod > 0 {
		params.ATRPeriod = c.ATRPeriod
	}
	if c.BollingerPeriod > 0 {
		params.BollingerPeriod = c.BollingerPeriod
	}
	return params
}

// SetupRotation builds the layer-one rotation from configuration.
//
// It does not start anything: a rotator that cannot be built is a configuration
// problem an operator should see before the bot begins calling the exchange, and
// separating construction from the loop is what allows that.
//
// A disabled coverage section yields a nil rotator and no error, because "not
// running the rotation" is a legitimate configuration and not a failure.
func (m *LLMManager) SetupRotation() (*Rotator, error) {
	if m == nil {
		return nil, ErrNilSubsystem
	}
	cfg := m.Config()
	if cfg == nil {
		return nil, errNilLLMConfig
	}
	if !cfg.Coverage.Enabled {
		return nil, nil
	}
	exch, err := m.exchanges.GetExchangeByName(cfg.Exchange)
	if err != nil {
		return nil, fmt.Errorf("resolving exchange %q for the rotation: %w", cfg.Exchange, err)
	}
	item, err := asset.New(cfg.Asset)
	if err != nil {
		return nil, fmt.Errorf("resolving asset %q for the rotation: %w", cfg.Asset, err)
	}
	interval, err := intervalFromConfig(cfg.Indicators.DefaultInterval)
	if err != nil {
		return nil, err
	}
	// The exchange must satisfy the rotation's narrow interface. This is a
	// runtime check as well as a compile-time one because the manager holds the
	// interface type, so the concrete wrapper is not known until here.
	src, ok := exch.(iRotationSource)
	if !ok {
		return nil, fmt.Errorf("%w: exchange %q does not expose the rotation calls",
			ErrNilRotationSource, cfg.Exchange)
	}
	rotator, err := NewRotator(src, &RotationConfig{
		Asset:    item,
		Interval: interval,
		Plan:     planFromCoverage(&cfg.Coverage),
		Cache:    m.cache,
	})
	if err != nil {
		return nil, err
	}
	// The production assembly warms the candle cache on the first sweep: a
	// restart must not leave the screener blind until the next clock hour.
	rotator.warmOnFirstSweep = true
	// The scanner is part of the production rotation: a sweep that reloads
	// history without feeding the triggers would keep the markets warm and
	// notice nothing.
	scanner, err := NewTriggerScanner(triggerPolicyFromConfig(cfg), time.Now)
	if err != nil {
		return nil, fmt.Errorf("building the trigger scanner: %w", err)
	}
	scanInterval, err := snapshotInterval(interval)
	if err != nil {
		return nil, fmt.Errorf("resolving the scan interval: %w", err)
	}
	params := indicatorParamsFromConfig(&cfg.Indicators)
	rotator.attachScanner(scanner, &params, scanInterval)
	// The delist watch is optional and pre-attached by Engine.Start before the
	// loop runs: a market disappearing from the enabled list must trigger the
	// 10 §7 row 18 response chain instead of being a silent shrink.
	rotator.attachDelistWatcher(m.marketWatch)
	return rotator, nil
}

// triggerPolicyFromConfig starts from the Q5 defaults and applies the two
// configuration options; the armed-by-default triggers stay armed.
func triggerPolicyFromConfig(cfg *config.LLMConfig) trigger.Policy {
	policy := trigger.DefaultPolicy()
	if cfg == nil {
		return policy
	}
	policy.FundingWindow = cfg.Triggers.FundingWindow
	if cfg.Triggers.FundingWindowLeadSeconds > 0 {
		policy.FundingWindowLead = time.Duration(cfg.Triggers.FundingWindowLeadSeconds) * time.Second
	}
	policy.ATRSpike = cfg.Triggers.ATRSpike
	if cfg.Triggers.ATRSpikeMultiple > 0 {
		policy.ATRSpikeMultiple = cfg.Triggers.ATRSpikeMultiple
	}
	return policy
}

// rotationWindow is how long one call to Rotator.Run lasts before the market
// list is re-read.
//
// Re-reading costs one map operation plus one exchange call for the enabled
// list, and it is what makes an operator enabling a market see it covered
// without a restart. A cycle is the natural period: the list cannot usefully
// change faster than the sweep that walks it.
const rotationWindow = 10 * time.Minute

// rotationLoop runs layer one until the context ends.
//
// Every path sleeps, so the loop cannot spin: the plan's spacing when it made
// progress, and the weight gate's own figure when the budget refused. That is
// the property that keeps a bug here from becoming a request flood.
func (r *Rotator) rotationLoop(ctx context.Context) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := r.RefreshMarkets(ctx); err != nil {
			log.Warnf(log.LLMMgr, "coverage rotation could not read the market list: %v", err)
		} else if r.delist != nil {
			// A market that left the list gets the row-18 response before the
			// next sweep starts covering whatever remains.
			if _, err := r.delist.Check(ctx); err != nil {
				log.Warnf(log.LLMMgr, "market delist watch: %v", err)
			}
		}
		deadline := time.Now().Add(rotationWindow)
		for time.Now().Before(deadline) {
			if _, err := r.Run(ctx, 1, Sleep); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Warnf(log.LLMMgr, "coverage rotation stopped: %v", err)
				return
			}
		}
	}
}
