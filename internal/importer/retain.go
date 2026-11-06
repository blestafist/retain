package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const retainFormat = "retain/v1"

// RetainAdapter parses the native retain/v1 exchange format.
type RetainAdapter struct{}

var _ Adapter = RetainAdapter{}

func (RetainAdapter) Name() string { return "retain" }

// Detect reports whether raw is a JSON object whose format is retain/v1.
func (RetainAdapter) Detect(raw []byte) bool {
	root, err := decodeRoot(raw)
	if err != nil {
		return false
	}
	formatRaw, ok := root["format"]
	if !ok || !isJSONString(formatRaw) {
		return false
	}
	var format string
	if json.Unmarshal(formatRaw, &format) != nil {
		return false
	}
	return format == retainFormat
}

// Parse converts a retain/v1 document into a validated Bundle.
func (RetainAdapter) Parse(raw []byte) (*Bundle, error) { return ParseRetain(raw) }

// ParseRetain parses and validates the native retain/v1 exchange format.
func ParseRetain(raw []byte) (*Bundle, error) {
	root, err := decodeRoot(raw)
	if err != nil {
		return nil, err
	}
	if err := rejectUnknown(root, "", "format", "source", "decks"); err != nil {
		return nil, err
	}

	formatRaw, ok := root["format"]
	if !ok {
		return nil, parseError(ErrMissingRequiredField, "format")
	}
	if !isJSONString(formatRaw) {
		return nil, parseError(ErrInvalidStructure, "format")
	}
	var format string
	if err := json.Unmarshal(formatRaw, &format); err != nil {
		return nil, parseError(ErrInvalidStructure, "format")
	}
	if format != retainFormat {
		return nil, parseError(ErrUnsupportedFormat, "format")
	}

	bundle := &Bundle{Format: format}
	if sourceRaw, ok := root["source"]; ok {
		bundle.Source, err = readString(sourceRaw, "source")
		if err != nil {
			return nil, err
		}
	}
	if decksRaw, ok := root["decks"]; ok {
		bundle.Decks, err = readDecks(decksRaw, "decks")
		if err != nil {
			return nil, err
		}
	}
	return bundle, nil
}

func decodeRoot(raw []byte) (map[string]json.RawMessage, error) {
	var value json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, parseError(ErrInvalidJSON, "")
	}
	return readObject(value, "")
}

func readDecks(raw json.RawMessage, path string) ([]Deck, error) {
	items, err := readArray(raw, path)
	if err != nil {
		return nil, err
	}
	decks := make([]Deck, 0, len(items))
	for i, item := range items {
		deck, err := readDeck(item, fmt.Sprintf("%s[%d]", path, i))
		if err != nil {
			return nil, err
		}
		decks = append(decks, deck)
	}
	return decks, nil
}

func readDeck(raw json.RawMessage, path string) (Deck, error) {
	var deck Deck
	obj, err := readObject(raw, path)
	if err != nil {
		return deck, err
	}
	if err := rejectUnknown(obj, path,
		"external_id", "name", "lang_target", "lang_native", "expected_count", "decks", "notes",
	); err != nil {
		return deck, err
	}

	if value, ok := obj["external_id"]; ok {
		deck.ExternalID, err = readString(value, joinPath(path, "external_id"))
		if err != nil {
			return deck, err
		}
	}
	nameRaw, ok := obj["name"]
	if !ok {
		return deck, parseError(ErrMissingRequiredField, joinPath(path, "name"))
	}
	deck.Name, err = readString(nameRaw, joinPath(path, "name"))
	if err != nil {
		return deck, err
	}
	if strings.TrimSpace(deck.Name) == "" {
		return deck, parseError(ErrMissingRequiredField, joinPath(path, "name"))
	}

	if value, ok := obj["lang_target"]; ok {
		deck.LangTarget, err = readString(value, joinPath(path, "lang_target"))
		if err != nil {
			return deck, err
		}
		if deck.LangTarget != "de" && deck.LangTarget != "en" {
			return deck, parseError(ErrInvalidLanguage, joinPath(path, "lang_target"))
		}
	}
	if value, ok := obj["lang_native"]; ok {
		deck.LangNative, err = readString(value, joinPath(path, "lang_native"))
		if err != nil {
			return deck, err
		}
		if deck.LangNative != "pl" && deck.LangNative != "ru" {
			return deck, parseError(ErrInvalidLanguage, joinPath(path, "lang_native"))
		}
	}
	if value, ok := obj["expected_count"]; ok {
		count, err := readInteger(value, joinPath(path, "expected_count"))
		if err != nil {
			return deck, err
		}
		if count < 0 {
			return deck, parseError(ErrNegativeExpectedCount, joinPath(path, "expected_count"))
		}
		deck.ExpectedCount = &count
	}
	if value, ok := obj["decks"]; ok {
		deck.Decks, err = readDecks(value, joinPath(path, "decks"))
		if err != nil {
			return deck, err
		}
	}
	if value, ok := obj["notes"]; ok {
		deck.Notes, err = readNotes(value, joinPath(path, "notes"))
		if err != nil {
			return deck, err
		}
	}
	return deck, nil
}

func readNotes(raw json.RawMessage, path string) ([]Note, error) {
	items, err := readArray(raw, path)
	if err != nil {
		return nil, err
	}
	notes := make([]Note, 0, len(items))
	for i, item := range items {
		note, err := readNote(item, fmt.Sprintf("%s[%d]", path, i))
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	return notes, nil
}

func readNote(raw json.RawMessage, path string) (Note, error) {
	var note Note
	obj, err := readObject(raw, path)
	if err != nil {
		return note, err
	}
	if err := rejectUnknown(obj, path,
		"external_id", "term", "translation", "transcription", "example", "example_translation", "notes", "tags", "alternatives", "fields",
	); err != nil {
		return note, err
	}
	if value, ok := obj["external_id"]; ok {
		note.ExternalID, err = readString(value, joinPath(path, "external_id"))
		if err != nil {
			return note, err
		}
	}
	termRaw, ok := obj["term"]
	if !ok {
		return note, parseError(ErrMissingRequiredField, joinPath(path, "term"))
	}
	note.Term, err = readString(termRaw, joinPath(path, "term"))
	if err != nil {
		return note, err
	}
	if strings.TrimSpace(note.Term) == "" {
		return note, parseError(ErrMissingRequiredField, joinPath(path, "term"))
	}
	translationRaw, ok := obj["translation"]
	if !ok {
		return note, parseError(ErrMissingRequiredField, joinPath(path, "translation"))
	}
	note.Translation, err = readString(translationRaw, joinPath(path, "translation"))
	if err != nil {
		return note, err
	}
	if strings.TrimSpace(note.Translation) == "" {
		return note, parseError(ErrMissingRequiredField, joinPath(path, "translation"))
	}

	optionalStrings := []struct {
		key         string
		destination *string
	}{
		{key: "transcription", destination: &note.Transcription},
		{key: "example", destination: &note.Example},
		{key: "example_translation", destination: &note.ExampleTranslation},
		{key: "notes", destination: &note.Notes},
	}
	for _, field := range optionalStrings {
		if value, ok := obj[field.key]; ok {
			*field.destination, err = readString(value, joinPath(path, field.key))
			if err != nil {
				return note, err
			}
		}
	}
	if value, ok := obj["tags"]; ok {
		note.Tags, err = readStringArray(value, joinPath(path, "tags"))
		if err != nil {
			return note, err
		}
	}
	if value, ok := obj["alternatives"]; ok {
		note.Alternatives, err = readStringArray(value, joinPath(path, "alternatives"))
		if err != nil {
			return note, err
		}
	}
	if value, ok := obj["fields"]; ok {
		if _, err := readObject(value, joinPath(path, "fields")); err != nil {
			return note, err
		}
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		if err := decoder.Decode(&note.Fields); err != nil || note.Fields == nil {
			return note, parseError(ErrInvalidStructure, joinPath(path, "fields"))
		}
	}
	return note, nil
}

func readObject(raw json.RawMessage, path string) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, parseError(ErrInvalidStructure, path)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil || obj == nil {
		return nil, parseError(ErrInvalidStructure, path)
	}
	return obj, nil
}

func readArray(raw json.RawMessage, path string) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, parseError(ErrInvalidStructure, path)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil || items == nil {
		return nil, parseError(ErrInvalidStructure, path)
	}
	return items, nil
}

func readString(raw json.RawMessage, path string) (string, error) {
	if !isJSONString(raw) {
		return "", parseError(ErrInvalidStructure, path)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", parseError(ErrInvalidStructure, path)
	}
	return value, nil
}

func readStringArray(raw json.RawMessage, path string) ([]string, error) {
	items, err := readArray(raw, path)
	if err != nil {
		return nil, err
	}
	values := make([]string, 0, len(items))
	for i, item := range items {
		value, err := readString(item, fmt.Sprintf("%s[%d]", path, i))
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func readInteger(raw json.RawMessage, path string) (int, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] == '"' || trimmed[0] == '{' || trimmed[0] == '[' || bytes.Equal(trimmed, []byte("null")) {
		return 0, parseError(ErrInvalidStructure, path)
	}
	var value int
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return 0, parseError(ErrInvalidStructure, path)
	}
	return value, nil
}

func rejectUnknown(obj map[string]json.RawMessage, path string, allowed ...string) error {
	known := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		known[key] = struct{}{}
	}
	for key := range obj {
		if _, ok := known[key]; !ok {
			return parseError(ErrInvalidStructure, path)
		}
	}
	return nil
}

func isJSONString(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '"'
}

func joinPath(base, field string) string {
	if base == "" {
		return field
	}
	return base + "." + field
}
