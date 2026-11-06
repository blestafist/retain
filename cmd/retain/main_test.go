package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pestit/retain/internal/auth"
)

func TestDispatchWithoutArgumentsKeepsServerStartup(t *testing.T) {
	serverCalls := 0
	adminCalls := 0
	serverErr := errors.New("server stopped")
	err := dispatch(nil, func() error {
		serverCalls++
		return serverErr
	}, func([]string) error {
		adminCalls++
		return nil
	})
	if !errors.Is(err, serverErr) {
		t.Fatalf("dispatch() error = %v, want server error", err)
	}
	if serverCalls != 1 || adminCalls != 0 {
		t.Errorf("server/admin calls = %d/%d, want 1/0", serverCalls, adminCalls)
	}
}

func TestDispatchPassesAdminArgumentsToAdminCommand(t *testing.T) {
	wantArgs := []string{"create", "--email=admin@example.test"}
	serverCalls := 0
	adminCalls := 0
	err := dispatch([]string{"admin", "create", "--email=admin@example.test"}, func() error {
		serverCalls++
		return nil
	}, func(args []string) error {
		adminCalls++
		if !reflect.DeepEqual(args, wantArgs) {
			t.Errorf("admin args = %q, want %q", args, wantArgs)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("dispatch() error = %v", err)
	}
	if serverCalls != 0 || adminCalls != 1 {
		t.Errorf("server/admin calls = %d/%d, want 0/1", serverCalls, adminCalls)
	}
}

func TestDispatchUnknownArgumentsReturnUsageError(t *testing.T) {
	err := dispatch([]string{"serve"}, func() error {
		t.Fatal("server started for unknown arguments")
		return nil
	}, func([]string) error {
		t.Fatal("admin command ran for unknown arguments")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "usage: retain") {
		t.Fatalf("dispatch() error = %v, want usage error", err)
	}
}

func TestAdminHelpDispatchSkipsServerDatabaseAndTerminal(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "admin help", args: []string{"--help"}, want: "Usage: retain admin <command>"},
		{name: "create help", args: []string{"create", "--help"}, want: "Usage of retain admin create:"},
		{name: "create short help", args: []string{"create", "-h"}, want: "Usage of retain admin create:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DATABASE_PATH", "")
			t.Setenv("HTTP_ADDR", "")
			t.Setenv("PUBLIC_ORIGIN", "invalid origin")
			stdin, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			originalStdin := os.Stdin
			os.Stdin = stdin
			t.Cleanup(func() {
				os.Stdin = originalStdin
				_ = stdin.Close()
			})

			var output strings.Builder
			serverCalls := 0
			err = dispatch(append([]string{"admin"}, test.args...), func() error {
				serverCalls++
				return nil
			}, func(args []string) error {
				return runAdminEntrypoint(args, &output)
			})
			if err != nil {
				t.Fatalf("dispatch() error = %v", err)
			}
			if serverCalls != 0 {
				t.Errorf("server calls = %d, want 0", serverCalls)
			}
			if !strings.Contains(output.String(), test.want) {
				t.Errorf("help output %q does not contain %q", output.String(), test.want)
			}
			if strings.Contains(output.String(), "password") {
				t.Errorf("help unexpectedly requested password input: %q", output.String())
			}
		})
	}
}

func TestAdminCreateHelpValueIsValidatedBeforeDatabaseOpen(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "not-created", "retain.db")
	t.Setenv("DATABASE_PATH", databasePath)
	t.Setenv("HTTP_ADDR", ":8080")
	t.Setenv("PUBLIC_ORIGIN", "https://retain.example.test")
	args := []string{"create", "--email", "x", "--name", "--help"}
	var output strings.Builder

	err := runAdminEntrypoint(args, &output)
	if !errors.Is(err, auth.ErrInvalidInput) {
		t.Fatalf("runAdminEntrypoint(conflicting flag value) error = %v, want invalid input", err)
	}
	if strings.Contains(err.Error(), "--help") || strings.Contains(err.Error(), "x") {
		t.Errorf("validation error exposed an argument value: %v", err)
	}
	if strings.Contains(output.String(), "Usage of retain admin create:") {
		t.Errorf("name value was treated as help: %q", output.String())
	}
	if _, err := os.Stat(filepath.Dir(databasePath)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("database directory stat error = %v, want not exist", err)
	}
}
