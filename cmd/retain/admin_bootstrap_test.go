package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/config"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
)

const adminBootstrapTestPassword = "correct horse battery staple"

func TestRunAdminCommandCreatesAdminAndDefaultSettings(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "retain.db")
	passwordReader, prompts := passwordSequence(adminBootstrapTestPassword, adminBootstrapTestPassword)
	var stdout strings.Builder

	err := runAdminCommand(ctx, []string{"create", "--email=Admin@Example.test", "--name=Admin User"}, config.Config{
		DatabasePath: databasePath,
	}, passwordReader, &stdout)
	if err != nil {
		t.Fatalf("runAdminCommand() error = %v", err)
	}
	if got, want := *prompts, []string{"Password: ", "Confirm password: "}; !equalStrings(got, want) {
		t.Errorf("password prompts = %q, want %q", got, want)
	}
	if strings.Contains(stdout.String(), adminBootstrapTestPassword) {
		t.Fatalf("stdout leaked the password: %q", stdout.String())
	}
	adminID, err := strconv.ParseInt(strings.TrimSpace(stdout.String()), 10, 64)
	if err != nil {
		t.Fatalf("stdout = %q, want printed admin ID: %v", stdout.String(), err)
	}

	database := openAdminBootstrapTestDB(t, databasePath)
	user, err := database.Queries().GetUserByID(ctx, adminID)
	if err != nil {
		t.Fatalf("GetUserByID(%d) error = %v", adminID, err)
	}
	if user.Email != "admin@example.test" || user.Name != "Admin User" || !user.IsAdmin {
		t.Errorf("created user = %+v, want normalized admin", user)
	}

	settings, err := database.Queries().GetUserSettings(ctx, adminID)
	if err != nil {
		t.Fatalf("GetUserSettings(%d) error = %v", adminID, err)
	}
	wantSettings := gen.UserSetting{
		UserID:           adminID,
		NewPerDay:        20,
		MaxReviewsPerDay: 200,
		RequestRetention: 0.9,
		MaximumInterval:  36500,
		LearningSteps:    "1m 10m",
		RelearningSteps:  "10m",
		Timezone:         "UTC",
		DayStartHour:     4,
		ThemeMode:        "system",
		ThemeSeed:        "#3A4527",
		ThemeVariant:     "fidelity",
	}
	if settings != wantSettings {
		t.Errorf("settings = %+v, want defaults %+v", settings, wantSettings)
	}
}

func TestRunAdminCommandReturnsValidationAndDuplicateEmailErrors(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "retain.db")
	cfg := config.Config{DatabasePath: databasePath}

	reader, _ := passwordSequence(adminBootstrapTestPassword, adminBootstrapTestPassword)
	err := runAdminCommand(ctx, []string{"create", "--email=not-an-email"}, cfg, reader, io.Discard)
	if !errors.Is(err, auth.ErrInvalidInput) {
		t.Fatalf("invalid email error = %v, want auth.ErrInvalidInput", err)
	}
	if strings.Contains(err.Error(), adminBootstrapTestPassword) {
		t.Fatalf("validation error leaked the password: %v", err)
	}

	reader, _ = passwordSequence(adminBootstrapTestPassword, adminBootstrapTestPassword)
	if err := runAdminCommand(ctx, []string{"create", "--email=admin@example.test"}, cfg, reader, io.Discard); err != nil {
		t.Fatalf("initial admin creation error = %v", err)
	}

	reader, prompts := passwordSequence(adminBootstrapTestPassword, adminBootstrapTestPassword)
	var stdout strings.Builder
	err = runAdminCommand(ctx, []string{"create", "--email=ADMIN@example.test"}, cfg, reader, &stdout)
	if !errors.Is(err, auth.ErrEmailExists) {
		t.Fatalf("duplicate email error = %v, want auth.ErrEmailExists", err)
	}
	if got, want := *prompts, []string{"Password: ", "Confirm password: "}; !equalStrings(got, want) {
		t.Errorf("password prompts = %q, want %q", got, want)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout after duplicate email = %q, want empty", stdout.String())
	}

	database := openAdminBootstrapTestDB(t, databasePath)
	adminCount, err := database.Queries().CountAdmins(ctx)
	if err != nil {
		t.Fatalf("CountAdmins() error = %v", err)
	}
	if adminCount != 1 {
		t.Errorf("admin count = %d, want 1", adminCount)
	}
}

func TestRunAdminCommandHelpDoesNotOpenDatabaseOrReadPassword(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "admin help",
			args: []string{"--help"},
			want: []string{"Usage: retain admin <command>", "create", "--help"},
		},
		{
			name: "create help",
			args: []string{"create", "--help"},
			want: []string{"Usage of retain admin create:", "-email string", "-name string"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "not-created", "retain.db")
			parentPath := filepath.Dir(databasePath)
			readerCalls := 0
			reader := func(string) ([]byte, error) {
				readerCalls++
				return []byte(adminBootstrapTestPassword), nil
			}
			var stdout strings.Builder
			err := runAdminCommand(context.Background(), tt.args, config.Config{DatabasePath: databasePath}, reader, &stdout)
			if err != nil {
				t.Fatalf("runAdminCommand(%v) error = %v", tt.args, err)
			}
			for _, want := range tt.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("help output %q does not contain %q", stdout.String(), want)
				}
			}
			if readerCalls != 0 {
				t.Errorf("password reader calls = %d, want 0", readerCalls)
			}
			if _, err := os.Stat(parentPath); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("database directory stat error = %v, want os.ErrNotExist", err)
			}
		})
	}
}

func TestRunAdminCommandUnknownSubcommandReturnsSafeUsageError(t *testing.T) {
	const secret = "--password=never-echo-this"
	databasePath := filepath.Join(t.TempDir(), "not-created", "retain.db")
	readerCalls := 0
	err := runAdminCommand(context.Background(), []string{secret}, config.Config{DatabasePath: databasePath}, func(string) ([]byte, error) {
		readerCalls++
		return nil, nil
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "usage: retain admin") {
		t.Fatalf("unknown command error = %v, want safe usage error", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("unknown command leaked its argument: %v", err)
	}
	if readerCalls != 0 {
		t.Errorf("password reader calls = %d, want 0", readerCalls)
	}
	if _, err := os.Stat(filepath.Dir(databasePath)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("database directory stat error = %v, want os.ErrNotExist", err)
	}
}

func openAdminBootstrapTestDB(t *testing.T, path string) *db.DB {
	t.Helper()
	database, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("database.Close() error = %v", err)
		}
	})
	return database
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
