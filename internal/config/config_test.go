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
		wantErr     string
	}{
		{
			name:     "default database path",
			wantPath: defaultDatabasePath,
		},
		{
			name:        "configured database path",
			environment: map[string]string{"DATABASE_PATH": "/tmp/retain.db"},
			wantPath:    "/tmp/retain.db",
		},
		{
			name:        "blank database path",
			environment: map[string]string{"DATABASE_PATH": "   "},
			wantErr:     "DATABASE_PATH must not be blank",
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
		})
	}
}
