package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/config"
	"github.com/pestit/retain/internal/db"
)

const adminDatabaseStartupTimeout = 10 * time.Second

const adminCommandUsage = "Usage: retain admin <command>\n\nCommands:\n  create  Create an administrator\n  --help  Show this help\n"

// runAdminCommand runs a command from the retain admin namespace. It accepts
// arguments after "admin", so args[0] is the admin subcommand.
func runAdminCommand(ctx context.Context, args []string, cfg config.Config, readPassword passwordReader, stdout io.Writer) (resultErr error) {
	if len(args) == 1 && isAdminHelpFlag(args[0]) {
		return writeAdminCommandUsage(stdout)
	}
	if len(args) == 0 {
		return errors.New("admin command is required; use `retain admin --help`")
	}
	if args[0] != "create" {
		return errors.New("unknown admin command; usage: retain admin <command> (commands: create, --help)")
	}

	parsed, fs, err := parseAdminCreateArgs(args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return writeAdminCreateUsage(fs, stdout)
	}
	if err != nil {
		return err
	}

	dbCtx, cancelDB := context.WithTimeout(ctx, adminDatabaseStartupTimeout)
	database, err := db.Open(dbCtx, cfg.DatabasePath)
	cancelDB()
	if err != nil {
		return fmt.Errorf("open database for admin command: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close database after admin command: %w", err))
		}
	}()

	service := auth.NewService(database, clock.Real{})
	return runParsedAdminCreate(ctx, parsed, service, readPassword, stdout)
}

func writeAdminCommandUsage(stdout io.Writer) error {
	if stdout == nil {
		return errors.New("admin command output is required")
	}
	if _, err := io.WriteString(stdout, adminCommandUsage); err != nil {
		return fmt.Errorf("write admin command usage: %w", err)
	}
	return nil
}

func isAdminHelpFlag(arg string) bool {
	switch arg {
	case "-h", "-help", "--help":
		return true
	default:
		return false
	}
}
