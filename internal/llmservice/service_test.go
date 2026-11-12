package llmservice

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/pestit/retain/internal/llmstore"
)

const validSuggestionJSON = `{"translation":"Katze","alternatives":["Kater"],"transcription":"ˈkaːtsə","example":"Die Katze schläft.","example_translation":"Kot śpi.","fields":{"pos":"noun","article":"die","plural":"Katzen","verb_forms":[],"cefr":"A1"}}`

type serviceTestClock struct{ now time.Time }

func (c serviceTestClock) Now() time.Time { return c.now }

type fakeGenerator struct {
	response json.RawMessage
	err      error
	calls    int
	request  llm.GenerateJSONRequest
}

func (f *fakeGenerator) GenerateJSON(ctx context.Context, request llm.GenerateJSONRequest) (llm.GenerateJSONResult, error) {
	f.calls++
	f.request = request
	if f.err != nil {
		return llm.GenerateJSONResult{}, f.err
	}
	if request.Validate != nil {
		if err := request.Validate(ctx, f.response); err != nil {
			return llm.GenerateJSONResult{}, &llm.UsageError{Err: err, PromptTokens: 17, CompletionTokens: 9}
		}
	}
	return llm.GenerateJSONResult{JSON: append(json.RawMessage(nil), f.response...), Model: "provider-model", PromptTokens: 17, CompletionTokens: 9}, nil
}

type serviceFixture struct {
	database *db.DB
	auth     *auth.Service
	decks    *decks.Service
	store    *llmstore.Store
	clock    serviceTestClock
	userID   int64
	deckID   int64
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "llmservice.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})
	now := time.Date(2026, time.April, 3, 12, 0, 0, 0, time.UTC)
	testClock := serviceTestClock{now: now}
	queries := database.Queries()
	user, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email: "llmservice@example.test", Name: "LLM service", PasswordHash: "unused", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateUserSettings(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, testClock)
	if _, err := authService.UpdateSettings(ctx, user.ID, map[string]json.RawMessage{
		"timezone": []byte(`"Europe/Warsaw"`), "day_start_hour": []byte(`4`),
	}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}
	deckService := decks.NewService(database, testClock)
	target, native := "de", "pl"
	deck, err := deckService.Create(ctx, user.ID, decks.CreateInput{
		Name: "German", LangTarget: &target, LangNative: &native,
	})
	if err != nil {
		t.Fatalf("Create(deck) error = %v", err)
	}
	return &serviceFixture{
		database: database, auth: authService, decks: deckService,
		store: llmstore.New(queries), clock: testClock, userID: user.ID, deckID: deck.ID,
	}
}

func (f *serviceFixture) service(generator jsonGenerator, dailyLimit int64) *Service {
	return NewService(f.auth, f.decks, generator, f.store, f.clock, Config{Model: "server-model", DailyLimit: dailyLimit})
}

func TestEnrichUsesServerDeckLanguageCachesAndRecordsUsage(t *testing.T) {
	fixture := newServiceFixture(t)
	child, err := fixture.decks.Create(context.Background(), fixture.userID, decks.CreateInput{
		Name: "Inherited languages", ParentID: &fixture.deckID,
	})
	if err != nil {
		t.Fatalf("Create(child) error = %v", err)
	}
	generator := &fakeGenerator{response: json.RawMessage(validSuggestionJSON)}
	service := fixture.service(generator, 5)
	input := Request{DeckID: child.ID, Word: " Katze "}

	first, err := service.Enrich(context.Background(), fixture.userID, input)
	if err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}
	if first.Translation != "Katze" || first.Fields.Article != "die" || first.Alternatives == nil || first.Fields.VerbForms == nil {
		t.Fatalf("Enrich() = %+v, expected structured suggestion and non-nil arrays", first)
	}
	if generator.calls != 1 || generator.request.Model != "server-model" || generator.request.SchemaName != "word_enrichment_v1" {
		t.Fatalf("GenerateJSON calls=%d request=%+v", generator.calls, generator.request)
	}
	if !strings.Contains(generator.request.System, "German") || !strings.Contains(generator.request.System, "Polish") {
		t.Errorf("system prompt omitted inherited language pair: %q", generator.request.System)
	}
	var promptInput map[string]string
	if err := json.Unmarshal([]byte(generator.request.User), &promptInput); err != nil || promptInput["word"] != "Katze" {
		t.Fatalf("user prompt = %q, decoded=%v, err=%v", generator.request.User, promptInput, err)
	}
	if err := generator.request.Validate(context.Background(), json.RawMessage(validSuggestionJSON)); err != nil {
		t.Fatalf("Validate(valid response) error = %v", err)
	}

	second, err := service.Enrich(context.Background(), fixture.userID, input)
	if err != nil {
		t.Fatalf("cached Enrich() error = %v", err)
	}
	if generator.calls != 1 || second.Translation != first.Translation {
		t.Fatalf("cache result=%+v, generator calls=%d", second, generator.calls)
	}
	usage, err := fixture.store.ListUsage(context.Background(), fixture.userID, fixture.clock.now.Add(-24*time.Hour), fixture.clock.now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ListUsage() error = %v", err)
	}
	if len(usage) != 2 || usage[0].Cached || usage[0].PromptTokens != 17 || usage[0].CompletionTokens != 9 || !usage[1].Cached {
		t.Errorf("usage = %+v, want provider usage and cache-hit usage", usage)
	}
}

func TestEnrichResolvesModelOncePerRequestForProviderQuotaAndCache(t *testing.T) {
	fixture := newServiceFixture(t)
	generator := &fakeGenerator{response: json.RawMessage(validSuggestionJSON)}
	resolverCalls := 0
	service := NewService(fixture.auth, fixture.decks, generator, fixture.store, fixture.clock, Config{
		DailyLimit: 5,
		ModelResolver: func(context.Context) (string, error) {
			resolverCalls++
			return "selected-model", nil
		},
	})
	request := Request{DeckID: fixture.deckID, Word: "Katze"}
	for range 2 {
		if _, err := service.Enrich(context.Background(), fixture.userID, request); err != nil {
			t.Fatalf("Enrich() error = %v", err)
		}
	}
	if resolverCalls != 2 {
		t.Errorf("model resolver calls = %d, want once for each request", resolverCalls)
	}
	if generator.calls != 1 || generator.request.Model != "selected-model" {
		t.Errorf("provider calls/model = %d/%q, want one call using resolved model", generator.calls, generator.request.Model)
	}
	usage, err := fixture.store.ListUsage(context.Background(), fixture.userID, fixture.clock.now.Add(-time.Hour), fixture.clock.now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ListUsage() error = %v", err)
	}
	if len(usage) != 2 || usage[0].Model != "selected-model" || usage[1].Model != "selected-model" || !usage[1].Cached {
		t.Errorf("usage rows = %+v, want provider and cache-hit rows on resolved model", usage)
	}
}

func TestEnrichFailsClosedWhenModelResolverFails(t *testing.T) {
	fixture := newServiceFixture(t)
	generator := &fakeGenerator{response: json.RawMessage(validSuggestionJSON)}
	service := NewService(fixture.auth, fixture.decks, generator, fixture.store, fixture.clock, Config{
		DailyLimit:    5,
		ModelResolver: func(context.Context) (string, error) { return "", errors.New("private setting detail") },
	})
	if _, err := service.Enrich(context.Background(), fixture.userID, Request{DeckID: fixture.deckID, Word: "Katze"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Enrich() error = %v, want ErrUnavailable", err)
	}
	usage, err := fixture.store.ListUsage(context.Background(), fixture.userID, fixture.clock.now.Add(-time.Hour), fixture.clock.now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ListUsage() error = %v", err)
	}
	if generator.calls != 0 || len(usage) != 0 {
		t.Fatal("failed model resolution started provider work or recorded usage")
	}
}

func TestEnrichForBulkUsesOverrideAndDeckEnrichUsageWithSharedCache(t *testing.T) {
	fixture := newServiceFixture(t)
	generator := &fakeGenerator{response: json.RawMessage(validSuggestionJSON)}
	resolverCalls := 0
	service := NewService(fixture.auth, fixture.decks, generator, fixture.store, fixture.clock, Config{
		DailyLimit: 5,
		ModelResolver: func(context.Context) (string, error) {
			resolverCalls++
			return "selected-model", nil
		},
	})
	ctx := context.Background()
	request := Request{DeckID: fixture.deckID, Word: "Katze"}
	if _, err := service.Enrich(ctx, fixture.userID, request); err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}
	for range 2 {
		if _, err := service.EnrichForBulk(ctx, fixture.userID, request, "fast-model"); err != nil {
			t.Fatalf("EnrichForBulk(override) error = %v", err)
		}
	}
	// No override means bulk work falls back to the currently selected model.
	if _, err := service.EnrichForBulk(ctx, fixture.userID, request, ""); err != nil {
		t.Fatalf("EnrichForBulk(fallback) error = %v", err)
	}
	if resolverCalls != 2 {
		t.Errorf("model resolver calls = %d, want selected-model lookup for Enrich and bulk fallback only", resolverCalls)
	}
	if generator.calls != 2 || generator.request.Model != "fast-model" {
		t.Errorf("provider calls/model = %d/%q, want selected model and fast override calls", generator.calls, generator.request.Model)
	}
	usage, err := fixture.store.ListUsage(ctx, fixture.userID, fixture.clock.now.Add(-time.Hour), fixture.clock.now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ListUsage() error = %v", err)
	}
	want := []struct {
		kind   string
		model  string
		cached bool
	}{
		{kind: usageKind, model: "selected-model"},
		{kind: bulkUsageKind, model: "fast-model"},
		{kind: bulkUsageKind, model: "fast-model", cached: true},
		{kind: bulkUsageKind, model: "selected-model", cached: true},
	}
	if len(usage) != len(want) {
		t.Fatalf("usage rows = %+v, want %d rows", usage, len(want))
	}
	for index, expected := range want {
		if usage[index].Kind != expected.kind || usage[index].Model != expected.model || usage[index].Cached != expected.cached {
			t.Errorf("usage[%d] = %+v, want kind/model/cached %q/%q/%v", index, usage[index], expected.kind, expected.model, expected.cached)
		}
	}
}

func TestEnrichRejectsUnsupportedAndForeignDecks(t *testing.T) {
	fixture := newServiceFixture(t)
	generator := &fakeGenerator{response: json.RawMessage(validSuggestionJSON)}
	service := fixture.service(generator, 5)

	if _, err := service.Enrich(context.Background(), fixture.userID, Request{DeckID: fixture.deckID + 999, Word: "word"}); !errors.Is(err, ErrDeckNotFound) {
		t.Errorf("Enrich(foreign deck) error = %v, want ErrDeckNotFound", err)
	}
	other, err := fixture.database.Queries().CreateUser(context.Background(), gen.CreateUserParams{
		Email: "other-llmservice@example.test", Name: "Other", PasswordHash: "unused", CreatedAt: fixture.clock.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Queries().CreateUserSettings(context.Background(), other.ID); err != nil {
		t.Fatal(err)
	}
	target, native := "de", "pl"
	foreignDeck, err := fixture.decks.Create(context.Background(), other.ID, decks.CreateInput{
		Name: "Other's deck", LangTarget: &target, LangNative: &native,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Enrich(context.Background(), fixture.userID, Request{DeckID: foreignDeck.ID, Word: "word"}); !errors.Is(err, ErrDeckNotFound) {
		t.Errorf("Enrich(other user's deck) error = %v, want ErrDeckNotFound", err)
	}
	unsupportedDeck, err := fixture.decks.Create(context.Background(), fixture.userID, decks.CreateInput{Name: "No language pair"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Enrich(context.Background(), fixture.userID, Request{DeckID: unsupportedDeck.ID, Word: "word"}); !errors.Is(err, ErrUnsupportedPair) {
		t.Errorf("Enrich(unsupported pair) error = %v, want ErrUnsupportedPair", err)
	}
	if _, err := service.Enrich(context.Background(), fixture.userID, Request{DeckID: fixture.deckID, Word: strings.Repeat("a", maxWordRunes+1)}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Enrich(oversized word) error = %v, want ErrInvalidInput", err)
	}
	if generator.calls != 0 {
		t.Errorf("provider calls = %d, want none for invalid deck or word", generator.calls)
	}
}

func TestEnrichLimitAndInvalidResponseDoNotCache(t *testing.T) {
	t.Run("daily limit", func(t *testing.T) {
		fixture := newServiceFixture(t)
		generator := &fakeGenerator{response: json.RawMessage(validSuggestionJSON)}
		if _, err := fixture.service(generator, 0).Enrich(context.Background(), fixture.userID, Request{DeckID: fixture.deckID, Word: "Katze"}); !errors.Is(err, ErrDailyLimit) {
			t.Fatalf("Enrich() error = %v, want ErrDailyLimit", err)
		}
		if generator.calls != 0 {
			t.Errorf("provider calls = %d, want none after rejected reservation", generator.calls)
		}
	})

	t.Run("invalid response", func(t *testing.T) {
		for _, test := range []struct {
			name string
			json string
		}{
			{name: "blank required translation", json: strings.Replace(validSuggestionJSON, `"translation":"Katze"`, `"translation":" "`, 1)},
			{name: "null optional string", json: strings.Replace(validSuggestionJSON, `"transcription":"ˈkaːtsə"`, `"transcription":null`, 1)},
			{name: "null language field", json: strings.Replace(validSuggestionJSON, `"cefr":"A1"`, `"cefr":null`, 1)},
		} {
			t.Run(test.name, func(t *testing.T) {
				fixture := newServiceFixture(t)
				generator := &fakeGenerator{response: json.RawMessage(test.json)}
				service := fixture.service(generator, 5)
				if _, err := service.Enrich(context.Background(), fixture.userID, Request{DeckID: fixture.deckID, Word: "Katze"}); !errors.Is(err, ErrUnavailable) {
					t.Fatalf("Enrich() error = %v, want ErrUnavailable", err)
				}
				key := makeCacheKey("server-model", PromptVersion, "de/pl", "Katze")
				if _, found, err := fixture.store.GetCache(context.Background(), key); err != nil || found {
					t.Errorf("GetCache() found=%v err=%v, want invalid response not cached", found, err)
				}
			})
		}
	})
}

func TestUsageDayWindowUsesLocalDSTBoundaries(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want time.Duration
	}{
		{
			name: "spring forward day is twenty-three hours",
			now:  time.Date(2026, time.March, 29, 1, 30, 0, 0, time.UTC),
			want: 23 * time.Hour,
		},
		{
			name: "fall back day is twenty-five hours",
			now:  time.Date(2026, time.October, 25, 1, 30, 0, 0, time.UTC),
			want: 25 * time.Hour,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start, end, err := usageDayWindow(test.now, "Europe/Warsaw", 4)
			if err != nil {
				t.Fatalf("usageDayWindow() error = %v", err)
			}
			if got := end.Sub(start); got != test.want {
				t.Errorf("day window duration = %s, want %s (%s - %s)", got, test.want, start, end)
			}
		})
	}
}

func TestResponseValidationRequiresContractShape(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{name: "unknown field", json: strings.Replace(validSuggestionJSON, `"translation":"Katze"`, `"extra":"leak","translation":"Katze"`, 1)},
		{name: "missing fields", json: `{"translation":"cat"}`},
		{name: "article for English", json: validSuggestionJSON},
		{name: "empty optional fields", json: `{"translation":"cat","alternatives":[],"transcription":"","example":"","example_translation":"","fields":{"pos":"","article":"","plural":"","verb_forms":[],"cefr":""}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := "de"
			if test.name == "article for English" {
				target = "en"
			}
			_, ok := decodeResponse(json.RawMessage(test.json), target)
			if test.name == "empty optional fields" {
				if !ok {
					t.Fatal("decodeResponse() rejected valid empty optional values")
				}
				return
			}
			if ok {
				t.Fatal("decodeResponse() accepted invalid response")
			}
		})
	}
}

func TestResponseValidationRejectsNullAndWrongJSONTypes(t *testing.T) {
	tests := []struct {
		name        string
		original    string
		replacement string
	}{
		{name: "translation null", original: `"translation":"Katze"`, replacement: `"translation":null`},
		{name: "translation non-string", original: `"translation":"Katze"`, replacement: `"translation":42`},
		{name: "alternatives null", original: `"alternatives":["Kater"]`, replacement: `"alternatives":null`},
		{name: "alternatives non-array", original: `"alternatives":["Kater"]`, replacement: `"alternatives":"Kater"`},
		{name: "alternative non-string", original: `"alternatives":["Kater"]`, replacement: `"alternatives":[null]`},
		{name: "transcription null", original: `"transcription":"ˈkaːtsə"`, replacement: `"transcription":null`},
		{name: "transcription non-string", original: `"transcription":"ˈkaːtsə"`, replacement: `"transcription":false`},
		{name: "example null", original: `"example":"Die Katze schläft."`, replacement: `"example":null`},
		{name: "example non-string", original: `"example":"Die Katze schläft."`, replacement: `"example":[]`},
		{name: "example translation null", original: `"example_translation":"Kot śpi."`, replacement: `"example_translation":null`},
		{name: "example translation non-string", original: `"example_translation":"Kot śpi."`, replacement: `"example_translation":{}`},
		{name: "fields null", original: `"fields":{"pos":"noun"`, replacement: `"fields":null`},
		{name: "fields non-object", original: `"fields":{"pos":"noun"`, replacement: `"fields":[]`},
		{name: "pos null", original: `"pos":"noun"`, replacement: `"pos":null`},
		{name: "pos non-string", original: `"pos":"noun"`, replacement: `"pos":1`},
		{name: "article null", original: `"article":"die"`, replacement: `"article":null`},
		{name: "article non-string", original: `"article":"die"`, replacement: `"article":true`},
		{name: "plural null", original: `"plural":"Katzen"`, replacement: `"plural":null`},
		{name: "plural non-string", original: `"plural":"Katzen"`, replacement: `"plural":[]`},
		{name: "verb forms null", original: `"verb_forms":[]`, replacement: `"verb_forms":null`},
		{name: "verb forms non-array", original: `"verb_forms":[]`, replacement: `"verb_forms":"gehen"`},
		{name: "verb form non-string", original: `"verb_forms":[]`, replacement: `"verb_forms":[null]`},
		{name: "cefr null", original: `"cefr":"A1"`, replacement: `"cefr":null`},
		{name: "cefr non-string", original: `"cefr":"A1"`, replacement: `"cefr":false`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := strings.Replace(validSuggestionJSON, test.original, test.replacement, 1)
			if invalid == validSuggestionJSON {
				t.Fatal("test input replacement did not match valid response")
			}
			if _, ok := decodeResponse(json.RawMessage(invalid), "de"); ok {
				t.Fatalf("decodeResponse() accepted %s", invalid)
			}
		})
	}
}

func TestContextAwareValidatorChecksCancellation(t *testing.T) {
	fixture := newServiceFixture(t)
	generator := &fakeGenerator{response: json.RawMessage(validSuggestionJSON)}
	service := fixture.service(generator, 5)
	if _, err := service.Enrich(context.Background(), fixture.userID, Request{DeckID: fixture.deckID, Word: "Katze"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := generator.request.Validate(ctx, json.RawMessage(validSuggestionJSON)); !errors.Is(err, context.Canceled) {
		t.Errorf("Validate(canceled context) error = %v, want context.Canceled", err)
	}
}

var _ clock.Clock = serviceTestClock{}
