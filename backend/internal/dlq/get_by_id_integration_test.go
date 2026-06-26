//go:build integration_pg

package dlq

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestGetByID_ResolvesBeyondListWindowAndIsTenantScoped 是支撑 dlq_replay 目标查找的
// 租户作用域按 id 读 DLQ 的 H4 S3 判别性测试。
//
// 它证明旧的 List(limit) 取回再匹配的查找方式做不到的三条性质：
//  1. 比有界 List 窗口更老的记录仍能按 id 解析出来。我们灌入比小 List 窗口
//     返回数更多的行，并确认 GetByID 找到了【最老】的那行，而 List(小窗口)
//     （按 failure_at DESC, id DESC 排序）会把它【排除】——因此解析与窗口无关。
//  2. 错误租户的 id【无法】解析（租户隔离）：对【另一个】租户名下的行做
//     GetByID 返回 ErrNotFound，绝不返回外租户记录。
//  3. 不存在的 id 返回 ErrNotFound。
//
// 变异（自证）：
//   - 如果 GetByID 的 SELECT 去掉 `AND d.tenant_id = $2`，错误租户的查找会
//     【返回】外租户记录，ErrNotFound 断言会变红。
//   - 如果 GetByID 改成在有界窗口内匹配而非直接按 id 读，最老行的查找
//     （它落在小 List 窗口之外）会失败，解析断言会变红。
func TestGetByID_ResolvesBeyondListWindowAndIsTenantScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openDLQPool(t, ctx)
	tenantA := seedDLQTenant(t, ctx, pool)
	tenantB := seedDLQTenant(t, ctx, pool)
	store := NewStore(pool)

	// 给租户 A 灌入若干行，failure_at 严格递增，使 List 排序
	// （failure_at DESC, id DESC）确定。【第一】行灌入的是【最老】的
	// -> 它排在【最后】，会被小 List 窗口排除。
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

	// 小 List 窗口（小于灌入数）返回【最新】的几行，【排除】最老的那行
	// ——这正是旧查找存在的缺口。
	window := store.mustListWindow(t, ctx, tenantA, seeded-2)
	if _, present := window[oldestID]; present {
		t.Fatalf("test precondition broken: oldest id=%d unexpectedly inside the small List window", oldestID)
	}

	// 1. 即便最老的行落在 List 窗口之外，GetByID 仍能解析它。
	got, err := store.GetByID(ctx, tenantA, oldestID)
	if err != nil {
		t.Fatalf("GetByID(tenantA, oldest=%d) err=%v want the record (must resolve beyond the List window)", oldestID, err)
	}
	if got.ID != oldestID || got.TenantID != tenantA {
		t.Fatalf("GetByID returned id=%d tenant=%d want id=%d tenant=%d", got.ID, got.TenantID, oldestID, tenantA)
	}

	// 2. 同一个 id 在租户 B 名下【无法】解析（租户作用域）。
	if _, err := store.GetByID(ctx, tenantB, oldestID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID(tenantB, tenantA's id=%d) err=%v want ErrNotFound — must not cross tenants", oldestID, err)
	}

	// 3. 不存在的 id 返回 ErrNotFound。
	if _, err := store.GetByID(ctx, tenantA, oldestID+1_000_000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID(tenantA, missing id) err=%v want ErrNotFound", err)
	}
}

// mustListWindow 返回该租户一次有界 List 读出的 id 集合——即旧查找在其内匹配的
// 窗口。用于证明新的按 id 读能找到有界窗口排除掉的行。
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
