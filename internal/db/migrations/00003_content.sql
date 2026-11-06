-- +goose Up
CREATE TABLE decks (
    id INTEGER PRIMARY KEY,
    owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_id INTEGER REFERENCES decks(id) ON DELETE RESTRICT,
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

CREATE INDEX decks_owner_parent_idx ON decks(owner_id, parent_id, position, id);
CREATE INDEX decks_parent_id_idx ON decks(parent_id);

CREATE TABLE import_batches (
    id TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    adapter TEXT NOT NULL CHECK (length(trim(adapter)) > 0),
    filename TEXT,
    raw BLOB NOT NULL,
    raw_sha256 BLOB NOT NULL CHECK (length(raw_sha256) = 32),
    status TEXT NOT NULL DEFAULT 'previewed' CHECK (status IN ('previewed', 'applied', 'failed')),
    summary TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(summary) AND json_type(summary) = 'object'),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    applied_at DATETIME
);

CREATE INDEX import_batches_user_created_idx ON import_batches(user_id, created_at DESC);

CREATE TABLE notes (
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

CREATE INDEX notes_deck_id_idx ON notes(deck_id);

CREATE TABLE cards (
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

CREATE INDEX cards_user_due_idx ON cards(user_id, due);
CREATE INDEX cards_note_id_idx ON cards(note_id);

-- +goose Down
DROP TABLE cards;
DROP INDEX notes_deck_id_idx;
DROP TABLE notes;
DROP INDEX import_batches_user_created_idx;
DROP TABLE import_batches;
DROP INDEX decks_parent_id_idx;
DROP INDEX decks_owner_parent_idx;
DROP TABLE decks;
