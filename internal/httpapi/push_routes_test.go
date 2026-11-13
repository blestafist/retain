package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/push"
)

func TestPushRoutesManageAuthenticatedBrowserSubscriptions(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "push-routes.db"))
	if err != nil {
		t.Fatalf("db.Open(): %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	authService := auth.NewService(database, clock.Real{})
	adminID, err := authService.CreateAdmin(ctx, "push-admin@example.test", "Push admin", contentTestPassword)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	login, err := authService.Login(ctx, auth.LoginInput{Email: "push-admin@example.test", Password: contentTestPassword}, "push-test")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys(): %v", err)
	}
	service := push.NewService(database, push.Config{
		VAPIDPublicKey: publicKey, VAPIDPrivateKey: privateKey,
		VAPIDSubject: "push-admin@example.test",
	})
	mux := http.NewServeMux()
	RegisterPushRoutes(mux, authService, service)
	cookie := &http.Cookie{Name: sessionCookieName, Value: login.Token}

	keyResponse := pushRouteRequest(mux, http.MethodGet, "/api/v1/push/vapid-key", "", cookie)
	if keyResponse.Code != http.StatusOK {
		t.Fatalf("GET VAPID key status=%d body=%s", keyResponse.Code, keyResponse.Body)
	}
	var keyBody map[string]string
	if err := json.Unmarshal(keyResponse.Body.Bytes(), &keyBody); err != nil || keyBody["public_key"] != publicKey {
		t.Fatalf("VAPID key response=%s error=%v", keyResponse.Body, err)
	}
	if strings.Contains(keyResponse.Body.String(), privateKey) {
		t.Fatal("VAPID private key was returned by the public-key endpoint")
	}

	endpoint := "https://fcm.googleapis.com/fcm/send/route-test"
	authKey := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))
	body := `{"endpoint":"` + endpoint + `","keys":{"p256dh":"` + publicKey + `","auth":"` + authKey + `"}}`
	created := pushRouteRequest(mux, http.MethodPost, "/api/v1/push/subscribe", body, cookie)
	if created.Code != http.StatusNoContent {
		t.Fatalf("POST subscription status=%d body=%s", created.Code, created.Body)
	}
	var stored int
	if err := database.SQL.QueryRowContext(ctx, `SELECT count(*) FROM push_subscriptions WHERE user_id = ? AND endpoint = ?`, adminID, endpoint).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("stored subscription count=%d error=%v", stored, err)
	}
	invalid := pushRouteRequest(mux, http.MethodPost, "/api/v1/push/subscribe", strings.ReplaceAll(body, endpoint, "http://127.0.0.1/private"), cookie)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("private endpoint status=%d body=%s; want bad request", invalid.Code, invalid.Body)
	}
	deleted := pushRouteRequest(mux, http.MethodDelete, "/api/v1/push/subscribe", `{"endpoint":"`+endpoint+`"}`, cookie)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE subscription status=%d body=%s", deleted.Code, deleted.Body)
	}
	if unauthorized := pushRouteRequest(mux, http.MethodGet, "/api/v1/push/vapid-key", "", nil); unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated VAPID-key request status=%d", unauthorized.Code)
	}
}

func TestPushRoutesReportDisabledConfiguration(t *testing.T) {
	database, _, cookie, _ := newContentRoutesFixture(t)
	mux := http.NewServeMux()
	RegisterPushRoutes(mux, auth.NewService(database, clock.Real{}), push.NewService(database, push.Config{}))
	response := pushRouteRequest(mux, http.MethodGet, "/api/v1/push/vapid-key", "", cookie)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"push_unavailable"`) {
		t.Fatalf("disabled push endpoint status=%d body=%s", response.Code, response.Body)
	}
}

func pushRouteRequest(handler http.Handler, method, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
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
