-- +goose Up
CREATE TABLE IF NOT EXISTS llm_budget_reservations
(
    id                  TEXT NOT NULL PRIMARY KEY,
    profile_revision_id TEXT NOT NULL,
    kind                TEXT NOT NULL,
    purpose             TEXT NOT NULL,
    decision_run_id     TEXT,
    status              TEXT NOT NULL,
    reserved_at         timestamp NOT NULL,
    expires_at          timestamp NOT NULL,
    completed_at        timestamp,
    input_tokens        INTEGER,
    output_tokens       INTEGER,
    latency_ms          INTEGER,
    FOREIGN KEY(profile_revision_id) REFERENCES llm_provider_profile_revisions(id),
    FOREIGN KEY(decision_run_id) REFERENCES llm_decision_runs(id)
);
CREATE INDEX idx_llm_budget_window
    ON llm_budget_reservations (profile_revision_id, reserved_at, status);

CREATE TABLE IF NOT EXISTS llm_new_risk_counters
(
    id             TEXT NOT NULL PRIMARY KEY,
    account_id     TEXT NOT NULL,
    scope          TEXT NOT NULL,
    scope_id       TEXT NOT NULL,
    intent_id      TEXT NOT NULL,
    counted_at     timestamp NOT NULL,
    window_minutes INTEGER NOT NULL,
    released_at    timestamp,
    UNIQUE(scope, scope_id, intent_id),
    FOREIGN KEY(intent_id) REFERENCES llm_order_intents(id)
);
CREATE INDEX idx_llm_new_risk_window
    ON llm_new_risk_counters (account_id, counted_at);

-- +goose Down
DROP INDEX idx_llm_new_risk_window;
DROP TABLE llm_new_risk_counters;
DROP INDEX idx_llm_budget_window;
DROP TABLE llm_budget_reservations;
