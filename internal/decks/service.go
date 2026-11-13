package decks

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
)

var (
	ErrInvalidInput = errors.New("invalid deck input")
	ErrNotFound     = errors.New("deck not found")
	ErrInternal     = errors.New("internal deck service error")
)

// Service implements owner-scoped deck operations using the generated queries.
type Service struct {
	database *db.DB
	clock    clock.Clock
}

// NewService creates a deck service backed by database and the provided clock.
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

// OptionalInt64 represents an omitted, explicitly null, or present PATCH value.
type OptionalInt64 struct {
	Set   bool
	Value *int64
}

// CreateInput contains user-editable deck fields. A nil parent or language is
// stored as NULL, meaning root deck or inherited language respectively.
type CreateInput struct {
	Name               string  `json:"name"`
	ParentID           *int64  `json:"parent_id"`
	Description        *string `json:"description"`
	Position           *int64  `json:"position"`
	LangTarget         *string `json:"lang_target"`
	LangNative         *string `json:"lang_native"`
	ReverseCards       bool    `json:"reverse_cards"`
	WrongArticleRating *int64  `json:"wrong_article_rating"`
	ColorSeed          *string `json:"color_seed"`
}

// PatchInput contains only fields which can be changed by the owner.
type PatchInput struct {
	Name               *string
	ParentID           OptionalInt64
	Description        OptionalString
	Position           *int64
	LangTarget         OptionalString
	LangNative         OptionalString
	ReverseCards       *bool
	WrongArticleRating *int64
	ColorSeed          OptionalString
}

// Counts contains direct counts plus, for tree responses, all descendant decks.
type Counts struct {
	Notes    int64 `json:"notes"`
	New      int64 `json:"new"`
	Learning int64 `json:"learning"`
	Due      int64 `json:"due"`
}

// Deck is the public deck representation. Children is always a JSON array.
type Deck struct {
	ID                 int64     `json:"id"`
	OwnerID            int64     `json:"owner_id"`
	CanEdit            bool      `json:"can_edit"`
	ParentID           *int64    `json:"parent_id"`
	Name               string    `json:"name"`
	Description        *string   `json:"description"`
	Position           int64     `json:"position"`
	LangTarget         *string   `json:"lang_target"`
	LangNative         *string   `json:"lang_native"`
	ReverseCards       bool      `json:"reverse_cards"`
	WrongArticleRating int64     `json:"wrong_article_rating"`
	ColorSeed          *string   `json:"color_seed"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	Counts             *Counts   `json:"counts,omitempty"`
	Children           []Deck    `json:"children"`
}

// Create adds a deck owned by ownerID. Parent references are scoped to that owner.
func (s *Service) Create(ctx context.Context, ownerID int64, input CreateInput) (Deck, error) {
	if !s.ready() || ownerID <= 0 {
		return Deck{}, ErrInternal
	}
	name, err := normalizeName(input.Name)
	if err != nil {
		return Deck{}, err
	}
	if input.ParentID != nil && *input.ParentID <= 0 {
		return Deck{}, ErrInvalidInput
	}
	position := int64(0)
	if input.Position != nil {
		position = *input.Position
		if position < 0 {
			return Deck{}, ErrInvalidInput
		}
	}
	rating := int64(2)
	if input.WrongArticleRating != nil {
		rating = *input.WrongArticleRating
	}
	if !validWrongArticleRating(rating) {
		return Deck{}, ErrInvalidInput
	}
	if !validLanguage(input.LangTarget, "de", "en") || !validLanguage(input.LangNative, "pl", "ru") || !validColor(input.ColorSeed) {
		return Deck{}, ErrInvalidInput
	}

	deck, err := s.database.Queries().CreateDeck(ctx, gen.CreateDeckParams{
		OwnerID:            ownerID,
		ParentID:           nullableInt64(input.ParentID),
		Name:               name,
		Description:        nullableString(input.Description),
		Position:           position,
		LangTarget:         nullableString(input.LangTarget),
		LangNative:         nullableString(input.LangNative),
		ReverseCards:       input.ReverseCards,
		WrongArticleRating: rating,
		ColorSeed:          nullableString(input.ColorSeed),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Deck{}, ErrNotFound
	}
	if err != nil {
		return Deck{}, ErrInternal
	}
	return publicDeck(deck), nil
}

// List returns the owner's deck tree and aggregate note/card counts.
func (s *Service) List(ctx context.Context, ownerID int64) ([]Deck, error) {
	if !s.ready() || ownerID <= 0 {
		return nil, ErrInternal
	}
	queries := s.database.Queries()
	rows, err := queries.ListDecksByOwner(ctx, ownerID)
	if err != nil {
		return nil, ErrInternal
	}

	nodes := make(map[int64]*Deck, len(rows))
	children := make(map[int64][]int64, len(rows))
	for _, row := range rows {
		deck := publicDeck(row)
		deck.CanEdit = row.OwnerID == ownerID
		deck.Counts = &Counts{}
		deck.Children = []Deck{}
		nodes[deck.ID] = &deck
	}
	for _, row := range rows {
		if row.ParentID.Valid {
			if _, ok := nodes[row.ParentID.Int64]; ok {
				children[row.ParentID.Int64] = append(children[row.ParentID.Int64], row.ID)
			}
		}
	}

	noteDecks := make(map[int64]int64)
	for _, row := range rows {
		noteRows, err := queries.ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: row.ID, OwnerID: ownerID})
		if err != nil {
			return nil, ErrInternal
		}
		nodes[row.ID].Counts.Notes = int64(len(noteRows))
		for _, note := range noteRows {
			// The list only includes active, owner-visible notes.
			noteDecks[note.ID] = row.ID
		}
	}

	cards, err := queries.ListCardsByUser(ctx, ownerID)
	if err != nil {
		return nil, ErrInternal
	}
	now := s.now()
	for _, card := range cards {
		deckID, ok := noteDecks[card.NoteID]
		if !ok {
			continue
		}
		counts := nodes[deckID].Counts
		switch card.State {
		case 0:
			counts.New++
		case 1, 3:
			counts.Learning++
		case 2:
			if !card.Due.After(now) {
				counts.Due++
			}
		}
	}

	var build func(int64) Deck
	build = func(id int64) Deck {
		deck := *nodes[id]
		for _, childID := range children[id] {
			child := build(childID)
			deck.Children = append(deck.Children, child)
			deck.Counts.Notes += child.Counts.Notes
			deck.Counts.New += child.Counts.New
			deck.Counts.Learning += child.Counts.Learning
			deck.Counts.Due += child.Counts.Due
		}
		return deck
	}

	rootIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		if !row.ParentID.Valid || nodes[row.ParentID.Int64] == nil {
			rootIDs = append(rootIDs, row.ID)
		}
	}
	// Keep the tree deterministic even if the SQL ordering changes.
	sort.SliceStable(rootIDs, func(i, j int) bool {
		left, right := nodes[rootIDs[i]], nodes[rootIDs[j]]
		if left.Position != right.Position {
			return left.Position < right.Position
		}
		return left.ID < right.ID
	})
	result := make([]Deck, 0, len(rootIDs))
	for _, id := range rootIDs {
		result = append(result, build(id))
	}
	return result, nil
}

// Update applies a partial owner-scoped deck update.
func (s *Service) Update(ctx context.Context, ownerID, id int64, patch PatchInput) (Deck, error) {
	if !s.ready() || ownerID <= 0 || id <= 0 {
		return Deck{}, ErrInternal
	}
	queries := s.database.Queries()
	current, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: id, OwnerID: ownerID})
	if errors.Is(err, sql.ErrNoRows) {
		return Deck{}, ErrNotFound
	}
	if err != nil {
		return Deck{}, ErrInternal
	}

	parentID := current.ParentID
	if patch.ParentID.Set {
		parentID = nullableInt64(patch.ParentID.Value)
		if patch.ParentID.Value != nil {
			if *patch.ParentID.Value <= 0 {
				return Deck{}, ErrInvalidInput
			}
			if *patch.ParentID.Value == id {
				return Deck{}, ErrInvalidInput
			}
			if _, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: *patch.ParentID.Value, OwnerID: ownerID}); errors.Is(err, sql.ErrNoRows) {
				return Deck{}, ErrNotFound
			} else if err != nil {
				return Deck{}, ErrInternal
			}
			isDescendant, err := isDescendant(ctx, queries, ownerID, id, *patch.ParentID.Value)
			if err != nil {
				return Deck{}, ErrInternal
			}
			if isDescendant {
				return Deck{}, ErrInvalidInput
			}
		}
	}

	name := current.Name
	if patch.Name != nil {
		name, err = normalizeName(*patch.Name)
		if err != nil {
			return Deck{}, err
		}
	}
	description := current.Description
	if patch.Description.Set {
		description = nullableString(patch.Description.Value)
	}
	position := current.Position
	if patch.Position != nil {
		position = *patch.Position
		if position < 0 {
			return Deck{}, ErrInvalidInput
		}
	}
	langTarget := current.LangTarget
	if patch.LangTarget.Set {
		langTarget = nullableString(patch.LangTarget.Value)
	}
	langNative := current.LangNative
	if patch.LangNative.Set {
		langNative = nullableString(patch.LangNative.Value)
	}
	reverse := current.ReverseCards
	if patch.ReverseCards != nil {
		reverse = *patch.ReverseCards
	}
	rating := current.WrongArticleRating
	if patch.WrongArticleRating != nil {
		rating = *patch.WrongArticleRating
	}
	color := current.ColorSeed
	if patch.ColorSeed.Set {
		color = nullableString(patch.ColorSeed.Value)
	}
	if !validWrongArticleRating(rating) || !validNullableLanguage(langTarget, "de", "en") ||
		!validNullableLanguage(langNative, "pl", "ru") || !validNullableColor(color) {
		return Deck{}, ErrInvalidInput
	}

	updated, err := queries.UpdateDeck(ctx, gen.UpdateDeckParams{
		ParentID:           parentID,
		Name:               name,
		Description:        description,
		Position:           position,
		LangTarget:         langTarget,
		LangNative:         langNative,
		ReverseCards:       reverse,
		WrongArticleRating: rating,
		ColorSeed:          color,
		Source:             current.Source,
		ExternalID:         current.ExternalID,
		ExpectedCount:      current.ExpectedCount,
		SourceDeckID:       current.SourceDeckID,
		UpdatedAt:          s.now(),
		ID:                 id,
		OwnerID:            ownerID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Deck{}, ErrNotFound
	}
	if err != nil {
		return Deck{}, ErrInternal
	}
	return publicDeck(updated), nil
}

func validWrongArticleRating(rating int64) bool {
	return rating == 1 || rating == 2
}

// Delete soft-deletes a deck and its owned descendant subtree.
func (s *Service) Delete(ctx context.Context, ownerID, id int64) error {
	if !s.ready() || ownerID <= 0 || id <= 0 {
		return ErrInternal
	}
	now := s.now()
	_, err := s.database.Queries().SoftDeleteDeck(ctx, gen.SoftDeleteDeckParams{
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

func normalizeName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) {
		return "", ErrInvalidInput
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return "", ErrInvalidInput
		}
	}
	return value, nil
}

func validLanguage(value *string, choices ...string) bool {
	if value == nil {
		return true
	}
	for _, choice := range choices {
		if *value == choice {
			return true
		}
	}
	return false
}

func validNullableLanguage(value sql.NullString, choices ...string) bool {
	if !value.Valid {
		return true
	}
	for _, choice := range choices {
		if value.String == choice {
			return true
		}
	}
	return false
}

func validColor(value *string) bool {
	if value == nil {
		return true
	}
	return validColorValue(*value)
}

func validNullableColor(value sql.NullString) bool {
	return !value.Valid || validColorValue(value.String)
}

func validColorValue(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, char := range value[1:] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func nullableInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func nullableString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func publicDeck(deck gen.Deck) Deck {
	return Deck{
		ID:                 deck.ID,
		OwnerID:            deck.OwnerID,
		CanEdit:            true,
		ParentID:           int64Pointer(deck.ParentID),
		Name:               deck.Name,
		Description:        stringPointer(deck.Description),
		Position:           deck.Position,
		LangTarget:         stringPointer(deck.LangTarget),
		LangNative:         stringPointer(deck.LangNative),
		ReverseCards:       deck.ReverseCards,
		WrongArticleRating: deck.WrongArticleRating,
		ColorSeed:          stringPointer(deck.ColorSeed),
		CreatedAt:          deck.CreatedAt.UTC(),
		UpdatedAt:          deck.UpdatedAt.UTC(),
		Children:           []Deck{},
	}
}

func int64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copy := value.Int64
	return &copy
}

func stringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	copy := value.String
	return &copy
}

func isDescendant(ctx context.Context, queries *gen.Queries, ownerID, deckID, candidateID int64) (bool, error) {
	rows, err := queries.ListDecksByOwner(ctx, ownerID)
	if err != nil {
		return false, err
	}
	parents := make(map[int64]int64, len(rows))
	for _, row := range rows {
		if row.ParentID.Valid {
			parents[row.ID] = row.ParentID.Int64
		}
	}
	for current := candidateID; current != 0; current = parents[current] {
		if current == deckID {
			return true, nil
		}
		if _, ok := parents[current]; !ok {
			break
		}
	}
	return false, nil
}
