-- +goose Up
CREATE TABLE IF NOT EXISTS llm_budget_reservations
(
    id                  TEXT PRIMARY KEY,
    profile_revision_id TEXT NOT NULL REFERENCES llm_provider_profile_revisions(id),
    kind                TEXT NOT NULL,
    purpose             TEXT NOT NULL,
    decision_run_id     TEXT REFERENCES llm_decision_runs(id),
    status              TEXT NOT NULL,
    reserved_at         TIMESTAMPTZ NOT NULL,
    expires_at          TIMESTAMPTZ NOT NULL,
    completed_at        TIMESTAMPTZ,
    input_tokens        INTEGER,
    output_tokens       INTEGER,
    latency_ms          INTEGER
);
CREATE INDEX idx_llm_budget_window
    ON llm_budget_reservations (profile_revision_id, reserved_at, status);

CREATE TABLE IF NOT EXISTS llm_new_risk_counters
(
    id             TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL,
    scope          TEXT NOT NULL,
    scope_id       TEXT NOT NULL,
    intent_id      TEXT NOT NULL REFERENCES llm_order_intents(id),
    counted_at     TIMESTAMPTZ NOT NULL,
    window_minutes INTEGER NOT NULL,
    released_at    TIMESTAMPTZ,
    UNIQUE (scope, scope_id, intent_id)
);
CREATE INDEX idx_llm_new_risk_window
    ON llm_new_risk_counters (account_id, counted_at);

-- +goose Down
DROP INDEX idx_llm_new_risk_window;
DROP TABLE llm_new_risk_counters;
DROP INDEX idx_llm_budget_window;
DROP TABLE llm_budget_reservations;
