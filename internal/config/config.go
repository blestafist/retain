package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/caarlos0/env/v11"
)

const defaultDatabasePath = "data/retain.db"

// Config contains the settings required to bootstrap the backend.
type Config struct {
	DatabasePath string `env:"DATABASE_PATH" envDefault:"data/retain.db"`
}

// Load reads and validates backend settings from the process environment.
func Load() (Config, error) {
	return load(env.ToMap(os.Environ()))
}

func load(environment map[string]string) (Config, error) {
	var cfg Config
	if err := env.ParseWithOptions(&cfg, env.Options{Environment: environment}); err != nil {
		return Config{}, fmt.Errorf("parse configuration: %w", err)
	}
	if strings.TrimSpace(cfg.DatabasePath) == "" {
		return Config{}, fmt.Errorf("validate configuration: DATABASE_PATH must not be blank")
	}

	return cfg, nil
}
