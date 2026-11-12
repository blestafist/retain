package lang

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestMatchAnswerGolden(t *testing.T) {
	data, err := os.ReadFile("../../testdata/lang_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Format string `json:"format"`
		Cases  []struct {
			Name               string   `json:"name"`
			Language           string   `json:"language"`
			PartOfSpeech       string   `json:"part_of_speech"`
			Expected           string   `json:"expected"`
			Alternatives       []string `json:"alternatives"`
			Answer             string   `json:"answer"`
			GermanArticle      string   `json:"german_article"`
			WrongArticleRating int      `json:"wrong_article_rating"`
			Match              string   `json:"match"`
			SuggestedRating    int      `json:"suggested_rating"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	if golden.Format != "retain/lang-cases/v1" {
		t.Fatalf("golden format = %q, want retain/lang-cases/v1", golden.Format)
	}
	if len(golden.Cases) == 0 {
		t.Fatal("golden cases are empty")
	}

	for _, tc := range golden.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			got, err := MatchAnswer(AnswerInput{
				Language:           tc.Language,
				PartOfSpeech:       tc.PartOfSpeech,
				Expected:           tc.Expected,
				Alternatives:       tc.Alternatives,
				Answer:             tc.Answer,
				GermanArticle:      tc.GermanArticle,
				WrongArticleRating: tc.WrongArticleRating,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Match != tc.Match || got.SuggestedRating != tc.SuggestedRating {
				t.Errorf("MatchAnswer() = %#v, want match %q rating %d", got, tc.Match, tc.SuggestedRating)
			}
		})
	}
}

func TestMatchAnswerHandlesLargeStoredContent(t *testing.T) {
	longAnswer := strings.Repeat("a", 5000)
	variants := make([]string, 80)
	for i := range variants {
		variants[i] = "variant"
	}
	variants[len(variants)-1] = longAnswer
	alternatives := make([]string, 80)
	for i := range alternatives {
		alternatives[i] = "alternative"
	}
	alternatives[len(alternatives)-1] = longAnswer
	tests := []struct {
		name  string
		input AnswerInput
		match string
	}{
		{
			name: "long typed answer and expected variant",
			input: AnswerInput{
				Language: "en", Expected: strings.Join(variants, ";"), Answer: longAnswer,
				WrongArticleRating: RatingHard,
			},
			match: MatchExact,
		},
		{
			name: "large alternative set and long alternative",
			input: AnswerInput{
				Language: "en", Expected: "answer", Alternatives: alternatives, Answer: longAnswer,
				WrongArticleRating: RatingHard,
			},
			match: MatchExact,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := MatchAnswer(test.input)
			if err != nil {
				t.Fatalf("MatchAnswer() error = %v, want safe match", err)
			}
			if got.Match != test.match {
				t.Errorf("MatchAnswer().Match = %q, want %q", got.Match, test.match)
			}
		})
	}
}

func TestMatchAnswerRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name string
		in   AnswerInput
		kind MatchErrorKind
	}{
		{
			name: "unknown language",
			in:   AnswerInput{Language: "fr", Expected: "bonjour", Answer: "salut", WrongArticleRating: RatingHard},
			kind: ErrInvalidLanguage,
		},
		{
			name: "empty expected answer",
			in:   AnswerInput{Language: "en", Answer: "hello", WrongArticleRating: RatingHard},
			kind: ErrInvalidInput,
		},
		{
			name: "empty typed answer",
			in:   AnswerInput{Language: "en", Expected: "hello", WrongArticleRating: RatingHard},
			kind: ErrInvalidInput,
		},
		{
			name: "invalid utf8 expected",
			in:   AnswerInput{Language: "en", Expected: string([]byte{0xff}), Answer: "hello", WrongArticleRating: RatingHard},
			kind: ErrInvalidInput,
		},
		{
			name: "blank alternative",
			in:   AnswerInput{Language: "en", Expected: "hello", Alternatives: []string{"  "}, Answer: "hello", WrongArticleRating: RatingHard},
			kind: ErrInvalidInput,
		},
		{
			name: "expected normalizes empty",
			in:   AnswerInput{Language: "en", Expected: "?!", Answer: "hello", WrongArticleRating: RatingHard},
			kind: ErrInvalidInput,
		},
		{
			name: "invalid wrong article rating",
			in:   AnswerInput{Language: "de", Expected: "Hund", Answer: "Hund", WrongArticleRating: RatingGood},
			kind: ErrInvalidConfig,
		},
		{
			name: "invalid german article",
			in:   AnswerInput{Language: "de", Expected: "Hund", Answer: "Hund", GermanArticle: "den", WrongArticleRating: RatingHard},
			kind: ErrInvalidConfig,
		},
		{
			name: "article not valid for another language",
			in:   AnswerInput{Language: "en", Expected: "dog", Answer: "dog", GermanArticle: "der", WrongArticleRating: RatingHard},
			kind: ErrInvalidConfig,
		},
		{
			name: "expected article conflicts with configuration",
			in:   AnswerInput{Language: "de", Expected: "die Blume", Answer: "Blume", GermanArticle: "der", WrongArticleRating: RatingHard},
			kind: ErrInvalidInput,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := MatchAnswer(tc.in)
			if err == nil {
				t.Fatal("MatchAnswer() error = nil, want typed error")
			}
			var matchErr *MatchError
			if !errors.As(err, &matchErr) {
				t.Fatalf("MatchAnswer() error type = %T, want *MatchError", err)
			}
			if matchErr.Kind != tc.kind {
				t.Errorf("MatchError.Kind = %q, want %q", matchErr.Kind, tc.kind)
			}
		})
	}
}
