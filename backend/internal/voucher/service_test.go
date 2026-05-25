package voucher

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAT_BILL_002_001_CreateVoucherAuditRedactsRawCode(t *testing.T) {
	ctx := context.Background()
	now := fixedNow()
	store := NewMemoryStore()
	audit := NewMemoryAuditSink()
	svc := NewService(store, WithAuditSink(audit))

	result, err := svc.Create(ctx, CreateInput{
		TenantID: 1, AdminID: 10, Code: "secret-create-code", AmountCents: 2500,
		ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour), SingleUsePerUser: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if result.Voucher.ID == 0 || result.Voucher.CodeFingerprint == "" {
		t.Fatalf("created voucher missing id/fingerprint: %+v", result.Voucher)
	}
	events := audit.Events()
	if len(events) != 1 || events[0].EventType != AuditVoucherCreated {
		t.Fatalf("audit events = %+v", events)
	}
	if strings.Contains(fmt.Sprint(events), "secret-create-code") {
		t.Fatalf("audit leaked raw voucher code: %+v", events)
	}
}

func TestAT_BILL_002_002_007_RedeemWritesBillingEventAndBalance(t *testing.T) {
	ctx := context.Background()
	now := fixedNow()
	store := NewMemoryStore()
	audit := NewMemoryAuditSink()
	svc := NewService(store, WithAuditSink(audit))
	created := mustCreateVoucher(t, svc, "redeem-happy", 1500, now)

	result, err := svc.Redeem(ctx, RedeemInput{
		TenantID: 1, UserID: 22, Code: created.Code, IdempotencyKey: "redeem-1",
		SourceIP: "203.0.113.10", RequestID: "req-redeem", Now: now,
	})
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}
	if result.BalanceCents != 1500 {
		t.Fatalf("balance=%d, want 1500", result.BalanceCents)
	}
	if result.Redemption.BillingEventID == 0 {
		t.Fatalf("redemption missing billing event id: %+v", result.Redemption)
	}
	events, err := svc.BillingEvents(ctx, 1, 22)
	if err != nil {
		t.Fatalf("BillingEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].EventType != "voucher_redeemed" || events[0].AmountCents != 1500 {
		t.Fatalf("billing events = %+v", events)
	}
	if got := auditTypes(audit.Events()); !containsAll(got, AuditVoucherCreated, AuditVoucherRedeemed) {
		t.Fatalf("audit types = %v", got)
	}
}

func TestAT_BILL_002_003_RedeemRaceAllowsOneWinner(t *testing.T) {
	ctx := context.Background()
	now := fixedNow()
	svc := NewService(NewMemoryStore(), WithBurstLimiter(NewMemoryBurstLimiter(BurstPolicy{Limit: 100, Window: time.Minute, BlockPeriod: time.Minute})))
	created := mustCreateVoucher(t, svc, "race-code", 100, now)

	const workers = 32
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.Redeem(ctx, RedeemInput{
				TenantID: 1, UserID: int64(100 + i), Code: created.Code,
				IdempotencyKey: fmt.Sprintf("race-%d", i), SourceIP: "198.51.100.9", Now: now,
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrVoucherExhausted) && !errors.Is(err, ErrAlreadyRedeemed) {
			t.Fatalf("unexpected race error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("race successes=%d, want 1", successes)
	}
}

func TestAT_BILL_002_004_005_006_ExpiredWrongTenantAndRevoked(t *testing.T) {
	ctx := context.Background()
	now := fixedNow()
	svc := NewService(NewMemoryStore(), WithBurstLimiter(NewMemoryBurstLimiter(BurstPolicy{Limit: 100, Window: time.Minute, BlockPeriod: time.Minute})))

	expired := mustCreateVoucherWindow(t, svc, "expired-code", 100, now.Add(-2*time.Hour), now.Add(-time.Hour), now)
	if _, err := svc.Redeem(ctx, RedeemInput{TenantID: 1, UserID: 1, Code: expired.Code, SourceIP: "198.51.100.1", Now: now}); !errors.Is(err, ErrVoucherExpired) {
		t.Fatalf("expired redeem error = %v, want ErrVoucherExpired", err)
	}

	wrongTenant := mustCreateVoucher(t, svc, "tenant-code", 100, now)
	if _, err := svc.Redeem(ctx, RedeemInput{TenantID: 2, UserID: 1, Code: wrongTenant.Code, SourceIP: "198.51.100.2", Now: now}); !errors.Is(err, ErrVoucherNotFound) {
		t.Fatalf("wrong tenant redeem error = %v, want ErrVoucherNotFound", err)
	}
	targetUser := int64(77)
	targeted, err := svc.Create(ctx, CreateInput{
		TenantID: 1, AdminID: 10, Code: "targeted-code", AmountCents: 100,
		ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour),
		SingleUsePerUser: true, EligibleUserID: &targetUser, Now: now,
	})
	if err != nil {
		t.Fatalf("create targeted voucher: %v", err)
	}
	if _, err := svc.Redeem(ctx, RedeemInput{TenantID: 1, UserID: 78, Code: targeted.Code, SourceIP: "198.51.100.22", Now: now}); !errors.Is(err, ErrVoucherWrongUser) {
		t.Fatalf("wrong user redeem error = %v, want ErrVoucherWrongUser", err)
	}

	revoked := mustCreateVoucher(t, svc, "revoked-code", 100, now)
	if _, err := svc.Revoke(ctx, RevokeInput{TenantID: 1, ID: revoked.Voucher.ID, AdminID: 10, Reason: "operator stop", Now: now}); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, err := svc.Redeem(ctx, RedeemInput{TenantID: 1, UserID: 1, Code: revoked.Code, SourceIP: "198.51.100.3", Now: now}); !errors.Is(err, ErrVoucherRevoked) {
		t.Fatalf("revoked redeem error = %v, want ErrVoucherRevoked", err)
	}

	credited := mustCreateVoucher(t, svc, "credit-before-revoke", 700, now)
	if _, err := svc.Redeem(ctx, RedeemInput{TenantID: 1, UserID: 9, Code: credited.Code, SourceIP: "198.51.100.4", Now: now}); err != nil {
		t.Fatalf("redeem before revoke error = %v", err)
	}
	if _, err := svc.Revoke(ctx, RevokeInput{TenantID: 1, ID: credited.Voucher.ID, AdminID: 10, Now: now}); err != nil {
		t.Fatalf("revoke credited voucher error = %v", err)
	}
	events, _ := svc.BillingEvents(ctx, 1, 9)
	if len(events) != 1 || events[0].AmountCents != 700 {
		t.Fatalf("revoke changed prior credit events = %+v", events)
	}
}

func TestAT_BILL_002_008_BurstAntiFraudBlocksBeforeCredit(t *testing.T) {
	ctx := context.Background()
	now := fixedNow()
	store := NewMemoryStore()
	audit := NewMemoryAuditSink()
	svc := NewService(store,
		WithAuditSink(audit),
		WithBurstLimiter(NewMemoryBurstLimiter(BurstPolicy{Limit: 2, Window: time.Minute, BlockPeriod: time.Minute})),
	)
	created := mustCreateVoucher(t, svc, "burst-code", 100, now)

	for i := 0; i < 2; i++ {
		_, _ = svc.Redeem(ctx, RedeemInput{TenantID: 1, UserID: 5, Code: "bad-code", SourceIP: "203.0.113.99", Now: now})
	}
	if _, err := svc.Redeem(ctx, RedeemInput{TenantID: 1, UserID: 5, Code: created.Code, SourceIP: "203.0.113.99", Now: now}); !errors.Is(err, ErrBurstLimited) {
		t.Fatalf("burst redeem error = %v, want ErrBurstLimited", err)
	}
	events, _ := svc.BillingEvents(ctx, 1, 5)
	if len(events) != 0 {
		t.Fatalf("burst-limited attempt credited balance events = %+v", events)
	}
	if got := auditTypes(audit.Events()); !containsAll(got, AuditVoucherRedeemBurstAlert) {
		t.Fatalf("burst audit missing alert: %v", got)
	}
}

func TestAT_BILL_002_009_IdempotencyReturnsPriorResult(t *testing.T) {
	ctx := context.Background()
	now := fixedNow()
	svc := NewService(NewMemoryStore())
	created := mustCreateVoucher(t, svc, "idem-code", 900, now)

	first, err := svc.Redeem(ctx, RedeemInput{TenantID: 1, UserID: 3, Code: created.Code, IdempotencyKey: "same-key", SourceIP: "192.0.2.5", Now: now})
	if err != nil {
		t.Fatalf("first redeem error = %v", err)
	}
	second, err := svc.Redeem(ctx, RedeemInput{TenantID: 1, UserID: 3, Code: created.Code, IdempotencyKey: "same-key", SourceIP: "192.0.2.5", Now: now})
	if err != nil {
		t.Fatalf("second redeem error = %v", err)
	}
	if !second.Idempotent || second.Redemption.ID != first.Redemption.ID || second.BalanceCents != 900 {
		t.Fatalf("idempotent result mismatch: first=%+v second=%+v", first, second)
	}
	events, _ := svc.BillingEvents(ctx, 1, 3)
	if len(events) != 1 {
		t.Fatalf("idempotent replay wrote %d billing events, want 1", len(events))
	}

	other := mustCreateVoucher(t, svc, "idem-other", 900, now)
	if _, err := svc.Redeem(ctx, RedeemInput{TenantID: 1, UserID: 3, Code: other.Code, IdempotencyKey: "same-key", SourceIP: "192.0.2.5", Now: now}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict error = %v", err)
	}
}

func TestAT_BILL_002_010_BatchAllOrNothing(t *testing.T) {
	ctx := context.Background()
	now := fixedNow()
	store := NewMemoryStore()
	svc := NewService(store)
	result, err := svc.CreateBatch(ctx, BatchCreateInput{
		TenantID: 1, AdminID: 10, Count: 3, AmountCents: 111,
		ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour), SingleUsePerUser: true,
	})
	if err != nil {
		t.Fatalf("CreateBatch() error = %v", err)
	}
	if result.Batch.CreatedCount != 3 || len(result.Vouchers) != 3 || len(result.Codes) != 3 {
		t.Fatalf("batch result mismatch: %+v", result)
	}

	hash, fp := CodeHash(1, "DUPLICATE")
	_, _, err = store.CreateBatch(ctx, createBatchRecord{
		TenantID: 1, AdminID: 10, RequestedCount: 2, AmountCents: 111, CurrencyCode: "USD",
		ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour), MaxRedemptions: 1,
		SingleUsePerUser: true, Now: now,
	}, []createVoucherRecord{
		{TenantID: 1, AdminID: 10, CodeHash: hash, CodeFingerprint: fp, AmountCents: 111, CurrencyCode: "USD", ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour), MaxRedemptions: 1, SingleUsePerUser: true, Now: now},
		{TenantID: 1, AdminID: 10, CodeHash: hash, CodeFingerprint: fp, AmountCents: 111, CurrencyCode: "USD", ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour), MaxRedemptions: 1, SingleUsePerUser: true, Now: now},
	})
	if !errors.Is(err, ErrVoucherDuplicate) {
		t.Fatalf("duplicate batch error = %v, want ErrVoucherDuplicate", err)
	}
	list, err := svc.List(ctx, ListInput{TenantID: 1, Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("duplicate batch was partially inserted; vouchers=%d, want 3", len(list))
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
}

func mustCreateVoucher(t *testing.T, svc *Service, code string, amount int64, now time.Time) CreateResult {
	t.Helper()
	return mustCreateVoucherWindow(t, svc, code, amount, now.Add(-time.Minute), now.Add(time.Hour), now)
}

func mustCreateVoucherWindow(t *testing.T, svc *Service, code string, amount int64, from, until, now time.Time) CreateResult {
	t.Helper()
	result, err := svc.Create(context.Background(), CreateInput{
		TenantID: 1, AdminID: 10, Code: code, AmountCents: amount,
		ValidFrom: from, ValidUntil: until, SingleUsePerUser: true, Now: now,
	})
	if err != nil {
		t.Fatalf("Create(%s) error = %v", code, err)
	}
	return result
}

func auditTypes(events []AuditEvent) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.EventType)
	}
	return out
}

func containsAll(haystack []string, needles ...string) bool {
	set := map[string]struct{}{}
	for _, item := range haystack {
		set[item] = struct{}{}
	}
	for _, needle := range needles {
		if _, ok := set[needle]; !ok {
			return false
		}
	}
	return true
}
