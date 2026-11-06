package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/pestit/retain/internal/db/gen"
)

// UpdateSettings applies a partial flat snake_case settings patch and returns
// the full result. Reading and writing occur in one transaction to avoid lost
// updates between concurrent patches.
func (s *Service) UpdateSettings(ctx context.Context, userID int64, patch map[string]json.RawMessage) (Settings, error) {
	if !s.ready() {
		return Settings{}, ErrInternal
	}
	for key := range patch {
		if !isSettingsPatchKey(key) {
			return Settings{}, ErrInvalidInput
		}
	}

	tx, queries, err := s.begin(ctx)
	if err != nil {
		return Settings{}, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := queries.GetUserSettings(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, ErrUnauthenticated
	}
	if err != nil {
		return Settings{}, ErrInternal
	}
	updated, err := applySettingsPatch(current, patch)
	if err != nil {
		return Settings{}, err
	}
	if err := validateSettings(updated); err != nil {
		return Settings{}, ErrInvalidInput
	}

	result, err := queries.UpdateUserSettings(ctx, gen.UpdateUserSettingsParams{
		NewPerDay:        updated.NewPerDay,
		MaxReviewsPerDay: updated.MaxReviewsPerDay,
		RequestRetention: updated.RequestRetention,
		MaximumInterval:  updated.MaximumInterval,
		LearningSteps:    updated.LearningSteps,
		RelearningSteps:  updated.RelearningSteps,
		FsrsWeights:      updated.FsrsWeights,
		Timezone:         updated.Timezone,
		DayStartHour:     updated.DayStartHour,
		ReminderTime:     updated.ReminderTime,
		TtsAuto:          updated.TtsAuto,
		ThemeMode:        updated.ThemeMode,
		ThemeSeed:        updated.ThemeSeed,
		ThemeVariant:     updated.ThemeVariant,
		ThemeContrast:    updated.ThemeContrast,
		UserID:           userID,
	})
	if err != nil {
		return Settings{}, ErrInternal
	}
	if err := tx.Commit(); err != nil {
		return Settings{}, ErrInternal
	}
	return publicSettings(result), nil
}

func isSettingsPatchKey(key string) bool {
	switch key {
	case "new_per_day", "max_reviews_per_day", "request_retention", "maximum_interval",
		"learning_steps", "relearning_steps", "fsrs_weights", "timezone", "day_start_hour",
		"reminder_time", "tts_auto", "theme_mode", "theme_seed", "theme_variant", "theme_contrast":
		return true
	default:
		return false
	}
}

func applySettingsPatch(current gen.UserSetting, patch map[string]json.RawMessage) (gen.UserSetting, error) {
	for key, raw := range patch {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			return gen.UserSetting{}, ErrInvalidInput
		}
		isNull := bytes.Equal(raw, []byte("null"))
		switch key {
		case "new_per_day":
			if err := decodeNonNull(raw, &current.NewPerDay); err != nil {
				return gen.UserSetting{}, err
			}
		case "max_reviews_per_day":
			if err := decodeNonNull(raw, &current.MaxReviewsPerDay); err != nil {
				return gen.UserSetting{}, err
			}
		case "request_retention":
			if err := decodeNonNull(raw, &current.RequestRetention); err != nil {
				return gen.UserSetting{}, err
			}
		case "maximum_interval":
			if err := decodeNonNull(raw, &current.MaximumInterval); err != nil {
				return gen.UserSetting{}, err
			}
		case "learning_steps":
			if err := decodeNonNull(raw, &current.LearningSteps); err != nil {
				return gen.UserSetting{}, err
			}
		case "relearning_steps":
			if err := decodeNonNull(raw, &current.RelearningSteps); err != nil {
				return gen.UserSetting{}, err
			}
		case "fsrs_weights":
			if isNull {
				current.FsrsWeights = sql.NullString{}
				continue
			}
			compact, err := compactFSRSWeights(raw)
			if err != nil {
				return gen.UserSetting{}, err
			}
			current.FsrsWeights = sql.NullString{String: compact, Valid: true}
		case "timezone":
			if err := decodeNonNull(raw, &current.Timezone); err != nil {
				return gen.UserSetting{}, err
			}
		case "day_start_hour":
			if err := decodeNonNull(raw, &current.DayStartHour); err != nil {
				return gen.UserSetting{}, err
			}
		case "reminder_time":
			if isNull {
				current.ReminderTime = sql.NullString{}
				continue
			}
			var reminder string
			if err := decodeNonNull(raw, &reminder); err != nil {
				return gen.UserSetting{}, err
			}
			current.ReminderTime = sql.NullString{String: reminder, Valid: true}
		case "tts_auto":
			if err := decodeNonNull(raw, &current.TtsAuto); err != nil {
				return gen.UserSetting{}, err
			}
		case "theme_mode":
			if err := decodeNonNull(raw, &current.ThemeMode); err != nil {
				return gen.UserSetting{}, err
			}
		case "theme_seed":
			if err := decodeNonNull(raw, &current.ThemeSeed); err != nil {
				return gen.UserSetting{}, err
			}
			current.ThemeSeed = strings.ToUpper(current.ThemeSeed)
		case "theme_variant":
			if err := decodeNonNull(raw, &current.ThemeVariant); err != nil {
				return gen.UserSetting{}, err
			}
		case "theme_contrast":
			if err := decodeNonNull(raw, &current.ThemeContrast); err != nil {
				return gen.UserSetting{}, err
			}
		default:
			return gen.UserSetting{}, ErrInvalidInput
		}
	}
	return current, nil
}

func decodeNonNull(raw json.RawMessage, destination any) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ErrInvalidInput
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return ErrInvalidInput
	}
	return nil
}

func compactFSRSWeights(raw []byte) (string, error) {
	var values []any
	if err := json.Unmarshal(raw, &values); err != nil {
		return "", ErrInvalidInput
	}
	if len(values) != 17 && len(values) != 19 {
		return "", ErrInvalidInput
	}
	for _, value := range values {
		weight, ok := value.(float64)
		if !ok || math.IsNaN(weight) || math.IsInf(weight, 0) {
			return "", ErrInvalidInput
		}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", ErrInvalidInput
	}
	return compact.String(), nil
}

func validateSettings(settings gen.UserSetting) error {
	if settings.NewPerDay < 0 || settings.MaxReviewsPerDay < 0 ||
		math.IsNaN(settings.RequestRetention) || math.IsInf(settings.RequestRetention, 0) ||
		settings.RequestRetention <= 0 || settings.RequestRetention > 1 || settings.MaximumInterval <= 0 ||
		!validSteps(settings.LearningSteps) || !validSteps(settings.RelearningSteps) {
		return ErrInvalidInput
	}
	if strings.TrimSpace(settings.Timezone) == "" {
		return ErrInvalidInput
	}
	if _, err := time.LoadLocation(settings.Timezone); err != nil {
		return ErrInvalidInput
	}
	if settings.DayStartHour < 0 || settings.DayStartHour > 23 {
		return ErrInvalidInput
	}
	if settings.ReminderTime.Valid && !validReminderTime(settings.ReminderTime.String) {
		return ErrInvalidInput
	}
	if settings.ThemeMode != "light" && settings.ThemeMode != "dark" && settings.ThemeMode != "system" {
		return ErrInvalidInput
	}
	if !validThemeSeed(settings.ThemeSeed) {
		return ErrInvalidInput
	}
	switch settings.ThemeVariant {
	case "fidelity", "tonal_spot", "vibrant", "expressive", "monochrome":
	default:
		return ErrInvalidInput
	}
	if settings.ThemeContrast != 0 && settings.ThemeContrast != 0.5 && settings.ThemeContrast != 1 {
		return ErrInvalidInput
	}
	if settings.FsrsWeights.Valid {
		if _, err := compactFSRSWeights([]byte(settings.FsrsWeights.String)); err != nil {
			return ErrInvalidInput
		}
	}
	return nil
}

func validSteps(value string) bool {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		duration, err := time.ParseDuration(field)
		if err != nil || duration <= 0 {
			return false
		}
	}
	return true
}

func validReminderTime(value string) bool {
	if len(value) != 5 || value[2] != ':' {
		return false
	}
	if _, err := strconv.Atoi(value[:2]); err != nil {
		return false
	}
	if _, err := strconv.Atoi(value[3:]); err != nil {
		return false
	}
	parsed, err := time.Parse("15:04", value)
	return err == nil && parsed.Format("15:04") == value
}

func validThemeSeed(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, r := range value[1:] {
		if !((r >= '0' && r <= '9') || (r >= 'A' && r <= 'F') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
