package importer

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

func TestArchiveSourceRoundTripAndDeterminism(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "empty"},
		{name: "bytes", raw: []byte{0, 1, 2, 0xff, 0, 0x7f}},
		{name: "UTF-8", raw: []byte("Zażółć gęślą jaźń — Привет, мир! こんにちは")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, gotHash, err := ArchiveSource(tt.raw)
			if err != nil {
				t.Fatalf("ArchiveSource() error = %v", err)
			}

			wantHash := sha256.Sum256(tt.raw)
			if gotHash != wantHash {
				t.Fatalf("ArchiveSource() hash = %x, want raw hash %x", gotHash, wantHash)
			}
			if gotHash == sha256.Sum256(compressed) {
				t.Fatal("ArchiveSource() returned the hash of the compressed data")
			}

			restored, err := RestoreSource(compressed)
			if err != nil {
				t.Fatalf("RestoreSource() error = %v", err)
			}
			if !bytes.Equal(restored, tt.raw) {
				t.Fatalf("RestoreSource() = %q, want exact source %q", restored, tt.raw)
			}

			again, againHash, err := ArchiveSource(tt.raw)
			if err != nil {
				t.Fatalf("second ArchiveSource() error = %v", err)
			}
			if !bytes.Equal(again, compressed) {
				t.Fatal("ArchiveSource() produced different gzip bytes for the same source")
			}
			if againHash != gotHash {
				t.Fatalf("second ArchiveSource() hash = %x, want %x", againHash, gotHash)
			}

			if len(compressed) < 10 {
				t.Fatalf("gzip archive has length %d, want at least its 10-byte header", len(compressed))
			}
			if !bytes.Equal(compressed[:4], []byte{0x1f, 0x8b, 0x08, 0}) {
				t.Fatalf("gzip header prefix/flags = % x, want deterministic gzip header", compressed[:4])
			}
			if !bytes.Equal(compressed[4:8], []byte{0, 0, 0, 0}) {
				t.Fatalf("gzip mtime = % x, want zero", compressed[4:8])
			}
			if compressed[9] != 255 {
				t.Fatalf("gzip OS = %d, want stable unknown value 255", compressed[9])
			}
		})
	}
}

func TestRestoreSourceRejectsMalformedAndCorruptArchives(t *testing.T) {
	valid, _, err := ArchiveSource([]byte("source content that must not appear in errors"))
	if err != nil {
		t.Fatalf("ArchiveSource() error = %v", err)
	}

	truncatedHeader := append([]byte(nil), valid[:5]...)
	truncatedStream := append([]byte(nil), valid[:len(valid)-1]...)
	badChecksum := append([]byte(nil), valid...)
	badChecksum[len(badChecksum)-8] ^= 0xff

	tests := []struct {
		name       string
		compressed []byte
	}{
		{name: "malformed", compressed: []byte("not a gzip archive: secret input")},
		{name: "truncated header", compressed: truncatedHeader},
		{name: "truncated stream", compressed: truncatedStream},
		{name: "bad checksum", compressed: badChecksum},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RestoreSource(tt.compressed)
			if !errors.Is(err, ErrRestoreSource) {
				t.Fatalf("RestoreSource() error = %v, want ErrRestoreSource", err)
			}
			if got != nil {
				t.Fatalf("RestoreSource() data = %q, want nil on error", got)
			}
			if err.Error() != ErrRestoreSource.Error() {
				t.Fatalf("RestoreSource() error = %q, want stable sentinel message %q", err, ErrRestoreSource)
			}
		})
	}
}
