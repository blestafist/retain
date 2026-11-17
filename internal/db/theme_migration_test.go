package db

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/pestit/retain/internal/db/gen"
)

func TestThemeDefaultMigrationPreservesCustomizedSettings(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	provider := newEmbeddedMigrationProvider(t, database.SQL)
	if _, err := provider.DownTo(ctx, 11); err != nil {
		t.Fatalf("roll back theme default migration: %v", err)
	}
	assertMigrationVersion(t, ctx, database.SQL, 11)

	type themeCase struct {
		name, seed, variant, mode string
		contrast                  float64
		migrate                   bool
	}
	var cases []themeCase
	for _, mode := range []string{"light", "dark", "system"} {
		for _, contrast := range []float64{0, 0.5, 1} {
			cases = append(cases, themeCase{
				name: fmt.Sprintf("legacy-%s-%.1f", mode, contrast),
				seed: "#3A4527", variant: "fidelity", mode: mode, contrast: contrast,
				migrate: true,
			})
		}
	}
	for _, variant := range []string{"tonal_spot", "vibrant", "expressive", "monochrome"} {
		cases = append(cases, themeCase{
			name: "legacy-seed-" + variant,
			seed: "#3A4527", variant: variant, mode: "dark", contrast: 0.5,
		})
	}
	cases = append(cases,
		themeCase{name: "custom-seed-fidelity", seed: "#ABCDEF", variant: "fidelity", mode: "light", contrast: 1},
		themeCase{name: "custom-seed-and-variant", seed: "#ABCDEF", variant: "expressive", mode: "dark", contrast: 0.5},
		themeCase{name: "blue-fidelity", seed: "#2563EB", variant: "fidelity", mode: "system"},
		themeCase{name: "current-default", seed: "#2563EB", variant: "tonal_spot", mode: "light", contrast: 1},
		themeCase{name: "lowercase-legacy-seed", seed: "#3a4527", variant: "fidelity", mode: "dark", contrast: 0.5},
	)

	queries := database.Queries()
	want := make([]gen.UserSetting, 0, len(cases))
	for _, test := range cases {
		user := createTestUser(t, queries, test.name+"@example.com", false)
		// Exercise the original schema defaults, not the current account INSERT.
		if _, err := database.SQL.ExecContext(ctx, "INSERT INTO user_settings (user_id) VALUES (?)", user.ID); err != nil {
			t.Fatalf("insert legacy settings for %s: %v", test.name, err)
		}
		legacy, err := queries.GetUserSettings(ctx, user.ID)
		if err != nil {
			t.Fatalf("read legacy settings for %s: %v", test.name, err)
		}
		if legacy.ThemeSeed != "#3A4527" || legacy.ThemeVariant != "fidelity" {
			t.Fatalf("original schema defaults = %q, %q; want unchanged legacy defaults", legacy.ThemeSeed, legacy.ThemeVariant)
		}
		settings, err := queries.UpdateUserSettings(ctx, gen.UpdateUserSettingsParams{
			UserID:           user.ID,
			NewPerDay:        35,
			MaxReviewsPerDay: 250,
			RequestRetention: 0.95,
			MaximumInterval:  18000,
			LearningSteps:    "2m 15m",
			RelearningSteps:  "15m",
			FsrsWeights:      sql.NullString{String: "[1,2,3]", Valid: true},
			Timezone:         "Europe/Warsaw",
			DayStartHour:     5,
			ReminderTime:     sql.NullString{String: "19:30", Valid: true},
			TtsAuto:          true,
			ThemeMode:        test.mode,
			ThemeSeed:        test.seed,
			ThemeVariant:     test.variant,
			ThemeContrast:    test.contrast,
		})
		if err != nil {
			t.Fatalf("customize settings for %s: %v", test.name, err)
		}
		if test.migrate {
			settings.ThemeSeed = "#2563EB"
			settings.ThemeVariant = "tonal_spot"
		}
		want = append(want, settings)
	}

	assertSettings := func(stage string) {
		t.Helper()
		for index, expected := range want {
			t.Run(stage+"/"+cases[index].name, func(t *testing.T) {
				got, err := queries.GetUserSettings(ctx, expected.UserID)
				if err != nil {
					t.Fatalf("GetUserSettings() error = %v", err)
				}
				if got != expected {
					t.Errorf("settings after %s = %+v, want %+v", stage, got, expected)
				}
			})
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("apply theme default migration: %v", err)
	}
	assertMigrationVersion(t, ctx, database.SQL, 13)
	assertSettings("migrate")
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("reopen current migrations: %v", err)
	}
	assertSettings("repeat")
	if _, err := provider.DownTo(ctx, 11); err != nil {
		t.Fatalf("roll back data refresh without overwriting custom themes: %v", err)
	}
	assertSettings("rollback")
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("reapply theme default migration: %v", err)
	}
	assertSettings("reapply")
	assertForeignKeyCheckClean(t, ctx, database.SQL)
}
