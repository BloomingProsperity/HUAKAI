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
//   - 未配置 SMTP         → ReminderSkippedUnconfigured (不记账, 配好后重试)
//   - 其它失败            → ReminderRetry (不记账, 下个 tick 重试)
//
// 不durably记永久失败: email 包的 permanent 判定 (坏地址/4xx/设置无效) 未导出, 无法干净区分
// "真永久(坏收件人)" 与 "可恢复(From 配置无效 / 瞬时无 DLQ)"。若把可恢复失败记成永久, operator
// 修好配置后该档会被永久跳过 (silent missed reminder)。故一律可重试, 宁可对坏地址每 tick 重试一次。
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
