package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pestit/retain/internal/llm"
)

const secretProviderDetail = "test-api-key provider response body"

type fakeModels struct {
	models []string
	err    error
}

func (f fakeModels) ListModels(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.models, f.err
}

type fakeGenerator struct {
	err error
}

func (f fakeGenerator) GenerateJSON(ctx context.Context, request llm.GenerateJSONRequest) (llm.GenerateJSONResult, error) {
	if err := ctx.Err(); err != nil {
		return llm.GenerateJSONResult{}, err
	}
	if request.Validate == nil {
		return llm.GenerateJSONResult{}, errors.New("missing validator")
	}
	if err := request.Validate(ctx, json.RawMessage(`{"ok":true}`)); err != nil {
		return llm.GenerateJSONResult{}, err
	}
	return llm.GenerateJSONResult{}, f.err
}

type fakeStreamer struct {
	deltas []string
	err    error
}

func (f fakeStreamer) StreamText(ctx context.Context, _ llm.StreamRequest, onDelta func(context.Context, string) error) (llm.Usage, error) {
	for _, delta := range f.deltas {
		if err := onDelta(ctx, delta); err != nil {
			return llm.Usage{}, err
		}
	}
	return llm.Usage{}, f.err
}

func TestRunSmoke(t *testing.T) {
	tests := []struct {
		name       string
		models     fakeModels
		generator  fakeGenerator
		streamer   fakeStreamer
		wantErr    string
		wantOutput []string
	}{
		{
			name:       "success",
			models:     fakeModels{models: []string{"other", "selected-model"}},
			streamer:   fakeStreamer{deltas: []string{"hello"}},
			wantOutput: []string{"Models: checking selected model", "Models: OK", "Structured JSON: checking strict schema", "Structured JSON: OK", "Text stream: checking for a delta", "Text stream: OK"},
		},
		{
			name:       "missing model",
			models:     fakeModels{models: []string{"different-model"}},
			wantErr:    "selected model is unavailable",
			wantOutput: []string{"Models: checking selected model"},
		},
		{
			name:       "model API error is sanitized",
			models:     fakeModels{err: errors.New(secretProviderDetail)},
			wantErr:    "model discovery failed",
			wantOutput: []string{"Models: checking selected model"},
		},
		{
			name:       "JSON API error is sanitized",
			models:     fakeModels{models: []string{"selected-model"}},
			generator:  fakeGenerator{err: errors.New(secretProviderDetail)},
			wantErr:    "structured JSON check failed",
			wantOutput: []string{"Models: checking selected model", "Models: OK", "Structured JSON: checking strict schema"},
		},
		{
			name:       "empty stream fails",
			models:     fakeModels{models: []string{"selected-model"}},
			streamer:   fakeStreamer{},
			wantErr:    "text stream returned no delta",
			wantOutput: []string{"Models: checking selected model", "Models: OK", "Structured JSON: checking strict schema", "Structured JSON: OK", "Text stream: checking for a delta"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := runSmoke(context.Background(), "selected-model", test.models, test.generator, test.streamer, &output)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("runSmoke() error = %v", err)
				}
			} else {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("runSmoke() error = %v, want %q", err, test.wantErr)
				}
			}
			if strings.Contains(output.String(), secretProviderDetail) {
				t.Fatalf("output leaked provider details: %q", output.String())
			}
			gotLines := strings.Split(strings.TrimSpace(output.String()), "\n")
			if output.Len() == 0 {
				gotLines = nil
			}
			if len(gotLines) != len(test.wantOutput) {
				t.Fatalf("output lines = %#v, want %#v", gotLines, test.wantOutput)
			}
			for i := range gotLines {
				if gotLines[i] != test.wantOutput[i] {
					t.Errorf("output line %d = %q, want %q", i, gotLines[i], test.wantOutput[i])
				}
			}
		})
	}
}

func TestRunSmokeChecksCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runSmoke(ctx, "selected-model", fakeModels{}, fakeGenerator{}, fakeStreamer{}, &bytes.Buffer{})
	if !errors.Is(err, errSmokeCanceled) {
		t.Fatalf("runSmoke() error = %v, want cancellation", err)
	}
}

func TestValidateOKResponse(t *testing.T) {
	ctx := context.Background()
	if err := validateOKResponse(ctx, json.RawMessage(`{"ok":false}`)); err != nil {
		t.Fatalf("validateOKResponse() = %v, want valid boolean response", err)
	}
	for _, response := range []string{`{}`, `{"ok":"yes"}`, `{"ok":true,"extra":1}`, `{"ok":true} {}`} {
		if err := validateOKResponse(ctx, json.RawMessage(response)); err == nil {
			t.Errorf("validateOKResponse(%s) = nil, want error", response)
		}
	}
}
