package usersession

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestFamilyBelongsToUser_HitMissCrossUser 是对那次索引支持的 ownership 查找的
// 判别性测试 —— 它取代了 session-family revoke 路径中原先的全量
// ListFamilies 扫描。
func TestFamilyBelongsToUser_HitMissCrossUser(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 5, 9, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()

	const tenant = int64(1)
	const owner = int64(42)
	const other = int64(43)

	if _, err := svc.Create(ctx, CreateInput{TenantID: tenant, UserID: owner, IP: "10.0.0.1", UserAgent: "Chrome/1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	families, err := svc.List(ctx, tenant, owner)
	if err != nil || len(families) != 1 {
		t.Fatalf("List owner families: err=%v fams=%d", err, len(families))
	}
	familyID := families[0].ID

	// 命中: owner 看得到自己的 family。
	if ok, err := svc.FamilyBelongsToUser(ctx, tenant, owner, familyID); err != nil || !ok {
		t.Fatalf("owner ownership = (%v,%v), want (true,nil)", ok, err)
	}

	// 跨用户: 同一租户下的另一个用户绝不能拥有它。
	if ok, err := svc.FamilyBelongsToUser(ctx, tenant, other, familyID); err != nil || ok {
		t.Fatalf("cross-user ownership = (%v,%v), want (false,nil)", ok, err)
	}

	// 跨租户: 不同租户下相同的 user id 绝不能拥有它。
	if ok, err := svc.FamilyBelongsToUser(ctx, tenant+1, owner, familyID); err != nil || ok {
		t.Fatalf("cross-tenant ownership = (%v,%v), want (false,nil)", ok, err)
	}

	// 未命中: 不存在的 family id 解析为 false (而非 error)。
	const ghost = "00000000-0000-0000-0000-0000000000ff"
	if ok, err := svc.FamilyBelongsToUser(ctx, tenant, owner, ghost); err != nil || ok {
		t.Fatalf("unknown-family ownership = (%v,%v), want (false,nil)", ok, err)
	}
}

// TestRevokeFamily_RejectsCrossUserOwnership 锁定接线: 用一个属于另一个
// 用户的 FamilyID (按 UserID 限定) 调用 Revoke, 必须经由索引化的 ownership
// 检查被拒, 返回 ErrFamilyNotFound 而非执行 revoke。
func TestRevokeFamily_RejectsCrossUserOwnership(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 5, 9, 30, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()

	const tenant = int64(7)
	const owner = int64(100)
	const attacker = int64(200)

	if _, err := svc.Create(ctx, CreateInput{TenantID: tenant, UserID: owner, IP: "10.0.0.9", UserAgent: "Chrome/9"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	families, err := svc.List(ctx, tenant, owner)
	if err != nil || len(families) != 1 {
		t.Fatalf("List: err=%v fams=%d", err, len(families))
	}
	familyID := families[0].ID

	// 攻击者 (不同的 UserID) 无法 revoke owner 的 family。
	if _, err := svc.Revoke(ctx, RevokeInput{TenantID: tenant, UserID: attacker, FamilyID: familyID}); !errors.Is(err, ErrFamilyNotFound) {
		t.Fatalf("cross-user revoke err = %v, want ErrFamilyNotFound", err)
	}
	// 被拒的 revoke 之后, family 必须仍然 active。
	families, err = svc.List(ctx, tenant, owner)
	if err != nil || len(families) != 1 || families[0].Status == FamilyStatusRevoked {
		t.Fatalf("owner family unexpectedly mutated: err=%v fams=%+v", err, families)
	}

	// owner 能 revoke 自己的 family。
	if n, err := svc.Revoke(ctx, RevokeInput{TenantID: tenant, UserID: owner, FamilyID: familyID}); err != nil || n != 1 {
		t.Fatalf("owner revoke = (%d,%v), want (1,nil)", n, err)
	}
}
