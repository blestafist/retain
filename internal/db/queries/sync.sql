-- name: GetSyncCursorKey :one
SELECT value FROM app_settings WHERE key = 'sync_cursor_hmac_key';

-- name: GetSyncOwnerScope :one
SELECT scope_id FROM sync_owner_scopes WHERE user_id = sqlc.arg(user_id);

-- name: InsertSyncCursorKey :exec
INSERT INTO app_settings(key, value, updated_at)
VALUES ('sync_cursor_hmac_key', sqlc.arg(value), CURRENT_TIMESTAMP)
ON CONFLICT(key) DO NOTHING;

-- name: ListSyncEvents :many
SELECT seq, entity_type, entity_id, operation, payload
FROM sync_events
WHERE owner_id = sqlc.arg(owner_id)
  AND owner_scope_id = sqlc.arg(owner_scope_id)
  AND seq > sqlc.arg(after_seq)
ORDER BY seq
LIMIT sqlc.arg(page_size);

-- name: GetSyncCardSnapshot :one
SELECT cards.*,
       CAST(COALESCE((
           SELECT MAX(seq)
           FROM sync_events
           WHERE owner_id = cards.user_id
             AND owner_scope_id = sqlc.arg(owner_scope_id)
             AND entity_type = 'card'
             AND entity_id = cards.id
       ), 0) AS INTEGER) AS sync_seq
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.id = sqlc.arg(card_id)
  AND cards.user_id = sqlc.arg(owner_id)
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
  AND decks.owner_id = cards.user_id;
