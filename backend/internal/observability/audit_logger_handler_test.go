package observability

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
)

func TestAuditLoggerHandlerRequiredRefRejectsAllRefsEmpty(t *testing.T) {
	handler := NewAuditLoggerHandler(time.Second, WithRequiredAuditRef())

	err := handler.Handle(context.Background(), eventbus.RequestCompletionEvent{})
	if !errors.Is(err, ErrAuditRefMissing) {
		t.Fatalf("Handle err=%v want ErrAuditRefMissing", err)
	}
}

func TestAuditLoggerHandlerRequiredRefAcceptsDLQRefOnly(t *testing.T) {
	handler := NewAuditLoggerHandler(time.Second, WithRequiredAuditRef())

	// Mutation: 把 required-ref 回退为只检查 AuditLedgerID 时，本用例会变红。
	err := handler.Handle(context.Background(), eventbus.RequestCompletionEvent{
		AuditLedgerDLQRef: "audit_ledger_dlq:1",
	})
	if err != nil {
		t.Fatalf("Handle with DLQRef only: %v", err)
	}
}

func TestAuditLoggerHandlerRequiredRefAcceptsLedgerIDOnly(t *testing.T) {
	handler := NewAuditLoggerHandler(time.Second, WithRequiredAuditRef())

	err := handler.Handle(context.Background(), eventbus.RequestCompletionEvent{
		AuditLedgerID: "ledger-1",
	})
	if err != nil {
		t.Fatalf("Handle with LedgerID only: %v", err)
	}
}

func TestAuditLoggerHandlerRequiredRefAllowsMissingRefWhenEscapePolicyActive(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})

	handler := NewAuditLoggerHandler(time.Second,
		WithRequiredAuditRef(),
		WithAuditRefPolicy(&eventbus.AuditRefPolicy{
			ReleaseMode:          eventbus.ReleaseModeProduction,
			AllowMissingMoneyRef: true,
		}),
	)

	// Mutation: 去掉 audit logger 的 escape flag 旁路时，nil-return 断言会变红；
	// 删除 ERROR 可见性日志时，bypass marker 断言会变红。
	err := handler.Handle(context.Background(), eventbus.RequestCompletionEvent{
		RequestID: "req-escape-1",
		TenantID:  7,
		Metadata:  map[string]string{"route_id": "route-7"},
	})
	if err != nil {
		t.Fatalf("Handle with AllowMissingMoneyRef policy: %v", err)
	}
	logged := logs.String()
	if !strings.Contains(logged, "audit_logger_escape_bypass") {
		t.Fatalf("log missing bypass marker; logs=%s", logged)
	}
	if !strings.Contains(logged, "HUAKAI_TRUST_LEDGER_ALLOW_MISSING_MONEY_REF") {
		t.Fatalf("log missing escape env var; logs=%s", logged)
	}
	if !strings.Contains(logged, `"level":"ERROR"`) {
		t.Fatalf("log level must be ERROR; logs=%s", logged)
	}
}
