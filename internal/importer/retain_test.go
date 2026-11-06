package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRetainAdapterValidNestedBundle(t *testing.T) {
	input := []byte(`{
		"format":"retain/v1",
		"source":"school-wordsets",
		"decks":[{
			"external_id":"class:26083",
			"name":"3C 2026/2027 · Ein tolles Team 3",
			"lang_target":"de",
			"lang_native":"pl",
			"decks":[{
				"external_id":"wordset:114543",
				"name":"Kapitel 12 · Berlin",
				"expected_count":65,
				"decks":[{"name":"Nested again"}],
				"notes":[{
					"external_id":"entry:1",
					"term":"der Drachenflieger",
					"translation":"paralotnia",
					"transcription":"ˈdʁaːxn̩ˌfliːɡɐ",
					"example":"Der Drachenflieger startet.",
					"example_translation":"Paralotniarz startuje.",
					"notes":"A note",
					"tags":["noun","chapter 12"],
					"alternatives":["paralotnia","lotnia"],
					"fields":{"kind":"word","pos":"noun","article":"der","extra":{"rank":2},"score":1.25}
				}]
			}]
		}]
	}`)

	adapter := RetainAdapter{}
	if got := adapter.Name(); got != "retain" {
		t.Fatalf("Name() = %q, want retain", got)
	}
	if !adapter.Detect(input) {
		t.Fatal("Detect() = false for valid retain/v1 document")
	}
	bundle, err := adapter.Parse(input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if bundle.Format != "retain/v1" || bundle.Source != "school-wordsets" {
		t.Fatalf("bundle header = (%q, %q)", bundle.Format, bundle.Source)
	}
	if len(bundle.Decks) != 1 {
		t.Fatalf("top-level decks = %d, want 1", len(bundle.Decks))
	}
	parent := bundle.Decks[0]
	if parent.ExternalID != "class:26083" || parent.Name != "3C 2026/2027 · Ein tolles Team 3" {
		t.Fatalf("parent deck = %#v", parent)
	}
	if parent.LangTarget != "de" || parent.LangNative != "pl" {
		t.Fatalf("parent languages = (%q, %q)", parent.LangTarget, parent.LangNative)
	}
	child := parent.Decks[0]
	if child.LangTarget != "" || child.LangNative != "" {
		t.Fatalf("omitted child languages must stay empty for inheritance, got (%q, %q)", child.LangTarget, child.LangNative)
	}
	if child.ExpectedCount == nil || *child.ExpectedCount != 65 {
		t.Fatalf("child expected_count = %v, want 65", child.ExpectedCount)
	}
	if child.Decks[0].LangTarget != "" || child.Decks[0].LangNative != "" {
		t.Fatalf("omitted grandchild languages must stay empty for inheritance, got (%q, %q)", child.Decks[0].LangTarget, child.Decks[0].LangNative)
	}
	if len(child.Notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(child.Notes))
	}
	note := child.Notes[0]
	if note.ExternalID != "entry:1" || note.Term != "der Drachenflieger" || note.Translation != "paralotnia" {
		t.Fatalf("note identity = %#v", note)
	}
	if note.Transcription != "ˈdʁaːxn̩ˌfliːɡɐ" || note.Example != "Der Drachenflieger startet." || note.ExampleTranslation != "Paralotniarz startuje." || note.Notes != "A note" {
		t.Fatalf("note optional strings were not preserved: %#v", note)
	}
	if !reflect.DeepEqual(note.Tags, []string{"noun", "chapter 12"}) || !reflect.DeepEqual(note.Alternatives, []string{"paralotnia", "lotnia"}) {
		t.Fatalf("note lists were not preserved: tags=%#v alternatives=%#v", note.Tags, note.Alternatives)
	}
	if note.Fields["kind"] != "word" || note.Fields["extra"].(map[string]any)["rank"] != json.Number("2") || note.Fields["score"] != json.Number("1.25") {
		t.Fatalf("free-form fields were not preserved: %#v", note.Fields)
	}
}

func TestParseRetainAllowsOmittedOptionalData(t *testing.T) {
	bundle, err := ParseRetain([]byte(`{"format":"retain/v1","decks":[{"name":"Deck","notes":[{"term":"term","translation":"translation"}]}]}`))
	if err != nil {
		t.Fatalf("ParseRetain() error = %v", err)
	}
	deck := bundle.Decks[0]
	if deck.ExpectedCount != nil || deck.LangTarget != "" || deck.LangNative != "" {
		t.Fatalf("omitted deck options = %#v", deck)
	}
	if note := deck.Notes[0]; note.Transcription != "" || note.Example != "" || note.ExampleTranslation != "" || note.Notes != "" || note.Tags != nil || note.Alternatives != nil || note.Fields != nil {
		t.Fatalf("omitted note options = %#v", note)
	}
}

func TestParseRetainErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  error
	}{
		{name: "malformed JSON", input: `{"format":`, want: ErrInvalidJSON},
		{name: "trailing JSON value", input: `{"format":"retain/v1"} {}`, want: ErrInvalidJSON},
		{name: "trailing garbage", input: `{"format":"retain/v1"} source-secret`, want: ErrInvalidJSON},
		{name: "root is not object", input: `[]`, want: ErrInvalidStructure},
		{name: "unknown root key", input: `{"format":"retain/v1","secret_key":"body-secret"}`, want: ErrInvalidStructure},
		{name: "unknown deck key", input: `{"format":"retain/v1","decks":[{"name":"Deck","unexpected":true}]}`, want: ErrInvalidStructure},
		{name: "unknown note key", input: `{"format":"retain/v1","decks":[{"name":"Deck","notes":[{"term":"word","translation":"слово","unexpected":true}]}]}`, want: ErrInvalidStructure},
		{name: "missing format", input: `{}`, want: ErrMissingRequiredField},
		{name: "wrong format", input: `{"format":"retain/v2"}`, want: ErrUnsupportedFormat},
		{name: "missing deck name", input: `{"format":"retain/v1","decks":[{}]}`, want: ErrMissingRequiredField},
		{name: "empty deck name", input: `{"format":"retain/v1","decks":[{"name":""}]}`, want: ErrMissingRequiredField},
		{name: "whitespace deck name", input: `{"format":"retain/v1","decks":[{"name":"  \t"}]}`, want: ErrMissingRequiredField},
		{name: "missing note term", input: `{"format":"retain/v1","decks":[{"name":"Deck","notes":[{"translation":"слово"}]}]}`, want: ErrMissingRequiredField},
		{name: "empty note term", input: `{"format":"retain/v1","decks":[{"name":"Deck","notes":[{"term":"","translation":"слово"}]}]}`, want: ErrMissingRequiredField},
		{name: "missing translation", input: `{"format":"retain/v1","decks":[{"name":"Deck","notes":[{"term":"word"}]}]}`, want: ErrMissingRequiredField},
		{name: "empty translation", input: `{"format":"retain/v1","decks":[{"name":"Deck","notes":[{"term":"word","translation":"  "}]}]}`, want: ErrMissingRequiredField},
		{name: "invalid target language", input: `{"format":"retain/v1","decks":[{"name":"Deck","lang_target":"pl"}]}`, want: ErrInvalidLanguage},
		{name: "invalid native language", input: `{"format":"retain/v1","decks":[{"name":"Deck","lang_native":"de"}]}`, want: ErrInvalidLanguage},
		{name: "empty specified language", input: `{"format":"retain/v1","decks":[{"name":"Deck","lang_target":""}]}`, want: ErrInvalidLanguage},
		{name: "negative expected count", input: `{"format":"retain/v1","decks":[{"name":"Deck","expected_count":-1}]}`, want: ErrNegativeExpectedCount},
		{name: "null optional string", input: `{"format":"retain/v1","source":null}`, want: ErrInvalidStructure},
		{name: "null array", input: `{"format":"retain/v1","decks":null}`, want: ErrInvalidStructure},
		{name: "non-string tags", input: `{"format":"retain/v1","decks":[{"name":"Deck","notes":[{"term":"word","translation":"слово","tags":[1]}]}]}`, want: ErrInvalidStructure},
		{name: "fields must be object", input: `{"format":"retain/v1","decks":[{"name":"Deck","notes":[{"term":"word","translation":"слово","fields":[]}]}]}`, want: ErrInvalidStructure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRetain([]byte(tt.input))
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseRetain() error = %v, want errors.Is(_, %v)", err, tt.want)
			}
			if err == nil {
				t.Fatal("ParseRetain() error = nil")
			}
			var parseErr *ParseError
			if !errors.As(err, &parseErr) {
				t.Fatalf("error type = %T, want *ParseError", err)
			}
			if strings.Contains(err.Error(), "body-secret") || strings.Contains(err.Error(), "source-secret") {
				t.Fatalf("error leaked input content: %v", err)
			}
		})
	}
}

func TestRetainDetect(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "native", raw: `{"format":"retain/v1"}`, want: true},
		{name: "valid JSON other format", raw: `{"format":"other"}`},
		{name: "missing format", raw: `{"decks":[]}`},
		{name: "malformed", raw: `{"format":`},
		{name: "trailing value", raw: `{"format":"retain/v1"} {}`},
		{name: "not object", raw: `null`},
	}

	adapter := RetainAdapter{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adapter.Detect([]byte(tt.raw)); got != tt.want {
				t.Fatalf("Detect() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseRetainRejectsInvalidUTF8(t *testing.T) {
	input := append([]byte(`{"format":"retain/v1","source":"`), 0xff)
	input = append(input, []byte(`"}`)...)

	if (RetainAdapter{}).Detect(input) {
		t.Fatal("Detect() = true for invalid UTF-8")
	}
	_, err := ParseRetain(input)
	if !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("ParseRetain() error = %v, want ErrInvalidJSON", err)
	}
	if strings.ContainsRune(err.Error(), '\uFFFD') {
		t.Fatalf("error unexpectedly contains a replacement character: %v", err)
	}
}

func TestParseRetainRejectsUnpairedEscapedUTF16Surrogates(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "lone high surrogate", input: `{"format":"retain/v1","source":"\uD800"}`},
		{name: "lone low surrogate", input: `{"format":"retain/v1","source":"\uDC00"}`},
		{name: "high followed by non-low", input: `{"format":"retain/v1","source":"\uD800\u0041"}`},
		{name: "high followed by another high", input: `{"format":"retain/v1","source":"\uD800\uD801"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte(tt.input)
			if (RetainAdapter{}).Detect(raw) {
				t.Fatal("Detect() = true for invalid escaped UTF-16")
			}
			_, err := ParseRetain(raw)
			if !errors.Is(err, ErrInvalidJSON) {
				t.Fatalf("ParseRetain() error = %v, want ErrInvalidJSON", err)
			}
			if strings.ContainsRune(err.Error(), '\uFFFD') {
				t.Fatalf("error contains replacement character: %v", err)
			}
		})
	}
}

func TestParseRetainAcceptsValidEscapedUTF16SurrogatePair(t *testing.T) {
	input := []byte(`{"format":"retain/v1","decks":[{"name":"Deck","notes":[{"term":"\uD83D\uDE00","translation":"smile"}]}]}`)
	bundle, err := ParseRetain(input)
	if err != nil {
		t.Fatalf("ParseRetain() error = %v", err)
	}
	if got := bundle.Decks[0].Notes[0].Term; got != "😀" {
		t.Fatalf("decoded term = %q, want 😀", got)
	}

	// A backslash escaped as data is not a UTF-16 escape sequence.
	input = []byte(`{"format":"retain/v1","decks":[{"name":"Deck","notes":[{"term":"\\uD800","translation":"literal"}]}]}`)
	if _, err := ParseRetain(input); err != nil {
		t.Fatalf("ParseRetain() rejected a literal backslash-u sequence: %v", err)
	}
}

func TestParseRetainExpectedCountMatchesJSONSchemaIntegerSemantics(t *testing.T) {
	tests := []struct {
		name    string
		number  string
		want    int64
		wantErr error
	}{
		{name: "integer", number: "1", want: 1},
		{name: "decimal integer", number: "1.0", want: 1},
		{name: "exponent integer", number: "1e0", want: 1},
		{name: "decimal exponent integer", number: "10.00e-1", want: 1},
		{name: "leading decimal zeros", number: "0.001e3", want: 1},
		{name: "exponent moves decimal point", number: "10.05e2", want: 1005},
		{name: "zero with exponent", number: "0e999999999999999999999999", want: 0},
		{name: "negative zero", number: "-0.0", want: 0},
		{name: "maximum int64", number: "9223372036854775807", want: 9223372036854775807},
		{name: "maximum decimal integer", number: "92233720368547758070e-1", want: 9223372036854775807},
		{name: "maximum decimal notation", number: "9223372036854775807.0", want: 9223372036854775807},
		{name: "fractional decimal", number: "1.01", wantErr: ErrInvalidStructure},
		{name: "fractional exponent", number: "1e-1", wantErr: ErrInvalidStructure},
		{name: "above maximum int64", number: "9223372036854775808", wantErr: ErrInvalidStructure},
		{name: "above maximum int64 exponent", number: "9223372036854775808e0", wantErr: ErrInvalidStructure},
		{name: "above maximum int64 decimal", number: "9223372036854775808.0", wantErr: ErrInvalidStructure},
		{name: "huge exponent", number: "1e999999999999999999999999", wantErr: ErrInvalidStructure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(`{"format":"retain/v1","decks":[{"name":"Deck","expected_count":` + tt.number + `}]}`)
			bundle, err := ParseRetain(input)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ParseRetain() error = %v, want errors.Is(_, %v)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRetain() error = %v", err)
			}
			got := bundle.Decks[0].ExpectedCount
			if got == nil || *got != tt.want {
				t.Fatalf("expected_count = %v, want %d", got, tt.want)
			}
		})
	}
}

func TestValidateBundleRequiredTextMatchesECMAScriptWhitespace(t *testing.T) {
	fields := []struct {
		name string
		set  func(*Deck, string)
	}{
		{name: "deck name", set: func(deck *Deck, value string) { deck.Name = value }},
		{name: "note term", set: func(deck *Deck, value string) { deck.Notes[0].Term = value }},
		{name: "note translation", set: func(deck *Deck, value string) { deck.Notes[0].Translation = value }},
	}
	values := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "U+0085 is non-whitespace", value: "\u0085"},
		{name: "FEFF is whitespace", value: "\uFEFF", wantErr: true},
		{name: "ordinary whitespace only", value: " \t\n\r\v\f", wantErr: true},
	}

	for _, field := range fields {
		for _, value := range values {
			t.Run(field.name+"/"+value.name, func(t *testing.T) {
				deck := Deck{Name: "Deck", Notes: []Note{{Term: "word", Translation: "meaning"}}}
				field.set(&deck, value.value)
				err := ValidateBundle(&Bundle{Format: retainFormat, Decks: []Deck{deck}})
				if value.wantErr {
					if !errors.Is(err, ErrMissingRequiredField) {
						t.Fatalf("ValidateBundle() error = %v, want ErrMissingRequiredField", err)
					}
				} else if err != nil {
					t.Fatalf("ValidateBundle() error = %v, want nil", err)
				}
			})
		}
	}
}

func TestRetainV1SchemaIsEmbeddedJSON(t *testing.T) {
	if !json.Valid(RetainV1Schema) {
		t.Fatal("embedded retain/v1 schema is not valid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(RetainV1Schema))
	decoder.UseNumber()
	var schema map[string]any
	if err := decoder.Decode(&schema); err != nil {
		t.Fatalf("unmarshal embedded schema: %v", err)
	}
	if schema["title"] != "Retain v1 exchange format" {
		t.Fatalf("schema title = %v", schema["title"])
	}
	defs := schema["$defs"].(map[string]any)
	deck := defs["deck"].(map[string]any)
	deckProperties := deck["properties"].(map[string]any)
	expectedCount := deckProperties["expected_count"].(map[string]any)
	if expectedCount["type"] != "integer" || expectedCount["maximum"] != json.Number("9223372036854775807") {
		t.Fatalf("expected_count schema = %#v, want integer <= MaxInt64", expectedCount)
	}
	for _, property := range []struct {
		object   string
		property string
	}{
		{object: "deck", property: "name"},
		{object: "note", property: "term"},
		{object: "note", property: "translation"},
	} {
		object := defs[property.object].(map[string]any)
		properties := object["properties"].(map[string]any)
		text := properties[property.property].(map[string]any)
		if text["pattern"] != `\S` {
			t.Errorf("%s.%s pattern = %v, want ECMAScript \\S", property.object, property.property, text["pattern"])
		}
	}
}
