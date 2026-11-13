-- name: UpsertPushSubscription :one
INSERT INTO push_subscriptions(user_id, endpoint, p256dh, auth)
VALUES (sqlc.arg(user_id), sqlc.arg(endpoint), sqlc.arg(p256dh), sqlc.arg(auth))
ON CONFLICT(endpoint) DO UPDATE SET
    p256dh = excluded.p256dh,
    auth = excluded.auth
WHERE push_subscriptions.user_id = excluded.user_id
RETURNING id, user_id, endpoint, p256dh, auth, created_at;

-- name: DeletePushSubscription :execrows
DELETE FROM push_subscriptions
WHERE user_id = sqlc.arg(user_id) AND endpoint = sqlc.arg(endpoint);

-- name: ListPushSubscriptionsByUser :many
SELECT id, user_id, endpoint, p256dh, auth, created_at
FROM push_subscriptions
WHERE user_id = sqlc.arg(user_id)
ORDER BY id;

-- name: DeletePushSubscriptionByID :execrows
DELETE FROM push_subscriptions WHERE id = sqlc.arg(id);

-- name: ListReminderCandidates :many
SELECT settings.user_id, settings.timezone, settings.reminder_time,
       CAST((
           SELECT COUNT(*)
           FROM cards
           JOIN notes ON notes.id = cards.note_id
           JOIN decks ON decks.id = notes.deck_id
           WHERE cards.user_id = settings.user_id
             AND cards.state <> 0
             AND cards.suspended = 0
             AND cards.due <= sqlc.arg(now_at)
             AND cards.deleted_at IS NULL
             AND notes.deleted_at IS NULL
             AND decks.deleted_at IS NULL
             AND (
                 decks.owner_id = cards.user_id
                 OR EXISTS (
                     SELECT 1 FROM social_subscription_tree AS access
                     WHERE access.recipient_id = cards.user_id
                       AND access.deck_id = decks.id
                       AND access.share_status = 'active'
                 )
             )
       ) AS INTEGER) AS due_count
FROM user_settings AS settings
WHERE settings.reminder_time IS NOT NULL
  AND EXISTS (SELECT 1 FROM push_subscriptions WHERE user_id = settings.user_id)
ORDER BY settings.user_id;

-- name: ClaimReminderDay :execrows
INSERT OR IGNORE INTO push_reminder_sends(user_id, reminder_day, attempted_at)
VALUES (sqlc.arg(user_id), sqlc.arg(reminder_day), sqlc.arg(attempted_at));
