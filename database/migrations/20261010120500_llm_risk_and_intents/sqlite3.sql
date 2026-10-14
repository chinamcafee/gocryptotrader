-- +goose Up
CREATE TABLE IF NOT EXISTS llm_risk_evaluations
(
    id                TEXT NOT NULL PRIMARY KEY,
    decision_run_id   TEXT NOT NULL,
    check_version     TEXT NOT NULL,
    verdict           TEXT NOT NULL,
    checks_json       TEXT NOT NULL,
    rejections_json   TEXT,
    envelope_hash     TEXT NOT NULL,
    computed_qty      TEXT,
    computed_notional TEXT,
    reference_price   TEXT,
    evaluated_at      timestamp NOT NULL,
    UNIQUE(decision_run_id, evaluated_at),
    FOREIGN KEY(decision_run_id) REFERENCES llm_decision_runs(id)
);

-- quantity, price and notional_usd are decimal strings: order sizing must never be
-- recomputed through binary floating point.
CREATE TABLE IF NOT EXISTS llm_order_intents
(
    id                  TEXT NOT NULL PRIMARY KEY,
    decision_run_id     TEXT NOT NULL,
    risk_evaluation_id  TEXT NOT NULL,
    strategy_version_id TEXT NOT NULL,
    approval_id         TEXT NOT NULL,
    exchange            TEXT NOT NULL,
    pair                TEXT NOT NULL,
    asset               TEXT NOT NULL,
    action              TEXT NOT NULL,
    side                TEXT NOT NULL,
    order_type          TEXT NOT NULL,
    quantity            TEXT NOT NULL,
    price               TEXT,
    reduce_only         INTEGER NOT NULL,
    client_order_id     TEXT NOT NULL UNIQUE,
    notional_usd        TEXT NOT NULL,
    envelope_hash       TEXT NOT NULL,
    pair_epoch          INTEGER NOT NULL,
    status              TEXT NOT NULL,
    internal_order_id   TEXT,
    exchange_order_id   TEXT,
    expires_at          timestamp NOT NULL,
    created_at          timestamp NOT NULL,
    updated_at          timestamp NOT NULL,
    FOREIGN KEY(decision_run_id) REFERENCES llm_decision_runs(id),
    FOREIGN KEY(risk_evaluation_id) REFERENCES llm_risk_evaluations(id),
    FOREIGN KEY(approval_id) REFERENCES llm_strategy_approvals(id)
);

CREATE TABLE IF NOT EXISTS llm_protective_exits
(
    id                  TEXT NOT NULL PRIMARY KEY,
    parent_intent_id    TEXT NOT NULL,
    exit_kind           TEXT NOT NULL,
    client_order_id     TEXT NOT NULL UNIQUE,
    quantity            TEXT NOT NULL,
    trigger_price       TEXT NOT NULL,
    status              TEXT NOT NULL,
    fail_safe_triggered INTEGER NOT NULL DEFAULT 0,
    internal_order_id   TEXT,
    exchange_order_id   TEXT,
    created_at          timestamp NOT NULL,
    updated_at          timestamp NOT NULL,
    FOREIGN KEY(parent_intent_id) REFERENCES llm_order_intents(id)
);

-- +goose Down
DROP TABLE llm_protective_exits;
DROP TABLE llm_order_intents;
DROP TABLE llm_risk_evaluations;
