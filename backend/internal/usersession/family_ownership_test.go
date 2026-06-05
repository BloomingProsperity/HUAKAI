package usersession

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestFamilyBelongsToUser_HitMissCrossUser is a discriminating test for the
// index-backed ownership lookup that replaced the full ListFamilies scan in the
// session-family revoke path.
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

	// Hit: owner sees their own family.
	if ok, err := svc.FamilyBelongsToUser(ctx, tenant, owner, familyID); err != nil || !ok {
		t.Fatalf("owner ownership = (%v,%v), want (true,nil)", ok, err)
	}

	// Cross-user: a different user in the same tenant must NOT own it.
	if ok, err := svc.FamilyBelongsToUser(ctx, tenant, other, familyID); err != nil || ok {
		t.Fatalf("cross-user ownership = (%v,%v), want (false,nil)", ok, err)
	}

	// Cross-tenant: same user id under a different tenant must NOT own it.
	if ok, err := svc.FamilyBelongsToUser(ctx, tenant+1, owner, familyID); err != nil || ok {
		t.Fatalf("cross-tenant ownership = (%v,%v), want (false,nil)", ok, err)
	}

	// Miss: a family id that does not exist resolves to false (not an error).
	const ghost = "00000000-0000-0000-0000-0000000000ff"
	if ok, err := svc.FamilyBelongsToUser(ctx, tenant, owner, ghost); err != nil || ok {
		t.Fatalf("unknown-family ownership = (%v,%v), want (false,nil)", ok, err)
	}
}

// TestRevokeFamily_RejectsCrossUserOwnership pins the wiring: Revoke with a
// FamilyID owned by another user (scoped by UserID) must be denied via the
// indexed ownership check, returning ErrFamilyNotFound rather than revoking.
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

	// Attacker (different UserID) cannot revoke the owner's family.
	if _, err := svc.Revoke(ctx, RevokeInput{TenantID: tenant, UserID: attacker, FamilyID: familyID}); !errors.Is(err, ErrFamilyNotFound) {
		t.Fatalf("cross-user revoke err = %v, want ErrFamilyNotFound", err)
	}
	// Family must still be active after the denied revoke.
	families, err = svc.List(ctx, tenant, owner)
	if err != nil || len(families) != 1 || families[0].Status == FamilyStatusRevoked {
		t.Fatalf("owner family unexpectedly mutated: err=%v fams=%+v", err, families)
	}

	// Owner can revoke their own family.
	if n, err := svc.Revoke(ctx, RevokeInput{TenantID: tenant, UserID: owner, FamilyID: familyID}); err != nil || n != 1 {
		t.Fatalf("owner revoke = (%d,%v), want (1,nil)", n, err)
	}
}
