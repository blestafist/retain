// Command llm-smoke manually checks the configured real LLM provider.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/pestit/retain/internal/config"
	"github.com/pestit/retain/internal/llm"
)

func main() {
	if err := run(); err != nil {
		// Configuration and provider errors can contain sensitive details. Keep the
		// command's diagnostic deliberately generic.
		fmt.Fprintln(os.Stderr, "llm-smoke failed; check LLM configuration and provider availability")
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !cfg.LLMEnabled {
		return errLLMDisabled
	}

	client, err := llm.NewClient(llm.Config{
		BaseURL: cfg.LLMBaseURL,
		APIKey:  cfg.LLMAPIKey,
		Model:   cfg.LLMModel,
		Timeout: cfg.LLMTimeout,
	})
	if err != nil {
		return err
	}

	// Bound the entire smoke run, including model discovery and both completion
	// modes, by the configured LLM timeout.
	ctx, cancel := context.WithTimeout(context.Background(), cfg.LLMTimeout)
	defer cancel()

	return runSmoke(ctx, cfg.LLMModel, client, client, client, os.Stdout)
}
