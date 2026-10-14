-- +goose Up
CREATE TABLE IF NOT EXISTS llm_strategies
(
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    created_by  TEXT NOT NULL,
    archived_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS llm_strategy_versions
(
    id                   TEXT PRIMARY KEY,
    strategy_id          TEXT NOT NULL REFERENCES llm_strategies(id),
    parent_version_id    TEXT,
    state                TEXT NOT NULL,
    source_prompt_cipher BYTEA,
    source_prompt_sha256 TEXT NOT NULL,
    proposal_json        TEXT,
    frozen_json          TEXT,
    strategy_hash        TEXT,
    risk_envelope_hash   TEXT,
    clarifying_questions TEXT,
    proposal_state       TEXT,
    created_at           TIMESTAMPTZ NOT NULL,
    frozen_at            TIMESTAMPTZ,
    UNIQUE (strategy_id, created_at)
);

CREATE TABLE IF NOT EXISTS llm_strategy_approvals
(
    id                      TEXT PRIMARY KEY,
    strategy_version_id     TEXT NOT NULL UNIQUE REFERENCES llm_strategy_versions(id),
    account_id              TEXT NOT NULL,
    network                 TEXT NOT NULL,
    execution_mode          TEXT NOT NULL,
    approved_market_ids     TEXT NOT NULL,
    risk_envelope_json      TEXT NOT NULL,
    risk_envelope_hash      TEXT NOT NULL,
    pair_epoch              INTEGER NOT NULL,
    llm_profile_revision_id TEXT NOT NULL,
    jev_profile_revision_id TEXT NOT NULL,
    valid_from              TIMESTAMPTZ NOT NULL,
    valid_until             TIMESTAMPTZ NOT NULL,
    approved_by             TEXT NOT NULL,
    approved_at             TIMESTAMPTZ NOT NULL,
    revoked_at              TIMESTAMPTZ,
    revoked_by              TEXT,
    revoke_reason           TEXT,
    expired_at              TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS llm_market_trade_grants
(
    id            TEXT PRIMARY KEY,
    account_id    TEXT NOT NULL,
    network       TEXT NOT NULL,
    market_id     TEXT NOT NULL,
    granted_by    TEXT NOT NULL,
    granted_at    TIMESTAMPTZ NOT NULL,
    valid_until   TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ,
    revoked_by    TEXT,
    revoke_reason TEXT
);

CREATE TABLE IF NOT EXISTS llm_strategy_state_transitions
(
    id                  TEXT PRIMARY KEY,
    strategy_version_id TEXT NOT NULL REFERENCES llm_strategy_versions(id),
    from_state          TEXT NOT NULL,
    to_state            TEXT NOT NULL,
    reason              TEXT NOT NULL,
    actor               TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE llm_strategy_state_transitions;
DROP TABLE llm_market_trade_grants;
DROP TABLE llm_strategy_approvals;
DROP TABLE llm_strategy_versions;
DROP TABLE llm_strategies;
