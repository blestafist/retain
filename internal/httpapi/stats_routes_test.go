package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/stats"
)

type statsRoutesClock struct{ now time.Time }

func (c statsRoutesClock) Now() time.Time { return c.now }

func TestRegisterStatsRoutesAuthenticationValidationAndOwnerScope(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "stats-routes.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	})

	now := time.Date(2026, time.April, 3, 12, 0, 0, 0, time.UTC)
	clock := statsRoutesClock{now: now}
	accounts := auth.NewService(database, clock)
	ownerID, err := accounts.CreateAdmin(ctx, "stats-routes@example.test", "Stats routes", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	login, err := accounts.Login(ctx, auth.LoginInput{Email: "stats-routes@example.test", Password: "correct horse battery staple"}, "stats-routes-test")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	other, err := database.Queries().CreateUser(ctx, gen.CreateUserParams{
		Email: "stats-routes-other@example.test", Name: "Other", PasswordHash: "unused", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser(other) error = %v", err)
	}
	if _, err := database.Queries().CreateUserSettings(ctx, other.ID); err != nil {
		t.Fatalf("CreateUserSettings(other) error = %v", err)
	}
	ownerCard := createStatsRouteCard(t, database, ownerID, now)
	foreignCard := createStatsRouteCard(t, database, other.ID, now)

	mux := http.NewServeMux()
	RegisterStatsRoutes(mux, accounts, stats.NewService(database, clock))
	request := func(path string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if authenticated {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: login.Token})
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, r)
		return response
	}

	if got := request("/api/v1/stats/overview", false); got.Code != http.StatusUnauthorized {
		t.Errorf("anonymous overview status/body = %d/%s, want 401", got.Code, got.Body)
	}
	if got := request("/api/v1/stats/forecast?days=0", true); got.Code != http.StatusBadRequest {
		t.Errorf("invalid forecast status/body = %d/%s, want 400", got.Code, got.Body)
	}
	if got := request("/api/v1/stats/forecast?days=2&days=3", true); got.Code != http.StatusBadRequest {
		t.Errorf("duplicate forecast query status/body = %d/%s, want 400", got.Code, got.Body)
	}
	if got := request("/api/v1/stats/retention?period=quarter", true); got.Code != http.StatusBadRequest {
		t.Errorf("invalid retention period status/body = %d/%s, want 400", got.Code, got.Body)
	}
	if got := request("/api/v1/stats/heatmap?year=2099", true); got.Code != http.StatusBadRequest {
		t.Errorf("out-of-range heatmap year status/body = %d/%s, want 400", got.Code, got.Body)
	}
	if got := request("/api/v1/stats/overview?secret=1", true); got.Code != http.StatusBadRequest {
		t.Errorf("unknown overview query status/body = %d/%s, want 400", got.Code, got.Body)
	}
	for _, route := range []string{
		"/api/v1/stats/overview",
		"/api/v1/stats/forecast?days=2",
		"/api/v1/stats/retention?period=week",
		"/api/v1/stats/heatmap?year=2026",
		"/api/v1/stats/cards/" + jsonNumber(ownerCard) + "/curve",
		"/api/v1/stats/distribution",
	} {
		if got := request(route, true); got.Code != http.StatusOK {
			t.Errorf("GET %s status/body = %d/%s, want 200", route, got.Code, got.Body)
		}
	}
	for _, cardID := range []int64{foreignCard, ownerCard + 1000} {
		got := request("/api/v1/stats/cards/"+jsonNumber(cardID)+"/curve", true)
		if got.Code != http.StatusNotFound {
			t.Errorf("foreign or missing curve status/body = %d/%s, want indistinguishable 404", got.Code, got.Body)
		}
	}
	var overview stats.Overview
	if got := request("/api/v1/stats/overview", true); json.Unmarshal(got.Body.Bytes(), &overview) != nil || overview.Timezone != "UTC" {
		t.Errorf("overview response = %+v, want valid owner response in UTC", overview)
	}
}

func createStatsRouteCard(t *testing.T, database *db.DB, userID int64, now time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	deckResult, err := database.SQL.ExecContext(ctx, `
		INSERT INTO decks (owner_id, name, lang_target, lang_native, created_at, updated_at)
		VALUES (?, 'Stats route deck', 'de', 'pl', ?, ?)
	`, userID, now, now)
	if err != nil {
		t.Fatalf("create route deck: %v", err)
	}
	deckID, err := deckResult.LastInsertId()
	if err != nil {
		t.Fatalf("read route deck ID: %v", err)
	}
	noteResult, err := database.SQL.ExecContext(ctx, `
		INSERT INTO notes (deck_id, term, term_key, translation, created_at, updated_at)
		VALUES (?, 'Haus', 'haus', 'dom', ?, ?)
	`, deckID, now, now)
	if err != nil {
		t.Fatalf("create route note: %v", err)
	}
	noteID, err := noteResult.LastInsertId()
	if err != nil {
		t.Fatalf("read route note ID: %v", err)
	}
	cardResult, err := database.SQL.ExecContext(ctx, `
		INSERT INTO cards (note_id, user_id, direction, state, due, updated_at)
		VALUES (?, ?, 'forward', 0, ?, ?)
	`, noteID, userID, now, now)
	if err != nil {
		t.Fatalf("create route card: %v", err)
	}
	cardID, err := cardResult.LastInsertId()
	if err != nil {
		t.Fatalf("read route card ID: %v", err)
	}
	return cardID
}

func jsonNumber(value int64) string {
	return strconv.FormatInt(value, 10)
}
