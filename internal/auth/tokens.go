package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"fmt"
)

const (
	sessionTokenSize = 32
	inviteCodeSize   = 16
)

var inviteCodeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSessionToken returns a cryptographically random URL-safe bearer token.
func GenerateSessionToken() (string, error) {
	bytes := make([]byte, sessionTokenSize)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// HashSessionToken returns the SHA-256 digest suitable for storing instead of the token.
func HashSessionToken(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}

// GenerateInviteCode returns a cryptographically random, unpadded Base32 code.
func GenerateInviteCode() (string, error) {
	bytes := make([]byte, inviteCodeSize)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate invite code: %w", err)
	}
	return inviteCodeEncoding.EncodeToString(bytes), nil
}
