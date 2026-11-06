package lang

import (
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

	result.Term = removeGoverningMarker(result.Term, &result.Fields)
	words := strings.Fields(result.Term)
	if len(words) == 0 {
		return result
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
		clone[key] = value
	}
	return clone
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

func removeGoverningMarker(term string, fields *map[string]any) string {
	words := strings.Fields(term)
	if len(words) == 0 {
		return term
	}

	marker := strings.ToUpper(words[len(words)-1])
	switch marker {
	case "A", "D", "G":
		setIfEmpty(fields, "governs", marker)
		trimmed := strings.TrimRightFunc(term, unicode.IsSpace)
		return strings.TrimSpace(strings.TrimSuffix(trimmed, words[len(words)-1]))
	default:
		return term
	}
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
