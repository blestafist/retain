package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/social"
)

// RegisterSocialRoutes registers authenticated friendship, sharing, and
// aggregate social analytics endpoints.
func RegisterSocialRoutes(mux *http.ServeMux, authentication *auth.Service, service *social.Service) {
	mux.HandleFunc("GET /api/v1/friends", listFriendsHandler(authentication, service))
	mux.HandleFunc("POST /api/v1/friends/requests", createFriendRequestHandler(authentication, service))
	mux.HandleFunc("GET /api/v1/friends/requests", listFriendRequestsHandler(authentication, service))
	mux.HandleFunc("POST /api/v1/friends/requests/{id}/accept", updateFriendRequestHandler(authentication, service, "accept"))
	mux.HandleFunc("POST /api/v1/friends/requests/{id}/decline", updateFriendRequestHandler(authentication, service, "decline"))
	mux.HandleFunc("POST /api/v1/friends/requests/{id}/cancel", updateFriendRequestHandler(authentication, service, "cancel"))
	mux.HandleFunc("DELETE /api/v1/friends/{user_id}", removeFriendHandler(authentication, service))
	mux.HandleFunc("GET /api/v1/friends/activity", socialActivityHandler(authentication, service))
	mux.HandleFunc("GET /api/v1/friends/leaderboard", socialLeaderboardHandler(authentication, service))
	mux.HandleFunc("GET /api/v1/friends/shares", listUserSharesHandler(authentication, service))
	mux.HandleFunc("GET /api/v1/social/settings", getSocialSettingsHandler(authentication, service))
	mux.HandleFunc("PATCH /api/v1/social/settings", updateSocialSettingsHandler(authentication, service))
	mux.HandleFunc("POST /api/v1/decks/{id}/shares", shareDeckHandler(authentication, service))
	mux.HandleFunc("GET /api/v1/decks/{id}/shares", listDeckSharesHandler(authentication, service))
	mux.HandleFunc("DELETE /api/v1/decks/{id}/shares/{share_id}", endDeckShareHandler(authentication, service))
}

func listFriendsHandler(authentication *auth.Service, service *social.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		if !validStatsQuery(w, r, nil) {
			return
		}
		result, err := service.Friends(r.Context(), user.ID)
		writeSocialResult(w, result, err)
	}
}

func createFriendRequestHandler(authentication *auth.Service, service *social.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		var input struct {
			Email string `json:"email"`
		}
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		if err := service.RequestFriend(r.Context(), user.ID, input.Email); err != nil {
			writeSocialError(w, err)
			return
		}
		// This response is intentionally identical whether the address maps to
		// an account, an existing friendship, or no account.
		_ = WriteJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
	}
}

func listFriendRequestsHandler(authentication *auth.Service, service *social.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		if !validStatsQuery(w, r, nil) {
			return
		}
		result, err := service.Requests(r.Context(), user.ID)
		writeSocialResult(w, result, err)
	}
}

func updateFriendRequestHandler(authentication *auth.Service, service *social.Service, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		id, ok := positivePathID(w, r, "id")
		if !ok {
			return
		}
		var err error
		switch action {
		case "accept":
			err = service.AcceptFriendRequest(r.Context(), user.ID, id)
		case "decline":
			err = service.DeclineFriendRequest(r.Context(), user.ID, id)
		case "cancel":
			err = service.CancelFriendRequest(r.Context(), user.ID, id)
		}
		if err != nil {
			writeSocialError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func removeFriendHandler(authentication *auth.Service, service *social.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		friendID, ok := positivePathID(w, r, "user_id")
		if !ok {
			return
		}
		if err := service.RemoveFriend(r.Context(), user.ID, friendID); err != nil {
			writeSocialError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func socialActivityHandler(authentication *auth.Service, service *social.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		if !validStatsQuery(w, r, nil) {
			return
		}
		result, err := service.Activity(r.Context(), user.ID)
		writeSocialResult(w, result, err)
	}
}

func socialLeaderboardHandler(authentication *auth.Service, service *social.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		values, ok := socialQuery(w, r, "period")
		if !ok {
			return
		}
		period := values.Get("period")
		if period == "" {
			period = "week"
		}
		result, err := service.Leaderboard(r.Context(), user.ID, period)
		writeSocialResult(w, result, err)
	}
}

func getSocialSettingsHandler(authentication *auth.Service, service *social.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		if !validStatsQuery(w, r, nil) {
			return
		}
		result, err := service.Settings(r.Context(), user.ID)
		writeSocialResult(w, result, err)
	}
}

func updateSocialSettingsHandler(authentication *auth.Service, service *social.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		var input struct {
			Visible *bool `json:"visible"`
		}
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		if input.Visible == nil {
			writeSocialError(w, social.ErrInvalidInput)
			return
		}
		result, err := service.UpdateSettings(r.Context(), user.ID, *input.Visible)
		writeSocialResult(w, result, err)
	}
}

func shareDeckHandler(authentication *auth.Service, service *social.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		deckID, ok := positivePathID(w, r, "id")
		if !ok {
			return
		}
		var input struct {
			RecipientID int64  `json:"recipient_id"`
			Mode        string `json:"mode"`
		}
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		result, err := service.ShareDeck(r.Context(), user.ID, deckID, input.RecipientID, input.Mode)
		if err != nil {
			writeSocialError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusCreated, result); err != nil {
			writeInternalError(w)
		}
	}
}

func listDeckSharesHandler(authentication *auth.Service, service *social.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		deckID, ok := positivePathID(w, r, "id")
		if !ok {
			return
		}
		if !validStatsQuery(w, r, nil) {
			return
		}
		result, err := service.SharesForDeck(r.Context(), user.ID, deckID)
		writeSocialResult(w, result, err)
	}
}

func listUserSharesHandler(authentication *auth.Service, service *social.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		if !validStatsQuery(w, r, nil) {
			return
		}
		result, err := service.SharesForUser(r.Context(), user.ID)
		writeSocialResult(w, result, err)
	}
}

func endDeckShareHandler(authentication *auth.Service, service *social.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		deckID, ok := positivePathID(w, r, "id")
		if !ok {
			return
		}
		shareID, ok := positivePathID(w, r, "share_id")
		if !ok {
			return
		}
		if !validStatsQuery(w, r, nil) {
			return
		}
		if err := service.EndSubscription(r.Context(), user.ID, deckID, shareID); err != nil {
			writeSocialError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func positivePathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		_ = WriteAPIError(w, http.StatusNotFound, "not_found", "Resource not found.")
		return 0, false
	}
	return id, true
}

func socialQuery(w http.ResponseWriter, r *http.Request, allowed ...string) (url.Values, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeSocialInputError(w)
		return nil, false
	}
	allowedKeys := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedKeys[key] = struct{}{}
	}
	for key, items := range values {
		if _, ok := allowedKeys[key]; !ok || len(items) != 1 {
			writeSocialInputError(w)
			return nil, false
		}
	}
	return values, true
}

func writeSocialResult(w http.ResponseWriter, result any, err error) {
	if err != nil {
		writeSocialError(w, err)
		return
	}
	if err := WriteJSON(w, http.StatusOK, result); err != nil {
		writeInternalError(w)
	}
}

func writeSocialError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, social.ErrInvalidInput):
		writeSocialInputError(w)
	case errors.Is(err, social.ErrNotFound):
		_ = WriteAPIError(w, http.StatusNotFound, "not_found", "Resource not found.")
	case errors.Is(err, social.ErrConflict):
		_ = WriteAPIError(w, http.StatusConflict, "conflict", "This sharing request conflicts with an existing share.")
	default:
		writeInternalError(w)
	}
}

func writeSocialInputError(w http.ResponseWriter) {
	_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The social request contains invalid input.")
}
