package notes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"golang.org/x/text/unicode/norm"
	"modernc.org/sqlite"
)

const (
	// These match the size bounds for learner answers sent to synonym checking.
	maxAlternativeBytes = 512
	maxAlternativeRunes = 200
)

var (
	ErrInvalidInput = errors.New("invalid note input")
	ErrNotFound     = errors.New("note or deck not found")
	ErrConflict     = errors.New("note conflicts with an existing resource")
	ErrInternal     = errors.New("internal note service error")
)

// Service implements owner-scoped note operations and card creation.
type Service struct {
	database *db.DB
	clock    clock.Clock
}

// NewService creates a note service backed by database and the provided clock.
func NewService(database *db.DB, c clock.Clock) *Service {
	if c == nil {
		c = clock.Real{}
	}
	return &Service{database: database, clock: c}
}

// OptionalString represents an omitted, explicitly null, or present PATCH value.
type OptionalString struct {
	Set   bool
	Value *string
}

// CreateInput contains the public, user-editable note fields.
type CreateInput struct {
	Term               string         `json:"term"`
	Translation        string         `json:"translation"`
	Transcription      *string        `json:"transcription"`
	Example            *string        `json:"example"`
	ExampleTranslation *string        `json:"example_translation"`
	Notes              *string        `json:"notes"`
	Tags               []string       `json:"tags"`
	Alternatives       []string       `json:"alternatives"`
	Fields             map[string]any `json:"fields"`
	ExternalID         *string        `json:"external_id"`
}

// PatchInput contains fields which may be changed without replacing the note.
type PatchInput struct {
	Term               *string
	Translation        *string
	Transcription      OptionalString
	Example            OptionalString
	ExampleTranslation OptionalString
	Notes              OptionalString
	Tags               *[]string
	Alternatives       *[]string
	Fields             *map[string]any
	ExternalID         OptionalString
}

// EnrichmentInput contains only selected enrichment suggestions. Nil fields
// are omitted; supplied values are applied only when the current note is empty.
type EnrichmentInput struct {
	Transcription      *string         `json:"transcription"`
	Example            *string         `json:"example"`
	ExampleTranslation *string         `json:"example_translation"`
	Alternatives       *[]string       `json:"alternatives"`
	Fields             *map[string]any `json:"fields"`
}

// ApplyEnrichmentResult reports the current note and the top-level suggestion
// fields that were applied.
type ApplyEnrichmentResult struct {
	Note          Note     `json:"note"`
	AppliedFields []string `json:"applied_fields"`
}

// Note is the public note representation; import bookkeeping and term_key stay private.
type Note struct {
	ID                 int64          `json:"id"`
	DeckID             int64          `json:"deck_id"`
	Term               string         `json:"term"`
	Translation        string         `json:"translation"`
	Transcription      *string        `json:"transcription"`
	Example            *string        `json:"example"`
	ExampleTranslation *string        `json:"example_translation"`
	Notes              *string        `json:"notes"`
	Tags               []string       `json:"tags"`
	Alternatives       []string       `json:"alternatives"`
	Fields             map[string]any `json:"fields"`
	ExternalID         *string        `json:"external_id"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

// Get returns an active note in an active deck owned by ownerID.
func (s *Service) Get(ctx context.Context, ownerID, noteID int64) (Note, error) {
	if !s.ready() || ownerID <= 0 || noteID <= 0 {
		return Note{}, ErrInternal
	}
	row, err := s.database.Queries().GetNote(ctx, gen.GetNoteParams{ID: noteID, OwnerID: ownerID})
	if errors.Is(err, sql.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	if err != nil {
		return Note{}, ErrInternal
	}
	item, err := publicNote(row)
	if err != nil {
		return Note{}, ErrInternal
	}
	return item, nil
}

// List returns active notes in an owner-visible deck.
func (s *Service) List(ctx context.Context, ownerID, deckID int64) ([]Note, error) {
	if !s.ready() || ownerID <= 0 || deckID <= 0 {
		return nil, ErrInternal
	}
	queries := s.database.Queries()
	if _, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: deckID, OwnerID: ownerID}); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, ErrInternal
	}
	rows, err := queries.ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: deckID, OwnerID: ownerID})
	if err != nil {
		return nil, ErrInternal
	}
	result := make([]Note, 0, len(rows))
	for _, row := range rows {
		item, err := publicNote(row)
		if err != nil {
			return nil, ErrInternal
		}
		result = append(result, item)
	}
	return result, nil
}

// Create inserts a note and its forward and optional reverse cards atomically.
func (s *Service) Create(ctx context.Context, ownerID, deckID int64, input CreateInput) (Note, error) {
	if !s.ready() || ownerID <= 0 || deckID <= 0 {
		return Note{}, ErrInternal
	}
	values, err := normalizeCreate(input)
	if err != nil {
		return Note{}, err
	}

	tx, err := s.database.SQL.BeginTx(ctx, nil)
	if err != nil {
		return Note{}, ErrInternal
	}
	defer func() { _ = tx.Rollback() }()
	queries := s.database.Queries().WithTx(tx)
	deck, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: deckID, OwnerID: ownerID})
	if errors.Is(err, sql.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	if err != nil {
		return Note{}, ErrInternal
	}

	created, err := queries.CreateNote(ctx, gen.CreateNoteParams{
		Term:               values.term,
		TermKey:            termKey(values.term),
		Translation:        values.translation,
		Transcription:      nullableString(values.transcription),
		Example:            nullableString(values.example),
		ExampleTranslation: nullableString(values.exampleTranslation),
		Notes:              nullableString(values.notes),
		Tags:               values.tagsJSON,
		Alternatives:       values.alternativesJSON,
		Fields:             values.fieldsJSON,
		ExternalID:         nullableString(values.externalID),
		DeckID:             deckID,
		UserID:             ownerID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	if isUniqueConstraint(err) {
		return Note{}, ErrConflict
	}
	if err != nil {
		return Note{}, ErrInternal
	}
	if _, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: created.ID, UserID: ownerID, Direction: "forward"}); err != nil {
		return Note{}, ErrInternal
	}
	if deck.ReverseCards {
		if _, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: created.ID, UserID: ownerID, Direction: "reverse"}); err != nil {
			return Note{}, ErrInternal
		}
	}
	if err := tx.Commit(); err != nil {
		return Note{}, ErrInternal
	}
	item, err := publicNote(created)
	if err != nil {
		return Note{}, ErrInternal
	}
	return item, nil
}

// Update applies a partial update to an owner-visible note.
func (s *Service) Update(ctx context.Context, ownerID, id int64, patch PatchInput) (Note, error) {
	if !s.ready() || ownerID <= 0 || id <= 0 {
		return Note{}, ErrInternal
	}
	queries := s.database.Queries()
	current, err := queries.GetNote(ctx, gen.GetNoteParams{ID: id, OwnerID: ownerID})
	if errors.Is(err, sql.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	if err != nil {
		return Note{}, ErrInternal
	}
	old, err := publicNote(current)
	if err != nil {
		return Note{}, ErrInternal
	}

	term, translation := old.Term, old.Translation
	if patch.Term != nil {
		term = strings.TrimSpace(*patch.Term)
	}
	if patch.Translation != nil {
		translation = strings.TrimSpace(*patch.Translation)
	}
	if !validRequiredText(term) || !validRequiredText(translation) {
		return Note{}, ErrInvalidInput
	}
	transcription := old.Transcription
	if patch.Transcription.Set {
		transcription = patch.Transcription.Value
	}
	example := old.Example
	if patch.Example.Set {
		example = patch.Example.Value
	}
	exampleTranslation := old.ExampleTranslation
	if patch.ExampleTranslation.Set {
		exampleTranslation = patch.ExampleTranslation.Value
	}
	notesText := old.Notes
	if patch.Notes.Set {
		notesText = patch.Notes.Value
	}
	tags := old.Tags
	if patch.Tags != nil {
		tags = *patch.Tags
	}
	alternatives := old.Alternatives
	if patch.Alternatives != nil {
		alternatives = *patch.Alternatives
	}
	fields := old.Fields
	if patch.Fields != nil {
		fields = *patch.Fields
	}
	externalID := old.ExternalID
	if patch.ExternalID.Set {
		externalID = patch.ExternalID.Value
	}
	if !validOptionalText(transcription, example, exampleTranslation, notesText, externalID) {
		return Note{}, ErrInvalidInput
	}
	tagsJSON, alternativesJSON, fieldsJSON, err := encodeJSONFields(tags, alternatives, fields)
	if err != nil {
		return Note{}, ErrInvalidInput
	}

	updated, err := queries.UpdateNote(ctx, gen.UpdateNoteParams{
		Term:               term,
		TermKey:            termKey(term),
		Translation:        translation,
		Transcription:      nullableString(transcription),
		Example:            nullableString(example),
		ExampleTranslation: nullableString(exampleTranslation),
		Notes:              nullableString(notesText),
		Tags:               tagsJSON,
		Alternatives:       alternativesJSON,
		Fields:             fieldsJSON,
		ExternalID:         nullableString(externalID),
		ImportBatchID:      current.ImportBatchID,
		UpdatedAt:          s.now(),
		ID:                 id,
		OwnerID:            ownerID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	if isUniqueConstraint(err) {
		return Note{}, ErrConflict
	}
	if err != nil {
		return Note{}, ErrInternal
	}
	item, err := publicNote(updated)
	if err != nil {
		return Note{}, ErrInternal
	}
	return item, nil
}

// AddAlternative appends an accepted answer without replacing concurrent
// additions. Repeated additions of the same trimmed value are idempotent.
func (s *Service) AddAlternative(ctx context.Context, ownerID, noteID int64, alternative string) (Note, error) {
	if !s.ready() || ownerID <= 0 || noteID <= 0 {
		return Note{}, ErrInternal
	}
	alternative = strings.TrimSpace(alternative)
	if alternative == "" || !utf8.ValidString(alternative) || len(alternative) > maxAlternativeBytes ||
		utf8.RuneCountInString(alternative) > maxAlternativeRunes {
		return Note{}, ErrInvalidInput
	}

	row, err := s.database.Queries().AddNoteAlternative(ctx, gen.AddNoteAlternativeParams{
		Alternative: alternative,
		UpdatedAt:   s.now(),
		ID:          noteID,
		OwnerID:     ownerID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	if err != nil {
		return Note{}, ErrInternal
	}
	item, err := publicNote(row)
	if err != nil {
		return Note{}, ErrInternal
	}
	return item, nil
}

// ApplyEnrichment fills selected empty enrichment fields on an active,
// owner-visible note. The current note is read and updated in one transaction
// so stale suggestions cannot replace values that were already saved.
func (s *Service) ApplyEnrichment(ctx context.Context, ownerID, noteID int64, input EnrichmentInput) (ApplyEnrichmentResult, error) {
	if !s.ready() || ownerID <= 0 || noteID <= 0 {
		return ApplyEnrichmentResult{}, ErrInternal
	}
	input, err := normalizeEnrichment(input)
	if err != nil {
		return ApplyEnrichmentResult{}, err
	}

	tx, err := s.database.SQL.BeginTx(ctx, nil)
	if err != nil {
		return ApplyEnrichmentResult{}, ErrInternal
	}
	defer func() { _ = tx.Rollback() }()
	queries := s.database.Queries().WithTx(tx)
	current, err := queries.GetNote(ctx, gen.GetNoteParams{ID: noteID, OwnerID: ownerID})
	if errors.Is(err, sql.ErrNoRows) {
		return ApplyEnrichmentResult{}, ErrNotFound
	}
	if err != nil {
		return ApplyEnrichmentResult{}, ErrInternal
	}
	item, err := publicNote(current)
	if err != nil {
		return ApplyEnrichmentResult{}, ErrInternal
	}

	applied := make([]string, 0, 5)
	if isEmptyText(item.Transcription) && input.Transcription != nil {
		item.Transcription = input.Transcription
		applied = append(applied, "transcription")
	}
	if isEmptyText(item.Example) && input.Example != nil {
		item.Example = input.Example
		applied = append(applied, "example")
	}
	if isEmptyText(item.ExampleTranslation) && input.ExampleTranslation != nil {
		item.ExampleTranslation = input.ExampleTranslation
		applied = append(applied, "example_translation")
	}
	if len(item.Alternatives) == 0 && input.Alternatives != nil {
		item.Alternatives = *input.Alternatives
		applied = append(applied, "alternatives")
	}
	if input.Fields != nil {
		for key, value := range *input.Fields {
			if isEmptyMetadataValue(item.Fields[key]) {
				item.Fields[key] = value
				if !containsString(applied, "fields") {
					applied = append(applied, "fields")
				}
			}
		}
	}

	if len(applied) == 0 {
		if err := tx.Commit(); err != nil {
			return ApplyEnrichmentResult{}, ErrInternal
		}
		return ApplyEnrichmentResult{Note: item, AppliedFields: applied}, nil
	}

	_, alternativesJSON, fieldsJSON, err := encodeJSONFields(item.Tags, item.Alternatives, item.Fields)
	if err != nil {
		return ApplyEnrichmentResult{}, ErrInternal
	}
	updated, err := queries.UpdateNote(ctx, gen.UpdateNoteParams{
		Term:               current.Term,
		TermKey:            current.TermKey,
		Translation:        current.Translation,
		Transcription:      nullableString(item.Transcription),
		Example:            nullableString(item.Example),
		ExampleTranslation: nullableString(item.ExampleTranslation),
		Notes:              current.Notes,
		Tags:               current.Tags,
		Alternatives:       alternativesJSON,
		Fields:             fieldsJSON,
		ExternalID:         current.ExternalID,
		ImportBatchID:      current.ImportBatchID,
		UpdatedAt:          s.now(),
		ID:                 noteID,
		OwnerID:            ownerID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ApplyEnrichmentResult{}, ErrNotFound
	}
	if err != nil {
		return ApplyEnrichmentResult{}, ErrInternal
	}
	item, err = publicNote(updated)
	if err != nil {
		return ApplyEnrichmentResult{}, ErrInternal
	}
	if err := tx.Commit(); err != nil {
		return ApplyEnrichmentResult{}, ErrInternal
	}
	return ApplyEnrichmentResult{Note: item, AppliedFields: applied}, nil
}

// Delete soft-deletes a note and its cards for the owner.
func (s *Service) Delete(ctx context.Context, ownerID, id int64) error {
	if !s.ready() || ownerID <= 0 || id <= 0 {
		return ErrInternal
	}
	now := s.now()
	_, err := s.database.Queries().SoftDeleteNote(ctx, gen.SoftDeleteNoteParams{
		DeletedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now, ID: id, OwnerID: ownerID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return ErrInternal
	}
	return nil
}

func (s *Service) ready() bool {
	return s != nil && s.database != nil && s.database.SQL != nil && s.clock != nil
}

func (s *Service) now() time.Time { return s.clock.Now().UTC() }

type normalizedInput struct {
	term               string
	translation        string
	transcription      *string
	example            *string
	exampleTranslation *string
	notes              *string
	tagsJSON           string
	alternativesJSON   string
	fieldsJSON         string
	externalID         *string
}

func normalizeCreate(input CreateInput) (normalizedInput, error) {
	term := strings.TrimSpace(input.Term)
	translation := strings.TrimSpace(input.Translation)
	if !validRequiredText(term) || !validRequiredText(translation) {
		return normalizedInput{}, ErrInvalidInput
	}
	transcription := normalizeOptionalString(input.Transcription)
	example := normalizeOptionalString(input.Example)
	exampleTranslation := normalizeOptionalString(input.ExampleTranslation)
	notesText := normalizeOptionalString(input.Notes)
	externalID := normalizeOptionalString(input.ExternalID)
	if !validOptionalText(transcription, example, exampleTranslation, notesText, externalID) {
		return normalizedInput{}, ErrInvalidInput
	}
	if input.Tags == nil {
		input.Tags = []string{}
	}
	if input.Alternatives == nil {
		input.Alternatives = []string{}
	}
	if input.Fields == nil {
		input.Fields = map[string]any{}
	}
	tagsJSON, alternativesJSON, fieldsJSON, err := encodeJSONFields(input.Tags, input.Alternatives, input.Fields)
	if err != nil {
		return normalizedInput{}, ErrInvalidInput
	}
	return normalizedInput{
		term: term, translation: translation,
		transcription: transcription, example: example, exampleTranslation: exampleTranslation,
		notes: notesText, tagsJSON: tagsJSON, alternativesJSON: alternativesJSON,
		fieldsJSON: fieldsJSON, externalID: externalID,
	}, nil
}

func validRequiredText(value string) bool { return value != "" && utf8.ValidString(value) }

func validOptionalText(values ...*string) bool {
	for _, value := range values {
		if value != nil && !utf8.ValidString(*value) {
			return false
		}
	}
	return true
}

func normalizeOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := strings.TrimSpace(*value)
	if copy == "" {
		return nil
	}
	return &copy
}

func normalizeEnrichment(input EnrichmentInput) (EnrichmentInput, error) {
	for _, value := range []*string{input.Transcription, input.Example, input.ExampleTranslation} {
		if value != nil && (!utf8.ValidString(*value) || strings.TrimSpace(*value) == "") {
			return EnrichmentInput{}, ErrInvalidInput
		}
	}
	input.Transcription = normalizeOptionalString(input.Transcription)
	input.Example = normalizeOptionalString(input.Example)
	input.ExampleTranslation = normalizeOptionalString(input.ExampleTranslation)
	if input.Alternatives != nil {
		if len(*input.Alternatives) == 0 {
			return EnrichmentInput{}, ErrInvalidInput
		}
		alternatives := make([]string, len(*input.Alternatives))
		for i, value := range *input.Alternatives {
			value = strings.TrimSpace(value)
			if !utf8.ValidString(value) || value == "" {
				return EnrichmentInput{}, ErrInvalidInput
			}
			alternatives[i] = value
		}
		input.Alternatives = &alternatives
	}
	if input.Fields != nil {
		fields := make(map[string]any, len(*input.Fields))
		for key, value := range *input.Fields {
			if !utf8.ValidString(key) || !isEnrichmentMetadataField(key) {
				return EnrichmentInput{}, ErrInvalidInput
			}
			if key == "verb_forms" {
				verbForms, ok := normalizeVerbForms(value)
				if !ok {
					return EnrichmentInput{}, ErrInvalidInput
				}
				fields[key] = verbForms
				continue
			}
			text, ok := value.(string)
			if !ok || !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
				return EnrichmentInput{}, ErrInvalidInput
			}
			fields[key] = strings.TrimSpace(text)
		}
		input.Fields = &fields
	}
	return input, nil
}

func isEnrichmentMetadataField(key string) bool {
	switch key {
	case "pos", "article", "plural", "verb_forms", "cefr":
		return true
	default:
		return false
	}
}

func normalizeVerbForms(value any) ([]string, bool) {
	var forms []string
	switch value := value.(type) {
	case []string:
		forms = value
	case []any:
		forms = make([]string, len(value))
		for i, form := range value {
			text, ok := form.(string)
			if !ok {
				return nil, false
			}
			forms[i] = text
		}
	default:
		return nil, false
	}
	if len(forms) == 0 {
		return nil, false
	}
	normalized := make([]string, len(forms))
	for i, form := range forms {
		form = strings.TrimSpace(form)
		if !utf8.ValidString(form) || form == "" {
			return nil, false
		}
		normalized[i] = form
	}
	return normalized, true
}

func isEmptyMetadataValue(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(value) == ""
	case []any:
		return len(value) == 0
	case map[string]any:
		return len(value) == 0
	default:
		return false
	}
}

func isEmptyText(value *string) bool {
	return value == nil || strings.TrimSpace(*value) == ""
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func nullableString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func encodeJSONFields(tags, alternatives []string, fields map[string]any) (string, string, string, error) {
	if tags == nil {
		tags = []string{}
	}
	if alternatives == nil {
		alternatives = []string{}
	}
	if fields == nil {
		fields = map[string]any{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return "", "", "", err
	}
	alternativesJSON, err := json.Marshal(alternatives)
	if err != nil {
		return "", "", "", err
	}
	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return "", "", "", err
	}
	return string(tagsJSON), string(alternativesJSON), string(fieldsJSON), nil
}

func publicNote(note gen.Note) (Note, error) {
	var tags []string
	var alternatives []string
	var fields map[string]any
	if err := json.Unmarshal([]byte(note.Tags), &tags); err != nil {
		return Note{}, err
	}
	if err := json.Unmarshal([]byte(note.Alternatives), &alternatives); err != nil {
		return Note{}, err
	}
	if err := json.Unmarshal([]byte(note.Fields), &fields); err != nil {
		return Note{}, err
	}
	if tags == nil {
		tags = []string{}
	}
	if alternatives == nil {
		alternatives = []string{}
	}
	if fields == nil {
		fields = map[string]any{}
	}
	return Note{
		ID: note.ID, DeckID: note.DeckID, Term: note.Term, Translation: note.Translation,
		Transcription: stringPointer(note.Transcription), Example: stringPointer(note.Example),
		ExampleTranslation: stringPointer(note.ExampleTranslation), Notes: stringPointer(note.Notes),
		Tags: tags, Alternatives: alternatives, Fields: fields, ExternalID: stringPointer(note.ExternalID),
		CreatedAt: note.CreatedAt.UTC(), UpdatedAt: note.UpdatedAt.UTC(),
	}, nil
}

func stringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	copy := value.String
	return &copy
}

func termKey(term string) string {
	term = norm.NFC.String(term)
	return norm.NFC.String(strings.ToLower(strings.Join(strings.Fields(term), " ")))
}

func isUniqueConstraint(err error) bool {
	var sqliteError *sqlite.Error
	return errors.As(err, &sqliteError) && sqliteError.Code()&0xff == 19
}
