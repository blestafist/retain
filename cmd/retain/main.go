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
	"github.com/pestit/retain/internal/decks"
	"github.com/pestit/retain/internal/httpapi"
	"github.com/pestit/retain/internal/jobs"
	"github.com/pestit/retain/internal/llm"
	"github.com/pestit/retain/internal/llmservice"
	"github.com/pestit/retain/internal/llmstore"
	"github.com/pestit/retain/internal/notes"
	"github.com/pestit/retain/internal/study"
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
	appClock := clock.Real{}
	deckService := decks.NewService(database, appClock)
	noteService := notes.NewService(database, appClock)
	studyService := study.NewService(database, appClock)
	llmStore := llmstore.New(database.Queries())

	var generator interface {
		GenerateJSON(context.Context, llm.GenerateJSONRequest) (llm.GenerateJSONResult, error)
	}
	var streamer interface {
		StreamText(context.Context, llm.StreamRequest, func(context.Context, string) error) (llm.Usage, error)
	}
	var modelProvider llmservice.ModelProvider
	if cfg.LLMEnabled {
		client, err := llm.NewClient(llm.Config{
			BaseURL: cfg.LLMBaseURL,
			APIKey:  cfg.LLMAPIKey,
			Model:   cfg.LLMModel,
			Timeout: cfg.LLMTimeout,
		})
		if err != nil {
			return fmt.Errorf("initialize LLM client: %w", err)
		}
		generator, streamer, modelProvider = client, client, client
	}
	adminService := llmservice.NewAdminService(llmStore, modelProvider, cfg.LLMModel, appClock)
	if err := adminService.Initialize(ctx); err != nil {
		// Serving without model selection persistence is safer than blocking startup.
		logger.Warn("LLM model selection initialization unavailable", "component", "llm_admin")
	}
	if cfg.LLMEnabled {
		go func() {
			modelCtx, cancelModels := context.WithTimeout(ctx, cfg.LLMTimeout)
			defer cancelModels()
			status, err := adminService.Models(modelCtx)
			if ctx.Err() == nil && (err != nil || !status.Available || status.Warning != "") {
				logger.Warn("LLM model discovery or configured model validation is unavailable", "component", "llm_admin")
			}
		}()
	}
	llmConfig := llmservice.Config{
		Model: cfg.LLMModel, DailyLimit: int64(cfg.LLMDailyLimitPerUser),
		ModelResolver: adminService.ResolveModel,
	}
	llmService := llmservice.NewService(authService, deckService, generator, llmStore, appClock, llmConfig)
	askService := llmservice.NewAskService(authService, deckService, noteService, streamer, llmStore, appClock, llmConfig)
	mnemonicService := llmservice.NewMnemonicService(authService, deckService, noteService, generator, llmStore, appClock, llmConfig)
	synonymService := llmservice.NewSynonymService(authService, deckService, studyService, noteService, generator, llmStore, appClock, llmConfig)
	deckEnrichService := llmservice.NewDeckEnrichService(deckService, noteService, llmStore, llmService, appClock, llmservice.DeckEnrichConfig{
		FastModel: cfg.LLMModelFast,
	})
	var worker *jobs.Worker
	if cfg.LLMEnabled {
		worker, err = jobs.New(llmStore, appClock, map[string]jobs.Handler{
			llmservice.DeckEnrichJobKind: deckEnrichService.Handler(),
		}, jobs.Config{Workers: 1})
		if err != nil {
			return fmt.Errorf("initialize jobs worker: %w", err)
		}
	}

	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewHandlerWithOptions(cfg.PublicOrigin, database.SQL, authService, database, web.Files(), httpapi.HandlerOptions{
			LLMStore: llmStore, LLMService: llmService, AskService: askService,
			MnemonicService: mnemonicService, SynonymService: synonymService,
			DeckEnrichService: deckEnrichService, AdminService: adminService,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      writeTimeoutForLLM(cfg.LLMTimeout),
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("HTTP server listening", "address", cfg.HTTPAddr)
		serverErrors <- server.ListenAndServe()
	}()
	var workerErrors chan error
	if worker != nil {
		workerErrors = make(chan error, 1)
		go func() { workerErrors <- worker.Run(ctx) }()
	}

	select {
	case err := <-serverErrors:
		stop()
		if workerErrors != nil {
			if workerErr := <-workerErrors; workerErr != nil {
				logger.Error("background jobs worker stopped", "component", "jobs")
			}
		}
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
		if workerErrors != nil {
			if err := <-workerErrors; err != nil {
				return fmt.Errorf("run background jobs: %w", err)
			}
		}
		return nil
	}
}

func writeTimeoutForLLM(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return 2*timeout + 15*time.Second
}
