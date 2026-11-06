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
	// PRAGMA foreign_keys is connection-local. SQLite migrations must use the same connection for
	// the PRAGMA, explicit transaction, and error cleanup.
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrations)
	if err != nil {
		return fmt.Errorf("create goose migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		if cleanupErr := cleanupFailedMigration(ctx, database); cleanupErr != nil {
			return fmt.Errorf("apply goose migrations: %w (restore SQLite connection state: %v)", err, cleanupErr)
		}
		return fmt.Errorf("apply goose migrations: %w", err)
	}
	return nil
}

// A NO TRANSACTION SQLite migration can leave an explicit transaction open with foreign-key
// enforcement disabled after an error. Recover the pooled connection before returning the error.
func cleanupFailedMigration(ctx context.Context, database *sql.DB) error {
	_, rollbackErr := database.ExecContext(ctx, "ROLLBACK")
	_, enableErr := database.ExecContext(ctx, "PRAGMA foreign_keys = ON")

	var foreignKeys int
	queryErr := database.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys)
	if queryErr != nil {
		return fmt.Errorf("verify foreign_keys after rollback (rollback: %v, enable: %v): %w", rollbackErr, enableErr, queryErr)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("foreign_keys remains %d after rollback (rollback: %v, enable: %v)", foreignKeys, rollbackErr, enableErr)
	}
	return nil
}
