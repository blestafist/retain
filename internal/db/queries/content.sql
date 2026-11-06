-- name: CreateDeck :one
INSERT INTO decks (
    owner_id, parent_id, name, description, position,
    lang_target, lang_native, reverse_cards, wrong_article_rating,
    color_seed, source, external_id, expected_count, source_deck_id
)
SELECT CAST(sqlc.arg(owner_id) AS INTEGER),
       CAST(sqlc.narg(parent_id) AS INTEGER),
       CAST(sqlc.arg(name) AS TEXT),
       CAST(sqlc.narg(description) AS TEXT),
       CAST(sqlc.arg(position) AS INTEGER),
       CAST(sqlc.narg(lang_target) AS TEXT),
       CAST(sqlc.narg(lang_native) AS TEXT),
       CAST(sqlc.arg(reverse_cards) AS BOOLEAN),
       CAST(sqlc.arg(wrong_article_rating) AS INTEGER),
       CAST(sqlc.narg(color_seed) AS TEXT),
       CAST(sqlc.narg(source) AS TEXT),
       CAST(sqlc.narg(external_id) AS TEXT),
       CAST(sqlc.narg(expected_count) AS INTEGER),
       CAST(sqlc.narg(source_deck_id) AS INTEGER)
WHERE sqlc.narg(parent_id) IS NULL OR EXISTS (
    SELECT 1 FROM decks AS parent
    WHERE parent.id = sqlc.narg(parent_id)
      AND parent.owner_id = sqlc.arg(owner_id)
      AND parent.deleted_at IS NULL
)
RETURNING *;

-- name: GetDeck :one
SELECT * FROM decks
WHERE id = ? AND owner_id = ? AND deleted_at IS NULL;

-- name: ListDecksByOwner :many
SELECT * FROM decks
WHERE owner_id = ? AND deleted_at IS NULL
ORDER BY parent_id, position, id;

-- name: ListChildDecks :many
SELECT child.* FROM decks AS child
WHERE child.owner_id = ?
  AND child.parent_id = ?
  AND child.deleted_at IS NULL
  AND EXISTS (
      SELECT 1 FROM decks AS parent
      WHERE parent.id = child.parent_id
        AND parent.owner_id = child.owner_id
        AND parent.deleted_at IS NULL
  )
ORDER BY child.position, child.id;

-- name: UpdateDeck :one
UPDATE decks
SET parent_id = sqlc.narg(parent_id),
    name = sqlc.arg(name),
    description = sqlc.narg(description),
    position = sqlc.arg(position),
    lang_target = sqlc.narg(lang_target),
    lang_native = sqlc.narg(lang_native),
    reverse_cards = sqlc.arg(reverse_cards),
    wrong_article_rating = sqlc.arg(wrong_article_rating),
    color_seed = sqlc.narg(color_seed),
    source = sqlc.narg(source),
    external_id = sqlc.narg(external_id),
    expected_count = sqlc.narg(expected_count),
    source_deck_id = sqlc.narg(source_deck_id),
    updated_at = sqlc.arg(updated_at)
WHERE decks.id = sqlc.arg(id)
  AND decks.owner_id = sqlc.arg(owner_id)
  AND decks.deleted_at IS NULL
  AND (
      sqlc.narg(parent_id) IS NULL OR EXISTS (
          SELECT 1 FROM decks AS parent
          WHERE parent.id = sqlc.narg(parent_id)
            AND parent.owner_id = decks.owner_id
            AND parent.deleted_at IS NULL
      )
  )
RETURNING *;

-- name: SoftDeleteDeck :one
UPDATE decks
SET deleted_at = ?, updated_at = ?
WHERE id = ? AND owner_id = ? AND deleted_at IS NULL
RETURNING *;

-- name: CreateImportBatch :one
INSERT INTO import_batches (id, user_id, adapter, filename, raw, raw_sha256)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetImportBatch :one
SELECT * FROM import_batches
WHERE id = ? AND user_id = ?;

-- name: ListImportBatches :many
SELECT id, user_id, adapter, filename, raw_sha256, status, summary, created_at, applied_at
FROM import_batches
WHERE user_id = ?
ORDER BY created_at DESC, id;

-- name: UpdateImportBatch :one
UPDATE import_batches
SET adapter = ?,
    filename = ?,
    raw = ?,
    raw_sha256 = ?,
    status = ?,
    summary = ?,
    applied_at = ?
WHERE id = ? AND user_id = ?
RETURNING *;

-- name: CreateNote :one
INSERT INTO notes (
    deck_id, term, term_key, translation, transcription,
    example, example_translation, notes, tags, alternatives, fields,
    external_id, import_batch_id
)
SELECT decks.id,
       CAST(sqlc.arg(term) AS TEXT),
       CAST(sqlc.arg(term_key) AS TEXT),
       CAST(sqlc.arg(translation) AS TEXT),
       CAST(sqlc.narg(transcription) AS TEXT),
       CAST(sqlc.narg(example) AS TEXT),
       CAST(sqlc.narg(example_translation) AS TEXT),
       CAST(sqlc.narg(notes) AS TEXT),
       CAST(sqlc.arg(tags) AS TEXT),
       CAST(sqlc.arg(alternatives) AS TEXT),
       CAST(sqlc.arg(fields) AS TEXT),
       CAST(sqlc.narg(external_id) AS TEXT),
       CAST(sqlc.narg(import_batch_id) AS TEXT)
FROM decks
WHERE decks.id = CAST(sqlc.arg(deck_id) AS INTEGER)
  AND decks.owner_id = CAST(sqlc.arg(user_id) AS INTEGER)
  AND decks.deleted_at IS NULL
  AND (
      sqlc.narg(import_batch_id) IS NULL OR EXISTS (
          SELECT 1 FROM import_batches
          WHERE import_batches.id = sqlc.narg(import_batch_id)
            AND import_batches.user_id = sqlc.arg(user_id)
      )
  )
RETURNING *;

-- name: GetNote :one
SELECT notes.* FROM notes
JOIN decks ON decks.id = notes.deck_id
WHERE notes.id = ?
  AND decks.owner_id = ?
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL;

-- name: ListNotesByDeck :many
SELECT notes.* FROM notes
JOIN decks ON decks.id = notes.deck_id
WHERE notes.deck_id = ?
  AND decks.owner_id = ?
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
ORDER BY notes.id;

-- name: UpdateNote :one
UPDATE notes
SET term = sqlc.arg(term),
    term_key = sqlc.arg(term_key),
    translation = sqlc.arg(translation),
    transcription = sqlc.narg(transcription),
    example = sqlc.narg(example),
    example_translation = sqlc.narg(example_translation),
    notes = sqlc.narg(notes),
    tags = sqlc.arg(tags),
    alternatives = sqlc.arg(alternatives),
    fields = sqlc.arg(fields),
    external_id = sqlc.narg(external_id),
    import_batch_id = sqlc.narg(import_batch_id),
    updated_at = sqlc.arg(updated_at)
WHERE notes.id = sqlc.arg(id)
  AND notes.deleted_at IS NULL
  AND EXISTS (
      SELECT 1 FROM decks
      WHERE decks.id = notes.deck_id
        AND decks.owner_id = sqlc.arg(owner_id)
        AND decks.deleted_at IS NULL
  )
  AND (
      sqlc.narg(import_batch_id) IS NULL OR EXISTS (
          SELECT 1 FROM import_batches
          WHERE import_batches.id = sqlc.narg(import_batch_id)
            AND import_batches.user_id = sqlc.arg(owner_id)
      )
  )
RETURNING *;

-- name: SoftDeleteNote :one
UPDATE notes
SET deleted_at = ?, updated_at = ?
WHERE notes.id = ?
  AND notes.deleted_at IS NULL
  AND EXISTS (
      SELECT 1 FROM decks
      WHERE decks.id = notes.deck_id
        AND decks.owner_id = ?
        AND decks.deleted_at IS NULL
  )
RETURNING *;

-- name: CreateCard :one
INSERT INTO cards (note_id, user_id, direction)
SELECT notes.id, CAST(sqlc.arg(user_id) AS INTEGER), CAST(sqlc.arg(direction) AS TEXT)
FROM notes
JOIN decks ON decks.id = notes.deck_id
WHERE notes.id = CAST(sqlc.arg(note_id) AS INTEGER)
  AND decks.owner_id = CAST(sqlc.arg(user_id) AS INTEGER)
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
ON CONFLICT (note_id, user_id, direction) DO UPDATE
SET direction = excluded.direction
RETURNING *;

-- name: GetCard :one
SELECT cards.* FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.id = ?
  AND cards.user_id = ?
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL;

-- name: ListCardsByUser :many
SELECT cards.* FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.user_id = ?
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
ORDER BY cards.due, cards.id;

-- name: ListCardsByNote :many
SELECT cards.* FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
WHERE cards.note_id = ?
  AND cards.user_id = ?
  AND notes.deleted_at IS NULL
  AND decks.deleted_at IS NULL
ORDER BY cards.id;
