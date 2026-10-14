-- +goose Up
CREATE TABLE IF NOT EXISTS llm_risk_evaluations
(
    id                TEXT PRIMARY KEY,
    decision_run_id   TEXT NOT NULL REFERENCES llm_decision_runs(id),
    check_version     TEXT NOT NULL,
    verdict           TEXT NOT NULL,
    checks_json       TEXT NOT NULL,
    rejections_json   TEXT,
    envelope_hash     TEXT NOT NULL,
    computed_qty      TEXT,
    computed_notional TEXT,
    reference_price   TEXT,
    evaluated_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (decision_run_id, evaluated_at)
);

-- quantity, price and notional_usd are decimal strings: order sizing must never be
-- recomputed through binary floating point.
CREATE TABLE IF NOT EXISTS llm_order_intents
(
    id                  TEXT PRIMARY KEY,
    decision_run_id     TEXT NOT NULL REFERENCES llm_decision_runs(id),
    risk_evaluation_id  TEXT NOT NULL REFERENCES llm_risk_evaluations(id),
    strategy_version_id TEXT NOT NULL,
    approval_id         TEXT NOT NULL REFERENCES llm_strategy_approvals(id),
    exchange            TEXT NOT NULL,
    pair                TEXT NOT NULL,
    asset               TEXT NOT NULL,
    action              TEXT NOT NULL,
    side                TEXT NOT NULL,
    order_type          TEXT NOT NULL,
    quantity            TEXT NOT NULL,
    price               TEXT,
    reduce_only         BOOLEAN NOT NULL,
    client_order_id     TEXT NOT NULL UNIQUE,
    notional_usd        TEXT NOT NULL,
    envelope_hash       TEXT NOT NULL,
    pair_epoch          INTEGER NOT NULL,
    status              TEXT NOT NULL,
    internal_order_id   TEXT,
    exchange_order_id   TEXT,
    expires_at          TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS llm_protective_exits
(
    id                  TEXT PRIMARY KEY,
    parent_intent_id    TEXT NOT NULL REFERENCES llm_order_intents(id),
    exit_kind           TEXT NOT NULL,
    client_order_id     TEXT NOT NULL UNIQUE,
    quantity            TEXT NOT NULL,
    trigger_price       TEXT NOT NULL,
    status              TEXT NOT NULL,
    fail_safe_triggered BOOLEAN NOT NULL DEFAULT false,
    internal_order_id   TEXT,
    exchange_order_id   TEXT,
    created_at          TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE llm_protective_exits;
DROP TABLE llm_order_intents;
DROP TABLE llm_risk_evaluations;
