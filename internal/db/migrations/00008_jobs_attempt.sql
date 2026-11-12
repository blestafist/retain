-- +goose Up
ALTER TABLE jobs ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE jobs DROP COLUMN attempt;
