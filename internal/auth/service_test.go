package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
)

const serviceTestPassword = "correct horse battery staple"

var serviceTestNow = time.Date(2026, time.March, 14, 9, 26, 53, 0, time.UTC)

type serviceTestClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *serviceTestClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *serviceTestClock) Set(value time.Time) {
	c.mu.Lock()
	c.now = value
	c.mu.Unlock()
}

func newServiceTest(t *testing.T) (*Service, *db.DB, *serviceTestClock) {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "retain.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})

	c := &serviceTestClock{now: serviceTestNow}
	service := NewService(database, c)
	service.hashPassword = func(string) (string, error) { return "test-password-hash", nil }
	service.verifyPassword = func(password, hash string) (bool, error) {
		return password == serviceTestPassword && hash == "test-password-hash", nil
	}
	return service, database, c
}

func createServiceTestInvite(t *testing.T, service *Service, adminID int64) Invite {
	t.Helper()
	invite, err := service.CreateInvite(context.Background(), adminID)
	if err != nil {
		t.Fatalf("CreateInvite() error = %v", err)
	}
	return invite
}

func TestCreateAdminRegisterAndAccount(t *testing.T) {
	ctx := context.Background()
	service, database, c := newServiceTest(t)

	adminID, err := service.CreateAdmin(ctx, " Admin@Example.COM ", " Admin ", serviceTestPassword)
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	admin, err := service.GetAccount(ctx, adminID)
	if err != nil {
		t.Fatalf("GetAccount(admin) error = %v", err)
	}
	if !admin.User.IsAdmin || admin.User.Email != "admin@example.com" || admin.User.Name != "Admin" {
		t.Errorf("CreateAdmin() account user = %+v", admin.User)
	}
	if admin.Settings.UserID != adminID || admin.Settings.NewPerDay != 20 ||
		admin.Settings.MaxReviewsPerDay != 200 || admin.Settings.RequestRetention != 0.9 ||
		admin.Settings.MaximumInterval != 36500 || admin.Settings.LearningSteps != "1m 10m" ||
		admin.Settings.RelearningSteps != "10m" || admin.Settings.Timezone != "UTC" ||
		admin.Settings.DayStartHour != 4 || admin.Settings.ThemeMode != "system" ||
		admin.Settings.ThemeSeed != "#3A4527" || admin.Settings.ThemeVariant != "fidelity" ||
		admin.Settings.ThemeContrast != 0 || admin.Settings.FsrsWeights != nil || admin.Settings.ReminderTime != nil {
		t.Errorf("CreateAdmin() settings = %+v, want complete defaults", admin.Settings)
	}

	invite := createServiceTestInvite(t, service, adminID)
	if want := c.Now().Add(inviteLifetime); !invite.ExpiresAt.Equal(want) {
		t.Errorf("CreateInvite().ExpiresAt = %v, want %v", invite.ExpiresAt, want)
	}
	registered, err := service.Register(ctx, RegisterInput{
		Invite:   invite.Code,
		Email:    "  Friend@Example.COM ",
		Name:     "  Friend Name  ",
		Password: serviceTestPassword,
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if registered.Email != "friend@example.com" || registered.Name != "Friend Name" || registered.IsAdmin {
		t.Errorf("Register() user = %+v, want normalized non-admin user", registered)
	}
	if !registered.CreatedAt.Equal(c.Now()) {
		t.Errorf("Register().CreatedAt = %v, want fake clock time %v", registered.CreatedAt, c.Now())
	}

	account, err := service.GetAccount(ctx, registered.ID)
	if err != nil {
		t.Fatalf("GetAccount(registered) error = %v", err)
	}
	if account.User != registered || account.Settings.UserID != registered.ID {
		t.Errorf("GetAccount() = %+v, want user %+v and own settings", account, registered)
	}
	if _, err := service.CreateInvite(ctx, registered.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("CreateInvite(non-admin) error = %v, want ErrForbidden", err)
	}
	if _, err := service.CreateInvite(ctx, registered.ID+100); !errors.Is(err, ErrForbidden) {
		t.Errorf("CreateInvite(missing user) error = %v, want ErrForbidden", err)
	}
	if _, err := service.GetAccount(ctx, registered.ID+100); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("GetAccount(missing user) error = %v, want ErrUnauthenticated", err)
	}

	encoded, err := json.Marshal(registered)
	if err != nil {
		t.Fatalf("json.Marshal(User) error = %v", err)
	}
	if strings.Contains(string(encoded), "password_hash") || strings.Contains(string(encoded), "test-password-hash") {
		t.Errorf("public user JSON leaked password data: %s", encoded)
	}
	var stored gen.User
	stored, err = database.Queries().GetUserByID(ctx, registered.ID)
	if err != nil {
		t.Fatalf("GetUserByID() error = %v", err)
	}
	if stored.PasswordHash != "test-password-hash" {
		t.Errorf("stored test hash = %q, want fake hash", stored.PasswordHash)
	}
}

func TestRegistrationErrorsAndInviteExpiration(t *testing.T) {
	ctx := context.Background()
	service, database, c := newServiceTest(t)
	adminID, err := service.CreateAdmin(ctx, "admin@example.com", "Admin", serviceTestPassword)
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}

	invite := createServiceTestInvite(t, service, adminID)
	if _, err := service.Register(ctx, RegisterInput{
		Invite: invite.Code, Email: "person@example.com", Name: "Person", Password: serviceTestPassword,
	}); err != nil {
		t.Fatalf("Register(first) error = %v", err)
	}
	if _, err := service.Register(ctx, RegisterInput{
		Invite: invite.Code, Email: "other@example.com", Name: "Other", Password: serviceTestPassword,
	}); !errors.Is(err, ErrInviteUsed) {
		t.Errorf("Register(used invite) error = %v, want ErrInviteUsed", err)
	}
	if _, err := service.Register(ctx, RegisterInput{
		Invite: "does-not-exist", Email: "other@example.com", Name: "Other", Password: serviceTestPassword,
	}); !errors.Is(err, ErrInvalidInvite) {
		t.Errorf("Register(unknown invite) error = %v, want ErrInvalidInvite", err)
	}

	expiredCode := "expired-test-invite"
	_, err = database.Queries().CreateInvite(ctx, gen.CreateInviteParams{
		Code:      expiredCode,
		CreatedBy: adminID,
		ExpiresAt: c.Now().Add(-time.Hour),
		CreatedAt: c.Now().Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateInvite(expired) error = %v", err)
	}
	if _, err := service.Register(ctx, RegisterInput{
		Invite: expiredCode, Email: "expired@example.com", Name: "Expired", Password: serviceTestPassword,
	}); !errors.Is(err, ErrInviteExpired) {
		t.Errorf("Register(expired invite) error = %v, want ErrInviteExpired", err)
	}

	secondInvite := createServiceTestInvite(t, service, adminID)
	if _, err := service.Register(ctx, RegisterInput{
		Invite: secondInvite.Code, Email: " PERSON@example.com ", Name: "Duplicate", Password: serviceTestPassword,
	}); !errors.Is(err, ErrEmailExists) {
		t.Errorf("Register(duplicate normalized email) error = %v, want ErrEmailExists", err)
	}
	storedInvite, err := database.Queries().GetInvite(ctx, secondInvite.Code)
	if err != nil {
		t.Fatalf("GetInvite(duplicate registration) error = %v", err)
	}
	if storedInvite.UsedBy.Valid {
		t.Errorf("duplicate-email registration consumed invite: %+v", storedInvite)
	}

	invalidInputs := []RegisterInput{
		{Invite: secondInvite.Code, Email: "Friend <friend@example.com>", Name: "Name", Password: serviceTestPassword},
		{Invite: secondInvite.Code, Email: "bad-email", Name: "Name", Password: serviceTestPassword},
		{Invite: secondInvite.Code, Email: "valid@example.com", Name: "  ", Password: serviceTestPassword},
		{Invite: secondInvite.Code, Email: "valid@example.com", Name: strings.Repeat("x", maxNameRunes+1), Password: serviceTestPassword},
		{Invite: secondInvite.Code, Email: "valid@example.com", Name: "Name", Password: "short"},
	}
	for _, input := range invalidInputs {
		if _, err := service.Register(ctx, input); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Register(invalid input) error = %v, want ErrInvalidInput", err)
		}
	}
}

func TestRegisterRollsBackWhenInviteIsConsumedConcurrently(t *testing.T) {
	ctx := context.Background()
	service, database, _ := newServiceTest(t)
	adminID, err := service.CreateAdmin(ctx, "admin@example.com", "Admin", serviceTestPassword)
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	invite := createServiceTestInvite(t, service, adminID)

	gate := make(chan struct{})
	ready := make(chan struct{})
	var hashCalls atomic.Int32
	service.hashPassword = func(string) (string, error) {
		if hashCalls.Add(1) == 2 {
			close(ready)
		}
		<-gate
		return "test-password-hash", nil
	}
	type registrationResult struct {
		user  User
		email string
		err   error
	}
	results := make(chan registrationResult, 2)
	for _, email := range []string{"first@example.com", "second@example.com"} {
		email := email
		go func() {
			user, err := service.Register(ctx, RegisterInput{
				Invite: invite.Code, Email: email, Name: "User", Password: serviceTestPassword,
			})
			results <- registrationResult{user: user, email: email, err: err}
		}()
	}
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("both password hashes did not reach the synchronization barrier")
	}
	close(gate)

	succeeded := 0
	var winnerID int64
	var losingEmail string
	for range 2 {
		result := <-results
		if result.err == nil {
			succeeded++
			winnerID = result.user.ID
			continue
		}
		if !errors.Is(result.err, ErrInviteUsed) {
			t.Errorf("Register(%q) error = %v, want ErrInviteUsed", result.email, result.err)
		}
		losingEmail = result.email
	}
	if succeeded != 1 {
		t.Fatalf("successful registrations = %d, want exactly one", succeeded)
	}
	if _, err := database.Queries().GetUserByEmail(ctx, losingEmail); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("losing registration user lookup error = %v, want no user after rollback", err)
	}
	if _, err := database.Queries().GetUserSettings(ctx, winnerID); err != nil {
		t.Errorf("winning registration settings lookup error = %v, want committed settings", err)
	}
	storedInvite, err := database.Queries().GetInvite(ctx, invite.Code)
	if err != nil {
		t.Fatalf("GetInvite() error = %v", err)
	}
	if !storedInvite.UsedBy.Valid || storedInvite.UsedBy.Int64 != winnerID {
		t.Errorf("invite usage = %+v, want only winning user %d", storedInvite.UsedBy, winnerID)
	}
}

func TestRegisterPreservesPasswordHashBusy(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newServiceTest(t)
	adminID, err := service.CreateAdmin(ctx, "admin@example.com", "Admin", serviceTestPassword)
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	invite := createServiceTestInvite(t, service, adminID)
	service.hashPassword = func(string) (string, error) { return "", ErrPasswordHashBusy }
	if _, err := service.Register(ctx, RegisterInput{
		Invite: invite.Code, Email: "person@example.com", Name: "Person", Password: serviceTestPassword,
	}); !errors.Is(err, ErrPasswordHashBusy) {
		t.Errorf("Register() error = %v, want ErrPasswordHashBusy", err)
	}
}

func TestRegisterRollsBackIfInviteExpiresDuringPasswordHashing(t *testing.T) {
	ctx := context.Background()
	service, database, c := newServiceTest(t)
	adminID, err := service.CreateAdmin(ctx, "admin@example.com", "Admin", serviceTestPassword)
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	invite := createServiceTestInvite(t, service, adminID)
	service.hashPassword = func(string) (string, error) {
		c.Set(invite.ExpiresAt)
		return "test-password-hash", nil
	}

	if _, err := service.Register(ctx, RegisterInput{
		Invite: invite.Code, Email: "person@example.com", Name: "Person", Password: serviceTestPassword,
	}); !errors.Is(err, ErrInviteExpired) {
		t.Fatalf("Register() error = %v, want ErrInviteExpired", err)
	}
	if _, err := database.Queries().GetUserByEmail(ctx, "person@example.com"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("failed registration left a user behind: %v", err)
	}
	storedInvite, err := database.Queries().GetInvite(ctx, invite.Code)
	if err != nil {
		t.Fatalf("GetInvite() error = %v", err)
	}
	if storedInvite.UsedBy.Valid {
		t.Errorf("expired invite was consumed despite registration rollback: %+v", storedInvite)
	}
}

func TestLoginSessionsAuthenticationAndLogout(t *testing.T) {
	ctx := context.Background()
	service, database, c := newServiceTest(t)
	userID, err := service.CreateAdmin(ctx, "user@example.com", "User", serviceTestPassword)
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	user, err := database.Queries().GetUserByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserByID() error = %v", err)
	}

	oldHash := make([]byte, 32)
	oldHash[0] = 1
	_, err = database.Queries().CreateSession(ctx, gen.CreateSessionParams{
		TokenHash: oldHash,
		UserID:    userID,
		ExpiresAt: c.Now().Add(-time.Second),
		CreatedAt: c.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateSession(expired) error = %v", err)
	}

	userAgent := strings.Repeat("a", maxUserAgentBytes+100)
	result, err := service.Login(ctx, LoginInput{Email: " USER@EXAMPLE.COM ", Password: serviceTestPassword}, userAgent)
	if err != nil {
		t.Fatalf("Login(valid) error = %v", err)
	}
	if result.User != publicUser(user) {
		t.Errorf("Login().User = %+v, want %+v", result.User, publicUser(user))
	}
	if want := c.Now().Add(sessionLifetime); !result.ExpiresAt.Equal(want) {
		t.Errorf("Login().ExpiresAt = %v, want %v", result.ExpiresAt, want)
	}
	decodedToken, err := base64.RawURLEncoding.DecodeString(result.Token)
	if err != nil || len(decodedToken) != sessionTokenSize {
		t.Fatalf("Login() token decoded length/error = %d/%v, want %d bytes", len(decodedToken), err, sessionTokenSize)
	}
	digest := sessionTokenDigest(result.Token)
	session, err := database.Queries().GetSession(ctx, digest)
	if err != nil {
		t.Fatalf("GetSession(digest) error = %v", err)
	}
	if session.UserID != userID || !session.ExpiresAt.Equal(result.ExpiresAt) ||
		!session.UserAgent.Valid || len(session.UserAgent.String) != maxUserAgentBytes {
		t.Errorf("stored session = %+v, want user, expiry, and bounded agent", session)
	}
	if _, err := database.Queries().GetSession(ctx, []byte(result.Token)); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetSession(raw token) error = %v, want no raw-token row", err)
	}
	if _, err := database.Queries().GetSession(ctx, oldHash); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("login did not clean up expired session: %v", err)
	}

	authenticated, err := service.Authenticate(ctx, result.Token)
	if err != nil || authenticated != result.User {
		t.Fatalf("Authenticate() = %+v, %v; want %+v, nil", authenticated, err, result.User)
	}

	_, wrongErr := service.Login(ctx, LoginInput{Email: "user@example.com", Password: "wrong password"}, "")
	_, unknownErr := service.Login(ctx, LoginInput{Email: "missing@example.com", Password: "wrong password"}, "")
	if !errors.Is(wrongErr, ErrInvalidCredentials) || !errors.Is(unknownErr, ErrInvalidCredentials) || wrongErr.Error() != unknownErr.Error() {
		t.Errorf("login errors for wrong/unknown credentials = %v / %v, want identical generic errors", wrongErr, unknownErr)
	}

	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal(LoginResult) error = %v", err)
	}
	if strings.Contains(string(encodedResult), result.Token) || strings.Contains(string(encodedResult), "token") {
		t.Errorf("LoginResult JSON exposed cookie token: %s", encodedResult)
	}

	if err := service.Logout(ctx, result.Token); err != nil {
		t.Fatalf("Logout(first) error = %v", err)
	}
	if err := service.Logout(ctx, result.Token); err != nil {
		t.Fatalf("Logout(second) error = %v, want idempotent success", err)
	}
	if _, err := service.Authenticate(ctx, result.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("Authenticate(logged out) error = %v, want ErrUnauthenticated", err)
	}
	if _, err := database.Queries().GetSession(ctx, digest); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("logged-out session lookup error = %v, want no rows", err)
	}
	if err := service.Logout(ctx, "not-a-token"); err != nil {
		t.Errorf("Logout(malformed) error = %v, want no-op", err)
	}
	if _, err := service.Authenticate(ctx, "not-a-token"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("Authenticate(malformed) error = %v, want ErrUnauthenticated", err)
	}

	expiring, err := service.Login(ctx, LoginInput{Email: "user@example.com", Password: serviceTestPassword}, "test-agent")
	if err != nil {
		t.Fatalf("Login(for expiration) error = %v", err)
	}
	expiringDigest := sessionTokenDigest(expiring.Token)
	c.Set(expiring.ExpiresAt)
	if _, err := service.Authenticate(ctx, expiring.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("Authenticate(expired) error = %v, want ErrUnauthenticated", err)
	}
	if _, err := database.Queries().GetSession(ctx, expiringDigest); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expired session lookup error = %v, want deleted row", err)
	}
}

func TestLoginPasswordHashBusyAndPublicErrorsAreSanitized(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newServiceTest(t)
	if _, err := service.CreateAdmin(ctx, "admin@example.com", "Admin", serviceTestPassword); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	service.verifyPassword = func(string, string) (bool, error) { return false, ErrPasswordHashBusy }
	if _, err := service.Login(ctx, LoginInput{Email: "admin@example.com", Password: serviceTestPassword}, ""); !errors.Is(err, ErrPasswordHashBusy) {
		t.Errorf("Login() error = %v, want ErrPasswordHashBusy", err)
	}

	service.verifyPassword = func(string, string) (bool, error) { return false, errors.New("SQL failed with secret token") }
	_, err := service.Login(ctx, LoginInput{Email: "admin@example.com", Password: serviceTestPassword}, "")
	if !errors.Is(err, ErrInvalidCredentials) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "SQL") {
		t.Errorf("Login(internal verifier error) = %v, want sanitized generic credentials error", err)
	}
}

func TestSettingsPatchValidationAndIsolation(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newServiceTest(t)
	firstID, err := service.CreateAdmin(ctx, "first@example.com", "First", serviceTestPassword)
	if err != nil {
		t.Fatalf("CreateAdmin(first) error = %v", err)
	}
	secondID, err := service.CreateAdmin(ctx, "second@example.com", "Second", serviceTestPassword)
	if err != nil {
		t.Fatalf("CreateAdmin(second) error = %v", err)
	}

	patch := map[string]json.RawMessage{
		"new_per_day":         json.RawMessage(`35`),
		"max_reviews_per_day": json.RawMessage(`250`),
		"request_retention":   json.RawMessage(`0.95`),
		"learning_steps":      json.RawMessage(`"30s 2h"`),
		"relearning_steps":    json.RawMessage(`"15m"`),
		"fsrs_weights":        json.RawMessage(`[ 1, 2.5 ]`),
		"timezone":            json.RawMessage(`"America/New_York"`),
		"day_start_hour":      json.RawMessage(`5`),
		"reminder_time":       json.RawMessage(`"19:30"`),
		"tts_auto":            json.RawMessage(`true`),
		"theme_mode":          json.RawMessage(`"dark"`),
		"theme_seed":          json.RawMessage(`"#abcdef"`),
		"theme_variant":       json.RawMessage(`"expressive"`),
		"theme_contrast":      json.RawMessage(`0.5`),
	}
	updated, err := service.UpdateSettings(ctx, firstID, patch)
	if err != nil {
		t.Fatalf("UpdateSettings(valid patch) error = %v", err)
	}
	if updated.UserID != firstID || updated.NewPerDay != 35 || updated.MaxReviewsPerDay != 250 ||
		updated.RequestRetention != 0.95 || updated.LearningSteps != "30s 2h" || updated.RelearningSteps != "15m" ||
		string(updated.FsrsWeights) != `[1,2.5]` || updated.Timezone != "America/New_York" || updated.DayStartHour != 5 ||
		updated.ReminderTime == nil || *updated.ReminderTime != "19:30" || !updated.TtsAuto || updated.ThemeMode != "dark" ||
		updated.ThemeSeed != "#ABCDEF" || updated.ThemeVariant != "expressive" || updated.ThemeContrast != 0.5 ||
		updated.MaximumInterval != 36500 {
		t.Errorf("UpdateSettings() = %+v, want full merged normalized settings", updated)
	}

	cleared, err := service.UpdateSettings(ctx, firstID, map[string]json.RawMessage{
		"reminder_time": json.RawMessage(`null`),
		"fsrs_weights":  json.RawMessage(`null`),
	})
	if err != nil {
		t.Fatalf("UpdateSettings(clear nullable values) error = %v", err)
	}
	if cleared.ReminderTime != nil || cleared.FsrsWeights != nil || cleared.NewPerDay != 35 {
		t.Errorf("UpdateSettings(nullable clear) = %+v, want cleared nullable values and preserved fields", cleared)
	}
	encoded, err := json.Marshal(cleared)
	if err != nil {
		t.Fatalf("json.Marshal(Settings) error = %v", err)
	}
	if !strings.Contains(string(encoded), `"fsrs_weights":null`) || !strings.Contains(string(encoded), `"reminder_time":null`) {
		t.Errorf("nullable settings JSON = %s, want explicit nulls", encoded)
	}

	invalidPatches := []struct {
		name  string
		patch map[string]json.RawMessage
	}{
		{name: "unknown key", patch: map[string]json.RawMessage{"unknown": json.RawMessage(`1`)}},
		{name: "read-only user id", patch: map[string]json.RawMessage{"user_id": json.RawMessage(`2`)}},
		{name: "negative new limit", patch: map[string]json.RawMessage{"new_per_day": json.RawMessage(`-1`)}},
		{name: "negative review limit", patch: map[string]json.RawMessage{"max_reviews_per_day": json.RawMessage(`-1`)}},
		{name: "zero retention", patch: map[string]json.RawMessage{"request_retention": json.RawMessage(`0`)}},
		{name: "retention above one", patch: map[string]json.RawMessage{"request_retention": json.RawMessage(`1.01`)}},
		{name: "zero interval", patch: map[string]json.RawMessage{"maximum_interval": json.RawMessage(`0`)}},
		{name: "invalid learning step", patch: map[string]json.RawMessage{"learning_steps": json.RawMessage(`"0s 1m"`)}},
		{name: "invalid relearning step", patch: map[string]json.RawMessage{"relearning_steps": json.RawMessage(`"later"`)}},
		{name: "invalid timezone", patch: map[string]json.RawMessage{"timezone": json.RawMessage(`"Mars/Olympus"`)}},
		{name: "invalid day start", patch: map[string]json.RawMessage{"day_start_hour": json.RawMessage(`24`)}},
		{name: "invalid reminder", patch: map[string]json.RawMessage{"reminder_time": json.RawMessage(`"9:00"`)}},
		{name: "invalid reminder hour", patch: map[string]json.RawMessage{"reminder_time": json.RawMessage(`"24:00"`)}},
		{name: "non-boolean tts", patch: map[string]json.RawMessage{"tts_auto": json.RawMessage(`"true"`)}},
		{name: "invalid theme mode", patch: map[string]json.RawMessage{"theme_mode": json.RawMessage(`"sepia"`)}},
		{name: "invalid theme seed", patch: map[string]json.RawMessage{"theme_seed": json.RawMessage(`"green"`)}},
		{name: "invalid theme variant", patch: map[string]json.RawMessage{"theme_variant": json.RawMessage(`"unknown"`)}},
		{name: "invalid contrast", patch: map[string]json.RawMessage{"theme_contrast": json.RawMessage(`0.25`)}},
		{name: "invalid fsrs json", patch: map[string]json.RawMessage{"fsrs_weights": json.RawMessage(`not-json`)}},
		{name: "null non-nullable value", patch: map[string]json.RawMessage{"timezone": json.RawMessage(`null`)}},
	}
	for _, test := range invalidPatches {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.UpdateSettings(ctx, firstID, test.patch); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("UpdateSettings() error = %v, want ErrInvalidInput", err)
			}
		})
	}

	second, err := service.GetAccount(ctx, secondID)
	if err != nil {
		t.Fatalf("GetAccount(second) error = %v", err)
	}
	if second.Settings.NewPerDay != 20 || second.Settings.ThemeMode != "system" || second.Settings.ThemeSeed != "#3A4527" {
		t.Errorf("first user's patch leaked to second user's settings: %+v", second.Settings)
	}
	if _, err := service.UpdateSettings(ctx, secondID+999, map[string]json.RawMessage{"new_per_day": json.RawMessage(`5`)}); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("UpdateSettings(missing user) error = %v, want ErrUnauthenticated", err)
	}
}

func TestAccountErrorsDoNotExposeDatabaseDetails(t *testing.T) {
	service, _, _ := newServiceTest(t)
	service.database = nil
	if _, err := service.GetAccount(context.Background(), 1); !errors.Is(err, ErrInternal) {
		t.Errorf("GetAccount(unavailable DB) error = %v, want ErrInternal", err)
	}
}
