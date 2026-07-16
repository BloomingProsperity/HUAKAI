package alerting

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"

	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

type AdminNotificationSettingGetter interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type TenantMessageSender interface {
	SendTenantMessage(context.Context, int64, mailinfra.Message) error
}

// AdminEmailDeliverer 把 firing 边沿发送到全局管理员通知邮箱，并复用租户 SMTP、DLQ 与重试链。
type AdminEmailDeliverer struct {
	settings AdminNotificationSettingGetter
	sender   TenantMessageSender
	logger   *slog.Logger
}

func NewAdminEmailDeliverer(settings AdminNotificationSettingGetter, sender TenantMessageSender, logger *slog.Logger) *AdminEmailDeliverer {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdminEmailDeliverer{settings: settings, sender: sender, logger: logger}
}

func (d *AdminEmailDeliverer) DeliverFiringEmail(ctx context.Context, tenantID int64, notice FiringNotice) (bool, error) {
	if d == nil || d.settings == nil || d.sender == nil {
		return false, mailinfra.ErrEmailBackendUnconfigured
	}
	setting, err := d.settings.Get(ctx, platformsettings.KeyAdminNotificationEmail)
	if err != nil {
		return false, err
	}
	recipient := strings.TrimSpace(setting.Value)
	if recipient == "" {
		d.logger.WarnContext(ctx, "告警邮件已跳过：未配置管理员通知邮箱",
			slog.Int64("tenant_id", tenantID),
			slog.Int64("rule_id", notice.RuleID),
			slog.String("rule_name", notice.RuleName),
		)
		return false, nil
	}
	subject := "HUAKAI 告警：" + mailinfra.SanitizeHeaderValue(notice.RuleName)
	body := fmt.Sprintf(
		"<html><body><h2>%s</h2><p>租户：%d</p><p>指标：%s</p><p>当前值：%g</p><p>阈值：%s %g</p><p>级别：%s</p><p>触发时间：%s</p></body></html>",
		html.EscapeString(notice.RuleName),
		tenantID,
		html.EscapeString(notice.Metric),
		notice.ObservedValue,
		html.EscapeString(string(notice.Comparator)),
		notice.Threshold,
		html.EscapeString(string(notice.Severity)),
		notice.FiredAt.UTC().Format("2006-01-02 15:04:05Z07:00"),
	)
	err = d.sender.SendTenantMessage(ctx, tenantID, mailinfra.Message{
		TenantID: tenantID,
		To:       recipient,
		Subject:  subject,
		HTMLBody: body,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}
