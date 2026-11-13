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

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/decks"
	"github.com/pestit/retain/internal/notes"
	"github.com/pestit/retain/internal/social"
)

func TestSocialRoutesHideAccountExistenceAndEnforceSubscriptionReadOnly(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "social-routes.db"))
	if err != nil {
		t.Fatalf("db.Open(): %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	authService := auth.NewService(database, clock.Real{})
	adminID, err := authService.CreateAdmin(ctx, "social-admin@example.test", "Social owner", "correct horse battery staple")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	adminLogin, err := authService.Login(ctx, auth.LoginInput{Email: "social-admin@example.test", Password: "correct horse battery staple"}, "social-test")
	if err != nil {
		t.Fatalf("login admin: %v", err)
	}
	invite, err := authService.CreateInvite(ctx, adminID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	friend, err := authService.Register(ctx, auth.RegisterInput{
		Invite: invite.Code, Email: "social-friend@example.test", Name: "Social friend",
		Password: "another correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("register friend: %v", err)
	}
	friendLogin, err := authService.Login(ctx, auth.LoginInput{Email: friend.Email, Password: "another correct horse battery staple"}, "social-test")
	if err != nil {
		t.Fatalf("login friend: %v", err)
	}
	ownerCookie := &http.Cookie{Name: sessionCookieName, Value: adminLogin.Token}
	friendCookie := &http.Cookie{Name: sessionCookieName, Value: friendLogin.Token}
	mux := http.NewServeMux()
	service := social.NewService(database, clock.Real{})
	RegisterSocialRoutes(mux, authService, service)
	RegisterContentRoutes(mux, authService, decks.NewService(database, clock.Real{}), notes.NewService(database, clock.Real{}))

	missing := socialRouteRequest(mux, http.MethodPost, "/api/v1/friends/requests", `{"email":"nobody@example.test"}`, ownerCookie)
	existing := socialRouteRequest(mux, http.MethodPost, "/api/v1/friends/requests", `{"email":"social-friend@example.test"}`, ownerCookie)
	if missing.Code != http.StatusAccepted || existing.Code != missing.Code || missing.Body.String() != existing.Body.String() {
		t.Fatalf("friend request responses reveal account existence: missing=%d %q existing=%d %q", missing.Code, missing.Body, existing.Code, existing.Body)
	}
	requests := socialRouteRequest(mux, http.MethodGet, "/api/v1/friends/requests", "", friendCookie)
	if requests.Code != http.StatusOK {
		t.Fatalf("GET incoming requests status=%d body=%s", requests.Code, requests.Body)
	}
	var requestList []social.FriendRequest
	if err := json.Unmarshal(requests.Body.Bytes(), &requestList); err != nil || len(requestList) != 1 {
		t.Fatalf("incoming requests=%s, err=%v", requests.Body, err)
	}
	accept := socialRouteRequest(mux, http.MethodPost, "/api/v1/friends/requests/"+strconv.FormatInt(requestList[0].ID, 10)+"/accept", "", friendCookie)
	if accept.Code != http.StatusNoContent {
		t.Fatalf("accept request status=%d body=%s", accept.Code, accept.Body)
	}

	deck, err := decks.NewService(database, clock.Real{}).Create(ctx, adminID, decks.CreateInput{Name: "Shared deck"})
	if err != nil {
		t.Fatalf("create source deck: %v", err)
	}
	note, err := notes.NewService(database, clock.Real{}).Create(ctx, adminID, deck.ID, notes.CreateInput{Term: "Wort", Translation: "word"})
	if err != nil {
		t.Fatalf("create source note: %v", err)
	}
	shareResponse := socialRouteRequest(mux, http.MethodPost,
		"/api/v1/decks/"+strconv.FormatInt(deck.ID, 10)+"/shares",
		`{"recipient_id":`+strconv.FormatInt(friend.ID, 10)+`,"mode":"subscribe"}`, ownerCookie)
	if shareResponse.Code != http.StatusCreated {
		t.Fatalf("share deck status=%d body=%s", shareResponse.Code, shareResponse.Body)
	}
	var share social.DeckShare
	if err := json.Unmarshal(shareResponse.Body.Bytes(), &share); err != nil || share.ID == 0 {
		t.Fatalf("share response=%s err=%v", shareResponse.Body, err)
	}
	decksResponse := socialRouteRequest(mux, http.MethodGet, "/api/v1/decks", "", friendCookie)
	if decksResponse.Code != http.StatusOK || !strings.Contains(decksResponse.Body.String(), `"can_edit":false`) {
		t.Fatalf("subscriber deck list should be readable and read-only: status=%d body=%s", decksResponse.Code, decksResponse.Body)
	}
	wordResponse := socialRouteRequest(mux, http.MethodGet, "/api/v1/decks/"+strconv.FormatInt(deck.ID, 10)+"/notes", "", friendCookie)
	if wordResponse.Code != http.StatusOK || !strings.Contains(wordResponse.Body.String(), `"id":`+strconv.FormatInt(note.ID, 10)) {
		t.Fatalf("subscriber cannot read source words: status=%d body=%s", wordResponse.Code, wordResponse.Body)
	}
	patchResponse := socialRouteRequest(mux, http.MethodPatch, "/api/v1/notes/"+strconv.FormatInt(note.ID, 10), `{"translation":"modified"}`, friendCookie)
	if patchResponse.Code != http.StatusNotFound {
		t.Fatalf("subscriber could update source note: status=%d body=%s", patchResponse.Code, patchResponse.Body)
	}
	sharesResponse := socialRouteRequest(mux, http.MethodGet, "/api/v1/friends/shares", "", friendCookie)
	if sharesResponse.Code != http.StatusOK || !strings.Contains(sharesResponse.Body.String(), `"role":"recipient"`) {
		t.Fatalf("recipient's share list status=%d body=%s", sharesResponse.Code, sharesResponse.Body)
	}
	revoke := socialRouteRequest(mux, http.MethodDelete,
		"/api/v1/decks/"+strconv.FormatInt(deck.ID, 10)+"/shares/"+strconv.FormatInt(share.ID, 10), "", ownerCookie)
	if revoke.Code != http.StatusNoContent {
		t.Fatalf("owner revoke status=%d body=%s", revoke.Code, revoke.Body)
	}
	denied := socialRouteRequest(mux, http.MethodGet, "/api/v1/decks/"+strconv.FormatInt(deck.ID, 10)+"/notes", "", friendCookie)
	if denied.Code != http.StatusNotFound {
		t.Fatalf("revoked subscriber can still read words: status=%d body=%s", denied.Code, denied.Body)
	}
	if unauthorized := socialRouteRequest(mux, http.MethodGet, "/api/v1/friends", "", nil); unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated friends request status=%d, want unauthorized", unauthorized.Code)
	}
}

func socialRouteRequest(handler http.Handler, method, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
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
