-- name: GetStatsSettings :one
SELECT user_id, request_retention, maximum_interval, fsrs_weights, timezone, day_start_hour
FROM user_settings
WHERE user_id = ?;

-- name: GetStatsOverview :one
WITH card_metrics AS (
    SELECT CAST(COUNT(*) AS INTEGER) AS total_cards,
           CAST(COALESCE(SUM(CASE WHEN cards.state = 0 THEN 1 ELSE 0 END), 0) AS INTEGER) AS new_cards,
           CAST(COALESCE(SUM(CASE WHEN cards.state = 1 THEN 1 ELSE 0 END), 0) AS INTEGER) AS learning_cards,
           CAST(COALESCE(SUM(CASE WHEN cards.state = 2 THEN 1 ELSE 0 END), 0) AS INTEGER) AS review_cards,
           CAST(COALESCE(SUM(CASE WHEN cards.state = 3 THEN 1 ELSE 0 END), 0) AS INTEGER) AS relearning_cards,
           CAST(COALESCE(SUM(CASE WHEN cards.stability IS NOT NULL AND cards.stability < 21 THEN 1 ELSE 0 END), 0) AS INTEGER) AS young_cards,
           CAST(COALESCE(SUM(CASE WHEN cards.stability >= 21 THEN 1 ELSE 0 END), 0) AS INTEGER) AS mature_cards,
           CAST(COALESCE(SUM(CASE WHEN cards.state != 0 AND cards.suspended = 0 AND CAST(strftime('%s', substr(cards.due, 1, 19)) AS INTEGER) <= CAST(sqlc.arg(now_unix) AS INTEGER) THEN 1 ELSE 0 END), 0) AS INTEGER) AS due_cards,
           CAST(COALESCE(SUM(CASE WHEN cards.is_leech = 1 OR cards.lapses >= 8 THEN 1 ELSE 0 END), 0) AS INTEGER) AS leech_cards
    FROM cards
    JOIN notes ON notes.id = cards.note_id
    JOIN decks ON decks.id = notes.deck_id
    WHERE cards.user_id = sqlc.arg(user_id)
      AND cards.deleted_at IS NULL
      AND notes.deleted_at IS NULL
      AND decks.deleted_at IS NULL
      AND decks.owner_id = cards.user_id
), log_metrics AS (
     SELECT CAST(COUNT(*) AS INTEGER) AS total_reviews,
            CAST(COALESCE(SUM(logs.duration_ms), 0) AS INTEGER) AS total_duration_ms,
            CAST(COALESCE(AVG(logs.duration_ms), 0) AS INTEGER) AS average_duration_ms,
             CAST(COALESCE(SUM(CASE WHEN CAST(strftime('%s', substr(logs.reviewed_at, 1, 19)) AS INTEGER) >= CAST(sqlc.arg(day_start_unix) AS INTEGER)
                                      AND CAST(strftime('%s', substr(logs.reviewed_at, 1, 19)) AS INTEGER) < CAST(sqlc.arg(day_end_unix) AS INTEGER)
                                    THEN 1 ELSE 0 END), 0) AS INTEGER) AS reviews_today,
            CAST(COALESCE(SUM(CASE WHEN projection.state_before = 0 AND CAST(strftime('%s', substr(logs.reviewed_at, 1, 19)) AS INTEGER) >= CAST(sqlc.arg(day_start_unix) AS INTEGER)
                                     AND CAST(strftime('%s', substr(logs.reviewed_at, 1, 19)) AS INTEGER) < CAST(sqlc.arg(day_end_unix) AS INTEGER)
                                   THEN 1 ELSE 0 END), 0) AS INTEGER) AS new_reviews_today,
            CAST(COALESCE(SUM(CASE WHEN projection.state_before = 2 AND CAST(strftime('%s', substr(logs.reviewed_at, 1, 19)) AS INTEGER) >= CAST(sqlc.arg(day_start_unix) AS INTEGER)
                                     AND CAST(strftime('%s', substr(logs.reviewed_at, 1, 19)) AS INTEGER) < CAST(sqlc.arg(day_end_unix) AS INTEGER)
                                   THEN 1 ELSE 0 END), 0) AS INTEGER) AS review_cards_today,
            CAST(COALESCE(SUM(CASE WHEN projection.state_before = 2 AND logs.rating >= 2 AND CAST(strftime('%s', substr(logs.reviewed_at, 1, 19)) AS INTEGER) >= CAST(sqlc.arg(day_start_unix) AS INTEGER)
                                     AND CAST(strftime('%s', substr(logs.reviewed_at, 1, 19)) AS INTEGER) < CAST(sqlc.arg(day_end_unix) AS INTEGER)
                                   THEN 1 ELSE 0 END), 0) AS INTEGER) AS recalled_reviews_today,
            CAST(COALESCE(SUM(CASE WHEN CAST(strftime('%s', substr(logs.reviewed_at, 1, 19)) AS INTEGER) >= CAST(sqlc.arg(day_start_unix) AS INTEGER)
                                     AND CAST(strftime('%s', substr(logs.reviewed_at, 1, 19)) AS INTEGER) < CAST(sqlc.arg(day_end_unix) AS INTEGER)
                                   THEN logs.duration_ms ELSE 0 END), 0) AS INTEGER) AS duration_today_ms
     FROM review_logs AS logs
     JOIN review_log_projection AS projection ON projection.review_id = logs.id
      WHERE logs.user_id = sqlc.arg(user_id) AND logs.undone = 0
         AND logs.reviewed_at <= CAST(sqlc.arg(now_at) AS TEXT)
)
SELECT card_metrics.total_cards, card_metrics.new_cards, card_metrics.learning_cards,
       card_metrics.review_cards, card_metrics.relearning_cards, card_metrics.young_cards,
       card_metrics.mature_cards, card_metrics.due_cards, card_metrics.leech_cards,
       log_metrics.total_reviews, log_metrics.total_duration_ms, log_metrics.average_duration_ms,
       log_metrics.reviews_today, log_metrics.new_reviews_today, log_metrics.review_cards_today,
       log_metrics.recalled_reviews_today, log_metrics.duration_today_ms
FROM card_metrics CROSS JOIN log_metrics;

-- name: GetStatsBucketsActivity :one
-- start_unix/end_unix are user-local day/period boundaries computed by the service.
SELECT CAST(sqlc.arg(label) AS TEXT) AS label,
       CAST(COUNT(*) AS INTEGER) AS reviews,
       CAST(COALESCE(SUM(logs.duration_ms), 0) AS INTEGER) AS duration_ms,
       CAST(COALESCE(SUM(CASE WHEN projection.state_before = 2 THEN 1 ELSE 0 END), 0) AS INTEGER) AS review_cards,
       CAST(COALESCE(SUM(CASE WHEN projection.state_before = 2 AND logs.rating >= 2 THEN 1 ELSE 0 END), 0) AS INTEGER) AS recalled_reviews
FROM review_logs AS logs
JOIN review_log_projection AS projection ON projection.review_id = logs.id
 WHERE logs.user_id = sqlc.arg(user_id)
  AND logs.undone = 0
   AND logs.reviewed_at <= CAST(sqlc.arg(now_at) AS TEXT)
  AND CAST(strftime('%s', substr(logs.reviewed_at, 1, 19)) AS INTEGER) >= CAST(sqlc.arg(start_unix) AS INTEGER)
    AND CAST(strftime('%s', substr(logs.reviewed_at, 1, 19)) AS INTEGER) < CAST(sqlc.arg(end_unix) AS INTEGER);

-- name: ListStatsReviewTimestamps :many
-- The service maps a bounded date range to study days for streak calculation.
SELECT logs.reviewed_at
FROM review_logs AS logs
JOIN review_log_projection AS projection ON projection.review_id = logs.id
WHERE logs.user_id = sqlc.arg(user_id)
  AND logs.undone = 0
  AND logs.reviewed_at >= CAST(sqlc.arg(start_at) AS TEXT)
  AND logs.reviewed_at < CAST(sqlc.arg(end_at) AS TEXT)
  AND logs.reviewed_at <= CAST(sqlc.arg(now_at) AS TEXT)
ORDER BY logs.reviewed_at DESC;

-- name: ListStatsActivityRange :many
-- This uses the user/reviewed_at composite index; Go applies exact local-day buckets.
SELECT logs.reviewed_at, logs.duration_ms, projection.state_before, logs.rating
FROM review_logs AS logs
JOIN review_log_projection AS projection ON projection.review_id = logs.id
WHERE logs.user_id = sqlc.arg(user_id)
  AND logs.undone = 0
   AND logs.reviewed_at <= CAST(sqlc.arg(now_at) AS TEXT)
  AND logs.reviewed_at >= CAST(sqlc.arg(start_at) AS TEXT)
  AND logs.reviewed_at < CAST(sqlc.arg(end_at) AS TEXT)
ORDER BY logs.reviewed_at, logs.id;

-- name: GetStatsVocabularyGrowth :many
WITH RECURSIVE deck_path AS (
    SELECT id AS deck_id, id AS current_id, parent_id, lang_target
    FROM decks
    WHERE owner_id = sqlc.arg(user_id) AND deleted_at IS NULL
    UNION
    SELECT deck_path.deck_id AS deck_id, parent.id AS current_id, parent.parent_id AS parent_id,
           parent.lang_target AS lang_target
    FROM deck_path
    JOIN decks AS parent ON parent.id = deck_path.parent_id
    WHERE parent.owner_id = sqlc.arg(user_id)
      AND parent.deleted_at IS NULL
      AND deck_path.lang_target IS NULL
), deck_language AS (
    SELECT deck_path.deck_id AS deck_id,
           CAST(COALESCE(MAX(deck_path.lang_target), '') AS TEXT) AS lang_target
    FROM deck_path
    GROUP BY deck_path.deck_id
)
SELECT deck_language.lang_target,
       CAST(COUNT(DISTINCT cards.note_id) AS INTEGER) AS learned
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
JOIN review_logs AS logs ON logs.card_id = cards.id AND logs.user_id = cards.user_id
JOIN review_log_projection AS projection ON projection.review_id = logs.id
JOIN deck_language ON deck_language.deck_id = decks.id
WHERE cards.user_id = sqlc.arg(user_id)
  AND cards.direction = 'forward'
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
  AND decks.owner_id = cards.user_id
   AND logs.undone = 0
  AND projection.stability_after >= 21
   AND logs.reviewed_at <= CAST(sqlc.arg(now_at) AS TEXT)
  AND logs.reviewed_at >= CAST(sqlc.arg(start_at) AS TEXT)
  AND logs.reviewed_at < CAST(sqlc.arg(end_at) AS TEXT)
  AND logs.id = (
       SELECT candidate.id
       FROM review_logs AS candidate
       JOIN review_log_projection AS candidate_projection ON candidate_projection.review_id = candidate.id
       WHERE candidate.card_id = cards.id
        AND candidate.user_id = cards.user_id
          AND candidate.undone = 0
           AND candidate.reviewed_at <= CAST(sqlc.arg(now_at) AS TEXT)
          AND candidate_projection.stability_after >= 21
      ORDER BY candidate.reviewed_at, candidate.id
      LIMIT 1
  )
GROUP BY deck_language.lang_target
ORDER BY deck_language.lang_target;

-- name: GetStatsForecast :one
SELECT CAST(sqlc.arg(label) AS TEXT) AS label,
       CAST(COUNT(cards.id) AS INTEGER) AS due_cards
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.user_id = sqlc.arg(user_id)
  AND cards.state != 0
  AND cards.suspended = 0
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
  AND decks.owner_id = cards.user_id
  AND CAST(strftime('%s', substr(cards.due, 1, 19)) AS INTEGER) >= CAST(sqlc.arg(start_unix) AS INTEGER)
  AND CAST(strftime('%s', substr(cards.due, 1, 19)) AS INTEGER) < CAST(sqlc.arg(end_unix) AS INTEGER);

-- name: GetStatsCardCurveCard :one
SELECT cards.id AS card_id, cards.note_id, cards.direction, cards.state,
       cards.stability, cards.difficulty, cards.scheduled_days, cards.lapses,
       cards.last_review, notes.term
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.id = sqlc.arg(card_id)
  AND cards.user_id = sqlc.arg(user_id)
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
  AND decks.owner_id = cards.user_id;

-- name: ListStatsCardCurveLogs :many
SELECT logs.id, logs.rating, projection.state_before, projection.stability_before,
       projection.difficulty_before, projection.stability_after, projection.difficulty_after,
       logs.reviewed_at, projection.scheduled_days
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
JOIN review_logs AS logs ON logs.card_id = cards.id AND logs.user_id = cards.user_id
JOIN review_log_projection AS projection ON projection.review_id = logs.id
WHERE cards.id = sqlc.arg(card_id)
  AND cards.user_id = sqlc.arg(user_id)
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
  AND decks.owner_id = cards.user_id
   AND logs.undone = 0
    AND logs.reviewed_at <= CAST(sqlc.arg(now_at) AS TEXT)
ORDER BY logs.reviewed_at, logs.id;

-- name: GetStatsRetrievabilityCards :many
SELECT cards.id, cards.state, cards.stability, cards.difficulty, cards.due,
       cards.elapsed_days, cards.scheduled_days, cards.step, cards.reps, cards.lapses,
       cards.last_review
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.user_id = sqlc.arg(user_id)
  AND cards.state = 2
  AND cards.stability IS NOT NULL
  AND cards.difficulty IS NOT NULL
   AND cards.last_review IS NOT NULL
    AND cards.last_review <= CAST(sqlc.arg(now_at) AS TEXT)
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
  AND decks.owner_id = cards.user_id;

-- name: GetStatsStateDistribution :many
SELECT cards.state,
       CAST(COUNT(*) AS INTEGER) AS total,
       CAST(COALESCE(SUM(CASE WHEN cards.stability IS NOT NULL AND cards.stability < 21 THEN 1 ELSE 0 END), 0) AS INTEGER) AS young,
       CAST(COALESCE(SUM(CASE WHEN cards.stability >= 21 THEN 1 ELSE 0 END), 0) AS INTEGER) AS mature
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.user_id = sqlc.arg(user_id)
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
  AND decks.owner_id = cards.user_id
GROUP BY cards.state
ORDER BY cards.state;

-- name: GetStatsStabilityDistribution :many
SELECT CASE
           WHEN stability < 1 THEN '<1'
           WHEN stability < 3 THEN '1-3'
           WHEN stability < 7 THEN '3-7'
           WHEN stability < 21 THEN '7-21'
           WHEN stability < 60 THEN '21-60'
           ELSE '60+'
       END AS bucket,
       CASE
           WHEN stability < 1 THEN 0
           WHEN stability < 3 THEN 1
           WHEN stability < 7 THEN 2
           WHEN stability < 21 THEN 3
           WHEN stability < 60 THEN 4
           ELSE 5
       END AS sort_order,
       CAST(COUNT(*) AS INTEGER) AS count
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.user_id = sqlc.arg(user_id)
  AND cards.stability IS NOT NULL
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
  AND decks.owner_id = cards.user_id
GROUP BY bucket, sort_order
ORDER BY sort_order;

-- name: GetStatsDifficultyDistribution :many
SELECT CASE
           WHEN difficulty < 2 THEN '1-2'
           WHEN difficulty < 4 THEN '2-4'
           WHEN difficulty < 6 THEN '4-6'
           WHEN difficulty < 8 THEN '6-8'
           ELSE '8-10'
       END AS bucket,
       CASE
           WHEN difficulty < 2 THEN 0
           WHEN difficulty < 4 THEN 1
           WHEN difficulty < 6 THEN 2
           WHEN difficulty < 8 THEN 3
           ELSE 4
       END AS sort_order,
       CAST(COUNT(*) AS INTEGER) AS count
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.user_id = sqlc.arg(user_id)
  AND cards.difficulty IS NOT NULL
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
  AND decks.owner_id = cards.user_id
GROUP BY bucket, sort_order
ORDER BY sort_order;

-- name: ListStatsLeeches :many
SELECT cards.id AS card_id, cards.direction, cards.lapses, notes.term
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.user_id = sqlc.arg(user_id)
  AND (cards.is_leech = 1 OR cards.lapses >= 8)
  AND cards.deleted_at IS NULL
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
  AND decks.owner_id = cards.user_id
ORDER BY cards.lapses DESC, cards.id
LIMIT 20;

-- name: ListStatsDeckMedianStability :many
WITH ranked AS (
    SELECT decks.id AS deck_id, decks.name AS deck_name, cards.stability,
           ROW_NUMBER() OVER (PARTITION BY decks.id ORDER BY cards.stability) AS sequence_no,
           COUNT(*) OVER (PARTITION BY decks.id) AS card_count
    FROM cards
    JOIN notes ON notes.id = cards.note_id
    JOIN decks ON decks.id = notes.deck_id
    WHERE cards.user_id = sqlc.arg(user_id)
      AND cards.state = 2
      AND cards.stability IS NOT NULL
      AND cards.deleted_at IS NULL
      AND notes.deleted_at IS NULL
      AND decks.deleted_at IS NULL
      AND decks.owner_id = cards.user_id
)
SELECT deck_id, deck_name, AVG(stability) AS median_stability
FROM ranked
WHERE sequence_no IN ((card_count + 1) / 2, (card_count + 2) / 2)
GROUP BY deck_id, deck_name
ORDER BY deck_name, deck_id;
