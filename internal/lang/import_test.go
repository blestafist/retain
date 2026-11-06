package lang

import (
	"reflect"
	"testing"
)

func TestEnrichImportNoteTranslation(t *testing.T) {
	tests := []struct {
		name  string
		lang  string
		input ImportNote
		want  ImportNote
	}{
		{
			name: "splits and cleans translation variants",
			lang: "pl",
			input: ImportNote{
				Term:        "czytać",
				Translation: " czytac, odczytywać; lektura. ",
			},
			want: ImportNote{
				Term:         "czytać",
				Translation:  "czytac",
				Alternatives: []string{"odczytywać", "lektura"},
			},
		},
		{
			name: "moves parenthetical notes and preserves existing notes",
			lang: "pl",
			input: ImportNote{
				Term:         "powstanie",
				Translation:  "powstanie (np. zbrojne), rebelia (historic; rare).",
				Notes:        "existing note",
				Alternatives: []string{"uprising"},
			},
			want: ImportNote{
				Term:         "powstanie",
				Translation:  "powstanie",
				Notes:        "existing note\n(np. zbrojne)\n(historic; rare)",
				Alternatives: []string{"uprising", "rebelia"},
			},
		},
		{
			name: "deduplicates existing and parsed alternatives",
			lang: "en",
			input: ImportNote{
				Term:         "read",
				Translation:  "primary, duplicate; second; second.",
				Alternatives: []string{"duplicate", "kept", "kept", "primary"},
			},
			want: ImportNote{
				Term:         "read",
				Translation:  "primary",
				Alternatives: []string{"duplicate", "kept", "second"},
			},
		},
		{
			name: "does not infer German fields for another language",
			lang: "en",
			input: ImportNote{
				Term:        "der Drachenflieger A",
				Translation: "hang glider.",
				Fields:      map[string]any{"custom": "value"},
			},
			want: ImportNote{
				Term:        "der Drachenflieger A",
				Translation: "hang glider",
				Fields:      map[string]any{"custom": "value"},
			},
		},
		{
			name: "enriches Russian translations without German term rules",
			lang: "ru",
			input: ImportNote{
				Term:        "der Hund A",
				Translation: "собака, пёс.",
			},
			want: ImportNote{
				Term:         "der Hund A",
				Translation:  "собака",
				Alternatives: []string{"пёс"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EnrichImportNote(tt.lang, tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("EnrichImportNote() = %#v, want %#v", got, tt.want)
			}
			if again := EnrichImportNote(tt.lang, tt.input); !reflect.DeepEqual(again, got) {
				t.Errorf("EnrichImportNote() is not deterministic: second result %#v, first %#v", again, got)
			}
		})
	}
}

func TestEnrichImportNoteGerman(t *testing.T) {
	tests := []struct {
		name  string
		input ImportNote
		want  ImportNote
	}{
		{
			name: "extracts noun article",
			input: ImportNote{
				Term:        "der Drachenflieger",
				Translation: "hang glider",
			},
			want: ImportNote{
				Term:        "der Drachenflieger",
				Translation: "hang glider",
				Fields:      map[string]any{"pos": "noun", "article": "der"},
			},
		},
		{
			name: "extracts capitalized article noun with unicode",
			input: ImportNote{
				Term:        "das Übermorgen",
				Translation: "the day after tomorrow",
			},
			want: ImportNote{
				Term:        "das Übermorgen",
				Translation: "the day after tomorrow",
				Fields:      map[string]any{"pos": "noun", "article": "das"},
			},
		},
		{
			name: "removes governing marker before phrase detection",
			input: ImportNote{
				Term:        "berichten über A",
				Translation: "to report",
			},
			want: ImportNote{
				Term:        "berichten über",
				Translation: "to report",
				Fields:      map[string]any{"governs": "A"},
			},
		},
		{
			name: "recognizes a lowercase standalone marker and normalizes it",
			input: ImportNote{
				Term:        "warten auf d",
				Translation: "to wait",
			},
			want: ImportNote{
				Term:        "warten auf",
				Translation: "to wait",
				Fields:      map[string]any{"governs": "D"},
			},
		},
		{
			name: "extracts genitive marker",
			input: ImportNote{
				Term:        "sich bewusst sein G",
				Translation: "to be aware",
			},
			want: ImportNote{
				Term:        "sich bewusst sein",
				Translation: "to be aware",
				Fields:      map[string]any{"governs": "G", "reflexive": true, "kind": "phrase"},
			},
		},
		{
			name: "marks reflexive phrase",
			input: ImportNote{
				Term:        "sich für etwas interessieren",
				Translation: "to be interested in",
			},
			want: ImportNote{
				Term:        "sich für etwas interessieren",
				Translation: "to be interested in",
				Fields:      map[string]any{"reflexive": true, "kind": "phrase"},
			},
		},
		{
			name: "marks a phrase without an initial article",
			input: ImportNote{
				Term:        "ich bin nicht einverstanden",
				Translation: "I disagree",
			},
			want: ImportNote{
				Term:        "ich bin nicht einverstanden",
				Translation: "I disagree",
				Fields:      map[string]any{"kind": "phrase"},
			},
		},
		{
			name: "does not classify a term beginning with an article as a phrase",
			input: ImportNote{
				Term:        "der alte Mann",
				Translation: "the old man",
			},
			want: ImportNote{
				Term:        "der alte Mann",
				Translation: "the old man",
			},
		},
		{
			name: "does not infer noun without capitalization",
			input: ImportNote{
				Term:        "die blaue Blume",
				Translation: "the blue flower",
			},
			want: ImportNote{
				Term:        "die blaue Blume",
				Translation: "the blue flower",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EnrichImportNote("de", tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("EnrichImportNote() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestEnrichImportNotePreservesFilledFields(t *testing.T) {
	input := ImportNote{
		Term:        "der Drachenflieger A",
		Translation: "hang glider",
		Fields: map[string]any{
			"pos":       "verb",
			"article":   "die",
			"governs":   "D",
			"kind":      "word",
			"reflexive": false,
		},
	}
	want := ImportNote{
		Term:        "der Drachenflieger",
		Translation: "hang glider",
		Fields: map[string]any{
			"pos":       "verb",
			"article":   "die",
			"governs":   "D",
			"kind":      "word",
			"reflexive": false,
		},
	}

	got := EnrichImportNote("de", input)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EnrichImportNote() = %#v, want %#v", got, want)
	}
}

func TestEnrichImportNoteFillsEmptyFields(t *testing.T) {
	input := ImportNote{
		Term:        "die Blume",
		Translation: "flower",
		Fields:      map[string]any{"pos": "", "article": nil},
	}
	want := ImportNote{
		Term:        "die Blume",
		Translation: "flower",
		Fields:      map[string]any{"pos": "noun", "article": "die"},
	}

	got := EnrichImportNote("de", input)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EnrichImportNote() = %#v, want %#v", got, want)
	}
}

func TestEnrichImportNoteDoesNotMutateInput(t *testing.T) {
	input := ImportNote{
		Term:         "der Drachenflieger A",
		Translation:  "hang glider, paraglider.",
		Notes:        "source note",
		Alternatives: []string{"paraglider", "existing"},
		Fields: map[string]any{
			"custom": "preserved",
			"nested": map[string]any{"items": []string{"unchanged"}},
		},
	}
	before := ImportNote{
		Term:         input.Term,
		Translation:  input.Translation,
		Notes:        input.Notes,
		Alternatives: cloneStrings(input.Alternatives),
		Fields: map[string]any{
			"custom": "preserved",
			"nested": map[string]any{"items": []string{"unchanged"}},
		},
	}

	got := EnrichImportNote("de", input)
	if !reflect.DeepEqual(input, before) {
		t.Errorf("EnrichImportNote mutated input: got %#v, want %#v", input, before)
	}
	got.Alternatives[0] = "changed"
	if input.Alternatives[0] != "paraglider" {
		t.Error("changing result alternatives changed the input slice")
	}
	got.Fields["custom"] = "changed"
	if input.Fields["custom"] != "preserved" {
		t.Error("changing result fields changed the input map")
	}
	if got.Fields["article"] != "der" || got.Fields["governs"] != "A" {
		t.Errorf("result fields were not enriched: %#v", got.Fields)
	}
}
