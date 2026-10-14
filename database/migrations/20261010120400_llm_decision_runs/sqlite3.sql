-- +goose Up
-- Immutability triggers for fact tables (no UPDATE/DELETE) are created by a separate
-- later migration; this one only creates the table.
CREATE TABLE IF NOT EXISTS llm_decision_runs
(
    id                        TEXT NOT NULL PRIMARY KEY,
    strategy_version_id       TEXT,
    snapshot_id               TEXT,
    trigger                   TEXT NOT NULL,
    run_mode                  TEXT NOT NULL,
    llm_profile_revision_id   TEXT NOT NULL,
    jev_profile_revision_id   TEXT,
    pair_epoch                INTEGER NOT NULL,
    llm_model                 TEXT NOT NULL,
    jev_model                 TEXT,
    prompt_version            TEXT NOT NULL,
    question_template_version TEXT,
    candidate_json            TEXT,
    jev_json                  TEXT,
    jev_manifest_state        TEXT,
    tool_transcript_json      TEXT,
    tool_call_count           INTEGER NOT NULL,
    llm_round_count           INTEGER NOT NULL,
    usage_input_tokens        INTEGER NOT NULL,
    usage_output_tokens       INTEGER NOT NULL,
    jev_input_tokens          INTEGER,
    jev_output_tokens         INTEGER,
    latency_ms                INTEGER NOT NULL,
    deadline_at               timestamp,
    decision                  TEXT NOT NULL,
    reason                    TEXT NOT NULL,
    analysis_safety           TEXT,
    analysis_safety_reason    TEXT,
    input_hash                TEXT NOT NULL,
    created_at                timestamp NOT NULL,
    FOREIGN KEY(strategy_version_id) REFERENCES llm_strategy_versions(id),
    FOREIGN KEY(snapshot_id) REFERENCES llm_market_snapshots(id)
);

-- +goose Down
DROP TABLE llm_decision_runs;
