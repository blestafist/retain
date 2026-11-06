package adapters

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pestit/retain/internal/importer"
)

func TestCSVAdapterExactHeaderQuotingMultilineAndTags(t *testing.T) {
	input := "term;translation;example;tags\r\n" +
		`"word; phrase";"translation; variant";"first line` + "\n" +
		`second line";" noun, chapter, , verb "`
	adapter := CSVAdapter{}
	if got := adapter.Name(); got != "csv" {
		t.Fatalf("Name() = %q, want csv", got)
	}
	bundle, err := adapter.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if bundle.Format != "retain/v1" || bundle.Source != "csv" || len(bundle.Decks) != 1 || bundle.Decks[0].Name != "CSV import" {
		t.Fatalf("bundle header/deck = %#v", bundle)
	}
	if len(bundle.Decks[0].Notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(bundle.Decks[0].Notes))
	}
	note := bundle.Decks[0].Notes[0]
	if note.Term != "word; phrase" || note.Translation != "translation; variant" || note.Example != "first line\nsecond line" {
		t.Fatalf("parsed note fields = %#v", note)
	}
	if !reflect.DeepEqual(note.Tags, []string{"noun", "chapter", "verb"}) {
		t.Fatalf("tags = %#v, want trimmed nonempty tags", note.Tags)
	}
}

func TestCSVAdapterWithoutHeaderAndEmptyOptionalFields(t *testing.T) {
	bundle, err := (CSVAdapter{}).Parse([]byte("word;translation;;\nsecond;another;; , , \n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(bundle.Decks[0].Notes) != 2 {
		t.Fatalf("notes = %d, want 2", len(bundle.Decks[0].Notes))
	}
	for i, note := range bundle.Decks[0].Notes {
		if note.Example != "" || note.Tags != nil {
			t.Errorf("notes[%d] optional fields = (%q, %#v), want empty", i, note.Example, note.Tags)
		}
	}
}

func TestCSVHeaderRequiresExactFieldsInOrder(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantNotes int
		wantTerm  string
	}{
		{name: "exact header", input: "term;translation;example;tags\nword;meaning;;", wantNotes: 1, wantTerm: "word"},
		{name: "spaces and case are data", input: " TERM ;Translation;example; TAGS \nword;meaning;;", wantNotes: 2, wantTerm: " TERM "},
		{name: "case mismatch is data", input: "Term;translation;example;tags\nword;meaning;;", wantNotes: 2, wantTerm: "Term"},
		{name: "wrong order is data", input: "translation;term;example;tags\nword;meaning;;", wantNotes: 2, wantTerm: "translation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundle, err := (CSVAdapter{}).Parse([]byte(tt.input))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got := len(bundle.Decks[0].Notes); got != tt.wantNotes {
				t.Fatalf("notes = %d, want %d", got, tt.wantNotes)
			}
			if got := bundle.Decks[0].Notes[0].Term; got != tt.wantTerm {
				t.Fatalf("first term = %q, want %q", got, tt.wantTerm)
			}
		})
	}
}

func TestCSVAdapterRejectsInvalidUTF8(t *testing.T) {
	input := append([]byte("word;trans"), 0xff)
	if (CSVAdapter{}).Detect(input) {
		t.Fatal("Detect() = true for invalid UTF-8")
	}
	_, err := (CSVAdapter{}).Parse(input)
	if !errors.Is(err, ErrInvalidCSV) {
		t.Fatalf("Parse() error = %v, want ErrInvalidCSV", err)
	}
	if strings.ContainsRune(err.Error(), '\uFFFD') {
		t.Fatalf("error contains replacement character: %v", err)
	}
}

func TestCSVAdapterSkipsBlankRecordsConsistently(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantDetect bool
		wantNotes  int
		wantErr    error
	}{
		{name: "blank row then data", input: "  ; ; ; \nword;translation;;", wantDetect: true, wantNotes: 1},
		{name: "blank row then exact header and data", input: "  ; ; ; \nterm;translation;example;tags\nword;translation;;", wantDetect: true, wantNotes: 1},
		{name: "ragged blank row then data", input: " ; \nword;translation;;", wantDetect: true, wantNotes: 1},
		{name: "only blank rows", input: "  ; ; ; ", wantErr: ErrInvalidCSVStructure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (CSVAdapter{}).Detect([]byte(tt.input)); got != tt.wantDetect {
				t.Fatalf("Detect() = %v, want %v", got, tt.wantDetect)
			}
			bundle, err := (CSVAdapter{}).Parse([]byte(tt.input))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Parse() error = %v, want errors.Is(_, %v)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got := len(bundle.Decks[0].Notes); got != tt.wantNotes {
				t.Fatalf("notes = %d, want %d", got, tt.wantNotes)
			}
		})
	}
}

func TestCSVAdapterMatchesECMAScriptWhitespace(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantDetect bool
		wantErr    error
		wantTerm   string
	}{
		{name: "U+0085 is a valid term and translation", input: "\u0085;\u0085;;", wantDetect: true, wantTerm: "\u0085"},
		{name: "U+FEFF required term is rejected", input: "\ufeff;translation;;", wantErr: importer.ErrMissingRequiredField},
		{name: "U+FEFF-only row is blank", input: "\ufeff;\ufeff;;", wantErr: ErrInvalidCSVStructure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (CSVAdapter{}).Detect([]byte(tt.input)); got != tt.wantDetect {
				t.Fatalf("Detect() = %v, want %v", got, tt.wantDetect)
			}
			bundle, err := (CSVAdapter{}).Parse([]byte(tt.input))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Parse() error = %v, want errors.Is(_, %v)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got := len(bundle.Decks[0].Notes); got != 1 {
				t.Fatalf("notes = %d, want 1", got)
			}
			if got := bundle.Decks[0].Notes[0].Term; got != tt.wantTerm {
				t.Fatalf("term = %q, want %q", got, tt.wantTerm)
			}
		})
	}
}

func TestCSVAdapterRejectsMalformedAndRaggedRecordsWithoutEchoingInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  error
	}{
		{name: "malformed quoting", input: "\"RAW-SECRET;translation;;", want: ErrInvalidCSV},
		{name: "too few fields", input: "term;translation;example", want: ErrInvalidCSVStructure},
		{name: "too many fields", input: "term;translation;example;tags;extra", want: ErrInvalidCSVStructure},
		{name: "ragged data row", input: "term;translation;example;tags\nword;translation;", want: ErrInvalidCSVStructure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (CSVAdapter{}).Parse([]byte(tt.input))
			if err == nil {
				t.Fatal("Parse() error = nil")
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("Parse() error = %v, want errors.Is(_, %v)", err, tt.want)
			}
			if strings.Contains(err.Error(), "RAW-SECRET") {
				t.Fatalf("error leaked raw content: %v", err)
			}
		})
	}
}

func TestCSVAdapterUsesBundleRequiredFieldValidation(t *testing.T) {
	for _, input := range []string{
		";translation;;",
		"term; \t ;;",
	} {
		_, err := (CSVAdapter{}).Parse([]byte(input))
		if err == nil {
			t.Errorf("Parse(%q) error = nil", input)
			continue
		}
		if !errors.Is(err, importer.ErrMissingRequiredField) {
			t.Errorf("Parse(%q) error = %v, want ErrMissingRequiredField", input, err)
		}
		if strings.Contains(err.Error(), input) {
			t.Errorf("error leaked raw content: %v", err)
		}
	}
}

func TestCSVAdapterDetect(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "exact header", raw: "\nterm;translation;example;tags\n", want: true},
		{name: "near header is data", raw: " TERM ;Translation;example; TAGS \n", want: true},
		{name: "data row", raw: "word;translation;;", want: true},
		{name: "blank then data", raw: "\n  ; ; ; \nword;translation;;", want: true},
		{name: "not enough columns", raw: "word;translation", want: false},
		{name: "empty", raw: "\n", want: false},
		{name: "ragged row", raw: "word;translation;example", want: false},
		{name: "malformed quoting", raw: "\"word;translation;;", want: false},
		{name: "not CSV", raw: "arbitrary text", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (CSVAdapter{}).Detect([]byte(tt.raw)); got != tt.want {
				t.Fatalf("Detect() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAllRegistersNativeAndCSVAdapters(t *testing.T) {
	registered := All()
	if len(registered) != 2 || registered[0].Name() != "retain" || registered[1].Name() != "csv" {
		t.Fatalf("registered adapters = %#v", registered)
	}
	registered[0] = CSVAdapter{}
	if All()[0].Name() != "retain" {
		t.Fatal("All() exposed mutable registry state")
	}
}
