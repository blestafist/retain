package llmservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/llmstore"
)

const llmModelSettingKey = "llm_model"

var (
	ErrModelDiscoveryUnavailable = errors.New("LLM model discovery is unavailable")
	ErrUnknownModel              = errors.New("LLM model is not offered by the provider")
	ErrInvalidModel              = errors.New("invalid LLM model")
	ErrInvalidModelSetting       = errors.New("saved LLM model setting is invalid")
)

// ModelProvider is the provider capability used by the admin model controls.
type ModelProvider interface {
	ListModels(context.Context) ([]string, error)
}

// AdminStore is the persistence contract used by admin model controls.
// llmstore.Store is the production implementation.
type AdminStore interface {
	GetAppSetting(context.Context, string) (llmstore.AppSetting, error)
	SetAppSetting(context.Context, llmstore.AppSetting) (llmstore.AppSetting, error)
	SetAppSettingIfAbsent(context.Context, llmstore.AppSetting) (bool, error)
	AggregateUsage(context.Context) ([]llmstore.UsageAggregate, error)
}

// ModelStatus is the safe, public admin model-discovery response.
type ModelStatus struct {
	Models        []string `json:"models"`
	SelectedModel string   `json:"selected_model"`
	Available     bool     `json:"available"`
	Warning       string   `json:"warning"`
}

// LLMUsageAggregate is one lifetime user/model/feature row for admin reporting.
type LLMUsageAggregate = llmstore.UsageAggregate

// AdminService persists server-wide model selection and exposes admin-only
// model discovery and lifetime usage data. The HTTP layer enforces admin auth.
type AdminService struct {
	store        AdminStore
	provider     ModelProvider
	defaultModel string
	clock        clock.Clock
}

// NewAdminService constructs the LLM admin service. A nil provider represents
// a disabled or currently unavailable LLM endpoint.
func NewAdminService(store AdminStore, provider ModelProvider, defaultModel string, c clock.Clock) *AdminService {
	if c == nil {
		c = clock.Real{}
	}
	return &AdminService{
		store: store, provider: provider, defaultModel: strings.TrimSpace(defaultModel), clock: c,
	}
}

// Initialize persists the configured default only if no model has ever been
// selected. Existing values, including malformed values, are never overwritten.
// A malformed stored value instead makes ResolveModel fail closed.
func (s *AdminService) Initialize(ctx context.Context) error {
	if ctx == nil || s == nil || s.store == nil || s.clock == nil {
		return llmstore.ErrUnavailable
	}
	_, err := s.store.GetAppSetting(ctx, llmModelSettingKey)
	if err == nil {
		return nil
	}
	if !errors.Is(err, llmstore.ErrNotFound) {
		return fmt.Errorf("read selected LLM model: %w", err)
	}
	if s.defaultModel == "" {
		return nil
	}
	encoded, err := json.Marshal(s.defaultModel)
	if err != nil {
		return fmt.Errorf("encode configured LLM model: %w", err)
	}
	_, err = s.store.SetAppSettingIfAbsent(ctx, llmstore.AppSetting{
		Key: llmModelSettingKey, Value: encoded, UpdatedAt: s.clock.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("persist configured LLM model: %w", err)
	}
	return nil
}

// ResolveModel reads the persisted selection for one LLM request. It uses the
// configured default only when the setting is absent (normally before
// Initialize); malformed settings fail closed rather than silently switching.
func (s *AdminService) ResolveModel(ctx context.Context) (string, error) {
	if ctx == nil || s == nil || s.store == nil {
		return "", llmstore.ErrUnavailable
	}
	setting, err := s.store.GetAppSetting(ctx, llmModelSettingKey)
	if errors.Is(err, llmstore.ErrNotFound) {
		if s.defaultModel == "" {
			return "", ErrInvalidModelSetting
		}
		return s.defaultModel, nil
	}
	if err != nil {
		return "", fmt.Errorf("read selected LLM model: %w", err)
	}
	model, err := decodeModelSetting(setting.Value)
	if err != nil {
		return "", err
	}
	return model, nil
}

// Models returns the persisted selection even when provider discovery fails.
// The warning is a fixed, non-sensitive message and never contains provider data.
func (s *AdminService) Models(ctx context.Context) (ModelStatus, error) {
	if ctx == nil || s == nil || s.store == nil {
		return ModelStatus{}, llmstore.ErrUnavailable
	}
	status := ModelStatus{Models: []string{}}
	selected, err := s.ResolveModel(ctx)
	if err != nil {
		if errors.Is(err, ErrInvalidModelSetting) {
			status.Warning = "The saved model setting is invalid."
		} else {
			return ModelStatus{}, err
		}
	} else {
		status.SelectedModel = selected
	}

	if s.provider == nil {
		status.Warning = appendWarning(status.Warning, "The model provider is currently unavailable.")
		return status, nil
	}
	models, err := s.provider.ListModels(ctx)
	if err != nil {
		status.Warning = appendWarning(status.Warning, "The model provider is currently unavailable.")
		return status, nil
	}
	status.Models = cleanModelIDs(models)
	status.Available = true
	if status.SelectedModel != "" && !containsModel(status.Models, status.SelectedModel) {
		status.Warning = appendWarning(status.Warning, "The selected model is not listed by the provider.")
	}
	return status, nil
}

// SetModel updates the stored selection only when the ID appears in the
// provider's current model list.
func (s *AdminService) SetModel(ctx context.Context, model string) (string, error) {
	if ctx == nil || s == nil || s.store == nil {
		return "", llmstore.ErrUnavailable
	}
	if strings.TrimSpace(model) == "" || model != strings.TrimSpace(model) {
		return "", ErrInvalidModel
	}
	if s.provider == nil {
		return "", ErrModelDiscoveryUnavailable
	}
	models, err := s.provider.ListModels(ctx)
	if err != nil {
		return "", ErrModelDiscoveryUnavailable
	}
	if !containsModel(cleanModelIDs(models), model) {
		return "", ErrUnknownModel
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return "", ErrInvalidModel
	}
	_, err = s.store.SetAppSetting(ctx, llmstore.AppSetting{
		Key: llmModelSettingKey, Value: encoded, UpdatedAt: s.clock.Now().UTC(),
	})
	if err != nil {
		return "", fmt.Errorf("save selected LLM model: %w", err)
	}
	return model, nil
}

// Usage returns lifetime aggregates by user, model, and feature.
func (s *AdminService) Usage(ctx context.Context) ([]LLMUsageAggregate, error) {
	if ctx == nil || s == nil || s.store == nil {
		return nil, llmstore.ErrUnavailable
	}
	return s.store.AggregateUsage(ctx)
}

func decodeModelSetting(raw json.RawMessage) (string, error) {
	var model string
	if !json.Valid(raw) || json.Unmarshal(raw, &model) != nil || strings.TrimSpace(model) == "" {
		return "", ErrInvalidModelSetting
	}
	return model, nil
}

func cleanModelIDs(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	cleaned := make([]string, 0, len(models))
	for _, model := range models {
		if strings.TrimSpace(model) == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		cleaned = append(cleaned, model)
	}
	sort.Strings(cleaned)
	return cleaned
}

func containsModel(models []string, model string) bool {
	index := sort.SearchStrings(models, model)
	return index < len(models) && models[index] == model
}

func appendWarning(current, warning string) string {
	if current == "" {
		return warning
	}
	return current + " " + warning
}
