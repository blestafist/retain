package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/pestit/retain/internal/db/gen"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()

	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "retain.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return database
}

func createTestUser(t *testing.T, queries *gen.Queries, email string, admin bool) gen.User {
	t.Helper()

	user, err := queries.CreateUser(context.Background(), gen.CreateUserParams{
		Email:        email,
		Name:         "Test User",
		PasswordHash: "argon2id-test-hash",
		IsAdmin:      admin,
		CreatedAt:    time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateUser(%q) error = %v", email, err)
	}
	return user
}

func TestAccountQueriesAndSettingsDefaults(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	queries := database.Queries()

	user := createTestUser(t, queries, "Admin@example.com", true)
	gotByEmail, err := queries.GetUserByEmail(ctx, "ADMIN@EXAMPLE.COM")
	if err != nil {
		t.Fatalf("GetUserByEmail() error = %v", err)
	}
	if gotByEmail.ID != user.ID || gotByEmail.Email != user.Email || !gotByEmail.IsAdmin {
		t.Errorf("GetUserByEmail() = %+v, want user %+v", gotByEmail, user)
	}
	gotByID, err := queries.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID() error = %v", err)
	}
	if gotByID != user {
		t.Errorf("GetUserByID() = %+v, want %+v", gotByID, user)
	}
	if got, err := queries.CountAdmins(ctx); err != nil || got != 1 {
		t.Fatalf("CountAdmins() = %d, %v; want 1, nil", got, err)
	}

	settings, err := queries.CreateUserSettings(ctx, user.ID)
	if err != nil {
		t.Fatalf("CreateUserSettings() error = %v", err)
	}
	wantDefaults := gen.UserSetting{
		UserID:           user.ID,
		NewPerDay:        20,
		MaxReviewsPerDay: 200,
		RequestRetention: 0.9,
		MaximumInterval:  36500,
		LearningSteps:    "1m 10m",
		RelearningSteps:  "10m",
		Timezone:         "UTC",
		DayStartHour:     4,
		ThemeMode:        "system",
		ThemeSeed:        "#3A4527",
		ThemeVariant:     "fidelity",
		ThemeContrast:    0,
	}
	if settings != wantDefaults {
		t.Errorf("CreateUserSettings() = %+v, want defaults %+v", settings, wantDefaults)
	}
	if settings.FsrsWeights.Valid || settings.ReminderTime.Valid || settings.TtsAuto {
		t.Errorf("nullable/default settings = %+v, want null weights/reminder and tts_auto=false", settings)
	}

	gotSettings, err := queries.GetUserSettings(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserSettings() error = %v", err)
	}
	if gotSettings != settings {
		t.Errorf("GetUserSettings() = %+v, want %+v", gotSettings, settings)
	}

	updated, err := queries.UpdateUserSettings(ctx, gen.UpdateUserSettingsParams{
		NewPerDay:        35,
		MaxReviewsPerDay: 250,
		RequestRetention: 0.92,
		MaximumInterval:  18000,
		LearningSteps:    "2m 15m",
		RelearningSteps:  "15m",
		FsrsWeights:      sql.NullString{String: "[1,2,3]", Valid: true},
		Timezone:         "Europe/Warsaw",
		DayStartHour:     5,
		ReminderTime:     sql.NullString{String: "19:30", Valid: true},
		TtsAuto:          true,
		ThemeMode:        "dark",
		ThemeSeed:        "#abcdef",
		ThemeVariant:     "tonal_spot",
		ThemeContrast:    0.5,
		UserID:           user.ID,
	})
	if err != nil {
		t.Fatalf("UpdateUserSettings() error = %v", err)
	}
	if updated.NewPerDay != 35 || updated.MaxReviewsPerDay != 250 || updated.RequestRetention != 0.92 ||
		updated.MaximumInterval != 18000 || updated.LearningSteps != "2m 15m" || updated.RelearningSteps != "15m" ||
		!updated.FsrsWeights.Valid || updated.FsrsWeights.String != "[1,2,3]" || updated.Timezone != "Europe/Warsaw" ||
		updated.DayStartHour != 5 || !updated.ReminderTime.Valid || updated.ReminderTime.String != "19:30" ||
		!updated.TtsAuto || updated.ThemeMode != "dark" || updated.ThemeSeed != "#abcdef" ||
		updated.ThemeVariant != "tonal_spot" || updated.ThemeContrast != 0.5 {
		t.Errorf("UpdateUserSettings() = %+v, want all supplied settings", updated)
	}
}

func TestAccountConstraintsAndForeignKeys(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	queries := database.Queries()
	user := createTestUser(t, queries, "member@example.com", false)

	if _, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email:        "MEMBER@example.com",
		Name:         "Duplicate",
		PasswordHash: "hash",
		CreatedAt:    time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	}); err == nil {
		t.Fatal("CreateUser() with case-insensitive duplicate email error = nil")
	}
	if _, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email:        "empty-name@example.com",
		Name:         "  ",
		PasswordHash: "hash",
		CreatedAt:    time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	}); err == nil {
		t.Fatal("CreateUser() with blank name error = nil")
	}
	if _, err := queries.CreateUserSettings(ctx, user.ID+1000); err == nil {
		t.Fatal("CreateUserSettings() with missing user error = nil")
	}
	if _, err := queries.CreateInvite(ctx, gen.CreateInviteParams{
		Code:      "orphan-invite",
		CreatedBy: user.ID + 1000,
		ExpiresAt: time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	}); err == nil {
		t.Fatal("CreateInvite() with missing creator error = nil")
	}
	if _, err := queries.CreateSession(ctx, gen.CreateSessionParams{
		TokenHash: make([]byte, 32),
		UserID:    user.ID + 1000,
		ExpiresAt: time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	}); err == nil {
		t.Fatal("CreateSession() with missing user error = nil")
	}
	if _, err := queries.CreateSession(ctx, gen.CreateSessionParams{
		TokenHash: nil,
		UserID:    user.ID,
		ExpiresAt: time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	}); err == nil {
		t.Fatal("CreateSession() with a null token hash error = nil")
	}

	settings, err := queries.CreateUserSettings(ctx, user.ID)
	if err != nil {
		t.Fatalf("CreateUserSettings() error = %v", err)
	}
	invalidUpdates := []struct {
		name string
		edit func(*gen.UpdateUserSettingsParams)
	}{
		{name: "negative new limit", edit: func(p *gen.UpdateUserSettingsParams) { p.NewPerDay = -1 }},
		{name: "zero retention", edit: func(p *gen.UpdateUserSettingsParams) { p.RequestRetention = 0 }},
		{name: "invalid day start", edit: func(p *gen.UpdateUserSettingsParams) { p.DayStartHour = 24 }},
		{name: "invalid theme mode", edit: func(p *gen.UpdateUserSettingsParams) { p.ThemeMode = "sepia" }},
		{name: "invalid theme seed", edit: func(p *gen.UpdateUserSettingsParams) { p.ThemeSeed = "green" }},
		{name: "invalid FSRS JSON", edit: func(p *gen.UpdateUserSettingsParams) { p.FsrsWeights = sql.NullString{String: "not-json", Valid: true} }},
	}
	for _, test := range invalidUpdates {
		t.Run(test.name, func(t *testing.T) {
			params := gen.UpdateUserSettingsParams{
				NewPerDay:        settings.NewPerDay,
				MaxReviewsPerDay: settings.MaxReviewsPerDay,
				RequestRetention: settings.RequestRetention,
				MaximumInterval:  settings.MaximumInterval,
				LearningSteps:    settings.LearningSteps,
				RelearningSteps:  settings.RelearningSteps,
				FsrsWeights:      settings.FsrsWeights,
				Timezone:         settings.Timezone,
				DayStartHour:     settings.DayStartHour,
				ReminderTime:     settings.ReminderTime,
				TtsAuto:          settings.TtsAuto,
				ThemeMode:        settings.ThemeMode,
				ThemeSeed:        settings.ThemeSeed,
				ThemeVariant:     settings.ThemeVariant,
				ThemeContrast:    settings.ThemeContrast,
				UserID:           user.ID,
			}
			test.edit(&params)
			if _, err := queries.UpdateUserSettings(ctx, params); err == nil {
				t.Fatal("UpdateUserSettings() error = nil, want constraint error")
			}
		})
	}

	if _, err := queries.GetInvite(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetInvite(missing) error = %v, want sql.ErrNoRows", err)
	}
	if _, err := queries.GetUserByID(ctx, user.ID+1000); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetUserByID(missing) error = %v, want sql.ErrNoRows", err)
	}
}

func TestInvitesAreSingleUseAndRespectExpiration(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	queries := database.Queries()
	creator := createTestUser(t, queries, "creator@example.com", true)
	consumer := createTestUser(t, queries, "consumer@example.com", false)
	createdAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	expiresAt := time.Date(2026, time.January, 9, 0, 0, 0, 0, time.UTC)

	invite, err := queries.CreateInvite(ctx, gen.CreateInviteParams{
		Code:      "single-use-code",
		CreatedBy: creator.ID,
		ExpiresAt: expiresAt,
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("CreateInvite() error = %v", err)
	}
	gotInvite, err := queries.GetInvite(ctx, invite.Code)
	if err != nil || gotInvite != invite {
		t.Fatalf("GetInvite() = %+v, %v; want %+v, nil", gotInvite, err, invite)
	}

	consumed, err := queries.ConsumeInvite(ctx, gen.ConsumeInviteParams{
		UsedBy: consumer.ID,
		Code:   invite.Code,
		Now:    time.Date(2026, time.January, 8, 23, 59, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ConsumeInvite() error = %v", err)
	}
	if !consumed.UsedBy.Valid || consumed.UsedBy.Int64 != consumer.ID {
		t.Errorf("ConsumeInvite().UsedBy = %+v, want %d", consumed.UsedBy, consumer.ID)
	}
	if _, err := queries.ConsumeInvite(ctx, gen.ConsumeInviteParams{
		UsedBy: creator.ID,
		Code:   invite.Code,
		Now:    time.Date(2026, time.January, 8, 23, 59, 0, 0, time.UTC),
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("second ConsumeInvite() error = %v, want sql.ErrNoRows", err)
	}

	_, err = queries.CreateInvite(ctx, gen.CreateInviteParams{
		Code:      "expired-code",
		CreatedBy: creator.ID,
		ExpiresAt: expiresAt,
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("CreateInvite(expired) error = %v", err)
	}
	if _, err := queries.ConsumeInvite(ctx, gen.ConsumeInviteParams{
		UsedBy: consumer.ID,
		Code:   "expired-code",
		Now:    expiresAt,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("ConsumeInvite(expired) error = %v, want sql.ErrNoRows", err)
	}

	if _, err := database.SQL.ExecContext(ctx, "DELETE FROM users WHERE id = ?", creator.ID); err == nil {
		t.Fatal("deleting an invite creator error = nil, want RESTRICT")
	}
	if _, err := database.SQL.ExecContext(ctx, "DELETE FROM users WHERE id = ?", consumer.ID); err == nil {
		t.Fatal("deleting an invite consumer error = nil, want RESTRICT")
	}
}

func TestSessionQueriesCleanupAndUserCascade(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	queries := database.Queries()
	user := createTestUser(t, queries, "sessions@example.com", false)
	createdAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	cutoff := time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC)

	makeSession := func(token byte, expiresAt time.Time) gen.Session {
		t.Helper()
		hash := make([]byte, 32)
		hash[0] = token
		session, err := queries.CreateSession(ctx, gen.CreateSessionParams{
			TokenHash: hash,
			UserID:    user.ID,
			ExpiresAt: expiresAt,
			UserAgent: sql.NullString{String: "integration-test", Valid: true},
			CreatedAt: createdAt,
		})
		if err != nil {
			t.Fatalf("CreateSession(%d) error = %v", token, err)
		}
		return session
	}

	expired := makeSession(1, cutoff.Add(-time.Second))
	active := makeSession(2, cutoff.Add(time.Second))
	gotSession, err := queries.GetSession(ctx, active.TokenHash)
	if err != nil || !slices.Equal(gotSession.TokenHash, active.TokenHash) || gotSession.UserID != active.UserID ||
		!gotSession.ExpiresAt.Equal(active.ExpiresAt) || gotSession.UserAgent != active.UserAgent ||
		!gotSession.CreatedAt.Equal(active.CreatedAt) {
		t.Fatalf("GetSession() = %+v, %v; want %+v, nil", gotSession, err, active)
	}

	deleted, err := queries.DeleteExpiredSessions(ctx, cutoff)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteExpiredSessions() = %d, %v; want 1, nil", deleted, err)
	}
	if _, err := queries.GetSession(ctx, expired.TokenHash); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetSession(expired) error = %v, want sql.ErrNoRows", err)
	}
	deleted, err = queries.DeleteSession(ctx, active.TokenHash)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteSession() = %d, %v; want 1, nil", deleted, err)
	}
	if deleted, err := queries.DeleteSession(ctx, active.TokenHash); err != nil || deleted != 0 {
		t.Errorf("DeleteSession(missing) = %d, %v; want 0, nil", deleted, err)
	}

	if _, err := queries.CreateUserSettings(ctx, user.ID); err != nil {
		t.Fatalf("CreateUserSettings() error = %v", err)
	}
	cascaded := makeSession(3, cutoff.Add(time.Hour))
	if _, err := database.SQL.ExecContext(ctx, "DELETE FROM users WHERE id = ?", user.ID); err != nil {
		t.Fatalf("delete user with dependent settings/session: %v", err)
	}
	if _, err := queries.GetUserSettings(ctx, user.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetUserSettings(deleted user) error = %v, want sql.ErrNoRows", err)
	}
	if _, err := queries.GetSession(ctx, cascaded.TokenHash); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetSession(cascaded) error = %v, want sql.ErrNoRows", err)
	}
}
