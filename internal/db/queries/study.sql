-- name: GetStudyDeck :one
SELECT id FROM decks
WHERE id = sqlc.arg(deck_id) AND owner_id = sqlc.arg(user_id) AND deleted_at IS NULL;

-- name: GetStudyCard :one
SELECT cards.* FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.id = sqlc.arg(id)
  AND cards.user_id = sqlc.arg(user_id)
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
  AND decks.owner_id = cards.user_id;

-- name: GetStudyAnswerCard :one
WITH RECURSIVE card_deck_path(depth, id, parent_id, lang_target, lang_native, visited) AS (
    SELECT 0, deck.id, deck.parent_id, deck.lang_target, deck.lang_native, printf(',%d,', deck.id)
    FROM cards
    JOIN notes ON notes.id = cards.note_id
    JOIN decks AS deck ON deck.id = notes.deck_id
    WHERE cards.id = sqlc.arg(card_id)
      AND cards.user_id = sqlc.arg(user_id)
      AND cards.deleted_at IS NULL
      AND notes.deleted_at IS NULL
      AND deck.deleted_at IS NULL
      AND deck.owner_id = cards.user_id
    UNION ALL
    SELECT child.depth + 1, parent.id, parent.parent_id, parent.lang_target, parent.lang_native,
           child.visited || printf('%d,', parent.id)
    FROM card_deck_path AS child
    JOIN decks AS parent ON parent.id = child.parent_id
    WHERE parent.owner_id = sqlc.arg(user_id)
      AND parent.deleted_at IS NULL
      AND instr(child.visited, printf(',%d,', parent.id)) = 0
)
SELECT cards.id AS card_id,
       cards.note_id,
       notes.deck_id,
       cards.direction,
       cards.state,
       cards.suspended,
       notes.term,
       notes.translation,
       notes.alternatives,
       notes.fields,
       CAST(COALESCE((
           SELECT lang_target FROM card_deck_path
           WHERE lang_target IS NOT NULL
           ORDER BY depth LIMIT 1
       ), '') AS TEXT) AS lang_target,
       CAST(COALESCE((
           SELECT lang_native FROM card_deck_path
           WHERE lang_native IS NOT NULL
           ORDER BY depth LIMIT 1
       ), '') AS TEXT) AS lang_native,
       deck.wrong_article_rating
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks AS deck ON deck.id = notes.deck_id
WHERE cards.id = sqlc.arg(card_id)
  AND cards.user_id = sqlc.arg(user_id)
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND deck.deleted_at IS NULL
  AND deck.owner_id = cards.user_id;

-- name: ListStudyChoiceDistractors :many
SELECT notes.id AS note_id,
CAST(CASE
    WHEN cards.direction = 'reverse' THEN notes.term
    ELSE notes.translation
END AS TEXT) AS answer,
notes.alternatives
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.user_id = sqlc.arg(user_id)
  AND cards.direction = sqlc.arg(direction)
  AND notes.deck_id = sqlc.arg(deck_id)
  AND cards.note_id != sqlc.arg(note_id)
  AND notes.id > sqlc.arg(after_note_id)
  AND cards.suspended = 0
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
  AND decks.owner_id = cards.user_id
  AND trim(CASE
      WHEN cards.direction = 'reverse' THEN notes.term
      ELSE notes.translation
  END) != ''
ORDER BY notes.id
LIMIT sqlc.arg(page_size);

-- name: ListStudyCards :many
WITH RECURSIVE selected_decks(id) AS (
    SELECT id FROM decks
    WHERE owner_id = sqlc.arg(user_id)
      AND deleted_at IS NULL
      AND (sqlc.narg(deck_id) IS NULL OR id = sqlc.narg(deck_id))
    UNION
    SELECT child.id FROM decks AS child
    JOIN selected_decks AS parent ON child.parent_id = parent.id
    WHERE child.owner_id = sqlc.arg(user_id) AND child.deleted_at IS NULL
)
SELECT cards.id AS card_id,
       cards.note_id,
       cards.user_id,
       notes.deck_id,
       cards.direction,
       cards.state,
       cards.due,
       cards.stability,
       cards.difficulty,
       cards.elapsed_days,
       cards.scheduled_days,
       cards.step,
       cards.reps,
       cards.lapses,
       cards.last_review,
       notes.term,
       notes.translation
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.user_id = sqlc.arg(user_id)
  AND cards.suspended = 0
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
  AND decks.owner_id = cards.user_id
  AND (cards.state = 0 OR cards.due <= sqlc.arg(due_before))
  AND (sqlc.narg(deck_id) IS NULL OR decks.id IN (SELECT id FROM selected_decks))
ORDER BY CASE cards.state
             WHEN 2 THEN 0
             WHEN 1 THEN 1
             WHEN 3 THEN 1
             WHEN 0 THEN 2
             ELSE 3
         END,
         cards.due,
         cards.id;

-- name: GetDailyReviewCounts :one
SELECT CAST(COALESCE(SUM(CASE WHEN projection.state_before = 0 THEN 1 ELSE 0 END), 0) AS INTEGER) AS new_count,
       CAST(COALESCE(SUM(CASE WHEN projection.state_before = 2 THEN 1 ELSE 0 END), 0) AS INTEGER) AS review_count
FROM review_logs AS logs
JOIN review_log_projection AS projection ON projection.review_id = logs.id
WHERE logs.user_id = sqlc.arg(user_id)
  AND logs.undone = 0
  AND logs.reviewed_at >= sqlc.arg(reviewed_at)
  AND logs.reviewed_at < sqlc.arg(reviewed_at_2);

-- name: GetReviewLog :one
SELECT * FROM review_logs WHERE id = ?;

-- name: ListActiveReviewLogs :many
SELECT * FROM review_logs
WHERE card_id = sqlc.arg(card_id) AND user_id = sqlc.arg(user_id) AND undone = 0
ORDER BY reviewed_at, id;

-- name: InsertReviewLog :one
INSERT INTO review_logs (
    id, card_id, user_id, rating, mode, state_before,
    stability_before, difficulty_before, stability_after, difficulty_after,
    scheduled_days, scheduler_parameters, reviewed_at, received_at, duration_ms
)
VALUES (
    sqlc.arg(id), sqlc.arg(card_id), sqlc.arg(user_id), sqlc.arg(rating), sqlc.arg(mode),
    sqlc.arg(state_before), sqlc.narg(stability_before), sqlc.narg(difficulty_before),
    sqlc.arg(stability_after), sqlc.arg(difficulty_after), sqlc.arg(scheduled_days), sqlc.narg(scheduler_parameters),
    sqlc.arg(reviewed_at), sqlc.arg(received_at), sqlc.arg(duration_ms)
)
ON CONFLICT(id) DO NOTHING
RETURNING *;

-- name: DeleteReviewLogProjectionsForCard :exec
DELETE FROM review_log_projection WHERE user_id = sqlc.arg(user_id) AND card_id = sqlc.arg(card_id);

-- name: UpsertReviewLogProjection :exec
INSERT INTO review_log_projection(
    review_id, card_id, user_id, state_before, stability_before, difficulty_before,
    stability_after, difficulty_after, scheduled_days
)
VALUES (
    sqlc.arg(review_id), sqlc.arg(card_id), sqlc.arg(user_id), sqlc.arg(state_before),
    sqlc.narg(stability_before), sqlc.narg(difficulty_before),
    sqlc.arg(stability_after), sqlc.arg(difficulty_after), sqlc.arg(scheduled_days)
)
ON CONFLICT(review_id) DO UPDATE SET
    card_id = excluded.card_id,
    user_id = excluded.user_id,
    state_before = excluded.state_before,
    stability_before = excluded.stability_before,
    difficulty_before = excluded.difficulty_before,
    stability_after = excluded.stability_after,
    difficulty_after = excluded.difficulty_after,
    scheduled_days = excluded.scheduled_days;

-- name: MarkReviewLogUndone :one
UPDATE review_logs
SET undone = 1
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND undone = 0
RETURNING *;

-- name: UpdateStudyCard :one
UPDATE cards
SET state = sqlc.arg(state),
    due = sqlc.arg(due),
    stability = sqlc.narg(stability),
    difficulty = sqlc.narg(difficulty),
    elapsed_days = sqlc.arg(elapsed_days),
    scheduled_days = sqlc.arg(scheduled_days),
    step = sqlc.arg(step),
    reps = sqlc.arg(reps),
    lapses = sqlc.arg(lapses),
    last_review = sqlc.narg(last_review),
    updated_at = sqlc.arg(updated_at)
WHERE cards.id = sqlc.arg(card_id)
  AND cards.user_id = sqlc.arg(user_id)
  AND cards.deleted_at IS NULL
  AND EXISTS (
      SELECT 1 FROM notes
      JOIN decks ON decks.id = notes.deck_id
      WHERE notes.id = cards.note_id
        AND notes.deleted_at IS NULL
        AND decks.deleted_at IS NULL
        AND decks.owner_id = cards.user_id
  )
RETURNING *;
