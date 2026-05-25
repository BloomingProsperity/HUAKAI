package eventbus_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
)

func TestValidateMoneyPathAuditRef_ProductionMissingRefRejects(t *testing.T) {
	policy := productionAuditRefPolicy(false)
	missing := validAuditRefEvent()
	withDLQRef := missing
	withDLQRef.AuditLedgerDLQRef = "dlq:1"

	// Mutation: 删除 validator 的 AuditLedgerDLQRef 非空放行分支时，withDLQRef 断言会失败。
	if err := eventbus.ValidateMoneyPathAuditRef(&missing, policy); !errors.Is(err, eventbus.ErrAuditRefMissing) {
		t.Fatalf("missing ref err=%v want ErrAuditRefMissing", err)
	}
	if err := eventbus.ValidateMoneyPathAuditRef(&withDLQRef, policy); err != nil {
		t.Fatalf("DLQRef-only err=%v want nil", err)
	}
}

func TestValidateMoneyPathAuditRef_PersistedNeedsBothIDAndFingerprint(t *testing.T) {
	policy := productionAuditRefPolicy(false)
	ledgerIDOnly := validAuditRefEvent()
	ledgerIDOnly.AuditLedgerID = "ledger-1"
	withFingerprint := ledgerIDOnly
	withFingerprint.AuditSignatureFingerprint = "fp"

	// Mutation: 把 validator 改成只要 LedgerID 非空就放行时，ledgerIDOnly 断言会失败。
	if err := eventbus.ValidateMoneyPathAuditRef(&ledgerIDOnly, policy); !errors.Is(err, eventbus.ErrAuditRefMissing) {
		t.Fatalf("ledger ID only err=%v want ErrAuditRefMissing", err)
	}
	if err := eventbus.ValidateMoneyPathAuditRef(&withFingerprint, policy); err != nil {
		t.Fatalf("ledger ID + fingerprint err=%v want nil", err)
	}
}

func TestValidateMoneyPathAuditRef_DLQRefDoesNotRequireFingerprint(t *testing.T) {
	policy := productionAuditRefPolicy(false)
	withDLQRef := validAuditRefEvent()
	withDLQRef.AuditLedgerDLQRef = "dlq:1"
	missing := withDLQRef
	missing.AuditLedgerDLQRef = ""

	// Mutation: 给 DLQRef 分支追加 fingerprint 要求时，withDLQRef 断言会失败。
	if err := eventbus.ValidateMoneyPathAuditRef(&withDLQRef, policy); err != nil {
		t.Fatalf("DLQRef without fingerprint err=%v want nil", err)
	}
	if err := eventbus.ValidateMoneyPathAuditRef(&missing, policy); !errors.Is(err, eventbus.ErrAuditRefMissing) {
		t.Fatalf("missing DLQRef err=%v want ErrAuditRefMissing", err)
	}
}

func TestValidateMoneyPathAuditRef_NonProductionExempt(t *testing.T) {
	devPolicy := &eventbus.AuditRefPolicy{ReleaseMode: eventbus.ReleaseModeDev}
	prodPolicy := productionAuditRefPolicy(false)
	missing := validAuditRefEvent()

	// Mutation: 删除非 production 豁免分支时，devPolicy 断言会失败。
	if err := eventbus.ValidateMoneyPathAuditRef(&missing, devPolicy); err != nil {
		t.Fatalf("dev missing ref err=%v want nil", err)
	}
	if err := eventbus.ValidateMoneyPathAuditRef(&missing, prodPolicy); !errors.Is(err, eventbus.ErrAuditRefMissing) {
		t.Fatalf("production missing ref err=%v want ErrAuditRefMissing", err)
	}
}

func TestValidateMoneyPathAuditRef_EscapeFlagBypasses(t *testing.T) {
	allowPolicy := productionAuditRefPolicy(true)
	denyPolicy := productionAuditRefPolicy(false)
	missing := validAuditRefEvent()

	// Mutation: 忽略 AllowMissingMoneyRef 时，allowPolicy 断言会失败。
	if err := eventbus.ValidateMoneyPathAuditRef(&missing, allowPolicy); err != nil {
		t.Fatalf("escape flag missing ref err=%v want nil", err)
	}
	if err := eventbus.ValidateMoneyPathAuditRef(&missing, denyPolicy); !errors.Is(err, eventbus.ErrAuditRefMissing) {
		t.Fatalf("deny missing ref err=%v want ErrAuditRefMissing", err)
	}
}

func TestEmit_RejectsMoneyPathMissingRef(t *testing.T) {
	bus := eventbus.New(eventbus.Config{
		HighWorkers:    1,
		HighBuffer:     1,
		HandlerTimeout: time.Second,
		AuditRefPolicy: productionAuditRefPolicy(false),
	})
	defer func() { _ = bus.Stop(context.Background()) }()

	calls := 0
	mustRegister(t, bus, eventbus.HandlerFunc{
		HandlerID:      eventbus.HandlerBillingPersister,
		HandlerTier:    eventbus.TierHigh,
		HandlerOrder:   10,
		IsCritical:     true,
		HandlerTimeout: time.Second,
		Fn: func(context.Context, eventbus.RequestCompletionEvent) error {
			calls++
			return nil
		},
	})

	missing := testEvent("evt-audit-ref-missing")
	withDLQRef := testEvent("evt-audit-ref-dlq")
	withDLQRef.AuditLedgerDLQRef = "dlq:1"

	// Mutation: 在 bus.go 中恢复成 event.normalized() 不传 policy 时，missing 分支会调用 handler。
	if err := bus.Emit(context.Background(), missing); !errors.Is(err, eventbus.ErrAuditRefMissing) {
		t.Fatalf("missing ref Emit err=%v want ErrAuditRefMissing", err)
	}
	if calls != 0 {
		t.Fatalf("missing ref handler calls=%d want 0", calls)
	}
	if err := bus.Emit(context.Background(), withDLQRef); err != nil {
		t.Fatalf("DLQRef Emit: %v", err)
	}
	if calls != 1 {
		t.Fatalf("DLQRef handler calls=%d want 1", calls)
	}
}

func productionAuditRefPolicy(allowMissing bool) *eventbus.AuditRefPolicy {
	return &eventbus.AuditRefPolicy{
		ReleaseMode:          eventbus.ReleaseModeProduction,
		AllowMissingMoneyRef: allowMissing,
	}
}

func validAuditRefEvent() eventbus.RequestCompletionEvent {
	return eventbus.RequestCompletionEvent{
		ID:        "evt-audit-ref",
		Kind:      eventbus.EventKindRequestCompletion,
		TenantID:  7,
		ClaimID:   11,
		RequestID: "req-audit-ref",
	}
}
