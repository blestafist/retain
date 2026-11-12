package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/srs"
	"github.com/pestit/retain/internal/study"
	syncservice "github.com/pestit/retain/internal/sync"
)

type studyRoutesFixture struct {
	database    *db.DB
	handler     http.Handler
	ownerCookie *http.Cookie
	otherCookie *http.Cookie
	ownerID     int64
	otherID     int64
	ownerDeckID int64
	otherDeckID int64
	cardID      int64
	otherCardID int64
	now         time.Time
}

type studyRoutesClock struct{ now time.Time }

func (c studyRoutesClock) Now() time.Time { return c.now }

func newStudyRoutesFixture(t *testing.T) *studyRoutesFixture {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "study-routes.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})

	now := time.Date(2026, time.April, 3, 12, 0, 0, 0, time.UTC)
	authService := auth.NewService(database, studyRoutesClock{now: now})
	ownerID, err := authService.CreateAdmin(ctx, "study-routes@example.test", "Study owner", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	login, err := authService.Login(ctx, auth.LoginInput{
		Email: "study-routes@example.test", Password: "correct horse battery staple",
	}, "study-routes-test")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	queries := database.Queries()
	other, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email: "study-routes-other@example.test", Name: "Other", PasswordHash: "unused", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser(other) error = %v", err)
	}
	if _, err := queries.CreateUserSettings(ctx, other.ID); err != nil {
		t.Fatalf("CreateUserSettings(other) error = %v", err)
	}
	otherToken, err := auth.GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken(other) error = %v", err)
	}
	otherDigest := auth.HashSessionToken(otherToken)
	if _, err := queries.CreateSession(ctx, gen.CreateSessionParams{
		TokenHash: otherDigest[:], UserID: other.ID, ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSession(other) error = %v", err)
	}

	ownerDeckID := studyRoutesCreateCard(t, database, ownerID, "Owner deck", "hello", srs.New, now)
	otherDeckID := studyRoutesCreateCard(t, database, other.ID, "Other deck", "foreign", srs.New, now)
	cardID := studyRoutesCardForTerm(t, database, ownerID, ownerDeckID, "hello")
	otherCardID := studyRoutesCardForTerm(t, database, other.ID, otherDeckID, "foreign")
	mux := http.NewServeMux()
	studyService := study.NewService(database, studyRoutesClock{now: now})
	RegisterStudyRoutes(mux, authService, studyService)
	RegisterSyncRoutes(mux, authService, syncservice.NewService(database, studyService))
	return &studyRoutesFixture{
		database: database, handler: mux,
		ownerCookie: &http.Cookie{Name: sessionCookieName, Value: login.Token},
		otherCookie: &http.Cookie{Name: sessionCookieName, Value: otherToken},
		ownerID:     ownerID, otherID: other.ID, ownerDeckID: ownerDeckID, otherDeckID: otherDeckID,
		cardID: cardID, otherCardID: otherCardID, now: now,
	}
}

func TestSyncRouteAuthenticatesAndBindsCursorToOwner(t *testing.T) {
	f := newStudyRoutesFixture(t)
	unauthenticated := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/sync", `{"reviews":[]}`, nil)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated sync = %d/%s, want 401", unauthenticated.Code, unauthenticated.Body)
	}
	ownerResponse := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/sync", `{"reviews":[]}`, f.ownerCookie)
	if ownerResponse.Code != http.StatusOK {
		t.Fatalf("owner sync = %d/%s, want 200", ownerResponse.Code, ownerResponse.Body)
	}
	var result struct {
		Cursor       string `json:"cursor"`
		HasMore      bool   `json:"has_more"`
		ChangedCards []any  `json:"changed_cards"`
	}
	if err := json.Unmarshal(ownerResponse.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	if result.Cursor == "" || result.HasMore || len(result.ChangedCards) != 1 {
		t.Fatalf("sync response = %+v, want signed cursor and initial owner card", result)
	}
	foreign := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/sync",
		`{"reviews":[],"since":"`+result.Cursor+`"}`, f.otherCookie)
	if foreign.Code != http.StatusBadRequest || !strings.Contains(foreign.Body.String(), `"code":"invalid_cursor"`) {
		t.Errorf("cross-owner cursor response = %d/%s, want 400 invalid_cursor", foreign.Code, foreign.Body)
	}
	invalidReview := `{"reviews":[{"id":"018f0f3e-7b00-7000-8000-000000000001","card_id":999999,"rating":3,"mode":"flip","reviewed_at":"2026-04-03T12:00:00Z","duration_ms":500}]}`
	rejected := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/sync", invalidReview, f.ownerCookie)
	if rejected.Code != http.StatusOK || !strings.Contains(rejected.Body.String(), `"code":"not_found"`) {
		t.Errorf("rejected review response = %d/%s, want 200 with per-review rejection", rejected.Code, rejected.Body)
	}
}

// Creates a deck, note, and one new card; the card ID is looked up by term.
func studyRoutesCreateCard(t *testing.T, database *db.DB, ownerID int64, deckName, term string, state srs.State, now time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	deckResult, err := database.SQL.ExecContext(ctx, "INSERT INTO decks(owner_id, name) VALUES (?, ?)", ownerID, deckName)
	if err != nil {
		t.Fatalf("insert deck: %v", err)
	}
	deckID, err := deckResult.LastInsertId()
	if err != nil {
		t.Fatalf("deck LastInsertId: %v", err)
	}
	noteResult, err := database.SQL.ExecContext(ctx,
		"INSERT INTO notes(deck_id, term, term_key, translation) VALUES (?, ?, ?, ?)",
		deckID, term, term, "translation")
	if err != nil {
		t.Fatalf("insert note: %v", err)
	}
	noteID, err := noteResult.LastInsertId()
	if err != nil {
		t.Fatalf("note LastInsertId: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx,
		"INSERT INTO cards(note_id, user_id, direction, state, due) VALUES (?, ?, 'forward', ?, ?)",
		noteID, ownerID, int64(state), now,
	); err != nil {
		t.Fatalf("insert card: %v", err)
	}
	return deckID
}

func studyRoutesCardForTerm(t *testing.T, database *db.DB, ownerID, deckID int64, term string) int64 {
	t.Helper()
	var cardID int64
	err := database.SQL.QueryRowContext(context.Background(), `
		SELECT cards.id FROM cards
		JOIN notes ON notes.id = cards.note_id
		WHERE cards.user_id = ? AND notes.deck_id = ? AND notes.term = ?`, ownerID, deckID, term).Scan(&cardID)
	if err != nil {
		t.Fatalf("find card %q: %v", term, err)
	}
	return cardID
}

func studyRoutesRequest(handler http.Handler, method, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestStudyRoutesQueueReviewUndoAndOwnerIsolation(t *testing.T) {
	f := newStudyRoutesFixture(t)
	for _, request := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/v1/study/queue", ""},
		{http.MethodPost, "/api/v1/study/queue/next", `{"limit":10,"exclude_card_ids":[]}`},
		{http.MethodPost, "/api/v1/study/review", `{}`},
		{http.MethodPost, "/api/v1/study/undo", `{}`},
	} {
		response := studyRoutesRequest(f.handler, request.method, request.target, request.body, nil)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated %s %s status = %d, want 401", request.method, request.target, response.Code)
		}
	}

	for _, query := range []string{"?limit=0", "?limit=501", "?limit=abc", "?deck_id=0", "?other=1", "?limit=1&limit=2"} {
		response := studyRoutesRequest(f.handler, http.MethodGet, "/api/v1/study/queue"+query, "", f.ownerCookie)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_input"`) {
			t.Errorf("invalid queue query %q response = %d/%s, want 400 invalid_input", query, response.Code, response.Body)
		}
	}
	queue := studyRoutesRequest(f.handler, http.MethodGet, "/api/v1/study/queue?deck_id="+strconv.FormatInt(f.ownerDeckID, 10)+"&limit=10", "", f.ownerCookie)
	if queue.Code != http.StatusOK || !strings.Contains(queue.Body.String(), `"front":"hello"`) {
		t.Fatalf("owner queue = %d/%s, want card prompt", queue.Code, queue.Body)
	}
	nextQueue := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/queue/next",
		`{"deck_id":`+strconv.FormatInt(f.ownerDeckID, 10)+`,"limit":10,"exclude_card_ids":[`+strconv.FormatInt(f.cardID, 10)+`]}`, f.ownerCookie)
	if nextQueue.Code != http.StatusOK || strings.TrimSpace(nextQueue.Body.String()) != "[]" {
		t.Errorf("next owner queue = %d/%s, want empty after excluding its only card", nextQueue.Code, nextQueue.Body)
	}
	duplicateExclusion := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/queue/next",
		`{"limit":10,"exclude_card_ids":[`+strconv.FormatInt(f.cardID, 10)+`,`+strconv.FormatInt(f.cardID, 10)+`]}`, f.ownerCookie)
	if duplicateExclusion.Code != http.StatusBadRequest {
		t.Errorf("next queue with duplicate exclusions = %d/%s, want 400", duplicateExclusion.Code, duplicateExclusion.Body)
	}
	foreignQueue := studyRoutesRequest(f.handler, http.MethodGet,
		"/api/v1/study/queue?deck_id="+strconv.FormatInt(f.otherDeckID, 10), "", f.ownerCookie)
	if foreignQueue.Code != http.StatusNotFound {
		t.Errorf("foreign deck queue = %d/%s, want 404", foreignQueue.Code, foreignQueue.Body)
	}

	const reviewID = "018f0f3e-7b00-7000-8000-000000000031"
	input := study.ReviewInput{ID: reviewID, CardID: f.cardID, Rating: srs.Good, Mode: "flip", ReviewedAt: f.now, DurationMS: 900}
	response := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/review", mustStudyJSON(t, input), f.ownerCookie)
	if response.Code != http.StatusOK {
		t.Fatalf("review response = %d/%s, want 200", response.Code, response.Body)
	}
	var reviewed study.Card
	if err := json.Unmarshal(response.Body.Bytes(), &reviewed); err != nil {
		t.Fatal(err)
	}
	if reviewed.ID != f.cardID || reviewed.State != srs.Learning || reviewed.Reps != 1 {
		t.Fatalf("review card = %+v, want transitioned card", reviewed)
	}
	if _, err := f.database.Queries().GetReviewLog(context.Background(), reviewID); err != nil {
		t.Fatalf("review log was not persisted: %v", err)
	}
	foreignInput := input
	foreignInput.ID = "018f0f3e-7b00-7000-8000-000000000032"
	foreignReview := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/review", mustStudyJSON(t, foreignInput), f.otherCookie)
	if foreignReview.Code != http.StatusNotFound {
		t.Errorf("cross-owner review = %d/%s, want 404", foreignReview.Code, foreignReview.Body)
	}

	undo := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/undo", `{"review_id":"`+reviewID+`"}`, f.ownerCookie)
	if undo.Code != http.StatusOK {
		t.Fatalf("undo response = %d/%s, want 200", undo.Code, undo.Body)
	}
	var undone study.Card
	if err := json.Unmarshal(undo.Body.Bytes(), &undone); err != nil {
		t.Fatal(err)
	}
	if undone.State != srs.New || undone.Reps != 0 {
		t.Errorf("card after undo = %+v, want New", undone)
	}
	log, err := f.database.Queries().GetReviewLog(context.Background(), reviewID)
	if err != nil || !log.Undone {
		t.Errorf("review log after undo = %+v, %v; want retained and undone", log, err)
	}
	foreignUndo := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/undo", `{"review_id":"`+reviewID+`"}`, f.otherCookie)
	if foreignUndo.Code != http.StatusNotFound {
		t.Errorf("cross-owner undo = %d/%s, want 404", foreignUndo.Code, foreignUndo.Body)
	}
	if _, err := f.database.SQL.Exec("DELETE FROM review_logs WHERE id = ?", reviewID); err == nil {
		t.Error("append-only trigger allowed deleting an undone review")
	}
}

func mustStudyJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal study request: %v", err)
	}
	return string(raw)
}
