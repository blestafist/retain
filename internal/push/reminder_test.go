package push

import (
	"context"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
)

type reminderTestClock struct{ now time.Time }

func (c reminderTestClock) Now() time.Time { return c.now }

type sendCall struct {
	endpoint string
	payload  []byte
}

type fakeSender struct {
	calls     []sendCall
	responses map[string]int
	errors    map[string]error
}

func (s *fakeSender) Send(_ context.Context, subscription Subscription, payload []byte) (int, error) {
	s.calls = append(s.calls, sendCall{endpoint: subscription.Endpoint, payload: append([]byte(nil), payload...)})
	if err := s.errors[subscription.Endpoint]; err != nil {
		return 0, err
	}
	return s.responses[subscription.Endpoint], nil
}

type reminderFixture struct {
	database *db.DB
	service  *Service
	userID   int64
	cardID   int64
	clock    reminderTestClock
}

func newReminderFixture(t *testing.T, timezone, reminderTime string, now time.Time) *reminderFixture {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "push.db"))
	if err != nil {
		t.Fatalf("db.Open(): %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	queries := database.Queries()
	user, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email: "reminder@example.test", Name: "Reminder learner", PasswordHash: "unused", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := queries.CreateUserSettings(ctx, user.ID); err != nil {
		t.Fatalf("create user settings: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, `UPDATE user_settings SET timezone = ?, reminder_time = ? WHERE user_id = ?`, timezone, reminderTime, user.ID); err != nil {
		t.Fatalf("configure reminder: %v", err)
	}
	deck, err := queries.CreateDeck(ctx, gen.CreateDeckParams{OwnerID: user.ID, Name: "Reminder deck", WrongArticleRating: 2})
	if err != nil {
		t.Fatalf("create deck: %v", err)
	}
	note, err := queries.CreateNote(ctx, gen.CreateNoteParams{
		Term: "private word", TermKey: "private word", Translation: "private translation",
		Tags: "[]", Alternatives: "[]", Fields: "{}", DeckID: deck.ID, UserID: user.ID,
	})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	card, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: note.ID, UserID: user.ID, Direction: "forward"})
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, `UPDATE cards SET state = 2, due = ? WHERE id = ?`, now.Add(-time.Hour), card.ID); err != nil {
		t.Fatalf("make card due: %v", err)
	}
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys(): %v", err)
	}
	service := NewService(database, Config{VAPIDPublicKey: publicKey, VAPIDPrivateKey: privateKey, VAPIDSubject: "admin@example.test"})
	return &reminderFixture{database: database, service: service, userID: user.ID, cardID: card.ID, clock: reminderTestClock{now: now}}
}

func (f *reminderFixture) addSubscription(t *testing.T, endpoint string) {
	t.Helper()
	_, x, y, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate subscription public key: %v", err)
	}
	p256dh := base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), x, y))
	authKey := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))
	input := SubscriptionInput{Endpoint: endpoint}
	input.Keys.P256dh, input.Keys.Auth = p256dh, authKey
	if err := f.service.Subscribe(context.Background(), f.userID, input); err != nil {
		t.Fatalf("subscribe endpoint: %v", err)
	}
}

func TestReminderWorkerSendsOncePerLocalDayAndDeletesGoneEndpoints(t *testing.T) {
	now := time.Date(2026, time.November, 13, 14, 30, 0, 0, time.UTC) // 09:30 in New York.
	f := newReminderFixture(t, "America/New_York", "09:30", now)
	endpoints := []string{
		"https://fcm.googleapis.com/fcm/send/one",
		"https://updates.push.services.mozilla.com/wpush/v2/two",
		"https://web.push.apple.com/three",
	}
	for _, endpoint := range endpoints {
		f.addSubscription(t, endpoint)
	}
	sender := &fakeSender{
		responses: map[string]int{endpoints[0]: 201, endpoints[2]: 410},
		errors:    map[string]error{endpoints[1]: errors.New("request to https://private-secret.example failed")},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker := NewReminderWorker(f.database, f.clock, sender, logger)
	if err := worker.ProcessAt(context.Background(), now); err != nil {
		t.Fatalf("ProcessAt(): %v", err)
	}
	if len(sender.calls) != 3 {
		t.Fatalf("send attempts = %d; want 3", len(sender.calls))
	}
	for _, call := range sender.calls {
		var payload reminderPayload
		if err := json.Unmarshal(call.payload, &payload); err != nil {
			t.Fatalf("decode reminder payload: %v", err)
		}
		if payload.Title != "Retain" || payload.Body != "You have 1 card to review." || payload.URL != "/study" {
			t.Errorf("reminder payload = %+v", payload)
		}
		if strings.Contains(payload.Body, "private") {
			t.Errorf("reminder exposed study content: %+v", payload)
		}
	}
	if got := countPushRows(t, f.database, "push_reminder_sends", f.userID); got != 1 {
		t.Fatalf("daily claims = %d; want 1", got)
	}
	if got := countPushRows(t, f.database, "push_subscriptions", f.userID); got != 2 {
		t.Fatalf("subscriptions after 410 cleanup = %d; want 2", got)
	}

	if err := worker.ProcessAt(context.Background(), now.Add(30*time.Second)); err != nil {
		t.Fatalf("same-day retry ProcessAt(): %v", err)
	}
	if len(sender.calls) != 3 {
		t.Fatalf("same-day retry added deliveries; total attempts = %d", len(sender.calls))
	}

	if err := worker.ProcessAt(context.Background(), now.AddDate(0, 0, 1)); err != nil {
		t.Fatalf("next-day ProcessAt(): %v", err)
	}
	if len(sender.calls) != 5 {
		t.Fatalf("next local day deliveries = %d; want 2 more", len(sender.calls))
	}
	if got := countPushRows(t, f.database, "push_reminder_sends", f.userID); got != 2 {
		t.Fatalf("daily claims after next day = %d; want 2", got)
	}
}

func TestReminderWorkerSkipsUsersWithoutDueCardsOrAtWrongLocalTime(t *testing.T) {
	now := time.Date(2026, time.November, 13, 14, 30, 0, 0, time.UTC)
	tests := []struct {
		name         string
		timezone     string
		reminderTime string
		makeNotDue   bool
	}{
		{name: "not the configured local time", timezone: "Europe/Warsaw", reminderTime: "17:00"},
		{name: "no due cards", timezone: "America/New_York", reminderTime: "09:30", makeNotDue: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newReminderFixture(t, test.timezone, test.reminderTime, now)
			f.addSubscription(t, "https://fcm.googleapis.com/fcm/send/skip")
			if test.makeNotDue {
				if _, err := f.database.SQL.ExecContext(context.Background(), `UPDATE cards SET state = 0 WHERE id = ?`, f.cardID); err != nil {
					t.Fatalf("make card new: %v", err)
				}
			}
			sender := &fakeSender{responses: make(map[string]int), errors: make(map[string]error)}
			worker := NewReminderWorker(f.database, f.clock, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err := worker.ProcessAt(context.Background(), now); err != nil {
				t.Fatalf("ProcessAt(): %v", err)
			}
			if len(sender.calls) != 0 || countPushRows(t, f.database, "push_reminder_sends", f.userID) != 0 {
				t.Fatalf("worker sent or claimed reminder unexpectedly: calls=%d", len(sender.calls))
			}
		})
	}
}

func TestReminderDayIsLocalAndUniqueAcrossRepeatedDSTHour(t *testing.T) {
	first := time.Date(2026, time.November, 1, 5, 30, 0, 0, time.UTC)
	f := newReminderFixture(t, "America/New_York", "01:30", first)
	endpoint := "https://fcm.googleapis.com/fcm/send/dst"
	f.addSubscription(t, endpoint)
	sender := &fakeSender{responses: map[string]int{endpoint: 201}, errors: make(map[string]error)}
	worker := NewReminderWorker(f.database, f.clock, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := worker.ProcessAt(context.Background(), first); err != nil {
		t.Fatalf("first fall-back hour ProcessAt(): %v", err)
	}
	second := first.Add(time.Hour) // 01:30 repeats after the DST fall-back.
	if err := worker.ProcessAt(context.Background(), second); err != nil {
		t.Fatalf("second fall-back hour ProcessAt(): %v", err)
	}
	if len(sender.calls) != 1 || countPushRows(t, f.database, "push_reminder_sends", f.userID) != 1 {
		t.Fatalf("repeated local time sent twice: calls=%d", len(sender.calls))
	}
}

func TestPushServiceValidatesProviderEndpointsAndScopesUnsubscribe(t *testing.T) {
	now := time.Date(2026, time.November, 13, 14, 30, 0, 0, time.UTC)
	f := newReminderFixture(t, "UTC", "14:30", now)
	input := SubscriptionInput{Endpoint: "http://127.0.0.1/private"}
	if err := f.service.Subscribe(context.Background(), f.userID, input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Subscribe(private endpoint) error = %v; want invalid input", err)
	}
	endpoint := "https://fcm.googleapis.com/fcm/send/scoped"
	f.addSubscription(t, endpoint)
	if err := f.service.Unsubscribe(context.Background(), f.userID+1, endpoint); err != nil {
		t.Fatalf("Unsubscribe for another user: %v", err)
	}
	if got := countPushRows(t, f.database, "push_subscriptions", f.userID); got != 1 {
		t.Fatalf("other user removed subscription: remaining=%d", got)
	}
	if err := f.service.Unsubscribe(context.Background(), f.userID, endpoint); err != nil {
		t.Fatalf("owner Unsubscribe(): %v", err)
	}
	if got := countPushRows(t, f.database, "push_subscriptions", f.userID); got != 0 {
		t.Fatalf("owner did not remove endpoint: remaining=%d", got)
	}
}

func TestPushSubscriptionEndpointCannotBeTransferredBetweenUsers(t *testing.T) {
	now := time.Date(2026, time.November, 13, 14, 30, 0, 0, time.UTC)
	f := newReminderFixture(t, "UTC", "14:30", now)
	endpoint := "https://fcm.googleapis.com/fcm/send/account-bound"
	f.addSubscription(t, endpoint)

	var p256dh, authKey string
	if err := f.database.SQL.QueryRowContext(context.Background(), `SELECT p256dh, auth FROM push_subscriptions WHERE endpoint = ?`, endpoint).Scan(&p256dh, &authKey); err != nil {
		t.Fatalf("read original endpoint keys: %v", err)
	}
	other, err := f.database.Queries().CreateUser(context.Background(), gen.CreateUserParams{
		Email: "other-reminder@example.test", Name: "Other learner", PasswordHash: "unused", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create second user: %v", err)
	}
	input := SubscriptionInput{Endpoint: endpoint}
	input.Keys.P256dh, input.Keys.Auth = p256dh, authKey
	if err := f.service.Subscribe(context.Background(), other.ID, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("Subscribe endpoint for another user error = %v; want conflict", err)
	}
	var ownerID int64
	if err := f.database.SQL.QueryRowContext(context.Background(), `SELECT user_id FROM push_subscriptions WHERE endpoint = ?`, endpoint).Scan(&ownerID); err != nil {
		t.Fatalf("read endpoint owner: %v", err)
	}
	if ownerID != f.userID {
		t.Fatalf("endpoint owner = %d; want unchanged owner %d", ownerID, f.userID)
	}
}

func countPushRows(t *testing.T, database *db.DB, table string, userID int64) int {
	t.Helper()
	if table != "push_subscriptions" && table != "push_reminder_sends" {
		t.Fatalf("unexpected table %q", table)
	}
	var count int
	if err := database.SQL.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table+" WHERE user_id = ?", userID).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
