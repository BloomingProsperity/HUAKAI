package auditledger

import (
	"context"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

// TestB20_MemoryLedgerPerTenantMerkleChain 断言 MemoryLedger 与 PostgresLedger
// 一样按 tenant 维护独立的 Merkle 链（每个 tenant 首条 PrevMerkleRoot==ZeroRoot，
// 后续逐条链接到同一 tenant 的上一条），而不是跨 tenant 的全局链。
//
// 复现路径与 audit-export verify endpoint 完全一致：ListByRange 取出某个 tenant
// 的范围化子列表，再对该子列表跑 VerifyChain。当 MemoryLedger 用全局链时，
// 子列表里的 prev-root 指向其他 tenant 的 entry，VerifyChain 会报
// prev_merkle_root_mismatch —— 这正是本 bug（B20）。
func TestB20_MemoryLedgerPerTenantMerkleChain(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, err := NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("NewMemoryLedger: %v", err)
	}
	ctx := context.Background()

	// 交错写入两个 tenant，模拟真实多租户流量。
	appends := []LedgerEntry{
		{RequestID: "t7_r1", TenantID: 7, Timestamp: "2026-06-01T00:00:00Z"},
		{RequestID: "t8_r1", TenantID: 8, Timestamp: "2026-06-01T00:00:01Z"},
		{RequestID: "t7_r2", TenantID: 7, Timestamp: "2026-06-01T00:00:02Z"},
		{RequestID: "t8_r2", TenantID: 8, Timestamp: "2026-06-01T00:00:03Z"},
		{RequestID: "t7_r3", TenantID: 7, Timestamp: "2026-06-01T00:00:04Z"},
	}
	for _, e := range appends {
		if _, err := l.Append(ctx, mustPrepareForAppend(t, ctx, e)); err != nil {
			t.Fatalf("Append %s: %v", e.RequestID, err)
		}
	}

	// verify endpoint 的真实路径：按 tenant scope 拉子列表 -> VerifyChain。
	for _, tenantID := range []int64{7, 8} {
		rows, err := l.ListByRange(ctx, TenantScopeRef(tenantID), time.Time{}, time.Time{}, 100)
		if err != nil {
			t.Fatalf("ListByRange tenant %d: %v", tenantID, err)
		}
		if len(rows) == 0 {
			t.Fatalf("tenant %d: expected rows, got none", tenantID)
		}
		// 正确行为：每个 tenant 的子链自成完整 Merkle 链。
		if rows[0].PrevMerkleRoot != ZeroRoot {
			t.Errorf("tenant %d: first entry PrevMerkleRoot must be ZeroRoot (per-tenant chain), got non-zero", tenantID)
		}
		if err := VerifyChain(rows); err != nil {
			t.Errorf("tenant %d: per-tenant chain must verify, got: %v", tenantID, err)
		}
	}
}
