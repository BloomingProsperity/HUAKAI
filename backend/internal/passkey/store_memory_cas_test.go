package passkey

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestMemoryStoreUpdateCredentialUsageCAS 守护 MemoryStore 与 PostgresStore 的 CAS
// 契约对齐:非递增(回退/重放)写入被挡返 ErrCloneDetected,递增放行。
// 变异证伪:删 MemoryStore.UpdateCredentialUsage 里的 CAS 分支(回退到盲写),
// 则回退用例返 nil 且库内被盲写成 3 → 两条断言转红。
func TestMemoryStoreUpdateCredentialUsageCAS(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	cid := []byte("cas-mem")
	if _, err := store.SaveCredential(ctx, CredentialRecord{
		TenantID: 7, UserID: 1, CredentialID: cid, PublicKey: []byte("pk"), SignCount: 5, CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}
	// 回退 asserted=3 < stored=5:挡。
	if _, err := store.UpdateCredentialUsage(ctx, 7, cid, 3, false, now.Add(time.Hour)); !errors.Is(err, ErrCloneDetected) {
		t.Fatalf("回退 err=%v want ErrCloneDetected", err)
	}
	// 反向不变量:盲写没发生,库内仍 5。
	if got, err := store.GetCredentialByCredentialID(ctx, 7, cid); err != nil || got.SignCount != 5 {
		t.Fatalf("CAS 失败后 sign_count 应仍 5,got=%d err=%v", got.SignCount, err)
	}
	// 重放 asserted==stored:挡。
	if _, err := store.UpdateCredentialUsage(ctx, 7, cid, 5, false, now.Add(time.Hour)); !errors.Is(err, ErrCloneDetected) {
		t.Fatalf("重放 err=%v want ErrCloneDetected", err)
	}
	// 递增 asserted=9 > 5:放行。
	if updated, err := store.UpdateCredentialUsage(ctx, 7, cid, 9, false, now.Add(2*time.Hour)); err != nil || updated.SignCount != 9 {
		t.Fatalf("递增应放行,got=%d err=%v", updated.SignCount, err)
	}
}

// TestMemoryStoreCloneWarningOnlyRaises 守护"clone_warning 只升不降":一条历史被
// 置位告警的凭据,正常递增登录(入参 cloneWarning=false)不得把它抹回 false。
// 变异证伪:把 MemoryStore 的 `record.CloneWarning = record.CloneWarning || cloneWarning`
// 改回 `record.CloneWarning = cloneWarning`,则递增后告警被重置 → 末断言转红。
func TestMemoryStoreCloneWarningOnlyRaises(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	cid := []byte("warn-mem")
	if _, err := store.SaveCredential(ctx, CredentialRecord{
		TenantID: 7, UserID: 1, CredentialID: cid, PublicKey: []byte("pk"), SignCount: 5, CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}
	// 历史置位告警(模拟某次疑似克隆)。
	if err := store.FlagCredentialCloneWarning(ctx, 7, cid, now); err != nil {
		t.Fatalf("Flag: %v", err)
	}
	// 正常递增登录,cloneWarning 入参 false。
	updated, err := store.UpdateCredentialUsage(ctx, 7, cid, 9, false, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("递增 err=%v", err)
	}
	if !updated.CloneWarning {
		t.Fatal("正常递增不应把已置位的 clone_warning 重置为 false(防克隆信号丢失)")
	}
}
