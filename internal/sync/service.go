// Package sync implements owner-scoped offline review ingestion and change feeds.
package sync

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/study"
)

var (
	ErrInvalidInput  = errors.New("invalid sync input")
	ErrInvalidCursor = errors.New("invalid sync cursor")
	ErrInternal      = errors.New("sync service error")
)

const (
	maxReviewBatch = 100
	maxPageSize    = 200
	maxCursorBytes = 512
	cursorVersion  = 1
	cursorKeyName  = "sync_cursor_hmac_key"
)

// Reviewer is the authoritative review service. Sync delegates every review
// to it so idempotency, validation, and FSRS replay remain identical to online study.
type Reviewer interface {
	Review(context.Context, int64, study.ReviewInput) (study.Card, error)
}

type Service struct {
	database *db.DB
	reviewer Reviewer
}

type Request struct {
	Reviews []study.ReviewInput `json:"reviews"`
	Since   string              `json:"since,omitempty"`
}

type Response struct {
	Applied      []string          `json:"applied"`
	Rejected     []RejectedReview  `json:"rejected"`
	ChangedCards []json.RawMessage `json:"changed_cards"`
	ChangedNotes []json.RawMessage `json:"changed_notes"`
	Cursor       string            `json:"cursor"`
	HasMore      bool              `json:"has_more"`
}

// RejectedReview identifies a permanently invalid review without blocking feed delivery.
type RejectedReview struct {
	ID   string `json:"id"`
	Code string `json:"code"`
}

type cursorPayload struct {
	Version int   `json:"v"`
	UserID  int64 `json:"user_id"`
	ScopeID int64 `json:"scope_id"`
	Seq     int64 `json:"seq"`
}

type syncCardSnapshot struct {
	SyncSeq       int64      `json:"sync_seq"`
	ID            int64      `json:"id"`
	NoteID        int64      `json:"note_id"`
	UserID        int64      `json:"user_id"`
	Direction     string     `json:"direction"`
	State         int64      `json:"state"`
	Due           time.Time  `json:"due"`
	Stability     *float64   `json:"stability,omitempty"`
	Difficulty    *float64   `json:"difficulty,omitempty"`
	ElapsedDays   int64      `json:"elapsed_days"`
	ScheduledDays int64      `json:"scheduled_days"`
	Step          int64      `json:"step"`
	Reps          int64      `json:"reps"`
	Lapses        int64      `json:"lapses"`
	LastReview    *time.Time `json:"last_review,omitempty"`
	Suspended     bool       `json:"suspended"`
	IsLeech       bool       `json:"is_leech"`
	Deleted       bool       `json:"deleted"`
}

func NewService(database *db.DB, reviewer Reviewer) *Service {
	return &Service{database: database, reviewer: reviewer}
}

// Sync first validates the caller's cursor, idempotently applies its review
// batch, then returns the next bounded page of changes after that cursor.
func (s *Service) Sync(ctx context.Context, userID int64, input Request) (Response, error) {
	if s == nil || s.database == nil || s.reviewer == nil || userID <= 0 {
		return Response{}, ErrInternal
	}
	if len(input.Reviews) > maxReviewBatch || len(input.Since) > maxCursorBytes {
		return Response{}, ErrInvalidInput
	}
	seenReviewIDs := make(map[string]struct{}, len(input.Reviews))
	for _, review := range input.Reviews {
		if _, duplicate := seenReviewIDs[review.ID]; duplicate {
			return Response{}, ErrInvalidInput
		}
		seenReviewIDs[review.ID] = struct{}{}
	}

	key, err := s.getOrCreateCursorKey(ctx)
	if err != nil {
		return Response{}, fmt.Errorf("initialize sync cursor key: %w: %w", ErrInternal, err)
	}
	scopeID, err := s.database.Queries().GetSyncOwnerScope(ctx, userID)
	if err != nil {
		return Response{}, fmt.Errorf("read sync owner scope: %w: %w", ErrInternal, err)
	}
	if scopeID <= 0 {
		return Response{}, fmt.Errorf("read sync owner scope: %w", ErrInternal)
	}
	seq := int64(0)
	if input.Since != "" {
		seq, err = decodeCursor(input.Since, userID, scopeID, key)
		if err != nil {
			return Response{}, ErrInvalidCursor
		}
	}

	response := Response{
		Applied:      make([]string, 0, len(input.Reviews)),
		Rejected:     make([]RejectedReview, 0),
		ChangedCards: make([]json.RawMessage, 0),
		ChangedNotes: make([]json.RawMessage, 0),
	}
	for _, review := range input.Reviews {
		if _, err := s.reviewer.Review(ctx, userID, review); err != nil {
			code := reviewRejectionCode(err)
			if code == "" {
				return Response{}, err
			}
			response.Rejected = append(response.Rejected, RejectedReview{ID: review.ID, Code: code})
			if snapshot, snapshotErr := s.currentCardSnapshot(ctx, userID, scopeID, review.CardID); snapshotErr == nil {
				response.ChangedCards = append(response.ChangedCards, snapshot)
			} else if !errors.Is(snapshotErr, sql.ErrNoRows) {
				return Response{}, fmt.Errorf("read rejected review card snapshot: %w: %v", ErrInternal, snapshotErr)
			}
			continue
		}
		response.Applied = append(response.Applied, review.ID)
	}

	rows, err := s.database.Queries().ListSyncEvents(ctx, gen.ListSyncEventsParams{
		OwnerID: userID, OwnerScopeID: scopeID, AfterSeq: seq, PageSize: maxPageSize + 1,
	})
	if err != nil {
		return Response{}, fmt.Errorf("list sync events: %w: %w", ErrInternal, err)
	}
	response.HasMore = len(rows) > maxPageSize
	if response.HasMore {
		rows = rows[:maxPageSize]
	}
	for _, row := range rows {
		if !json.Valid([]byte(row.Payload)) {
			return Response{}, fmt.Errorf("invalid persisted sync event payload: %w", ErrInternal)
		}
		payload, payloadErr := addSyncSequence(row.Payload, row.Seq)
		if payloadErr != nil {
			return Response{}, fmt.Errorf("add sync event sequence: %w: %v", ErrInternal, payloadErr)
		}
		switch row.EntityType {
		case "card":
			response.ChangedCards = append(response.ChangedCards, payload)
		case "note":
			response.ChangedNotes = append(response.ChangedNotes, payload)
		default:
			return Response{}, fmt.Errorf("unknown persisted sync event type: %w", ErrInternal)
		}
		seq = row.Seq
	}
	response.Cursor, err = encodeCursor(userID, scopeID, seq, key)
	if err != nil {
		return Response{}, fmt.Errorf("encode sync cursor: %w: %w", ErrInternal, err)
	}
	return response, nil
}

func addSyncSequence(payload string, seq int64) (json.RawMessage, error) {
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		return nil, fmt.Errorf("decode sync event payload: %w", err)
	}
	sequence, err := json.Marshal(seq)
	if err != nil {
		return nil, fmt.Errorf("marshal sync sequence: %w", err)
	}
	snapshot["sync_seq"] = sequence
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("encode sync event payload: %w", err)
	}
	return encoded, nil
}

func (s *Service) currentCardSnapshot(ctx context.Context, userID, scopeID, cardID int64) (json.RawMessage, error) {
	card, err := s.database.Queries().GetSyncCardSnapshot(ctx, gen.GetSyncCardSnapshotParams{
		CardID:       cardID,
		OwnerID:      userID,
		OwnerScopeID: scopeID,
	})
	if err != nil {
		return nil, err
	}
	payload := syncCardSnapshot{
		SyncSeq: card.SyncSeq,
		ID:      card.ID, NoteID: card.NoteID, UserID: card.UserID, Direction: card.Direction,
		State: card.State, Due: card.Due.UTC(), ElapsedDays: card.ElapsedDays,
		ScheduledDays: card.ScheduledDays, Step: card.Step, Reps: card.Reps, Lapses: card.Lapses,
		Suspended: card.Suspended, IsLeech: card.IsLeech,
	}
	if card.Stability.Valid {
		value := card.Stability.Float64
		payload.Stability = &value
	}
	if card.Difficulty.Valid {
		value := card.Difficulty.Float64
		payload.Difficulty = &value
	}
	if card.LastReview.Valid {
		value := card.LastReview.Time.UTC()
		payload.LastReview = &value
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal rejected review card snapshot: %w", err)
	}
	return encoded, nil
}

func reviewRejectionCode(err error) string {
	switch {
	case errors.Is(err, study.ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, study.ErrNotFound):
		return "not_found"
	case errors.Is(err, study.ErrConflict):
		return "conflict"
	case errors.Is(err, study.ErrChoiceUnavailable):
		return "choice_unavailable"
	default:
		return ""
	}
}

func (s *Service) getOrCreateCursorKey(ctx context.Context) ([]byte, error) {
	queries := s.database.Queries()
	var err error
	value, err := queries.GetSyncCursorKey(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		key := make([]byte, sha256.Size)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate sync cursor key: %w", err)
		}
		encoded, marshalErr := json.Marshal(base64.RawStdEncoding.EncodeToString(key))
		if marshalErr != nil {
			return nil, fmt.Errorf("encode sync cursor key: %w", marshalErr)
		}
		if err := queries.InsertSyncCursorKey(ctx, string(encoded)); err != nil {
			return nil, fmt.Errorf("persist sync cursor key: %w", err)
		}
		value, err = queries.GetSyncCursorKey(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("read sync cursor key: %w", err)
	}
	var encoded string
	if err := json.Unmarshal([]byte(value), &encoded); err != nil {
		return nil, fmt.Errorf("decode sync cursor key: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(key) != sha256.Size {
		return nil, errors.New("stored sync cursor key is invalid")
	}
	return key, nil
}

func encodeCursor(userID, scopeID, seq int64, key []byte) (string, error) {
	payload, err := json.Marshal(cursorPayload{Version: cursorVersion, UserID: userID, ScopeID: scopeID, Seq: seq})
	if err != nil {
		return "", fmt.Errorf("marshal sync cursor: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write([]byte(body)); err != nil {
		return "", fmt.Errorf("sign sync cursor: %w", err)
	}
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeCursor(value string, userID, scopeID int64, key []byte) (int64, error) {
	if len(value) == 0 || len(value) > maxCursorBytes {
		return 0, ErrInvalidCursor
	}
	body, signature, ok := strings.Cut(value, ".")
	if !ok || body == "" || signature == "" || strings.Contains(signature, ".") {
		return 0, ErrInvalidCursor
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return 0, ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(body))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return 0, ErrInvalidCursor
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return 0, ErrInvalidCursor
	}
	var payload cursorPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil ||
		payload.Version != cursorVersion || payload.UserID != userID || payload.ScopeID != scopeID || payload.Seq < 0 {
		return 0, ErrInvalidCursor
	}
	return payload.Seq, nil
}
