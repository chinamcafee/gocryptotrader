-- +goose Up
CREATE TABLE IF NOT EXISTS llm_market_snapshots
(
    id                 TEXT PRIMARY KEY,
    exchange           TEXT NOT NULL,
    pair               TEXT NOT NULL,
    asset              TEXT NOT NULL,
    source_time        TIMESTAMPTZ NOT NULL,
    built_at           TIMESTAMPTZ NOT NULL,
    freshness_ms       INTEGER NOT NULL,
    quality_json       TEXT NOT NULL,
    candle_window_json TEXT NOT NULL,
    indicator_json     TEXT NOT NULL,
    ticker_json        TEXT NOT NULL,
    orderbook_json     TEXT NOT NULL,
    funding_json       TEXT NOT NULL,
    account_json       TEXT NOT NULL,
    total_bytes        INTEGER NOT NULL,
    content_sha256     TEXT NOT NULL
);

-- +goose Down
DROP TABLE llm_market_snapshots;
