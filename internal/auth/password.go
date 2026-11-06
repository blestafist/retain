package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	minPasswordCodePoints = 12

	argon2Version     = 19
	argon2MemoryKiB   = 19 * 1024
	argon2Iterations  = 2
	argon2Parallelism = 1
	argon2SaltSize    = 16
	argon2KeySize     = 32

	minEncodedMemoryKiB  = 19 * 1024
	maxEncodedMemoryKiB  = 64 * 1024
	minEncodedIterations = 2
	maxEncodedIterations = 4
	maxEncodedThreads    = 4
	maxEncodedHashLength = 256

	maxConcurrentArgon2Operations = 2
)

var (
	ErrPasswordInvalidUTF8   = errors.New("password must be valid UTF-8")
	ErrPasswordTooShort      = errors.New("password must contain at least 12 Unicode code points")
	ErrMalformedPasswordHash = errors.New("malformed password hash")
	// ErrPasswordHashBusy is returned when both Argon2 operation slots are occupied.
	ErrPasswordHashBusy = errors.New("password hashing capacity is full")

	argon2Slots = make(chan struct{}, maxConcurrentArgon2Operations)
)

type passwordKeyDeriver func(password, salt []byte, iterations, memoryKiB uint32, parallelism uint8) ([]byte, error)

// ValidatePassword checks that a password contains at least 12 Unicode code points.
func ValidatePassword(password string) error {
	if !utf8.ValidString(password) {
		return ErrPasswordInvalidUTF8
	}
	if utf8.RuneCountInString(password) < minPasswordCodePoints {
		return ErrPasswordTooShort
	}
	return nil
}

// HashPassword returns an Argon2id hash in the PHC string format.
func HashPassword(password string) (string, error) {
	return hashPasswordWith(password, deriveKey)
}

func hashPasswordWith(password string, derive passwordKeyDeriver) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}

	salt := make([]byte, argon2SaltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	key, err := derive([]byte(password), salt, argon2Iterations, argon2MemoryKiB, argon2Parallelism)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version,
		argon2MemoryKiB,
		argon2Iterations,
		argon2Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password matches an encoded Argon2id hash.
// Malformed or unsupported encodings return ErrMalformedPasswordHash.
func VerifyPassword(password, encodedHash string) (bool, error) {
	return verifyPasswordWith(password, encodedHash, deriveKey)
}

func verifyPasswordWith(password, encodedHash string, derive passwordKeyDeriver) (bool, error) {
	parsed, err := parsePasswordHash(encodedHash)
	if err != nil {
		return false, ErrMalformedPasswordHash
	}

	key, err := derive([]byte(password), parsed.salt, parsed.iterations, parsed.memoryKiB, parsed.parallelism)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(key, parsed.key) == 1, nil
}

type passwordHash struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
	salt        []byte
	key         []byte
}

func parsePasswordHash(encoded string) (passwordHash, error) {
	if len(encoded) > maxEncodedHashLength {
		return passwordHash{}, ErrMalformedPasswordHash
	}

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return passwordHash{}, ErrMalformedPasswordHash
	}

	memoryKiB, iterations, parallelism, ok := parseArgon2Parameters(parts[3])
	if !ok {
		return passwordHash{}, ErrMalformedPasswordHash
	}

	salt, err := decodePHCBase64(parts[4])
	if err != nil || len(salt) < argon2SaltSize || len(salt) > 32 {
		return passwordHash{}, ErrMalformedPasswordHash
	}
	key, err := decodePHCBase64(parts[5])
	if err != nil || len(key) != argon2KeySize {
		return passwordHash{}, ErrMalformedPasswordHash
	}

	return passwordHash{
		memoryKiB:   memoryKiB,
		iterations:  iterations,
		parallelism: parallelism,
		salt:        salt,
		key:         key,
	}, nil
}

func parseArgon2Parameters(parameters string) (memoryKiB, iterations uint32, parallelism uint8, ok bool) {
	parts := strings.Split(parameters, ",")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}

	memory, ok := parseCanonicalUint(strings.TrimPrefix(parts[0], "m="), 32)
	if !ok || !strings.HasPrefix(parts[0], "m=") {
		return 0, 0, 0, false
	}
	iterationCount, ok := parseCanonicalUint(strings.TrimPrefix(parts[1], "t="), 32)
	if !ok || !strings.HasPrefix(parts[1], "t=") {
		return 0, 0, 0, false
	}
	threadCount, ok := parseCanonicalUint(strings.TrimPrefix(parts[2], "p="), 8)
	if !ok || !strings.HasPrefix(parts[2], "p=") {
		return 0, 0, 0, false
	}

	if memory < minEncodedMemoryKiB || memory > maxEncodedMemoryKiB ||
		iterationCount < minEncodedIterations || iterationCount > maxEncodedIterations ||
		threadCount < 1 || threadCount > maxEncodedThreads || memory < 8*threadCount {
		return 0, 0, 0, false
	}

	return uint32(memory), uint32(iterationCount), uint8(threadCount), true
}

func parseCanonicalUint(value string, bitSize int) (uint64, bool) {
	parsed, err := strconv.ParseUint(value, 10, bitSize)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		return 0, false
	}
	return parsed, true
}

func decodePHCBase64(value string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.Strict().DecodeString(value)
	if err != nil || base64.RawStdEncoding.EncodeToString(decoded) != value {
		return nil, ErrMalformedPasswordHash
	}
	return decoded, nil
}

func deriveKey(password, salt []byte, iterations, memoryKiB uint32, parallelism uint8) ([]byte, error) {
	return deriveKeyWithSlots(argon2Slots, password, salt, iterations, memoryKiB, parallelism, argon2.IDKey)
}

func deriveKeyWithSlots(
	slots chan struct{},
	password, salt []byte,
	iterations, memoryKiB uint32,
	parallelism uint8,
	derive func(password, salt []byte, iterations, memoryKiB uint32, parallelism uint8, keyLen uint32) []byte,
) ([]byte, error) {
	select {
	case slots <- struct{}{}:
	default:
		return nil, ErrPasswordHashBusy
	}
	defer func() { <-slots }()
	return derive(password, salt, iterations, memoryKiB, parallelism, argon2KeySize), nil
}
