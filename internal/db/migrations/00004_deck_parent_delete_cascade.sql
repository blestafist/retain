-- +goose Up
-- Preserve the original v3 tables and their full UNIQUE constraints. The marker columns let a
-- cascade restore exactly the rows deleted with its owner without rebuilding content tables.
ALTER TABLE decks ADD COLUMN deletion_batch TEXT;
ALTER TABLE notes ADD COLUMN deletion_batch TEXT;
ALTER TABLE cards ADD COLUMN deleted_at DATETIME;
ALTER TABLE cards ADD COLUMN deletion_batch TEXT;

-- v3 allowed soft-deleted parents to retain active descendants. Give pre-existing tombstones
-- distinct batches, then apply each tombstone to only active descendants with the same owner.
UPDATE decks
SET deletion_batch = 'migration-00004-deck-' || id
WHERE deleted_at IS NOT NULL;

-- A deleted child deck is a separate deletion root. Stopping recursion at it preserves its own
-- timestamp and batch; that child is seeded independently and cascades to its active descendants.
WITH RECURSIVE cascaded(deck_id, owner_id, deleted_at, deletion_batch) AS (
    SELECT id, owner_id, deleted_at, deletion_batch
    FROM decks
    WHERE deleted_at IS NOT NULL
    UNION
    SELECT child.id, parent.owner_id, parent.deleted_at, parent.deletion_batch
    FROM decks AS child
    JOIN cascaded AS parent ON child.parent_id = parent.deck_id
    WHERE child.owner_id = parent.owner_id
      AND child.deleted_at IS NULL
)
UPDATE decks
SET deleted_at = (
        SELECT cascaded.deleted_at FROM cascaded
        WHERE cascaded.deck_id = decks.id
        LIMIT 1
    ),
    deletion_batch = (
        SELECT cascaded.deletion_batch FROM cascaded
        WHERE cascaded.deck_id = decks.id
        LIMIT 1
    )
WHERE deleted_at IS NULL
  AND id IN (SELECT deck_id FROM cascaded);

UPDATE notes
SET deletion_batch = 'migration-00004-note-' || id
WHERE deleted_at IS NOT NULL;

-- Active notes below a deleted deck join that deck's cascade. Independently deleted notes retain
-- their legacy timestamp and receive their own batch above.
UPDATE notes
SET deleted_at = (
        SELECT decks.deleted_at FROM decks WHERE decks.id = notes.deck_id
    ),
    deletion_batch = (
        SELECT decks.deletion_batch FROM decks WHERE decks.id = notes.deck_id
    )
WHERE deleted_at IS NULL
  AND EXISTS (
      SELECT 1 FROM decks
      WHERE decks.id = notes.deck_id AND decks.deleted_at IS NOT NULL
  );

-- Cards did not have tombstones in v3. Their note is the authoritative deletion boundary, while
-- card scheduling state and review history remain untouched.
UPDATE cards
SET deleted_at = (
        SELECT notes.deleted_at FROM notes WHERE notes.id = cards.note_id
    ),
    deletion_batch = (
        SELECT notes.deletion_batch FROM notes WHERE notes.id = cards.note_id
    )
WHERE EXISTS (
    SELECT 1 FROM notes
    WHERE notes.id = cards.note_id AND notes.deleted_at IS NOT NULL
);

-- Keep the nullable marker pair consistent without rebuilding the v3 tables.
-- +goose StatementBegin
CREATE TRIGGER decks_deletion_markers_insert
BEFORE INSERT ON decks
WHEN (NEW.deleted_at IS NULL) <> (NEW.deletion_batch IS NULL)
  OR (NEW.deletion_batch IS NOT NULL AND length(NEW.deletion_batch) = 0)
BEGIN
    SELECT RAISE(ABORT, 'invalid deck deletion markers');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER decks_deletion_markers_update
BEFORE UPDATE OF deleted_at, deletion_batch ON decks
WHEN (NEW.deleted_at IS NULL) <> (NEW.deletion_batch IS NULL)
  OR (NEW.deletion_batch IS NOT NULL AND length(NEW.deletion_batch) = 0)
BEGIN
    SELECT RAISE(ABORT, 'invalid deck deletion markers');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER notes_deletion_markers_insert
BEFORE INSERT ON notes
WHEN (NEW.deleted_at IS NULL) <> (NEW.deletion_batch IS NULL)
  OR (NEW.deletion_batch IS NOT NULL AND length(NEW.deletion_batch) = 0)
BEGIN
    SELECT RAISE(ABORT, 'invalid note deletion markers');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER notes_deletion_markers_update
BEFORE UPDATE OF deleted_at, deletion_batch ON notes
WHEN (NEW.deleted_at IS NULL) <> (NEW.deletion_batch IS NULL)
  OR (NEW.deletion_batch IS NOT NULL AND length(NEW.deletion_batch) = 0)
BEGIN
    SELECT RAISE(ABORT, 'invalid note deletion markers');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER cards_deletion_markers_insert
BEFORE INSERT ON cards
WHEN (NEW.deleted_at IS NULL) <> (NEW.deletion_batch IS NULL)
  OR (NEW.deletion_batch IS NOT NULL AND length(NEW.deletion_batch) = 0)
BEGIN
    SELECT RAISE(ABORT, 'invalid card deletion markers');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER cards_deletion_markers_update
BEFORE UPDATE OF deleted_at, deletion_batch ON cards
WHEN (NEW.deleted_at IS NULL) <> (NEW.deletion_batch IS NULL)
  OR (NEW.deletion_batch IS NOT NULL AND length(NEW.deletion_batch) = 0)
BEGIN
    SELECT RAISE(ABORT, 'invalid card deletion markers');
END;
-- +goose StatementEnd

-- A private batch groups only rows changed by this exact cascade. Descendant queries always
-- filter by owner_id, so corrupt legacy cross-owner links are never soft-deleted as foreign data.
-- +goose StatementBegin
CREATE TRIGGER decks_soft_delete_cascade
AFTER UPDATE OF deleted_at ON decks
WHEN OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL
BEGIN
    UPDATE decks
    SET deleted_at = NEW.deleted_at,
        deletion_batch = NEW.deletion_batch,
        updated_at = NEW.updated_at
    WHERE deleted_at IS NULL
      AND id IN (
          WITH RECURSIVE descendants(id) AS (
              SELECT child.id
              FROM decks AS child
              WHERE child.parent_id = NEW.id
                AND child.owner_id = NEW.owner_id
                AND child.deleted_at IS NULL
              UNION
              SELECT child.id
              FROM decks AS child
              JOIN descendants AS parent ON child.parent_id = parent.id
              WHERE child.owner_id = NEW.owner_id
                AND child.deleted_at IS NULL
          )
          SELECT id FROM descendants
      );

    UPDATE notes
    SET deleted_at = NEW.deleted_at,
        deletion_batch = NEW.deletion_batch,
        updated_at = NEW.updated_at
    WHERE deleted_at IS NULL
      AND deck_id IN (
          WITH RECURSIVE subtree(id) AS (
              SELECT NEW.id
              UNION
              SELECT child.id
              FROM decks AS child
              JOIN subtree AS parent ON child.parent_id = parent.id
              WHERE child.owner_id = NEW.owner_id
                AND child.deleted_at = NEW.deleted_at
                AND child.deletion_batch = NEW.deletion_batch
          )
          SELECT id FROM subtree
      );
END;
-- +goose StatementEnd

-- Restore only rows that carry this exact cascade's timestamp and marker. Independent deletions
-- inside the subtree keep their own marker and remain deleted.
-- +goose StatementBegin
CREATE TRIGGER decks_restore_cascade
AFTER UPDATE OF deleted_at ON decks
WHEN OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL
BEGIN
    UPDATE cards
    SET deleted_at = NULL,
        deletion_batch = NULL,
        updated_at = NEW.updated_at
    WHERE deleted_at = OLD.deleted_at
      AND deletion_batch = OLD.deletion_batch
      AND note_id IN (
          SELECT notes.id
          FROM notes
          WHERE notes.deleted_at = OLD.deleted_at
            AND notes.deletion_batch = OLD.deletion_batch
            AND notes.deck_id IN (
                WITH RECURSIVE subtree(id) AS (
                    SELECT NEW.id
                    UNION
                    SELECT child.id
                    FROM decks AS child
                    JOIN subtree AS parent ON child.parent_id = parent.id
                    WHERE child.owner_id = NEW.owner_id
                      AND child.deleted_at = OLD.deleted_at
                      AND child.deletion_batch = OLD.deletion_batch
                )
                SELECT id FROM subtree
            )
      );

    UPDATE notes
    SET deleted_at = NULL,
        deletion_batch = NULL,
        updated_at = NEW.updated_at
    WHERE deleted_at = OLD.deleted_at
      AND deletion_batch = OLD.deletion_batch
      AND deck_id IN (
          WITH RECURSIVE subtree(id) AS (
              SELECT NEW.id
              UNION
              SELECT child.id
              FROM decks AS child
              JOIN subtree AS parent ON child.parent_id = parent.id
              WHERE child.owner_id = NEW.owner_id
                AND child.deleted_at = OLD.deleted_at
                AND child.deletion_batch = OLD.deletion_batch
          )
          SELECT id FROM subtree
      );

    UPDATE decks
    SET deleted_at = NULL,
        deletion_batch = NULL,
        updated_at = NEW.updated_at
    WHERE deleted_at = OLD.deleted_at
      AND deletion_batch = OLD.deletion_batch
      AND owner_id = NEW.owner_id
      AND id IN (
          WITH RECURSIVE descendants(id) AS (
              SELECT child.id
              FROM decks AS child
              WHERE child.parent_id = NEW.id
                AND child.owner_id = NEW.owner_id
                AND child.deleted_at = OLD.deleted_at
                AND child.deletion_batch = OLD.deletion_batch
              UNION
              SELECT child.id
              FROM decks AS child
              JOIN descendants AS parent ON child.parent_id = parent.id
              WHERE child.owner_id = NEW.owner_id
                AND child.deleted_at = OLD.deleted_at
                AND child.deletion_batch = OLD.deletion_batch
          )
          SELECT id FROM descendants
      );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER notes_soft_delete_cards
AFTER UPDATE OF deleted_at ON notes
WHEN OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL
BEGIN
    UPDATE cards
    SET deleted_at = NEW.deleted_at,
        deletion_batch = NEW.deletion_batch,
        updated_at = NEW.updated_at
    WHERE note_id = NEW.id AND deleted_at IS NULL;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER notes_restore_cards
AFTER UPDATE OF deleted_at ON notes
WHEN OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL
BEGIN
    UPDATE cards
    SET deleted_at = NULL,
        deletion_batch = NULL,
        updated_at = NEW.updated_at
    WHERE note_id = NEW.id
      AND deleted_at = OLD.deleted_at
      AND deletion_batch = OLD.deletion_batch;
END;
-- +goose StatementEnd

-- parent_id remains ON DELETE RESTRICT. Delete owned children before the parent; a cross-owner
-- legacy child is intentionally skipped and makes SQLite reject (and roll back) the parent delete.
-- This trigger recurses through nested decks, so the connection enables recursive_triggers.
-- +goose StatementBegin
CREATE TRIGGER decks_hard_delete_descendants
BEFORE DELETE ON decks
BEGIN
    DELETE FROM decks
    WHERE parent_id = OLD.id AND owner_id = OLD.owner_id;
END;
-- +goose StatementEnd

-- +goose Down
-- The v3 schema cannot represent cards' tombstones or cascade ownership. Refuse rollback rather
-- than silently discard those markers; Goose wraps this guard and the schema changes atomically.
CREATE TEMP TABLE migration_00004_down_guard (
    allowed INTEGER NOT NULL CHECK (allowed = 1)
);
INSERT INTO migration_00004_down_guard
SELECT CASE WHEN
    EXISTS (SELECT 1 FROM decks WHERE deleted_at IS NOT NULL OR deletion_batch IS NOT NULL) OR
    EXISTS (SELECT 1 FROM notes WHERE deleted_at IS NOT NULL OR deletion_batch IS NOT NULL) OR
    EXISTS (SELECT 1 FROM cards WHERE deleted_at IS NOT NULL OR deletion_batch IS NOT NULL)
THEN 0 ELSE 1 END;
DROP TABLE migration_00004_down_guard;

DROP TRIGGER decks_hard_delete_descendants;
DROP TRIGGER notes_restore_cards;
DROP TRIGGER notes_soft_delete_cards;
DROP TRIGGER decks_restore_cascade;
DROP TRIGGER decks_soft_delete_cascade;
DROP TRIGGER cards_deletion_markers_update;
DROP TRIGGER cards_deletion_markers_insert;
DROP TRIGGER notes_deletion_markers_update;
DROP TRIGGER notes_deletion_markers_insert;
DROP TRIGGER decks_deletion_markers_update;
DROP TRIGGER decks_deletion_markers_insert;

ALTER TABLE cards DROP COLUMN deletion_batch;
ALTER TABLE cards DROP COLUMN deleted_at;
ALTER TABLE notes DROP COLUMN deletion_batch;
ALTER TABLE decks DROP COLUMN deletion_batch;
