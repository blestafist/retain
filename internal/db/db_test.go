package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenConfiguresSQLiteAndQueries(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "retain db.sqlite")

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if got := database.SQL.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", got)
	}

	var journalMode string
	if err := database.SQL.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want %q", journalMode, "wal")
	}

	var busyTimeout int
	if err := database.SQL.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy timeout: %v", err)
	}
	if busyTimeout != busyTimeoutMilliseconds {
		t.Errorf("busy_timeout = %d, want %d", busyTimeout, busyTimeoutMilliseconds)
	}

	var foreignKeys int
	if err := database.SQL.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign keys setting: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}

	got, err := database.Queries().Ping(ctx)
	if err != nil {
		t.Fatalf("generated Ping() error = %v", err)
	}
	if got != 1 {
		t.Errorf("generated Ping() = %d, want 1", got)
	}
}

func TestOpenRejectsBlankPath(t *testing.T) {
	if _, err := Open(context.Background(), "  "); err == nil {
		t.Fatal("Open() error = nil, want an error")
	}
}

func TestOpenAppliesMigrationsOnceAndCreatesAccountAndContentSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "retain.db")

	for range 2 {
		database, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}

		var currentVersion int
		if err := database.SQL.QueryRowContext(ctx,
			"SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1",
		).Scan(&currentVersion); err != nil {
			_ = database.Close()
			t.Fatalf("read current migration version: %v", err)
		}
		if currentVersion != 8 {
			_ = database.Close()
			t.Errorf("current migration version = %d, want 8", currentVersion)
		}

		var productTables int
		if err := database.SQL.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'goose_db_version'",
		).Scan(&productTables); err != nil {
			_ = database.Close()
			t.Fatalf("count product tables: %v", err)
		}
		if productTables != 14 {
			_ = database.Close()
			t.Errorf("product tables = %d, want 14", productTables)
		}

		if err := database.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
}

func TestMigrateRejectsNilDatabase(t *testing.T) {
	if err := Migrate(context.Background(), nil); err == nil {
		t.Fatal("Migrate(nil) error = nil, want an error")
	}
}
