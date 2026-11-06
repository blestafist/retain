package adapters

import (
	"errors"
	"strings"
	"testing"

	"github.com/pestit/retain/internal/importer"
)

type detectionTestAdapter struct {
	name    string
	matches bool
}

func (a *detectionTestAdapter) Name() string { return a.name }
func (a *detectionTestAdapter) Detect([]byte) bool {
	return a.matches
}
func (*detectionTestAdapter) Parse([]byte) (*importer.Bundle, error) {
	return nil, nil
}

func TestDetectWith(t *testing.T) {
	first := &detectionTestAdapter{name: "first", matches: true}
	second := &detectionTestAdapter{name: "second", matches: true}
	nonMatch := &detectionTestAdapter{name: "non-match"}

	tests := []struct {
		name      string
		adapters  []importer.Adapter
		want      importer.Adapter
		wantError error
	}{
		{name: "no candidates", wantError: ErrNoAdapter},
		{name: "no match", adapters: []importer.Adapter{nonMatch}, wantError: ErrNoAdapter},
		{name: "one match", adapters: []importer.Adapter{nonMatch, first}, want: first},
		{name: "multiple matches", adapters: []importer.Adapter{first, second}, wantError: ErrAmbiguousAdapter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := detectWith([]byte("fake source"), tt.adapters)
			if tt.wantError != nil {
				if err != tt.wantError {
					t.Fatalf("detectWith() error = %v, want sentinel %v", err, tt.wantError)
				}
				if got != nil {
					t.Fatalf("detectWith() adapter = %v, want nil", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("detectWith() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("detectWith() adapter = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectRegisteredAdapters(t *testing.T) {
	tests := []struct {
		name      string
		raw       []byte
		wantName  string
		wantError error
	}{
		{
			name:      "empty source",
			raw:       nil,
			wantError: ErrNoAdapter,
		},
		{
			name:      "malformed JSON",
			raw:       []byte(`{"format":`),
			wantError: ErrNoAdapter,
		},
		{
			name:      "malformed CSV",
			raw:       []byte(`"private-source;translation;;`),
			wantError: ErrNoAdapter,
		},
		{
			name:      "unknown source",
			raw:       []byte("private unknown source bytes"),
			wantError: ErrNoAdapter,
		},
		{
			name:     "retain/v1",
			raw:      []byte(`{"format":"retain/v1","decks":[]}`),
			wantName: "retain",
		},
		{
			name:     "CSV",
			raw:      []byte("term;translation;example;tags\nword;meaning;;"),
			wantName: "csv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Detect(tt.raw)
			if tt.wantError != nil {
				if err != tt.wantError {
					t.Fatalf("Detect() error = %v, want sentinel %v", err, tt.wantError)
				}
				if got != nil {
					t.Fatalf("Detect() adapter = %v, want nil", got)
				}
				if len(tt.raw) > 0 && strings.Contains(err.Error(), string(tt.raw)) {
					t.Fatalf("Detect() error leaked source bytes: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			if got == nil || got.Name() != tt.wantName {
				t.Fatalf("Detect() adapter = %v, want %q", got, tt.wantName)
			}
		})
	}
}

func TestDetectAmbiguousErrorDoesNotLeakSource(t *testing.T) {
	const source = "private ambiguous source bytes"
	candidates := []importer.Adapter{
		&detectionTestAdapter{name: "first", matches: true},
		&detectionTestAdapter{name: "second", matches: true},
	}

	adapter, err := detectWith([]byte(source), candidates)
	if adapter != nil {
		t.Fatalf("detectWith() adapter = %v, want nil", adapter)
	}
	if !errors.Is(err, ErrAmbiguousAdapter) {
		t.Fatalf("detectWith() error = %v, want ErrAmbiguousAdapter", err)
	}
	if strings.Contains(err.Error(), source) {
		t.Fatalf("detectWith() error leaked source bytes: %v", err)
	}
}
