package pricingcatalog

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/shopspring/decimal"
)

// TestVerifyChain_DetectsTamperedEntryHash 通过审计写入路径构造一条真实的
// 两条目签名哈希链，先确认 VerifyChain 证明其完好，随后篡改其中一条已存储的
// entry_hash，并断言 VerifyChain 能精确指出出问题的那条记录。变异判别力：
// 一个永远返回 OK（或忘记重算 entry_hash）的校验器会让篡改断言失败。
func TestVerifyChain_DetectsTamperedEntryHash(t *testing.T) {
	ctx := context.Background()
	db := newRatioAuditTxDB()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := newPostgresStoreWithAuditDB(db, signer, fixedRatioAuditNow)

	// 两次审计写入 => 形成两段链（先 upsert，再 re-upsert）。
	for _, ratio := range []string{"1.10", "1.20"} {
		if _, err := store.UpsertRatio(ctx, UpsertRatioParams{
			TenantID:    7,
			PoolGroupID: 42,
			Ratio:       decimal.RequireFromString(ratio),
			PublicRatio: true,
			Actor:       "admin_token:99",
			ActorRole:   "platform_admin",
		}); err != nil {
			t.Fatalf("UpsertRatio(%s): %v", ratio, err)
		}
	}
	if len(db.auditRows) != 2 {
		t.Fatalf("audit rows=%d want 2", len(db.auditRows))
	}

	// 完好的链必须证明为干净，且没有出问题的记录。
	clean, err := store.VerifyChain(ctx)
	if err != nil {
		t.Fatalf("VerifyChain(clean): %v", err)
	}
	if !clean.OK || clean.RowID != 0 || clean.Reason != "" {
		t.Fatalf("clean verify=%+v want OK with no offending row", clean)
	}

	// 篡改：翻转第二条记录已存储 entry_hash 的一个字节，模拟运维人员
	// 怀疑有人悄悄改动过某条计费倍率变更记录的情形。
	tamperedID := db.auditRows[1].ID
	if len(db.auditRows[1].EntryHash) == 0 {
		t.Fatalf("row 2 has empty entry_hash; cannot tamper")
	}
	db.auditRows[1].EntryHash[0] ^= 0xFF

	tampered, err := store.VerifyChain(ctx)
	if err != nil {
		t.Fatalf("VerifyChain(tampered): %v", err)
	}
	if tampered.OK {
		t.Fatalf("tampered chain reported OK=true; verifier did not detect mutation")
	}
	if tampered.RowID != tamperedID {
		t.Fatalf("offending row_id=%d want %d (the mutated row)", tampered.RowID, tamperedID)
	}
	if tampered.Reason == "" {
		t.Fatalf("tampered verify gave empty reason; want a non-empty diagnosis")
	}
}
