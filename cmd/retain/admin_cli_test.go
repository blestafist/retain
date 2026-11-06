package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

type adminCreatorFunc func(context.Context, string, string, string) (int64, error)

func (f adminCreatorFunc) CreateAdmin(ctx context.Context, email, name, password string) (int64, error) {
	return f(ctx, email, name, password)
}

func TestRunAdminCreateParsesArgumentsAndDefaultsName(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{name: "explicit name", args: []string{"--email=user@example.test", "--name=User Name"}, expected: "User Name"},
		{name: "email local part default", args: []string{"--email", "user@example.test"}, expected: "user"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader, prompts := passwordSequence("correct horse", "correct horse")
			var output strings.Builder
			creatorCalls := 0
			creator := adminCreatorFunc(func(_ context.Context, email, name, password string) (int64, error) {
				creatorCalls++
				if email != "user@example.test" {
					t.Errorf("email = %q, want user@example.test", email)
				}
				if name != tt.expected {
					t.Errorf("name = %q, want %q", name, tt.expected)
				}
				if password != "correct horse" {
					t.Errorf("password did not reach creator")
				}
				return 42, nil
			})

			if err := runAdminCreate(context.Background(), tt.args, creator, reader, &output); err != nil {
				t.Fatalf("runAdminCreate() error = %v", err)
			}
			if creatorCalls != 1 {
				t.Fatalf("creator calls = %d, want 1", creatorCalls)
			}
			if output.String() != "42\n" {
				t.Errorf("stdout = %q, want only admin ID", output.String())
			}
			if strings.Contains(output.String(), "correct horse") {
				t.Error("stdout echoed the password")
			}
			if got := strings.Join(*prompts, ","); got != "Password: ,Confirm password: " {
				t.Errorf("password prompts = %q", got)
			}
		})
	}
}

func TestRunAdminCreateRequiresEmail(t *testing.T) {
	readerCalls := 0
	creatorCalls := 0
	err := runAdminCreate(context.Background(), []string{"--name=User"}, adminCreatorFunc(func(context.Context, string, string, string) (int64, error) {
		creatorCalls++
		return 0, nil
	}), func(string) ([]byte, error) {
		readerCalls++
		return nil, nil
	}, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "--email is required") {
		t.Fatalf("runAdminCreate() error = %v, want missing email", err)
	}
	if readerCalls != 0 || creatorCalls != 0 {
		t.Errorf("reader calls = %d, creator calls = %d, want neither called", readerCalls, creatorCalls)
	}
}

func TestRunAdminCreateRejectsPasswordFlagWithoutEchoingValue(t *testing.T) {
	const secret = "do-not-echo-this"
	err := runAdminCreate(context.Background(), []string{"--email=user@example.test", "--password=" + secret}, adminCreatorFunc(func(context.Context, string, string, string) (int64, error) {
		t.Fatal("creator called with unsupported password flag")
		return 0, nil
	}), func(string) ([]byte, error) {
		t.Fatal("password reader called for invalid arguments")
		return nil, nil
	}, io.Discard)
	if err == nil {
		t.Fatal("runAdminCreate() succeeded with --password")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("argument value leaked in error: %v", err)
	}
}

func TestRunAdminCreateRejectsMismatchedPasswords(t *testing.T) {
	reader, _ := passwordSequence("first secret", "different secret")
	creatorCalls := 0
	var output strings.Builder
	err := runAdminCreate(context.Background(), []string{"--email=user@example.test"}, adminCreatorFunc(func(context.Context, string, string, string) (int64, error) {
		creatorCalls++
		return 0, nil
	}), reader, &output)

	if err == nil || err.Error() != "passwords do not match" {
		t.Fatalf("runAdminCreate() error = %v, want mismatch", err)
	}
	if creatorCalls != 0 || output.Len() != 0 {
		t.Errorf("creator calls = %d, stdout = %q; want no creation or output", creatorCalls, output.String())
	}
	if strings.Contains(err.Error(), "first secret") || strings.Contains(err.Error(), "different secret") {
		t.Fatalf("password leaked in error: %v", err)
	}
}

func TestRunAdminCreatePropagatesServiceErrorWithoutEchoingPassword(t *testing.T) {
	const secret = "never-log-me"
	serviceErr := errors.New("service rejected password " + secret)
	reader, _ := passwordSequence(secret, secret)
	var output strings.Builder
	err := runAdminCreate(context.Background(), []string{"--email=user@example.test"}, adminCreatorFunc(func(context.Context, string, string, string) (int64, error) {
		return 0, serviceErr
	}), reader, &output)

	if !errors.Is(err, serviceErr) {
		t.Fatalf("runAdminCreate() error = %v, want wrapped service error", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(output.String(), secret) {
		t.Fatalf("password leaked in error/stdout: error=%q stdout=%q", err, output.String())
	}
	if output.Len() != 0 {
		t.Errorf("stdout = %q, want empty on failure", output.String())
	}
}

func TestReadAdminPasswordRejectsNonTerminal(t *testing.T) {
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

	password, err := readAdminPassword("Password: ")
	if err == nil {
		t.Fatal("readAdminPassword() succeeded with non-terminal stdin")
	}
	if password != nil {
		t.Fatalf("password = %q, want nil", password)
	}
}

func passwordSequence(passwords ...string) (passwordReader, *[]string) {
	var prompts []string
	index := 0
	return func(prompt string) ([]byte, error) {
		prompts = append(prompts, prompt)
		if index >= len(passwords) {
			return nil, errors.New("unexpected password prompt")
		}
		password := []byte(passwords[index])
		index++
		return password, nil
	}, &prompts
}
