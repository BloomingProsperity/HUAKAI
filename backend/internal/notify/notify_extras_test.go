package notify

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
)

// NOTIF-013 + NOTIF-014。变异覆盖:
//   - 去掉 threshold_type 校验 -> "bogus" 被接受 -> validate 测试失败。
//   - notifier 忽略 ExtraEmails -> 只发送给主接收者 -> fanout 测试失败。
func TestThresholdTypeAndExtraEmailsValidate(t *testing.T) {
	base := Settings{
		TenantID:          7,
		UserID:            42,
		NotifyType:        TypeEmail,
		NotificationEmail: "a@example.test",
		BalanceThreshold:  decimal.RequireFromString("10.00000000"),
	}

	valid := base
	valid.ThresholdType = "percentage"
	valid.ExtraEmails = []string{"b@example.test", "c@example.test"}
	if _, err := ValidateSettings(valid); err != nil {
		t.Fatalf("valid percentage+extras rejected: %v", err)
	}

	def := base
	out, err := ValidateSettings(def)
	if err != nil || out.ThresholdType != "fixed" {
		t.Fatalf("default threshold_type=%q err=%v want fixed", out.ThresholdType, err)
	}

	bogus := base
	bogus.ThresholdType = "bogus"
	if _, err := ValidateSettings(bogus); err == nil {
		t.Fatalf("bogus threshold_type accepted; MUTATION killed")
	}

	crlf := base
	crlf.ExtraEmails = []string{"x@example.test\r\nBcc: evil@example.test"}
	if _, err := ValidateSettings(crlf); err == nil {
		t.Fatalf("CRLF-injected extra email accepted")
	}

	tooMany := base
	for i := 0; i < 11; i++ {
		tooMany.ExtraEmails = append(tooMany.ExtraEmails, "x@example.test")
	}
	if _, err := ValidateSettings(tooMany); err == nil {
		t.Fatalf(">10 extra emails accepted")
	}
}

func TestExtraEmailsFanout(t *testing.T) {
	store := fakeStore{settings: Settings{
		TenantID:          7,
		UserID:            42,
		NotifyType:        TypeEmail,
		NotificationEmail: "main@example.test",
		ExtraEmails:       []string{"extra1@example.test", "extra2@example.test"},
		BalanceThreshold:  decimal.RequireFromString("10.00000000"),
	}}
	sender := &recordingEmailSender{}
	notifier := NewNotifier(Config{Store: store, EmailSender: sender, Now: fixedNow})

	if err := notifier.NotifyLowBalance(context.Background(), 7, 42, decimal.RequireFromString("1.00000000"), 11); err != nil {
		t.Fatalf("NotifyLowBalance: %v", err)
	}

	if got := sender.Count(); got != 3 {
		t.Fatalf("email sends=%d want 3 (main + 2 extras); MUTATION: ignoring ExtraEmails sends 1", got)
	}
	recipients := map[string]bool{}
	for _, m := range sender.messages {
		recipients[m.To] = true
	}
	for _, want := range []string{"main@example.test", "extra1@example.test", "extra2@example.test"} {
		if !recipients[want] {
			t.Fatalf("recipient %q missing; got %v", want, recipients)
		}
	}
}
