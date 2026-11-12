package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/llmservice"
)

type routeLLMAdminFake struct {
	status  llmservice.ModelStatus
	usage   []llmservice.LLMUsageAggregate
	setErr  error
	setCall string
}

func (f *routeLLMAdminFake) Models(context.Context) (llmservice.ModelStatus, error) {
	return f.status, nil
}

func (f *routeLLMAdminFake) SetModel(_ context.Context, model string) (string, error) {
	f.setCall = model
	if f.setErr != nil {
		return "", f.setErr
	}
	return model, nil
}

func (f *routeLLMAdminFake) Usage(context.Context) ([]llmservice.LLMUsageAggregate, error) {
	return f.usage, nil
}

func TestLLMAdminRoutesRequireAuthenticationAndAdmin(t *testing.T) {
	service, _, _ := newAccountHandlerTest(t)
	adminID, err := service.CreateAdmin(context.Background(), "admin-llm@example.test", "Admin", handlerTestPassword)
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	invite, err := service.CreateInvite(context.Background(), adminID)
	if err != nil {
		t.Fatalf("CreateInvite() error = %v", err)
	}
	if _, err := service.Register(context.Background(), auth.RegisterInput{
		Invite: invite.Code, Email: "learner-llm@example.test", Name: "Learner", Password: handlerTestPassword,
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	adminLogin, err := service.Login(context.Background(), auth.LoginInput{Email: "admin-llm@example.test", Password: handlerTestPassword}, "test")
	if err != nil {
		t.Fatalf("admin Login() error = %v", err)
	}
	learnerLogin, err := service.Login(context.Background(), auth.LoginInput{Email: "learner-llm@example.test", Password: handlerTestPassword}, "test")
	if err != nil {
		t.Fatalf("learner Login() error = %v", err)
	}

	fake := &routeLLMAdminFake{}
	mux := http.NewServeMux()
	RegisterLLMAdminRoutes(mux, service, fake)
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		cookie     *http.Cookie
		wantStatus int
	}{
		{name: "anonymous models", method: http.MethodGet, path: "/api/v1/admin/llm/models", wantStatus: http.StatusUnauthorized},
		{name: "anonymous model update", method: http.MethodPut, path: "/api/v1/admin/llm/model", body: `{"model":"x"}`, wantStatus: http.StatusUnauthorized},
		{name: "anonymous usage", method: http.MethodGet, path: "/api/v1/admin/llm-usage", wantStatus: http.StatusUnauthorized},
		{name: "learner models", method: http.MethodGet, path: "/api/v1/admin/llm/models", cookie: &http.Cookie{Name: sessionCookieName, Value: learnerLogin.Token}, wantStatus: http.StatusForbidden},
		{name: "learner model update", method: http.MethodPut, path: "/api/v1/admin/llm/model", body: `{"model":"x"}`, cookie: &http.Cookie{Name: sessionCookieName, Value: learnerLogin.Token}, wantStatus: http.StatusForbidden},
		{name: "learner usage", method: http.MethodGet, path: "/api/v1/admin/llm-usage", cookie: &http.Cookie{Name: sessionCookieName, Value: learnerLogin.Token}, wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := requestLLMAdminRoute(mux, test.method, test.path, test.body, test.cookie)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d body=%s, want %d", response.Code, response.Body, test.wantStatus)
			}
		})
	}

	fake.status = llmservice.ModelStatus{Models: []string{"model-a"}, SelectedModel: "model-a", Available: true}
	fake.usage = []llmservice.LLMUsageAggregate{{
		UserID: 3, UserName: "Learner", UserEmail: "learner@example.test", Model: "model-a", Feature: "ask",
		Requests: 4, CachedRequests: 1, PromptTokens: 17, CompletionTokens: 9,
	}}
	adminCookie := &http.Cookie{Name: sessionCookieName, Value: adminLogin.Token}
	models := requestLLMAdminRoute(mux, http.MethodGet, "/api/v1/admin/llm/models", "", adminCookie)
	if models.Code != http.StatusOK {
		t.Fatalf("admin GET models = %d %s", models.Code, models.Body)
	}
	var modelStatus llmservice.ModelStatus
	if err := json.Unmarshal(models.Body.Bytes(), &modelStatus); err != nil {
		t.Fatalf("decode model status: %v", err)
	}
	if !modelStatus.Available || modelStatus.SelectedModel != "model-a" || len(modelStatus.Models) != 1 {
		t.Errorf("model status = %+v", modelStatus)
	}

	updated := requestLLMAdminRoute(mux, http.MethodPut, "/api/v1/admin/llm/model", `{"model":"new-model"}`, adminCookie)
	if updated.Code != http.StatusOK || fake.setCall != "new-model" || !strings.Contains(updated.Body.String(), `"selected_model":"new-model"`) {
		t.Errorf("admin PUT model = %d %s, set call %q", updated.Code, updated.Body, fake.setCall)
	}

	usage := requestLLMAdminRoute(mux, http.MethodGet, "/api/v1/admin/llm-usage", "", adminCookie)
	if usage.Code != http.StatusOK {
		t.Fatalf("admin GET usage = %d %s", usage.Code, usage.Body)
	}
	var usageResponse struct {
		Users []struct {
			UserID int64  `json:"user_id"`
			Name   string `json:"name"`
			Email  string `json:"email"`
			Models []struct {
				Model            string `json:"model"`
				Kind             string `json:"kind"`
				Requests         int64  `json:"requests"`
				CachedRequests   int64  `json:"cached_requests"`
				PromptTokens     int64  `json:"prompt_tokens"`
				CompletionTokens int64  `json:"completion_tokens"`
			} `json:"models"`
		} `json:"users"`
	}
	if err := json.Unmarshal(usage.Body.Bytes(), &usageResponse); err != nil {
		t.Fatalf("decode usage response: %v", err)
	}
	if len(usageResponse.Users) != 1 || usageResponse.Users[0].Name != "Learner" || usageResponse.Users[0].Email != "learner@example.test" ||
		len(usageResponse.Users[0].Models) != 1 || usageResponse.Users[0].Models[0].Kind != "ask" || usageResponse.Users[0].Models[0].CachedRequests != 1 {
		t.Errorf("usage response = %+v", usageResponse)
	}
	if strings.Contains(usage.Body.String(), "cost") {
		t.Errorf("usage response must not expose monetary cost: %s", usage.Body)
	}
}

func TestLLMAdminModelUpdateMapsValidationAndDiscoveryErrors(t *testing.T) {
	service, _, _ := newAccountHandlerTest(t)
	_, err := service.CreateAdmin(context.Background(), "admin-update@example.test", "Admin", handlerTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	login, err := service.Login(context.Background(), auth.LoginInput{Email: "admin-update@example.test", Password: handlerTestPassword}, "test")
	if err != nil {
		t.Fatal(err)
	}
	fake := &routeLLMAdminFake{}
	mux := http.NewServeMux()
	RegisterLLMAdminRoutes(mux, service, fake)
	cookie := &http.Cookie{Name: sessionCookieName, Value: login.Token}
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "unknown model", err: llmservice.ErrUnknownModel, wantStatus: http.StatusUnprocessableEntity, wantCode: "unknown_model"},
		{name: "provider unavailable", err: llmservice.ErrModelDiscoveryUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: "model_discovery_unavailable"},
		{name: "invalid model", err: llmservice.ErrInvalidModel, wantStatus: http.StatusBadRequest, wantCode: "invalid_model"},
		{name: "internal error", err: errors.New("private details"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake.setErr = test.err
			response := requestLLMAdminRoute(mux, http.MethodPut, "/api/v1/admin/llm/model", `{"model":"requested"}`, cookie)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("PUT model = %d %s, want %d code %q", response.Code, response.Body, test.wantStatus, test.wantCode)
			}
			if strings.Contains(response.Body.String(), "private details") {
				t.Errorf("error response leaked details: %s", response.Body)
			}
		})
	}

	fake.setErr = nil
	invalid := requestLLMAdminRoute(mux, http.MethodPut, "/api/v1/admin/llm/model", `{"model":"x","other":true}`, cookie)
	if invalid.Code != http.StatusBadRequest {
		t.Errorf("unknown JSON field status = %d, want 400", invalid.Code)
	}
}

func requestLLMAdminRoute(mux http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}
