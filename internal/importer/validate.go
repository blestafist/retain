package importer

import (
	"strconv"
	"strings"
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
	if strings.TrimSpace(deck.Name) == "" {
		return parseError(ErrMissingRequiredField, joinPath(path, "name"))
	}
	for i, note := range deck.Notes {
		notePath := joinPath(path, "notes["+strconv.Itoa(i)+"]")
		if strings.TrimSpace(note.Term) == "" {
			return parseError(ErrMissingRequiredField, joinPath(notePath, "term"))
		}
		if strings.TrimSpace(note.Translation) == "" {
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
