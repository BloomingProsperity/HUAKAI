package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/shopspring/decimal"
)

const (
	headerSignature = "X-HUAKAI-Notification-Signature"
	headerEvent     = "X-HUAKAI-Notification-Event"
)

type EmailSender interface {
	SendTenantMessage(context.Context, int64, mailinfra.Message) error
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Config struct {
	Store       Store
	EmailSender EmailSender
	HTTPClient  HTTPDoer
	RateLimiter *RateLimiter
	Now         func() time.Time
}

type Notifier struct {
	store       Store
	emailSender EmailSender
	httpClient  HTTPDoer
	limiter     *RateLimiter
	now         func() time.Time
}

func NewNotifier(cfg Config) *Notifier {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = auth.NewSSRFProtectedOAuthClient(nil)
	}
	limiter := cfg.RateLimiter
	if limiter == nil {
		limiter = NewRateLimiter(time.Hour)
	}
	return &Notifier{
		store:       cfg.Store,
		emailSender: cfg.EmailSender,
		httpClient:  httpClient,
		limiter:     limiter,
		now:         now,
	}
}

func (n *Notifier) NotifyLowBalance(ctx context.Context, tenantID, userID int64, balance decimal.Decimal, billingEventID int64) error {
	if n == nil || n.store == nil {
		return nil
	}
	settings, err := n.store.GetSettings(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	settings = settings.normalized()
	if settings.NotifyType == TypeNone {
		return nil
	}
	threshold := settings.BalanceThreshold
	if threshold.IsZero() {
		threshold = DefaultLowBalanceThreshold
	}
	if threshold.IsNegative() {
		return fmt.Errorf("%w: balance_threshold", ErrInvalidSettings)
	}
	if !balance.LessThan(threshold) {
		return nil
	}
	settings.BalanceThreshold = threshold
	settings, err = ValidateSettings(settings)
	if err != nil {
		return err
	}
	now := n.now()
	if n.limiter != nil && !n.limiter.Allow(tenantID, userID, EventLowBalance, now) {
		return nil
	}
	return n.send(ctx, settings, Event{
		EventType:      EventLowBalance,
		TenantID:       tenantID,
		UserID:         userID,
		Balance:        balance,
		Threshold:      threshold,
		BillingEventID: billingEventID,
		OccurredAt:     now.UTC(),
	})
}

func (n *Notifier) send(ctx context.Context, settings Settings, event Event) error {
	switch settings.NotifyType {
	case TypeEmail:
		return n.sendEmail(ctx, settings, event)
	case TypeWebhook:
		return n.sendWebhook(ctx, settings, event)
	case TypeBark:
		return n.sendBark(ctx, settings, event)
	case TypeGotify:
		return n.sendGotify(ctx, settings, event)
	default:
		return nil
	}
}

func (n *Notifier) sendEmail(ctx context.Context, settings Settings, event Event) error {
	if n.emailSender == nil {
		return fmt.Errorf("%w: email sender unavailable", ErrDeliveryFailed)
	}
	subject := "HUAKAI low balance alert"
	if err := rejectHeaderInjection(settings.NotificationEmail); err != nil {
		return err
	}
	if err := rejectHeaderInjection(subject); err != nil {
		return err
	}
	body := fmt.Sprintf(
		"<p>Your HUAKAI balance is below the configured threshold.</p><p>Balance: %s</p><p>Threshold: %s</p>",
		html.EscapeString(event.Balance.StringFixedBank(8)),
		html.EscapeString(event.Threshold.StringFixedBank(8)),
	)
	msg := mailinfra.Message{
		TenantID: settings.TenantID,
		To:       settings.NotificationEmail,
		Subject:  subject,
		HTMLBody: body,
	}
	if err := n.emailSender.SendTenantMessage(ctx, settings.TenantID, msg); err != nil {
		return fmt.Errorf("%w: email", ErrDeliveryFailed)
	}
	return nil
}

func (n *Notifier) sendWebhook(ctx context.Context, settings Settings, event Event) error {
	if err := validateOutboundURL("webhook_url", settings.WebhookURL); err != nil {
		return err
	}
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	headers := map[string]string{
		headerEvent:     event.EventType,
		headerSignature: signWebhook(settings.WebhookSecret, body),
	}
	return n.postJSON(ctx, settings.WebhookURL, body, headers)
}

func (n *Notifier) sendBark(ctx context.Context, settings Settings, event Event) error {
	if err := validateOutboundURL("bark_url", settings.BarkURL); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"title": "HUAKAI low balance",
		"body":  "Balance " + event.Balance.StringFixedBank(8) + " is below " + event.Threshold.StringFixedBank(8),
	})
	if err != nil {
		return err
	}
	return n.postJSON(ctx, settings.BarkURL, body, map[string]string{headerEvent: event.EventType})
}

func (n *Notifier) sendGotify(ctx context.Context, settings Settings, event Event) error {
	if err := validateOutboundURL("gotify_url", settings.GotifyURL); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"title":    "HUAKAI low balance",
		"message":  "Balance " + event.Balance.StringFixedBank(8) + " is below " + event.Threshold.StringFixedBank(8),
		"priority": settings.GotifyPriority,
	})
	if err != nil {
		return err
	}
	return n.postJSON(ctx, settings.GotifyURL, body, map[string]string{
		headerEvent:    event.EventType,
		"X-Gotify-Key": settings.GotifyToken,
	})
}

func (n *Notifier) postJSON(ctx context.Context, rawURL string, body []byte, headers map[string]string) error {
	if n.httpClient == nil {
		return fmt.Errorf("%w: http client unavailable", ErrDeliveryFailed)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		if value == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: http", ErrDeliveryFailed)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: status %d", ErrDeliveryFailed, resp.StatusCode)
	}
	return nil
}

func signWebhook(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func validateOutboundURL(label, raw string) error {
	if err := userauth.ValidateOAuthEndpointURL(label, raw); err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafeEndpoint, err)
	}
	return nil
}

type RateLimiter struct {
	mu     sync.Mutex
	window time.Duration
	last   map[string]time.Time
}

func NewRateLimiter(window time.Duration) *RateLimiter {
	return &RateLimiter{
		window: window,
		last:   make(map[string]time.Time),
	}
}

func (l *RateLimiter) Allow(tenantID, userID int64, eventType string, now time.Time) bool {
	if l == nil || l.window <= 0 {
		return true
	}
	key := fmt.Sprintf("%d:%d:%s", tenantID, userID, eventType)
	l.mu.Lock()
	defer l.mu.Unlock()
	if last, ok := l.last[key]; ok && now.Sub(last) < l.window {
		return false
	}
	l.last[key] = now
	return true
}
