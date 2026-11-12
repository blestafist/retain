-- +goose Up
CREATE TABLE app_settings (
    key TEXT PRIMARY KEY NOT NULL CHECK (length(trim(key)) > 0),
    value TEXT NOT NULL CHECK (json_valid(value)),
    updated_at DATETIME NOT NULL
);

CREATE TABLE llm_cache (
    key BLOB PRIMARY KEY NOT NULL CHECK (length(key) = 32),
    model TEXT NOT NULL CHECK (length(trim(model)) > 0),
    prompt_version INTEGER NOT NULL CHECK (prompt_version > 0),
    response TEXT NOT NULL CHECK (json_valid(response)),
    created_at DATETIME NOT NULL
);

CREATE TABLE llm_usage (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (length(trim(kind)) > 0),
    model TEXT NOT NULL CHECK (length(trim(model)) > 0),
    prompt_tokens INTEGER NOT NULL DEFAULT 0 CHECK (prompt_tokens >= 0),
    completion_tokens INTEGER NOT NULL DEFAULT 0 CHECK (completion_tokens >= 0),
    cached BOOLEAN NOT NULL DEFAULT 0 CHECK (cached IN (0, 1)),
    created_at DATETIME NOT NULL
);

CREATE INDEX llm_usage_user_created_idx ON llm_usage(user_id, created_at, id);

CREATE TABLE jobs (
    id TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (length(trim(kind)) > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed')),
    progress INTEGER NOT NULL DEFAULT 0 CHECK (progress >= 0),
    total INTEGER NOT NULL DEFAULT 0 CHECK (total >= 0 AND progress <= total),
    error TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE INDEX jobs_user_updated_idx ON jobs(user_id, updated_at, id);
CREATE INDEX jobs_active_queue_idx ON jobs(status, user_id, kind) WHERE status IN ('queued', 'running');

-- Admission is checked inside the same SQLite write transaction as insertion,
-- preventing concurrent submitters from exceeding per-user/global queue limits.
-- +goose StatementBegin
CREATE TRIGGER jobs_active_queue_capacity
BEFORE INSERT ON jobs
WHEN NEW.status IN ('queued', 'running') AND (
    (SELECT COUNT(*) FROM jobs WHERE status IN ('queued', 'running')) >= 100
    OR (SELECT COUNT(*) FROM jobs
        WHERE user_id = NEW.user_id AND status IN ('queued', 'running')) >= 3
    OR (NEW.kind = 'deck_enrich' AND EXISTS (
        SELECT 1 FROM jobs AS active
        WHERE active.user_id = NEW.user_id
          AND active.kind = 'deck_enrich'
          AND active.status IN ('queued', 'running')
          AND json_extract(active.payload, '$.deck_id') = json_extract(NEW.payload, '$.deck_id')
    ))
)
BEGIN
    SELECT RAISE(IGNORE);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER jobs_active_queue_capacity;
DROP INDEX jobs_active_queue_idx;
DROP INDEX jobs_user_updated_idx;
DROP TABLE jobs;
DROP INDEX llm_usage_user_created_idx;
DROP TABLE llm_usage;
DROP TABLE llm_cache;
DROP TABLE app_settings;
