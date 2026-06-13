//go:build integration_pg

package dlq

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestGetByID_ResolvesBeyondListWindowAndIsTenantScoped is the H4 S3
// discriminating test for the tenant-scoped by-id DLQ read that backs the
// dlq_replay target lookup.
//
// It proves three properties the old List(limit)-and-match lookup could not:
//  1. A record older than a bounded List window still resolves by id. We seed
//     more rows than a small List window returns and confirm GetByID finds the
//     OLDEST row while List(small window) (ordered failure_at DESC, id DESC)
//     EXCLUDES it — so the resolution is window-independent.
//  2. A wrong-tenant id does NOT resolve (tenant isolation): GetByID for the row
//     under a DIFFERENT tenant returns ErrNotFound, never the foreign record.
//  3. A non-existent id returns ErrNotFound.
//
// MUTATION (self-proving):
//   - If GetByID's SELECT dropped `AND d.tenant_id = $2`, the wrong-tenant lookup
//     would RETURN the foreign record and the ErrNotFound assertion goes RED.
//   - If GetByID matched within a bounded window instead of a direct id read, the
//     oldest-row lookup (which sits outside the small List window) would fail and
//     the resolve assertion goes RED.
func TestGetByID_ResolvesBeyondListWindowAndIsTenantScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openDLQPool(t, ctx)
	tenantA := seedDLQTenant(t, ctx, pool)
	tenantB := seedDLQTenant(t, ctx, pool)
	store := NewStore(pool)

	// Seed several rows for tenant A with strictly increasing failure_at so the
	// List ordering (failure_at DESC, id DESC) is deterministic. The FIRST seeded
	// row is the OLDEST -> it sorts LAST and is excluded by a small List window.
	const seeded = 6
	base := time.Now().UTC().Add(-time.Hour)
	ids := make([]int64, 0, seeded)
	for i := 0; i < seeded; i++ {
		id, err := store.Enqueue(ctx, Event{
			TenantID:       tenantA,
			EventKind:      EventKindUsageRecord,
			Lane:           LaneMed,
			Payload:        []byte(`{"x":1}`),
			FailureReason:  "h4s3_seed",
			IdempotencyKey: "h4s3:a:" + string(rune('a'+i)),
			SourceTable:    "usage_records",
			SourceID:       int64(i + 1),
			NextRetryAt:    base.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("enqueue tenantA[%d]: %v", i, err)
		}
		ids = append(ids, id)
	}
	oldestID := ids[0]

	// A small List window (smaller than seeded) returns the NEWEST rows and
	// EXCLUDES the oldest — this is precisely the gap the old lookup had.
	window := store.mustListWindow(t, ctx, tenantA, seeded-2)
	if _, present := window[oldestID]; present {
		t.Fatalf("test precondition broken: oldest id=%d unexpectedly inside the small List window", oldestID)
	}

	// 1. GetByID resolves the oldest row even though it is outside the List window.
	got, err := store.GetByID(ctx, tenantA, oldestID)
	if err != nil {
		t.Fatalf("GetByID(tenantA, oldest=%d) err=%v want the record (must resolve beyond the List window)", oldestID, err)
	}
	if got.ID != oldestID || got.TenantID != tenantA {
		t.Fatalf("GetByID returned id=%d tenant=%d want id=%d tenant=%d", got.ID, got.TenantID, oldestID, tenantA)
	}

	// 2. The SAME id under tenant B does NOT resolve (tenant-scoped).
	if _, err := store.GetByID(ctx, tenantB, oldestID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID(tenantB, tenantA's id=%d) err=%v want ErrNotFound — must not cross tenants", oldestID, err)
	}

	// 3. A non-existent id returns ErrNotFound.
	if _, err := store.GetByID(ctx, tenantA, oldestID+1_000_000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID(tenantA, missing id) err=%v want ErrNotFound", err)
	}
}

// mustListWindow returns the id-set of a bounded List read for the tenant — the
// window the OLD lookup matched within. Used to prove the new by-id read finds
// rows the bounded window excludes.
func (s *Store) mustListWindow(t *testing.T, ctx context.Context, tenantID int64, limit int) map[int64]struct{} {
	t.Helper()
	rows, err := s.List(ctx, ListFilter{TenantID: &tenantID, Limit: limit})
	if err != nil {
		t.Fatalf("List window: %v", err)
	}
	set := make(map[int64]struct{}, len(rows))
	for i := range rows {
		set[rows[i].ID] = struct{}{}
	}
	return set
}
