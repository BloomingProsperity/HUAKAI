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
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
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
	// NotifyTypeCacheTTL bounds the in-process notify_type cache that lets the
	// settlement hot path skip the GetSettings DB read while notifications are
	// disabled. Zero selects DefaultNotifyTypeCacheTTL; negative disables the
	// cache (every call reads the DB).
	NotifyTypeCacheTTL time.Duration
}

type Notifier struct {
	store       Store
	emailSender EmailSender
	httpClient  HTTPDoer
	limiter     *RateLimiter
	now         func() time.Time
	typeCache   *notifyTypeCache
}

type activeSettingsLister interface {
	ListActiveSettings(context.Context, int64) ([]Settings, error)
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
	cacheTTL := cfg.NotifyTypeCacheTTL
	if cacheTTL == 0 {
		cacheTTL = DefaultNotifyTypeCacheTTL
	}
	return &Notifier{
		store:       cfg.Store,
		emailSender: cfg.EmailSender,
		httpClient:  httpClient,
		limiter:     limiter,
		now:         now,
		typeCache:   newNotifyTypeCache(cacheTTL),
	}
}

func (n *Notifier) NotifyLowBalance(ctx context.Context, tenantID, userID int64, balance decimal.Decimal, billingEventID int64) error {
	if n == nil || n.store == nil {
		return nil
	}
	// Cheap in-process short-circuit first: when a fresh cache entry says this
	// (tenant,user) has notifications disabled, skip the GetSettings DB read so
	// the common notify_type=none settlement never touches the shared DB pool.
	if n.typeCache.disabled(tenantID, userID, n.now()) {
		return nil
	}
	settings, err := n.store.GetSettings(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	settings = settings.normalized()
	n.typeCache.store(tenantID, userID, settings.NotifyType, n.now())
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

func (n *Notifier) NotifyAlertFiring(ctx context.Context, tenantID int64, alert AlertFiringInfo) error {
	if n == nil || n.store == nil {
		return nil
	}
	if tenantID <= 0 {
		return fmt.Errorf("%w: tenant_id", ErrInvalidSettings)
	}
	now := n.now().UTC()
	alert, err := normalizeAlertFiringInfo(alert, now)
	if err != nil {
		return err
	}
	return n.broadcast(ctx, tenantID, EventAlertFiring, now, func(ctx context.Context, settings Settings) error {
		return n.sendAlertFiring(ctx, settings, alert, now)
	})
}

// broadcast fans a tenant-level operational notification out to every active
// per-(tenant,user) notification channel: it resolves ListActiveSettings, skips
// notify_type=none recipients, re-validates each recipient's settings, enforces
// the tenant boundary, and applies the per-(tenant,user,eventType) rate limit
// before invoking deliver for the surviving recipients. NotifyAlertFiring and
// NotifyProviderAccountDown share this loop so the multi-channel pipeline is
// reused, not forked. deliver receives validated, tenant-matched settings.
func (n *Notifier) broadcast(ctx context.Context, tenantID int64, eventType string, now time.Time, deliver func(context.Context, Settings) error) error {
	lister, ok := n.store.(activeSettingsLister)
	if !ok {
		return nil
	}
	settingsList, err := lister.ListActiveSettings(ctx, tenantID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, settings := range settingsList {
		settings = settings.normalized()
		if settings.NotifyType == TypeNone {
			continue
		}
		settings, err = ValidateSettings(settings)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if settings.TenantID != tenantID {
			if firstErr == nil {
				firstErr = fmt.Errorf("%w: tenant_id mismatch", ErrInvalidSettings)
			}
			continue
		}
		if n.limiter != nil && !n.limiter.Allow(tenantID, settings.UserID, eventType, now) {
			continue
		}
		if err := deliver(ctx, settings); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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

func (n *Notifier) sendAlertFiring(ctx context.Context, settings Settings, alert AlertFiringInfo, occurredAt time.Time) error {
	switch settings.NotifyType {
	case TypeEmail:
		return n.sendAlertFiringEmail(ctx, settings, alert)
	case TypeWebhook:
		return n.sendAlertFiringWebhook(ctx, settings, alert, occurredAt)
	case TypeBark:
		return n.sendAlertFiringBark(ctx, settings, alert)
	case TypeGotify:
		return n.sendAlertFiringGotify(ctx, settings, alert)
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
	n.sendExtraEmailCopies(ctx, settings, msg)
	return nil
}

func (n *Notifier) sendAlertFiringEmail(ctx context.Context, settings Settings, alert AlertFiringInfo) error {
	if n.emailSender == nil {
		return fmt.Errorf("%w: email sender unavailable", ErrDeliveryFailed)
	}
	subject := "HUAKAI alert firing: " + alert.RuleName
	if err := rejectHeaderInjection(settings.NotificationEmail); err != nil {
		return err
	}
	if err := rejectHeaderInjection(subject); err != nil {
		return err
	}
	body := fmt.Sprintf(
		"<p>A HUAKAI alert is firing.</p><p>Rule: %s</p><p>Metric: %s</p><p>Condition: %s %s</p><p>Observed: %s</p><p>Severity: %s</p><p>Dimensions: %s</p><p>Fired at: %s</p>",
		html.EscapeString(alert.RuleName),
		html.EscapeString(alert.Metric),
		html.EscapeString(alert.Comparator),
		html.EscapeString(formatAlertFloat(alert.Threshold)),
		html.EscapeString(formatAlertFloat(alert.ObservedValue)),
		html.EscapeString(alert.Severity),
		html.EscapeString(formatAlertDimensions(alert.Dimensions)),
		html.EscapeString(alert.FiredAt.UTC().Format(time.RFC3339)),
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
	n.sendExtraEmailCopies(ctx, settings, msg)
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

func (n *Notifier) sendAlertFiringWebhook(ctx context.Context, settings Settings, alert AlertFiringInfo, occurredAt time.Time) error {
	if err := validateOutboundURL("webhook_url", settings.WebhookURL); err != nil {
		return err
	}
	body, err := json.Marshal(alertFiringPayload{
		EventType:     EventAlertFiring,
		TenantID:      settings.TenantID,
		UserID:        settings.UserID,
		RuleName:      alert.RuleName,
		Metric:        alert.Metric,
		MetricType:    alert.MetricType,
		Comparator:    alert.Comparator,
		Threshold:     alert.Threshold,
		Severity:      alert.Severity,
		ObservedValue: alert.ObservedValue,
		Dimensions:    cloneAlertDimensions(alert.Dimensions),
		FiredAt:       alert.FiredAt.UTC(),
		OccurredAt:    occurredAt.UTC(),
	})
	if err != nil {
		return err
	}
	headers := map[string]string{
		headerEvent:     EventAlertFiring,
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

func (n *Notifier) sendAlertFiringBark(ctx context.Context, settings Settings, alert AlertFiringInfo) error {
	if err := validateOutboundURL("bark_url", settings.BarkURL); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"title": "HUAKAI alert firing",
		"body":  alertFiringSummary(alert),
	})
	if err != nil {
		return err
	}
	return n.postJSON(ctx, settings.BarkURL, body, map[string]string{headerEvent: EventAlertFiring})
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

func (n *Notifier) sendAlertFiringGotify(ctx context.Context, settings Settings, alert AlertFiringInfo) error {
	if err := validateOutboundURL("gotify_url", settings.GotifyURL); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"title":    "HUAKAI alert firing",
		"message":  alertFiringSummary(alert),
		"priority": settings.GotifyPriority,
	})
	if err != nil {
		return err
	}
	return n.postJSON(ctx, settings.GotifyURL, body, map[string]string{
		headerEvent:    EventAlertFiring,
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

type alertFiringPayload struct {
	EventType     string            `json:"event_type"`
	TenantID      int64             `json:"tenant_id"`
	UserID        int64             `json:"user_id"`
	RuleName      string            `json:"rule_name"`
	Metric        string            `json:"metric"`
	MetricType    string            `json:"metric_type,omitempty"`
	Comparator    string            `json:"comparator"`
	Threshold     float64           `json:"threshold"`
	Severity      string            `json:"severity"`
	ObservedValue float64           `json:"observed_value"`
	Dimensions    map[string]string `json:"dimensions,omitempty"`
	FiredAt       time.Time         `json:"fired_at"`
	OccurredAt    time.Time         `json:"occurred_at"`
}

func normalizeAlertFiringInfo(alert AlertFiringInfo, fallback time.Time) (AlertFiringInfo, error) {
	alert.RuleName = strings.TrimSpace(alert.RuleName)
	alert.Metric = strings.TrimSpace(alert.Metric)
	alert.MetricType = strings.TrimSpace(alert.MetricType)
	alert.Comparator = strings.TrimSpace(alert.Comparator)
	alert.Severity = strings.TrimSpace(alert.Severity)
	alert.Dimensions = cloneAlertDimensions(alert.Dimensions)
	if alert.RuleName == "" || alert.Metric == "" || alert.Comparator == "" || alert.Severity == "" {
		return alert, fmt.Errorf("%w: alert firing info", ErrInvalidSettings)
	}
	if math.IsNaN(alert.Threshold) || math.IsInf(alert.Threshold, 0) || math.IsNaN(alert.ObservedValue) || math.IsInf(alert.ObservedValue, 0) {
		return alert, fmt.Errorf("%w: alert firing value", ErrInvalidSettings)
	}
	if alert.FiredAt.IsZero() {
		alert.FiredAt = fallback
	}
	alert.FiredAt = alert.FiredAt.UTC()
	return alert, nil
}

func cloneAlertDimensions(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func formatAlertDimensions(dimensions map[string]string) string {
	if len(dimensions) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(dimensions))
	for key, value := range dimensions {
		parts = append(parts, key+"="+value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func alertFiringSummary(alert AlertFiringInfo) string {
	return fmt.Sprintf(
		"%s %s: %s %s %s (observed %s)",
		alert.Severity,
		alert.RuleName,
		alert.Metric,
		alert.Comparator,
		formatAlertFloat(alert.Threshold),
		formatAlertFloat(alert.ObservedValue),
	)
}

func formatAlertFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
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

func (n *Notifier) sendExtraEmailCopies(ctx context.Context, settings Settings, base mailinfra.Message) {
	for _, extra := range settings.ExtraEmails {
		if extra == "" || extra == settings.NotificationEmail {
			continue
		}
		if err := rejectHeaderInjection(extra); err != nil {
			continue
		}
		copyMsg := base
		copyMsg.To = extra
		_ = n.emailSender.SendTenantMessage(ctx, settings.TenantID, copyMsg)
	}
}
