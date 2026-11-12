package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pestit/retain/internal/study"
)

func TestStudyCheckAnswerUsesDirectionLanguagesAndDeckPolicy(t *testing.T) {
	f := newStudyRoutesFixture(t)
	ctx := context.Background()
	if _, err := f.database.SQL.ExecContext(ctx, `
		UPDATE decks SET lang_target = 'de', lang_native = 'pl' WHERE id = ?`, f.ownerDeckID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SQL.ExecContext(ctx, `
		UPDATE notes SET term = 'der Hund', translation = 'pies; kundel',
			alternatives = '["zwierzę"]', fields = '{"article":"der"}'
		WHERE deck_id = ?`, f.ownerDeckID); err != nil {
		t.Fatal(err)
	}
	var noteID int64
	if err := f.database.SQL.QueryRowContext(ctx, `
		SELECT id FROM notes WHERE deck_id = ?`, f.ownerDeckID).Scan(&noteID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SQL.ExecContext(ctx, `
		INSERT INTO cards(note_id, user_id, direction) VALUES (?, ?, 'reverse')`, noteID, f.ownerID); err != nil {
		t.Fatal(err)
	}
	var reverseCardID int64
	if err := f.database.SQL.QueryRowContext(ctx, `
		SELECT id FROM cards WHERE note_id = ? AND direction = 'reverse'`, noteID).Scan(&reverseCardID); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name       string
		cardID     int64
		answer     string
		wantMatch  string
		wantRating int
	}{
		{name: "forward checks native translation", cardID: f.cardID, answer: "kundel", wantMatch: "exact", wantRating: 3},
		{name: "translation alternative", cardID: f.cardID, answer: "zwierzę", wantMatch: "exact", wantRating: 3},
		{name: "forward does not accept target term", cardID: f.cardID, answer: "der Hund", wantMatch: "wrong", wantRating: 1},
		{name: "reverse checks target term and ignores capitalization", cardID: reverseCardID, answer: "der HUND", wantMatch: "exact", wantRating: 3},
		{name: "reverse missing article uses deck Hard policy", cardID: reverseCardID, answer: "Hund", wantMatch: "article", wantRating: 2},
		{name: "reverse wrong article uses deck Hard policy", cardID: reverseCardID, answer: "die Hund", wantMatch: "article", wantRating: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := studyCheckAnswer(t, f, f.ownerCookie, test.cardID, test.answer)
			if result.Match != test.wantMatch || int(result.SuggestedRating) != test.wantRating {
				t.Fatalf("CheckAnswer() = %+v, want match=%q rating=%d", result, test.wantMatch, test.wantRating)
			}
		})
	}

	if _, err := f.database.SQL.ExecContext(ctx, `UPDATE decks SET wrong_article_rating = 1 WHERE id = ?`, f.ownerDeckID); err != nil {
		t.Fatal(err)
	}
	result := studyCheckAnswer(t, f, f.ownerCookie, reverseCardID, "Hund")
	if result.Match != "article" || int(result.SuggestedRating) != 1 {
		t.Fatalf("Again article policy = %+v, want article/1", result)
	}
}

func TestStudyCheckAnswerInheritsLanguagesAndHidesForeignCards(t *testing.T) {
	f := newStudyRoutesFixture(t)
	ctx := context.Background()
	parentResult, err := f.database.SQL.ExecContext(ctx, `
		INSERT INTO decks(owner_id, name, lang_target, lang_native) VALUES (?, 'Language parent', 'en', 'ru')`, f.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	parentID, err := parentResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	childResult, err := f.database.SQL.ExecContext(ctx, `
		INSERT INTO decks(owner_id, parent_id, name) VALUES (?, ?, 'Inherited child')`, f.ownerID, parentID)
	if err != nil {
		t.Fatal(err)
	}
	childID, err := childResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	noteResult, err := f.database.SQL.ExecContext(ctx, `
		INSERT INTO notes(deck_id, term, term_key, translation) VALUES (?, 'to wander', 'to wander', 'бродить')`, childID)
	if err != nil {
		t.Fatal(err)
	}
	noteID, err := noteResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	cardResult, err := f.database.SQL.ExecContext(ctx, `
		INSERT INTO cards(note_id, user_id, direction) VALUES (?, ?, 'reverse')`, noteID, f.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	cardID, err := cardResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	for _, answer := range []string{"wander", "to wander"} {
		result := studyCheckAnswer(t, f, f.ownerCookie, cardID, answer)
		if result.Match != "exact" || int(result.SuggestedRating) != 3 {
			t.Errorf("inherited English answer %q = %+v, want exact/3", answer, result)
		}
	}

	foreignResponse := checkAnswerResponse(t, f, f.ownerCookie, f.otherCardID, "foreign")
	if foreignResponse.Code != http.StatusNotFound {
		t.Fatalf("foreign card check = %d/%s, want 404", foreignResponse.Code, foreignResponse.Body)
	}
	ownerAgainstForeign := checkAnswerResponse(t, f, f.otherCookie, f.cardID, "hello")
	if ownerAgainstForeign.Code != http.StatusNotFound {
		t.Fatalf("cross-owner card check = %d/%s, want 404", ownerAgainstForeign.Code, ownerAgainstForeign.Body)
	}
	unauthenticated := checkAnswerResponse(t, f, nil, f.cardID, "hello")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated check = %d/%s, want 401", unauthenticated.Code, unauthenticated.Body)
	}
	unconfigured := checkAnswerResponse(t, f, f.ownerCookie, f.cardID, "hello")
	if unconfigured.Code != http.StatusBadRequest {
		t.Fatalf("unconfigured deck check = %d/%s, want 400", unconfigured.Code, unconfigured.Body)
	}
	forgedLanguage := studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/check-answer",
		`{"card_id":`+jsonInt(f.cardID)+`,"answer":"hello","language":"de"}`, f.ownerCookie)
	if forgedLanguage.Code != http.StatusBadRequest {
		t.Fatalf("client-supplied language check = %d/%s, want 400", forgedLanguage.Code, forgedLanguage.Body)
	}
}

func studyCheckAnswer(t *testing.T, f *studyRoutesFixture, cookie *http.Cookie, cardID int64, answer string) study.AnswerResult {
	t.Helper()
	response := checkAnswerResponse(t, f, cookie, cardID, answer)
	if response.Code != http.StatusOK {
		t.Fatalf("check answer %q = %d/%s, want 200", answer, response.Code, response.Body)
	}
	var result study.AnswerResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode answer result: %v", err)
	}
	return result
}

func checkAnswerResponse(t *testing.T, f *studyRoutesFixture, cookie *http.Cookie, cardID int64, answer string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(study.CheckAnswerInput{CardID: cardID, Answer: answer})
	if err != nil {
		t.Fatal(err)
	}
	return studyRoutesRequest(f.handler, http.MethodPost, "/api/v1/study/check-answer", string(body), cookie)
}
