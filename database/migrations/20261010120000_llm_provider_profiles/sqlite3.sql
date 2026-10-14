-- +goose Up
-- SQLite has no BOOLEAN or TIMESTAMPTZ: flags are stored as INTEGER 0/1 and timestamps
-- as ISO-8601 UTC strings, which is exactly what the Go layer writes for both dialects.
CREATE TABLE IF NOT EXISTS llm_provider_profiles
(
    id            TEXT NOT NULL PRIMARY KEY,
    kind          TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 0,
    created_at    timestamp NOT NULL,
    created_by    TEXT NOT NULL,
    archived_at   timestamp,
    UNIQUE(kind, display_name)
);

CREATE TABLE IF NOT EXISTS llm_provider_profile_revisions
(
    id                        TEXT NOT NULL PRIMARY KEY,
    profile_id                TEXT NOT NULL,
    revision_no               INTEGER NOT NULL,
    kind                      TEXT NOT NULL,
    base_url                  TEXT NOT NULL,
    model_id                  TEXT NOT NULL,
    api_key_ref               TEXT NOT NULL,
    api_key_fingerprint       TEXT NOT NULL,
    timeout_seconds           INTEGER NOT NULL,
    max_output_tokens         INTEGER,
    max_tool_rounds           INTEGER,
    max_tool_calls            INTEGER,
    max_cumulative_tokens     INTEGER,
    prompt_version            TEXT,
    question_template_version TEXT,
    daily_budget_usd          TEXT,
    reservation_cap_24h       INTEGER NOT NULL,
    allow_insecure_localhost  INTEGER NOT NULL DEFAULT 0,
    proxy_address             TEXT,
    created_at                timestamp NOT NULL,
    created_by                TEXT NOT NULL,
    superseded_at             timestamp,
    UNIQUE(profile_id, revision_no),
    FOREIGN KEY(profile_id) REFERENCES llm_provider_profiles(id)
);

-- Single-row table: the CHECK constraint pins id to 1 so a second row cannot exist.
CREATE TABLE IF NOT EXISTS llm_active_model_pair
(
    id                       INTEGER PRIMARY KEY CHECK (id = 1),
    llm_profile_revision_id  TEXT,
    jev_profile_revision_id  TEXT,
    pair_epoch               INTEGER NOT NULL DEFAULT 0,
    readiness                TEXT NOT NULL,
    updated_at               timestamp NOT NULL,
    updated_by               TEXT NOT NULL,
    FOREIGN KEY(llm_profile_revision_id) REFERENCES llm_provider_profile_revisions(id),
    FOREIGN KEY(jev_profile_revision_id) REFERENCES llm_provider_profile_revisions(id)
);

CREATE TABLE IF NOT EXISTS llm_provider_probe_results
(
    id                  TEXT NOT NULL PRIMARY KEY,
    profile_revision_id TEXT NOT NULL,
    kind                TEXT NOT NULL,
    layer               TEXT NOT NULL,
    passed              INTEGER NOT NULL,
    error_code          TEXT,
    latency_ms          INTEGER,
    input_tokens        INTEGER,
    output_tokens       INTEGER,
    response_model      TEXT,
    request_sha256      TEXT,
    probed_at           timestamp NOT NULL,
    FOREIGN KEY(profile_revision_id) REFERENCES llm_provider_profile_revisions(id)
);

CREATE TABLE IF NOT EXISTS llm_pair_switch_commands
(
    id                  TEXT NOT NULL PRIMARY KEY,
    idempotency_key     TEXT NOT NULL UNIQUE,
    from_pair_epoch     INTEGER NOT NULL,
    to_pair_epoch       INTEGER NOT NULL,
    llm_revision_id     TEXT,
    jev_revision_id     TEXT,
    operator            TEXT NOT NULL,
    affected_strategies INTEGER NOT NULL,
    revoked_approvals   INTEGER NOT NULL,
    created_at          timestamp NOT NULL
);

-- +goose Down
DROP TABLE llm_pair_switch_commands;
DROP TABLE llm_provider_probe_results;
DROP TABLE llm_active_model_pair;
DROP TABLE llm_provider_profile_revisions;
DROP TABLE llm_provider_profiles;
