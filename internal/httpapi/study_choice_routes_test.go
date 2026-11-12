package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pestit/retain/internal/srs"
	"github.com/pestit/retain/internal/study"
)

func TestStudyChoiceOptionsUseOwnedNewCardSameDeckAndDirection(t *testing.T) {
	f := newStudyRoutesFixture(t)
	ctx := context.Background()
	if _, err := f.database.SQL.ExecContext(ctx, `
		UPDATE decks SET lang_target = 'de', lang_native = 'pl' WHERE id = ?`, f.ownerDeckID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SQL.ExecContext(ctx, `
		UPDATE notes SET term = 'der Hund', translation = 'pies; psi', alternatives = '["kundel"]'
		WHERE deck_id = ?`, f.ownerDeckID); err != nil {
		t.Fatal(err)
	}
	addStudyChoiceNote(t, f, f.ownerDeckID, "die Katze", "kot", `["kotek"]`, "forward")
	addStudyChoiceNote(t, f, f.ownerDeckID, "der Hund 2", "pies", `[]`, "forward")
	addStudyChoiceNote(t, f, f.ownerDeckID, "der Hund 3", "kundel", `[]`, "forward")
	addStudyChoiceNote(t, f, f.ownerDeckID, "der Vogel", "ptak", `[]`, "forward")
	addStudyChoiceNote(t, f, f.ownerDeckID, "der Bär", "kot", `[]`, "forward")
	addStudyChoiceNote(t, f, f.ownerDeckID, "wrong reverse candidate", "not used", `[]`, "reverse")
	outsideDeckID := studyRoutesCreateCard(t, f.database, f.ownerID, "Separate deck", "outside term", srs.New, f.now)
	addStudyChoiceNote(t, f, outsideDeckID, "outside word", "outside deck", `[]`, "forward")

	childResult, err := f.database.SQL.ExecContext(ctx,
		`INSERT INTO decks(owner_id, parent_id, name) VALUES (?, ?, 'Child deck')`, f.ownerID, f.ownerDeckID)
	if err != nil {
		t.Fatal(err)
	}
	childDeckID, err := childResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	addStudyChoiceNote(t, f, childDeckID, "child word", "child only", `[]`, "forward")

	response := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/choice-options",
		`{"card_id":`+strconv.FormatInt(f.cardID, 10)+`}`, f.ownerCookie)
	if response.Code != http.StatusOK {
		t.Fatalf("choice options response = %d/%s, want 200", response.Code, response.Body)
	}
	var result struct {
		Choices []string `json:"choices"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode choice options: %v", err)
	}
	if len(result.Choices) != 4 {
		t.Fatalf("got %d choices %q, want exactly 4", len(result.Choices), result.Choices)
	}
	set := make(map[string]bool, len(result.Choices))
	for _, choice := range result.Choices {
		if set[choice] {
			t.Fatalf("duplicate choice %q in %q", choice, result.Choices)
		}
		set[choice] = true
	}
	if !set["pies"] {
		t.Fatalf("correct answer missing from choices %q", result.Choices)
	}
	for _, excluded := range []string{"psi", "kundel", "child only", "wrong reverse candidate", "not used"} {
		if set[excluded] {
			t.Errorf("choice %q must be excluded from %q", excluded, result.Choices)
		}
	}
	for _, distractor := range []string{"kot", "ptak"} {
		if !set[distractor] {
			t.Errorf("same-deck forward distractor %q missing from %q", distractor, result.Choices)
		}
	}
	if set["outside deck"] {
		t.Errorf("unrelated deck answer leaked into choices %q", result.Choices)
	}

	foreignCard := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/choice-options",
		`{"card_id":`+strconv.FormatInt(f.otherCardID, 10)+`}`, f.ownerCookie)
	if foreignCard.Code != http.StatusNotFound {
		t.Errorf("foreign card choice options response = %d/%s, want 404", foreignCard.Code, foreignCard.Body)
	}
	ownerFromOther := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/choice-options",
		`{"card_id":`+strconv.FormatInt(f.cardID, 10)+`}`, f.otherCookie)
	if ownerFromOther.Code != http.StatusNotFound {
		t.Errorf("cross-user choice options response = %d/%s, want 404", ownerFromOther.Code, ownerFromOther.Body)
	}
	forged := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/choice-options",
		`{"card_id":`+strconv.FormatInt(f.cardID, 10)+`,"answer":"forged"}`, f.ownerCookie)
	if forged.Code != http.StatusBadRequest {
		t.Errorf("choice options accepted extra answer field: %d/%s", forged.Code, forged.Body)
	}
}

func TestStudyChoiceOptionsRequireNewCardAndFourAvailableChoices(t *testing.T) {
	f := newStudyRoutesFixture(t)
	if _, err := f.database.SQL.Exec(`UPDATE decks SET lang_target = 'de', lang_native = 'pl' WHERE id = ?`, f.ownerDeckID); err != nil {
		t.Fatal(err)
	}
	for _, state := range []srs.State{srs.Learning, srs.New} {
		_, err := f.database.SQL.Exec(`UPDATE cards SET state = ?, suspended = ? WHERE id = ?`, int64(state), state == srs.New, f.cardID)
		if err != nil {
			t.Fatal(err)
		}
		response := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/choice-options",
			`{"card_id":`+strconv.FormatInt(f.cardID, 10)+`}`, f.ownerCookie)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"choice_unavailable"`) {
			t.Errorf("state=%d/suspended response = %d/%s, want generic 409", state, response.Code, response.Body)
		}
	}
	_, err := f.database.SQL.Exec(`UPDATE cards SET state = 0, suspended = 0 WHERE id = ?`, f.cardID)
	if err != nil {
		t.Fatal(err)
	}
	tooFew := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/choice-options",
		`{"card_id":`+strconv.FormatInt(f.cardID, 10)+`}`, f.ownerCookie)
	if tooFew.Code != http.StatusConflict || !strings.Contains(tooFew.Body.String(), `"code":"choice_unavailable"`) {
		t.Fatalf("too-few distractors response = %d/%s, want generic 409", tooFew.Code, tooFew.Body)
	}
	if strings.Contains(tooFew.Body.String(), "translation") {
		t.Fatalf("unavailable response leaked card answer: %s", tooFew.Body)
	}
}

func TestStudyChoiceAnswerIsExactGoodOrWrongAgain(t *testing.T) {
	f := newStudyRoutesFixture(t)
	if _, err := f.database.SQL.Exec(`
		UPDATE decks SET lang_target = 'de', lang_native = 'pl' WHERE id = ?`, f.ownerDeckID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SQL.Exec(`
		UPDATE notes SET translation = 'pies', alternatives = '["kundel"]' WHERE deck_id = ?`, f.ownerDeckID); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		answer string
		match  string
		rating int
	}{{"pies", "exact", int(srs.Good)}, {"kundel", "wrong", int(srs.Again)}, {"PIES", "wrong", int(srs.Again)}, {"piesz", "wrong", int(srs.Again)}} {
		body, err := json.Marshal(study.CheckAnswerInput{CardID: f.cardID, Answer: test.answer, Mode: "choice"})
		if err != nil {
			t.Fatal(err)
		}
		response := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/check-answer", string(body), f.ownerCookie)
		if response.Code != http.StatusOK {
			t.Fatalf("choice check %q = %d/%s, want 200", test.answer, response.Code, response.Body)
		}
		var result study.AnswerResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Match != test.match || int(result.SuggestedRating) != test.rating {
			t.Errorf("choice check %q = %+v, want %s/%d", test.answer, result, test.match, test.rating)
		}
	}
	if _, err := f.database.SQL.Exec(`UPDATE cards SET state = ? WHERE id = ?`, int64(srs.Learning), f.cardID); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(study.CheckAnswerInput{CardID: f.cardID, Answer: "pies", Mode: "choice"})
	response := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/check-answer", string(body), f.ownerCookie)
	if response.Code != http.StatusConflict {
		t.Errorf("non-new choice answer = %d/%s, want 409", response.Code, response.Body)
	}
}

func TestStudyChoiceReviewLogsChoiceModeAndOnlyAllowsNewCards(t *testing.T) {
	f := newStudyRoutesFixture(t)
	first := study.ReviewInput{
		ID: "018f0f3e-7b00-7000-8000-000000000041", CardID: f.cardID,
		Rating: srs.Good, Mode: "choice", ReviewedAt: f.now,
	}
	response := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/review", mustStudyJSON(t, first), f.ownerCookie)
	if response.Code != http.StatusOK {
		t.Fatalf("choice review = %d/%s, want 200", response.Code, response.Body)
	}
	log, err := f.database.Queries().GetReviewLog(context.Background(), first.ID)
	if err != nil || log.Mode != "choice" || log.Rating != int64(srs.Good) {
		t.Fatalf("choice review log = %+v, %v; want choice/Good", log, err)
	}
	second := first
	second.ID = "018f0f3e-7b00-7000-8000-000000000042"
	second.ReviewedAt = f.now.Add(time.Minute)
	response = studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/review", mustStudyJSON(t, second), f.ownerCookie)
	if response.Code != http.StatusBadRequest {
		t.Errorf("choice review on a non-New card = %d/%s, want 400", response.Code, response.Body)
	}
}

func addStudyChoiceNote(t *testing.T, f *studyRoutesFixture, deckID int64, term, translation, alternatives, direction string) int64 {
	t.Helper()
	result, err := f.database.SQL.Exec(`INSERT INTO notes(deck_id, term, term_key, translation, alternatives) VALUES (?, ?, ?, ?, ?)`,
		deckID, term, term, translation, alternatives)
	if err != nil {
		t.Fatalf("insert choice note %q: %v", term, err)
	}
	noteID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SQL.Exec(`INSERT INTO cards(note_id, user_id, direction, state) VALUES (?, ?, ?, ?)`,
		noteID, f.ownerID, direction, srs.New); err != nil {
		t.Fatalf("insert choice card %q: %v", term, err)
	}
	return noteID
}
