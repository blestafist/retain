package push

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type Sender interface {
	Send(context.Context, Subscription, []byte) (int, error)
}

type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

type WebPushSender struct {
	config Config
	client *http.Client
}

func NewWebPushSender(config Config) *WebPushSender {
	return &WebPushSender{
		config: config,
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (s *WebPushSender) Send(ctx context.Context, subscription Subscription, payload []byte) (int, error) {
	if s == nil || !s.config.Enabled() || !validEndpoint(subscription.Endpoint) {
		return 0, ErrUnavailable
	}
	response, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: subscription.Endpoint,
		Keys:     webpush.Keys{P256dh: subscription.P256dh, Auth: subscription.Auth},
	}, &webpush.Options{
		HTTPClient:     s.client,
		Subscriber:     normalizedSubject(s.config.VAPIDSubject),
		TTL:            3600,
		VAPIDPublicKey: s.config.VAPIDPublicKey, VAPIDPrivateKey: s.config.VAPIDPrivateKey,
	})
	if err != nil {
		return 0, err
	}
	if response == nil {
		return 0, io.ErrUnexpectedEOF
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	return response.StatusCode, nil
}

func normalizedSubject(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "mailto:") {
		return strings.TrimSpace(value[len("mailto:"):])
	}
	if strings.HasPrefix(strings.ToLower(value), "https://") {
		return "https:" + value[len("https:"):]
	}
	return value
}
