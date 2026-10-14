-- +goose Up
CREATE TABLE IF NOT EXISTS llm_conversations
(
    id            TEXT NOT NULL PRIMARY KEY,
    title         TEXT NOT NULL,
    kind          TEXT NOT NULL,
    strategy_id   TEXT,
    market_scope  TEXT,
    created_at    timestamp NOT NULL,
    updated_at    timestamp NOT NULL,
    archived_at   timestamp,
    message_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS llm_chat_messages
(
    id                  TEXT NOT NULL PRIMARY KEY,
    conversation_id     TEXT NOT NULL,
    seq                 INTEGER NOT NULL,
    role                TEXT NOT NULL,
    content_cipher      BLOB,
    content_nonce       BLOB,
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
    created_at          timestamp NOT NULL,
    UNIQUE(conversation_id, seq),
    FOREIGN KEY(conversation_id) REFERENCES llm_conversations(id)
);

-- +goose Down
DROP TABLE llm_chat_messages;
DROP TABLE llm_conversations;
