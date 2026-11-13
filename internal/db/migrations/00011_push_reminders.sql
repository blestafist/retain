-- +goose Up
CREATE TABLE push_subscriptions (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint TEXT NOT NULL UNIQUE,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (length(endpoint) BETWEEN 1 AND 4096),
    CHECK (length(p256dh) BETWEEN 1 AND 256),
    CHECK (length(auth) BETWEEN 1 AND 256)
);

CREATE INDEX push_subscriptions_user_idx ON push_subscriptions(user_id, id);

-- The date is local to each user's selected time zone. The unique key makes
-- the daily notification batch idempotent across worker restarts.
CREATE TABLE push_reminder_sends (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reminder_day TEXT NOT NULL,
    attempted_at DATETIME NOT NULL,
    PRIMARY KEY(user_id, reminder_day),
    CHECK (length(reminder_day) = 10)
);

-- +goose Down
DROP TABLE push_reminder_sends;
DROP INDEX push_subscriptions_user_idx;
DROP TABLE push_subscriptions;
