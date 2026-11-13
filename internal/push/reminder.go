package push

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
)

const reminderInterval = time.Minute

type ReminderWorker struct {
	database *db.DB
	clock    clock.Clock
	sender   Sender
	logger   *slog.Logger
}

type reminderPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
}

func NewReminderWorker(database *db.DB, c clock.Clock, sender Sender, logger *slog.Logger) *ReminderWorker {
	if c == nil {
		c = clock.Real{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ReminderWorker{database: database, clock: c, sender: sender, logger: logger}
}

func (w *ReminderWorker) Run(ctx context.Context) {
	if w == nil || w.database == nil || w.database.SQL == nil || w.sender == nil {
		return
	}
	ticker := time.NewTicker(reminderInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.ProcessAt(ctx, w.clock.Now()); err != nil && ctx.Err() == nil {
				w.logger.Error("process push reminders", "error", err)
			}
		}
	}
}

// ProcessAt sends at most one notification batch per user-local calendar day.
func (w *ReminderWorker) ProcessAt(ctx context.Context, now time.Time) error {
	if w == nil || w.database == nil || w.database.SQL == nil || w.sender == nil || w.clock == nil {
		return ErrUnavailable
	}
	queries := w.database.Queries()
	candidates, err := queries.ListReminderCandidates(ctx, now.UTC())
	if err != nil {
		return fmt.Errorf("list reminder candidates: %w", err)
	}
	for _, candidate := range candidates {
		if candidate.DueCount <= 0 || !candidate.ReminderTime.Valid {
			continue
		}
		location, err := time.LoadLocation(candidate.Timezone)
		if err != nil {
			w.logger.Warn("skip reminder with invalid time zone", "user_id", candidate.UserID)
			continue
		}
		localNow := now.In(location)
		if localNow.Format("15:04") != candidate.ReminderTime.String {
			continue
		}
		subscriptions, err := listSubscriptions(ctx, w.database, candidate.UserID)
		if err != nil {
			return err
		}
		if len(subscriptions) == 0 {
			continue
		}
		day := localNow.Format("2006-01-02")
		claimed, err := queries.ClaimReminderDay(ctx, gen.ClaimReminderDayParams{
			UserID: candidate.UserID, ReminderDay: day, AttemptedAt: now.UTC(),
		})
		if err != nil {
			return fmt.Errorf("claim reminder day: %w", err)
		}
		if claimed == 0 {
			continue
		}
		payload, err := json.Marshal(reminderPayload{
			Title: "Retain",
			Body:  dueReminderBody(candidate.DueCount),
			URL:   "/study",
		})
		if err != nil {
			return fmt.Errorf("encode reminder payload: %w", err)
		}
		for _, subscription := range subscriptions {
			status, err := w.sender.Send(ctx, Subscription{
				Endpoint: subscription.endpoint, P256dh: subscription.p256dh, Auth: subscription.auth,
			}, payload)
			if err != nil {
				// Sender errors may contain the endpoint URL, which is a bearer secret.
				w.logger.Warn("push delivery failed", "user_id", candidate.UserID)
				continue
			}
			if status == http.StatusGone {
				if err := deleteSubscription(ctx, w.database, subscription.id); err != nil {
					w.logger.Error("delete expired push subscription", "user_id", candidate.UserID, "error", err)
				}
				continue
			}
			if status < http.StatusOK || status >= http.StatusMultipleChoices {
				w.logger.Warn("push provider rejected reminder", "user_id", candidate.UserID, "status", status)
			}
		}
	}
	return nil
}

func dueReminderBody(count int64) string {
	unit := "cards"
	if count == 1 {
		unit = "card"
	}
	return fmt.Sprintf("You have %d %s to review.", count, unit)
}
