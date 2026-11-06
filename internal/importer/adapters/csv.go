// Package adapters contains adapters for external import formats.
package adapters

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/pestit/retain/internal/importer"
)

var (
	// ErrInvalidCSV indicates malformed CSV syntax.
	ErrInvalidCSV = errors.New("invalid CSV")
	// ErrInvalidCSVStructure indicates a valid CSV document with an invalid shape.
	ErrInvalidCSVStructure = errors.New("invalid CSV structure")
)

// CSVAdapter parses semicolon-separated term, translation, example, and tags rows.
type CSVAdapter struct{}

var _ importer.Adapter = CSVAdapter{}

func (CSVAdapter) Name() string { return "csv" }

// Detect reports whether the first nonempty record resembles a supported CSV
// header or a data row with all four columns.
func (CSVAdapter) Detect(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	reader := csv.NewReader(bytes.NewReader(raw))
	reader.Comma = ';'
	reader.FieldsPerRecord = -1

	for {
		record, err := reader.Read()
		if err != nil {
			return false
		}
		if emptyRecord(record) {
			continue
		}
		if isHeader(record) {
			return true
		}
		return hasRequiredCSVFields(record)
	}
}

// Parse converts semicolon-separated CSV into a validated retain/v1 Bundle.
func (CSVAdapter) Parse(raw []byte) (*importer.Bundle, error) {
	if !utf8.Valid(raw) {
		return nil, csvParseError(ErrInvalidCSV, 0)
	}
	reader := csv.NewReader(bytes.NewReader(raw))
	reader.Comma = ';'
	reader.FieldsPerRecord = -1

	bundle := &importer.Bundle{
		Format: "retain/v1",
		Source: "csv",
		Decks:  []importer.Deck{{Name: "CSV import"}},
	}
	firstRecord := true
	recordIndex := 0
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if errors.Is(err, csv.ErrFieldCount) {
				return nil, csvParseError(ErrInvalidCSVStructure, recordIndex)
			}
			return nil, csvParseError(ErrInvalidCSV, recordIndex)
		}
		if emptyRecord(record) {
			recordIndex++
			continue
		}
		if len(record) != 4 {
			return nil, csvParseError(ErrInvalidCSVStructure, recordIndex)
		}
		if firstRecord {
			firstRecord = false
			if isHeader(record) {
				recordIndex++
				continue
			}
		}
		bundle.Decks[0].Notes = append(bundle.Decks[0].Notes, importer.Note{
			Term:        record[0],
			Translation: record[1],
			Example:     record[2],
			Tags:        splitTags(record[3]),
		})
		recordIndex++
	}
	if firstRecord {
		return nil, csvParseError(ErrInvalidCSVStructure, 0)
	}
	if err := importer.ValidateBundle(bundle); err != nil {
		return nil, err
	}
	return bundle, nil
}

func isHeader(record []string) bool {
	if len(record) != 4 {
		return false
	}
	want := [...]string{"term", "translation", "example", "tags"}
	for i := range want {
		if record[i] != want[i] {
			return false
		}
	}
	return true
}

func emptyRecord(record []string) bool {
	for _, field := range record {
		if importer.HasECMAScriptNonWhitespace(field) {
			return false
		}
	}
	return true
}

func hasRequiredCSVFields(record []string) bool {
	return len(record) == 4 && importer.HasECMAScriptNonWhitespace(record[0]) && importer.HasECMAScriptNonWhitespace(record[1])
}

func splitTags(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		if tag := strings.TrimSpace(part); tag != "" {
			tags = append(tags, tag)
		}
	}
	if len(tags) == 0 {
		return nil
	}
	return tags
}

func csvParseError(kind error, row int) error {
	return &importer.ParseError{Kind: kind, Path: fmt.Sprintf("records[%d]", row)}
}
