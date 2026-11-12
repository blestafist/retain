package llmservice

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/pestit/retain/internal/llmstore"
)

type adminTestStore struct {
	settings map[string]llmstore.AppSetting
	usage    []llmstore.UsageAggregate
	getErr   error
}

func (s *adminTestStore) GetAppSetting(_ context.Context, key string) (llmstore.AppSetting, error) {
	if s.getErr != nil {
		return llmstore.AppSetting{}, s.getErr
	}
	setting, ok := s.settings[key]
	if !ok {
		return llmstore.AppSetting{}, llmstore.ErrNotFound
	}
	return setting, nil
}

func (s *adminTestStore) SetAppSetting(_ context.Context, setting llmstore.AppSetting) (llmstore.AppSetting, error) {
	if s.settings == nil {
		s.settings = make(map[string]llmstore.AppSetting)
	}
	s.settings[setting.Key] = setting
	return setting, nil
}

func (s *adminTestStore) SetAppSettingIfAbsent(_ context.Context, setting llmstore.AppSetting) (bool, error) {
	if s.settings == nil {
		s.settings = make(map[string]llmstore.AppSetting)
	}
	if _, exists := s.settings[setting.Key]; exists {
		return false, nil
	}
	s.settings[setting.Key] = setting
	return true, nil
}

func (s *adminTestStore) AggregateUsage(context.Context) ([]llmstore.UsageAggregate, error) {
	return append([]llmstore.UsageAggregate(nil), s.usage...), nil
}

type adminTestProvider struct {
	models []string
	err    error
	calls  int
}

func (p *adminTestProvider) ListModels(context.Context) ([]string, error) {
	p.calls++
	return append([]string(nil), p.models...), p.err
}

func TestAdminServiceInitializesOnlyMissingSetting(t *testing.T) {
	now := time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		setting    *llmstore.AppSetting
		want       string
		wantStored bool
	}{
		{name: "missing setting", want: `"configured-model"`, wantStored: true},
		{name: "existing selection", setting: &llmstore.AppSetting{Key: llmModelSettingKey, Value: json.RawMessage(`"admin-choice"`), UpdatedAt: now}, want: `"admin-choice"`, wantStored: true},
		{name: "malformed value preserved", setting: &llmstore.AppSetting{Key: llmModelSettingKey, Value: json.RawMessage(`17`), UpdatedAt: now}, want: `17`, wantStored: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &adminTestStore{settings: make(map[string]llmstore.AppSetting)}
			if test.setting != nil {
				store.settings[llmModelSettingKey] = *test.setting
			}
			service := NewAdminService(store, nil, "configured-model", serviceTestClock{now: now})
			if err := service.Initialize(context.Background()); err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}
			setting, exists := store.settings[llmModelSettingKey]
			if exists != test.wantStored {
				t.Fatalf("setting exists = %v, want %v", exists, test.wantStored)
			}
			if exists && string(setting.Value) != test.want {
				t.Errorf("stored setting = %s, want %s", setting.Value, test.want)
			}
			if test.setting == nil && !setting.UpdatedAt.Equal(now) {
				t.Errorf("default UpdatedAt = %s, want %s", setting.UpdatedAt, now)
			}
		})
	}
}

func TestAdminServiceModelsReturnsSafeStatusWhenDiscoveryUnavailable(t *testing.T) {
	store := &adminTestStore{settings: map[string]llmstore.AppSetting{
		llmModelSettingKey: {Key: llmModelSettingKey, Value: json.RawMessage(`"saved-model"`)},
	}}
	provider := &adminTestProvider{err: errors.New("provider leaked detail")}
	service := NewAdminService(store, provider, "configured-model", serviceTestClock{now: time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)})

	status, err := service.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	if status.Available || status.SelectedModel != "saved-model" || len(status.Models) != 0 {
		t.Fatalf("Models() = %+v, want unavailable with saved selection", status)
	}
	if status.Warning == "" || status.Warning == provider.err.Error() {
		t.Errorf("warning = %q, want a safe non-empty warning", status.Warning)
	}
}

func TestAdminServiceSetModelRequiresCurrentProviderList(t *testing.T) {
	now := time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		provider   *adminTestProvider
		model      string
		wantErr    error
		wantStored string
	}{
		{name: "listed model", provider: &adminTestProvider{models: []string{"new-model", "old-model"}}, model: "new-model", wantStored: `"new-model"`},
		{name: "unknown model", provider: &adminTestProvider{models: []string{"old-model"}}, model: "not-listed", wantErr: ErrUnknownModel},
		{name: "discovery unavailable", provider: &adminTestProvider{err: errors.New("secret")}, model: "new-model", wantErr: ErrModelDiscoveryUnavailable},
		{name: "blank model", provider: &adminTestProvider{models: []string{"new-model"}}, model: " ", wantErr: ErrInvalidModel},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &adminTestStore{settings: map[string]llmstore.AppSetting{
				llmModelSettingKey: {Key: llmModelSettingKey, Value: json.RawMessage(`"old-model"`)},
			}}
			service := NewAdminService(store, test.provider, "configured-model", serviceTestClock{now: now})
			got, err := service.SetModel(context.Background(), test.model)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("SetModel() error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil && got != test.model {
				t.Errorf("SetModel() = %q, want %q", got, test.model)
			}
			if stored := string(store.settings[llmModelSettingKey].Value); stored != func() string {
				if test.wantStored != "" {
					return test.wantStored
				}
				return `"old-model"`
			}() {
				t.Errorf("stored setting = %s, unexpected", stored)
			}
		})
	}
}

func TestAdminServiceResolveModelFailsClosedOnMalformedSetting(t *testing.T) {
	store := &adminTestStore{settings: map[string]llmstore.AppSetting{
		llmModelSettingKey: {Key: llmModelSettingKey, Value: json.RawMessage(`{"model":"unexpected"}`)},
	}}
	service := NewAdminService(store, nil, "configured-model", nil)
	if model, err := service.ResolveModel(context.Background()); !errors.Is(err, ErrInvalidModelSetting) || model != "" {
		t.Fatalf("ResolveModel() = %q, %v, want empty model and ErrInvalidModelSetting", model, err)
	}
	status, err := service.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	if status.SelectedModel != "" || status.Warning == "" {
		t.Errorf("Models() = %+v, want blank safe fallback and warning", status)
	}
}

func TestAdminServiceUsageReturnsLifetimeAggregates(t *testing.T) {
	want := []llmstore.UsageAggregate{{
		UserID: 7, UserName: "Learner", UserEmail: "learner@example.test", Model: "model-a", Feature: "ask",
		Requests: 3, CachedRequests: 1, PromptTokens: 20, CompletionTokens: 8,
	}}
	service := NewAdminService(&adminTestStore{usage: want}, nil, "", nil)
	got, err := service.Usage(context.Background())
	if err != nil {
		t.Fatalf("Usage() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Usage() = %+v, want %+v", got, want)
	}
}
