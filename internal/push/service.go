// Package push implements opt-in Web Push reminders.
package push

import (
	"context"
	"crypto/elliptic"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"

	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
)

var (
	ErrInvalidInput = errors.New("invalid push input")
	ErrUnavailable  = errors.New("push notifications are unavailable")
	ErrConflict     = errors.New("push subscription belongs to another user")
	ErrInternal     = errors.New("push service error")
)

type Config struct {
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string
}

func (c Config) Enabled() bool {
	return strings.TrimSpace(c.VAPIDPublicKey) != "" &&
		strings.TrimSpace(c.VAPIDPrivateKey) != "" &&
		validSubject(c.VAPIDSubject)
}

type SubscriptionInput struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

type Service struct {
	database *db.DB
	config   Config
}

func NewService(database *db.DB, config Config) *Service {
	return &Service{database: database, config: config}
}

func (s *Service) PublicKey() (string, error) {
	if !s.ready() || !s.config.Enabled() {
		return "", ErrUnavailable
	}
	return s.config.VAPIDPublicKey, nil
}

func (s *Service) Subscribe(ctx context.Context, userID int64, input SubscriptionInput) error {
	if !s.ready() || userID <= 0 {
		return ErrInvalidInput
	}
	if !s.config.Enabled() {
		return ErrUnavailable
	}
	if !validEndpoint(input.Endpoint) || !validSubscriptionKeys(input.Keys.P256dh, input.Keys.Auth) {
		return ErrInvalidInput
	}
	if _, err := s.database.Queries().UpsertPushSubscription(ctx, gen.UpsertPushSubscriptionParams{
		UserID: userID, Endpoint: input.Endpoint,
		P256dh: input.Keys.P256dh, Auth: input.Keys.Auth,
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("save push subscription: %w: %v", ErrInternal, err)
	}
	return nil
}

func (s *Service) Unsubscribe(ctx context.Context, userID int64, endpoint string) error {
	if !s.ready() || userID <= 0 {
		return ErrInvalidInput
	}
	if !validEndpoint(endpoint) {
		return ErrInvalidInput
	}
	if _, err := s.database.Queries().DeletePushSubscription(ctx, gen.DeletePushSubscriptionParams{
		UserID: userID, Endpoint: endpoint,
	}); err != nil {
		return fmt.Errorf("delete push subscription: %w: %v", ErrInternal, err)
	}
	return nil
}

func (s *Service) ready() bool {
	return s != nil && s.database != nil && s.database.SQL != nil
}

func validEndpoint(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || !strings.EqualFold(parsed.Scheme, "https") ||
		parsed.User != nil || parsed.Hostname() == "" || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.ForceQuery {
		return false
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return false
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "fcm.googleapis.com", "updates.push.services.mozilla.com", "push.services.mozilla.com", "web.push.apple.com":
		return parsed.Path != "" && len(value) <= 4096
	default:
		return false
	}
}

func validSubscriptionKeys(p256dh, auth string) bool {
	publicKey, ok := decodeBase64URL(p256dh)
	if !ok || len(publicKey) != 65 {
		return false
	}
	if x, y := elliptic.Unmarshal(elliptic.P256(), publicKey); x == nil || y == nil {
		return false
	}
	secret, ok := decodeBase64URL(auth)
	return ok && len(secret) == 16
}

func decodeBase64URL(value string) ([]byte, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
	return decoded, err == nil
}

type storedSubscription struct {
	id       int64
	endpoint string
	p256dh   string
	auth     string
}

func listSubscriptions(ctx context.Context, database *db.DB, userID int64) ([]storedSubscription, error) {
	rows, err := database.Queries().ListPushSubscriptionsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list push subscriptions: %w", err)
	}
	result := make([]storedSubscription, 0, len(rows))
	for _, row := range rows {
		result = append(result, storedSubscription{id: row.ID, endpoint: row.Endpoint, p256dh: row.P256dh, auth: row.Auth})
	}
	return result, nil
}

func deleteSubscription(ctx context.Context, database *db.DB, id int64) error {
	if _, err := database.Queries().DeletePushSubscriptionByID(ctx, id); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("delete expired push subscription: %w", err)
	}
	return nil
}

func validSubject(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "mailto:") {
		value = strings.TrimSpace(value[len("mailto:"):])
	}
	if strings.HasPrefix(strings.ToLower(value), "https://") {
		parsed, err := url.Parse(value)
		return err == nil && parsed.IsAbs() && strings.EqualFold(parsed.Scheme, "https") && parsed.Hostname() != "" && parsed.User == nil && parsed.Fragment == ""
	}
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value && !strings.ContainsAny(value, "\r\n")
}
