-- +goose Up
ALTER TABLE jobs ADD COLUMN result TEXT CHECK (result IS NULL OR json_valid(result));

-- +goose Down
ALTER TABLE jobs DROP COLUMN result;
