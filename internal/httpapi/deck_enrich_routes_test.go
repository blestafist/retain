package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pestit/retain/internal/llmservice"
)

type deckEnrichRouteFake struct {
	jobID  string
	err    error
	calls  int
	userID int64
	deckID int64
}

func (f *deckEnrichRouteFake) Submit(_ context.Context, userID, deckID int64) (string, error) {
	f.calls++
	f.userID, f.deckID = userID, deckID
	return f.jobID, f.err
}

func TestRegisterDeckEnrichRoutesAuthenticatesAndReturnsJobID(t *testing.T) {
	fixture := newLLMAskRouteFixture(t)
	service := &deckEnrichRouteFake{jobID: "job-123"}
	mux := http.NewServeMux()
	RegisterDeckEnrichRoutes(mux, fixture.auth, service)

	unauthenticated := httptest.NewRecorder()
	mux.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "/api/v1/decks/23/enrich", nil))
	if unauthenticated.Code != http.StatusUnauthorized || service.calls != 0 {
		t.Fatalf("unauthenticated response = %d %s, calls=%d", unauthenticated.Code, unauthenticated.Body, service.calls)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/decks/23/enrich", strings.NewReader("not-json"))
	request.AddCookie(fixture.cookie)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || strings.TrimSpace(response.Body.String()) != `{"job_id":"job-123"}` {
		t.Fatalf("POST enrich response = %d %s, want 202 with job_id", response.Code, response.Body)
	}
	if service.calls != 1 || service.userID != fixture.userID || service.deckID != 23 {
		t.Errorf("Submit() calls=%d user=%d deck=%d, want authenticated owner and path deck ID", service.calls, service.userID, service.deckID)
	}

	invalidPath := httptest.NewRecorder()
	badRequest := httptest.NewRequest(http.MethodPost, "/api/v1/decks/not-an-id/enrich", nil)
	badRequest.AddCookie(fixture.cookie)
	mux.ServeHTTP(invalidPath, badRequest)
	if invalidPath.Code != http.StatusNotFound || service.calls != 1 {
		t.Errorf("invalid path response=%d calls=%d, want 404 without submit", invalidPath.Code, service.calls)
	}
}

func TestRegisterDeckEnrichRoutesMapsErrorsWithoutLeakingDetails(t *testing.T) {
	fixture := newLLMAskRouteFixture(t)
	tests := []struct {
		name string
		err  error
		code int
		want string
	}{
		{name: "not found", err: llmservice.ErrDeckNotFound, code: http.StatusNotFound, want: `"code":"not_found"`},
		{name: "candidate limit", err: llmservice.ErrDeckEnrichCandidateLimit, code: http.StatusUnprocessableEntity, want: `"code":"too_many_candidates"`},
		{name: "job capacity", err: llmservice.ErrDeckEnrichQueueFull, code: http.StatusTooManyRequests, want: `"code":"job_capacity_reached"`},
		{name: "internal error is hidden", err: errors.New("private provider key"), code: http.StatusInternalServerError, want: `"code":"internal_error"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &deckEnrichRouteFake{err: test.err}
			mux := http.NewServeMux()
			RegisterDeckEnrichRoutes(mux, fixture.auth, service)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/decks/23/enrich", nil)
			request.AddCookie(fixture.cookie)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.code || !strings.Contains(response.Body.String(), test.want) {
				t.Fatalf("response = %d %s, want status %d and %s", response.Code, response.Body, test.code, test.want)
			}
			if strings.Contains(response.Body.String(), "private provider key") {
				t.Errorf("response leaked private error: %s", response.Body)
			}
		})
	}
}
