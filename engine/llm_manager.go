package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/config"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/log"
)

// SetupLLMManager creates the LLM decision subsystem.
//
// The run mode is normalised here rather than at use sites so that every
// downstream component observes the same effective mode. Two fail-closed rules
// apply (docs/llm-dev/10-安全与风控.md §5):
//
//  1. An unknown or empty mode becomes READ_ONLY.
//  2. LIVE without the exact acknowledgement string becomes READ_ONLY, and the
//     downgrade is logged so an operator cannot silently believe they are live.
func SetupLLMManager(em iExchangeManager, cfg *config.LLMConfig) (*LLMManager, error) {
	if em == nil {
		return nil, errNilExchangeManager
	}
	if cfg == nil {
		return nil, errNilLLMConfig
	}

	mode := normaliseLLMRunMode(cfg.RunMode)
	if mode == LLMRunModeLive && cfg.AcknowledgeLiveTrading != liveTradingAck {
		log.Warnf(log.LLMMgr, "LLM run mode LIVE requested without llm.acknowledgeLiveTrading=%q; downgrading to %s",
			liveTradingAck, LLMRunModeReadOnly)
		mode = LLMRunModeReadOnly
	}

	cache, err := NewKlineCache(defaultLLMCacheKeys)
	if err != nil {
		return nil, fmt.Errorf("building the LLM candle cache: %w", err)
	}

	return &LLMManager{
		cfg:       cfg,
		exchanges: em,
		shutdown:  make(chan struct{}),
		runMode:   mode,
		cache:     cache,
	}, nil
}

// normaliseLLMRunMode maps any input to a known run mode, defaulting to
// READ_ONLY.
func normaliseLLMRunMode(mode string) string {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case LLMRunModeShadow:
		return LLMRunModeShadow
	case LLMRunModePaper:
		return LLMRunModePaper
	case LLMRunModeTestnet:
		return LLMRunModeTestnet
	case LLMRunModeLive:
		return LLMRunModeLive
	default:
		return LLMRunModeReadOnly
	}
}

// Start runs the subsystem.
//
// When the coverage section is enabled it also starts layer-one rotation: the
// slow sweep that keeps every tracked market warm so a trigger can be noticed
// anywhere. The sweep is read-only market data and is bounded by the plan's
// weight budget, so it cannot crowd out a decision's own fetches.
func (m *LLMManager) Start(ctx context.Context) error {
	if m == nil {
		return ErrNilSubsystem
	}
	if !m.started.CompareAndSwap(false, true) {
		return ErrSubSystemAlreadyStarted
	}
	m.shutdown = make(chan struct{})

	rotation, err := m.SetupRotation()
	if err != nil {
		// The mode and lifecycle are already established; a coverage problem is
		// reported and the rest of the subsystem still runs, because the
		// decision path does not depend on layer one having started.
		log.Errorf(log.LLMMgr, "coverage rotation could not be set up: %v", err)
	} else if rotation != nil {
		m.rotation = rotation
		loopCtx, cancel := context.WithCancel(ctx)
		go func() {
			defer cancel()
			rotation.rotationLoop(loopCtx)
		}()
		log.Debugf(log.LLMMgr, "coverage rotation started over %d markets", rotation.Stats().Markets)
	}

	log.Debugf(log.LLMMgr, "LLM manager %v mode %s", MsgSubSystemStarted, m.runMode)
	return nil
}

// SetMarketWatch attaches the market-delist watcher the rotation should run.
//
// It must be called before Start (the rotation loop reads the field without a
// lock, exactly like its other pre-start wiring); a nil watcher leaves the
// watch off, which is a valid configuration.
func (m *LLMManager) SetMarketWatch(w *MarketDelistWatcher) {
	if m == nil {
		return
	}
	m.marketWatch = w
}

// IsRunning checks whether the subsystem is running.
func (m *LLMManager) IsRunning() bool {
	if m == nil {
		return false
	}
	return m.started.Load()
}

// Stop stops the subsystem.
//
// It is deliberately safe to call on a manager that never started so that
// engine shutdown does not need to special-case a failed startup.
func (m *LLMManager) Stop() error {
	if m == nil {
		return ErrNilSubsystem
	}
	if !m.started.CompareAndSwap(true, false) {
		return ErrSubSystemNotStarted
	}
	close(m.shutdown)
	log.Debugf(log.LLMMgr, "LLM manager %v", MsgSubSystemShutdown)
	return nil
}

// Status returns a fail-closed snapshot of subsystem state.
//
// Until the provider registry (M1-09/M1-10) and the reconciliation loop
// (M5-08) land, readiness stays INCOMPLETE and every permission flag stays
// false. Returning conservative values here means the webUI and any RPC caller
// cannot mistake an unimplemented capability for an available one.
func (m *LLMManager) Status() LLMStatus {
	if m == nil {
		return LLMStatus{RunMode: LLMRunModeReadOnly, PairReadiness: LLMPairIncomplete, HaltLevel: "NONE"}
	}
	return LLMStatus{
		RunMode:           m.runMode,
		LiveUnlocked:      m.runMode == LLMRunModeLive,
		PairReadiness:     LLMPairIncomplete,
		PairEpoch:         0,
		TradeAuthorized:   false,
		ExecutionReady:    false,
		AccountReconciled: false,
		Halted:            false,
		HaltLevel:         "NONE",
	}
}

// Rotation returns the layer-one rotation, or nil when coverage is disabled or
// the manager has not started.
func (m *LLMManager) Rotation() *Rotator {
	if m == nil {
		return nil
	}
	return m.rotation
}

// EffectiveRunMode reports the run mode after fail-closed downgrades have been
// applied; a nil manager reports READ_ONLY, the same fail-closed default.
func (m *LLMManager) EffectiveRunMode() string {
	if m == nil {
		return LLMRunModeReadOnly
	}
	m.modeMu.RLock()
	defer m.modeMu.RUnlock()
	return m.runMode
}

// SetRunMode applies an operator-approved switch to the runtime. Callers must
// have passed ModeSwitcher.Switch first (re-auth, unlock, audit, notify); this
// setter carries no guards of its own.
func (m *LLMManager) SetRunMode(mode string) {
	if m == nil {
		return
	}
	m.modeMu.Lock()
	defer m.modeMu.Unlock()
	m.runMode = mode
}

// Cache returns the candle cache shared by the rotation, the snapshot assembler
// and the tool executors. It is nil only for a nil manager.
func (m *LLMManager) Cache() *KlineCache {
	if m == nil {
		return nil
	}
	return m.cache
}

// InvalidateLLMCachedPair drops a pair from the LLM candle cache, when the
// subsystem and its cache exist.
//
// This is the hook for the moment a pair leaves the enabled set: a disabled
// market must not keep being served from storage that was fetched while it was
// still tracked. Safe on a bot that never enabled the LLM manager.
func (bot *Engine) InvalidateLLMCachedPair(pair currency.Pair, a asset.Item) int {
	if bot == nil || bot.llmManager == nil {
		return 0
	}
	return bot.llmManager.Cache().InvalidatePair(pair, a)
}

// Config returns the configuration the manager was set up with.
func (m *LLMManager) Config() *config.LLMConfig {
	if m == nil {
		return nil
	}
	return m.cfg
}
