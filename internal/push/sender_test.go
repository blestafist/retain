package push

import (
	"context"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestWebPushSenderEncryptsPayloadAndUsesVAPID(t *testing.T) {
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys(): %v", err)
	}
	_, x, y, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate subscription public key: %v", err)
	}
	p256dh := base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), x, y))
	authKey := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))
	payload := []byte(`{"title":"Retain","body":"You have 2 cards to review."}`)
	config := Config{VAPIDPublicKey: publicKey, VAPIDPrivateKey: privateKey, VAPIDSubject: "admin@example.test"}
	sender := NewWebPushSender(config)
	var request *http.Request
	var encryptedBody []byte
	sender.client.Transport = roundTripFunc(func(got *http.Request) (*http.Response, error) {
		request = got
		body, err := io.ReadAll(got.Body)
		if err != nil {
			return nil, err
		}
		encryptedBody = body
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	status, err := sender.Send(context.Background(), Subscription{
		Endpoint: "https://fcm.googleapis.com/fcm/send/sender-test", P256dh: p256dh, Auth: authKey,
	}, payload)
	if err != nil {
		t.Fatalf("Send(): %v", err)
	}
	if status != http.StatusCreated {
		t.Fatalf("status = %d; want %d", status, http.StatusCreated)
	}
	if request == nil {
		t.Fatal("sender did not issue an HTTP request")
	}
	if request.Method != http.MethodPost || request.Header.Get("Content-Encoding") != "aes128gcm" {
		t.Errorf("request method/content encoding = %s/%q", request.Method, request.Header.Get("Content-Encoding"))
	}
	if !strings.HasPrefix(request.Header.Get("Authorization"), "vapid t=") {
		t.Errorf("Authorization header does not contain a VAPID token: %q", request.Header.Get("Authorization"))
	}
	if request.Header.Get("TTL") != "3600" {
		t.Errorf("TTL = %q; want 3600", request.Header.Get("TTL"))
	}
	if len(encryptedBody) == 0 || strings.Contains(string(encryptedBody), string(payload)) {
		t.Errorf("payload was not encrypted: body length %d", len(encryptedBody))
	}
	parts := strings.Split(request.Header.Get("Authorization"), ",")
	var token string
	for _, part := range parts {
		if strings.HasPrefix(part, "vapid t=") {
			token = strings.TrimPrefix(part, "vapid t=")
		}
	}
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		t.Fatalf("VAPID authorization token has %d segments; want 3", len(segments))
	}
	claims, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		t.Fatalf("decode VAPID claims: %v", err)
	}
	var claimBody map[string]any
	if err := json.Unmarshal(claims, &claimBody); err != nil {
		t.Fatalf("parse VAPID claims: %v", err)
	}
	if claimBody["sub"] != "mailto:admin@example.test" {
		t.Errorf("VAPID subject = %v; want mailto URI", claimBody["sub"])
	}
}
