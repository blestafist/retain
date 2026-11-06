package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

// MaxJSONBodyBytes is the maximum size accepted by DecodeJSON.
const MaxJSONBodyBytes = 64 << 10

var (
	// ErrInvalidJSON covers malformed JSON, unknown fields, and trailing values.
	ErrInvalidJSON = errors.New("invalid JSON request body")
	// ErrUnsupportedMediaType means the request Content-Type is not application/json.
	ErrUnsupportedMediaType = errors.New("Content-Type must be application/json")
	// ErrJSONBodyTooLarge means the request body exceeds MaxJSONBodyBytes.
	ErrJSONBodyTooLarge = errors.New("JSON request body is too large")
)

// WriteJSON encodes value as JSON and writes it with the given HTTP status.
// It marshals before writing headers so encoding failures do not leave a partial response.
func WriteJSON(w http.ResponseWriter, status int, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, err = w.Write(body)
	return err
}

type apiErrorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteAPIError writes the standard API error envelope.
func WriteAPIError(w http.ResponseWriter, status int, code, message string) error {
	return WriteJSON(w, status, apiErrorEnvelope{
		Error: apiError{Code: code, Message: message},
	})
}

// DecodeJSON reads one bounded JSON value into destination. The request must
// have an application/json Content-Type, and unknown fields or trailing values
// are rejected. Returned errors contain no request data and are safe to expose.
func DecodeJSON(r *http.Request, destination any) error {
	if r == nil {
		return ErrInvalidJSON
	}
	if !hasJSONContentType(r.Header.Get("Content-Type")) {
		return ErrUnsupportedMediaType
	}
	if r.Body == nil {
		return ErrInvalidJSON
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxJSONBodyBytes+1))
	if len(body) > MaxJSONBodyBytes {
		return ErrJSONBodyTooLarge
	}
	if err != nil {
		return ErrInvalidJSON
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return ErrInvalidJSON
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ErrInvalidJSON
	}
	return nil
}

func hasJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

// JSONErrorStatus maps DecodeJSON errors to their corresponding HTTP status.
// It returns 0 for errors that did not originate from DecodeJSON.
func JSONErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrUnsupportedMediaType):
		return http.StatusUnsupportedMediaType
	case errors.Is(err, ErrJSONBodyTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrInvalidJSON):
		return http.StatusBadRequest
	default:
		return 0
	}
}
