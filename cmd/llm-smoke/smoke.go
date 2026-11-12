package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/pestit/retain/internal/llm"
)

var (
	errLLMDisabled   = errors.New("LLM is disabled")
	errSmokeCanceled = errors.New("smoke test canceled")
)

type modelLister interface {
	ListModels(context.Context) ([]string, error)
}

type jsonGenerator interface {
	GenerateJSON(context.Context, llm.GenerateJSONRequest) (llm.GenerateJSONResult, error)
}

type textStreamer interface {
	StreamText(context.Context, llm.StreamRequest, func(context.Context, string) error) (llm.Usage, error)
}

func runSmoke(ctx context.Context, model string, modelsClient modelLister, generator jsonGenerator, streamer textStreamer, output io.Writer) error {
	if ctx == nil || modelsClient == nil || generator == nil || streamer == nil || output == nil {
		return errors.New("invalid smoke test configuration")
	}
	if err := smokeContextError(ctx); err != nil {
		return err
	}
	if err := printStep(output, "Models: checking selected model"); err != nil {
		return err
	}
	models, err := modelsClient.ListModels(ctx)
	if ctxErr := smokeContextError(ctx); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return errors.New("model discovery failed")
	}
	available := false
	for _, listedModel := range models {
		if listedModel == model {
			available = true
			break
		}
	}
	if !available {
		return errors.New("selected model is unavailable")
	}
	if err := printStep(output, "Models: OK"); err != nil {
		return err
	}

	if err := smokeContextError(ctx); err != nil {
		return err
	}
	if err := printStep(output, "Structured JSON: checking strict schema"); err != nil {
		return err
	}
	_, err = generator.GenerateJSON(ctx, llm.GenerateJSONRequest{
		Model:      model,
		System:     "Return only a JSON object matching the requested schema.",
		User:       "Return an object where ok is true.",
		SchemaName: "smoke_ok",
		Schema:     json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`),
		Validate:   validateOKResponse,
	})
	if ctxErr := smokeContextError(ctx); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return errors.New("structured JSON check failed")
	}
	if err := printStep(output, "Structured JSON: OK"); err != nil {
		return err
	}

	if err := smokeContextError(ctx); err != nil {
		return err
	}
	if err := printStep(output, "Text stream: checking for a delta"); err != nil {
		return err
	}
	hasDelta := false
	_, err = streamer.StreamText(ctx, llm.StreamRequest{
		Model:  model,
		System: "Reply briefly and plainly.",
		User:   "Say hello.",
	}, func(deltaCtx context.Context, delta string) error {
		if err := smokeContextError(deltaCtx); err != nil {
			return err
		}
		if delta != "" {
			hasDelta = true
		}
		return nil
	})
	if ctxErr := smokeContextError(ctx); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return errors.New("text stream check failed")
	}
	if !hasDelta {
		return errors.New("text stream returned no delta")
	}
	return printStep(output, "Text stream: OK")
}

func validateOKResponse(ctx context.Context, response json.RawMessage) error {
	if err := smokeContextError(ctx); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(response))
	decoder.DisallowUnknownFields()
	var result struct {
		OK *bool `json:"ok"`
	}
	if err := decoder.Decode(&result); err != nil || result.OK == nil {
		return errors.New("response does not match the smoke schema")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("response contains trailing JSON")
	}
	return smokeContextError(ctx)
}

func smokeContextError(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return errSmokeCanceled
	}
	return nil
}

func printStep(output io.Writer, step string) error {
	if _, err := fmt.Fprintln(output, step); err != nil {
		return errors.New("could not write smoke test progress")
	}
	return nil
}
