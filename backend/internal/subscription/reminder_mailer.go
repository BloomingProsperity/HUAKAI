// HUAKAI · iKun

package subscription

import (
	"context"
	"errors"

	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
)

// emailReminderMailer 用 internal/email 的租户发信能力实现 ReminderMailer。
// 本文件是 subscription → email 唯一耦合点; 核心提醒逻辑 (reminder.go) 与 email 解耦。
type emailReminderMailer struct {
	sender *mailinfra.AuthSender
}

// NewEmailReminderMailer 用已构造 (并接好 DLQ outbox) 的 email sender 建提醒 mailer。
func NewEmailReminderMailer(sender *mailinfra.AuthSender) ReminderMailer {
	return &emailReminderMailer{sender: sender}
}

// SendReminder 发送一封提醒并分类结果:
//   - nil               → ReminderSent (已投递, 或瞬时失败已入 DLQ 待重试)
//   - 未配置 SMTP         → ReminderSkippedUnconfigured (claim 已落库, 上层记失败 tick)
//   - 其它失败            → ReminderRetry (claim 已落库, 上层记失败 tick)
//
// 发送闸门在 reminder.go 的 RecordReminder claim。claim 后失败不回滚, 避免多副本重复提醒。
func (m *emailReminderMailer) SendReminder(ctx context.Context, tenantID int64, to, subject, htmlBody string) ReminderOutcome {
	if m == nil || m.sender == nil {
		return ReminderSkippedUnconfigured
	}
	err := m.sender.SendTenantMessage(ctx, tenantID, mailinfra.Message{
		TenantID: tenantID,
		To:       to,
		Subject:  subject,
		HTMLBody: htmlBody,
	})
	switch {
	case err == nil:
		return ReminderSent
	case errors.Is(err, mailinfra.ErrEmailBackendUnconfigured):
		return ReminderSkippedUnconfigured
	default:
		return ReminderRetry
	}
}
