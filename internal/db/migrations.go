package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrate applies all embedded SQLite migrations.
func Migrate(ctx context.Context, database *sql.DB) error {
	if database == nil {
		return fmt.Errorf("apply embedded migrations: database must not be nil")
	}

	migrations, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}
	return migrate(ctx, database, migrations)
}

func migrate(ctx context.Context, database *sql.DB, migrations fs.FS) error {
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrations)
	if err != nil {
		return fmt.Errorf("create goose migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply goose migrations: %w", err)
	}
	return nil
}
