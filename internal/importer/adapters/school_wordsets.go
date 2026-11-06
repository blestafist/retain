package adapters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pestit/retain/internal/importer"
)

// SchoolWordsetsAdapter converts the school's classes/wordsets export into
// nested retain/v1 decks.
type SchoolWordsetsAdapter struct{}

var _ importer.Adapter = SchoolWordsetsAdapter{}

func (SchoolWordsetsAdapter) Name() string { return "school-wordsets" }

// Detect reports whether raw is valid school-wordsets JSON with at least one
// recognizable class and wordset.
func (SchoolWordsetsAdapter) Detect(raw []byte) bool {
	bundle, err := ParseSchoolWordsets(raw)
	if err != nil {
		return false
	}
	for _, class := range bundle.Decks {
		if len(class.Decks) > 0 {
			return true
		}
	}
	return false
}

// Parse converts a school-wordsets document into a retain/v1 Bundle.
func (SchoolWordsetsAdapter) Parse(raw []byte) (*importer.Bundle, error) {
	return ParseSchoolWordsets(raw)
}

// ParseSchoolWordsets parses the school's classes/wordsets export.
func ParseSchoolWordsets(raw []byte) (*importer.Bundle, error) {
	root, err := schoolWordsetsRoot(raw)
	if err != nil {
		return nil, err
	}

	classesRaw, ok := root["classes"]
	if !ok {
		return nil, schoolWordsetsError(importer.ErrMissingRequiredField, "classes")
	}
	classes, err := schoolWordsetsArray(classesRaw, "classes")
	if err != nil {
		return nil, err
	}

	bundle := &importer.Bundle{
		Format: "retain/v1",
		Source: "school-wordsets",
		Decks:  make([]importer.Deck, 0, len(classes)),
	}
	for i, rawClass := range classes {
		path := fmt.Sprintf("classes[%d]", i)
		classObject, err := schoolWordsetsObject(rawClass, path)
		if err != nil {
			return nil, err
		}

		classRaw, ok := classObject["class"]
		if !ok {
			return nil, schoolWordsetsError(importer.ErrMissingRequiredField, path+".class")
		}
		classInfo, err := schoolWordsetsObject(classRaw, path+".class")
		if err != nil {
			return nil, err
		}
		title, err := schoolWordsetsRequiredString(classInfo, "title", path+".class")
		if err != nil {
			return nil, err
		}
		groupID, err := schoolWordsetsRequiredID(classInfo, "group_id", path+".class")
		if err != nil {
			return nil, err
		}

		wordsetsRaw, ok := classObject["wordsets"]
		if !ok {
			return nil, schoolWordsetsError(importer.ErrMissingRequiredField, path+".wordsets")
		}
		wordsets, err := schoolWordsetsArray(wordsetsRaw, path+".wordsets")
		if err != nil {
			return nil, err
		}

		parent := importer.Deck{
			Name:       title,
			ExternalID: "class:" + groupID,
			Decks:      make([]importer.Deck, 0, len(wordsets)),
		}
		for j, rawWordset := range wordsets {
			wordsetPath := fmt.Sprintf("%s.wordsets[%d]", path, j)
			wordsetObject, err := schoolWordsetsObject(rawWordset, wordsetPath)
			if err != nil {
				return nil, err
			}

			infoRaw, ok := wordsetObject["info"]
			if !ok {
				return nil, schoolWordsetsError(importer.ErrMissingRequiredField, wordsetPath+".info")
			}
			info, err := schoolWordsetsObject(infoRaw, wordsetPath+".info")
			if err != nil {
				return nil, err
			}
			id, err := schoolWordsetsRequiredID(info, "id", wordsetPath+".info")
			if err != nil {
				return nil, err
			}
			name, err := schoolWordsetsRequiredString(info, "name", wordsetPath+".info")
			if err != nil {
				return nil, err
			}

			child := importer.Deck{
				Name:       name,
				ExternalID: "wordset:" + id,
			}
			if expectedRaw, ok := info["expected_count"]; ok {
				count, err := schoolWordsetsExpectedCount(expectedRaw, wordsetPath+".info.expected_count")
				if err != nil {
					return nil, err
				}
				child.ExpectedCount = &count
			}

			entriesRaw, ok := wordsetObject["entries"]
			if !ok {
				return nil, schoolWordsetsError(importer.ErrMissingRequiredField, wordsetPath+".entries")
			}
			entries, err := schoolWordsetsArray(entriesRaw, wordsetPath+".entries")
			if err != nil {
				return nil, err
			}
			child.Notes = make([]importer.Note, 0, len(entries))
			for k, rawEntry := range entries {
				entryPath := fmt.Sprintf("%s.entries[%d]", wordsetPath, k)
				entry, err := schoolWordsetsObject(rawEntry, entryPath)
				if err != nil {
					return nil, err
				}
				word, err := schoolWordsetsRequiredString(entry, "word", entryPath)
				if err != nil {
					return nil, err
				}
				translation, err := schoolWordsetsRequiredString(entry, "translation", entryPath)
				if err != nil {
					return nil, err
				}
				child.Notes = append(child.Notes, importer.Note{Term: word, Translation: translation})
			}
			parent.Decks = append(parent.Decks, child)
		}
		bundle.Decks = append(bundle.Decks, parent)
	}

	if err := importer.ValidateBundle(bundle); err != nil {
		return nil, err
	}
	return bundle, nil
}

func schoolWordsetsRoot(raw []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) || !schoolWordsetsValidEscapedSurrogates(raw) {
		return nil, schoolWordsetsError(importer.ErrInvalidJSON, "")
	}
	var document json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, schoolWordsetsError(importer.ErrInvalidJSON, "")
	}
	return schoolWordsetsObject(document, "")
}

func schoolWordsetsObject(raw json.RawMessage, path string) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, schoolWordsetsError(importer.ErrInvalidStructure, path)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return nil, schoolWordsetsError(importer.ErrInvalidStructure, path)
	}
	return object, nil
}

func schoolWordsetsArray(raw json.RawMessage, path string) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, schoolWordsetsError(importer.ErrInvalidStructure, path)
	}
	var values []json.RawMessage
	if err := json.Unmarshal(trimmed, &values); err != nil || values == nil {
		return nil, schoolWordsetsError(importer.ErrInvalidStructure, path)
	}
	return values, nil
}

func schoolWordsetsRequiredString(object map[string]json.RawMessage, field, path string) (string, error) {
	fieldPath := path + "." + field
	raw, ok := object[field]
	if !ok {
		return "", schoolWordsetsError(importer.ErrMissingRequiredField, fieldPath)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", schoolWordsetsError(importer.ErrInvalidStructure, fieldPath)
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", schoolWordsetsError(importer.ErrInvalidStructure, fieldPath)
	}
	if strings.TrimSpace(value) == "" {
		return "", schoolWordsetsError(importer.ErrMissingRequiredField, fieldPath)
	}
	return value, nil
}

// IDs in school exports may be strings or JSON integers. Both are preserved as
// their textual identifier when constructing retain external IDs.
func schoolWordsetsRequiredID(object map[string]json.RawMessage, field, path string) (string, error) {
	fieldPath := path + "." + field
	raw, ok := object[field]
	if !ok {
		return "", schoolWordsetsError(importer.ErrMissingRequiredField, fieldPath)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return "", schoolWordsetsError(importer.ErrInvalidStructure, fieldPath)
		}
		if strings.TrimSpace(value) == "" {
			return "", schoolWordsetsError(importer.ErrMissingRequiredField, fieldPath)
		}
		return value, nil
	}
	if schoolWordsetsIsJSONInteger(trimmed) {
		return string(trimmed), nil
	}
	return "", schoolWordsetsError(importer.ErrInvalidStructure, fieldPath)
}

func schoolWordsetsExpectedCount(raw json.RawMessage, path string) (int64, error) {
	trimmed := bytes.TrimSpace(raw)
	if !schoolWordsetsIsJSONInteger(trimmed) {
		return 0, schoolWordsetsError(importer.ErrInvalidStructure, path)
	}
	count, err := strconv.ParseInt(string(trimmed), 10, 64)
	if err != nil {
		return 0, schoolWordsetsError(importer.ErrInvalidStructure, path)
	}
	if count < 0 {
		return 0, schoolWordsetsError(importer.ErrNegativeExpectedCount, path)
	}
	return count, nil
}

func schoolWordsetsIsJSONInteger(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	start := 0
	if raw[0] == '-' {
		start = 1
	}
	if start == len(raw) {
		return false
	}
	if raw[start] == '0' {
		return start+1 == len(raw)
	}
	if raw[start] < '1' || raw[start] > '9' {
		return false
	}
	for _, digit := range raw[start+1:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func schoolWordsetsError(kind error, path string) error {
	return &importer.ParseError{Kind: kind, Path: path}
}

// encoding/json replaces unpaired UTF-16 surrogates with U+FFFD. Reject them
// so source text is never silently altered during import.
func schoolWordsetsValidEscapedSurrogates(raw []byte) bool {
	inString := false
	for i := 0; i < len(raw); i++ {
		if !inString {
			if raw[i] == '"' {
				inString = true
			}
			continue
		}
		switch raw[i] {
		case '"':
			inString = false
		case '\\':
			if i+1 >= len(raw) {
				return false
			}
			if raw[i+1] != 'u' {
				i++
				continue
			}
			unit, ok := schoolWordsetsHexCodeUnit(raw[i+2:])
			if !ok {
				return false
			}
			switch {
			case unit >= 0xD800 && unit <= 0xDBFF:
				if i+12 > len(raw) || raw[i+6] != '\\' || raw[i+7] != 'u' {
					return false
				}
				low, ok := schoolWordsetsHexCodeUnit(raw[i+8:])
				if !ok || low < 0xDC00 || low > 0xDFFF {
					return false
				}
				i += 11
			case unit >= 0xDC00 && unit <= 0xDFFF:
				return false
			default:
				i += 5
			}
		}
	}
	return true
}

func schoolWordsetsHexCodeUnit(raw []byte) (uint16, bool) {
	if len(raw) < 4 {
		return 0, false
	}
	var value uint16
	for _, digit := range raw[:4] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value |= uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value |= uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value |= uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}
