-- +goose Up
CREATE TABLE friend_requests (
    id INTEGER PRIMARY KEY,
    sender_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'declined', 'cancelled')),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (sender_id <> recipient_id)
);

CREATE UNIQUE INDEX friend_requests_pending_pair_idx
    ON friend_requests(min(sender_id, recipient_id), max(sender_id, recipient_id))
    WHERE status = 'pending';
CREATE INDEX friend_requests_recipient_status_idx ON friend_requests(recipient_id, status, created_at DESC);
CREATE INDEX friend_requests_sender_status_idx ON friend_requests(sender_id, status, created_at DESC);

CREATE TABLE friendships (
    user_a_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_b_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_a_id, user_b_id),
    CHECK (user_a_id < user_b_id)
);

CREATE TABLE social_settings (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    visible BOOLEAN NOT NULL DEFAULT 0 CHECK (visible IN (0, 1)),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO social_settings(user_id) SELECT id FROM users;

-- A copied share is a completed one-time operation. A subscribed share grants
-- live read-only access to its source subtree while status remains active.
CREATE TABLE deck_shares (
    id INTEGER PRIMARY KEY,
    source_deck_id INTEGER NOT NULL REFERENCES decks(id) ON DELETE CASCADE,
    recipient_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK (mode IN ('copy', 'subscribe')),
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked', 'unsubscribed', 'complete')),
    copied_deck_id INTEGER REFERENCES decks(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (
        (mode = 'copy' AND status = 'complete' AND copied_deck_id IS NOT NULL)
        OR (mode = 'subscribe' AND status IN ('active', 'revoked', 'unsubscribed') AND copied_deck_id IS NULL)
    )
);

CREATE UNIQUE INDEX deck_shares_source_recipient_idx
    ON deck_shares(source_deck_id, recipient_id);
CREATE INDEX deck_shares_recipient_status_idx ON deck_shares(recipient_id, status, mode);
CREATE INDEX deck_shares_source_status_idx ON deck_shares(source_deck_id, status, mode);

-- This view includes soft-deleted source decks so its consumers can emit
-- tombstones. Read queries must separately reject deleted decks and notes.
CREATE VIEW social_subscription_tree(share_id, recipient_id, source_owner_id, deck_id, share_status) AS
WITH RECURSIVE tree AS (
    SELECT shares.id AS share_id, shares.recipient_id AS recipient_id,
           root.owner_id AS source_owner_id, root.id AS deck_id, shares.status AS share_status
    FROM deck_shares AS shares
    JOIN decks AS root ON root.id = shares.source_deck_id
    WHERE shares.mode = 'subscribe'
    UNION
    SELECT tree.share_id AS share_id, tree.recipient_id AS recipient_id,
           tree.source_owner_id AS source_owner_id, child.id AS deck_id, tree.share_status AS share_status
    FROM tree
    JOIN decks AS child ON child.parent_id = tree.deck_id
    WHERE child.owner_id = tree.source_owner_id
)
SELECT tree.share_id AS share_id,
       tree.recipient_id AS recipient_id,
       tree.source_owner_id AS source_owner_id,
       tree.deck_id AS deck_id,
       tree.share_status AS share_status
FROM tree;

-- New accounts have social sharing disabled unless they opt in.
-- +goose StatementBegin
CREATE TRIGGER social_settings_user_insert
AFTER INSERT ON users
BEGIN
    INSERT INTO social_settings(user_id) VALUES (NEW.id);
END;
-- +goose StatementEnd

-- If a friendship ends, revoke both directions of live access. The share
-- trigger below emits tombstones in the same transaction.
-- +goose StatementBegin
CREATE TRIGGER social_friendship_delete
AFTER DELETE ON friendships
BEGIN
    UPDATE deck_shares
    SET status = 'revoked', updated_at = CURRENT_TIMESTAMP
    WHERE mode = 'subscribe' AND status = 'active'
      AND recipient_id IN (OLD.user_a_id, OLD.user_b_id)
      AND source_deck_id IN (
          SELECT id FROM decks
          WHERE (owner_id = OLD.user_a_id AND recipient_id = OLD.user_b_id)
             OR (owner_id = OLD.user_b_id AND recipient_id = OLD.user_a_id)
      );
END;
-- +goose StatementEnd

-- Publish the initial note snapshot to a new subscriber. Card rows are created
-- in the next trigger and their own sync triggers publish their snapshots.
-- +goose StatementBegin
CREATE TRIGGER social_subscription_insert
AFTER INSERT ON deck_shares
WHEN NEW.mode = 'subscribe' AND NEW.status = 'active'
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT NEW.recipient_id, scopes.scope_id, 'note', notes.id, 'upsert', json_object(
        'id', notes.id, 'deck_id', notes.deck_id, 'term', notes.term, 'translation', notes.translation,
        'transcription', notes.transcription, 'example', notes.example,
        'example_translation', notes.example_translation, 'notes', notes.notes,
        'tags', json(notes.tags), 'alternatives', json(notes.alternatives), 'fields', json(notes.fields),
        'updated_at', replace(substr(notes.updated_at, 1, instr(notes.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
        'deleted', json('false')
    )
    FROM social_subscription_tree AS tree
    JOIN notes ON notes.deck_id = tree.deck_id
    JOIN decks ON decks.id = notes.deck_id
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = NEW.recipient_id
    WHERE tree.share_id = NEW.id AND tree.share_status = 'active'
      AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL;

    INSERT OR IGNORE INTO cards(note_id, user_id, direction)
    SELECT notes.id, NEW.recipient_id, directions.direction
    FROM social_subscription_tree AS tree
    JOIN decks ON decks.id = tree.deck_id
    JOIN notes ON notes.deck_id = decks.id
    JOIN (SELECT 'forward' AS direction UNION ALL SELECT 'reverse') AS directions
      ON directions.direction = 'forward' OR decks.reverse_cards = 1
    WHERE tree.share_id = NEW.id AND tree.share_status = 'active'
      AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL;

    UPDATE cards
    SET updated_at = updated_at
    WHERE cards.user_id = NEW.recipient_id
      AND cards.deleted_at IS NULL
      AND cards.note_id IN (
          SELECT notes.id
          FROM social_subscription_tree AS tree
          JOIN notes ON notes.deck_id = tree.deck_id
          JOIN decks ON decks.id = notes.deck_id
          WHERE tree.share_id = NEW.id AND tree.share_status = 'active'
            AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL
      );
END;
-- +goose StatementEnd

-- Moving a source deck can change which notes belong to a shared subtree.
-- Reconcile the moved subtree for old and new subscribers, without exposing
-- snapshots to users who do not have an active share.
-- +goose StatementBegin
CREATE TRIGGER sync_shared_deck_parent_update
AFTER UPDATE OF parent_id ON decks
WHEN OLD.parent_id IS NOT NEW.parent_id
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT DISTINCT former.recipient_id, scopes.scope_id, 'note', notes.id, 'delete',
           json_object('id', notes.id, 'deleted', json('true'))
    FROM notes
    JOIN decks AS note_deck ON note_deck.id = notes.deck_id
    JOIN (
        WITH RECURSIVE moved_subtree(id) AS (
            SELECT NEW.id
            UNION
            SELECT child.id FROM decks AS child
            JOIN moved_subtree AS parent ON child.parent_id = parent.id
            WHERE child.owner_id = OLD.owner_id
        ), prior_path(id, parent_id) AS (
            SELECT OLD.id, OLD.parent_id
            UNION
            SELECT parent.id, parent.parent_id FROM decks AS parent
            JOIN prior_path AS child ON parent.id = child.parent_id
            WHERE parent.owner_id = OLD.owner_id
        ), former AS (
            SELECT DISTINCT shares.recipient_id
            FROM deck_shares AS shares
            JOIN prior_path ON prior_path.id = shares.source_deck_id
            WHERE shares.mode = 'subscribe' AND shares.status = 'active'
        )
        SELECT moved_subtree.id AS deck_id, former.recipient_id
        FROM moved_subtree CROSS JOIN former
    ) AS former ON former.deck_id = note_deck.id
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = former.recipient_id
    WHERE NOT EXISTS (
        SELECT 1 FROM social_subscription_tree AS current_access
        WHERE current_access.recipient_id = former.recipient_id
          AND current_access.deck_id = note_deck.id
          AND current_access.share_status = 'active'
    );

    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT DISTINCT former.recipient_id, scopes.scope_id, 'card', cards.id, 'delete',
           json_object('id', cards.id, 'deleted', json('true'))
    FROM cards
    JOIN notes ON notes.id = cards.note_id
    JOIN decks AS note_deck ON note_deck.id = notes.deck_id
    JOIN (
        WITH RECURSIVE moved_subtree(id) AS (
            SELECT NEW.id
            UNION
            SELECT child.id FROM decks AS child
            JOIN moved_subtree AS parent ON child.parent_id = parent.id
            WHERE child.owner_id = OLD.owner_id
        ), prior_path(id, parent_id) AS (
            SELECT OLD.id, OLD.parent_id
            UNION
            SELECT parent.id, parent.parent_id FROM decks AS parent
            JOIN prior_path AS child ON parent.id = child.parent_id
            WHERE parent.owner_id = OLD.owner_id
        ), former AS (
            SELECT DISTINCT shares.recipient_id
            FROM deck_shares AS shares
            JOIN prior_path ON prior_path.id = shares.source_deck_id
            WHERE shares.mode = 'subscribe' AND shares.status = 'active'
        )
        SELECT moved_subtree.id AS deck_id, former.recipient_id
        FROM moved_subtree CROSS JOIN former
    ) AS former ON former.deck_id = note_deck.id AND former.recipient_id = cards.user_id
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = former.recipient_id
    WHERE NOT EXISTS (
        SELECT 1 FROM social_subscription_tree AS current_access
        WHERE current_access.recipient_id = former.recipient_id
          AND current_access.deck_id = note_deck.id
          AND current_access.share_status = 'active'
    );

    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT DISTINCT access.recipient_id, scopes.scope_id, 'note', notes.id, 'upsert', json_object(
        'id', notes.id, 'deck_id', notes.deck_id, 'term', notes.term, 'translation', notes.translation,
        'transcription', notes.transcription, 'example', notes.example,
        'example_translation', notes.example_translation, 'notes', notes.notes,
        'tags', json(notes.tags), 'alternatives', json(notes.alternatives), 'fields', json(notes.fields),
        'updated_at', replace(substr(notes.updated_at, 1, instr(notes.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
        'deleted', json('false')
    )
    FROM (
        WITH RECURSIVE moved_subtree(id) AS (
            SELECT NEW.id
            UNION
            SELECT child.id FROM decks AS child
            JOIN moved_subtree AS parent ON child.parent_id = parent.id
            WHERE child.owner_id = NEW.owner_id
        )
        SELECT id FROM moved_subtree
    ) AS moved
    JOIN social_subscription_tree AS access ON access.deck_id = moved.id AND access.share_status = 'active'
    JOIN notes ON notes.deck_id = moved.id
    JOIN decks ON decks.id = notes.deck_id
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = access.recipient_id
    WHERE notes.deleted_at IS NULL AND decks.deleted_at IS NULL;

    INSERT OR IGNORE INTO cards(note_id, user_id, direction)
    SELECT notes.id, access.recipient_id, directions.direction
    FROM (
        WITH RECURSIVE moved_subtree(id) AS (
            SELECT NEW.id
            UNION
            SELECT child.id FROM decks AS child
            JOIN moved_subtree AS parent ON child.parent_id = parent.id
            WHERE child.owner_id = NEW.owner_id
        )
        SELECT id FROM moved_subtree
    ) AS moved
    JOIN social_subscription_tree AS access ON access.deck_id = moved.id AND access.share_status = 'active'
    JOIN decks ON decks.id = moved.id
    JOIN notes ON notes.deck_id = decks.id
    JOIN (SELECT 'forward' AS direction UNION ALL SELECT 'reverse') AS directions
      ON directions.direction = 'forward' OR decks.reverse_cards = 1
    WHERE notes.deleted_at IS NULL AND decks.deleted_at IS NULL;

    UPDATE cards
    SET updated_at = updated_at
    WHERE cards.deleted_at IS NULL
      AND cards.note_id IN (
          SELECT notes.id
          FROM (
              WITH RECURSIVE moved_subtree(id) AS (
                  SELECT NEW.id
                  UNION
                  SELECT child.id FROM decks AS child
                  JOIN moved_subtree AS parent ON child.parent_id = parent.id
                  WHERE child.owner_id = NEW.owner_id
              )
              SELECT id FROM moved_subtree
          ) AS moved
          JOIN social_subscription_tree AS access ON access.deck_id = moved.id AND access.share_status = 'active'
          JOIN notes ON notes.deck_id = moved.id
          JOIN decks ON decks.id = notes.deck_id
          WHERE notes.deleted_at IS NULL AND decks.deleted_at IS NULL
            AND access.recipient_id = cards.user_id
      );
END;
-- +goose StatementEnd

-- On revocation, emit only tombstones not still covered by another active
-- subscription to an overlapping source subtree.
-- +goose StatementBegin
CREATE TRIGGER social_subscription_revoke
AFTER UPDATE OF status ON deck_shares
WHEN OLD.mode = 'subscribe' AND OLD.status = 'active' AND NEW.status <> 'active'
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT DISTINCT OLD.recipient_id, scopes.scope_id, 'note', notes.id, 'delete',
           json_object('id', notes.id, 'deleted', json('true'))
    FROM social_subscription_tree AS old_tree
    JOIN notes ON notes.deck_id = old_tree.deck_id
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = OLD.recipient_id
    WHERE old_tree.share_id = OLD.id AND old_tree.share_status <> 'active'
      AND NOT EXISTS (
        SELECT 1 FROM social_subscription_tree AS remaining
        WHERE remaining.recipient_id = OLD.recipient_id AND remaining.deck_id = old_tree.deck_id
          AND remaining.share_status = 'active'
    );

    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT DISTINCT OLD.recipient_id, scopes.scope_id, 'card', cards.id, 'delete',
           json_object('id', cards.id, 'deleted', json('true'))
    FROM social_subscription_tree AS old_tree
    JOIN notes ON notes.deck_id = old_tree.deck_id
    JOIN cards ON cards.note_id = notes.id AND cards.user_id = OLD.recipient_id
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = OLD.recipient_id
    WHERE old_tree.share_id = OLD.id AND old_tree.share_status <> 'active'
      AND NOT EXISTS (
        SELECT 1 FROM social_subscription_tree AS remaining
        WHERE remaining.recipient_id = OLD.recipient_id AND remaining.deck_id = old_tree.deck_id
          AND remaining.share_status = 'active'
    );
END;
-- +goose StatementEnd

-- Source note changes are delivered to every recipient who can currently see
-- the note. Moving/deleting a note first revokes its former snapshot.
-- +goose StatementBegin
CREATE TRIGGER sync_shared_notes_insert
AFTER INSERT ON notes
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT DISTINCT access.recipient_id, scopes.scope_id, 'note', NEW.id,
        CASE WHEN NEW.deleted_at IS NULL AND decks.deleted_at IS NULL THEN 'upsert' ELSE 'delete' END,
        CASE WHEN NEW.deleted_at IS NULL AND decks.deleted_at IS NULL THEN json_object(
            'id', NEW.id, 'deck_id', NEW.deck_id, 'term', NEW.term, 'translation', NEW.translation,
            'transcription', NEW.transcription, 'example', NEW.example,
            'example_translation', NEW.example_translation, 'notes', NEW.notes,
            'tags', json(NEW.tags), 'alternatives', json(NEW.alternatives), 'fields', json(NEW.fields),
            'updated_at', replace(substr(NEW.updated_at, 1, instr(NEW.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
            'deleted', json('false')
        ) ELSE json_object('id', NEW.id, 'deleted', json('true')) END
    FROM social_subscription_tree AS access
    JOIN decks ON decks.id = NEW.deck_id
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = access.recipient_id
    WHERE access.deck_id = NEW.deck_id AND access.share_status = 'active';

    INSERT OR IGNORE INTO cards(note_id, user_id, direction)
    SELECT NEW.id, access.recipient_id, directions.direction
    FROM social_subscription_tree AS access
    JOIN decks ON decks.id = NEW.deck_id
    JOIN (SELECT 'forward' AS direction UNION ALL SELECT 'reverse') AS directions
      ON directions.direction = 'forward' OR decks.reverse_cards = 1
    WHERE access.deck_id = NEW.deck_id AND access.share_status = 'active'
      AND NEW.deleted_at IS NULL AND decks.deleted_at IS NULL;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sync_shared_notes_update
AFTER UPDATE ON notes
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT DISTINCT access.recipient_id, scopes.scope_id, 'note', OLD.id, 'delete',
           json_object('id', OLD.id, 'deleted', json('true'))
    FROM social_subscription_tree AS access
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = access.recipient_id
    WHERE access.deck_id = OLD.deck_id AND access.share_status = 'active'
      AND NOT (OLD.deck_id = NEW.deck_id AND NEW.deleted_at IS NULL AND EXISTS (
          SELECT 1 FROM decks WHERE id = NEW.deck_id AND deleted_at IS NULL
      ))
      AND NOT EXISTS (
          SELECT 1 FROM social_subscription_tree AS remaining
          JOIN decks AS current_deck ON current_deck.id = NEW.deck_id
        WHERE remaining.recipient_id = access.recipient_id AND remaining.share_status = 'active'
            AND remaining.deck_id = NEW.deck_id
            AND NEW.deleted_at IS NULL AND current_deck.deleted_at IS NULL
      );

    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT DISTINCT access.recipient_id, scopes.scope_id, 'note', NEW.id,
        CASE WHEN NEW.deleted_at IS NULL AND decks.deleted_at IS NULL THEN 'upsert' ELSE 'delete' END,
        CASE WHEN NEW.deleted_at IS NULL AND decks.deleted_at IS NULL THEN json_object(
            'id', NEW.id, 'deck_id', NEW.deck_id, 'term', NEW.term, 'translation', NEW.translation,
            'transcription', NEW.transcription, 'example', NEW.example,
            'example_translation', NEW.example_translation, 'notes', NEW.notes,
            'tags', json(NEW.tags), 'alternatives', json(NEW.alternatives), 'fields', json(NEW.fields),
            'updated_at', replace(substr(NEW.updated_at, 1, instr(NEW.updated_at || ' +', ' +') - 1), ' ', 'T') || 'Z',
            'deleted', json('false')
        ) ELSE json_object('id', NEW.id, 'deleted', json('true')) END
    FROM social_subscription_tree AS access
    JOIN decks ON decks.id = NEW.deck_id
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = access.recipient_id
    WHERE access.deck_id = NEW.deck_id AND access.share_status = 'active';
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sync_shared_notes_delete
AFTER DELETE ON notes
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT DISTINCT access.recipient_id, scopes.scope_id, 'note', OLD.id, 'delete',
           json_object('id', OLD.id, 'deleted', json('true'))
    FROM social_subscription_tree AS access
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = access.recipient_id
    WHERE access.deck_id = OLD.deck_id AND access.share_status = 'active';
END;
-- +goose StatementEnd

-- Subscriber cards use the same append-only feed as owned cards, but their
-- authorization is the active subscription rather than deck ownership.
-- +goose StatementBegin
CREATE TRIGGER sync_shared_cards_insert
AFTER INSERT ON cards
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT DISTINCT cards.user_id, scopes.scope_id, 'card', cards.id,
        CASE WHEN cards.deleted_at IS NULL AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL THEN 'upsert' ELSE 'delete' END,
        CASE WHEN cards.deleted_at IS NULL AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL THEN json_remove(json_object(
            'id', cards.id, 'note_id', cards.note_id, 'user_id', cards.user_id, 'direction', cards.direction,
            'state', cards.state, 'due', replace(substr(cards.due, 1, instr(cards.due || ' +', ' +') - 1), ' ', 'T') || 'Z',
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
        ) ELSE json_object('id', cards.id, 'deleted', json('true')) END
    FROM cards
    JOIN notes ON notes.id = cards.note_id
    JOIN decks ON decks.id = notes.deck_id
    JOIN social_subscription_tree AS access ON access.recipient_id = cards.user_id AND access.deck_id = decks.id AND access.share_status = 'active'
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = cards.user_id
    WHERE cards.id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sync_shared_cards_update
AFTER UPDATE ON cards
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT DISTINCT OLD.user_id, scopes.scope_id, 'card', OLD.id, 'delete',
           json_object('id', OLD.id, 'deleted', json('true'))
    FROM notes
    JOIN decks ON decks.id = notes.deck_id
    JOIN social_subscription_tree AS access ON access.recipient_id = OLD.user_id AND access.deck_id = decks.id AND access.share_status = 'active'
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = OLD.user_id
    WHERE notes.id = OLD.note_id
      AND (NEW.deleted_at IS NOT NULL OR notes.deleted_at IS NOT NULL OR decks.deleted_at IS NOT NULL OR NEW.user_id <> OLD.user_id);

    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT DISTINCT cards.user_id, scopes.scope_id, 'card', cards.id,
        CASE WHEN cards.deleted_at IS NULL AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL THEN 'upsert' ELSE 'delete' END,
        CASE WHEN cards.deleted_at IS NULL AND notes.deleted_at IS NULL AND decks.deleted_at IS NULL THEN json_remove(json_object(
            'id', cards.id, 'note_id', cards.note_id, 'user_id', cards.user_id, 'direction', cards.direction,
            'state', cards.state, 'due', replace(substr(cards.due, 1, instr(cards.due || ' +', ' +') - 1), ' ', 'T') || 'Z',
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
        ) ELSE json_object('id', cards.id, 'deleted', json('true')) END
    FROM cards
    JOIN notes ON notes.id = cards.note_id
    JOIN decks ON decks.id = notes.deck_id
    JOIN social_subscription_tree AS access ON access.recipient_id = cards.user_id AND access.deck_id = decks.id AND access.share_status = 'active'
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = cards.user_id
    WHERE cards.id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sync_shared_cards_delete
AFTER DELETE ON cards
BEGIN
    INSERT INTO sync_events(owner_id, owner_scope_id, entity_type, entity_id, operation, payload)
    SELECT DISTINCT OLD.user_id, scopes.scope_id, 'card', OLD.id, 'delete',
           json_object('id', OLD.id, 'deleted', json('true'))
    FROM notes
    JOIN decks ON decks.id = notes.deck_id
    JOIN social_subscription_tree AS access ON access.recipient_id = OLD.user_id AND access.deck_id = decks.id AND access.share_status = 'active'
    JOIN sync_owner_scopes AS scopes ON scopes.user_id = OLD.user_id
    WHERE notes.id = OLD.note_id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER sync_shared_deck_parent_update;
DROP TRIGGER sync_shared_cards_delete;
DROP TRIGGER sync_shared_cards_update;
DROP TRIGGER sync_shared_cards_insert;
DROP TRIGGER sync_shared_notes_delete;
DROP TRIGGER sync_shared_notes_update;
DROP TRIGGER sync_shared_notes_insert;
DROP TRIGGER social_subscription_revoke;
DROP TRIGGER social_subscription_insert;
DROP TRIGGER social_friendship_delete;
DROP TRIGGER social_settings_user_insert;
DROP VIEW social_subscription_tree;
DROP INDEX deck_shares_source_status_idx;
DROP INDEX deck_shares_recipient_status_idx;
DROP INDEX deck_shares_source_recipient_idx;
DROP TABLE deck_shares;
DROP TABLE social_settings;
DROP TABLE friendships;
DROP INDEX friend_requests_sender_status_idx;
DROP INDEX friend_requests_recipient_status_idx;
DROP INDEX friend_requests_pending_pair_idx;
DROP TABLE friend_requests;
