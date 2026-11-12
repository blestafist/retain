package lang

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	MatchExact   = "exact"
	MatchTypo    = "typo"
	MatchArticle = "article"
	MatchWrong   = "wrong"
)

const (
	RatingAgain = 1
	RatingHard  = 2
	RatingGood  = 3
)

// AnswerInput contains server-resolved answer data. PartOfSpeech enables the
// planned English infinitive normalization only for known verbs.
// GermanArticle is optional; when set, it must be der, die, or das.
// WrongArticleRating must be Again or Hard, even when GermanArticle is empty,
// so callers always provide an explicit policy.
type AnswerInput struct {
	Language           string
	PartOfSpeech       string
	Expected           string
	Alternatives       []string
	Answer             string
	GermanArticle      string
	WrongArticleRating int
}

// AnswerResult is a match classification and a suggested (not submitted)
// review rating.
type AnswerResult struct {
	Match           string
	SuggestedRating int
}

// MatchErrorKind identifies invalid matcher input.
type MatchErrorKind string

const (
	ErrInvalidLanguage MatchErrorKind = "invalid_language"
	ErrInvalidInput    MatchErrorKind = "invalid_input"
	ErrInvalidConfig   MatchErrorKind = "invalid_config"
)

// MatchError is returned instead of silently treating invalid inputs as wrong
// answers.
type MatchError struct {
	Kind  MatchErrorKind
	Field string
	Why   string
}

func (e *MatchError) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Kind, e.Field, e.Why)
}

// MatchAnswer normalizes and compares a typed answer with the expected answer
// and caller-supplied alternatives.
func MatchAnswer(input AnswerInput) (AnswerResult, error) {
	if !supportedLanguage(input.Language) {
		return AnswerResult{}, &MatchError{Kind: ErrInvalidLanguage, Field: "language", Why: "unsupported language code"}
	}
	if input.WrongArticleRating != RatingAgain && input.WrongArticleRating != RatingHard {
		return AnswerResult{}, &MatchError{Kind: ErrInvalidConfig, Field: "wrong_article_rating", Why: "must be 1 (Again) or 2 (Hard)"}
	}
	if input.Language != "de" && input.GermanArticle != "" {
		return AnswerResult{}, &MatchError{Kind: ErrInvalidConfig, Field: "german_article", Why: "is only valid for German"}
	}
	if input.Language == "de" && input.GermanArticle != "" && !isGermanArticle(input.GermanArticle) {
		return AnswerResult{}, &MatchError{Kind: ErrInvalidConfig, Field: "german_article", Why: "must be der, die, or das"}
	}
	if !utf8.ValidString(input.Expected) || strings.TrimSpace(input.Expected) == "" {
		return AnswerResult{}, &MatchError{Kind: ErrInvalidInput, Field: "expected", Why: "must be non-empty valid UTF-8"}
	}
	if !utf8.ValidString(input.Answer) || strings.TrimSpace(input.Answer) == "" {
		return AnswerResult{}, &MatchError{Kind: ErrInvalidInput, Field: "answer", Why: "must be non-empty valid UTF-8"}
	}
	for i, alternative := range input.Alternatives {
		if !utf8.ValidString(alternative) || strings.TrimSpace(alternative) == "" {
			return AnswerResult{}, &MatchError{Kind: ErrInvalidInput, Field: fmt.Sprintf("alternatives[%d]", i), Why: "must be non-empty valid UTF-8"}
		}
	}

	accepted, err := acceptedAnswers(input)
	if err != nil {
		return AnswerResult{}, err
	}
	answer := normalize(input.Language, input.Answer, input.PartOfSpeech)
	if answer == "" {
		return AnswerResult{Match: MatchWrong, SuggestedRating: RatingAgain}, nil
	}

	answerStem, answerArticle := answer, ""
	if input.Language == "de" && input.GermanArticle != "" {
		answerStem, answerArticle = splitArticle(answer)
	}

	// German alternatives are accepted as complete answers; the note's article
	// policy applies to the primary noun, whose article is stored separately.
	for _, candidate := range accepted.alternatives {
		if answer == candidate {
			return exactResult(), nil
		}
	}

	for _, candidate := range accepted.primary {
		if answer == candidate && (input.Language != "de" || input.GermanArticle == "") {
			return exactResult(), nil
		}
		if answerStem == candidate {
			if input.Language != "de" || input.GermanArticle == "" || answerArticle == input.GermanArticle {
				return exactResult(), nil
			}
			return AnswerResult{Match: MatchArticle, SuggestedRating: input.WrongArticleRating}, nil
		}
	}

	for _, candidate := range accepted.all {
		if input.Language == "de" && input.GermanArticle != "" {
			candidate, _ = splitArticle(candidate)
		}
		if input.Language == "pl" && polishDiacriticsOnlyDiffer(answerStem, candidate) {
			return typoResult(), nil
		}
		if isOneEditAway(answerStem, candidate) {
			return typoResult(), nil
		}
	}
	return AnswerResult{Match: MatchWrong, SuggestedRating: RatingAgain}, nil
}

type acceptedAnswersSet struct {
	primary      []string
	alternatives []string
	all          []string
}

func acceptedAnswers(input AnswerInput) (acceptedAnswersSet, error) {
	var result acceptedAnswersSet
	for _, variant := range strings.Split(input.Expected, ";") {
		variant = strings.TrimSpace(variant)
		if variant == "" {
			continue
		}
		normalized := normalize(input.Language, variant, input.PartOfSpeech)
		if normalized == "" {
			continue
		}
		if input.Language == "de" && input.GermanArticle != "" {
			stem, variantArticle := splitArticle(normalized)
			if variantArticle != "" && variantArticle != input.GermanArticle {
				return acceptedAnswersSet{}, &MatchError{Kind: ErrInvalidInput, Field: "expected", Why: "leading article conflicts with german_article"}
			}
			normalized = stem
		}
		result.primary = append(result.primary, normalized)
	}
	if len(result.primary) == 0 {
		return acceptedAnswersSet{}, &MatchError{Kind: ErrInvalidInput, Field: "expected", Why: "must contain a non-empty answer"}
	}
	result.all = append(result.all, result.primary...)
	for i, alternative := range input.Alternatives {
		normalized := normalize(input.Language, alternative, input.PartOfSpeech)
		if normalized == "" {
			return acceptedAnswersSet{}, &MatchError{Kind: ErrInvalidInput, Field: fmt.Sprintf("alternatives[%d]", i), Why: "normalizes to an empty answer"}
		}
		result.alternatives = append(result.alternatives, normalized)
		result.all = append(result.all, normalized)
	}
	return result, nil
}

func exactResult() AnswerResult {
	return AnswerResult{Match: MatchExact, SuggestedRating: RatingGood}
}

func typoResult() AnswerResult {
	return AnswerResult{Match: MatchTypo, SuggestedRating: RatingHard}
}

func supportedLanguage(language string) bool {
	switch language {
	case "de", "en", "pl", "ru":
		return true
	default:
		return false
	}
}

func isGermanArticle(article string) bool {
	switch article {
	case "der", "die", "das":
		return true
	default:
		return false
	}
}

func normalize(language, value, partOfSpeech string) string {
	value = norm.NFC.String(value)
	value = strings.ToLower(value)
	if language == "ru" {
		value = strings.ReplaceAll(value, "ё", "е")
		value = norm.NFD.String(value)
		value = strings.ReplaceAll(value, "\u0301", "")
		value = norm.NFC.String(value)
	}
	if language == "de" {
		value = strings.NewReplacer("ß", "ss", "ä", "ae", "ö", "oe", "ü", "ue").Replace(value)
	}
	value = punctuationSeparators(value)
	fields := strings.Fields(value)
	if language == "de" && len(fields) > 0 {
		switch fields[len(fields)-1] {
		case "a", "d", "g":
			fields = fields[:len(fields)-1]
		}
	}
	if language == "en" {
		if len(fields) > 0 {
			switch fields[0] {
			case "a", "an", "the":
				fields = fields[1:]
			}
		}
		if strings.EqualFold(strings.TrimSpace(partOfSpeech), "verb") && len(fields) > 0 && fields[0] == "to" {
			fields = fields[1:]
		}
	}
	return strings.Join(fields, " ")
}

func punctuationSeparators(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	for _, r := range value {
		if unicode.IsPunct(r) {
			result.WriteByte(' ')
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

func splitArticle(value string) (stem, article string) {
	fields := strings.Fields(value)
	if len(fields) > 0 && isGermanArticle(fields[0]) {
		return strings.Join(fields[1:], " "), fields[0]
	}
	return value, ""
}

func isOneEditAway(left, right string) bool {
	if left == right {
		return false
	}
	leftRunes, rightRunes := utf8.RuneCountInString(left), utf8.RuneCountInString(right)
	if leftRunes-rightRunes > 1 || rightRunes-leftRunes > 1 {
		return false
	}
	if leftRunes == rightRunes {
		differences := 0
		for left != "" {
			leftRune, leftSize := utf8.DecodeRuneInString(left)
			rightRune, rightSize := utf8.DecodeRuneInString(right)
			if leftRune != rightRune {
				differences++
				if differences > 1 {
					return false
				}
			}
			left, right = left[leftSize:], right[rightSize:]
		}
		return differences == 1
	}
	longer, shorter := left, right
	if leftRunes < rightRunes {
		longer, shorter = right, left
	}
	editUsed := false
	for longer != "" && shorter != "" {
		longRune, longSize := utf8.DecodeRuneInString(longer)
		shortRune, shortSize := utf8.DecodeRuneInString(shorter)
		if longRune == shortRune {
			longer, shorter = longer[longSize:], shorter[shortSize:]
			continue
		}
		if editUsed {
			return false
		}
		editUsed = true
		longer = longer[longSize:]
	}
	// The unmatched final rune in the longer answer is the one edit.
	return true
}

func polishDiacriticsOnlyDiffer(left, right string) bool {
	if left == right {
		return false
	}
	return stripPolishDiacritics(left) == stripPolishDiacritics(right)
}

func stripPolishDiacritics(value string) string {
	return strings.NewReplacer(
		"ą", "a", "ć", "c", "ę", "e", "ł", "l", "ń", "n",
		"ó", "o", "ś", "s", "ź", "z", "ż", "z",
	).Replace(value)
}
