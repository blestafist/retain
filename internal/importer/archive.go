package importer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"io"
)

var (
	// ErrArchiveSource indicates that the source could not be compressed.
	ErrArchiveSource = errors.New("could not archive source")
	// ErrRestoreSource indicates that a source archive could not be restored.
	ErrRestoreSource = errors.New("could not restore source archive")
)

// ArchiveSource compresses raw with a deterministic gzip header and returns
// the SHA-256 digest of raw (not of the compressed archive).
func ArchiveSource(raw []byte) (compressed []byte, rawSHA256 [32]byte, err error) {
	rawSHA256 = sha256.Sum256(raw)

	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	writer.Header.OS = 255 // Stable "unknown" operating system value.

	n, writeErr := writer.Write(raw)
	closeErr := writer.Close()
	if writeErr != nil || n != len(raw) || closeErr != nil {
		return nil, [32]byte{}, ErrArchiveSource
	}

	return buffer.Bytes(), rawSHA256, nil
}

// RestoreSource decompresses a source archive. Any malformed, truncated, or
// corrupt gzip input is reported with the stable ErrRestoreSource sentinel.
func RestoreSource(compressed []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, ErrRestoreSource
	}

	raw, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return nil, ErrRestoreSource
	}

	return raw, nil
}
