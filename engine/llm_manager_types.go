package engine

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/thrasher-corp/gocryptotrader/config"
)

// llmManagerName is the subsystem status key surfaced through
// Engine.GetSubsystemsStatus and the RPC subsystem controls.
const llmManagerName = "llm_manager"

// Accepted LLM run modes. Kept in sync with
// docs/llm-dev/01-需求与产品目标.md §5 and validated by config.CheckLLMConfig.
const (
	// LLMRunModeReadOnly permits analysis only; no order may be created.
	LLMRunModeReadOnly = "READ_ONLY"
	// LLMRunModeShadow permits automatic decisions but never executes them.
	LLMRunModeShadow = "SHADOW"
	// LLMRunModePaper routes decisions through OrderManager.SubmitFakeOrder.
	LLMRunModePaper = "PAPER"
	// LLMRunModeTestnet submits orders to the exchange sandbox.
	LLMRunModeTestnet = "TESTNET"
	// LLMRunModeLive submits orders with real funds. Requires an explicit
	// acknowledgement string in config before it can take effect.
	LLMRunModeLive = "LIVE"
)

// liveTradingAck is the exact string an operator must place in
// llm.acknowledgeLiveTrading before LIVE mode is honoured. Any other value is
// treated as a refusal and the engine falls back to READ_ONLY, because a
// partially configured live setup must never trade.
const liveTradingAck = "I_UNDERSTAND"

// defaultLLMCacheKeys bounds the candle cache the manager owns. Eviction is
// whole-key (pair + asset + interval) and one key holds a single market's
// history, so a few hundred keys cover every tracked market at the typical one
// or two intervals without letting memory grow with uptime.
const defaultLLMCacheKeys = 256

// Pair readiness values reported for the active LLM x Jev pairing.
const (
	// LLMPairIncomplete means at least one side of the pairing is missing.
	LLMPairIncomplete = "INCOMPLETE"
	// LLMPairReady means both profile revisions are active and probe-verified.
	LLMPairReady = "READY"
	// LLMPairDegraded means both sides are enabled but a probe is stale or failing.
	LLMPairDegraded = "DEGRADED"
)

// Errors returned by the LLM manager lifecycle.
var (
	errNilLLMConfig = errors.New("received nil LLM config")
	// ErrNilRotator marks a rotation call on a nil rotator.
	ErrNilRotator = errors.New("rotation is nil")
)

// LLMManager owns the LLM/Jev decision subsystem.
//
// Scope note: at this stage the manager implements the lifecycle, config
// validation and a fail-closed status snapshot only. The model clients
// (llm/openai, llm/jev), the read-only tool set and the decision pipeline are
// tracked as M1/M2/M4 in docs/llm-dev/taskProgress/TASKS.md. Keeping the shell
// in place first means the REST/gRPC surfaces and the webUI can be wired
// against a subsystem that is genuinely running.
type LLMManager struct {
	started  atomic.Bool
	shutdown chan struct{}

	cfg       *config.LLMConfig
	exchanges iExchangeManager

	// runMode is the effective mode after fail-closed downgrades have been
	// applied; it may differ from cfg.RunMode (see SetupLLMManager). Guarded by
	// modeMu: a live operator switch writes it while consumers read it.
	runMode string
	modeMu  sync.RWMutex

	// rotation is layer one of the coverage design. It is nil when the coverage
	// section is disabled, which is a legitimate configuration.
	rotation *Rotator

	// cache keeps closed candles fetched through the snapshot and tool paths.
	// The rotation's reload warms the same storage, so a decision's second
	// reader pays no second exchange weight.
	cache *KlineCache

	// marketWatch is the market-delist watcher the rotation runs after each
	// market-list refresh; nil leaves the watch off. Set before Start only.
	marketWatch *MarketDelistWatcher
}

// LLMStatus is an immutable snapshot of subsystem state. Every field defaults
// to the fail-closed value; callers must not infer permission from absence.
type LLMStatus struct {
	RunMode string `json:"runMode"`
	// LiveUnlocked reports whether the operator acknowledgement and the
	// envelope checks required for LIVE are satisfied.
	LiveUnlocked bool `json:"liveUnlocked"`
	// PairReadiness is INCOMPLETE until both an LLM and a Jev profile are
	// active and probe-verified; Jev is mandatory (OPEN-QUESTIONS Q3 = A).
	PairReadiness string `json:"pairReadiness"`
	// PairEpoch increments whenever the active pairing changes. Every decision
	// records the epoch it ran under so stale decisions can be detected.
	PairEpoch int64 `json:"pairEpoch"`
	// TradeAuthorized is false until a decision has been approved end to end.
	TradeAuthorized bool `json:"tradeAuthorized"`
	// ExecutionReady is false until the mode, pairing, halt and database
	// preconditions all hold.
	ExecutionReady bool `json:"executionReady"`
	// AccountReconciled is false until a reconciliation run has matched the
	// local position view against the exchange.
	AccountReconciled bool   `json:"accountReconciled"`
	Halted            bool   `json:"halted"`
	HaltLevel         string `json:"haltLevel"`
}

// Message-role and audit-actor vocabulary shared by the LLM services: roles
// label the OpenAI wire format, the actor labels system-written audit rows.
const (
	msgRoleSystem    = "system"
	msgRoleUser      = "user"
	auditActorSystem = "system"
	orderSideBuy     = "BUY"
	orderSideSell    = "SELL"
)
