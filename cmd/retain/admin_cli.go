package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/pestit/retain/internal/auth"
	"golang.org/x/term"
)

const maxAdminEmailBytes = 254

type adminCreator interface {
	CreateAdmin(context.Context, string, string, string) (int64, error)
}

type passwordReader func(prompt string) ([]byte, error)

type adminCreateArgs struct {
	email string
	name  string
}

var errPasswordInputRequiresTerminal = errors.New("password input requires a terminal")

func runAdminCreate(ctx context.Context, args []string, creator adminCreator, readPassword passwordReader, stdout io.Writer) error {
	parsed, fs, err := parseAdminCreateArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		return writeAdminCreateUsage(fs, stdout)
	}
	if err != nil {
		return err
	}
	return runParsedAdminCreate(ctx, parsed, creator, readPassword, stdout)
}

func parseAdminCreateArgs(args []string) (adminCreateArgs, *flag.FlagSet, error) {
	fs := flag.NewFlagSet("retain admin create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	// Keep flag.Parse's diagnostics private: malformed values may contain
	// secrets. Help is rendered separately from the parsed flag definitions.
	fs.Usage = func() {}
	var parsed adminCreateArgs
	fs.StringVar(&parsed.email, "email", "", "admin email address")
	fs.StringVar(&parsed.name, "name", "", "admin display name")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return parsed, fs, err
		}
		// Flag errors may include arbitrary argument values. Do not return them;
		// in particular, never reflect an attempted --password value.
		return parsed, fs, errors.New("invalid admin create arguments")
	}
	if fs.NArg() != 0 {
		return parsed, fs, errors.New("unexpected positional arguments")
	}
	if strings.TrimSpace(parsed.email) == "" {
		return parsed, fs, errors.New("--email is required")
	}
	if !validAdminEmail(parsed.email) {
		return parsed, fs, auth.ErrInvalidInput
	}
	return parsed, fs, nil
}

func validAdminEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || len(email) > maxAdminEmailBytes || !utf8.ValidString(email) {
		return false
	}
	address, err := mail.ParseAddress(email)
	return err == nil && address.Name == "" && address.Address == email
}

func runParsedAdminCreate(ctx context.Context, parsed adminCreateArgs, creator adminCreator, readPassword passwordReader, stdout io.Writer) error {
	if creator == nil {
		return errors.New("admin creator is required")
	}
	if readPassword == nil {
		return errors.New("password reader is required")
	}
	if parsed.name == "" {
		parsed.name, _, _ = strings.Cut(parsed.email, "@")
	}

	password, err := readPassword("Password: ")
	if err != nil {
		return adminPasswordReadError("read password", err)
	}
	defer clear(password)

	confirmation, err := readPassword("Confirm password: ")
	if err != nil {
		return adminPasswordReadError("confirm password", err)
	}
	defer clear(confirmation)

	if !bytes.Equal(password, confirmation) {
		return errors.New("passwords do not match")
	}

	id, err := creator.CreateAdmin(ctx, parsed.email, parsed.name, string(password))
	if err != nil {
		return redactAdminError("create admin", err, password, confirmation)
	}
	if _, err := fmt.Fprintln(stdout, id); err != nil {
		return fmt.Errorf("write admin ID: %w", err)
	}
	return nil
}

func writeAdminCreateUsage(fs *flag.FlagSet, stdout io.Writer) error {
	var defaults bytes.Buffer
	fs.SetOutput(&defaults)
	fs.PrintDefaults()
	if _, err := fmt.Fprintf(stdout, "Usage of %s:\n%s", fs.Name(), defaults.String()); err != nil {
		return fmt.Errorf("write admin create usage: %w", err)
	}
	return nil
}

func adminPasswordReadError(action string, cause error) error {
	reason := "terminal input failed"
	if errors.Is(cause, errPasswordInputRequiresTerminal) {
		reason = errPasswordInputRequiresTerminal.Error()
	}
	return &adminCLIError{message: action + ": " + reason, cause: cause}
}

// readAdminPassword reads a password without echoing it. It deliberately
// rejects redirected input rather than falling back to an unsafe prompt.
func readAdminPassword(prompt string) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, errPasswordInputRequiresTerminal
	}
	if _, err := fmt.Fprint(os.Stderr, prompt); err != nil {
		return nil, fmt.Errorf("write password prompt: %w", err)
	}
	password, readErr := term.ReadPassword(fd)
	_, newlineErr := fmt.Fprintln(os.Stderr)
	if readErr != nil {
		return nil, fmt.Errorf("read password: %w", readErr)
	}
	if newlineErr != nil {
		clear(password)
		return nil, fmt.Errorf("finish password prompt: %w", newlineErr)
	}
	return password, nil
}

type adminCLIError struct {
	message string
	cause   error
}

func (e *adminCLIError) Error() string { return e.message }

func (e *adminCLIError) Unwrap() error { return e.cause }

func redactAdminError(action string, cause error, secrets ...[]byte) error {
	message := action + ": " + cause.Error()
	for _, secret := range secrets {
		if len(secret) != 0 {
			message = strings.ReplaceAll(message, string(secret), "[REDACTED]")
		}
	}
	return &adminCLIError{message: message, cause: cause}
}
