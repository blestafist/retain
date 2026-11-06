-- +goose NO TRANSACTION

-- SQLite cannot change a foreign key's ON DELETE action in place. Rebuild the table with
-- foreign-key enforcement disabled, in an explicit transaction, and restore the schema objects.
-- +goose Up
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;

CREATE TABLE decks_new (
    id INTEGER PRIMARY KEY,
    owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_id INTEGER REFERENCES decks_new(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    description TEXT,
    position INTEGER NOT NULL DEFAULT 0,
    lang_target TEXT CHECK (lang_target IS NULL OR lang_target IN ('de', 'en')),
    lang_native TEXT CHECK (lang_native IS NULL OR lang_native IN ('pl', 'ru')),
    reverse_cards BOOLEAN NOT NULL DEFAULT 0 CHECK (reverse_cards IN (0, 1)),
    wrong_article_rating INTEGER NOT NULL DEFAULT 2 CHECK (wrong_article_rating BETWEEN 1 AND 4),
    color_seed TEXT CHECK (
        color_seed IS NULL OR
        color_seed GLOB '#[0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f]'
    ),
    source TEXT,
    external_id TEXT,
    expected_count INTEGER CHECK (expected_count IS NULL OR expected_count >= 0),
    -- Kept as a scalar until deck sharing is introduced; this is intentionally not a foreign key.
    source_deck_id INTEGER,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,
    deletion_batch TEXT,
    CHECK ((deleted_at IS NULL) = (deletion_batch IS NULL))
);

INSERT INTO decks_new (
    id, owner_id, parent_id, name, description, position, lang_target, lang_native,
    reverse_cards, wrong_article_rating, color_seed, source, external_id, expected_count,
    source_deck_id, created_at, updated_at, deleted_at, deletion_batch
)
SELECT
    id, owner_id, parent_id, name, description, position, lang_target, lang_native,
    reverse_cards, wrong_article_rating, color_seed, source, external_id, expected_count,
    source_deck_id, created_at, updated_at, deleted_at, deletion_batch
FROM decks;

DROP TABLE decks;
ALTER TABLE decks_new RENAME TO decks;

CREATE INDEX decks_owner_parent_idx ON decks(owner_id, parent_id, position, id);
CREATE INDEX decks_parent_id_idx ON decks(parent_id);
CREATE UNIQUE INDEX decks_owner_source_external_active_uq
    ON decks(owner_id, source, external_id)
    WHERE deleted_at IS NULL AND source IS NOT NULL AND external_id IS NOT NULL;

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
                AND child.deleted_at IS NOT NULL
                AND child.deletion_batch = NEW.deletion_batch
          )
          SELECT id FROM subtree
      );

    UPDATE cards
    SET deleted_at = NEW.deleted_at,
        deletion_batch = NEW.deletion_batch,
        updated_at = NEW.updated_at
    WHERE deleted_at IS NULL
      AND note_id IN (
          SELECT notes.id
          FROM notes
          WHERE notes.deleted_at = NEW.deleted_at
            AND notes.deletion_batch = NEW.deletion_batch
            AND notes.deck_id IN (
                WITH RECURSIVE subtree(id) AS (
                    SELECT NEW.id
                    UNION
                    SELECT child.id
                    FROM decks AS child
                    JOIN subtree AS parent ON child.parent_id = parent.id
                    WHERE child.owner_id = NEW.owner_id
                      AND child.deleted_at IS NOT NULL
                      AND child.deletion_batch = NEW.deletion_batch
                )
                SELECT id FROM subtree
            )
      );
END;
-- +goose StatementEnd

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

COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;

CREATE TABLE decks_new (
    id INTEGER PRIMARY KEY,
    owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_id INTEGER REFERENCES decks_new(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    description TEXT,
    position INTEGER NOT NULL DEFAULT 0,
    lang_target TEXT CHECK (lang_target IS NULL OR lang_target IN ('de', 'en')),
    lang_native TEXT CHECK (lang_native IS NULL OR lang_native IN ('pl', 'ru')),
    reverse_cards BOOLEAN NOT NULL DEFAULT 0 CHECK (reverse_cards IN (0, 1)),
    wrong_article_rating INTEGER NOT NULL DEFAULT 2 CHECK (wrong_article_rating BETWEEN 1 AND 4),
    color_seed TEXT CHECK (
        color_seed IS NULL OR
        color_seed GLOB '#[0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f]'
    ),
    source TEXT,
    external_id TEXT,
    expected_count INTEGER CHECK (expected_count IS NULL OR expected_count >= 0),
    -- Kept as a scalar until deck sharing is introduced; this is intentionally not a foreign key.
    source_deck_id INTEGER,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,
    deletion_batch TEXT,
    CHECK ((deleted_at IS NULL) = (deletion_batch IS NULL))
);

INSERT INTO decks_new (
    id, owner_id, parent_id, name, description, position, lang_target, lang_native,
    reverse_cards, wrong_article_rating, color_seed, source, external_id, expected_count,
    source_deck_id, created_at, updated_at, deleted_at, deletion_batch
)
SELECT
    id, owner_id, parent_id, name, description, position, lang_target, lang_native,
    reverse_cards, wrong_article_rating, color_seed, source, external_id, expected_count,
    source_deck_id, created_at, updated_at, deleted_at, deletion_batch
FROM decks;

DROP TABLE decks;
ALTER TABLE decks_new RENAME TO decks;

CREATE INDEX decks_owner_parent_idx ON decks(owner_id, parent_id, position, id);
CREATE INDEX decks_parent_id_idx ON decks(parent_id);
CREATE UNIQUE INDEX decks_owner_source_external_active_uq
    ON decks(owner_id, source, external_id)
    WHERE deleted_at IS NULL AND source IS NOT NULL AND external_id IS NOT NULL;

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
                AND child.deleted_at IS NOT NULL
                AND child.deletion_batch = NEW.deletion_batch
          )
          SELECT id FROM subtree
      );

    UPDATE cards
    SET deleted_at = NEW.deleted_at,
        deletion_batch = NEW.deletion_batch,
        updated_at = NEW.updated_at
    WHERE deleted_at IS NULL
      AND note_id IN (
          SELECT notes.id
          FROM notes
          WHERE notes.deleted_at = NEW.deleted_at
            AND notes.deletion_batch = NEW.deletion_batch
            AND notes.deck_id IN (
                WITH RECURSIVE subtree(id) AS (
                    SELECT NEW.id
                    UNION
                    SELECT child.id
                    FROM decks AS child
                    JOIN subtree AS parent ON child.parent_id = parent.id
                    WHERE child.owner_id = NEW.owner_id
                      AND child.deleted_at IS NOT NULL
                      AND child.deletion_batch = NEW.deletion_batch
                )
                SELECT id FROM subtree
            )
      );
END;
-- +goose StatementEnd

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

COMMIT;
PRAGMA foreign_keys = ON;
