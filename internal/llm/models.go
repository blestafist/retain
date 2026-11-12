package llm

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/pagination"
)

// ListModels returns the IDs advertised by the configured OpenAI-compatible provider.
// The API key remains server-side, and provider response bodies are never returned to callers.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("llm context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	callerCtx := ctx
	operationCtx, cancel := context.WithTimeout(callerCtx, c.config.Timeout)
	defer cancel()

	lease, err := c.breaker.acquire(time.Now())
	if err != nil {
		return nil, err
	}
	defer func() { c.breaker.release(lease, time.Now()) }()

	contextError := func() error {
		err, operationTimeout := operationContextError(callerCtx, operationCtx)
		if operationTimeout {
			c.breaker.failure(lease, time.Now())
		}
		return err
	}

	var page *pagination.Page[openai.Model]
	for attempt := 0; ; attempt++ {
		page, err = c.api.Models.List(operationCtx)
		if contextErr := contextError(); contextErr != nil {
			return nil, contextErr
		}
		if err == nil {
			break
		}

		var apiErr *openai.Error
		if !errors.As(err, &apiErr) {
			c.breaker.failure(lease, time.Now())
			return nil, &UpstreamError{}
		}

		retryable := apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500 && apiErr.StatusCode < 600
		if retryable && attempt < c.config.MaxRetries {
			if err := waitBackoff(operationCtx, retryDelay(apiErr, attempt, c.config)); err != nil {
				if contextErr := contextError(); contextErr != nil {
					return nil, contextErr
				}
				return nil, &UpstreamError{}
			}
			continue
		}

		upstreamErr := &UpstreamError{StatusCode: apiErr.StatusCode}
		if retryable {
			c.breaker.failure(lease, time.Now())
		} else {
			c.breaker.success(lease)
		}
		return nil, upstreamErr
	}
	if err := contextError(); err != nil {
		return nil, err
	}
	if page == nil {
		c.breaker.success(lease)
		return nil, errors.New("llm provider returned an invalid model list")
	}

	ids := make([]string, 0, len(page.Data))
	seen := make(map[string]struct{}, len(page.Data))
	for _, model := range page.Data {
		if strings.TrimSpace(model.ID) == "" {
			continue
		}
		if _, ok := seen[model.ID]; ok {
			continue
		}
		seen[model.ID] = struct{}{}
		ids = append(ids, model.ID)
	}
	sort.Strings(ids)
	c.breaker.success(lease)
	return ids, nil
}
