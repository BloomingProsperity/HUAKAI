package alerting

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

type alertEmailSettingsStub struct {
	value string
	err   error
}

func (s alertEmailSettingsStub) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	if s.err != nil {
		return platformsettings.StoredSetting{}, s.err
	}
	return platformsettings.StoredSetting{Key: key, Value: s.value, Source: platformsettings.SourceDB}, nil
}

type alertEmailSenderSpy struct {
	messages []mailinfra.Message
	err      error
}

func (s *alertEmailSenderSpy) SendTenantMessage(_ context.Context, tenantID int64, message mailinfra.Message) error {
	message.TenantID = tenantID
	s.messages = append(s.messages, message)
	return s.err
}

func TestAlertNotifyEmailTrueSendsExactlyOneChineseMessage(t *testing.T) {
	// 变异：删除 NotifyEmail=true 分支后 sender 为零次；绕过边沿去重则会超过一次。
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	sender := &alertEmailSenderSpy{}
	svc := NewService(NewMemoryStore(),
		WithClock(func() time.Time { return now }),
		WithFiringEmailDeliverer(NewAdminEmailDeliverer(alertEmailSettingsStub{value: "ops@huakai.example"}, sender, nil)),
	)
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID: 7, Name: "网关错误率过高", Metric: "gateway.error_rate", Comparator: ComparatorGTE,
		Threshold: 10, Severity: SeverityCritical, WindowSeconds: 60, NotifyEmail: true,
	})
	if err := svc.EvaluateRules(ctx, 7, map[string]float64{"gateway.error_rate": 12.5}); err != nil {
		t.Fatalf("EvaluateRules: %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("邮件数=%d, want 1", len(sender.messages))
	}
	message := sender.messages[0]
	if message.To != "ops@huakai.example" || !strings.Contains(message.Subject, "网关错误率过高") {
		t.Fatalf("邮件收件人/主题不符: %+v", message)
	}
	for _, want := range []string{"租户：7", "gateway.error_rate", "当前值：12.5", "阈值：gte 10"} {
		if !strings.Contains(message.HTMLBody, want) {
			t.Fatalf("邮件正文缺少 %q: %s", want, message.HTMLBody)
		}
	}
}

func TestAlertNotifyEmailFalseSendsZeroMessages(t *testing.T) {
	// 变异：去掉 NotifyEmail 判断会让 sender 从零次变成一次。
	now := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	sender := &alertEmailSenderSpy{}
	svc := NewService(NewMemoryStore(),
		WithClock(func() time.Time { return now }),
		WithFiringEmailDeliverer(NewAdminEmailDeliverer(alertEmailSettingsStub{value: "ops@huakai.example"}, sender, nil)),
	)
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID: 7, Name: "不发邮件", Metric: "gateway.requests", Comparator: ComparatorGTE,
		Threshold: 1, Severity: SeverityWarning, WindowSeconds: 60, NotifyEmail: false,
	})
	if err := svc.EvaluateRules(context.Background(), 7, map[string]float64{"gateway.requests": 2}); err != nil {
		t.Fatalf("EvaluateRules: %v", err)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("邮件数=%d, want 0", len(sender.messages))
	}
}

func TestAlertEmailFailureDoesNotBlockFiringEvent(t *testing.T) {
	// 变异：把发送错误返回给 EvaluateRules 会让本测试在事件落库断言前失败。
	now := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	sendErr := errors.New("smtp unavailable")
	sender := &alertEmailSenderSpy{err: sendErr}
	var recorded error
	svc := NewService(NewMemoryStore(),
		WithClock(func() time.Time { return now }),
		WithFiringEmailDeliverer(NewAdminEmailDeliverer(alertEmailSettingsStub{value: "ops@huakai.example"}, sender, nil)),
		WithFiringDeliveryErrorRecorder(func(_ context.Context, _ int64, _ FiringNotice, err error) { recorded = err }),
	)
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID: 7, Name: "邮件故障仍落库", Metric: "gateway.requests", Comparator: ComparatorGTE,
		Threshold: 1, Severity: SeverityCritical, WindowSeconds: 60, NotifyEmail: true,
	})
	if err := svc.EvaluateRules(context.Background(), 7, map[string]float64{"gateway.requests": 2}); err != nil {
		t.Fatalf("EvaluateRules 不应传播邮件错误: %v", err)
	}
	if !errors.Is(recorded, sendErr) {
		t.Fatalf("记录错误=%v, want %v", recorded, sendErr)
	}
	events, err := svc.ListEvents(context.Background(), ListEventsInput{TenantID: 7, State: EventStateFiring, Limit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("firing events=%+v err=%v, want one persisted event", events, err)
	}
}

func TestAlertRuleCooldownSuppressesSecondEmail(t *testing.T) {
	// 变异：绕过规则 cooldown 后，恢复后的第二次 firing 会把邮件数从一推到二。
	base := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	now := base
	sender := &alertEmailSenderSpy{}
	svc := NewService(NewMemoryStore(),
		WithClock(func() time.Time { return now }),
		WithFiringEmailDeliverer(NewAdminEmailDeliverer(alertEmailSettingsStub{value: "ops@huakai.example"}, sender, nil)),
	)
	mustCreateRule(t, svc, CreateRuleInput{
		TenantID: 7, Name: "冷却去重", Metric: "gateway.requests", Comparator: ComparatorGTE,
		Threshold: 10, Severity: SeverityWarning, WindowSeconds: 60, CooldownSeconds: 300, NotifyEmail: true,
	})
	if err := svc.EvaluateRules(context.Background(), 7, map[string]float64{"gateway.requests": 20}); err != nil {
		t.Fatalf("首次 firing: %v", err)
	}
	now = base.Add(time.Minute)
	if err := svc.EvaluateRules(context.Background(), 7, map[string]float64{"gateway.requests": 1}); err != nil {
		t.Fatalf("恢复: %v", err)
	}
	now = base.Add(2 * time.Minute)
	if err := svc.EvaluateRules(context.Background(), 7, map[string]float64{"gateway.requests": 30}); err != nil {
		t.Fatalf("冷却期二次 firing: %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("邮件数=%d, want cooldown 内仍为 1", len(sender.messages))
	}
}

func TestAlertEmailMissingRecipientWarnsAndSkips(t *testing.T) {
	// 变异：空收件人继续调用 SMTP 会使 sender 从零次变成一次。
	var logs bytes.Buffer
	sender := &alertEmailSenderSpy{}
	deliverer := NewAdminEmailDeliverer(alertEmailSettingsStub{}, sender, slog.New(slog.NewTextHandler(&logs, nil)))
	delivered, err := deliverer.DeliverFiringEmail(context.Background(), 7, FiringNotice{RuleID: 9, RuleName: "缺收件人"})
	if err != nil || delivered {
		t.Fatalf("delivered=%v err=%v, want safe skip", delivered, err)
	}
	if len(sender.messages) != 0 || !strings.Contains(logs.String(), "未配置管理员通知邮箱") {
		t.Fatalf("sender=%d logs=%q, want zero send plus warning", len(sender.messages), logs.String())
	}
}
