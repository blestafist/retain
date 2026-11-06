-- +goose NO TRANSACTION

-- Upgrade the schema as it was originally shipped in 00003_content.sql. Foreign keys must be
-- disabled before the explicit transaction because SQLite cannot change a foreign key in place.
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
    source_deck_id, created_at, updated_at, deleted_at,
    CASE WHEN deleted_at IS NULL THEN NULL ELSE 'migration-00004-deck-' || id END
FROM decks;

DROP TABLE decks;
ALTER TABLE decks_new RENAME TO decks;

CREATE TABLE notes_new (
    id INTEGER PRIMARY KEY,
    deck_id INTEGER NOT NULL REFERENCES decks(id) ON DELETE CASCADE,
    term TEXT NOT NULL CHECK (length(trim(term)) > 0),
    term_key TEXT NOT NULL CHECK (length(trim(term_key)) > 0),
    translation TEXT NOT NULL CHECK (length(trim(translation)) > 0),
    transcription TEXT,
    example TEXT,
    example_translation TEXT,
    notes TEXT,
    tags TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(tags) AND json_type(tags) = 'array'),
    alternatives TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(alternatives) AND json_type(alternatives) = 'array'),
    fields TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(fields) AND json_type(fields) = 'object'),
    external_id TEXT,
    import_batch_id TEXT REFERENCES import_batches(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,
    deletion_batch TEXT,
    CHECK ((deleted_at IS NULL) = (deletion_batch IS NULL))
);

INSERT INTO notes_new (
    id, deck_id, term, term_key, translation, transcription, example, example_translation,
    notes, tags, alternatives, fields, external_id, import_batch_id, created_at, updated_at,
    deleted_at, deletion_batch
)
SELECT
    id, deck_id, term, term_key, translation, transcription, example, example_translation,
    notes, tags, alternatives, fields, external_id, import_batch_id, created_at, updated_at,
    deleted_at,
    CASE WHEN deleted_at IS NULL THEN NULL ELSE 'migration-00004-note-' || id END
FROM notes;

DROP TABLE notes;
ALTER TABLE notes_new RENAME TO notes;

CREATE TABLE cards_new (
    id INTEGER PRIMARY KEY,
    note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    direction TEXT NOT NULL CHECK (direction IN ('forward', 'reverse')),
    state INTEGER NOT NULL DEFAULT 0 CHECK (state BETWEEN 0 AND 3),
    due DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    stability REAL CHECK (stability IS NULL OR stability > 0),
    difficulty REAL CHECK (difficulty IS NULL OR difficulty BETWEEN 1 AND 10),
    elapsed_days INTEGER NOT NULL DEFAULT 0 CHECK (elapsed_days >= 0),
    scheduled_days INTEGER NOT NULL DEFAULT 0 CHECK (scheduled_days >= 0),
    step INTEGER NOT NULL DEFAULT 0 CHECK (step >= 0),
    reps INTEGER NOT NULL DEFAULT 0 CHECK (reps >= 0),
    lapses INTEGER NOT NULL DEFAULT 0 CHECK (lapses >= 0),
    last_review DATETIME,
    suspended BOOLEAN NOT NULL DEFAULT 0 CHECK (suspended IN (0, 1)),
    is_leech BOOLEAN NOT NULL DEFAULT 0 CHECK (is_leech IN (0, 1)),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,
    deletion_batch TEXT,
    CHECK ((deleted_at IS NULL) = (deletion_batch IS NULL)),
    UNIQUE (note_id, user_id, direction)
);

INSERT INTO cards_new (
    id, note_id, user_id, direction, state, due, stability, difficulty, elapsed_days,
    scheduled_days, step, reps, lapses, last_review, suspended, is_leech, updated_at,
    deleted_at, deletion_batch
)
SELECT
    id, note_id, user_id, direction, state, due, stability, difficulty, elapsed_days,
    scheduled_days, step, reps, lapses, last_review, suspended, is_leech, updated_at,
    NULL, NULL
FROM cards;

DROP TABLE cards;
ALTER TABLE cards_new RENAME TO cards;

CREATE INDEX decks_owner_parent_idx ON decks(owner_id, parent_id, position, id);
CREATE INDEX decks_parent_id_idx ON decks(parent_id);
CREATE UNIQUE INDEX decks_owner_source_external_active_uq
    ON decks(owner_id, source, external_id)
    WHERE deleted_at IS NULL AND source IS NOT NULL AND external_id IS NOT NULL;

CREATE INDEX notes_deck_id_idx ON notes(deck_id);
CREATE UNIQUE INDEX notes_deck_term_key_active_uq
    ON notes(deck_id, term_key)
    WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX notes_deck_external_active_uq
    ON notes(deck_id, external_id)
    WHERE deleted_at IS NULL AND external_id IS NOT NULL;

CREATE INDEX cards_user_due_idx ON cards(user_id, due);
CREATE INDEX cards_note_id_idx ON cards(note_id);

-- A random marker groups only rows changed by this exact cascade. It prevents a restore from
-- reactivating descendants or notes that were deleted independently at an earlier time.
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

COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
-- Refuse lossy rollback before touching user tables. The 00003 schema had global uniqueness,
-- no card tombstones, and no deletion-batch markers. The migration's own synthetic markers are
-- safe to discard; markers written by the application and newly soft-deleted cards are not.
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;

CREATE TEMP TABLE migration_00004_down_guard (
    allowed INTEGER NOT NULL CHECK (allowed = 1)
);
INSERT INTO migration_00004_down_guard
SELECT CASE WHEN
    EXISTS (
        SELECT 1 FROM decks
        WHERE deletion_batch IS NOT NULL
          AND deletion_batch <> 'migration-00004-deck-' || id
    ) OR
    EXISTS (
        SELECT 1 FROM notes
        WHERE deletion_batch IS NOT NULL
          AND deletion_batch <> 'migration-00004-note-' || id
    ) OR
    EXISTS (SELECT 1 FROM cards WHERE deleted_at IS NOT NULL OR deletion_batch IS NOT NULL) OR
    EXISTS (
        SELECT 1 FROM decks
        WHERE source IS NOT NULL AND external_id IS NOT NULL
        GROUP BY owner_id, source, external_id
        HAVING COUNT(*) > 1
    ) OR
    EXISTS (
        SELECT 1 FROM notes
        GROUP BY deck_id, term_key
        HAVING COUNT(*) > 1
    ) OR
    EXISTS (
        SELECT 1 FROM notes
        WHERE external_id IS NOT NULL
        GROUP BY deck_id, external_id
        HAVING COUNT(*) > 1
    )
THEN 0 ELSE 1 END;
DROP TABLE migration_00004_down_guard;

DROP TRIGGER notes_restore_cards;
DROP TRIGGER notes_soft_delete_cards;
DROP TRIGGER decks_restore_cascade;
DROP TRIGGER decks_soft_delete_cascade;

CREATE TABLE cards_new (
    id INTEGER PRIMARY KEY,
    note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    direction TEXT NOT NULL CHECK (direction IN ('forward', 'reverse')),
    state INTEGER NOT NULL DEFAULT 0 CHECK (state BETWEEN 0 AND 3),
    due DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    stability REAL CHECK (stability IS NULL OR stability > 0),
    difficulty REAL CHECK (difficulty IS NULL OR difficulty BETWEEN 1 AND 10),
    elapsed_days INTEGER NOT NULL DEFAULT 0 CHECK (elapsed_days >= 0),
    scheduled_days INTEGER NOT NULL DEFAULT 0 CHECK (scheduled_days >= 0),
    step INTEGER NOT NULL DEFAULT 0 CHECK (step >= 0),
    reps INTEGER NOT NULL DEFAULT 0 CHECK (reps >= 0),
    lapses INTEGER NOT NULL DEFAULT 0 CHECK (lapses >= 0),
    last_review DATETIME,
    suspended BOOLEAN NOT NULL DEFAULT 0 CHECK (suspended IN (0, 1)),
    is_leech BOOLEAN NOT NULL DEFAULT 0 CHECK (is_leech IN (0, 1)),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (note_id, user_id, direction)
);

INSERT INTO cards_new (
    id, note_id, user_id, direction, state, due, stability, difficulty, elapsed_days,
    scheduled_days, step, reps, lapses, last_review, suspended, is_leech, updated_at
)
SELECT
    id, note_id, user_id, direction, state, due, stability, difficulty, elapsed_days,
    scheduled_days, step, reps, lapses, last_review, suspended, is_leech, updated_at
FROM cards;

DROP TABLE cards;
ALTER TABLE cards_new RENAME TO cards;
CREATE INDEX cards_user_due_idx ON cards(user_id, due);
CREATE INDEX cards_note_id_idx ON cards(note_id);

CREATE TABLE notes_new (
    id INTEGER PRIMARY KEY,
    deck_id INTEGER NOT NULL REFERENCES decks(id) ON DELETE CASCADE,
    term TEXT NOT NULL CHECK (length(trim(term)) > 0),
    term_key TEXT NOT NULL CHECK (length(trim(term_key)) > 0),
    translation TEXT NOT NULL CHECK (length(trim(translation)) > 0),
    transcription TEXT,
    example TEXT,
    example_translation TEXT,
    notes TEXT,
    tags TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(tags) AND json_type(tags) = 'array'),
    alternatives TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(alternatives) AND json_type(alternatives) = 'array'),
    fields TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(fields) AND json_type(fields) = 'object'),
    external_id TEXT,
    import_batch_id TEXT REFERENCES import_batches(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,
    UNIQUE (deck_id, term_key),
    UNIQUE (deck_id, external_id)
);

INSERT INTO notes_new (
    id, deck_id, term, term_key, translation, transcription, example, example_translation,
    notes, tags, alternatives, fields, external_id, import_batch_id, created_at, updated_at,
    deleted_at
)
SELECT
    id, deck_id, term, term_key, translation, transcription, example, example_translation,
    notes, tags, alternatives, fields, external_id, import_batch_id, created_at, updated_at,
    deleted_at
FROM notes;

DROP TABLE notes;
ALTER TABLE notes_new RENAME TO notes;
CREATE INDEX notes_deck_id_idx ON notes(deck_id);

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
    UNIQUE (owner_id, source, external_id)
);

INSERT INTO decks_new (
    id, owner_id, parent_id, name, description, position, lang_target, lang_native,
    reverse_cards, wrong_article_rating, color_seed, source, external_id, expected_count,
    source_deck_id, created_at, updated_at, deleted_at
)
SELECT
    id, owner_id, parent_id, name, description, position, lang_target, lang_native,
    reverse_cards, wrong_article_rating, color_seed, source, external_id, expected_count,
    source_deck_id, created_at, updated_at, deleted_at
FROM decks;

DROP TABLE decks;
ALTER TABLE decks_new RENAME TO decks;
CREATE INDEX decks_owner_parent_idx ON decks(owner_id, parent_id, position, id);
CREATE INDEX decks_parent_id_idx ON decks(parent_id);

COMMIT;
PRAGMA foreign_keys = ON;
