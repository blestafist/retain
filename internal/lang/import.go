package lang

import (
	"reflect"
	"strings"
	"unicode"
)

// ImportNote is the language-independent subset of a note used during import
// enrichment. Fields remains open-ended so language-specific metadata can be
// preserved without coupling this package to the importer.
type ImportNote struct {
	Term         string
	Translation  string
	Notes        string
	Alternatives []string
	Fields       map[string]any
}

// EnrichImportNote deterministically extracts language metadata and answer
// variants from an imported note. It does not modify input or its maps/slices.
func EnrichImportNote(langTarget string, input ImportNote) ImportNote {
	result := input
	result.Alternatives = cloneStrings(input.Alternatives)
	result.Fields = cloneFields(input.Fields)

	translation, parentheticalNotes := removeParentheticalNotes(input.Translation)
	variants := splitTranslation(translation)
	if len(variants) > 0 {
		result.Translation = variants[0]
		result.Alternatives = mergeAlternatives(variants[0], result.Alternatives, variants[1:])
	} else {
		result.Translation = ""
	}
	if len(parentheticalNotes) > 0 {
		appendNotes(&result.Notes, parentheticalNotes)
	}

	if langTarget != "de" {
		return result
	}

	words := strings.Fields(result.Term)
	if len(words) == 0 {
		return result
	}
	if marker, ok := trailingGoverningMarker(words); ok {
		setIfEmpty(&result.Fields, "governs", marker)
		words = words[:len(words)-1]
	}

	article, hasInitialArticle := initialArticle(words[0])
	if hasInitialArticle && len(words) > 1 && startsWithUppercase(words[1]) {
		setIfEmpty(&result.Fields, "pos", "noun")
		setIfEmpty(&result.Fields, "article", article)
	}

	if strings.HasPrefix(result.Term, "sich ") {
		setIfEmpty(&result.Fields, "reflexive", true)
	}
	if len(words) >= 3 && !hasInitialArticle {
		setIfEmpty(&result.Fields, "kind", "phrase")
	}

	return result
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	clone := make([]string, len(values))
	copy(clone, values)
	return clone
}

func cloneFields(fields map[string]any) map[string]any {
	if fields == nil {
		return nil
	}
	clone := make(map[string]any, len(fields))
	for key, value := range fields {
		if value == nil {
			clone[key] = nil
			continue
		}
		clone[key] = cloneMutableValue(reflect.ValueOf(value)).Interface()
	}
	return clone
}

func cloneMutableValue(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.New(value.Type()).Elem()
		cloned.Set(cloneMutableValue(value.Elem()))
		return cloned
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			cloned.SetMapIndex(iter.Key(), cloneMutableValue(iter.Value()))
		}
		return cloned
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			cloned.Index(i).Set(cloneMutableValue(value.Index(i)))
		}
		return cloned
	case reflect.Array:
		cloned := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			cloned.Index(i).Set(cloneMutableValue(value.Index(i)))
		}
		return cloned
	default:
		return value
	}
}

func removeParentheticalNotes(translation string) (string, []string) {
	var clean strings.Builder
	notes := make([]string, 0)

	for i := 0; i < len(translation); {
		if translation[i] != '(' {
			clean.WriteByte(translation[i])
			i++
			continue
		}

		end := matchingParenthesis(translation, i)
		if end < 0 {
			clean.WriteByte(translation[i])
			i++
			continue
		}

		notes = append(notes, translation[i:end+1])
		i = end + 1
	}

	return clean.String(), notes
}

func matchingParenthesis(value string, start int) int {
	depth := 0
	for i := start; i < len(value); i++ {
		switch value[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitTranslation(translation string) []string {
	parts := strings.FieldsFunc(translation, func(r rune) bool {
		return r == ',' || r == ';'
	})
	if len(parts) == 0 {
		return nil
	}

	variants := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.TrimSuffix(part, ".")
		part = strings.TrimSpace(part)
		if part != "" {
			variants = append(variants, part)
		}
	}
	return variants
}

func mergeAlternatives(primary string, existing, additions []string) []string {
	if len(existing) == 0 && len(additions) == 0 {
		return cloneStrings(existing)
	}

	merged := make([]string, 0, len(existing)+len(additions))
	seen := map[string]struct{}{primary: {}}

	for _, value := range existing {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	for _, value := range additions {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	return merged
}

func appendNotes(notes *string, additions []string) {
	if len(additions) == 0 {
		return
	}
	if *notes != "" {
		*notes += "\n"
	}
	*notes += strings.Join(additions, "\n")
}

func trailingGoverningMarker(words []string) (string, bool) {
	if len(words) < 2 || !isLowercaseToken(words[len(words)-2]) {
		return "", false
	}

	switch words[len(words)-1] {
	case "A", "D", "G":
		return words[len(words)-1], true
	default:
		return "", false
	}
}

func isLowercaseToken(token string) bool {
	hasLetter := false
	for _, r := range token {
		if !unicode.IsLetter(r) {
			continue
		}
		hasLetter = true
		if !unicode.IsLower(r) {
			return false
		}
	}
	return hasLetter
}

func initialArticle(word string) (string, bool) {
	article := strings.ToLower(word)
	switch article {
	case "der", "die", "das":
		return article, true
	default:
		return "", false
	}
}

func startsWithUppercase(word string) bool {
	for _, r := range word {
		return unicode.IsUpper(r)
	}
	return false
}

func setIfEmpty(fields *map[string]any, key string, value any) {
	if *fields == nil {
		*fields = make(map[string]any)
	}
	current, exists := (*fields)[key]
	if !exists || current == nil {
		(*fields)[key] = value
		return
	}
	if text, ok := current.(string); ok && strings.TrimSpace(text) == "" {
		(*fields)[key] = value
	}
}
