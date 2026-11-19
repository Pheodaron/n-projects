-- +goose Up
CREATE TABLE agent_sessions (
    id text PRIMARY KEY,
    title text,
    created_at timestamptz NOT NULL,
    last_activity_at timestamptz
);

CREATE INDEX agent_sessions_activity_idx
    ON agent_sessions (last_activity_at DESC, id DESC) WHERE last_activity_at IS NOT NULL;

CREATE TABLE agent_messages (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    session_id text NOT NULL REFERENCES agent_sessions (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    payload json NOT NULL
);

CREATE INDEX agent_messages_session_id_id_idx ON agent_messages (session_id, id);

-- +goose Down
DROP TABLE agent_messages;
DROP TABLE agent_sessions;
