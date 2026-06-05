package usersession

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDevicePolicyUsesBoundedActiveFamilyList(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 5, 9, 0, 0, 0, time.UTC)
	store := &policyListProbeStore{MemoryStore: NewMemoryStore()}
	seedDevicePolicyFamilies(t, store.MemoryStore, 1, 42, base)
	policyLimit := 5

	listed, err := store.MemoryStore.ListActiveFamiliesForDevicePolicy(ctx, 1, 42, policyLimit)
	if err != nil {
		t.Fatalf("ListActiveFamiliesForDevicePolicy: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("memory policy family count=%d want 3 active/suspicious families", len(listed))
	}

	svc := NewService(store)
	svc.Now = func() time.Time { return base.Add(time.Hour) }
	svc.SigningKey = testSigningKey()
	svc.MaxActiveFamilies = policyLimit

	if _, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 42, IP: "10.1.1.1", UserAgent: "Chrome/1"}); err != nil {
		t.Fatalf("Create should ignore revoked history below active limit: %v", err)
	}
	if !store.policyListed {
		t.Fatal("device policy did not use policy-specific family list")
	}
	if store.policyLimit != svc.MaxActiveFamilies {
		t.Fatalf("policy list limit=%d want %d", store.policyLimit, svc.MaxActiveFamilies)
	}
	if store.policyCount != 3 {
		t.Fatalf("policy family count=%d want 3 active/suspicious families", store.policyCount)
	}
	for _, status := range store.policyStatuses {
		if status != FamilyStatusActive && status != FamilyStatusSuspicious {
			t.Fatalf("policy list included status %q; want only active/suspicious", status)
		}
	}
}

func TestPostgresDevicePolicyListUsesStatusFilterAndLimit(t *testing.T) {
	ctx := context.Background()
	db := &capturePolicyDB{}

	families, err := NewPostgresStore(db).ListActiveFamiliesForDevicePolicy(ctx, 11, 22, 7)
	if err != nil {
		t.Fatalf("ListActiveFamiliesForDevicePolicy: %v", err)
	}
	if len(families) != 0 {
		t.Fatalf("families=%d want 0 from empty rows", len(families))
	}
	if !strings.Contains(db.query, "status IN ('active', 'suspicious')") {
		t.Fatalf("postgres policy query missing active status filter: %s", db.query)
	}
	if !strings.Contains(db.query, "LIMIT $3") {
		t.Fatalf("postgres policy query missing bound: %s", db.query)
	}
	if len(db.args) != 3 || db.args[0] != int64(11) || db.args[1] != int64(22) || db.args[2] != 7 {
		t.Fatalf("postgres policy args=%#v want tenant/user/limit", db.args)
	}
}

type policyListProbeStore struct {
	*MemoryStore
	policyListed   bool
	policyLimit    int
	policyCount    int
	policyStatuses []FamilyStatus
}

func (s *policyListProbeStore) ListFamilies(context.Context, int64, int64) ([]SessionFamily, error) {
	return nil, errors.New("device policy used unbounded ListFamilies")
}

func (s *policyListProbeStore) ListActiveFamiliesForDevicePolicy(
	ctx context.Context,
	tenantID, userID int64,
	limit int,
) ([]SessionFamily, error) {
	s.policyListed = true
	s.policyLimit = limit
	families, err := s.MemoryStore.ListActiveFamiliesForDevicePolicy(ctx, tenantID, userID, limit)
	s.policyCount = len(families)
	s.policyStatuses = s.policyStatuses[:0]
	for _, family := range families {
		s.policyStatuses = append(s.policyStatuses, family.Status)
	}
	return families, err
}

func seedDevicePolicyFamilies(t *testing.T, store *MemoryStore, tenantID, userID int64, base time.Time) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	for i := 0; i < 40; i++ {
		revokedAt := base.Add(time.Duration(i) * time.Minute)
		family := SessionFamily{
			ID: fmt.Sprintf("revoked-%02d", i), TenantID: tenantID, UserID: userID,
			Status: FamilyStatusRevoked, CreatedAt: base.Add(-time.Hour),
			LastActiveAt: revokedAt, RevokedAt: &revokedAt,
		}
		store.families[family.ID] = family
	}
	statuses := []FamilyStatus{FamilyStatusActive, FamilyStatusSuspicious, FamilyStatusActive}
	for i, status := range statuses {
		family := SessionFamily{
			ID: fmt.Sprintf("active-%02d", i), TenantID: tenantID, UserID: userID,
			Status: status, CreatedAt: base.Add(-2 * time.Hour),
			LastActiveAt: base.Add(-time.Duration(i+1) * time.Minute),
		}
		store.families[family.ID] = family
	}
}

type capturePolicyDB struct {
	query string
	args  []any
}

func (d *capturePolicyDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (d *capturePolicyDB) Query(_ context.Context, query string, args ...interface{}) (pgx.Rows, error) {
	d.query = query
	d.args = append([]any(nil), args...)
	return emptyPolicyRows{}, nil
}

func (d *capturePolicyDB) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return policyRow{err: pgx.ErrNoRows}
}

type emptyPolicyRows struct{}

func (emptyPolicyRows) Close()                                       {}
func (emptyPolicyRows) Err() error                                   { return nil }
func (emptyPolicyRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (emptyPolicyRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (emptyPolicyRows) Next() bool                                   { return false }
func (emptyPolicyRows) Scan(...any) error                            { return errors.New("unexpected scan") }
func (emptyPolicyRows) Values() ([]any, error)                       { return nil, errors.New("unexpected values") }
func (emptyPolicyRows) RawValues() [][]byte                          { return nil }
func (emptyPolicyRows) Conn() *pgx.Conn                              { return nil }

type policyRow struct {
	err error
}

func (r policyRow) Scan(...interface{}) error {
	return r.err
}
