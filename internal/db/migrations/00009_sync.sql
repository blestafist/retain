-- +goose Up
-- Keep feed identity distinct from reusable numeric user IDs. AUTOINCREMENT
-- prevents an account recreated with the same user_id from inheriting old events.
CREATE TABLE sync_owner_scopes (
    scope_id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE
);

INSERT INTO sync_owner_scopes(user_id)
SELECT id FROM users ORDER BY id;

-- +goose StatementBegin
CREATE TRIGGER sync_owner_scope_insert
AFTER INSERT ON users
BEGIN
    INSERT INTO sync_owner_scopes(user_id) VALUES (NEW.id);
END;
-- +goose StatementEnd

CREATE TABLE sync_events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_id INTEGER NOT NULL CHECK (owner_id > 0),
    owner_scope_id INTEGER NOT NULL CHECK (owner_scope_id > 0),
    entity_type TEXT NOT NULL CHECK (entity_type IN ('note', 'card')),
    entity_id INTEGER NOT NULL CHECK (entity_id > 0),
    operation TEXT NOT NULL CHECK (operation IN ('upsert', 'delete')),
    payload TEXT NOT NULL CHECK (json_valid(payload) AND json_type(payload) = 'object'),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX sync_events_owner_seq_idx ON sync_events(owner_scope_id, owner_id, seq);

-- Sync events are an append-only change feed. A client must never observe
-- mutations to an already-issued cursor page.
-- +goose StatementBegin
CREATE TRIGGER sync_events_append_only_update
BEFORE UPDATE ON sync_events
BEGIN
    SELECT RAISE(ABORT, 'sync events are append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sync_events_append_only_delete
BEFORE DELETE ON sync_events
BEGIN
    SELECT RAISE(ABORT, 'sync events are append-only');
END;
-- +goose StatementEnd

-- Note snapshots are owner-scoped through their deck. The deleted flag is a
-- tombstone; no note contents are included after access has been revoked.
-- +goose StatementBegin
CREATE TRIGGER sync_notes_insert
AFTER INSERT ON notes
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT decks.owner_id, scopes.scope_id, 'note', NEW.id,
           CASE WHEN NEW.deleted_at IS NULL THEN 'upsert' ELSE 'delete' END,
            CASE WHEN NEW.deleted_at IS NULL AND decks.deleted_at IS NULL THEN json_object(
               'id', NEW.id, 'deck_id', NEW.deck_id, 'term', NEW.term, 'translation', NEW.translation,
               'transcription', NEW.transcription, 'example', NEW.example,
               'example_translation', NEW.example_translation, 'notes', NEW.notes,
               'tags', json(NEW.tags), 'alternatives', json(NEW.alternatives), 'fields', json(NEW.fields),
                'updated_at', replace(substr(NEW.updated_at, 1, instr(NEW.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
                'deleted', json('false')
           ) ELSE json_object('id', NEW.id, 'deleted', json('true')) END
    FROM decks
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = decks.owner_id
    WHERE decks.id = NEW.deck_id AND (NEW.deleted_at IS NOT NULL OR decks.deleted_at IS NULL);
END;
-- +goose StatementEnd

-- If a note is moved between owners, revoke the previous owner's local copy
-- and publish the new snapshot to the destination owner.
-- +goose StatementBegin
CREATE TRIGGER sync_notes_update
AFTER UPDATE ON notes
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT old_deck.owner_id, old_scope.scope_id, 'note', OLD.id, 'delete', json_object('id', OLD.id, 'deleted', json('true'))
    FROM decks AS old_deck
    LEFT JOIN decks AS new_deck ON new_deck.id = NEW.deck_id
    JOIN sync_owner_scopes AS old_scope ON old_scope.user_id = old_deck.owner_id
    WHERE old_deck.id = OLD.deck_id
      AND OLD.deleted_at IS NULL AND old_deck.deleted_at IS NULL
      AND (
        new_deck.id IS NULL OR old_deck.owner_id <> new_deck.owner_id
        OR NEW.deleted_at IS NOT NULL OR new_deck.deleted_at IS NOT NULL
      );

    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT decks.owner_id, scopes.scope_id, 'note', NEW.id,
           CASE WHEN NEW.deleted_at IS NULL THEN 'upsert' ELSE 'delete' END,
            CASE WHEN NEW.deleted_at IS NULL AND decks.deleted_at IS NULL THEN json_object(
               'id', NEW.id, 'deck_id', NEW.deck_id, 'term', NEW.term, 'translation', NEW.translation,
               'transcription', NEW.transcription, 'example', NEW.example,
               'example_translation', NEW.example_translation, 'notes', NEW.notes,
               'tags', json(NEW.tags), 'alternatives', json(NEW.alternatives), 'fields', json(NEW.fields),
                'updated_at', replace(substr(NEW.updated_at, 1, instr(NEW.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
                'deleted', json('false')
           ) ELSE json_object('id', NEW.id, 'deleted', json('true')) END
    FROM decks
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = decks.owner_id
    WHERE decks.id = NEW.deck_id AND (NEW.deleted_at IS NOT NULL OR decks.deleted_at IS NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sync_notes_delete
AFTER DELETE ON notes
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT decks.owner_id, scopes.scope_id, 'note', OLD.id, 'delete', json_object('id', OLD.id, 'deleted', json('true'))
    FROM decks
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = decks.owner_id
    WHERE decks.id = OLD.deck_id;
END;
-- +goose StatementEnd

-- Card snapshots include the owner's FSRS state. The note is synchronized
-- independently, allowing either entity to be updated or tombstoned.
-- +goose StatementBegin
CREATE TRIGGER sync_cards_insert
AFTER INSERT ON cards
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT NEW.user_id, scopes.scope_id, 'card', NEW.id,
        CASE WHEN NEW.deleted_at IS NULL AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL AND decks.owner_id = NEW.user_id THEN 'upsert' ELSE 'delete' END,
        CASE WHEN NEW.deleted_at IS NULL AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL AND decks.owner_id = NEW.user_id THEN json_remove(json_object(
            'id', NEW.id, 'note_id', NEW.note_id, 'user_id', NEW.user_id, 'direction', NEW.direction,
            'state', NEW.state,
            'due', replace(substr(NEW.due, 1, instr(NEW.due || ' +', ' +') - 1), ' ', 'T') || 'Z',
            'stability', NEW.stability, 'difficulty', NEW.difficulty,
            'elapsed_days', NEW.elapsed_days, 'scheduled_days', NEW.scheduled_days, 'step', NEW.step,
            'reps', NEW.reps, 'lapses', NEW.lapses,
            'last_review', CASE WHEN NEW.last_review IS NULL THEN NULL ELSE replace(substr(NEW.last_review, 1, instr(NEW.last_review || ' +', ' +') - 1), ' ', 'T') || 'Z' END,
            'suspended', json(CASE WHEN NEW.suspended = 1 THEN 'true' ELSE 'false' END),
            'is_leech', json(CASE WHEN NEW.is_leech = 1 THEN 'true' ELSE 'false' END),
            'updated_at', replace(substr(NEW.updated_at, 1, instr(NEW.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
            'deleted', json('false')
        ),
            CASE WHEN NEW.stability IS NULL THEN '$.stability' ELSE '$.__sync_noop' END,
            CASE WHEN NEW.difficulty IS NULL THEN '$.difficulty' ELSE '$.__sync_noop' END,
            CASE WHEN NEW.last_review IS NULL THEN '$.last_review' ELSE '$.__sync_noop' END
        ) ELSE json_object('id', NEW.id, 'deleted', json('true')) END
    FROM sync_owner_scopes AS scopes
    LEFT JOIN notes ON notes.id = NEW.note_id
    LEFT JOIN decks ON decks.id = notes.deck_id
    WHERE scopes.user_id = NEW.user_id
      AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL AND decks.owner_id = NEW.user_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sync_cards_update
AFTER UPDATE ON cards
WHEN OLD.user_id = NEW.user_id
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT NEW.user_id, scopes.scope_id, 'card', NEW.id,
        CASE WHEN NEW.deleted_at IS NULL AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL AND decks.owner_id = NEW.user_id THEN 'upsert' ELSE 'delete' END,
        CASE WHEN NEW.deleted_at IS NULL AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL AND decks.owner_id = NEW.user_id THEN json_remove(json_object(
            'id', NEW.id, 'note_id', NEW.note_id, 'user_id', NEW.user_id, 'direction', NEW.direction,
            'state', NEW.state,
            'due', replace(substr(NEW.due, 1, instr(NEW.due || ' +', ' +') - 1), ' ', 'T') || 'Z',
            'stability', NEW.stability, 'difficulty', NEW.difficulty,
            'elapsed_days', NEW.elapsed_days, 'scheduled_days', NEW.scheduled_days, 'step', NEW.step,
            'reps', NEW.reps, 'lapses', NEW.lapses,
            'last_review', CASE WHEN NEW.last_review IS NULL THEN NULL ELSE replace(substr(NEW.last_review, 1, instr(NEW.last_review || ' +', ' +') - 1), ' ', 'T') || 'Z' END,
            'suspended', json(CASE WHEN NEW.suspended = 1 THEN 'true' ELSE 'false' END),
            'is_leech', json(CASE WHEN NEW.is_leech = 1 THEN 'true' ELSE 'false' END),
            'updated_at', replace(substr(NEW.updated_at, 1, instr(NEW.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
            'deleted', json('false')
        ),
            CASE WHEN NEW.stability IS NULL THEN '$.stability' ELSE '$.__sync_noop' END,
            CASE WHEN NEW.difficulty IS NULL THEN '$.difficulty' ELSE '$.__sync_noop' END,
            CASE WHEN NEW.last_review IS NULL THEN '$.last_review' ELSE '$.__sync_noop' END
        ) ELSE json_object('id', NEW.id, 'deleted', json('true')) END
    FROM sync_owner_scopes AS scopes
    LEFT JOIN notes ON notes.id = NEW.note_id
    LEFT JOIN decks ON decks.id = notes.deck_id
    LEFT JOIN notes AS old_notes ON old_notes.id = OLD.note_id
    LEFT JOIN decks AS old_decks ON old_decks.id = old_notes.deck_id
    WHERE scopes.user_id = NEW.user_id
      AND (
        (NEW.deleted_at IS NULL AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL AND decks.owner_id = NEW.user_id)
        OR (OLD.deleted_at IS NULL AND old_notes.deleted_at IS NULL AND old_decks.deleted_at IS NULL AND old_decks.owner_id = OLD.user_id)
        OR NEW.deleted_at IS NOT NULL
      );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sync_cards_owner_update
AFTER UPDATE ON cards
WHEN OLD.user_id <> NEW.user_id
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT OLD.user_id, scopes.scope_id, 'card', OLD.id, 'delete', json_object('id', OLD.id, 'deleted', json('true'))
    FROM sync_owner_scopes AS scopes
    JOIN notes AS old_notes ON old_notes.id = OLD.note_id
    JOIN decks AS old_decks ON old_decks.id = old_notes.deck_id
    WHERE scopes.user_id = OLD.user_id
      AND OLD.deleted_at IS NULL AND old_notes.deleted_at IS NULL
      AND old_decks.deleted_at IS NULL AND old_decks.owner_id = OLD.user_id;
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT NEW.user_id, scopes.scope_id, 'card', NEW.id,
        CASE WHEN NEW.deleted_at IS NULL AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL AND decks.owner_id = NEW.user_id THEN 'upsert' ELSE 'delete' END,
        CASE WHEN NEW.deleted_at IS NULL AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL AND decks.owner_id = NEW.user_id THEN json_remove(json_object(
            'id', NEW.id, 'note_id', NEW.note_id, 'user_id', NEW.user_id, 'direction', NEW.direction,
            'state', NEW.state,
            'due', replace(substr(NEW.due, 1, instr(NEW.due || ' +', ' +') - 1), ' ', 'T') || 'Z',
            'stability', NEW.stability, 'difficulty', NEW.difficulty,
            'elapsed_days', NEW.elapsed_days, 'scheduled_days', NEW.scheduled_days, 'step', NEW.step,
            'reps', NEW.reps, 'lapses', NEW.lapses,
            'last_review', CASE WHEN NEW.last_review IS NULL THEN NULL ELSE replace(substr(NEW.last_review, 1, instr(NEW.last_review || ' +', ' +') - 1), ' ', 'T') || 'Z' END,
            'suspended', json(CASE WHEN NEW.suspended = 1 THEN 'true' ELSE 'false' END),
            'is_leech', json(CASE WHEN NEW.is_leech = 1 THEN 'true' ELSE 'false' END),
            'updated_at', replace(substr(NEW.updated_at, 1, instr(NEW.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
            'deleted', json('false')
        ),
            CASE WHEN NEW.stability IS NULL THEN '$.stability' ELSE '$.__sync_noop' END,
            CASE WHEN NEW.difficulty IS NULL THEN '$.difficulty' ELSE '$.__sync_noop' END,
            CASE WHEN NEW.last_review IS NULL THEN '$.last_review' ELSE '$.__sync_noop' END
        ) ELSE json_object('id', NEW.id, 'deleted', json('true')) END
    FROM sync_owner_scopes AS scopes
    LEFT JOIN notes ON notes.id = NEW.note_id
    LEFT JOIN decks ON decks.id = notes.deck_id
    WHERE scopes.user_id = NEW.user_id
      AND NEW.deleted_at IS NULL AND notes.deleted_at IS NULL
      AND decks.deleted_at IS NULL AND decks.owner_id = NEW.user_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sync_cards_delete
AFTER DELETE ON cards
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT OLD.user_id, scopes.scope_id, 'card', OLD.id, 'delete', json_object('id', OLD.id, 'deleted', json('true'))
    FROM sync_owner_scopes AS scopes WHERE scopes.user_id = OLD.user_id;
END;
-- +goose StatementEnd

-- A note can move between active and inaccessible decks without changing its
-- card rows. Publish card tombstones/upserts for each affected card owner.
-- +goose StatementBegin
CREATE TRIGGER sync_cards_note_deck_update
AFTER UPDATE OF deck_id ON notes
WHEN OLD.deck_id IS NOT NEW.deck_id
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT cards.user_id, scopes.scope_id, 'card', cards.id, 'delete', json_object('id', cards.id, 'deleted', json('true'))
    FROM cards
    JOIN decks AS old_decks ON old_decks.id = OLD.deck_id
    LEFT JOIN decks AS new_decks ON new_decks.id = NEW.deck_id
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = cards.user_id
    WHERE cards.note_id = NEW.id AND cards.deleted_at IS NULL
      AND OLD.deleted_at IS NULL AND old_decks.deleted_at IS NULL
      AND old_decks.owner_id = cards.user_id
      AND (NEW.deleted_at IS NOT NULL OR new_decks.id IS NULL OR new_decks.deleted_at IS NOT NULL OR new_decks.owner_id <> cards.user_id);

    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT cards.user_id, scopes.scope_id, 'card', cards.id, 'upsert', json_remove(json_object(
        'id', cards.id, 'note_id', cards.note_id, 'user_id', cards.user_id, 'direction', cards.direction,
        'state', cards.state,
        'due', replace(substr(cards.due, 1, instr(cards.due || ' +', ' +') - 1), ' ', 'T') || 'Z',
        'stability', cards.stability, 'difficulty', cards.difficulty,
        'elapsed_days', cards.elapsed_days, 'scheduled_days', cards.scheduled_days, 'step', cards.step,
        'reps', cards.reps, 'lapses', cards.lapses,
        'last_review', CASE WHEN cards.last_review IS NULL THEN NULL ELSE replace(substr(cards.last_review, 1, instr(cards.last_review || ' +', ' +') - 1), ' ', 'T') || 'Z' END,
        'suspended', json(CASE WHEN cards.suspended = 1 THEN 'true' ELSE 'false' END),
        'is_leech', json(CASE WHEN cards.is_leech = 1 THEN 'true' ELSE 'false' END),
        'updated_at', replace(substr(cards.updated_at, 1, instr(cards.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
        'deleted', json('false')
    ),
        CASE WHEN cards.stability IS NULL THEN '$.stability' ELSE '$.__sync_noop' END,
        CASE WHEN cards.difficulty IS NULL THEN '$.difficulty' ELSE '$.__sync_noop' END,
        CASE WHEN cards.last_review IS NULL THEN '$.last_review' ELSE '$.__sync_noop' END
    )
    FROM cards
    JOIN decks AS new_decks ON new_decks.id = NEW.deck_id
    LEFT JOIN decks AS old_decks ON old_decks.id = OLD.deck_id
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = cards.user_id
    WHERE cards.note_id = NEW.id AND cards.deleted_at IS NULL
      AND NEW.deleted_at IS NULL AND new_decks.deleted_at IS NULL
      AND new_decks.owner_id = cards.user_id
      AND (OLD.deleted_at IS NOT NULL OR old_decks.id IS NULL OR old_decks.deleted_at IS NOT NULL OR old_decks.owner_id <> cards.user_id);
END;
-- +goose StatementEnd

-- A deck can be reparented out of a deleted ancestor without changing its
-- notes or cards. Refresh those snapshots when the subtree becomes visible.
-- +goose StatementBegin
CREATE TRIGGER sync_decks_visibility_update
AFTER UPDATE OF deleted_at, parent_id ON decks
WHEN OLD.deleted_at IS NOT NEW.deleted_at OR OLD.parent_id IS NOT NEW.parent_id
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT decks.owner_id, scopes.scope_id, 'note', notes.id, 'upsert', json_object(
        'id', notes.id, 'deck_id', notes.deck_id, 'term', notes.term, 'translation', notes.translation,
        'transcription', notes.transcription, 'example', notes.example,
        'example_translation', notes.example_translation, 'notes', notes.notes,
        'tags', json(notes.tags), 'alternatives', json(notes.alternatives), 'fields', json(notes.fields),
        'updated_at', replace(substr(notes.updated_at, 1, instr(notes.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
        'deleted', json('false')
    )
    FROM notes
    JOIN decks ON decks.id = notes.deck_id
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = decks.owner_id
    WHERE notes.deleted_at IS NULL AND decks.deleted_at IS NULL
      AND decks.owner_id = NEW.owner_id
      AND decks.id IN (
        WITH RECURSIVE subtree(id) AS (
            SELECT NEW.id
            UNION ALL
            SELECT child.id FROM decks AS child JOIN subtree ON child.parent_id = subtree.id
            WHERE child.owner_id = NEW.owner_id
        ) SELECT id FROM subtree
      )
      AND NOT EXISTS (
        WITH RECURSIVE ancestors(id, parent_id, deleted_at) AS (
            SELECT id, parent_id, deleted_at FROM decks WHERE id = NEW.id
            UNION ALL
            SELECT parent.id, parent.parent_id, parent.deleted_at
            FROM decks AS parent JOIN ancestors ON parent.id = ancestors.parent_id
        ) SELECT 1 FROM ancestors WHERE deleted_at IS NOT NULL
      )
      AND ((OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL) OR OLD.parent_id IS NOT NEW.parent_id);

    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT cards.user_id, scopes.scope_id, 'card', cards.id, 'upsert', json_remove(json_object(
        'id', cards.id, 'note_id', cards.note_id, 'user_id', cards.user_id, 'direction', cards.direction,
        'state', cards.state,
        'due', replace(substr(cards.due, 1, instr(cards.due || ' +', ' +') - 1), ' ', 'T') || 'Z',
        'stability', cards.stability, 'difficulty', cards.difficulty,
        'elapsed_days', cards.elapsed_days, 'scheduled_days', cards.scheduled_days, 'step', cards.step,
        'reps', cards.reps, 'lapses', cards.lapses,
        'last_review', CASE WHEN cards.last_review IS NULL THEN NULL ELSE replace(substr(cards.last_review, 1, instr(cards.last_review || ' +', ' +') - 1), ' ', 'T') || 'Z' END,
        'suspended', json(CASE WHEN cards.suspended = 1 THEN 'true' ELSE 'false' END),
        'is_leech', json(CASE WHEN cards.is_leech = 1 THEN 'true' ELSE 'false' END),
        'updated_at', replace(substr(cards.updated_at, 1, instr(cards.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
        'deleted', json('false')
    ),
        CASE WHEN cards.stability IS NULL THEN '$.stability' ELSE '$.__sync_noop' END,
        CASE WHEN cards.difficulty IS NULL THEN '$.difficulty' ELSE '$.__sync_noop' END,
        CASE WHEN cards.last_review IS NULL THEN '$.last_review' ELSE '$.__sync_noop' END
    )
    FROM cards
    JOIN notes ON notes.id = cards.note_id
    JOIN decks ON decks.id = notes.deck_id
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = cards.user_id
    WHERE cards.user_id = NEW.owner_id AND decks.owner_id = cards.user_id
      AND decks.id IN (
        WITH RECURSIVE subtree(id) AS (
            SELECT NEW.id
            UNION ALL
            SELECT child.id FROM decks AS child JOIN subtree ON child.parent_id = subtree.id
            WHERE child.owner_id = NEW.owner_id
        ) SELECT id FROM subtree
      )
      AND NOT EXISTS (
        WITH RECURSIVE ancestors(id, parent_id, deleted_at) AS (
            SELECT id, parent_id, deleted_at FROM decks WHERE id = NEW.id
            UNION ALL
            SELECT parent.id, parent.parent_id, parent.deleted_at
            FROM decks AS parent JOIN ancestors ON parent.id = ancestors.parent_id
        ) SELECT 1 FROM ancestors WHERE deleted_at IS NOT NULL
      )
      AND (
        (
          OLD.parent_id IS NOT NEW.parent_id
          AND cards.deleted_at IS NULL AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL
        ) OR (
          OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL
          AND cards.deleted_at = OLD.deleted_at AND cards.deletion_batch = OLD.deletion_batch
          AND notes.deleted_at = OLD.deleted_at AND notes.deletion_batch = OLD.deletion_batch
          AND (decks.deleted_at IS NULL OR (decks.deleted_at = OLD.deleted_at AND decks.deletion_batch = OLD.deletion_batch))
        )
      );
END;
-- +goose StatementEnd

-- Backfill a complete initial snapshot for existing users. New and updated
-- content is captured by the triggers above after this migration commits.
INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
SELECT decks.owner_id, scopes.scope_id, 'note', notes.id, 'upsert', json_object(
    'id', notes.id, 'deck_id', notes.deck_id, 'term', notes.term, 'translation', notes.translation,
    'transcription', notes.transcription, 'example', notes.example,
    'example_translation', notes.example_translation, 'notes', notes.notes,
    'tags', json(notes.tags), 'alternatives', json(notes.alternatives), 'fields', json(notes.fields),
    'updated_at', replace(substr(notes.updated_at, 1, instr(notes.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
    'deleted', json('false')
)
FROM notes
JOIN decks ON decks.id = notes.deck_id
JOIN sync_owner_scopes AS scopes ON scopes.user_id = decks.owner_id
WHERE notes.deleted_at IS NULL AND decks.deleted_at IS NULL
ORDER BY notes.id;

INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
SELECT cards.user_id, scopes.scope_id, 'card', cards.id, 'upsert', json_remove(json_object(
    'id', cards.id, 'note_id', cards.note_id, 'user_id', cards.user_id, 'direction', cards.direction,
    'state', cards.state,
    'due', replace(substr(cards.due, 1, instr(cards.due || ' +', ' +') - 1), ' ', 'T') || 'Z',
    'stability', cards.stability, 'difficulty', cards.difficulty,
    'elapsed_days', cards.elapsed_days, 'scheduled_days', cards.scheduled_days, 'step', cards.step,
    'reps', cards.reps, 'lapses', cards.lapses,
    'last_review', CASE WHEN cards.last_review IS NULL THEN NULL ELSE replace(substr(cards.last_review, 1, instr(cards.last_review || ' +', ' +') - 1), ' ', 'T') || 'Z' END,
    'suspended', json(CASE WHEN cards.suspended = 1 THEN 'true' ELSE 'false' END),
    'is_leech', json(CASE WHEN cards.is_leech = 1 THEN 'true' ELSE 'false' END),
    'updated_at', replace(substr(cards.updated_at, 1, instr(cards.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
    'deleted', json('false')
),
    CASE WHEN cards.stability IS NULL THEN '$.stability' ELSE '$.__sync_noop' END,
    CASE WHEN cards.difficulty IS NULL THEN '$.difficulty' ELSE '$.__sync_noop' END,
    CASE WHEN cards.last_review IS NULL THEN '$.last_review' ELSE '$.__sync_noop' END
)
FROM cards
JOIN notes ON notes.id = cards.note_id
JOIN decks ON decks.id = notes.deck_id
JOIN sync_owner_scopes AS scopes ON scopes.user_id = cards.user_id
WHERE cards.deleted_at IS NULL AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL
  AND cards.user_id = decks.owner_id
ORDER BY cards.id;

-- +goose Down
-- Cursors signed before rollback must not validate against recreated scope IDs
-- and event sequences if this migration is applied again.
DELETE FROM app_settings WHERE key = 'sync_cursor_hmac_key';
DROP TRIGGER sync_cards_delete;
DROP TRIGGER sync_cards_note_deck_update;
DROP TRIGGER sync_cards_owner_update;
DROP TRIGGER sync_cards_update;
DROP TRIGGER sync_cards_insert;
DROP TRIGGER sync_decks_visibility_update;
DROP TRIGGER sync_notes_delete;
DROP TRIGGER sync_notes_update;
DROP TRIGGER sync_notes_insert;
DROP TRIGGER sync_events_append_only_delete;
DROP TRIGGER sync_events_append_only_update;
DROP INDEX sync_events_owner_seq_idx;
DROP TABLE sync_events;
DROP TRIGGER sync_owner_scope_insert;
DROP TABLE sync_owner_scopes;
