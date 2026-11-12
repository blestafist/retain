-- name: GetAppSetting :one
SELECT * FROM app_settings
WHERE key = ?;

-- name: SetAppSetting :one
INSERT INTO app_settings (key, value, updated_at)
VALUES (sqlc.arg(key), sqlc.arg(value), sqlc.arg(updated_at))
ON CONFLICT(key) DO UPDATE
SET value = excluded.value,
    updated_at = excluded.updated_at
RETURNING *;

-- name: SetAppSettingIfAbsent :execrows
INSERT INTO app_settings (key, value, updated_at)
VALUES (sqlc.arg(key), sqlc.arg(value), sqlc.arg(updated_at))
ON CONFLICT(key) DO NOTHING;

-- Reserve one non-cached provider request atomically against the caller's UTC
-- time bounds. The inserted row is the reservation and exists before the call.
-- name: ReserveLLMUsage :one
INSERT INTO llm_usage (
    user_id, kind, model, prompt_tokens, completion_tokens, cached, created_at
)
SELECT sqlc.arg(user_id), sqlc.arg(kind), sqlc.arg(model), 0, 0, 0, sqlc.arg(created_at)
WHERE (
    SELECT COUNT(*) FROM llm_usage AS usage
    WHERE usage.user_id = sqlc.arg(user_id)
      AND usage.cached = 0
      AND usage.created_at >= sqlc.arg(day_start)
      AND usage.created_at < sqlc.arg(day_end)
) < sqlc.arg(daily_limit)
RETURNING *;

-- name: RecordCachedLLMUsage :one
INSERT INTO llm_usage (
    user_id, kind, model, prompt_tokens, completion_tokens, cached, created_at
)
VALUES (sqlc.arg(user_id), sqlc.arg(kind), sqlc.arg(model), 0, 0, 1, sqlc.arg(created_at))
RETURNING *;

-- name: CompleteLLMUsage :one
UPDATE llm_usage
SET prompt_tokens = sqlc.arg(prompt_tokens),
    completion_tokens = sqlc.arg(completion_tokens)
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
  AND cached = 0
RETURNING *;

-- name: ListLLMUsage :many
SELECT * FROM llm_usage
WHERE user_id = sqlc.arg(user_id)
  AND created_at >= sqlc.arg(start_at)
  AND created_at < sqlc.arg(end_at)
ORDER BY created_at, id;

-- name: AggregateLLMUsage :many
SELECT
    usage.user_id,
    users.name AS user_name,
    users.email AS user_email,
    usage.model,
    usage.kind AS feature,
    COUNT(*) AS requests,
    COUNT(CASE WHEN usage.cached THEN 1 END) AS cached_requests,
    CAST(SUM(usage.prompt_tokens) AS INTEGER) AS prompt_tokens,
    CAST(SUM(usage.completion_tokens) AS INTEGER) AS completion_tokens
FROM llm_usage AS usage
JOIN users ON users.id = usage.user_id
GROUP BY usage.user_id, users.name, users.email, usage.model, usage.kind
ORDER BY users.name COLLATE NOCASE, usage.user_id, usage.model COLLATE BINARY, usage.kind COLLATE BINARY;

-- name: GetLLMCache :one
SELECT * FROM llm_cache
WHERE key = ?;

-- name: SetLLMCache :one
INSERT INTO llm_cache (key, model, prompt_version, response, created_at)
VALUES (sqlc.arg(key), sqlc.arg(model), sqlc.arg(prompt_version), sqlc.arg(response), sqlc.arg(created_at))
ON CONFLICT(key) DO UPDATE
SET model = excluded.model,
    prompt_version = excluded.prompt_version,
    response = excluded.response,
    created_at = excluded.created_at
RETURNING *;

-- name: CreateJob :one
INSERT INTO jobs (id, user_id, kind, payload, status, progress, total, error, created_at, updated_at)
VALUES (
    sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(kind), sqlc.arg(payload), 'queued',
    0, sqlc.arg(total), NULL, sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetJob :one
SELECT * FROM jobs
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- Claim is a single conditional update, so only one worker can transition a
-- queued job to running.
-- name: ClaimJob :one
UPDATE jobs
SET status = 'running',
    attempt = attempt + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
  AND status = 'queued'
RETURNING *;

-- Claim the oldest queued job atomically for an in-process worker. The
-- conditional status check keeps concurrent workers from claiming one row.
-- name: ClaimNextJob :one
UPDATE jobs
SET status = 'running',
    attempt = attempt + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = (
    SELECT id FROM jobs
    WHERE status = 'queued'
    ORDER BY created_at, id
    LIMIT 1
)
  AND status = 'queued'
RETURNING *;

-- A process restart makes every running job eligible for another claim.
-- name: RequeueRunningJobs :execrows
UPDATE jobs
SET status = 'queued',
    updated_at = sqlc.arg(updated_at)
WHERE status = 'running';

-- Graceful shutdown returns only the worker's claimed row to the queue.
-- name: RequeueJob :execrows
UPDATE jobs
SET status = 'queued',
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND attempt = sqlc.arg(attempt)
  AND status = 'running';

-- name: UpdateJob :one
UPDATE jobs
SET status = sqlc.arg(status),
    progress = sqlc.arg(progress),
    total = sqlc.arg(total),
    error = sqlc.narg(error),
    result = CASE WHEN sqlc.arg(status) = 'done' THEN sqlc.narg(result) ELSE NULL END,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
  AND attempt = sqlc.arg(attempt)
  AND status = 'running'
  AND progress <= sqlc.arg(progress)
  AND sqlc.arg(status) != 'queued'
RETURNING *;
