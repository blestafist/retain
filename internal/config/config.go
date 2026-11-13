package config

import (
	"crypto/elliptic"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/mail"
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
	VAPIDPublicKey       string        `env:"VAPID_PUBLIC_KEY"`
	VAPIDPrivateKey      string        `env:"VAPID_PRIVATE_KEY"`
	VAPIDSubject         string        `env:"VAPID_SUBJECT"`
	PushEnabled          bool          `env:"-"`
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
	if err := validatePushConfig(&cfg); err != nil {
		return Config{}, err
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

func validatePushConfig(cfg *Config) error {
	provided := 0
	for _, value := range []string{cfg.VAPIDPublicKey, cfg.VAPIDPrivateKey, cfg.VAPIDSubject} {
		if strings.TrimSpace(value) != "" {
			provided++
		}
	}
	if provided == 0 {
		return nil
	}
	if provided != 3 {
		return fmt.Errorf("validate configuration: VAPID_PUBLIC_KEY, VAPID_PRIVATE_KEY, and VAPID_SUBJECT must be set together")
	}
	publicKey, publicErr := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(cfg.VAPIDPublicKey), "="))
	privateKey, privateErr := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(cfg.VAPIDPrivateKey), "="))
	if publicErr != nil || privateErr != nil || len(publicKey) != 65 || len(privateKey) != 32 {
		return fmt.Errorf("validate configuration: VAPID keys must be base64url encoded P-256 public and private keys")
	}
	curve := elliptic.P256()
	privateScalar := new(big.Int).SetBytes(privateKey)
	if privateScalar.Sign() <= 0 || privateScalar.Cmp(curve.Params().N) >= 0 {
		return fmt.Errorf("validate configuration: VAPID private key scalar is outside the P-256 range")
	}
	publicX, publicY := elliptic.Unmarshal(curve, publicKey)
	privateX, privateY := curve.ScalarBaseMult(privateKey)
	if publicX == nil || privateX == nil || publicX.Cmp(privateX) != 0 || publicY.Cmp(privateY) != 0 {
		return fmt.Errorf("validate configuration: VAPID public and private keys do not form a valid pair")
	}
	subject := strings.TrimSpace(cfg.VAPIDSubject)
	if strings.HasPrefix(strings.ToLower(subject), "mailto:") {
		subject = strings.TrimSpace(subject[len("mailto:"):])
	}
	if strings.HasPrefix(strings.ToLower(subject), "https://") {
		parsed, err := url.Parse(subject)
		if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
			return fmt.Errorf("validate configuration: VAPID_SUBJECT must be an email address or an absolute HTTPS URL")
		}
	} else {
		parsed, err := mail.ParseAddress(subject)
		if err != nil || parsed.Address != subject || strings.ContainsAny(subject, "\r\n") {
			return fmt.Errorf("validate configuration: VAPID_SUBJECT must be an email address or an absolute HTTPS URL")
		}
		subject = "mailto:" + subject
	}
	cfg.VAPIDPublicKey = strings.TrimSpace(cfg.VAPIDPublicKey)
	cfg.VAPIDPrivateKey = strings.TrimSpace(cfg.VAPIDPrivateKey)
	cfg.VAPIDSubject = subject
	cfg.PushEnabled = true
	return nil
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
