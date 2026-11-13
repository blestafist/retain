package config

import (
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		environment map[string]string
		wantPath    string
		wantAddr    string
		wantOrigin  string
		wantErr     string
	}{
		{
			name:       "default settings",
			wantPath:   defaultDatabasePath,
			wantAddr:   defaultHTTPAddr,
			wantOrigin: defaultPublicOrigin,
		},
		{
			name:        "configured database path",
			environment: map[string]string{"DATABASE_PATH": "/tmp/retain.db", "HTTP_ADDR": "127.0.0.1:9090", "PUBLIC_ORIGIN": "https://retain.example.test"},
			wantPath:    "/tmp/retain.db",
			wantAddr:    "127.0.0.1:9090",
			wantOrigin:  "https://retain.example.test",
		},
		{
			name:        "Vite development origin with explicit port",
			environment: map[string]string{"PUBLIC_ORIGIN": "http://localhost:5173"},
			wantPath:    defaultDatabasePath,
			wantAddr:    defaultHTTPAddr,
			wantOrigin:  "http://localhost:5173",
		},
		{
			name:        "valid IPv6 public origin",
			environment: map[string]string{"PUBLIC_ORIGIN": "https://[2001:db8::1]:8443"},
			wantPath:    defaultDatabasePath,
			wantAddr:    defaultHTTPAddr,
			wantOrigin:  "https://[2001:db8::1]:8443",
		},
		{
			name:        "blank database path",
			environment: map[string]string{"DATABASE_PATH": "   "},
			wantErr:     "DATABASE_PATH must not be blank",
		},
		{
			name:        "blank HTTP address",
			environment: map[string]string{"HTTP_ADDR": "  "},
			wantErr:     "HTTP_ADDR must not be blank",
		},
		{
			name:        "blank public origin",
			environment: map[string]string{"PUBLIC_ORIGIN": "  "},
			wantErr:     "PUBLIC_ORIGIN must be an absolute HTTP(S) origin",
		},
		{
			name:        "relative public origin",
			environment: map[string]string{"PUBLIC_ORIGIN": "/app"},
			wantErr:     "PUBLIC_ORIGIN must be an absolute HTTP(S) origin",
		},
		{
			name:        "unsupported public origin scheme",
			environment: map[string]string{"PUBLIC_ORIGIN": "ftp://retain.example.test"},
			wantErr:     "PUBLIC_ORIGIN must be an absolute HTTP(S) origin",
		},
		{
			name:        "public origin with userinfo",
			environment: map[string]string{"PUBLIC_ORIGIN": "https://user@retain.example.test"},
			wantErr:     "PUBLIC_ORIGIN must be an absolute HTTP(S) origin",
		},
		{
			name:        "public origin with path",
			environment: map[string]string{"PUBLIC_ORIGIN": "https://retain.example.test/"},
			wantErr:     "PUBLIC_ORIGIN must be an absolute HTTP(S) origin",
		},
		{
			name:        "public origin with query",
			environment: map[string]string{"PUBLIC_ORIGIN": "https://retain.example.test?x=1"},
			wantErr:     "PUBLIC_ORIGIN must be an absolute HTTP(S) origin",
		},
		{
			name:        "public origin with fragment",
			environment: map[string]string{"PUBLIC_ORIGIN": "https://retain.example.test#"},
			wantErr:     "PUBLIC_ORIGIN must be an absolute HTTP(S) origin",
		},
		{
			name:        "public origin with invalid port",
			environment: map[string]string{"PUBLIC_ORIGIN": "https://retain.example.test:70000"},
			wantErr:     "PUBLIC_ORIGIN must be an absolute HTTP(S) origin",
		},
		{
			name:        "public origin with bracketed DNS name",
			environment: map[string]string{"PUBLIC_ORIGIN": "https://[example.com]"},
			wantErr:     "PUBLIC_ORIGIN must be an absolute HTTP(S) origin",
		},
		{
			name:        "public origin with invalid IPv6 literal",
			environment: map[string]string{"PUBLIC_ORIGIN": "https://[2001:db8::zz]"},
			wantErr:     "PUBLIC_ORIGIN must be an absolute HTTP(S) origin",
		},
		{
			name:        "public origin with bracketed IPv4 literal",
			environment: map[string]string{"PUBLIC_ORIGIN": "https://[127.0.0.1]"},
			wantErr:     "PUBLIC_ORIGIN must be an absolute HTTP(S) origin",
		},
		{
			name:        "public origin with invalid hostname syntax",
			environment: map[string]string{"PUBLIC_ORIGIN": "https://retain_app.example.test"},
			wantErr:     "PUBLIC_ORIGIN must be an absolute HTTP(S) origin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := load(tt.environment)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("load() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("load() error = %v", err)
			}
			if got.DatabasePath != tt.wantPath {
				t.Errorf("DatabasePath = %q, want %q", got.DatabasePath, tt.wantPath)
			}
			if got.HTTPAddr != tt.wantAddr {
				t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, tt.wantAddr)
			}
			if got.PublicOrigin != tt.wantOrigin {
				t.Errorf("PublicOrigin = %q, want %q", got.PublicOrigin, tt.wantOrigin)
			}
		})
	}
}

func TestLoadPushConfiguration(t *testing.T) {
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys(): %v", err)
	}
	environment := map[string]string{
		"VAPID_PUBLIC_KEY": publicKey, "VAPID_PRIVATE_KEY": privateKey,
		"VAPID_SUBJECT": "mailto:admin@example.test",
	}
	cfg, err := load(environment)
	if err != nil {
		t.Fatalf("load valid push config: %v", err)
	}
	if !cfg.PushEnabled || cfg.VAPIDSubject != "mailto:admin@example.test" {
		t.Fatalf("push enabled = %t, subject = %q; want enabled and normalized subject", cfg.PushEnabled, cfg.VAPIDSubject)
	}

	tests := []struct {
		name string
		edit func(map[string]string)
		want string
	}{
		{name: "partial key set", edit: func(env map[string]string) { delete(env, "VAPID_PRIVATE_KEY") }, want: "must be set together"},
		{name: "invalid public key", edit: func(env map[string]string) { env["VAPID_PUBLIC_KEY"] = "not-a-key" }, want: "base64url encoded"},
		{name: "mismatched key pair", edit: func(env map[string]string) {
			otherPrivate, _, keyErr := webpush.GenerateVAPIDKeys()
			if keyErr != nil {
				t.Fatalf("GenerateVAPIDKeys(): %v", keyErr)
			}
			env["VAPID_PRIVATE_KEY"] = otherPrivate
		}, want: "do not form a valid pair"},
		{name: "invalid subject", edit: func(env map[string]string) { env["VAPID_SUBJECT"] = "file:///secret" }, want: "VAPID_SUBJECT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := make(map[string]string, len(environment))
			for key, value := range environment {
				input[key] = value
			}
			test.edit(input)
			if _, err := load(input); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("load() error = %v; want substring %q", err, test.want)
			}
		})
	}
}
