package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	response := httptest.NewRecorder()
	if err := WriteJSON(response, http.StatusCreated, struct {
		OK bool `json:"ok"`
	}{OK: true}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	if response.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", response.Code, http.StatusCreated)
	}
	if got, want := response.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := response.Body.String(), `{"ok":true}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestWriteAPIError(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		code       string
		message    string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "bad request",
			status:     http.StatusBadRequest,
			code:       "invalid_request",
			message:    "The request is invalid.",
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":{"code":"invalid_request","message":"The request is invalid."}}`,
		},
		{
			name:       "not found",
			status:     http.StatusNotFound,
			code:       "not_found",
			message:    "Resource not found.",
			wantStatus: http.StatusNotFound,
			wantBody:   `{"error":{"code":"not_found","message":"Resource not found."}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			if err := WriteAPIError(response, tt.status, tt.code, tt.message); err != nil {
				t.Fatalf("WriteAPIError() error = %v", err)
			}

			if response.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if got, want := response.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
				t.Errorf("Content-Type = %q, want %q", got, want)
			}
			if got := response.Body.String(); got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
		})
	}
}

func TestDecodeJSON(t *testing.T) {
	type requestBody struct {
		Name string `json:"name"`
	}

	oversized := `{"name":"ok"}` + strings.Repeat(" ", MaxJSONBodyBytes)
	tests := []struct {
		name        string
		contentType string
		body        string
		wantErr     error
		wantStatus  int
		wantName    string
	}{
		{
			name:        "valid body",
			contentType: "application/json",
			body:        `{"name":"Retain"}`,
			wantName:    "Retain",
		},
		{
			name:        "accepts JSON media type parameters",
			contentType: "application/json; charset=utf-8",
			body:        `{"name":"Retain"}`,
			wantName:    "Retain",
		},
		{
			name:        "malformed JSON",
			contentType: "application/json",
			body:        `{"name":"private-data"`,
			wantErr:     ErrInvalidJSON,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "unknown field",
			contentType: "application/json",
			body:        `{"name":"Retain","admin":true}`,
			wantErr:     ErrInvalidJSON,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "trailing JSON value",
			contentType: "application/json",
			body:        `{"name":"Retain"} {"name":"other"}`,
			wantErr:     ErrInvalidJSON,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "invalid content type",
			contentType: "text/plain",
			body:        `{"name":"Retain"}`,
			wantErr:     ErrUnsupportedMediaType,
			wantStatus:  http.StatusUnsupportedMediaType,
		},
		{
			name:       "missing content type",
			body:       `{"name":"Retain"}`,
			wantErr:    ErrUnsupportedMediaType,
			wantStatus: http.StatusUnsupportedMediaType,
		},
		{
			name:        "body size limit",
			contentType: "application/json",
			body:        oversized,
			wantErr:     ErrJSONBodyTooLarge,
			wantStatus:  http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			if tt.contentType != "" {
				request.Header.Set("Content-Type", tt.contentType)
			}
			var got requestBody
			err := DecodeJSON(request, &got)

			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("DecodeJSON() error = %v", err)
				}
				if got.Name != tt.wantName {
					t.Errorf("decoded name = %q, want %q", got.Name, tt.wantName)
				}
				return
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("DecodeJSON() error = %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
			if status := JSONErrorStatus(err); status != tt.wantStatus {
				t.Errorf("JSONErrorStatus() = %d, want %d", status, tt.wantStatus)
			}
			if strings.Contains(err.Error(), "private-data") {
				t.Errorf("error exposed request body: %q", err)
			}
		})
	}
}

func TestDecodeJSONMaxBytesReaderLimit(t *testing.T) {
	body := `{"name":"` + strings.Repeat("a", MaxJSONBodyBytes) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Body = http.MaxBytesReader(httptest.NewRecorder(), request.Body, MaxJSONBodyBytes)

	var got struct {
		Name string `json:"name"`
	}
	err := DecodeJSON(request, &got)
	if !errors.Is(err, ErrJSONBodyTooLarge) {
		t.Fatalf("DecodeJSON() error = %v, want errors.Is(_, %v)", err, ErrJSONBodyTooLarge)
	}
	if status := JSONErrorStatus(err); status != http.StatusRequestEntityTooLarge {
		t.Errorf("JSONErrorStatus() = %d, want %d", status, http.StatusRequestEntityTooLarge)
	}
}

func TestJSONErrorStatusUnknownError(t *testing.T) {
	if got := JSONErrorStatus(fmt.Errorf("unrelated error")); got != 0 {
		t.Errorf("JSONErrorStatus() = %d, want 0", got)
	}
}
