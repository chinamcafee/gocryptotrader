# LLM Manager

The LLM manager (subsystem key `llm_manager`) owns the LLM/Jev decision pipeline:
analysis of market data through read-only tools, structured candidate generation,
bounded Jev assessment and deterministic risk gating before any order is
considered.

Full design: `docs/llm-dev/` (14 documents + the implemented-state map
`docs/llm-dev/实现后状态对照.md`). Delivery tracking:
`docs/llm-dev/taskProgress/TASKS.md` (milestones `M0` to `M6`).

## Current state

The subsystem is implemented through **M5 (execution & governance) with M6
acceptance in progress**: configuration and lifecycle, the provider/pairing
administration, the budget reservation gate, the read-only tool loop, the market
snapshot builder and coverage rotator, the chat pipeline (sealed storage,
streaming), the decision pipeline (pre-gates → analysis → Jev → synthesis →
hard risk → approval), order execution (claim/status machine, protective exits,
reconciliation, day-start baseline, halts, notifications, mode switches) and the
readiness admission check all exist and are covered by the `engine` test suite
(330+ LLM tests).

The production assembly is wired at `Engine.Start` in two layers. First the
model-free seams: the metrics registry, the §5.6 risk-switch service and the
M6-01 readiness evaluator with its production fact source (`LLMRuntimeFacts` -
config, repository, rotator, switch state; see `engine/llm_runtime_facts.go`).
Then the **client factory + chat corridor**: `BuildActiveClients` reads the
active pair's latest revisions, reveals the API keys from the vault per call
(`llm.vault.directory`, default `<datadir>/llm-vault`) and builds the
OpenAI-compatible and Jev clients; the chat corridor is assembled on top of them
(`engine/llm_chat_runtime.go`) with a scope of the rotator's markets, the
configured account and every version of every non-archived strategy. A missing
piece (no pair, uninitialised vault, unknown exchange) logs and leaves the
affected RPCs fail-closed rather than aborting Start. The **shadow decision
runner** is assembled the same way on top of those clients
(`engine/llm_decision_runtime.go`), and `trigger`/`preview` build their per-run
facts from live state (`engine/llm_decision_facts.go`: frozen strategy text,
approval, pair revisions, gate facts, lazy snapshot assembly, Jev evidence
rebuilt from the transcript) - every LLM RPC is live. One assembly item remains:
the hard-risk facts for SHADOW runs (`RiskFacts` is nil, so a run records the
awaiting-verdict without evaluating; SHADOW never places an order), tracked in
`docs/llm-dev/taskProgress/PROGRESS.md`.

Where the code lives:

| Area | Location |
| --- | --- |
| Model-layer packages (no engine imports - asserted by `llm/boundary_test.go`) | `llm/{candidate,chat,coverage,httpclient,jev,openai,secrets,snapshot,tools,trigger}` |
| Engine subsystems & services | `engine/llm_*.go` (manager, provider admin, snapshot, tools, chat, analysis, jev, decision, risk, execution, protective exits, reconcile, day-start, halt, recovery, readiness, ...) |
| Repository (24 `llm_*` tables, 8 domains) | `database/repository/llm/` |
| Migrations (incl. 21 immutability triggers) | `database/migrations/20261010*` |
| RPC surface | `gctrpc/rpc.proto` (`LLM*` RPCs) + `engine/rpcserver_llm*.go` |
| CLI | `cmd/gctcli/llm_chat.go`, `cmd/gctcli/llm_strategy.go` (incl. `llm readiness`) |

## Configuration

```json
"llm": {
  "enabled": false,
  "runMode": "READ_ONLY",
  "acknowledgeLiveTrading": "",
  "network": "binance-testnet",
  "exchange": "Binance",
  "asset": "usdtmarginedfutures",
  "analysis": { "autoCandidateWindowSeconds": 60 },
  "coverage": { "scope": "ALL_USDTM_PERPETUAL", "rotationCycleMinutes": 10 }
}
```

Defaults are filled by `config.CheckLLMConfig`, which is tolerant of an entirely
absent section so that configurations written before this section existed keep
loading. A `config/versions/v18` migration (recorded, versioned upgrade) is still
tracked as `M0-09` - until then the tolerant default-fill path is the only one.

### Run modes

| Mode | Behaviour |
| --- | --- |
| `READ_ONLY` | Default. Analysis only; no order may be created. |
| `SHADOW` | Automatic decisions are recorded but never executed. |
| `PAPER` | Decisions reach `OrderManager.SubmitFakeOrder`; the exchange is never called. |
| `TESTNET` | Orders are submitted to the exchange sandbox. |
| `LIVE` | Orders are submitted with real funds. |

Two fail-closed rules are applied in `SetupLLMManager` rather than at use sites,
so every downstream component observes the same effective mode:

1. An unknown or empty mode becomes `READ_ONLY`.
2. `LIVE` without the exact `acknowledgeLiveTrading` string
   (`I_UNDERSTAND`) becomes `READ_ONLY`, and the downgrade is logged.

## Status snapshot

`LLMManager.Status()` returns an immutable `LLMStatus`. Every permission field
defaults to its fail-closed value:

| Field | Meaning |
| --- | --- |
| `RunMode` | The effective mode after downgrades. |
| `LiveUnlocked` | True only when the effective mode is `LIVE`. |
| `PairReadiness` | `INCOMPLETE` until both an LLM and a Jev profile are active and probe-verified. Jev is mandatory, so there is no degraded "LLM only" mode. |
| `PairEpoch` | Increments whenever the active pairing changes; every decision records the epoch it ran under so stale decisions can be detected. |
| `TradeAuthorized` | False until a decision has been approved end to end. |
| `ExecutionReady` | False until mode, pairing, halt and database preconditions all hold. |
| `AccountReconciled` | False until a reconciliation run has matched the local view against the exchange. |
| `Halted` / `HaltLevel` | Emergency-stop state (`NONE` / `SOFT` / `MARKET` / `FULL`). |

The admission report `gctcli llm readiness --network <net> [--mode <mode>]`
walks eleven gates in a contractual order (`INPUT_COMPLETE`, `MODE_PERMITTED`,
`PAIR_READY`, `PROBE_FRESH`, `NETWORK_CONSISTENT`, `NO_FULL_HALT`,
`RISK_SWITCHES_INTACT`, `ACCOUNT_RECONCILED`, `DAY_START_SEALED`,
`COVERAGE_PRESENT`, `APPROVAL_LIVE`) and exits 2 when NOT_READY. SHADOW/PAPER
are exempt from `NETWORK` and `APPROVAL` (no venue). The gate list is also the
vocabulary of the M6-02 failure-matrix drills
(`docs/llm-dev/taskProgress/acceptance/`).

## Lifecycle

| Method | Notes |
| --- | --- |
| `SetupLLMManager(exchangeManager, cfg)` | Returns `errNilExchangeManager` or `errNilLLMConfig` for missing inputs. |
| `Start(ctx)` | Idempotent guard: a second call returns `ErrSubSystemAlreadyStarted`. |
| `Stop()` | Guarded by `CompareAndSwap`, so a second call returns `ErrSubSystemNotStarted`. |
| `IsRunning()` | Safe on a nil receiver. |
| `Status()` / `Config()` | Read-only accessors; both safe on a nil receiver. |

Shutdown ordering matters: the engine stops the LLM manager **before** the order
manager so that no in-flight decision can submit an order while the order manager
is shutting down. The restart path is `CrashRecovery.Recover`: intents left in
`SUBMITTING` become `UNKNOWN` (reconciliation required, never blindly retried -
the claim is only ever taken from `PREPARED`), stale `PREPARED` intents expire.
The kill -9 drill lives in `engine/llm_crash_drill_test.go`.

## Known gaps before this subsystem can trade

Tracked in `docs/llm-dev/taskProgress/TASKS.md`:

- The production assembly (client factory) described under "Current state";
  it unblocks the chat/trigger/preview/metrics/risk-switch/readiness RPCs and
  the data-dependent M6 rows (`M6-03/04/05/07/08`).
- `M0-09` a `config/versions/v18` migration is required before release; until
  then an older configuration simply receives defaults through `CheckLLMConfig`
  instead of a recorded, versioned upgrade.
- `M6-09` documentation finalization is complete for the acceptance set
  (`taskProgress/acceptance/`) and the implemented-state map; any residual
  doc drift is tracked in `docs/llm-dev/实现后状态对照.md`.
