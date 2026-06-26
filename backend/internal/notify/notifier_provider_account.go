package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
)

// providerAccountDownPayload 是 EventProviderAccountDown webhook 投递的
// 线缆/JSON 报文体;它沿用 alertFiringPayload 的形状(event_type +
// tenant/user 信封 + 领域字段),这样现有 webhook 接收方无需新解析器即可
// 根据 event_type 做分支处理。
type providerAccountDownPayload struct {
	EventType         string    `json:"event_type"`
	TenantID          int64     `json:"tenant_id"`
	UserID            int64     `json:"user_id"`
	ProviderAccountID int64     `json:"provider_account_id"`
	VendorName        string    `json:"vendor_name,omitempty"`
	HealthState       string    `json:"health_state"`
	Outcome           string    `json:"outcome"`
	Severity          string    `json:"severity"`
	OccurredAt        time.Time `json:"occurred_at"`
}

// NotifyProviderAccountDown 将一次 provider-account-down 状态转移
//(一次把账号推入终态/不健康状态的凭证刷新)广播到每个活跃的租户通知渠道。
// 它通过 Notifier.broadcast 复用了与 NotifyAlertFiring 相同的扇出 +
// per-(tenant,user,eventType) 限流 + 多渠道分发内部逻辑,因此
// email / webhook(HMAC 签名) / bark / gotify 都会携带 EventProviderAccountDown 事件。
func (n *Notifier) NotifyProviderAccountDown(ctx context.Context, tenantID int64, info ProviderAccountDownInfo) error {
	if n == nil || n.store == nil {
		return nil
	}
	if tenantID <= 0 {
		return fmt.Errorf("%w: tenant_id", ErrInvalidSettings)
	}
	now := n.now().UTC()
	info, err := normalizeProviderAccountDownInfo(info, now)
	if err != nil {
		return err
	}
	return n.broadcast(ctx, tenantID, EventProviderAccountDown, now, func(ctx context.Context, settings Settings) error {
		return n.sendProviderAccountDown(ctx, settings, info, now)
	})
}

func normalizeProviderAccountDownInfo(info ProviderAccountDownInfo, fallback time.Time) (ProviderAccountDownInfo, error) {
	info.VendorName = strings.TrimSpace(info.VendorName)
	info.HealthState = strings.TrimSpace(info.HealthState)
	info.Outcome = strings.TrimSpace(info.Outcome)
	info.Severity = strings.TrimSpace(info.Severity)
	if info.Severity == "" {
		info.Severity = "critical"
	}
	if info.ProviderAccountID <= 0 {
		return info, fmt.Errorf("%w: provider_account_id", ErrInvalidSettings)
	}
	if info.HealthState == "" || info.Outcome == "" {
		return info, fmt.Errorf("%w: provider account down info", ErrInvalidSettings)
	}
	if info.OccurredAt.IsZero() {
		info.OccurredAt = fallback
	}
	info.OccurredAt = info.OccurredAt.UTC()
	return info, nil
}

func (n *Notifier) sendProviderAccountDown(ctx context.Context, settings Settings, info ProviderAccountDownInfo, occurredAt time.Time) error {
	switch settings.NotifyType {
	case TypeEmail:
		return n.sendProviderAccountDownEmail(ctx, settings, info)
	case TypeWebhook:
		return n.sendProviderAccountDownWebhook(ctx, settings, info, occurredAt)
	case TypeBark:
		return n.sendProviderAccountDownBark(ctx, settings, info)
	case TypeGotify:
		return n.sendProviderAccountDownGotify(ctx, settings, info)
	default:
		return nil
	}
}

func (n *Notifier) sendProviderAccountDownEmail(ctx context.Context, settings Settings, info ProviderAccountDownInfo) error {
	if n.emailSender == nil {
		return fmt.Errorf("%w: email sender unavailable", ErrDeliveryFailed)
	}
	subject := "HUAKAI provider account down: " + providerAccountDownLabel(info)
	if err := rejectHeaderInjection(settings.NotificationEmail); err != nil {
		return err
	}
	if err := rejectHeaderInjection(subject); err != nil {
		return err
	}
	body := fmt.Sprintf(
		"<p>A HUAKAI provider account has been driven into an unhealthy state by a credential refresh and may need operator action.</p>"+
			"<p>Provider account ID: %s</p><p>Vendor: %s</p><p>Health state: %s</p><p>Outcome: %s</p><p>Severity: %s</p><p>Occurred at: %s</p>",
		html.EscapeString(fmt.Sprintf("%d", info.ProviderAccountID)),
		html.EscapeString(providerAccountDownVendor(info)),
		html.EscapeString(info.HealthState),
		html.EscapeString(info.Outcome),
		html.EscapeString(info.Severity),
		html.EscapeString(info.OccurredAt.UTC().Format(time.RFC3339)),
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

func (n *Notifier) sendProviderAccountDownWebhook(ctx context.Context, settings Settings, info ProviderAccountDownInfo, occurredAt time.Time) error {
	if err := validateOutboundURL("webhook_url", settings.WebhookURL); err != nil {
		return err
	}
	body, err := json.Marshal(providerAccountDownPayload{
		EventType:         EventProviderAccountDown,
		TenantID:          settings.TenantID,
		UserID:            settings.UserID,
		ProviderAccountID: info.ProviderAccountID,
		VendorName:        info.VendorName,
		HealthState:       info.HealthState,
		Outcome:           info.Outcome,
		Severity:          info.Severity,
		OccurredAt:        occurredAt.UTC(),
	})
	if err != nil {
		return err
	}
	headers := map[string]string{
		headerEvent:     EventProviderAccountDown,
		headerSignature: signWebhook(settings.WebhookSecret, body),
	}
	return n.postJSON(ctx, settings.WebhookURL, body, headers)
}

func (n *Notifier) sendProviderAccountDownBark(ctx context.Context, settings Settings, info ProviderAccountDownInfo) error {
	if err := validateOutboundURL("bark_url", settings.BarkURL); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"title": "HUAKAI provider account down",
		"body":  providerAccountDownSummary(info),
	})
	if err != nil {
		return err
	}
	return n.postJSON(ctx, settings.BarkURL, body, map[string]string{headerEvent: EventProviderAccountDown})
}

func (n *Notifier) sendProviderAccountDownGotify(ctx context.Context, settings Settings, info ProviderAccountDownInfo) error {
	if err := validateOutboundURL("gotify_url", settings.GotifyURL); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"title":    "HUAKAI provider account down",
		"message":  providerAccountDownSummary(info),
		"priority": settings.GotifyPriority,
	})
	if err != nil {
		return err
	}
	return n.postJSON(ctx, settings.GotifyURL, body, map[string]string{
		headerEvent:    EventProviderAccountDown,
		"X-Gotify-Key": settings.GotifyToken,
	})
}

func providerAccountDownVendor(info ProviderAccountDownInfo) string {
	if info.VendorName == "" {
		return "unknown"
	}
	return info.VendorName
}

func providerAccountDownLabel(info ProviderAccountDownInfo) string {
	return fmt.Sprintf("%s account %d", providerAccountDownVendor(info), info.ProviderAccountID)
}

func providerAccountDownSummary(info ProviderAccountDownInfo) string {
	return fmt.Sprintf(
		"%s %s -> %s (outcome %s)",
		info.Severity,
		providerAccountDownLabel(info),
		info.HealthState,
		info.Outcome,
	)
}
