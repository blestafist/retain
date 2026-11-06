package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		environment map[string]string
		wantPath    string
		wantAddr    string
		wantErr     string
	}{
		{
			name:     "default settings",
			wantPath: defaultDatabasePath,
			wantAddr: defaultHTTPAddr,
		},
		{
			name:        "configured database path",
			environment: map[string]string{"DATABASE_PATH": "/tmp/retain.db", "HTTP_ADDR": "127.0.0.1:9090"},
			wantPath:    "/tmp/retain.db",
			wantAddr:    "127.0.0.1:9090",
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
		})
	}
}
