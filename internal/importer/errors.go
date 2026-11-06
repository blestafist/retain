package importer

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidJSON indicates malformed JSON or more than one JSON value.
	ErrInvalidJSON = errors.New("invalid JSON")
	// ErrInvalidStructure indicates a value with the wrong type or an unknown structural key.
	ErrInvalidStructure = errors.New("invalid retain/v1 structure")
	// ErrMissingRequiredField indicates a required field is missing or empty.
	ErrMissingRequiredField = errors.New("missing required field")
	// ErrUnsupportedFormat indicates that format is not exactly retain/v1.
	ErrUnsupportedFormat = errors.New("unsupported format")
	// ErrInvalidLanguage indicates a specified language is not supported for its direction.
	ErrInvalidLanguage = errors.New("invalid language")
	// ErrNegativeExpectedCount indicates that expected_count is less than zero.
	ErrNegativeExpectedCount = errors.New("negative expected_count")
)

// ParseError identifies the stable error category and structural location of
// a rejected bundle. It never includes input values or the source document.
type ParseError struct {
	Kind error
	Path string
}

func (e *ParseError) Error() string {
	if e.Path == "" {
		return e.Kind.Error()
	}
	return fmt.Sprintf("%s at %s", e.Kind, e.Path)
}

func (e *ParseError) Unwrap() error { return e.Kind }

func parseError(kind error, path string) error {
	return &ParseError{Kind: kind, Path: path}
}
