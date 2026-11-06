// Package importer defines the source-independent retain/v1 import model.
package importer

// Bundle is the retain/v1 exchange format.
type Bundle struct {
	Format string `json:"format"`
	Source string `json:"source,omitempty"`
	Decks  []Deck `json:"decks,omitempty"`
}

// Deck is a collection of notes and optionally nested child decks.
// Empty language values are left unset so consumers can inherit them from a
// parent deck.
type Deck struct {
	ExternalID    string `json:"external_id,omitempty"`
	Name          string `json:"name"`
	LangTarget    string `json:"lang_target,omitempty"`
	LangNative    string `json:"lang_native,omitempty"`
	ExpectedCount *int64 `json:"expected_count,omitempty"`
	Decks         []Deck `json:"decks,omitempty"`
	Notes         []Note `json:"notes,omitempty"`
}

// Note is a vocabulary item. Fields may contain arbitrary JSON values.
type Note struct {
	ExternalID         string         `json:"external_id,omitempty"`
	Term               string         `json:"term"`
	Translation        string         `json:"translation"`
	Transcription      string         `json:"transcription,omitempty"`
	Example            string         `json:"example,omitempty"`
	ExampleTranslation string         `json:"example_translation,omitempty"`
	Notes              string         `json:"notes,omitempty"`
	Tags               []string       `json:"tags,omitempty"`
	Alternatives       []string       `json:"alternatives,omitempty"`
	Fields             map[string]any `json:"fields,omitempty"`
}

// Adapter converts a source format into the source-independent Bundle model.
type Adapter interface {
	Name() string
	Detect([]byte) bool
	Parse([]byte) (*Bundle, error)
}
