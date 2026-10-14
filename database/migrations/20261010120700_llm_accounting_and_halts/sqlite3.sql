-- +goose Up
CREATE TABLE IF NOT EXISTS llm_day_start_inventory
(
    id             TEXT NOT NULL PRIMARY KEY,
    account_id     TEXT NOT NULL,
    utc_day        TEXT NOT NULL,
    equity_usdt    TEXT NOT NULL,
    balance_usdt   TEXT NOT NULL,
    unrealised_pnl TEXT NOT NULL,
    positions_json TEXT NOT NULL,
    source         TEXT NOT NULL,
    observed_at    timestamp NOT NULL,
    sealed_at      timestamp NOT NULL,
    UNIQUE(account_id, utc_day)
);

CREATE TABLE IF NOT EXISTS llm_external_flows
(
    id          TEXT NOT NULL PRIMARY KEY,
    account_id  TEXT NOT NULL,
    utc_day     TEXT NOT NULL,
    direction   TEXT NOT NULL,
    amount_usdt TEXT NOT NULL,
    source      TEXT NOT NULL,
    observed_at timestamp NOT NULL,
    UNIQUE(account_id, utc_day, observed_at, amount_usdt)
);

CREATE TABLE IF NOT EXISTS llm_reconciliations
(
    id                      TEXT NOT NULL PRIMARY KEY,
    account_id              TEXT NOT NULL,
    started_at              timestamp NOT NULL,
    completed_at            timestamp,
    status                  TEXT NOT NULL,
    local_positions_json    TEXT NOT NULL,
    exchange_positions_json TEXT NOT NULL,
    local_orders_json       TEXT NOT NULL,
    exchange_orders_json    TEXT NOT NULL,
    deltas_json             TEXT,
    reconciled              INTEGER NOT NULL DEFAULT 0,
    valid_until             timestamp
);

CREATE TABLE IF NOT EXISTS llm_halts
(
    id                  TEXT NOT NULL PRIMARY KEY,
    level               TEXT NOT NULL,
    market_id           TEXT,
    reason              TEXT NOT NULL,
    set_by              TEXT NOT NULL,
    set_at              timestamp NOT NULL,
    cleared_by          TEXT,
    cleared_at          timestamp,
    clear_reason        TEXT,
    affected_strategies INTEGER NOT NULL,
    canceled_orders     INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS llm_audit_events
(
    id          TEXT NOT NULL PRIMARY KEY,
    kind        TEXT NOT NULL,
    actor       TEXT NOT NULL,
    subject     TEXT,
    detail_json TEXT NOT NULL,
    created_at  timestamp NOT NULL
);

-- +goose Down
DROP TABLE llm_audit_events;
DROP TABLE llm_halts;
DROP TABLE llm_reconciliations;
DROP TABLE llm_external_flows;
DROP TABLE llm_day_start_inventory;
