package adapters

import (
	"errors"
	"strings"
	"testing"

	"github.com/pestit/retain/internal/importer"
)

func TestSchoolWordsetsAdapterParsesNestedDecksAndPreservesOrderAndText(t *testing.T) {
	input := []byte(`{
		"metadata":{"future_field":{"ignored":true}},
		"classes":[
			{
				"class":{"title":" 3C 2026/2027 · Ein tolles Team 3 ","group_id":26083,"future_class_field":true},
				"wordsets":[
					{
						"info":{"id":114543,"name":"Kapitel 12 · Berlin","expected_count":65,"future_info_field":"ignored"},
						"entries":[
							{"word":" der Drachenflieger ","translation":"paralotnia","future_entry_field":42},
							{"word":"berichten über A","translation":"donisić o, informować o"}
						]
					},
					{
						"info":{"id":"set-2","name":"Kapitel 13"},
						"entries":[{"word":"die Reise","translation":"podróż"}]
					}
				],
				"future_class_data":[1,2,3]
			},
			{
				"class":{"title":"Second class","group_id":"group-2"},
				"wordsets":[
					{"info":{"id":"set-3","name":"Unit 1","expected_count":0},"entries":[]}
				]
			}
		]
	}`)

	adapter := SchoolWordsetsAdapter{}
	if got := adapter.Name(); got != "school-wordsets" {
		t.Fatalf("Name() = %q, want school-wordsets", got)
	}
	if !adapter.Detect(input) {
		t.Fatal("Detect() = false for valid school-wordsets export")
	}
	bundle, err := adapter.Parse(input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if bundle.Format != "retain/v1" || bundle.Source != "school-wordsets" {
		t.Fatalf("bundle header = (%q, %q)", bundle.Format, bundle.Source)
	}
	if len(bundle.Decks) != 2 {
		t.Fatalf("classes = %d, want 2", len(bundle.Decks))
	}

	first := bundle.Decks[0]
	if first.Name != " 3C 2026/2027 · Ein tolles Team 3 " || first.ExternalID != "class:26083" {
		t.Fatalf("first class deck = %#v", first)
	}
	if first.LangTarget != "" || first.LangNative != "" {
		t.Fatalf("school export languages must be omitted, got (%q, %q)", first.LangTarget, first.LangNative)
	}
	if len(first.Decks) != 2 {
		t.Fatalf("first class wordsets = %d, want 2", len(first.Decks))
	}
	chapter := first.Decks[0]
	if chapter.Name != "Kapitel 12 · Berlin" || chapter.ExternalID != "wordset:114543" {
		t.Fatalf("first wordset deck = %#v", chapter)
	}
	if chapter.ExpectedCount == nil || *chapter.ExpectedCount != 65 {
		t.Fatalf("expected_count = %v, want 65", chapter.ExpectedCount)
	}
	if chapter.LangTarget != "" || chapter.LangNative != "" {
		t.Fatalf("wordset languages must be omitted, got (%q, %q)", chapter.LangTarget, chapter.LangNative)
	}
	if len(chapter.Notes) != 2 {
		t.Fatalf("notes = %d, want 2", len(chapter.Notes))
	}
	if got := chapter.Notes[0]; got.Term != " der Drachenflieger " || got.Translation != "paralotnia" {
		t.Fatalf("first entry was changed or reordered: %#v", got)
	}
	if got := chapter.Notes[1]; got.Term != "berichten über A" || got.Translation != "donisić o, informować o" {
		t.Fatalf("second entry was changed or reordered: %#v", got)
	}

	secondWordset := first.Decks[1]
	if secondWordset.Name != "Kapitel 13" || secondWordset.ExternalID != "wordset:set-2" || secondWordset.ExpectedCount != nil {
		t.Fatalf("second wordset deck = %#v", secondWordset)
	}
	if len(secondWordset.Notes) != 1 || secondWordset.Notes[0].Term != "die Reise" {
		t.Fatalf("second wordset notes = %#v", secondWordset.Notes)
	}

	secondClass := bundle.Decks[1]
	if secondClass.Name != "Second class" || secondClass.ExternalID != "class:group-2" || len(secondClass.Decks) != 1 {
		t.Fatalf("second class deck = %#v", secondClass)
	}
	if secondClass.Decks[0].ExternalID != "wordset:set-3" || secondClass.Decks[0].ExpectedCount == nil || *secondClass.Decks[0].ExpectedCount != 0 {
		t.Fatalf("second class wordset = %#v", secondClass.Decks[0])
	}
}

func TestParseSchoolWordsetsRequiredFieldsAndStructureErrors(t *testing.T) {
	classWithWordset := func(class, wordset string) string {
		return `{"classes":[{"class":` + class + `,"wordsets":[` + wordset + `]}]}`
	}
	inputWithClass := func(classFields string) string {
		return `{"classes":[{` + classFields + `}]}`
	}
	inputWithWordset := func(wordsetFields string) string {
		return `{"classes":[{"class":{"title":"Class","group_id":"g1"},"wordsets":[{` + wordsetFields + `}]}]}`
	}
	inputWithEntry := func(entryFields string) string {
		return `{"classes":[{"class":{"title":"Class","group_id":"g1"},"wordsets":[{"info":{"id":"w1","name":"Set"},"entries":[{` + entryFields + `}]}]}]}`
	}

	tests := []struct {
		name  string
		input string
		kind  error
		path  string
	}{
		{name: "missing classes", input: `{}`, kind: importer.ErrMissingRequiredField, path: "classes"},
		{name: "classes has wrong type", input: `{"classes":{}}`, kind: importer.ErrInvalidStructure, path: "classes"},
		{name: "class item has wrong type", input: `{"classes":[1]}`, kind: importer.ErrInvalidStructure, path: "classes[0]"},
		{name: "missing class object", input: inputWithClass(`"wordsets":[]`), kind: importer.ErrMissingRequiredField, path: "classes[0].class"},
		{name: "class has wrong type", input: inputWithClass(`"class":[],"wordsets":[]`), kind: importer.ErrInvalidStructure, path: "classes[0].class"},
		{name: "missing title", input: classWithWordset(`{"group_id":"g1"}`, `{"info":{"id":"w1","name":"Set"},"entries":[]}`), kind: importer.ErrMissingRequiredField, path: "classes[0].class.title"},
		{name: "empty title", input: classWithWordset(`{"title":"","group_id":"g1"}`, `{"info":{"id":"w1","name":"Set"},"entries":[]}`), kind: importer.ErrMissingRequiredField, path: "classes[0].class.title"},
		{name: "missing group id", input: classWithWordset(`{"title":"Class"}`, `{"info":{"id":"w1","name":"Set"},"entries":[]}`), kind: importer.ErrMissingRequiredField, path: "classes[0].class.group_id"},
		{name: "empty group id", input: classWithWordset(`{"title":"Class","group_id":"  "}`, `{"info":{"id":"w1","name":"Set"},"entries":[]}`), kind: importer.ErrMissingRequiredField, path: "classes[0].class.group_id"},
		{name: "group id has wrong type", input: classWithWordset(`{"title":"Class","group_id":true}`, `{"info":{"id":"w1","name":"Set"},"entries":[]}`), kind: importer.ErrInvalidStructure, path: "classes[0].class.group_id"},
		{name: "missing wordsets", input: `{"classes":[{"class":{"title":"Class","group_id":"g1"}}]}`, kind: importer.ErrMissingRequiredField, path: "classes[0].wordsets"},
		{name: "wordsets has wrong type", input: `{"classes":[{"class":{"title":"Class","group_id":"g1"},"wordsets":{}}]}`, kind: importer.ErrInvalidStructure, path: "classes[0].wordsets"},
		{name: "wordset item has wrong type", input: `{"classes":[{"class":{"title":"Class","group_id":"g1"},"wordsets":[null]}]}`, kind: importer.ErrInvalidStructure, path: "classes[0].wordsets[0]"},
		{name: "missing info", input: inputWithWordset(`"entries":[]`), kind: importer.ErrMissingRequiredField, path: "classes[0].wordsets[0].info"},
		{name: "info has wrong type", input: inputWithWordset(`"info":[],"entries":[]`), kind: importer.ErrInvalidStructure, path: "classes[0].wordsets[0].info"},
		{name: "missing id", input: inputWithWordset(`"info":{"name":"Set"},"entries":[]`), kind: importer.ErrMissingRequiredField, path: "classes[0].wordsets[0].info.id"},
		{name: "empty id", input: inputWithWordset(`"info":{"id":"","name":"Set"},"entries":[]`), kind: importer.ErrMissingRequiredField, path: "classes[0].wordsets[0].info.id"},
		{name: "id has wrong type", input: inputWithWordset(`"info":{"id":true,"name":"Set"},"entries":[]`), kind: importer.ErrInvalidStructure, path: "classes[0].wordsets[0].info.id"},
		{name: "missing name", input: inputWithWordset(`"info":{"id":"w1"},"entries":[]`), kind: importer.ErrMissingRequiredField, path: "classes[0].wordsets[0].info.name"},
		{name: "empty name", input: inputWithWordset(`"info":{"id":"w1","name":""},"entries":[]`), kind: importer.ErrMissingRequiredField, path: "classes[0].wordsets[0].info.name"},
		{name: "missing entries", input: inputWithWordset(`"info":{"id":"w1","name":"Set"}`), kind: importer.ErrMissingRequiredField, path: "classes[0].wordsets[0].entries"},
		{name: "entries has wrong type", input: inputWithWordset(`"info":{"id":"w1","name":"Set"},"entries":{}`), kind: importer.ErrInvalidStructure, path: "classes[0].wordsets[0].entries"},
		{name: "entry has wrong type", input: inputWithWordset(`"info":{"id":"w1","name":"Set"},"entries":[1]`), kind: importer.ErrInvalidStructure, path: "classes[0].wordsets[0].entries[0]"},
		{name: "missing word", input: inputWithEntry(`"translation":"meaning"`), kind: importer.ErrMissingRequiredField, path: "classes[0].wordsets[0].entries[0].word"},
		{name: "empty word", input: inputWithEntry(`"word":"","translation":"meaning"`), kind: importer.ErrMissingRequiredField, path: "classes[0].wordsets[0].entries[0].word"},
		{name: "missing translation", input: inputWithEntry(`"word":"word"`), kind: importer.ErrMissingRequiredField, path: "classes[0].wordsets[0].entries[0].translation"},
		{name: "empty translation", input: inputWithEntry(`"word":"word","translation":"  "`), kind: importer.ErrMissingRequiredField, path: "classes[0].wordsets[0].entries[0].translation"},
		{name: "word has wrong type", input: inputWithEntry(`"word":42,"translation":"meaning"`), kind: importer.ErrInvalidStructure, path: "classes[0].wordsets[0].entries[0].word"},
		{name: "negative expected count", input: inputWithWordset(`"info":{"id":"w1","name":"Set","expected_count":-1},"entries":[]`), kind: importer.ErrNegativeExpectedCount, path: "classes[0].wordsets[0].info.expected_count"},
		{name: "expected count has wrong type", input: inputWithWordset(`"info":{"id":"w1","name":"Set","expected_count":"2"},"entries":[]`), kind: importer.ErrInvalidStructure, path: "classes[0].wordsets[0].info.expected_count"},
		{name: "expected count is fractional", input: inputWithWordset(`"info":{"id":"w1","name":"Set","expected_count":1.5},"entries":[]`), kind: importer.ErrInvalidStructure, path: "classes[0].wordsets[0].info.expected_count"},
		{name: "malformed JSON", input: `{"classes":[`, kind: importer.ErrInvalidJSON, path: ""},
		{name: "trailing JSON value", input: `{"classes":[]} {}`, kind: importer.ErrInvalidJSON, path: ""},
		{name: "trailing garbage", input: `{"classes":[]} PRIVATE`, kind: importer.ErrInvalidJSON, path: ""},
		{name: "root has wrong type", input: `[]`, kind: importer.ErrInvalidStructure, path: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseSchoolWordsets([]byte(tt.input))
			if err == nil {
				t.Fatal("ParseSchoolWordsets() error = nil")
			}
			if !errors.Is(err, tt.kind) {
				t.Fatalf("ParseSchoolWordsets() error = %v, want errors.Is(_, %v)", err, tt.kind)
			}
			var parseErr *importer.ParseError
			if !errors.As(err, &parseErr) {
				t.Fatalf("error type = %T, want *importer.ParseError", err)
			}
			if parseErr.Path != tt.path {
				t.Fatalf("error path = %q, want %q", parseErr.Path, tt.path)
			}
			if strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), `"translation":"meaning"`) {
				t.Fatalf("error leaked source content: %v", err)
			}
		})
	}
}

func TestSchoolWordsetsRequiredTextMatchesECMAScriptWhitespace(t *testing.T) {
	fields := []struct {
		name     string
		path     string
		document string
	}{
		{
			name:     "class title",
			path:     "classes[0].class.title",
			document: `{"classes":[{"class":{"title":@@@,"group_id":"g1"},"wordsets":[{"info":{"id":"w1","name":"Set"},"entries":[]}]}]}`,
		},
		{
			name:     "class group_id",
			path:     "classes[0].class.group_id",
			document: `{"classes":[{"class":{"title":"Class","group_id":@@@},"wordsets":[{"info":{"id":"w1","name":"Set"},"entries":[]}]}]}`,
		},
		{
			name:     "wordset id",
			path:     "classes[0].wordsets[0].info.id",
			document: `{"classes":[{"class":{"title":"Class","group_id":"g1"},"wordsets":[{"info":{"id":@@@,"name":"Set"},"entries":[]}]}]}`,
		},
		{
			name:     "wordset name",
			path:     "classes[0].wordsets[0].info.name",
			document: `{"classes":[{"class":{"title":"Class","group_id":"g1"},"wordsets":[{"info":{"id":"w1","name":@@@},"entries":[]}]}]}`,
		},
		{
			name:     "entry word",
			path:     "classes[0].wordsets[0].entries[0].word",
			document: `{"classes":[{"class":{"title":"Class","group_id":"g1"},"wordsets":[{"info":{"id":"w1","name":"Set"},"entries":[{"word":@@@,"translation":"meaning"}]}]}]}`,
		},
		{
			name:     "entry translation",
			path:     "classes[0].wordsets[0].entries[0].translation",
			document: `{"classes":[{"class":{"title":"Class","group_id":"g1"},"wordsets":[{"info":{"id":"w1","name":"Set"},"entries":[{"word":"word","translation":@@@}]}]}]}`,
		},
	}
	values := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{name: "U+FEFF", json: `"\uFEFF"`, wantErr: true},
		{name: "U+0085", json: `"\u0085"`},
	}

	for _, field := range fields {
		for _, value := range values {
			t.Run(field.name+"/"+value.name, func(t *testing.T) {
				document := strings.Replace(field.document, "@@@", value.json, 1)
				bundle, err := ParseSchoolWordsets([]byte(document))
				if !value.wantErr {
					if err != nil {
						t.Fatalf("ParseSchoolWordsets() error = %v, want nil", err)
					}
					if bundle == nil {
						t.Fatal("ParseSchoolWordsets() bundle = nil, want parsed bundle")
					}
					return
				}

				if !errors.Is(err, importer.ErrMissingRequiredField) {
					t.Fatalf("ParseSchoolWordsets() error = %v, want ErrMissingRequiredField", err)
				}
				var parseErr *importer.ParseError
				if !errors.As(err, &parseErr) {
					t.Fatalf("error type = %T, want *importer.ParseError", err)
				}
				if parseErr.Path != field.path {
					t.Fatalf("error path = %q, want source path %q", parseErr.Path, field.path)
				}
				if strings.Contains(err.Error(), "decks[") {
					t.Fatalf("error leaked generic bundle validation path: %v", err)
				}
			})
		}
	}
}

func TestSchoolWordsetsDetectIsConservative(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{
			name: "complete recognizable export",
			raw:  `{"classes":[{"class":{"title":"Class","group_id":"g1"},"wordsets":[{"info":{"id":"w1","name":"Set"},"entries":[{"word":"word","translation":"meaning"}]}]}]}`,
			want: true,
		},
		{name: "empty classes", raw: `{"classes":[]}`},
		{name: "class without wordsets", raw: `{"classes":[{"class":{"title":"Class","group_id":"g1"},"wordsets":[]}]}`},
		{name: "unrelated classes object", raw: `{"classes":[{"name":"Class"}]}`},
		{name: "wrong structural type", raw: `{"classes":{}}`},
		{name: "malformed JSON", raw: `{"classes":[`},
		{name: "trailing JSON", raw: `{"classes":[]} {}`},
		{name: "non-JSON", raw: `school wordsets export`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (SchoolWordsetsAdapter{}).Detect([]byte(tt.raw)); got != tt.want {
				t.Fatalf("Detect() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseSchoolWordsetsAllowsEmptyArraysAndMissingExpectedCount(t *testing.T) {
	bundle, err := ParseSchoolWordsets([]byte(`{"classes":[{"class":{"title":"Class","group_id":"g1"},"wordsets":[{"info":{"id":"w1","name":"Set"},"entries":[]}]}]}`))
	if err != nil {
		t.Fatalf("ParseSchoolWordsets() error = %v", err)
	}
	if len(bundle.Decks) != 1 || len(bundle.Decks[0].Decks) != 1 {
		t.Fatalf("parsed hierarchy = %#v", bundle.Decks)
	}
	child := bundle.Decks[0].Decks[0]
	if child.ExpectedCount != nil || child.Notes == nil || len(child.Notes) != 0 {
		t.Fatalf("optional expected count/empty entries = %#v", child)
	}
}
