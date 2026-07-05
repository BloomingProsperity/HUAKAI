//go:build integration_pg

package quota

import (
	"context"
	"testing"
	"time"
)

// TestPostgresStore_ReleaseConcurrencySlots_CountsAndErrorPath 守住释放并发槽不是
// 只返回 nil:成功路径必须把目标 reservation 的 acquired 槽转成 released,错误路径不能
// 改动槽表。Mutation: 删除 ReleaseQuotaConcurrencySlotsByReservation 调用或忽略错误后
// 继续写槽表会分别在成功/错误断言变红。
func TestPostgresStore_ReleaseConcurrencySlots_CountsAndErrorPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)

	now := time.Date(2026, 7, 5, 12, 30, 0, 0, time.UTC)
	scope := Scope{TenantID: f.tenantID, Kind: ScopeUser, ID: "u1"}
	firstClaimID := f.seedClaim("release-slot-first")
	firstReservationID := f.seedReservation(firstClaimID, "release-slot-first")
	secondClaimID := f.seedClaim("release-slot-second")
	secondReservationID := f.seedReservation(secondClaimID, "release-slot-second")
	for _, item := range []struct {
		claimID       int64
		reservationID int64
	}{
		{claimID: firstClaimID, reservationID: firstReservationID},
		{claimID: secondClaimID, reservationID: secondReservationID},
	} {
		slot, err := store.AcquireConcurrencySlot(ctx, ConcurrencyAcquire{
			TenantID:       f.tenantID,
			ReservationID:  item.reservationID,
			ClaimID:        item.claimID,
			Scope:          scope,
			SlotLimit:      2,
			At:             now,
			LeaseExpiresAt: now.Add(5 * time.Minute),
		})
		if err != nil {
			t.Fatalf("AcquireConcurrencySlot reservation=%d: %v", item.reservationID, err)
		}
		if slot.ID == 0 {
			t.Fatalf("AcquireConcurrencySlot reservation=%d returned empty slot", item.reservationID)
		}
	}
	if got := f.activeSlotCount(ScopeUser, "u1"); got != 2 {
		t.Fatalf("release 前 active slots=%d; want 2", got)
	}

	canceledCtx, cancelRelease := context.WithCancel(ctx)
	cancelRelease()
	if err := store.ReleaseConcurrencySlots(canceledCtx, f.tenantID, firstReservationID, "canceled-release"); err == nil {
		t.Fatal("ReleaseConcurrencySlots canceled err=nil; want context cancellation error")
	}
	if got := f.activeSlotCount(ScopeUser, "u1"); got != 2 {
		t.Fatalf("取消释放后 active slots=%d; want 2(错误路径不得改槽表)", got)
	}
	if got := f.concurrencySlotCount(firstReservationID, "acquired"); got != 1 {
		t.Fatalf("取消释放后 first acquired slots=%d; want 1", got)
	}
	if got := f.concurrencySlotCount(firstReservationID, "released"); got != 0 {
		t.Fatalf("取消释放后 first released slots=%d; want 0", got)
	}

	if err := store.ReleaseConcurrencySlots(ctx, f.tenantID, firstReservationID, "normal-release"); err != nil {
		t.Fatalf("ReleaseConcurrencySlots: %v", err)
	}
	if got := f.activeSlotCount(ScopeUser, "u1"); got != 1 {
		t.Fatalf("release 后 active slots=%d; want 1(只释放目标 reservation)", got)
	}
	if got := f.concurrencySlotCount(firstReservationID, "acquired"); got != 0 {
		t.Fatalf("release 后 first acquired slots=%d; want 0", got)
	}
	if got := f.concurrencySlotCount(firstReservationID, "released"); got != 1 {
		t.Fatalf("release 后 first released slots=%d; want 1", got)
	}
	if got := f.concurrencySlotCount(secondReservationID, "acquired"); got != 1 {
		t.Fatalf("release 后 second acquired slots=%d; want 1", got)
	}
	if got := f.concurrencySlotReleaseReason(firstReservationID); got != "normal-release" {
		t.Fatalf("release_reason=%q; want normal-release", got)
	}
}

func (f *quotaFixture) concurrencySlotCount(reservationID int64, status string) int64 {
	f.t.Helper()
	var count int64
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*)
		 FROM quota_concurrency_slots
		 WHERE tenant_id=$1 AND reservation_id=$2 AND status=$3`,
		f.tenantID, reservationID, status,
	).Scan(&count); err != nil {
		f.t.Fatalf("count concurrency slots reservation=%d status=%s: %v", reservationID, status, err)
	}
	return count
}

func (f *quotaFixture) concurrencySlotReleaseReason(reservationID int64) string {
	f.t.Helper()
	var reason string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COALESCE(release_reason, '')
		 FROM quota_concurrency_slots
		 WHERE tenant_id=$1 AND reservation_id=$2 AND status='released'
		 ORDER BY id DESC
		 LIMIT 1`,
		f.tenantID, reservationID,
	).Scan(&reason); err != nil {
		f.t.Fatalf("read concurrency slot release reason reservation=%d: %v", reservationID, err)
	}
	return reason
}
