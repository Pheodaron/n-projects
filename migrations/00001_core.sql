-- +goose Up
-- IF NOT EXISTS preserves data from Core installations created before Goose.
CREATE TABLE IF NOT EXISTS sessions (
    id text PRIMARY KEY,
    owner_id text NOT NULL,
    workspace_id text NOT NULL,
    data jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS runs (
    id text PRIMARY KEY,
    session_id text NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    owner_id text NOT NULL,
    workspace_id text NOT NULL,
    data jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS receipts (
    key text PRIMARY KEY,
    hash text NOT NULL,
    result jsonb NOT NULL
);

-- +goose Down
DROP TABLE receipts;
DROP TABLE runs;
DROP TABLE sessions;
