// Package contentimport implements owner-scoped import, history, and export.
package contentimport

import "time"

// Counts groups note import results by whether they need to be inserted or updated.
type Counts struct {
	New       int64 `json:"new"`
	Changed   int64 `json:"changed"`
	Unchanged int64 `json:"unchanged"`
}

// DeckCounts reports how many imported decks are new, changed, or unchanged.
type DeckCounts struct {
	New       int64 `json:"new"`
	Changed   int64 `json:"changed"`
	Unchanged int64 `json:"unchanged"`
}

// PreviewDeck is one deck in the proposed import tree.
type PreviewDeck struct {
	ExternalID string        `json:"external_id,omitempty"`
	Name       string        `json:"name"`
	Status     string        `json:"status"`
	Counts     Counts        `json:"counts"`
	Warnings   []string      `json:"warnings"`
	Decks      []PreviewDeck `json:"decks"`
}

// Summary is the persisted and public import summary.
type Summary struct {
	Source     string        `json:"source"`
	Tree       []PreviewDeck `json:"tree"`
	Counts     Counts        `json:"counts"`
	DeckCounts DeckCounts    `json:"deck_counts"`
	Warnings   []string      `json:"warnings"`
}

// PreviewResult is returned by preview and apply endpoints.
type PreviewResult struct {
	BatchID string `json:"batch_id"`
	Adapter string `json:"adapter"`
	Status  string `json:"status"`
	Summary
}

// ApplyInput contains choices made after the user reviews an import preview.
type ApplyInput struct {
	LangTarget   string `json:"lang_target"`
	LangNative   string `json:"lang_native"`
	ParentDeckID *int64 `json:"parent_deck_id"`
	OnMissing    string `json:"on_missing"`
}

// Batch is a history entry. Raw source contents are never returned.
type Batch struct {
	ID        string     `json:"id"`
	Adapter   string     `json:"adapter"`
	Filename  *string    `json:"filename"`
	SHA256    string     `json:"sha256"`
	Status    string     `json:"status"`
	Summary   Summary    `json:"summary"`
	CreatedAt time.Time  `json:"created_at"`
	AppliedAt *time.Time `json:"applied_at"`
}

// Export is a completed download, ready for the HTTP handler to write.
type Export struct {
	Filename    string
	ContentType string
	Body        []byte
}
