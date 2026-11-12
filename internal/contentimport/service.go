package contentimport

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/importer"
	"github.com/pestit/retain/internal/importer/adapters"
	"github.com/pestit/retain/internal/lang"
	"golang.org/x/text/unicode/norm"
	"modernc.org/sqlite"
)

var (
	ErrInvalidInput = errors.New("invalid import input")
	ErrNotFound     = errors.New("import resource not found")
	ErrConflict     = errors.New("import conflicts with existing content")
	ErrInternal     = errors.New("internal content import error")
)

const maxSourceBytes = 100 << 20

// Service implements preview/apply, history, and export for a single database.
type Service struct {
	database *db.DB
	clock    clock.Clock
}

// NewService creates a content import service backed by database and c.
func NewService(database *db.DB, c clock.Clock) *Service {
	if c == nil {
		c = clock.Real{}
	}
	return &Service{database: database, clock: c}
}

// Preview parses a source file, compares it to owner content, and persists the
// compressed source with a digest so the preview can be applied later.
func (s *Service) Preview(ctx context.Context, ownerID int64, filename, adapterName string, raw []byte) (PreviewResult, error) {
	if !s.ready() || ownerID <= 0 {
		return PreviewResult{}, ErrInternal
	}
	if len(raw) == 0 || len(raw) > maxSourceBytes || !utf8.Valid(raw) {
		return PreviewResult{}, ErrInvalidInput
	}
	filename = safeFilename(filename)
	adapter, bundle, err := parseSource(raw, adapterName)
	if err != nil {
		return PreviewResult{}, err
	}
	source, err := sourceName(bundle, adapter.Name())
	if err != nil {
		return PreviewResult{}, err
	}
	if err := validateUniqueBundle(bundle); err != nil {
		return PreviewResult{}, err
	}

	result := PreviewResult{Adapter: adapter.Name(), Status: "previewed"}
	result.BatchID, err = newBatchID()
	if err != nil {
		return PreviewResult{}, ErrInternal
	}
	result.Summary.Source = source
	result.Summary.Tree, result.Summary.Counts, result.Summary.DeckCounts, result.Summary.Warnings, err = s.previewTree(ctx, ownerID, bundle.Decks, source, nil, false, "")
	if err != nil {
		return PreviewResult{}, err
	}
	compressed, err := gzipSource(raw)
	if err != nil {
		return PreviewResult{}, ErrInternal
	}
	digest := sha256.Sum256(raw)
	metadata, err := json.Marshal(result.Summary)
	if err != nil {
		return PreviewResult{}, ErrInternal
	}
	var nullableFilename sql.NullString
	if filename != "" {
		nullableFilename = sql.NullString{String: filename, Valid: true}
	}
	tx, err := s.database.SQL.BeginTx(ctx, nil)
	if err != nil {
		return PreviewResult{}, ErrInternal
	}
	defer func() { _ = tx.Rollback() }()
	queries := s.database.Queries().WithTx(tx)
	_, err = queries.CreateImportBatch(ctx, gen.CreateImportBatchParams{
		ID: result.BatchID, UserID: ownerID, Adapter: adapter.Name(), Filename: nullableFilename,
		Raw: compressed, RawSha256: digest[:],
	})
	if err != nil {
		return PreviewResult{}, ErrInternal
	}
	if _, err := queries.UpdateImportBatch(ctx, gen.UpdateImportBatchParams{
		Adapter: adapter.Name(), Filename: nullableFilename, Raw: compressed, RawSha256: digest[:],
		Status: "previewed", Summary: string(metadata), ID: result.BatchID, UserID: ownerID,
	}); err != nil {
		return PreviewResult{}, ErrInternal
	}
	if err := tx.Commit(); err != nil {
		return PreviewResult{}, ErrInternal
	}
	return result, nil
}

// Apply imports a previously previewed batch in one transaction.
func (s *Service) Apply(ctx context.Context, ownerID int64, batchID string, input ApplyInput) (PreviewResult, error) {
	if !s.ready() || ownerID <= 0 {
		return PreviewResult{}, ErrInternal
	}
	if !validBatchID(batchID) || !validApplyInput(input) {
		return PreviewResult{}, ErrInvalidInput
	}
	if input.OnMissing == "" {
		input.OnMissing = "keep"
	}
	tx, err := s.database.SQL.BeginTx(ctx, nil)
	if err != nil {
		return PreviewResult{}, ErrInternal
	}
	defer func() { _ = tx.Rollback() }()
	queries := s.database.Queries().WithTx(tx)
	batch, err := queries.GetImportBatch(ctx, gen.GetImportBatchParams{ID: batchID, UserID: ownerID})
	if errors.Is(err, sql.ErrNoRows) {
		return PreviewResult{}, ErrNotFound
	}
	if err != nil {
		return PreviewResult{}, ErrInternal
	}
	if batch.Status != "previewed" {
		return PreviewResult{}, ErrConflict
	}
	adapter, ok := adapterByName(batch.Adapter)
	if !ok {
		return PreviewResult{}, ErrInvalidInput
	}
	raw, err := ungzipSource(batch.Raw)
	if err != nil || len(raw) > maxSourceBytes {
		return PreviewResult{}, ErrInternal
	}
	digest := sha256.Sum256(raw)
	if !bytes.Equal(digest[:], batch.RawSha256) {
		return PreviewResult{}, ErrInternal
	}
	bundle, err := adapter.Parse(raw)
	if err != nil {
		return PreviewResult{}, ErrInvalidInput
	}
	source, err := sourceName(bundle, adapter.Name())
	if err != nil || validateUniqueBundle(bundle) != nil {
		return PreviewResult{}, ErrInvalidInput
	}
	var summary Summary
	if err := json.Unmarshal([]byte(batch.Summary), &summary); err != nil || summary.Source != source {
		return PreviewResult{}, ErrInternal
	}
	if input.ParentDeckID != nil {
		if *input.ParentDeckID <= 0 {
			return PreviewResult{}, ErrInvalidInput
		}
		if _, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: *input.ParentDeckID, OwnerID: ownerID}); errors.Is(err, sql.ErrNoRows) {
			return PreviewResult{}, ErrNotFound
		} else if err != nil {
			return PreviewResult{}, ErrInternal
		}
	}
	if err := s.applyDecks(ctx, tx, queries, ownerID, batchID, source, bundle.Decks, input.ParentDeckID, true, "", input); err != nil {
		return PreviewResult{}, err
	}
	metadata, err := json.Marshal(summary)
	if err != nil {
		return PreviewResult{}, ErrInternal
	}
	now := s.now()
	_, err = queries.UpdateImportBatch(ctx, gen.UpdateImportBatchParams{
		Adapter: batch.Adapter, Filename: batch.Filename, Raw: batch.Raw, RawSha256: batch.RawSha256,
		Status: "applied", Summary: string(metadata), AppliedAt: sql.NullTime{Time: now, Valid: true},
		ID: batch.ID, UserID: ownerID,
	})
	if err != nil {
		return PreviewResult{}, ErrInternal
	}
	if err := tx.Commit(); err != nil {
		return PreviewResult{}, ErrInternal
	}
	return PreviewResult{BatchID: batch.ID, Adapter: batch.Adapter, Status: "applied", Summary: summary}, nil
}

// History returns only the caller's import batches, newest first.
func (s *Service) History(ctx context.Context, ownerID int64) ([]Batch, error) {
	if !s.ready() || ownerID <= 0 {
		return nil, ErrInternal
	}
	rows, err := s.database.Queries().ListImportBatches(ctx, ownerID)
	if err != nil {
		return nil, ErrInternal
	}
	result := make([]Batch, 0, len(rows))
	for _, row := range rows {
		var summary Summary
		if err := json.Unmarshal([]byte(row.Summary), &summary); err != nil {
			return nil, ErrInternal
		}
		item := Batch{
			ID: row.ID, Adapter: row.Adapter, SHA256: hex.EncodeToString(row.RawSha256),
			Status: row.Status, Summary: summary, CreatedAt: row.CreatedAt.UTC(),
		}
		if row.Filename.Valid {
			filename := row.Filename.String
			item.Filename = &filename
		}
		if row.AppliedAt.Valid {
			appliedAt := row.AppliedAt.Time.UTC()
			item.AppliedAt = &appliedAt
		}
		result = append(result, item)
	}
	return result, nil
}

// Export returns a retain/v1 JSON or semicolon CSV download for a deck subtree.
func (s *Service) Export(ctx context.Context, ownerID, deckID int64, format string) (Export, error) {
	if !s.ready() || ownerID <= 0 || deckID <= 0 {
		return Export{}, ErrInvalidInput
	}
	if format != "retain" && format != "csv" {
		return Export{}, ErrInvalidInput
	}
	queries := s.database.Queries()
	deck, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: deckID, OwnerID: ownerID})
	if errors.Is(err, sql.ErrNoRows) {
		return Export{}, ErrNotFound
	}
	if err != nil {
		return Export{}, ErrInternal
	}
	allDecks, err := queries.ListDecksByOwner(ctx, ownerID)
	if err != nil {
		return Export{}, ErrInternal
	}
	byParent := make(map[int64][]gen.Deck)
	for _, item := range allDecks {
		if item.ParentID.Valid {
			byParent[item.ParentID.Int64] = append(byParent[item.ParentID.Int64], item)
		}
	}
	for id := range byParent {
		sort.Slice(byParent[id], func(i, j int) bool {
			if byParent[id][i].Position != byParent[id][j].Position {
				return byParent[id][i].Position < byParent[id][j].Position
			}
			return byParent[id][i].ID < byParent[id][j].ID
		})
	}
	var exportDeck func(gen.Deck) (importer.Deck, error)
	exportDeck = func(row gen.Deck) (importer.Deck, error) {
		result := importer.Deck{
			Name: row.Name, LangTarget: nullString(row.LangTarget), LangNative: nullString(row.LangNative),
			ExpectedCount: nullInt64(row.ExpectedCount),
		}
		if row.ExternalID.Valid {
			result.ExternalID = row.ExternalID.String
		}
		notes, err := queries.ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: row.ID, OwnerID: ownerID})
		if err != nil {
			return importer.Deck{}, err
		}
		result.Notes = make([]importer.Note, 0, len(notes))
		for _, note := range notes {
			var tags, alternatives []string
			var fields map[string]any
			if json.Unmarshal([]byte(note.Tags), &tags) != nil || json.Unmarshal([]byte(note.Alternatives), &alternatives) != nil || json.Unmarshal([]byte(note.Fields), &fields) != nil {
				return importer.Deck{}, ErrInternal
			}
			item := importer.Note{
				Term: note.Term, Translation: note.Translation, Transcription: nullString(note.Transcription),
				Example: nullString(note.Example), ExampleTranslation: nullString(note.ExampleTranslation),
				Notes: nullString(note.Notes), Tags: tags, Alternatives: alternatives, Fields: fields,
			}
			if note.ExternalID.Valid {
				item.ExternalID = note.ExternalID.String
			}
			result.Notes = append(result.Notes, item)
		}
		result.Decks = make([]importer.Deck, 0, len(byParent[row.ID]))
		for _, child := range byParent[row.ID] {
			converted, err := exportDeck(child)
			if err != nil {
				return importer.Deck{}, err
			}
			result.Decks = append(result.Decks, converted)
		}
		return result, nil
	}
	root, err := exportDeck(deck)
	if err != nil {
		if errors.Is(err, ErrInternal) {
			return Export{}, err
		}
		return Export{}, ErrInternal
	}
	bundle := importer.Bundle{Format: "retain/v1", Decks: []importer.Deck{root}}
	if deck.Source.Valid {
		bundle.Source = deck.Source.String
	}
	filename := fmt.Sprintf("deck-%d.%s", deckID, format)
	if format == "retain" {
		body, err := json.MarshalIndent(bundle, "", "  ")
		if err != nil {
			return Export{}, ErrInternal
		}
		return Export{Filename: filename, ContentType: "application/json; charset=utf-8", Body: body}, nil
	}
	body, err := exportCSV(root)
	if err != nil {
		return Export{}, ErrInternal
	}
	return Export{Filename: fmt.Sprintf("deck-%d.csv", deckID), ContentType: "text/csv; charset=utf-8", Body: body}, nil
}

func (s *Service) previewTree(ctx context.Context, ownerID int64, decks []importer.Deck, source string, parentID *int64, parentKnown bool, inheritedLangTarget string) ([]PreviewDeck, Counts, DeckCounts, []string, error) {
	tree := make([]PreviewDeck, 0, len(decks))
	var total Counts
	var deckTotal DeckCounts
	warnings := make([]string, 0)
	queries := s.database.Queries()
	for _, deck := range decks {
		existing, found, err := findDeck(ctx, s.database.SQL, queries, ownerID, source, deck, parentID, parentKnown)
		if err != nil {
			return nil, Counts{}, DeckCounts{}, nil, err
		}
		item := PreviewDeck{ExternalID: deck.ExternalID, Name: deck.Name, Status: "new", Warnings: []string{}, Decks: []PreviewDeck{}}
		var existingID *int64
		if found {
			id := existing.ID
			existingID = &id
			item.Status = "unchanged"
			if deckPreviewChanged(existing, deck) {
				item.Status = "changed"
			}
		}
		effectiveLangTarget := previewLangTarget(deck, existing, found, inheritedLangTarget)
		switch item.Status {
		case "new":
			deckTotal.New++
		case "changed":
			deckTotal.Changed++
		default:
			deckTotal.Unchanged++
		}
		for _, note := range deck.Notes {
			normalizedNote, err := enrichImportNote(effectiveLangTarget, note)
			if err != nil {
				return nil, Counts{}, DeckCounts{}, nil, err
			}
			status := "new"
			if found {
				existingNote, noteFound, _, err := findNote(ctx, queries, ownerID, existing.ID, normalizedNote)
				if err != nil {
					return nil, Counts{}, DeckCounts{}, nil, err
				}
				if noteFound {
					status = "unchanged"
					if noteChanged(existingNote, normalizedNote) {
						status = "changed"
					}
				}
			}
			addStatus(&item.Counts, status)
			addStatus(&total, status)
		}
		if deck.ExpectedCount != nil && *deck.ExpectedCount != int64(len(deck.Notes)) {
			item.Warnings = append(item.Warnings, fmt.Sprintf("Expected %d notes, found %d.", *deck.ExpectedCount, len(deck.Notes)))
		}
		childLookupParent := existingID
		if childLookupParent == nil {
			missingParent := int64(-1)
			childLookupParent = &missingParent
		}
		children, counts, childDeckCounts, childWarnings, err := s.previewTree(ctx, ownerID, deck.Decks, source, childLookupParent, true, effectiveLangTarget)
		if err != nil {
			return nil, Counts{}, DeckCounts{}, nil, err
		}
		item.Decks = children
		item.Counts.New += counts.New
		item.Counts.Changed += counts.Changed
		item.Counts.Unchanged += counts.Unchanged
		total.New += counts.New
		total.Changed += counts.Changed
		total.Unchanged += counts.Unchanged
		deckTotal.New += childDeckCounts.New
		deckTotal.Changed += childDeckCounts.Changed
		deckTotal.Unchanged += childDeckCounts.Unchanged
		warnings = append(warnings, item.Warnings...)
		warnings = append(warnings, childWarnings...)
		tree = append(tree, item)
	}
	return tree, total, deckTotal, warnings, nil
}

func previewLangTarget(deck importer.Deck, existing gen.Deck, found bool, inherited string) string {
	if deck.LangTarget != "" {
		return deck.LangTarget
	}
	if inherited != "" {
		return inherited
	}
	if found && existing.LangTarget.Valid {
		return existing.LangTarget.String
	}
	// PLAN.md defaults school-wordsets imports to German when the preview has no
	// persisted or inherited language yet. New notes are still counted as new.
	return "de"
}

func (s *Service) applyDecks(ctx context.Context, tx *sql.Tx, queries *gen.Queries, ownerID int64, batchID, source string, decks []importer.Deck, parentID *int64, roots bool, inheritedLangTarget string, input ApplyInput) error {
	for position, deck := range decks {
		parent := parentID
		if roots {
			parent = input.ParentDeckID
		}
		row, found, err := findDeck(ctx, tx, queries, ownerID, source, deck, parent, true)
		if err != nil {
			return err
		}
		langTarget, langNative := deck.LangTarget, deck.LangNative
		if roots && langTarget == "" {
			// The pair selected on the first import becomes the inherited language
			// context for later previews when the source itself has no language.
			if found && row.LangTarget.Valid {
				langTarget = row.LangTarget.String
			} else {
				langTarget = input.LangTarget
			}
		}
		if roots && langNative == "" {
			if found && row.LangNative.Valid {
				langNative = row.LangNative.String
			} else {
				langNative = input.LangNative
			}
		}
		effectiveLangTarget := deck.LangTarget
		if effectiveLangTarget == "" {
			if roots {
				effectiveLangTarget = langTarget
			} else {
				effectiveLangTarget = inheritedLangTarget
			}
		}
		if found && row.DeletedAt.Valid {
			row, err = restoreDeck(ctx, tx, ownerID, row.ID, s.now())
			if err != nil {
				return ErrConflict
			}
		}
		if found {
			updated, err := queries.UpdateDeck(ctx, gen.UpdateDeckParams{
				ParentID: nullableInt64(parent), Name: deck.Name, Description: row.Description,
				Position: int64(position), LangTarget: nullableStringPtr(langTarget), LangNative: nullableStringPtr(langNative),
				ReverseCards: row.ReverseCards, WrongArticleRating: row.WrongArticleRating, ColorSeed: row.ColorSeed,
				Source: sql.NullString{String: source, Valid: true}, ExternalID: nullableString(deck.ExternalID),
				ExpectedCount: expectedCount(deck.ExpectedCount, row.ExpectedCount), SourceDeckID: row.SourceDeckID,
				UpdatedAt: s.now(), ID: row.ID, OwnerID: ownerID,
			})
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConflict
			}
			if err != nil {
				if isUniqueConstraint(err) {
					return ErrConflict
				}
				return ErrInternal
			}
			row = updated
		} else {
			created, err := queries.CreateDeck(ctx, gen.CreateDeckParams{
				OwnerID: ownerID, ParentID: nullableInt64(parent), Name: deck.Name,
				Position: int64(position), LangTarget: nullableStringPtr(langTarget), LangNative: nullableStringPtr(langNative),
				WrongArticleRating: 2, Source: sql.NullString{String: source, Valid: true},
				ExternalID: nullableString(deck.ExternalID), ExpectedCount: nullableExpected(deck.ExpectedCount),
			})
			if errors.Is(err, sql.ErrNoRows) || isUniqueConstraint(err) {
				return ErrConflict
			}
			if err != nil {
				return ErrInternal
			}
			row = created
		}
		seen := make(map[int64]struct{}, len(deck.Notes))
		for _, note := range deck.Notes {
			normalizedNote, err := enrichImportNote(effectiveLangTarget, note)
			if err != nil {
				return err
			}
			current, noteFound, deleted, err := findNote(ctx, queries, ownerID, row.ID, normalizedNote)
			if err != nil {
				return err
			}
			if deleted {
				if err := restoreNote(ctx, queries, ownerID, row.ID, current, note, s.now()); err != nil {
					return ErrConflict
				}
			}
			if noteFound {
				if normalizedNote.ExternalID != "" && current.ExternalID.Valid && current.ExternalID.String != normalizedNote.ExternalID {
					return ErrConflict
				}
				values := noteParams(ownerID, row.ID, batchID, normalizedNote, current.ExternalID)
				updated, err := queries.UpdateNote(ctx, gen.UpdateNoteParams{
					Term: values.Term, TermKey: values.TermKey, Translation: values.Translation,
					Transcription: values.Transcription, Example: values.Example,
					ExampleTranslation: values.ExampleTranslation, Notes: values.Notes,
					Tags: values.Tags, Alternatives: values.Alternatives, Fields: values.Fields,
					ExternalID: values.ExternalID, ImportBatchID: values.ImportBatchID,
					UpdatedAt: s.now(), ID: current.ID, OwnerID: ownerID,
				})
				if errors.Is(err, sql.ErrNoRows) {
					return ErrConflict
				}
				if err != nil {
					if isUniqueConstraint(err) {
						return ErrConflict
					}
					return ErrInternal
				}
				current = updated
			} else {
				created, err := queries.CreateNote(ctx, noteParams(ownerID, row.ID, batchID, normalizedNote, sql.NullString{}))
				if errors.Is(err, sql.ErrNoRows) || isUniqueConstraint(err) {
					return ErrConflict
				}
				if err != nil {
					return ErrInternal
				}
				current = created
			}
			seen[current.ID] = struct{}{}
			if _, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: current.ID, UserID: ownerID, Direction: "forward"}); err != nil {
				return ErrInternal
			}
			if row.ReverseCards {
				if _, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: current.ID, UserID: ownerID, Direction: "reverse"}); err != nil {
					return ErrInternal
				}
			}
		}
		if input.OnMissing == "suspend" {
			if err := suspendMissing(ctx, tx, ownerID, row.ID, batchID, source, seen, s.now()); err != nil {
				return ErrInternal
			}
		}
		childID := row.ID
		if err := s.applyDecks(ctx, tx, queries, ownerID, batchID, source, deck.Decks, &childID, false, effectiveLangTarget, input); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ready() bool {
	return s != nil && s.database != nil && s.database.SQL != nil && s.clock != nil
}

func (s *Service) now() time.Time { return s.clock.Now().UTC() }

type queryRunner interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func findDeck(ctx context.Context, runner queryRunner, queries *gen.Queries, ownerID int64, source string, incoming importer.Deck, parentID *int64, parentKnown bool) (gen.Deck, bool, error) {
	if incoming.ExternalID != "" {
		params := gen.GetDeckBySourceExternalIDParams{
			OwnerID: ownerID, Source: sql.NullString{String: source, Valid: true},
			ExternalID: sql.NullString{String: incoming.ExternalID, Valid: true},
		}
		row, err := queries.GetDeckBySourceExternalID(ctx, params)
		if err == nil {
			return row, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return gen.Deck{}, false, ErrInternal
		}
		row, err = queries.GetDeletedDeckBySourceExternalID(ctx, gen.GetDeletedDeckBySourceExternalIDParams(params))
		if err == nil {
			return row, true, nil
		}
		if errors.Is(err, sql.ErrNoRows) {
			return gen.Deck{}, false, nil
		}
		return gen.Deck{}, false, ErrInternal
	}
	return findDeckByName(ctx, runner, queries, ownerID, source, incoming.Name, parentID, parentKnown)
}

func findDeckByName(ctx context.Context, runner queryRunner, queries *gen.Queries, ownerID int64, source, name string, parentID *int64, parentKnown bool) (gen.Deck, bool, error) {
	rows, err := queries.ListDecksByOwner(ctx, ownerID)
	if err != nil {
		return gen.Deck{}, false, ErrInternal
	}
	var matches []gen.Deck
	for _, row := range rows {
		if row.Source.Valid && row.Source.String == source && !row.ExternalID.Valid && row.Name == name && (!parentKnown || sameParent(row.ParentID, parentID)) {
			matches = append(matches, row)
		}
	}
	deletedRows, err := queriesDeletedDecksByName(ctx, runner, ownerID, source, name, parentID, parentKnown)
	if err != nil {
		return gen.Deck{}, false, ErrInternal
	}
	matches = append(matches, deletedRows...)
	if len(matches) > 1 {
		return gen.Deck{}, false, ErrConflict
	}
	if len(matches) == 0 {
		return gen.Deck{}, false, nil
	}
	return matches[0], true, nil
}

func queriesDeletedDecksByName(ctx context.Context, runner queryRunner, ownerID int64, source, name string, parentID *int64, parentKnown bool) ([]gen.Deck, error) {
	// sqlc's public query set intentionally lists only active decks; this query
	// is limited to restoring a name-matched, source-owned tombstone.
	var parent any
	if parentID != nil {
		parent = *parentID
	}
	rows, err := runner.QueryContext(ctx, `
		SELECT * FROM decks
		WHERE owner_id = ? AND source = ? AND external_id IS NULL
		  AND name = ? AND (? = 0 OR parent_id IS ?) AND deleted_at IS NOT NULL
		ORDER BY updated_at DESC, id DESC LIMIT 2`, ownerID, source, name, parentKnown, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []gen.Deck
	for rows.Next() {
		var row gen.Deck
		if err := scanDeck(rows, &row); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func restoreDeck(ctx context.Context, tx *sql.Tx, ownerID, id int64, now time.Time) (gen.Deck, error) {
	row := tx.QueryRowContext(ctx, `
		UPDATE decks
		SET deleted_at = NULL, deletion_batch = NULL, updated_at = ?
		WHERE id = ? AND owner_id = ? AND deleted_at IS NOT NULL AND deletion_batch IS NOT NULL
		RETURNING *`, now, id, ownerID)
	var result gen.Deck
	err := scanDeck(row, &result)
	return result, err
}

func findNote(ctx context.Context, queries *gen.Queries, ownerID, deckID int64, incoming importer.Note) (gen.Note, bool, bool, error) {
	var externalActive, externalDeleted, keyActive, keyDeleted gen.Note
	var extActiveFound, extDeletedFound, keyActiveFound, keyDeletedFound bool
	if incoming.ExternalID != "" {
		params := gen.GetNoteByExternalIDParams{DeckID: deckID, OwnerID: ownerID, ExternalID: sql.NullString{String: incoming.ExternalID, Valid: true}}
		row, err := queries.GetNoteByExternalID(ctx, params)
		if err == nil {
			externalActive, extActiveFound = row, true
		} else if !errors.Is(err, sql.ErrNoRows) {
			return gen.Note{}, false, false, ErrInternal
		}
		row, err = queries.GetDeletedNoteByExternalID(ctx, gen.GetDeletedNoteByExternalIDParams(params))
		if err == nil {
			externalDeleted, extDeletedFound = row, true
		} else if !errors.Is(err, sql.ErrNoRows) {
			return gen.Note{}, false, false, ErrInternal
		}
	}
	key := termKey(strings.TrimSpace(incoming.Term))
	row, err := queries.GetNoteByTermKey(ctx, gen.GetNoteByTermKeyParams{DeckID: deckID, OwnerID: ownerID, TermKey: key})
	if err == nil {
		keyActive, keyActiveFound = row, true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return gen.Note{}, false, false, ErrInternal
	}
	row, err = queries.GetDeletedNoteByTermKey(ctx, gen.GetDeletedNoteByTermKeyParams{DeckID: deckID, OwnerID: ownerID, TermKey: key})
	if err == nil {
		keyDeleted, keyDeletedFound = row, true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return gen.Note{}, false, false, ErrInternal
	}
	var extCandidate, keyCandidate gen.Note
	var hasExt, hasKey bool
	if extActiveFound {
		extCandidate, hasExt = externalActive, true
	} else if extDeletedFound {
		extCandidate, hasExt = externalDeleted, true
	}
	if keyActiveFound {
		keyCandidate, hasKey = keyActive, true
	} else if keyDeletedFound {
		keyCandidate, hasKey = keyDeleted, true
	}
	if hasExt && hasKey && extCandidate.ID != keyCandidate.ID {
		return gen.Note{}, false, false, ErrConflict
	}
	if hasExt {
		if incoming.ExternalID != "" && extCandidate.ExternalID.Valid && extCandidate.ExternalID.String != incoming.ExternalID {
			return gen.Note{}, false, false, ErrConflict
		}
		return extCandidate, true, extCandidate.DeletedAt.Valid, nil
	}
	if hasKey {
		if incoming.ExternalID != "" && keyCandidate.ExternalID.Valid && keyCandidate.ExternalID.String != incoming.ExternalID {
			return gen.Note{}, false, false, ErrConflict
		}
		return keyCandidate, true, keyCandidate.DeletedAt.Valid, nil
	}
	return gen.Note{}, false, false, nil
}

func restoreNote(ctx context.Context, queries *gen.Queries, ownerID, deckID int64, current gen.Note, incoming importer.Note, now time.Time) error {
	if current.ExternalID.Valid && (incoming.ExternalID == "" || incoming.ExternalID == current.ExternalID.String) {
		_, err := queries.RestoreNoteByExternalID(ctx, gen.RestoreNoteByExternalIDParams{
			DeckID: deckID, OwnerID: ownerID, ExternalID: current.ExternalID,
			UpdatedAt: now,
		})
		return err
	}
	_, err := queries.RestoreNoteByTermKey(ctx, gen.RestoreNoteByTermKeyParams{
		DeckID: deckID, OwnerID: ownerID, TermKey: termKey(strings.TrimSpace(incoming.Term)), UpdatedAt: now,
	})
	return err
}

func noteParams(ownerID, deckID int64, batchID string, note importer.Note, existingExternal sql.NullString) gen.CreateNoteParams {
	term := strings.TrimSpace(note.Term)
	translation := strings.TrimSpace(note.Translation)
	if note.Tags == nil {
		note.Tags = []string{}
	}
	if note.Alternatives == nil {
		note.Alternatives = []string{}
	}
	if note.Fields == nil {
		note.Fields = map[string]any{}
	}
	tags, _ := json.Marshal(note.Tags)
	alternatives, _ := json.Marshal(note.Alternatives)
	fields, _ := json.Marshal(note.Fields)
	external := existingExternal
	if note.ExternalID != "" {
		external = sql.NullString{String: note.ExternalID, Valid: true}
	}
	return gen.CreateNoteParams{
		Term: term, TermKey: termKey(term), Translation: translation,
		Transcription: optionalNullable(note.Transcription), Example: optionalNullable(note.Example),
		ExampleTranslation: optionalNullable(note.ExampleTranslation), Notes: optionalNullable(note.Notes),
		Tags: string(tags), Alternatives: string(alternatives), Fields: string(fields),
		ExternalID: external, ImportBatchID: sql.NullString{String: batchID, Valid: true},
		DeckID: deckID, UserID: ownerID,
	}
}

func enrichImportNote(langTarget string, note importer.Note) (importer.Note, error) {
	enriched := lang.EnrichImportNote(langTarget, lang.ImportNote{
		Term: note.Term, Translation: note.Translation, Notes: note.Notes,
		Alternatives: note.Alternatives, Fields: note.Fields,
	})
	if !importer.HasECMAScriptNonWhitespace(enriched.Translation) {
		return importer.Note{}, ErrInvalidInput
	}
	note.Translation = enriched.Translation
	note.Notes = enriched.Notes
	note.Alternatives = enriched.Alternatives
	note.Fields = enriched.Fields
	return note, nil
}

func noteChanged(existing gen.Note, incoming importer.Note) bool {
	params := noteParams(0, 0, "", incoming, existing.ExternalID)
	return existing.Term != params.Term || existing.TermKey != params.TermKey || existing.Translation != params.Translation ||
		!nullStringEqual(existing.Transcription, params.Transcription) || !nullStringEqual(existing.Example, params.Example) ||
		!nullStringEqual(existing.ExampleTranslation, params.ExampleTranslation) || !nullStringEqual(existing.Notes, params.Notes) ||
		existing.Tags != params.Tags || existing.Alternatives != params.Alternatives || existing.Fields != params.Fields ||
		(incoming.ExternalID != "" && (!existing.ExternalID.Valid || existing.ExternalID.String != incoming.ExternalID))
}

func deckPreviewChanged(existing gen.Deck, incoming importer.Deck) bool {
	if existing.Name != incoming.Name || (incoming.ExpectedCount != nil && (!existing.ExpectedCount.Valid || existing.ExpectedCount.Int64 != *incoming.ExpectedCount)) {
		return true
	}
	if incoming.LangTarget != "" && (!existing.LangTarget.Valid || existing.LangTarget.String != incoming.LangTarget) {
		return true
	}
	if incoming.LangNative != "" && (!existing.LangNative.Valid || existing.LangNative.String != incoming.LangNative) {
		return true
	}
	return false
}

func suspendMissing(ctx context.Context, tx *sql.Tx, ownerID, deckID int64, currentBatchID, source string, seen map[int64]struct{}, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT notes.id FROM notes
		JOIN import_batches ON import_batches.id = notes.import_batch_id
		WHERE notes.deck_id = ? AND notes.deleted_at IS NULL
		  AND import_batches.user_id = ? AND json_extract(import_batches.summary, '$.source') = ?
		  AND (import_batches.status = 'applied' OR import_batches.id = ?)`, deckID, ownerID, source, currentBatchID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, imported := seen[id]; imported {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cards SET suspended = 1, updated_at = ? WHERE user_id = ? AND note_id = ? AND deleted_at IS NULL`, now, ownerID, id); err != nil {
			return err
		}
	}
	return nil
}

func parseSource(raw []byte, requested string) (importer.Adapter, *importer.Bundle, error) {
	var selected importer.Adapter
	if requested != "" {
		var ok bool
		selected, ok = adapterByName(requested)
		if !ok {
			return nil, nil, ErrInvalidInput
		}
	} else {
		for _, candidate := range adapters.All() {
			if candidate.Detect(raw) {
				selected = candidate
				break
			}
		}
		if selected == nil {
			return nil, nil, ErrInvalidInput
		}
	}
	bundle, err := selected.Parse(raw)
	if err != nil || importer.ValidateBundle(bundle) != nil {
		return nil, nil, ErrInvalidInput
	}
	return selected, bundle, nil
}

func adapterByName(name string) (importer.Adapter, bool) {
	for _, candidate := range adapters.All() {
		if candidate.Name() == name {
			return candidate, true
		}
	}
	return nil, false
}

func sourceName(bundle *importer.Bundle, adapter string) (string, error) {
	source := strings.TrimSpace(bundle.Source)
	if source == "" {
		source = adapter
	}
	if source == "" || len(source) > 128 || !utf8.ValidString(source) {
		return "", ErrInvalidInput
	}
	for _, r := range source {
		if r < 0x20 || r == 0x7f {
			return "", ErrInvalidInput
		}
	}
	return source, nil
}

func validateUniqueBundle(bundle *importer.Bundle) error {
	allDeckIDs := make(map[string]struct{})
	var check func([]importer.Deck) error
	check = func(decks []importer.Deck) error {
		deckIDs := make(map[string]struct{})
		for _, deck := range decks {
			if deck.ExternalID != "" {
				if _, exists := allDeckIDs[deck.ExternalID]; exists {
					return ErrConflict
				}
				allDeckIDs[deck.ExternalID] = struct{}{}
			} else {
				if _, exists := deckIDs[deck.Name]; exists {
					return ErrConflict
				}
				deckIDs[deck.Name] = struct{}{}
			}
			keys := make(map[string]struct{})
			externalIDs := make(map[string]struct{})
			for _, note := range deck.Notes {
				key := termKey(strings.TrimSpace(note.Term))
				if _, exists := keys[key]; exists {
					return ErrConflict
				}
				keys[key] = struct{}{}
				if note.ExternalID != "" {
					if _, exists := externalIDs[note.ExternalID]; exists {
						return ErrConflict
					}
					externalIDs[note.ExternalID] = struct{}{}
				}
			}
			if err := check(deck.Decks); err != nil {
				return err
			}
		}
		return nil
	}
	return check(bundle.Decks)
}

func validApplyInput(input ApplyInput) bool {
	if input.LangTarget != "de" && input.LangTarget != "en" {
		return false
	}
	if input.LangNative != "pl" && input.LangNative != "ru" {
		return false
	}
	return input.OnMissing == "" || input.OnMissing == "keep" || input.OnMissing == "suspend"
}

func addStatus(counts *Counts, status string) {
	switch status {
	case "new":
		counts.New++
	case "changed":
		counts.Changed++
	default:
		counts.Unchanged++
	}
}

func termKey(term string) string {
	term = norm.NFC.String(term)
	return norm.NFC.String(strings.ToLower(strings.Join(strings.Fields(term), " ")))
}

func optionalNullable(value string) sql.NullString {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func nullableString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func nullableStringPtr(value string) sql.NullString { return nullableString(value) }

func nullableInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func nullableExpected(value *int64) sql.NullInt64 { return nullableInt64(value) }

func expectedCount(incoming *int64, current sql.NullInt64) sql.NullInt64 {
	if incoming == nil {
		return current
	}
	return sql.NullInt64{Int64: *incoming, Valid: true}
}

func nullString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func nullInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copy := value.Int64
	return &copy
}

func sameParent(existing sql.NullInt64, incoming *int64) bool {
	if incoming == nil {
		return !existing.Valid
	}
	return existing.Valid && existing.Int64 == *incoming
}

func nullStringEqual(left, right sql.NullString) bool {
	return left.Valid == right.Valid && (!left.Valid || left.String == right.String)
}

func newBatchID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func validBatchID(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func gzipSource(raw []byte) ([]byte, error) {
	var result bytes.Buffer
	writer := gzip.NewWriter(&result)
	if _, err := writer.Write(raw); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

func ungzipSource(raw []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	result, err := io.ReadAll(io.LimitReader(reader, maxSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(result) > maxSourceBytes {
		return nil, ErrInvalidInput
	}
	return result, nil
}

func safeFilename(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = filepath.Base(value)
	if len(value) > 255 {
		value = value[:255]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func exportCSV(root importer.Deck) ([]byte, error) {
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	writer.Comma = ';'
	if err := writer.Write([]string{"term", "translation", "example", "tags"}); err != nil {
		return nil, err
	}
	var writeDeck func(importer.Deck) error
	writeDeck = func(deck importer.Deck) error {
		for _, note := range deck.Notes {
			if err := writer.Write([]string{
				safeCSVCell(note.Term), safeCSVCell(note.Translation), safeCSVCell(note.Example),
				safeCSVCell(strings.Join(note.Tags, ",")),
			}); err != nil {
				return err
			}
		}
		for _, child := range deck.Decks {
			if err := writeDeck(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeDeck(root); err != nil {
		return nil, err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func safeCSVCell(value string) string {
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		switch r {
		case '=', '+', '-', '@':
			return "'" + value
		default:
			return value
		}
	}
	return value
}

func isUniqueConstraint(err error) bool {
	var sqliteError *sqlite.Error
	return errors.As(err, &sqliteError) && sqliteError.Code()&0xff == 19
}

func scanDeck(row interface{ Scan(...any) error }, deck *gen.Deck) error {
	return row.Scan(
		&deck.ID, &deck.OwnerID, &deck.ParentID, &deck.Name, &deck.Description, &deck.Position,
		&deck.LangTarget, &deck.LangNative, &deck.ReverseCards, &deck.WrongArticleRating,
		&deck.ColorSeed, &deck.Source, &deck.ExternalID, &deck.ExpectedCount, &deck.SourceDeckID,
		&deck.CreatedAt, &deck.UpdatedAt, &deck.DeletedAt, &deck.DeletionBatch,
	)
}
