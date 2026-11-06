package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pestit/retain/internal/db/gen"
	_ "modernc.org/sqlite"
)

const busyTimeoutMilliseconds = 5000

// DB is the SQLite connection pool and its generated query set.
type DB struct {
	SQL *sql.DB

	queries *gen.Queries
}

// Open opens SQLite, applies embedded migrations, and prepares generated queries.
func Open(ctx context.Context, path string) (*DB, error) {
	dsn, absolutePath, err := dataSourceName(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	// A single connection prevents concurrent writers from contending inside this process.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("connect to sqlite database: %w", err)
	}
	if err := Migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate sqlite database: %w", err)
	}

	return &DB{SQL: sqlDB, queries: gen.New(sqlDB)}, nil
}

// Queries returns the generated type-safe query methods for this database.
func (db *DB) Queries() *gen.Queries {
	return db.queries
}

// Close closes the underlying SQLite connection pool.
func (db *DB) Close() error {
	if err := db.SQL.Close(); err != nil {
		return fmt.Errorf("close sqlite database: %w", err)
	}
	return nil
}

func dataSourceName(path string) (string, string, error) {
	if strings.TrimSpace(path) == "" {
		return "", "", fmt.Errorf("open sqlite database: path must not be blank")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve sqlite database path: %w", err)
	}

	fileURL := url.URL{Scheme: "file", Path: absolutePath}
	query := url.Values{}
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMilliseconds))
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "recursive_triggers(ON)")
	fileURL.RawQuery = query.Encode()

	return fileURL.String(), absolutePath, nil
}
