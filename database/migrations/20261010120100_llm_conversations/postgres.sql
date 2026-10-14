-- +goose Up
CREATE TABLE IF NOT EXISTS llm_conversations
(
    id            TEXT PRIMARY KEY,
    title         TEXT NOT NULL,
    kind          TEXT NOT NULL,
    strategy_id   TEXT,
    market_scope  TEXT,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    archived_at   TIMESTAMPTZ,
    message_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS llm_chat_messages
(
    id                  TEXT PRIMARY KEY,
    conversation_id     TEXT NOT NULL REFERENCES llm_conversations(id),
    seq                 INTEGER NOT NULL,
    role                TEXT NOT NULL,
    content_cipher      BYTEA,
    content_nonce       BYTEA,
    content_sha256      TEXT NOT NULL,
    char_count          INTEGER NOT NULL,
    llm_profile_rev_id  TEXT,
    llm_model           TEXT,
    pair_epoch          INTEGER,
    prompt_version      TEXT,
    usage_input_tokens  INTEGER,
    usage_output_tokens INTEGER,
    latency_ms          INTEGER,
    tool_transcript     TEXT,
    error_code          TEXT,
    created_at          TIMESTAMPTZ NOT NULL,
    UNIQUE (conversation_id, seq)
);

-- +goose Down
DROP TABLE llm_chat_messages;
DROP TABLE llm_conversations;
