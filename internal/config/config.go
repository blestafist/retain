package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/pestit/retain/internal/origin"
)

const defaultDatabasePath = "data/retain.db"
const defaultHTTPAddr = ":8080"
const defaultPublicOrigin = "http://localhost:8080"

// Config contains the settings required to bootstrap the backend.
type Config struct {
	DatabasePath         string        `env:"DATABASE_PATH" envDefault:"data/retain.db"`
	HTTPAddr             string        `env:"HTTP_ADDR" envDefault:":8080"`
	PublicOrigin         string        `env:"PUBLIC_ORIGIN" envDefault:"http://localhost:8080"`
	LLMBaseURL           string        `env:"LLM_BASE_URL"`
	LLMAPIKey            string        `env:"LLM_API_KEY"`
	LLMModel             string        `env:"LLM_MODEL" envDefault:"cx/gpt-6-luna"`
	LLMModelFast         string        `env:"LLM_MODEL_FAST"`
	LLMTimeout           time.Duration `env:"LLM_TIMEOUT" envDefault:"30s"`
	LLMDailyLimitPerUser int           `env:"LLM_DAILY_LIMIT_PER_USER" envDefault:"300"`
	LLMEnabled           bool          `env:"-"`
}

// Load reads and validates backend settings from the process environment.
func Load() (Config, error) {
	return load(env.ToMap(os.Environ()))
}

func load(environment map[string]string) (Config, error) {
	llmConfigured := strings.TrimSpace(environment["LLM_BASE_URL"]) != ""
	parseEnvironment := environment
	if !llmConfigured {
		parseEnvironment = withoutLLMSettings(environment)
	}

	var cfg Config
	if err := env.ParseWithOptions(&cfg, env.Options{Environment: parseEnvironment}); err != nil {
		return Config{}, fmt.Errorf("parse configuration: %w", err)
	}
	if strings.TrimSpace(cfg.DatabasePath) == "" {
		return Config{}, fmt.Errorf("validate configuration: DATABASE_PATH must not be blank")
	}
	if strings.TrimSpace(cfg.HTTPAddr) == "" {
		return Config{}, fmt.Errorf("validate configuration: HTTP_ADDR must not be blank")
	}
	if _, err := origin.Parse(cfg.PublicOrigin); err != nil {
		return Config{}, fmt.Errorf("validate configuration: PUBLIC_ORIGIN must be an absolute HTTP(S) origin without userinfo, path, query, or fragment: %w", err)
	}
	if !llmConfigured {
		return cfg, nil
	}
	if err := validateLLMBaseURL(cfg.LLMBaseURL); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(cfg.LLMAPIKey) == "" {
		return Config{}, fmt.Errorf("validate configuration: LLM_API_KEY must not be blank when LLM_BASE_URL is set")
	}
	if cfg.LLMTimeout <= 0 {
		return Config{}, fmt.Errorf("validate configuration: LLM_TIMEOUT must be positive")
	}
	if cfg.LLMDailyLimitPerUser <= 0 {
		return Config{}, fmt.Errorf("validate configuration: LLM_DAILY_LIMIT_PER_USER must be positive")
	}
	cfg.LLMEnabled = true

	return cfg, nil
}

func withoutLLMSettings(environment map[string]string) map[string]string {
	filtered := make(map[string]string, len(environment))
	for key, value := range environment {
		switch key {
		case "LLM_BASE_URL", "LLM_API_KEY", "LLM_MODEL", "LLM_MODEL_FAST", "LLM_TIMEOUT", "LLM_DAILY_LIMIT_PER_USER":
			continue
		default:
			filtered[key] = value
		}
	}
	return filtered
}

func validateLLMBaseURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.Hostname() == "" ||
		(!strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https")) ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(value, "#") {
		return fmt.Errorf("validate configuration: LLM_BASE_URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if _, err := origin.Parse(u.Scheme + "://" + u.Host); err != nil {
		return fmt.Errorf("validate configuration: LLM_BASE_URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	return nil
}
