package social

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pestit/retain/internal/db/gen"
)

type DeckShare struct {
	ID              int64   `json:"id"`
	SourceDeckID    int64   `json:"source_deck_id"`
	SourceDeckName  string  `json:"source_deck_name,omitempty"`
	SourceOwnerID   int64   `json:"source_owner_id,omitempty"`
	SourceOwnerName string  `json:"source_owner_name,omitempty"`
	RecipientID     int64   `json:"recipient_id"`
	RecipientName   string  `json:"recipient_name,omitempty"`
	RecipientEmail  string  `json:"recipient_email,omitempty"`
	Mode            string  `json:"mode"`
	Status          string  `json:"status"`
	CopiedDeckID    *int64  `json:"copied_deck_id,omitempty"`
	CreatedAt       genTime `json:"created_at"`
	UpdatedAt       genTime `json:"updated_at"`
	Role            string  `json:"role,omitempty"`
}

// genTime aliases the wire-compatible timestamp type while keeping the
// service's public model independent from sqlc-generated row models.
type genTime = time.Time

func (s *Service) ShareDeck(ctx context.Context, ownerID, sourceDeckID, recipientID int64, mode string) (DeckShare, error) {
	if !s.ready() || ownerID <= 0 || sourceDeckID <= 0 || recipientID <= 0 || recipientID == ownerID ||
		(mode != "copy" && mode != "subscribe") {
		return DeckShare{}, ErrInvalidInput
	}
	tx, queries, err := s.begin(ctx)
	if err != nil {
		return DeckShare{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.requireFriend(ctx, queries, ownerID, recipientID); err != nil {
		return DeckShare{}, err
	}
	if _, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: sourceDeckID, OwnerID: ownerID}); errors.Is(err, sql.ErrNoRows) {
		return DeckShare{}, ErrNotFound
	} else if err != nil {
		return DeckShare{}, fmt.Errorf("get shared deck: %w: %v", ErrInternal, err)
	}
	exists, err := queries.HasDeckShare(ctx, gen.HasDeckShareParams{SourceDeckID: sourceDeckID, RecipientID: recipientID})
	if err != nil {
		return DeckShare{}, fmt.Errorf("check duplicate deck share: %w: %v", ErrInternal, err)
	}
	if exists != 0 {
		return DeckShare{}, ErrConflict
	}

	var share gen.DeckShare
	if mode == "subscribe" {
		share, err = queries.CreateActiveDeckSubscription(ctx, gen.CreateActiveDeckSubscriptionParams{
			RecipientUserID: recipientID, SourceDeckID: sourceDeckID, OwnerID: ownerID,
		})
	} else {
		share, err = s.copyDeck(ctx, queries, ownerID, sourceDeckID, recipientID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return DeckShare{}, ErrConflict
	}
	if isUniqueConstraint(err) {
		return DeckShare{}, ErrConflict
	}
	if err != nil {
		return DeckShare{}, err
	}
	result := publicDeckShare(share)
	if err := commit(tx, nil, "share deck"); err != nil {
		return DeckShare{}, err
	}
	return result, nil
}

func (s *Service) copyDeck(ctx context.Context, queries *gen.Queries, ownerID, sourceDeckID, recipientID int64) (gen.DeckShare, error) {
	tree, err := queries.ListDeckTreeForShare(ctx, gen.ListDeckTreeForShareParams{DeckID: sourceDeckID, OwnerID: ownerID})
	if err != nil {
		return gen.DeckShare{}, fmt.Errorf("read deck subtree for copy: %w: %v", ErrInternal, err)
	}
	if len(tree) == 0 {
		return gen.DeckShare{}, ErrNotFound
	}
	deckMap := make(map[int64]int64, len(tree))
	var copiedRootID int64
	for _, source := range tree {
		var parentID sql.NullInt64
		if source.ParentID.Valid {
			if copiedParent, exists := deckMap[source.ParentID.Int64]; exists {
				parentID = sql.NullInt64{Int64: copiedParent, Valid: true}
			}
		}
		copied, err := queries.CreateDeck(ctx, gen.CreateDeckParams{
			OwnerID: recipientID, ParentID: parentID, Name: source.Name,
			Description: source.Description, Position: source.Position,
			LangTarget: source.LangTarget, LangNative: source.LangNative,
			ReverseCards: source.ReverseCards, WrongArticleRating: source.WrongArticleRating,
			ColorSeed: source.ColorSeed, ExpectedCount: source.ExpectedCount,
			SourceDeckID: sql.NullInt64{Int64: source.ID, Valid: true},
		})
		if err != nil {
			return gen.DeckShare{}, fmt.Errorf("copy shared deck tree: %w: %v", ErrInternal, err)
		}
		deckMap[source.ID] = copied.ID
		if source.ID == sourceDeckID {
			copiedRootID = copied.ID
		}
		notes, err := queries.ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: source.ID, OwnerID: ownerID})
		if err != nil {
			return gen.DeckShare{}, fmt.Errorf("list source deck notes: %w: %v", ErrInternal, err)
		}
		for _, sourceNote := range notes {
			copiedNote, err := queries.CreateNote(ctx, gen.CreateNoteParams{
				Term: sourceNote.Term, TermKey: sourceNote.TermKey, Translation: sourceNote.Translation,
				Transcription: sourceNote.Transcription, Example: sourceNote.Example,
				ExampleTranslation: sourceNote.ExampleTranslation, Notes: sourceNote.Notes,
				Tags: sourceNote.Tags, Alternatives: sourceNote.Alternatives, Fields: sourceNote.Fields,
				DeckID: copied.ID, UserID: recipientID,
			})
			if err != nil {
				return gen.DeckShare{}, fmt.Errorf("copy source note: %w: %v", ErrInternal, err)
			}
			for _, direction := range []string{"forward", "reverse"} {
				if direction == "reverse" && !copied.ReverseCards {
					continue
				}
				if _, err := queries.CreateCard(ctx, gen.CreateCardParams{
					NoteID: copiedNote.ID, UserID: recipientID, Direction: direction,
				}); err != nil {
					return gen.DeckShare{}, fmt.Errorf("create copied card: %w: %v", ErrInternal, err)
				}
			}
		}
	}
	if copiedRootID == 0 {
		return gen.DeckShare{}, fmt.Errorf("copy root deck missing: %w", ErrInternal)
	}
	share, err := queries.CreateCompletedDeckCopyShare(ctx, gen.CreateCompletedDeckCopyShareParams{
		RecipientUserID: recipientID,
		CopiedDeckID:    sql.NullInt64{Int64: copiedRootID, Valid: true},
		SourceDeckID:    sourceDeckID,
		OwnerID:         ownerID,
	})
	if err != nil {
		return gen.DeckShare{}, fmt.Errorf("record copied deck share: %w: %v", ErrInternal, err)
	}
	return share, nil
}

func (s *Service) SharesForDeck(ctx context.Context, ownerID, deckID int64) ([]DeckShare, error) {
	if !s.ready() || ownerID <= 0 || deckID <= 0 {
		return nil, ErrInvalidInput
	}
	if _, err := s.database.Queries().GetDeck(ctx, gen.GetDeckParams{ID: deckID, OwnerID: ownerID}); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("get deck for shares: %w: %v", ErrInternal, err)
	}
	rows, err := s.database.Queries().ListSharesForDeckOwner(ctx, gen.ListSharesForDeckOwnerParams{DeckID: deckID, OwnerID: ownerID})
	if err != nil {
		return nil, fmt.Errorf("list deck shares: %w: %v", ErrInternal, err)
	}
	result := make([]DeckShare, 0, len(rows))
	for _, row := range rows {
		result = append(result, DeckShare{
			ID: row.ID, SourceDeckID: row.SourceDeckID, RecipientID: row.RecipientID,
			RecipientName: row.RecipientName, RecipientEmail: row.RecipientEmail,
			Mode: row.Mode, Status: row.Status, CopiedDeckID: int64Pointer(row.CopiedDeckID),
			CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
		})
	}
	return result, nil
}

func (s *Service) SharesForUser(ctx context.Context, userID int64) ([]DeckShare, error) {
	if !s.ready() || userID <= 0 {
		return nil, ErrInvalidInput
	}
	rows, err := s.database.Queries().ListSharesForParticipant(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list user deck shares: %w: %v", ErrInternal, err)
	}
	result := make([]DeckShare, 0, len(rows))
	for _, row := range rows {
		role := "recipient"
		if row.SourceOwnerID == userID {
			role = "owner"
		}
		result = append(result, DeckShare{
			ID: row.ID, SourceDeckID: row.SourceDeckID, SourceDeckName: row.SourceDeckName,
			SourceOwnerID: row.SourceOwnerID, SourceOwnerName: row.SourceOwnerName,
			RecipientID: row.RecipientID, RecipientName: row.RecipientName,
			RecipientEmail: row.RecipientEmail, Mode: row.Mode, Status: row.Status,
			CopiedDeckID: int64Pointer(row.CopiedDeckID), CreatedAt: row.CreatedAt.UTC(),
			UpdatedAt: row.UpdatedAt.UTC(), Role: role,
		})
	}
	return result, nil
}

func (s *Service) EndSubscription(ctx context.Context, userID, sourceDeckID, shareID int64) error {
	if !s.ready() || userID <= 0 || sourceDeckID <= 0 || shareID <= 0 {
		return ErrInvalidInput
	}
	tx, queries, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	share, err := queries.GetShareForParticipant(ctx, gen.GetShareForParticipantParams{ShareID: shareID, UserID: userID})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get deck share: %w: %v", ErrInternal, err)
	}
	if share.Mode != "subscribe" || share.SourceDeckID != sourceDeckID {
		return ErrNotFound
	}
	if share.Status != "active" {
		return commit(tx, nil, "end deck subscription")
	}
	if share.SourceOwnerID == userID {
		_, err = queries.RevokeSubscription(ctx, gen.RevokeSubscriptionParams{UpdatedAt: s.clock.Now().UTC(), ShareID: shareID, UserID: userID})
	} else if share.RecipientID == userID {
		_, err = queries.UnsubscribeFromDeck(ctx, gen.UnsubscribeFromDeckParams{UpdatedAt: s.clock.Now().UTC(), ShareID: shareID, UserID: userID})
	} else {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("end deck subscription: %w: %v", ErrInternal, err)
	}
	return commit(tx, nil, "end deck subscription")
}

func (s *Service) requireFriend(ctx context.Context, queries *gen.Queries, first, second int64) error {
	userA, userB := orderedPair(first, second)
	if _, err := queries.GetFriendship(ctx, gen.GetFriendshipParams{UserAID: userA, UserBID: userB}); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("check friend before sharing: %w: %v", ErrInternal, err)
	}
	return nil
}

func publicDeckShare(share gen.DeckShare) DeckShare {
	return DeckShare{
		ID: share.ID, SourceDeckID: share.SourceDeckID, RecipientID: share.RecipientID,
		Mode: share.Mode, Status: share.Status, CopiedDeckID: int64Pointer(share.CopiedDeckID),
		CreatedAt: share.CreatedAt.UTC(), UpdatedAt: share.UpdatedAt.UTC(),
	}
}

func int64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copy := value.Int64
	return &copy
}
