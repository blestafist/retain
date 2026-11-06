-- +goose Up
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    email TEXT NOT NULL COLLATE NOCASE UNIQUE
        CHECK (length(trim(email)) > 0),
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    password_hash TEXT NOT NULL CHECK (length(password_hash) > 0),
    is_admin BOOLEAN NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE invites (
    code TEXT PRIMARY KEY NOT NULL CHECK (length(trim(code)) > 0),
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    used_by INTEGER REFERENCES users(id) ON DELETE RESTRICT,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE sessions (
    token_hash BLOB PRIMARY KEY NOT NULL,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at DATETIME NOT NULL,
    user_agent TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

CREATE TABLE user_settings (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    new_per_day INTEGER NOT NULL DEFAULT 20 CHECK (new_per_day >= 0),
    max_reviews_per_day INTEGER NOT NULL DEFAULT 200 CHECK (max_reviews_per_day >= 0),
    request_retention REAL NOT NULL DEFAULT 0.9 CHECK (request_retention > 0 AND request_retention <= 1),
    maximum_interval INTEGER NOT NULL DEFAULT 36500 CHECK (maximum_interval > 0),
    learning_steps TEXT NOT NULL DEFAULT '1m 10m' CHECK (length(trim(learning_steps)) > 0),
    relearning_steps TEXT NOT NULL DEFAULT '10m' CHECK (length(trim(relearning_steps)) > 0),
    fsrs_weights TEXT CHECK (fsrs_weights IS NULL OR json_valid(fsrs_weights)),
    timezone TEXT NOT NULL DEFAULT 'UTC' CHECK (length(trim(timezone)) > 0),
    day_start_hour INTEGER NOT NULL DEFAULT 4 CHECK (day_start_hour BETWEEN 0 AND 23),
    reminder_time TEXT,
    tts_auto BOOLEAN NOT NULL DEFAULT 0 CHECK (tts_auto IN (0, 1)),
    theme_mode TEXT NOT NULL DEFAULT 'system' CHECK (theme_mode IN ('light', 'dark', 'system')),
    theme_seed TEXT NOT NULL DEFAULT '#3A4527'
        CHECK (theme_seed GLOB '#[0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f]'),
    theme_variant TEXT NOT NULL DEFAULT 'fidelity'
        CHECK (theme_variant IN ('fidelity', 'tonal_spot', 'vibrant', 'expressive', 'monochrome')),
    theme_contrast REAL NOT NULL DEFAULT 0 CHECK (theme_contrast BETWEEN 0 AND 1)
);

-- +goose Down
DROP TABLE user_settings;
DROP INDEX sessions_expires_at_idx;
DROP TABLE sessions;
DROP TABLE invites;
DROP TABLE users;
