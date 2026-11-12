// Package llmstore persists LLM settings, cache entries, usage reservations, and jobs.
package llmstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pestit/retain/internal/db/gen"
)

var (
	ErrInvalidInput         = errors.New("invalid LLM store input")
	ErrNotFound             = errors.New("LLM store resource not found")
	ErrUnavailable          = errors.New("LLM store unavailable")
	ErrInvalidJobTransition = errors.New("invalid job state transition")
	ErrJobCapacityReached   = errors.New("LLM job capacity reached")
)

// MaxJobResultBytes is the largest JSON result that may be persisted for a job.
const MaxJobResultBytes = 1 << 20

// DailyLimitReachedError means the caller's daily usage limit was reached.
type DailyLimitReachedError struct {
	Limit int64
}

func (e *DailyLimitReachedError) Error() string {
	return fmt.Sprintf("LLM daily limit reached (%d)", e.Limit)
}

// Store is a thin repository around the generated SQL queries.
type Store struct {
	queries *gen.Queries
}

func New(queries *gen.Queries) *Store {
	return &Store{queries: queries}
}

// AppSetting is a JSON-encoded application setting.
type AppSetting struct {
	Key       string
	Value     json.RawMessage
	UpdatedAt time.Time
}

// GetAppSetting reads one setting by key.
func (s *Store) GetAppSetting(ctx context.Context, key string) (AppSetting, error) {
	if !s.ready() {
		return AppSetting{}, ErrUnavailable
	}
	if strings.TrimSpace(key) == "" {
		return AppSetting{}, ErrInvalidInput
	}
	setting, err := s.queries.GetAppSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return AppSetting{}, ErrNotFound
	}
	if err != nil {
		return AppSetting{}, fmt.Errorf("get app setting: %w", err)
	}
	return AppSetting{Key: setting.Key, Value: json.RawMessage(setting.Value), UpdatedAt: setting.UpdatedAt.UTC()}, nil
}

// SetAppSetting upserts a JSON value using the caller-provided update time.
func (s *Store) SetAppSetting(ctx context.Context, setting AppSetting) (AppSetting, error) {
	if !s.ready() {
		return AppSetting{}, ErrUnavailable
	}
	if strings.TrimSpace(setting.Key) == "" || !json.Valid(setting.Value) || setting.UpdatedAt.IsZero() {
		return AppSetting{}, ErrInvalidInput
	}
	stored, err := s.queries.SetAppSetting(ctx, gen.SetAppSettingParams{
		Key: setting.Key, Value: string(setting.Value), UpdatedAt: setting.UpdatedAt.UTC(),
	})
	if err != nil {
		return AppSetting{}, fmt.Errorf("set app setting: %w", err)
	}
	return AppSetting{Key: stored.Key, Value: json.RawMessage(stored.Value), UpdatedAt: stored.UpdatedAt.UTC()}, nil
}

// SetAppSettingIfAbsent inserts a JSON setting only when the key does not yet
// exist. The conditional insert avoids overwriting a concurrent admin change.
func (s *Store) SetAppSettingIfAbsent(ctx context.Context, setting AppSetting) (bool, error) {
	if !s.ready() {
		return false, ErrUnavailable
	}
	if strings.TrimSpace(setting.Key) == "" || !json.Valid(setting.Value) || setting.UpdatedAt.IsZero() {
		return false, ErrInvalidInput
	}
	rows, err := s.queries.SetAppSettingIfAbsent(ctx, gen.SetAppSettingIfAbsentParams{
		Key: setting.Key, Value: string(setting.Value), UpdatedAt: setting.UpdatedAt.UTC(),
	})
	if err != nil {
		return false, fmt.Errorf("initialize app setting: %w", err)
	}
	return rows == 1, nil
}

// Usage is one per-user LLM request or cache-hit record.
type Usage struct {
	ID               int64
	UserID           int64
	Kind             string
	Model            string
	PromptTokens     int64
	CompletionTokens int64
	Cached           bool
	CreatedAt        time.Time
}

// ReserveUsageInput carries the full caller-computed UTC day window. CreatedAt
// is persisted in the same statement that checks the limit, before a provider
// request may begin.
type ReserveUsageInput struct {
	UserID     int64
	Kind       string
	Model      string
	DailyLimit int64
	DayStart   time.Time
	DayEnd     time.Time
	CreatedAt  time.Time
}

// ReserveUsage atomically checks the user's uncached request count and inserts
// its reservation. Concurrent callers cannot reserve past DailyLimit.
func (s *Store) ReserveUsage(ctx context.Context, input ReserveUsageInput) (Usage, error) {
	if !s.ready() {
		return Usage{}, ErrUnavailable
	}
	start, end, err := validateUsageWindow(input.UserID, input.Kind, input.Model, input.DayStart, input.DayEnd)
	if err != nil || input.DailyLimit < 0 || input.CreatedAt.IsZero() {
		return Usage{}, ErrInvalidInput
	}
	createdAt := input.CreatedAt.UTC()
	if createdAt.Before(start) || !createdAt.Before(end) {
		return Usage{}, ErrInvalidInput
	}
	row, err := s.queries.ReserveLLMUsage(ctx, gen.ReserveLLMUsageParams{
		UserID: input.UserID, Kind: input.Kind, Model: input.Model, CreatedAt: createdAt,
		DayStart: start, DayEnd: end, DailyLimit: input.DailyLimit,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Usage{}, &DailyLimitReachedError{Limit: input.DailyLimit}
	}
	if err != nil {
		return Usage{}, fmt.Errorf("reserve LLM usage: %w", err)
	}
	return usageFromRow(row), nil
}

// RecordCachedUsage records a cache hit without consuming provider-call quota.
func (s *Store) RecordCachedUsage(ctx context.Context, userID int64, kind, model string, createdAt time.Time) (Usage, error) {
	if !s.ready() {
		return Usage{}, ErrUnavailable
	}
	if userID <= 0 || strings.TrimSpace(kind) == "" || strings.TrimSpace(model) == "" || createdAt.IsZero() {
		return Usage{}, ErrInvalidInput
	}
	row, err := s.queries.RecordCachedLLMUsage(ctx, gen.RecordCachedLLMUsageParams{
		UserID: userID, Kind: kind, Model: model, CreatedAt: createdAt.UTC(),
	})
	if err != nil {
		return Usage{}, fmt.Errorf("record cached LLM usage: %w", err)
	}
	return usageFromRow(row), nil
}

// CompleteUsage records provider token counts on the reservation, scoped to its owner.
func (s *Store) CompleteUsage(ctx context.Context, userID, usageID, promptTokens, completionTokens int64) error {
	if !s.ready() {
		return ErrUnavailable
	}
	if userID <= 0 || usageID <= 0 || promptTokens < 0 || completionTokens < 0 {
		return ErrInvalidInput
	}
	_, err := s.queries.CompleteLLMUsage(ctx, gen.CompleteLLMUsageParams{
		PromptTokens: promptTokens, CompletionTokens: completionTokens, ID: usageID, UserID: userID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("complete LLM usage: %w", err)
	}
	return nil
}

// ListUsage returns only the caller's rows inside [startAt, endAt).
func (s *Store) ListUsage(ctx context.Context, userID int64, startAt, endAt time.Time) ([]Usage, error) {
	if !s.ready() {
		return nil, ErrUnavailable
	}
	start, end, err := validateBounds(startAt, endAt)
	if userID <= 0 || err != nil {
		return nil, ErrInvalidInput
	}
	rows, err := s.queries.ListLLMUsage(ctx, gen.ListLLMUsageParams{
		UserID: userID, StartAt: start, EndAt: end,
	})
	if err != nil {
		return nil, fmt.Errorf("list LLM usage: %w", err)
	}
	usage := make([]Usage, len(rows))
	for index, row := range rows {
		usage[index] = usageFromRow(row)
	}
	return usage, nil
}

// UsageAggregate is lifetime LLM usage grouped by user, model, and feature.
// Monetary cost is intentionally omitted because provider pricing is not stored.
type UsageAggregate struct {
	UserID           int64  `json:"user_id"`
	UserName         string `json:"user_name"`
	UserEmail        string `json:"user_email"`
	Model            string `json:"model"`
	Feature          string `json:"feature"`
	Requests         int64  `json:"requests"`
	CachedRequests   int64  `json:"cached_requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
}

// AggregateUsage returns deterministic lifetime aggregates for the admin panel.
func (s *Store) AggregateUsage(ctx context.Context) ([]UsageAggregate, error) {
	if !s.ready() {
		return nil, ErrUnavailable
	}
	rows, err := s.queries.AggregateLLMUsage(ctx)
	if err != nil {
		return nil, fmt.Errorf("aggregate LLM usage: %w", err)
	}
	aggregates := make([]UsageAggregate, len(rows))
	for index, row := range rows {
		aggregates[index] = UsageAggregate{
			UserID: row.UserID, UserName: row.UserName, UserEmail: row.UserEmail,
			Model: row.Model, Feature: row.Feature, Requests: row.Requests,
			CachedRequests: row.CachedRequests, PromptTokens: row.PromptTokens,
			CompletionTokens: row.CompletionTokens,
		}
	}
	return aggregates, nil
}

// CacheEntry contains a SHA-256 key and a JSON provider response.
type CacheEntry struct {
	Key           []byte
	Model         string
	PromptVersion int64
	Response      json.RawMessage
	CreatedAt     time.Time
}

// GetCache returns a cache entry and whether the key exists.
func (s *Store) GetCache(ctx context.Context, key []byte) (CacheEntry, bool, error) {
	if !s.ready() {
		return CacheEntry{}, false, ErrUnavailable
	}
	if len(key) != 32 {
		return CacheEntry{}, false, ErrInvalidInput
	}
	row, err := s.queries.GetLLMCache(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return CacheEntry{}, false, nil
	}
	if err != nil {
		return CacheEntry{}, false, fmt.Errorf("get LLM cache: %w", err)
	}
	return CacheEntry{
		Key: append([]byte(nil), row.Key...), Model: row.Model, PromptVersion: row.PromptVersion,
		Response: json.RawMessage(row.Response), CreatedAt: row.CreatedAt.UTC(),
	}, true, nil
}

// SetCache upserts a JSON response by its binary SHA-256 key.
func (s *Store) SetCache(ctx context.Context, entry CacheEntry) error {
	if !s.ready() {
		return ErrUnavailable
	}
	if len(entry.Key) != 32 || strings.TrimSpace(entry.Model) == "" || entry.PromptVersion <= 0 ||
		!json.Valid(entry.Response) || entry.CreatedAt.IsZero() {
		return ErrInvalidInput
	}
	_, err := s.queries.SetLLMCache(ctx, gen.SetLLMCacheParams{
		Key: entry.Key, Model: entry.Model, PromptVersion: entry.PromptVersion,
		Response: string(entry.Response), CreatedAt: entry.CreatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("set LLM cache: %w", err)
	}
	return nil
}

// Job is a persisted user-owned background task.
type Job struct {
	ID        string
	UserID    int64
	Kind      string
	Attempt   int64 // Monotonically increases whenever this job is claimed.
	Payload   json.RawMessage
	Result    json.RawMessage
	Status    string
	Progress  int64
	Total     int64
	Error     *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// JobUpdate contains mutable fields for one claimed attempt of a persisted job.
type JobUpdate struct {
	ID        string
	UserID    int64
	Attempt   int64 // Must match the current claim's fencing token.
	Status    string
	Progress  int64
	Total     int64
	Error     *string
	Result    json.RawMessage
	UpdatedAt time.Time
}

// CreateJob inserts a new queued job with caller-provided timestamps.
func (s *Store) CreateJob(ctx context.Context, job Job) (Job, error) {
	if !s.ready() {
		return Job{}, ErrUnavailable
	}
	if !validJob(job) {
		return Job{}, ErrInvalidInput
	}
	row, err := s.queries.CreateJob(ctx, gen.CreateJobParams{
		ID: job.ID, UserID: job.UserID, Kind: job.Kind, Payload: string(job.Payload), Total: job.Total,
		CreatedAt: job.CreatedAt.UTC(), UpdatedAt: job.UpdatedAt.UTC(),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Job{}, ErrJobCapacityReached
		}
		return Job{}, fmt.Errorf("create LLM job: %w", err)
	}
	return jobFromRow(row), nil
}

// ClaimJob atomically claims an owned queued job. At most one caller can
// transition the job to running; later claim attempts return
// ErrInvalidJobTransition.
func (s *Store) ClaimJob(ctx context.Context, userID int64, id string, updatedAt time.Time) (Job, error) {
	if !s.ready() {
		return Job{}, ErrUnavailable
	}
	if userID <= 0 || strings.TrimSpace(id) == "" || updatedAt.IsZero() {
		return Job{}, ErrInvalidInput
	}
	row, err := s.queries.ClaimJob(ctx, gen.ClaimJobParams{
		UpdatedAt: updatedAt.UTC(), ID: id, UserID: userID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, s.jobTransitionMiss(ctx, userID, id)
	}
	if err != nil {
		return Job{}, fmt.Errorf("claim LLM job: %w", err)
	}
	return jobFromRow(row), nil
}

// ClaimNextJob atomically claims the oldest queued job for an internal worker.
// A missing queue entry is reported as ErrNotFound.
func (s *Store) ClaimNextJob(ctx context.Context, updatedAt time.Time) (Job, error) {
	if !s.ready() {
		return Job{}, ErrUnavailable
	}
	if updatedAt.IsZero() {
		return Job{}, ErrInvalidInput
	}
	row, err := s.queries.ClaimNextJob(ctx, updatedAt.UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("claim next LLM job: %w", err)
	}
	return jobFromRow(row), nil
}

// RequeueRunningJobs recovers in-flight work after a process restart. Call it
// once before starting the worker pool, not while workers are active.
func (s *Store) RequeueRunningJobs(ctx context.Context, updatedAt time.Time) (int64, error) {
	if !s.ready() {
		return 0, ErrUnavailable
	}
	if updatedAt.IsZero() {
		return 0, ErrInvalidInput
	}
	count, err := s.queries.RequeueRunningJobs(ctx, updatedAt.UTC())
	if err != nil {
		return 0, fmt.Errorf("recover running LLM jobs: %w", err)
	}
	return count, nil
}

// RequeueJob releases one worker claim, for example when its context is
// canceled during graceful shutdown. Only the matching running attempt changes.
func (s *Store) RequeueJob(ctx context.Context, id string, attempt int64, updatedAt time.Time) error {
	if !s.ready() {
		return ErrUnavailable
	}
	if strings.TrimSpace(id) == "" || attempt < 0 || updatedAt.IsZero() {
		return ErrInvalidInput
	}
	count, err := s.queries.RequeueJob(ctx, gen.RequeueJobParams{ID: id, Attempt: attempt, UpdatedAt: updatedAt.UTC()})
	if err != nil {
		return fmt.Errorf("requeue LLM job: %w", err)
	}
	if count == 0 {
		return ErrInvalidJobTransition
	}
	return nil
}

// GetJob conceals jobs belonging to other users as not found.
func (s *Store) GetJob(ctx context.Context, userID int64, id string) (Job, error) {
	if !s.ready() {
		return Job{}, ErrUnavailable
	}
	if userID <= 0 || strings.TrimSpace(id) == "" {
		return Job{}, ErrInvalidInput
	}
	row, err := s.queries.GetJob(ctx, gen.GetJobParams{ID: id, UserID: userID})
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("get LLM job: %w", err)
	}
	return jobFromRow(row), nil
}

// UpdateJob updates task progress only when the supplied owner matches.
func (s *Store) UpdateJob(ctx context.Context, update JobUpdate) (Job, error) {
	if !s.ready() {
		return Job{}, ErrUnavailable
	}
	if strings.TrimSpace(update.ID) == "" || update.UserID <= 0 || update.Attempt < 0 ||
		update.Progress < 0 || update.Total < update.Progress || update.UpdatedAt.IsZero() {
		return Job{}, ErrInvalidInput
	}
	if update.Status == "queued" {
		return Job{}, ErrInvalidJobTransition
	}
	if !validJobUpdateStatus(update.Status) {
		return Job{}, ErrInvalidInput
	}
	if len(update.Result) > MaxJobResultBytes {
		return Job{}, ErrInvalidInput
	}
	if len(update.Result) > 0 && !json.Valid(update.Result) {
		return Job{}, ErrInvalidInput
	}
	if len(update.Result) > 0 && update.Status != "done" {
		return Job{}, ErrInvalidJobTransition
	}
	row, err := s.queries.UpdateJob(ctx, gen.UpdateJobParams{
		Status: update.Status, Progress: update.Progress, Total: update.Total,
		Error: nullableString(update.Error), Result: nullableString(rawMessageString(update.Result)),
		UpdatedAt: update.UpdatedAt.UTC(), ID: update.ID, UserID: update.UserID, Attempt: update.Attempt,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, s.jobTransitionMiss(ctx, update.UserID, update.ID)
	}
	if err != nil {
		return Job{}, fmt.Errorf("update LLM job: %w", err)
	}
	return jobFromRow(row), nil
}

func (s *Store) ready() bool { return s != nil && s.queries != nil }

// jobTransitionMiss distinguishes a rejected transition from an absent or
// foreign-owned job without exposing another user's job.
func (s *Store) jobTransitionMiss(ctx context.Context, userID int64, id string) error {
	_, err := s.queries.GetJob(ctx, gen.GetJobParams{ID: id, UserID: userID})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check LLM job after rejected transition: %w", err)
	}
	return ErrInvalidJobTransition
}

func validateUsageWindow(userID int64, kind, model string, startAt, endAt time.Time) (time.Time, time.Time, error) {
	start, end, err := validateBounds(startAt, endAt)
	if userID <= 0 || strings.TrimSpace(kind) == "" || strings.TrimSpace(model) == "" || err != nil {
		return time.Time{}, time.Time{}, ErrInvalidInput
	}
	return start, end, nil
}

func validateBounds(startAt, endAt time.Time) (time.Time, time.Time, error) {
	if startAt.IsZero() || endAt.IsZero() || !endAt.After(startAt) {
		return time.Time{}, time.Time{}, ErrInvalidInput
	}
	return startAt.UTC(), endAt.UTC(), nil
}

func validJob(job Job) bool {
	return strings.TrimSpace(job.ID) != "" && job.UserID > 0 && strings.TrimSpace(job.Kind) != "" &&
		json.Valid(job.Payload) && len(job.Result) == 0 && job.Status == "queued" && job.Progress == 0 && job.Error == nil && job.Total >= job.Progress &&
		!job.CreatedAt.IsZero() && !job.UpdatedAt.IsZero()
}

func validJobUpdateStatus(status string) bool {
	switch status {
	case "running", "done", "failed":
		return true
	default:
		return false
	}
}

func nullableString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func rawMessageString(value json.RawMessage) *string {
	if len(value) == 0 {
		return nil
	}
	message := string(value)
	return &message
}

func usageFromRow(row gen.LlmUsage) Usage {
	return Usage{
		ID: row.ID, UserID: row.UserID, Kind: row.Kind, Model: row.Model,
		PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens,
		Cached: row.Cached, CreatedAt: row.CreatedAt.UTC(),
	}
}

func jobFromRow(row gen.Job) Job {
	job := Job{
		ID: row.ID, UserID: row.UserID, Kind: row.Kind, Attempt: row.Attempt, Payload: json.RawMessage(row.Payload), Status: row.Status,
		Progress: row.Progress, Total: row.Total, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
	if row.Result.Valid {
		job.Result = json.RawMessage(row.Result.String)
	}
	if row.Error.Valid {
		message := row.Error.String
		job.Error = &message
	}
	return job
}
