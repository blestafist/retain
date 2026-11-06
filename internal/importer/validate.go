package importer

import (
	"strconv"
	"unicode"
)

// ValidateBundle checks required fields shared by all adapters.
func ValidateBundle(bundle *Bundle) error {
	if bundle == nil {
		return parseError(ErrInvalidStructure, "")
	}
	for i := range bundle.Decks {
		if err := validateDeck(bundle.Decks[i], "decks["+strconv.Itoa(i)+"]"); err != nil {
			return err
		}
	}
	return nil
}

func validateDeck(deck Deck, path string) error {
	if !hasECMAScriptNonWhitespace(deck.Name) {
		return parseError(ErrMissingRequiredField, joinPath(path, "name"))
	}
	for i, note := range deck.Notes {
		notePath := joinPath(path, "notes["+strconv.Itoa(i)+"]")
		if !hasECMAScriptNonWhitespace(note.Term) {
			return parseError(ErrMissingRequiredField, joinPath(notePath, "term"))
		}
		if !hasECMAScriptNonWhitespace(note.Translation) {
			return parseError(ErrMissingRequiredField, joinPath(notePath, "translation"))
		}
	}
	for i := range deck.Decks {
		if err := validateDeck(deck.Decks[i], joinPath(path, "decks["+strconv.Itoa(i)+"]")); err != nil {
			return err
		}
	}
	return nil
}

// hasECMAScriptNonWhitespace matches an ECMAScript regular-expression \S:
// WhiteSpace (including every Unicode Zs character) plus LineTerminator are
// excluded, while characters such as U+0085 are not whitespace here.
func hasECMAScriptNonWhitespace(value string) bool {
	for _, r := range value {
		if !isECMAScriptWhitespace(r) {
			return true
		}
	}
	return false
}

func isECMAScriptWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\ufeff', '\u2028', '\u2029':
		return true
	default:
		return unicode.Is(unicode.Zs, r)
	}
}
