package adapters

import (
	"errors"

	"github.com/pestit/retain/internal/importer"
)

var (
	// ErrNoAdapter indicates that no registered adapter recognized the source.
	ErrNoAdapter = errors.New("no import adapter detected")
	// ErrAmbiguousAdapter indicates that multiple adapters recognized the source.
	ErrAmbiguousAdapter = errors.New("multiple import adapters detected")
)

// Detect returns the registered adapter that recognizes raw when exactly one
// adapter matches.
func Detect(raw []byte) (importer.Adapter, error) {
	return detectWith(raw, All())
}

func detectWith(raw []byte, candidates []importer.Adapter) (importer.Adapter, error) {
	var match importer.Adapter
	for _, candidate := range candidates {
		if !candidate.Detect(raw) {
			continue
		}
		if match != nil {
			return nil, ErrAmbiguousAdapter
		}
		match = candidate
	}
	if match == nil {
		return nil, ErrNoAdapter
	}
	return match, nil
}
