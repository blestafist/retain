package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/config"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/httpapi"
	"github.com/pestit/retain/web"
)

const (
	databaseStartupTimeout = 10 * time.Second
	shutdownTimeout        = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	err := dispatch(os.Args[1:], func() error {
		return run(logger)
	}, func(args []string) error {
		return runAdminEntrypoint(args, os.Stdout)
	})
	if err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// dispatch selects the normal server startup or an explicit CLI command.
// The no-argument path intentionally keeps invoking the same server runner.
func dispatch(args []string, runServer func() error, runAdmin func([]string) error) error {
	if len(args) == 0 {
		return runServer()
	}
	if args[0] == "admin" {
		return runAdmin(args[1:])
	}
	return errors.New("unknown arguments; usage: retain [admin <command>]")
}

func runAdminEntrypoint(args []string, stdout io.Writer) error {
	if isAdminHelpRequest(args) {
		return runAdminCommand(context.Background(), args, config.Config{}, readAdminPassword, stdout)
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	return runAdminCommand(context.Background(), args, cfg, readAdminPassword, stdout)
}

func isAdminHelpRequest(args []string) bool {
	if len(args) == 1 && isAdminHelpFlag(args[0]) {
		return true
	}
	if len(args) == 0 || args[0] != "create" {
		return false
	}
	_, _, err := parseAdminCreateArgs(args[1:])
	return errors.Is(err, flag.ErrHelp)
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbCtx, cancelDB := context.WithTimeout(ctx, databaseStartupTimeout)
	database, err := db.Open(dbCtx, cfg.DatabasePath)
	cancelDB()
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			logger.Error("close database", "error", err)
		}
	}()

	authService := auth.NewService(database, clock.Real{})

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewHandler(cfg.PublicOrigin, database.SQL, authService, web.Files()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("HTTP server listening", "address", cfg.HTTPAddr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		if err := <-serverErrors; !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	}
}
