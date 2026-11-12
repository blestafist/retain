package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/contentimport"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
)

func TestImportRoutesPreviewApplyHistoryExportAndAccessControl(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "import-routes.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})
	queries := database.Queries()
	authService := auth.NewService(database, clock.Real{})
	ownerID, err := authService.CreateAdmin(ctx, "import-routes@example.test", "Import owner", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	login, err := authService.Login(ctx, auth.LoginInput{Email: "import-routes@example.test", Password: "correct horse battery staple"}, "import-routes-test")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	otherUser, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email: "import-other-routes@example.test", Name: "Other", PasswordHash: "unused",
		CreatedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateUser(other) error = %v", err)
	}
	otherToken, err := auth.GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken(other): %v", err)
	}
	otherDigest := auth.HashSessionToken(otherToken)
	if _, err := queries.CreateSession(ctx, gen.CreateSessionParams{
		TokenHash: otherDigest[:], UserID: otherUser.ID,
		ExpiresAt: time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC), CreatedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateSession(other) error = %v", err)
	}
	mux := http.NewServeMux()
	RegisterImportRoutes(mux, authService, contentimport.NewService(database, clock.Real{}))
	ownerCookie := &http.Cookie{Name: sessionCookieName, Value: login.Token}
	otherCookie := &http.Cookie{Name: sessionCookieName, Value: otherToken}

	unauthenticated := doImportRequest(mux, http.MethodGet, "/api/v1/import/batches", nil, "", "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated history status = %d, want 401", unauthenticated.Code)
	}

	raw := []byte(`{"format":"retain/v1","source":"route-test","decks":[{"external_id":"root","name":"Root","lang_target":"en","lang_native":"ru","decks":[{"external_id":"child","name":"Child","notes":[{"term":"hello","translation":"привет","example":"hello there"}]}]}]}`)
	previewResponse := doMultipartImport(mux, raw, ownerCookie)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview status/body = %d/%s", previewResponse.Code, previewResponse.Body)
	}
	var preview contentimport.PreviewResult
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview.BatchID == "" || preview.Adapter != "retain" || preview.Status != "previewed" || preview.Counts.New != 1 {
		t.Fatalf("preview response = %+v", preview)
	}
	applyBody := `{"lang_target":"en","lang_native":"ru"}`
	applyResponse := doImportRequest(mux, http.MethodPost, "/api/v1/import/"+preview.BatchID+"/apply", ownerCookie, applyBody, "application/json")
	if applyResponse.Code != http.StatusOK || !strings.Contains(applyResponse.Body.String(), `"status":"applied"`) {
		t.Fatalf("apply status/body = %d/%s", applyResponse.Code, applyResponse.Body)
	}
	otherApply := doImportRequest(mux, http.MethodPost, "/api/v1/import/"+preview.BatchID+"/apply", otherCookie, applyBody, "application/json")
	if otherApply.Code != http.StatusNotFound {
		t.Fatalf("other user's apply status/body = %d/%s, want 404", otherApply.Code, otherApply.Body)
	}

	history := doImportRequest(mux, http.MethodGet, "/api/v1/import/batches", ownerCookie, "", "")
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), preview.BatchID) || strings.Contains(history.Body.String(), `"raw"`) {
		t.Fatalf("history status/body = %d/%s", history.Code, history.Body)
	}
	otherHistory := doImportRequest(mux, http.MethodGet, "/api/v1/import/batches", otherCookie, "", "")
	if otherHistory.Code != http.StatusOK || strings.TrimSpace(otherHistory.Body.String()) != "[]" {
		t.Fatalf("other user's history = %d/%s, want []", otherHistory.Code, otherHistory.Body)
	}

	root, err := queries.GetDeckBySourceExternalID(ctx, gen.GetDeckBySourceExternalIDParams{
		OwnerID: ownerID, Source: sqlString("route-test"), ExternalID: sqlString("root"),
	})
	if err != nil {
		t.Fatalf("find imported root: %v", err)
	}
	exportURL := "/api/v1/decks/" + strconv.FormatInt(root.ID, 10) + "/export?format=retain"
	exportResponse := doImportRequest(mux, http.MethodGet, exportURL, ownerCookie, "", "")
	if exportResponse.Code != http.StatusOK || !strings.Contains(exportResponse.Header().Get("Content-Disposition"), ".retain\"") || !strings.Contains(exportResponse.Body.String(), `"format": "retain/v1"`) {
		t.Fatalf("retain export = %d, headers %v, body %s", exportResponse.Code, exportResponse.Header(), exportResponse.Body)
	}
	otherExport := doImportRequest(mux, http.MethodGet, exportURL, otherCookie, "", "")
	if otherExport.Code != http.StatusNotFound {
		t.Fatalf("other user's export status = %d, want 404", otherExport.Code)
	}

	badApply := doImportRequest(mux, http.MethodPost, "/api/v1/import/not-a-batch/apply", ownerCookie, applyBody, "application/json")
	if badApply.Code != http.StatusBadRequest {
		t.Fatalf("malformed batch ID apply status = %d, want 400", badApply.Code)
	}
	missingFile := doImportRequest(mux, http.MethodPost, "/api/v1/import/preview", ownerCookie, "", "application/json")
	if missingFile.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("non-multipart preview status = %d, want 415", missingFile.Code)
	}
}

func doMultipartImport(handler http.Handler, raw []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, _ := writer.CreateFormFile("file", "words.retain.json")
	_, _ = file.Write(raw)
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/import/preview", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func doImportRequest(handler http.Handler, method, target string, cookie *http.Cookie, body, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func sqlString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
}
