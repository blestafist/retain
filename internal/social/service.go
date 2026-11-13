// Package social implements friend relationships, private activity summaries,
// and owner-controlled deck sharing.
package social

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/stats"
	"modernc.org/sqlite"
)

var (
	ErrInvalidInput = errors.New("invalid social input")
	ErrNotFound     = errors.New("social resource not found")
	ErrConflict     = errors.New("social resource conflict")
	ErrInternal     = errors.New("social service error")
)

type Service struct {
	database *db.DB
	clock    clock.Clock
	stats    *stats.Service
}

func NewService(database *db.DB, c clock.Clock) *Service {
	if c == nil {
		c = clock.Real{}
	}
	return &Service{database: database, clock: c, stats: stats.NewService(database, c)}
}

type Friend struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	FriendsAt time.Time `json:"friends_at"`
}

type FriendRequest struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Direction string    `json:"direction"`
	CreatedAt time.Time `json:"created_at"`
}

type SocialSettings struct {
	Visible   bool      `json:"visible"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ActivityDay struct {
	UserID      int64  `json:"user_id"`
	Name        string `json:"name"`
	Day         string `json:"day"`
	ReviewCount int64  `json:"review_count"`
	StreakDays  int    `json:"streak_days"`
}

type LeaderboardEntry struct {
	Rank         int    `json:"rank"`
	UserID       int64  `json:"user_id"`
	Name         string `json:"name"`
	ReviewCount  int64  `json:"review_count"`
	LearnedCount int64  `json:"learned_count"`
	StreakDays   int    `json:"streak_days"`
}

func (s *Service) Friends(ctx context.Context, userID int64) ([]Friend, error) {
	if !s.ready() || userID <= 0 {
		return nil, ErrInternal
	}
	rows, err := s.database.Queries().ListFriends(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list friends: %w: %v", ErrInternal, err)
	}
	result := make([]Friend, 0, len(rows))
	for _, row := range rows {
		result = append(result, Friend{ID: row.ID, Email: row.Email, Name: row.Name, FriendsAt: row.CreatedAt.UTC()})
	}
	return result, nil
}

// RequestFriend always returns the same successful result for a syntactically
// valid email, whether or not an account exists, to prevent account discovery.
func (s *Service) RequestFriend(ctx context.Context, userID int64, email string) error {
	if !s.ready() || userID <= 0 {
		return ErrInternal
	}
	normalized, ok := normalizeEmail(email)
	if !ok {
		return ErrInvalidInput
	}
	tx, queries, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	account, err := queries.GetUserByEmail(ctx, normalized)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && account.ID == userID) {
		return commit(tx, nil, "submit friend request")
	}
	if err != nil {
		return fmt.Errorf("find friend request recipient: %w: %v", ErrInternal, err)
	}
	otherID := account.ID
	userA, userB := orderedPair(userID, otherID)
	if _, err := queries.GetFriendship(ctx, gen.GetFriendshipParams{UserAID: userA, UserBID: userB}); err == nil {
		return commit(tx, nil, "submit friend request")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check friendship: %w: %v", ErrInternal, err)
	}
	if pending, err := queries.GetPendingFriendRequestPair(ctx, gen.GetPendingFriendRequestPairParams{UserAID: userA, UserBID: userB}); err == nil {
		// A reciprocal pending request is already visible in the inbox; accepting
		// it there is an explicit action, not an implicit friendship transition.
		_ = pending
		return commit(tx, nil, "submit friend request")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check pending friend request: %w: %v", ErrInternal, err)
	}
	if _, err := queries.CreateFriendRequest(ctx, gen.CreateFriendRequestParams{SenderID: userID, RecipientID: otherID}); err != nil {
		return fmt.Errorf("create friend request: %w: %v", ErrInternal, err)
	}
	return commit(tx, nil, "submit friend request")
}

func (s *Service) Requests(ctx context.Context, userID int64) ([]FriendRequest, error) {
	if !s.ready() || userID <= 0 {
		return nil, ErrInternal
	}
	rows, err := s.database.Queries().ListFriendRequestsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list friend requests: %w: %v", ErrInternal, err)
	}
	result := make([]FriendRequest, 0, len(rows))
	for _, row := range rows {
		request := FriendRequest{ID: row.ID, CreatedAt: row.CreatedAt.UTC()}
		if row.RecipientID == userID {
			request.UserID, request.Name, request.Email, request.Direction = row.SenderID, row.SenderName, row.SenderEmail, "incoming"
		} else {
			request.UserID, request.Name, request.Email, request.Direction = row.RecipientID, row.RecipientName, row.RecipientEmail, "outgoing"
		}
		result = append(result, request)
	}
	return result, nil
}

func (s *Service) AcceptFriendRequest(ctx context.Context, userID, requestID int64) error {
	return s.updateFriendRequest(ctx, userID, requestID, "accept")
}

func (s *Service) DeclineFriendRequest(ctx context.Context, userID, requestID int64) error {
	return s.updateFriendRequest(ctx, userID, requestID, "decline")
}

func (s *Service) CancelFriendRequest(ctx context.Context, userID, requestID int64) error {
	return s.updateFriendRequest(ctx, userID, requestID, "cancel")
}

func (s *Service) updateFriendRequest(ctx context.Context, userID, requestID int64, action string) error {
	if !s.ready() || userID <= 0 || requestID <= 0 {
		return ErrInvalidInput
	}
	tx, queries, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.clock.Now().UTC()
	var request gen.FriendRequest
	switch action {
	case "accept":
		request, err = queries.AcceptFriendRequest(ctx, gen.AcceptFriendRequestParams{UpdatedAt: now, ID: requestID, UserID: userID})
	case "decline":
		request, err = queries.DeclineFriendRequest(ctx, gen.DeclineFriendRequestParams{UpdatedAt: now, ID: requestID, UserID: userID})
	case "cancel":
		request, err = queries.CancelFriendRequest(ctx, gen.CancelFriendRequestParams{UpdatedAt: now, ID: requestID, UserID: userID})
	default:
		return ErrInvalidInput
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("update friend request: %w: %v", ErrInternal, err)
	}
	if action == "accept" {
		userA, userB := orderedPair(request.SenderID, request.RecipientID)
		if err := queries.InsertFriendship(ctx, gen.InsertFriendshipParams{UserAID: userA, UserBID: userB}); err != nil {
			return fmt.Errorf("accept friend request: %w: %v", ErrInternal, err)
		}
		if err := queries.CancelReversePendingFriendRequest(ctx, gen.CancelReversePendingFriendRequestParams{
			UpdatedAt: now, UserBID: request.RecipientID, UserAID: request.SenderID,
		}); err != nil {
			return fmt.Errorf("cancel reciprocal friend request: %w: %v", ErrInternal, err)
		}
	}
	return commit(tx, nil, "update friend request")
}

func (s *Service) RemoveFriend(ctx context.Context, userID, friendID int64) error {
	if !s.ready() || userID <= 0 || friendID <= 0 || userID == friendID {
		return ErrInvalidInput
	}
	userA, userB := orderedPair(userID, friendID)
	affected, err := s.database.Queries().DeleteFriendship(ctx, gen.DeleteFriendshipParams{UserAID: userA, UserBID: userB})
	if err != nil {
		return fmt.Errorf("remove friendship: %w: %v", ErrInternal, err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) Settings(ctx context.Context, userID int64) (SocialSettings, error) {
	if !s.ready() || userID <= 0 {
		return SocialSettings{}, ErrInternal
	}
	row, err := s.database.Queries().GetSocialSettings(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return SocialSettings{Visible: false}, nil
	}
	if err != nil {
		return SocialSettings{}, fmt.Errorf("get social settings: %w: %v", ErrInternal, err)
	}
	return SocialSettings{Visible: row.Visible, UpdatedAt: row.UpdatedAt.UTC()}, nil
}

func (s *Service) UpdateSettings(ctx context.Context, userID int64, visible bool) (SocialSettings, error) {
	if !s.ready() || userID <= 0 {
		return SocialSettings{}, ErrInvalidInput
	}
	row, err := s.database.Queries().UpsertSocialSettings(ctx, gen.UpsertSocialSettingsParams{
		UserID: userID, Visible: visible, UpdatedAt: s.clock.Now().UTC(),
	})
	if err != nil {
		return SocialSettings{}, fmt.Errorf("update social settings: %w: %v", ErrInternal, err)
	}
	return SocialSettings{Visible: row.Visible, UpdatedAt: row.UpdatedAt.UTC()}, nil
}

func (s *Service) Activity(ctx context.Context, userID int64) ([]ActivityDay, error) {
	if !s.ready() || userID <= 0 {
		return nil, ErrInternal
	}
	now := s.clock.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -6)
	rows, err := s.database.Queries().ListSocialActivityDays(ctx, gen.ListSocialActivityDaysParams{
		StartAt: start, EndAt: now, UserID: userID,
	})
	if err != nil {
		return nil, fmt.Errorf("list social activity: %w: %v", ErrInternal, err)
	}
	result := make([]ActivityDay, 0, len(rows))
	streaks := make(map[int64]int)
	for _, row := range rows {
		streak, ok := streaks[row.UserID]
		if !ok {
			overview, err := s.stats.Overview(ctx, row.UserID)
			if err != nil {
				return nil, fmt.Errorf("read friend streak: %w: %v", ErrInternal, err)
			}
			streak = overview.StreakDays
			streaks[row.UserID] = streak
		}
		result = append(result, ActivityDay{
			UserID: row.UserID, Name: row.Name, Day: fmt.Sprint(row.Day),
			ReviewCount: row.ReviewCount, StreakDays: streak,
		})
	}
	return result, nil
}

func (s *Service) Leaderboard(ctx context.Context, userID int64, period string) ([]LeaderboardEntry, error) {
	if !s.ready() || userID <= 0 || (period != "" && period != "week") {
		return nil, ErrInvalidInput
	}
	now := s.clock.Now().UTC()
	weekStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	weekStart = weekStart.AddDate(0, 0, -((int(weekStart.Weekday()) + 6) % 7))
	rows, err := s.database.Queries().ListSocialLeaderboardStats(ctx, gen.ListSocialLeaderboardStatsParams{
		UserID: userID, StartAt: weekStart, EndAt: now,
	})
	if err != nil {
		return nil, fmt.Errorf("list weekly leaderboard: %w: %v", ErrInternal, err)
	}
	result := make([]LeaderboardEntry, 0, len(rows))
	for _, row := range rows {
		overview, err := s.stats.Overview(ctx, row.ID)
		if err != nil {
			return nil, fmt.Errorf("read leaderboard streak: %w: %v", ErrInternal, err)
		}
		result = append(result, LeaderboardEntry{
			UserID: row.ID, Name: row.Name, ReviewCount: row.ReviewCount,
			LearnedCount: row.LearnedCount, StreakDays: overview.StreakDays,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ReviewCount != result[j].ReviewCount {
			return result[i].ReviewCount > result[j].ReviewCount
		}
		if result[i].LearnedCount != result[j].LearnedCount {
			return result[i].LearnedCount > result[j].LearnedCount
		}
		if result[i].StreakDays != result[j].StreakDays {
			return result[i].StreakDays > result[j].StreakDays
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	for i := range result {
		result[i].Rank = i + 1
	}
	return result, nil
}

func normalizeEmail(value string) (string, bool) {
	value = strings.TrimSpace(value)
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || strings.ContainsAny(value, "\r\n") {
		return "", false
	}
	return strings.ToLower(value), true
}

func orderedPair(first, second int64) (int64, int64) {
	if first < second {
		return first, second
	}
	return second, first
}

func (s *Service) begin(ctx context.Context) (*sql.Tx, *gen.Queries, error) {
	tx, err := s.database.SQL.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("begin social transaction: %w: %v", ErrInternal, err)
	}
	return tx, s.database.Queries().WithTx(tx), nil
}

func commit(tx *sql.Tx, result error, operation string) error {
	if result != nil {
		return result
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: %w: %v", operation, ErrInternal, err)
	}
	return nil
}

func (s *Service) ready() bool {
	return s != nil && s.database != nil && s.database.SQL != nil && s.clock != nil && s.stats != nil
}

func isUniqueConstraint(err error) bool {
	var sqliteError *sqlite.Error
	return errors.As(err, &sqliteError) && sqliteError.Code()&0xff == 19
}
