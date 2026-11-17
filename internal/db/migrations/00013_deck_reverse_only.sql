-- +goose Up
-- reverse_only hides forward cards (term -> translation) from study without deleting them.
ALTER TABLE decks ADD COLUMN reverse_only BOOLEAN NOT NULL DEFAULT 0 CHECK (reverse_only IN (0, 1));

-- +goose Down
ALTER TABLE decks DROP COLUMN reverse_only;
