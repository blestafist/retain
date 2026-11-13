-- name: GetFriendship :one
SELECT user_a_id, user_b_id, created_at
FROM friendships
WHERE user_a_id = sqlc.arg(user_a_id) AND user_b_id = sqlc.arg(user_b_id);

-- name: GetPendingFriendRequestPair :one
SELECT id, sender_id, recipient_id, status, created_at, updated_at
FROM friend_requests
WHERE status = 'pending'
  AND min(sender_id, recipient_id) = sqlc.arg(user_a_id)
  AND max(sender_id, recipient_id) = sqlc.arg(user_b_id);

-- name: CreateFriendRequest :one
INSERT INTO friend_requests(sender_id, recipient_id)
VALUES (sqlc.arg(sender_id), sqlc.arg(recipient_id))
RETURNING *;

-- name: ListFriendRequestsForUser :many
SELECT requests.id, requests.sender_id, requests.recipient_id, requests.status,
       requests.created_at, requests.updated_at,
       sender.name AS sender_name, sender.email AS sender_email,
       recipient.name AS recipient_name, recipient.email AS recipient_email
FROM friend_requests AS requests
JOIN users AS sender ON sender.id = requests.sender_id
JOIN users AS recipient ON recipient.id = requests.recipient_id
WHERE requests.status = 'pending'
  AND (requests.sender_id = sqlc.arg(user_id) OR requests.recipient_id = sqlc.arg(user_id))
ORDER BY requests.created_at DESC, requests.id DESC;

-- name: AcceptFriendRequest :one
UPDATE friend_requests
SET status = 'accepted', updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND recipient_id = sqlc.arg(user_id) AND status = 'pending'
RETURNING *;

-- name: DeclineFriendRequest :one
UPDATE friend_requests
SET status = 'declined', updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND recipient_id = sqlc.arg(user_id) AND status = 'pending'
RETURNING *;

-- name: CancelFriendRequest :one
UPDATE friend_requests
SET status = 'cancelled', updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND sender_id = sqlc.arg(user_id) AND status = 'pending'
RETURNING *;

-- name: CancelReversePendingFriendRequest :exec
UPDATE friend_requests
SET status = 'cancelled', updated_at = sqlc.arg(updated_at)
WHERE sender_id = sqlc.arg(user_b_id) AND recipient_id = sqlc.arg(user_a_id) AND status = 'pending';

-- name: InsertFriendship :exec
INSERT INTO friendships(user_a_id, user_b_id)
VALUES (sqlc.arg(user_a_id), sqlc.arg(user_b_id))
ON CONFLICT(user_a_id, user_b_id) DO NOTHING;

-- name: ListFriends :many
SELECT friend.id, friend.email, friend.name, friendships.created_at
FROM friendships
JOIN users AS friend
  ON friend.id = CASE WHEN friendships.user_a_id = sqlc.arg(user_id)
                      THEN friendships.user_b_id ELSE friendships.user_a_id END
WHERE friendships.user_a_id = sqlc.arg(user_id) OR friendships.user_b_id = sqlc.arg(user_id)
ORDER BY lower(friend.name), friend.id;

-- name: DeleteFriendship :execrows
DELETE FROM friendships
WHERE user_a_id = sqlc.arg(user_a_id) AND user_b_id = sqlc.arg(user_b_id);

-- name: GetSocialSettings :one
SELECT user_id, visible, updated_at FROM social_settings WHERE user_id = sqlc.arg(user_id);

-- name: UpsertSocialSettings :one
INSERT INTO social_settings(user_id, visible, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(visible), sqlc.arg(updated_at))
ON CONFLICT(user_id) DO UPDATE SET visible = excluded.visible, updated_at = excluded.updated_at
RETURNING *;

-- name: ListSocialLeaderboardStats :many
WITH eligible AS (
    SELECT CAST(sqlc.arg(user_id) AS INTEGER) AS user_id
    UNION
    SELECT CASE WHEN friendships.user_a_id = sqlc.arg(user_id)
                THEN friendships.user_b_id ELSE friendships.user_a_id END
    FROM friendships
    JOIN social_settings ON social_settings.user_id = CASE WHEN friendships.user_a_id = sqlc.arg(user_id)
                                                            THEN friendships.user_b_id ELSE friendships.user_a_id END
    WHERE (friendships.user_a_id = sqlc.arg(user_id) OR friendships.user_b_id = sqlc.arg(user_id))
      AND social_settings.visible = 1
), reviews AS (
    SELECT logs.user_id, COUNT(*) AS reviews
    FROM review_logs AS logs
    JOIN eligible ON eligible.user_id = logs.user_id
    WHERE logs.undone = 0
      AND logs.reviewed_at >= sqlc.arg(start_at)
      AND logs.reviewed_at < sqlc.arg(end_at)
    GROUP BY logs.user_id
), learned AS (
    SELECT logs.user_id, COUNT(DISTINCT cards.note_id) AS learned
    FROM review_logs AS logs
    JOIN review_log_projection AS projection ON projection.review_id = logs.id
    JOIN cards ON cards.id = logs.card_id AND cards.user_id = logs.user_id
    JOIN notes ON notes.id = cards.note_id
    JOIN decks ON decks.id = notes.deck_id
    JOIN eligible ON eligible.user_id = logs.user_id
    WHERE logs.undone = 0
      AND cards.direction = 'forward'
      AND cards.deleted_at IS NULL
      AND notes.deleted_at IS NULL
      AND decks.deleted_at IS NULL
      AND (decks.owner_id = cards.user_id OR EXISTS (
          SELECT 1 FROM social_subscription_tree AS access
          WHERE access.recipient_id = cards.user_id AND access.deck_id = decks.id AND access.share_status = 'active'
      ))
      AND projection.stability_after >= 21
      AND logs.reviewed_at >= sqlc.arg(start_at)
      AND logs.reviewed_at < sqlc.arg(end_at)
      AND logs.id = (
          SELECT candidate.id
          FROM review_logs AS candidate
          JOIN review_log_projection AS candidate_projection ON candidate_projection.review_id = candidate.id
          WHERE candidate.card_id = cards.id
            AND candidate.user_id = cards.user_id
            AND candidate.undone = 0
            AND candidate_projection.stability_after >= 21
          ORDER BY candidate.reviewed_at, candidate.id
          LIMIT 1
      )
    GROUP BY logs.user_id
)
SELECT users.id, users.name,
       CAST(COALESCE(reviews.reviews, 0) AS INTEGER) AS review_count,
       CAST(COALESCE(learned.learned, 0) AS INTEGER) AS learned_count
FROM eligible
JOIN users ON users.id = eligible.user_id
LEFT JOIN reviews ON reviews.user_id = users.id
LEFT JOIN learned ON learned.user_id = users.id;

-- name: ListSocialActivityDays :many
WITH eligible(user_id) AS (
    SELECT CASE WHEN friendships.user_a_id = sqlc.arg(user_id)
                THEN friendships.user_b_id ELSE friendships.user_a_id END
    FROM friendships
    JOIN social_settings ON social_settings.user_id = CASE WHEN friendships.user_a_id = sqlc.arg(user_id)
                                                            THEN friendships.user_b_id ELSE friendships.user_a_id END
    WHERE (friendships.user_a_id = sqlc.arg(user_id) OR friendships.user_b_id = sqlc.arg(user_id))
      AND social_settings.visible = 1
)
SELECT logs.user_id, users.name, date(logs.reviewed_at) AS day,
       CAST(COUNT(*) AS INTEGER) AS review_count
FROM review_logs AS logs
JOIN eligible ON eligible.user_id = logs.user_id
JOIN users ON users.id = logs.user_id
WHERE logs.undone = 0
  AND logs.reviewed_at >= sqlc.arg(start_at)
  AND logs.reviewed_at < sqlc.arg(end_at)
GROUP BY logs.user_id, date(logs.reviewed_at)
ORDER BY day DESC, lower(users.name), users.id;

-- name: ListDeckTreeForShare :many
WITH RECURSIVE tree(id, depth) AS (
    SELECT decks.id, 0 FROM decks
    WHERE decks.id = sqlc.arg(deck_id) AND decks.owner_id = sqlc.arg(owner_id) AND decks.deleted_at IS NULL
    UNION ALL
    SELECT child.id, parent.depth + 1
    FROM decks AS child
    JOIN tree AS parent ON child.parent_id = parent.id
    JOIN decks AS parent_deck ON parent_deck.id = parent.id
    WHERE child.owner_id = sqlc.arg(owner_id) AND child.deleted_at IS NULL
      AND parent_deck.deleted_at IS NULL
)
SELECT decks.* FROM tree JOIN decks ON decks.id = tree.id
ORDER BY tree.depth, decks.position, decks.id;

-- name: CreateActiveDeckSubscription :one
INSERT INTO deck_shares(source_deck_id, recipient_id, mode, status)
SELECT decks.id, sqlc.arg(recipient_user_id), 'subscribe', 'active'
FROM decks
WHERE decks.id = sqlc.arg(source_deck_id)
  AND decks.owner_id = sqlc.arg(owner_id)
  AND decks.deleted_at IS NULL
  AND EXISTS (
      SELECT 1 FROM friendships
      WHERE user_a_id = min(sqlc.arg(owner_id), sqlc.arg(recipient_user_id))
        AND user_b_id = max(sqlc.arg(owner_id), sqlc.arg(recipient_user_id))
  )
RETURNING *;

-- name: CreateCompletedDeckCopyShare :one
INSERT INTO deck_shares(source_deck_id, recipient_id, mode, status, copied_deck_id)
SELECT decks.id, sqlc.arg(recipient_user_id), 'copy', 'complete', sqlc.arg(copied_deck_id)
FROM decks
WHERE decks.id = sqlc.arg(source_deck_id)
  AND decks.owner_id = sqlc.arg(owner_id)
  AND decks.deleted_at IS NULL
  AND EXISTS (
      SELECT 1 FROM friendships
      WHERE user_a_id = min(sqlc.arg(owner_id), sqlc.arg(recipient_user_id))
        AND user_b_id = max(sqlc.arg(owner_id), sqlc.arg(recipient_user_id))
  )
RETURNING *;

-- name: HasDeckShare :one
SELECT EXISTS (
    SELECT 1 FROM deck_shares
    WHERE source_deck_id = sqlc.arg(source_deck_id)
      AND recipient_id = sqlc.arg(recipient_id)
);

-- name: GetShareForParticipant :one
SELECT shares.*, source.owner_id AS source_owner_id
FROM deck_shares AS shares
JOIN decks AS source ON source.id = shares.source_deck_id
WHERE shares.id = sqlc.arg(share_id)
  AND (source.owner_id = sqlc.arg(user_id) OR shares.recipient_id = sqlc.arg(user_id));

-- name: ListSharesForDeckOwner :many
SELECT shares.*, recipient.name AS recipient_name, recipient.email AS recipient_email
FROM deck_shares AS shares
JOIN decks AS source ON source.id = shares.source_deck_id
JOIN users AS recipient ON recipient.id = shares.recipient_id
WHERE source.id = sqlc.arg(deck_id)
  AND source.owner_id = sqlc.arg(owner_id)
ORDER BY shares.created_at DESC, shares.id DESC;

-- name: ListSharesForParticipant :many
SELECT shares.*, source.name AS source_deck_name, source.owner_id AS source_owner_id,
       owner.name AS source_owner_name,
       recipient.name AS recipient_name, recipient.email AS recipient_email
FROM deck_shares AS shares
JOIN decks AS source ON source.id = shares.source_deck_id
JOIN users AS owner ON owner.id = source.owner_id
JOIN users AS recipient ON recipient.id = shares.recipient_id
WHERE source.owner_id = sqlc.arg(user_id) OR shares.recipient_id = sqlc.arg(user_id)
ORDER BY shares.created_at DESC, shares.id DESC;

-- name: RevokeSubscription :execrows
UPDATE deck_shares
SET status = 'revoked', updated_at = sqlc.arg(updated_at)
WHERE deck_shares.id = sqlc.arg(share_id) AND deck_shares.mode = 'subscribe' AND deck_shares.status = 'active'
  AND deck_shares.source_deck_id IN (SELECT decks.id FROM decks WHERE decks.owner_id = sqlc.arg(user_id));

-- name: UnsubscribeFromDeck :execrows
UPDATE deck_shares
SET status = 'unsubscribed', updated_at = sqlc.arg(updated_at)
WHERE deck_shares.id = sqlc.arg(share_id) AND deck_shares.recipient_id = sqlc.arg(user_id)
  AND deck_shares.mode = 'subscribe' AND deck_shares.status = 'active';
