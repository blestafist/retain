package auth

import (
	"encoding/base32"
	"encoding/base64"
	"testing"
)

func TestSessionTokenPrimitives(t *testing.T) {
	first, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken() error = %v", err)
	}
	second, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken() error = %v", err)
	}
	if first == second {
		t.Fatal("GenerateSessionToken() returned duplicate tokens")
	}

	decoded, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("session token is not raw URL-safe base64: %v", err)
	}
	if len(decoded) != sessionTokenSize {
		t.Fatalf("decoded session token size = %d, want %d", len(decoded), sessionTokenSize)
	}

	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{name: "same token has same digest", a: first, b: first, want: true},
		{name: "different tokens have different digests", a: first, b: second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := HashSessionToken(tt.a), HashSessionToken(tt.b)
			if (a == b) != tt.want {
				t.Fatalf("digest equality = %v, want %v", a == b, tt.want)
			}
		})
	}
}

func TestGenerateInviteCode(t *testing.T) {
	seen := make(map[string]struct{}, 16)
	encoding := base32.StdEncoding.WithPadding(base32.NoPadding)
	for i := 0; i < 16; i++ {
		code, err := GenerateInviteCode()
		if err != nil {
			t.Fatalf("GenerateInviteCode() error = %v", err)
		}
		if _, exists := seen[code]; exists {
			t.Fatalf("GenerateInviteCode() returned duplicate code %q", code)
		}
		seen[code] = struct{}{}

		decoded, err := encoding.DecodeString(code)
		if err != nil {
			t.Fatalf("invite code is not unpadded Base32: %v", err)
		}
		if len(decoded) != inviteCodeSize || len(code) != 26 {
			t.Fatalf("invite code sizes = %d bytes/%d chars, want %d bytes/26 chars", len(decoded), len(code), inviteCodeSize)
		}
	}
}
