-- +goose Up
UPDATE user_settings
SET theme_seed = '#2563EB',
    theme_variant = 'tonal_spot'
WHERE theme_seed = '#3A4527'
  AND theme_variant = 'fidelity';

-- +goose Down
-- This data refresh cannot be reversed without overwriting users who explicitly
-- chose the new default pair or customized their theme after the migration.
SELECT 1;
