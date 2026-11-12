package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/decks"
	"github.com/pestit/retain/internal/llm"
	"github.com/pestit/retain/internal/llmservice"
	"github.com/pestit/retain/internal/llmstore"
)

const llmRouteSuggestion = `{"translation":"Katze","alternatives":[],"transcription":"","example":"Die Katze schläft.","example_translation":"Kot śpi.","fields":{"pos":"noun","article":"die","plural":"Katzen","verb_forms":[],"cefr":"A1"}}`

type llmRouteClock struct{ now time.Time }

func (c llmRouteClock) Now() time.Time { return c.now }

type llmRouteGenerator struct {
	response json.RawMessage
	err      error
}

func (g llmRouteGenerator) GenerateJSON(ctx context.Context, request llm.GenerateJSONRequest) (llm.GenerateJSONResult, error) {
	if g.err != nil {
		return llm.GenerateJSONResult{}, g.err
	}
	if request.Validate != nil {
		if err := request.Validate(ctx, g.response); err != nil {
			return llm.GenerateJSONResult{}, err
		}
	}
	return llm.GenerateJSONResult{JSON: append(json.RawMessage(nil), g.response...), PromptTokens: 3, CompletionTokens: 7}, nil
}

type llmRoutesFixture struct {
	handler  http.Handler
	disabled http.Handler
	failure  http.Handler
	cookie   *http.Cookie
	deckID   int64
}

func newLLMRoutesFixture(t *testing.T) *llmRoutesFixture {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "llm-routes.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})
	now := time.Date(2026, time.April, 3, 12, 0, 0, 0, time.UTC)
	testClock := llmRouteClock{now: now}
	queries := database.Queries()
	user, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email: "llm-routes@example.test", Name: "LLM routes", PasswordHash: "unused", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateUserSettings(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.UpdateUserSettings(ctx, gen.UpdateUserSettingsParams{
		NewPerDay: 20, MaxReviewsPerDay: 200, RequestRetention: 0.9, MaximumInterval: 36500,
		LearningSteps: "1m 10m", RelearningSteps: "10m", Timezone: "Europe/Warsaw", DayStartHour: 4,
		TtsAuto: false, ThemeMode: "system", ThemeSeed: "#3A4527", ThemeVariant: "tonal_spot", ThemeContrast: 0,
		UserID: user.ID,
	}); err != nil {
		t.Fatalf("UpdateUserSettings() error = %v", err)
	}
	authService := auth.NewService(database, testClock)
	token, err := auth.GenerateSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	digest := auth.HashSessionToken(token)
	if _, err := queries.CreateSession(ctx, gen.CreateSessionParams{
		TokenHash: digest[:], UserID: user.ID, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	deckService := decks.NewService(database, testClock)
	target, native := "de", "pl"
	deck, err := deckService.Create(ctx, user.ID, decks.CreateInput{Name: "German", LangTarget: &target, LangNative: &native})
	if err != nil {
		t.Fatal(err)
	}
	service := llmservice.NewService(authService, deckService, llmRouteGenerator{response: json.RawMessage(llmRouteSuggestion)}, llmstore.New(queries), testClock, llmservice.Config{
		Model: "route-test-model", DailyLimit: 10,
	})
	mux := http.NewServeMux()
	RegisterLLMRoutes(mux, authService, service)
	disabledMux := http.NewServeMux()
	RegisterLLMRoutes(disabledMux, authService, llmservice.NewService(authService, deckService, nil, llmstore.New(queries), testClock, llmservice.Config{Model: "", DailyLimit: 10}))
	failureMux := http.NewServeMux()
	failureService := llmservice.NewService(authService, deckService, llmRouteGenerator{err: errors.New("upstream body contains secret-token and prompt text")}, llmstore.New(queries), testClock, llmservice.Config{
		Model: "route-test-model", DailyLimit: 10,
	})
	RegisterLLMRoutes(failureMux, authService, failureService)
	return &llmRoutesFixture{
		handler: mux, disabled: disabledMux, failure: failureMux,
		cookie: &http.Cookie{Name: sessionCookieName, Value: token}, deckID: deck.ID,
	}
}

func llmRouteRequest(handler http.Handler, method, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
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

func TestRegisterLLMRoutesStatusAndEnrichContract(t *testing.T) {
	fixture := newLLMRoutesFixture(t)
	status := llmRouteRequest(fixture.handler, http.MethodGet, "/api/v1/llm/status", "", nil)
	if status.Code != http.StatusOK || strings.TrimSpace(status.Body.String()) != `{"enabled":true}` {
		t.Fatalf("GET status = %d %s", status.Code, status.Body)
	}

	unauthenticated := llmRouteRequest(fixture.handler, http.MethodPost, "/api/v1/llm/enrich", `{"deck_id":1,"word":"Katze"}`, nil)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST status = %d, want 401 (%s)", unauthenticated.Code, unauthenticated.Body)
	}
	unknownField := llmRouteRequest(fixture.handler, http.MethodPost, "/api/v1/llm/enrich", `{"deck_id":1,"word":"Katze","model":"client-choice"}`, fixture.cookie)
	if unknownField.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d, want 400 (%s)", unknownField.Code, unknownField.Body)
	}

	response := llmRouteRequest(fixture.handler, http.MethodPost, "/api/v1/llm/enrich", `{"deck_id":`+jsonInt(fixture.deckID)+`,"word":"Katze"}`, fixture.cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("POST enrich = %d %s", response.Code, response.Body)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("response keys = %v, want exactly the six enrichment fields", got)
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(got["fields"], &nested); err != nil || len(nested) != 5 {
		t.Fatalf("fields = %s, decode error = %v, want five stable fields", got["fields"], err)
	}
	for _, key := range []string{"pos", "article", "plural", "verb_forms", "cefr"} {
		if _, exists := nested[key]; !exists {
			t.Errorf("fields missing %q", key)
		}
	}
	for _, key := range []string{"translation", "alternatives", "transcription", "example", "example_translation", "fields"} {
		if _, exists := got[key]; !exists {
			t.Errorf("response missing %q", key)
		}
	}
}

func TestDisabledLLMRoutesAreSafeAndSanitized(t *testing.T) {
	fixture := newLLMRoutesFixture(t)
	status := llmRouteRequest(fixture.disabled, http.MethodGet, "/api/v1/llm/status", "", nil)
	if status.Code != http.StatusOK || strings.TrimSpace(status.Body.String()) != `{"enabled":false}` {
		t.Fatalf("disabled GET status = %d %s", status.Code, status.Body)
	}
	response := llmRouteRequest(fixture.disabled, http.MethodPost, "/api/v1/llm/enrich", `{"deck_id":1,"word":"Katze"}`, nil)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"feature_disabled"`) {
		t.Fatalf("disabled POST = %d %s, want safe feature_disabled envelope", response.Code, response.Body)
	}
}

func TestProviderFailureDoesNotLeakProviderDetails(t *testing.T) {
	fixture := newLLMRoutesFixture(t)
	response := llmRouteRequest(fixture.failure, http.MethodPost, "/api/v1/llm/enrich", `{"deck_id":`+jsonInt(fixture.deckID)+`,"word":"Katze"}`, fixture.cookie)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST provider failure = %d %s, want 503", response.Code, response.Body)
	}
	for _, secret := range []string{"secret-token", "upstream body", "prompt text"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Errorf("response leaked %q: %s", secret, response.Body)
		}
	}
}

// Compile-time assurance that route fixtures use the same clock contract as
// the service's injected clock.
var _ clock.Clock = llmRouteClock{}
