package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  error
	}{
		{name: "minimum ASCII code points", password: "abc123456789"},
		{name: "minimum multibyte code points", password: "éééééééééééé"},
		{name: "combining marks count as code points", password: "e\u0301e\u0301e\u0301e\u0301e\u0301e\u0301"},
		{name: "too short", password: "abc12345678", wantErr: ErrPasswordTooShort},
		{name: "invalid UTF-8", password: string([]byte{0xff}), wantErr: ErrPasswordInvalidUTF8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidatePassword() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestHashPasswordRejectsInvalidPassword(t *testing.T) {
	for _, password := range []string{"short", string([]byte{0xff})} {
		hash, err := HashPassword(password)
		if err == nil {
			t.Fatalf("HashPassword(%q) unexpectedly succeeded", password)
		}
		if hash != "" {
			t.Fatalf("HashPassword(%q) returned a hash on error", password)
		}
		if strings.Contains(err.Error(), password) {
			t.Fatalf("error contains password: %v", err)
		}
	}
}

func TestHashPasswordAndVerifyPassword(t *testing.T) {
	const password = "correct horse battery staple"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("HashPassword() returned unexpected PHC format: %q", hash)
	}

	parsed, err := parsePasswordHash(hash)
	if err != nil {
		t.Fatalf("parsePasswordHash() error = %v", err)
	}
	if parsed.memoryKiB != argon2MemoryKiB || parsed.iterations != argon2Iterations || parsed.parallelism != argon2Parallelism {
		t.Fatalf("parsed parameters = (%d, %d, %d), want (%d, %d, %d)",
			parsed.memoryKiB, parsed.iterations, parsed.parallelism,
			argon2MemoryKiB, argon2Iterations, argon2Parallelism)
	}
	if len(parsed.salt) != argon2SaltSize || len(parsed.key) != argon2KeySize {
		t.Fatalf("parsed salt/key sizes = %d/%d, want %d/%d", len(parsed.salt), len(parsed.key), argon2SaltSize, argon2KeySize)
	}

	tests := []struct {
		name      string
		password  string
		hash      string
		wantMatch bool
		wantErr   error
	}{
		{name: "matching password", password: password, hash: hash, wantMatch: true},
		{name: "wrong password", password: "a different password", hash: hash},
		{name: "malformed hash", password: password, hash: hash + "$", wantErr: ErrMalformedPasswordHash},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, err := VerifyPassword(tt.password, tt.hash)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("VerifyPassword() error = %v, want %v", err, tt.wantErr)
			}
			if matched != tt.wantMatch {
				t.Fatalf("VerifyPassword() matched = %v, want %v", matched, tt.wantMatch)
			}
			if err != nil && (strings.Contains(err.Error(), tt.password) || strings.Contains(err.Error(), tt.hash)) {
				t.Fatalf("error contains a credential: %v", err)
			}
		})
	}
}

func TestParsePasswordHash(t *testing.T) {
	salt := []byte("0123456789abcdef")
	key := make([]byte, argon2KeySize)
	valid := encodeTestHash("argon2id", "19", "m=19456,t=2,p=1", salt, key)

	tests := []struct {
		name string
		hash string
		want bool
	}{
		{name: "valid standard format", hash: valid, want: true},
		{name: "wrong algorithm", hash: encodeTestHash("argon2i", "19", "m=19456,t=2,p=1", salt, key)},
		{name: "wrong version", hash: encodeTestHash("argon2id", "16", "m=19456,t=2,p=1", salt, key)},
		{name: "reordered parameters", hash: encodeTestHash("argon2id", "19", "t=2,m=19456,p=1", salt, key)},
		{name: "non-canonical integer", hash: encodeTestHash("argon2id", "19", "m=019456,t=2,p=1", salt, key)},
		{name: "unknown parameter", hash: encodeTestHash("argon2id", "19", "m=19456,t=2,p=1,x=1", salt, key)},
		{name: "memory below minimum", hash: encodeTestHash("argon2id", "19", "m=19455,t=2,p=1", salt, key)},
		{name: "memory above cap", hash: encodeTestHash("argon2id", "19", "m=65537,t=2,p=1", salt, key)},
		{name: "iterations below minimum", hash: encodeTestHash("argon2id", "19", "m=19456,t=1,p=1", salt, key)},
		{name: "iterations above cap", hash: encodeTestHash("argon2id", "19", "m=19456,t=5,p=1", salt, key)},
		{name: "parallelism above cap", hash: encodeTestHash("argon2id", "19", "m=19456,t=2,p=5", salt, key)},
		{name: "invalid base64", hash: strings.Replace(valid, base64.RawStdEncoding.EncodeToString(salt), "!!!!", 1)},
		{name: "padded base64", hash: strings.Replace(valid,
			base64.RawStdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(salt), 1)},
		{name: "short salt", hash: encodeTestHash("argon2id", "19", "m=19456,t=2,p=1", []byte("123456789012345"), key)},
		{name: "wrong key length", hash: encodeTestHash("argon2id", "19", "m=19456,t=2,p=1", salt, key[:argon2KeySize-1])},
		{name: "trailing field", hash: valid + "$extra"},
		{name: "overlong input", hash: valid + strings.Repeat("x", maxEncodedHashLength)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := parsePasswordHash(tt.hash)
			if tt.want {
				if err != nil {
					t.Fatalf("parsePasswordHash() error = %v", err)
				}
				if parsed.memoryKiB != argon2MemoryKiB || parsed.iterations != argon2Iterations || parsed.parallelism != argon2Parallelism {
					t.Fatalf("unexpected parsed parameters: %+v", parsed)
				}
				return
			}
			if !errors.Is(err, ErrMalformedPasswordHash) {
				t.Fatalf("parsePasswordHash() error = %v, want ErrMalformedPasswordHash", err)
			}
			if strings.Contains(err.Error(), tt.hash) {
				t.Fatalf("error contains encoded hash: %v", err)
			}
		})
	}
}

func encodeTestHash(algorithm, version, parameters string, salt, key []byte) string {
	return fmt.Sprintf("$%s$v=%s$%s$%s$%s", algorithm, version, parameters,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}
