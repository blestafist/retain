-- +goose Up
-- A composite FK ensures a review cannot be recorded against another user's card.
CREATE UNIQUE INDEX cards_id_user_id_idx ON cards(id, user_id);

CREATE TABLE review_logs (
    id TEXT PRIMARY KEY NOT NULL CHECK (
        length(id) = 36 AND id = lower(id) AND
        substr(id, 9, 1) = '-' AND substr(id, 14, 1) = '-' AND
        substr(id, 19, 1) = '-' AND substr(id, 24, 1) = '-' AND
        substr(id, 15, 1) = '7' AND
        substr(id, 20, 1) IN ('8', '9', 'a', 'b') AND
        length(replace(id, '-', '')) = 32 AND
        id NOT GLOB '*[^0-9a-f-]*'
    ),
    card_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    rating INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 4),
    mode TEXT NOT NULL CHECK (mode IN ('flip', 'typing', 'choice')),
    state_before INTEGER NOT NULL CHECK (state_before BETWEEN 0 AND 3),
    stability_before REAL CHECK (stability_before IS NULL OR stability_before > 0),
    difficulty_before REAL CHECK (difficulty_before IS NULL OR difficulty_before BETWEEN 1 AND 10),
    stability_after REAL NOT NULL CHECK (stability_after > 0),
    difficulty_after REAL NOT NULL CHECK (difficulty_after BETWEEN 1 AND 10),
    scheduled_days INTEGER NOT NULL CHECK (scheduled_days >= 0),
    scheduler_parameters TEXT CHECK (
        scheduler_parameters IS NULL OR
        (length(scheduler_parameters) <= 4096 AND json_valid(scheduler_parameters))
    ),
    reviewed_at DATETIME NOT NULL,
    received_at DATETIME NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    undone BOOLEAN NOT NULL DEFAULT 0 CHECK (undone IN (0, 1)),
    CHECK ((stability_before IS NULL) = (difficulty_before IS NULL)),
    FOREIGN KEY (card_id, user_id) REFERENCES cards(id, user_id) ON DELETE RESTRICT
);

CREATE INDEX review_logs_user_reviewed_idx ON review_logs(user_id, reviewed_at);
CREATE INDEX review_logs_card_reviewed_idx ON review_logs(card_id, reviewed_at, id);

-- This mutable projection is rebuilt from active, immutable review events after
-- a review/undo so late offline events cannot leave analytics metadata stale.
CREATE TABLE review_log_projection (
    review_id TEXT PRIMARY KEY NOT NULL REFERENCES review_logs(id) ON DELETE CASCADE,
    card_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    state_before INTEGER NOT NULL CHECK (state_before BETWEEN 0 AND 3),
    stability_before REAL CHECK (stability_before IS NULL OR stability_before > 0),
    difficulty_before REAL CHECK (difficulty_before IS NULL OR difficulty_before BETWEEN 1 AND 10),
    stability_after REAL NOT NULL CHECK (stability_after > 0),
    difficulty_after REAL NOT NULL CHECK (difficulty_after BETWEEN 1 AND 10),
    scheduled_days INTEGER NOT NULL CHECK (scheduled_days >= 0),
    CHECK ((stability_before IS NULL) = (difficulty_before IS NULL)),
    FOREIGN KEY (card_id, user_id) REFERENCES cards(id, user_id) ON DELETE CASCADE
) WITHOUT ROWID;

CREATE INDEX review_log_projection_user_card_idx ON review_log_projection(user_id, card_id, review_id);

-- Seed the projection for direct inserts/imported fixtures; the study service
-- replaces it with sequence-derived values before its transaction commits.
-- +goose StatementBegin
CREATE TRIGGER review_log_projection_insert
AFTER INSERT ON review_logs
BEGIN
    INSERT INTO review_log_projection(
        review_id, card_id, user_id, state_before, stability_before, difficulty_before,
        stability_after, difficulty_after, scheduled_days
    ) VALUES (
        NEW.id, NEW.card_id, NEW.user_id, NEW.state_before, NEW.stability_before, NEW.difficulty_before,
        NEW.stability_after, NEW.difficulty_after, NEW.scheduled_days
    );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER review_log_projection_undo
AFTER UPDATE OF undone ON review_logs
WHEN OLD.undone = 0 AND NEW.undone = 1
BEGIN
    DELETE FROM review_log_projection WHERE review_id = NEW.id;
END;
-- +goose StatementEnd

-- The only allowed mutation is the one-way undo marker. In particular, neither a review nor an
-- undo can be removed or rewritten, so replay remains auditable.
-- +goose StatementBegin
CREATE TRIGGER review_logs_append_only_update
BEFORE UPDATE ON review_logs
WHEN OLD.undone <> 0 OR NEW.undone <> 1 OR
     NEW.id IS NOT OLD.id OR NEW.card_id IS NOT OLD.card_id OR
     NEW.user_id IS NOT OLD.user_id OR NEW.rating IS NOT OLD.rating OR
     NEW.mode IS NOT OLD.mode OR NEW.state_before IS NOT OLD.state_before OR
     NEW.stability_before IS NOT OLD.stability_before OR
     NEW.difficulty_before IS NOT OLD.difficulty_before OR
      NEW.stability_after IS NOT OLD.stability_after OR
      NEW.difficulty_after IS NOT OLD.difficulty_after OR
      NEW.scheduled_days IS NOT OLD.scheduled_days OR
      NEW.scheduler_parameters IS NOT OLD.scheduler_parameters OR
     NEW.reviewed_at IS NOT OLD.reviewed_at OR
     NEW.received_at IS NOT OLD.received_at OR
     NEW.duration_ms IS NOT OLD.duration_ms
BEGIN
    SELECT RAISE(ABORT, 'review logs are append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER review_logs_append_only_delete
BEFORE DELETE ON review_logs
BEGIN
    SELECT RAISE(ABORT, 'review logs are append-only');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER review_logs_append_only_delete;
DROP TRIGGER review_logs_append_only_update;
DROP TRIGGER review_log_projection_undo;
DROP TRIGGER review_log_projection_insert;
DROP INDEX review_log_projection_user_card_idx;
DROP TABLE review_log_projection;
DROP TABLE review_logs;
DROP INDEX cards_id_user_id_idx;
