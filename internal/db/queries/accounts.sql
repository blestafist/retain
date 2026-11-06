-- name: CreateUser :one
INSERT INTO users (email, name, password_hash, is_admin, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = ?;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = ?;

-- name: CountAdmins :one
SELECT COUNT(*) FROM users
WHERE is_admin = 1;

-- name: CreateInvite :one
INSERT INTO invites (code, created_by, expires_at, created_at)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GetInvite :one
SELECT * FROM invites
WHERE code = ?;

-- name: ConsumeInvite :one
UPDATE invites
SET used_by = CAST(sqlc.arg(used_by) AS INTEGER)
WHERE code = sqlc.arg(code)
  AND used_by IS NULL
  AND expires_at > sqlc.arg(now)
RETURNING *;

-- name: CreateSession :one
INSERT INTO sessions (token_hash, user_id, expires_at, user_agent, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetSession :one
SELECT * FROM sessions
WHERE token_hash = ?;

-- name: DeleteSession :execrows
DELETE FROM sessions
WHERE token_hash = ?;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions
WHERE expires_at <= ?;

-- name: CreateUserSettings :one
INSERT INTO user_settings (user_id)
VALUES (?)
RETURNING *;

-- name: GetUserSettings :one
SELECT * FROM user_settings
WHERE user_id = ?;

-- name: UpdateUserSettings :one
UPDATE user_settings
SET new_per_day = ?,
    max_reviews_per_day = ?,
    request_retention = ?,
    maximum_interval = ?,
    learning_steps = ?,
    relearning_steps = ?,
    fsrs_weights = ?,
    timezone = ?,
    day_start_hour = ?,
    reminder_time = ?,
    tts_auto = ?,
    theme_mode = ?,
    theme_seed = ?,
    theme_variant = ?,
    theme_contrast = ?
WHERE user_id = ?
RETURNING *;
