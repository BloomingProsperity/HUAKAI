package invitation

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestATCOMM001001CodeGlobalUniqueAcrossGeneratedSample(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	service := NewService(store, WithNow(func() time.Time { return now }))

	seen := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		out, err := service.Generate(context.Background(), GenerateInvitationParams{
			TenantID: 1, InviterUserID: 10, MaxUsage: 1, ExpiresInDays: 30,
		})
		if err != nil {
			t.Fatalf("Generate #%d: %v", i, err)
		}
		if !ValidCode(out.Code) {
			t.Fatalf("generated invalid code %q", out.Code)
		}
		if _, ok := seen[out.Code]; ok {
			t.Fatalf("duplicate code generated: %s", out.Code)
		}
		seen[out.Code] = struct{}{}
	}
}

func TestATCOMM001002TenantMonthlyQuotaRejectsAfterOneHundred(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	service := NewService(store,
		WithNow(func() time.Time { return now }),
		WithCodeGenerator(&sequenceGenerator{}),
	)

	for i := 0; i < MonthlyTenantQuota; i++ {
		if _, err := service.Generate(context.Background(), GenerateInvitationParams{
			TenantID: 7, InviterUserID: 42, MaxUsage: 1, ExpiresInDays: 30,
		}); err != nil {
			t.Fatalf("Generate #%d before quota: %v", i, err)
		}
	}
	_, err := service.Generate(context.Background(), GenerateInvitationParams{
		TenantID: 7, InviterUserID: 42, MaxUsage: 1, ExpiresInDays: 30,
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("err=%v want ErrQuotaExceeded", err)
	}
}

func TestGenerateReusesClientIdempotencyKeyAtQuota(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	service := NewService(store,
		WithNow(func() time.Time { return now }),
		WithCodeGenerator(&sequenceGenerator{}),
	)
	key := "tenant-7-create-1"
	first, err := service.Generate(context.Background(), GenerateInvitationParams{
		TenantID: 7, InviterUserID: 42, MaxUsage: 1, ExpiresInDays: 30, ClientIdempotencyKey: &key,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < MonthlyTenantQuota; i++ {
		if _, err := service.Generate(context.Background(), GenerateInvitationParams{
			TenantID: 7, InviterUserID: 42, MaxUsage: 1, ExpiresInDays: 30,
		}); err != nil {
			t.Fatalf("Generate filler #%d before quota: %v", i, err)
		}
	}
	again, err := service.Generate(context.Background(), GenerateInvitationParams{
		TenantID: 7, InviterUserID: 42, MaxUsage: 3, ExpiresInDays: 5, ClientIdempotencyKey: &key,
	})
	if err != nil {
		t.Fatalf("idempotent retry at quota: %v", err)
	}
	if again.Code != first.Code || again.MaxUsage != first.MaxUsage || !again.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatalf("idempotent retry returned %+v want original %+v", again, first)
	}
	if got := store.rowCount(); got != MonthlyTenantQuota {
		t.Fatalf("row count=%d want %d", got, MonthlyTenantQuota)
	}
}

func TestATCOMM001003ExpiresAtWrittenFromRequestWindow(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	service := NewService(store,
		WithNow(func() time.Time { return now }),
		WithCodeGenerator(&sequenceGenerator{}),
	)

	out, err := service.Generate(context.Background(), GenerateInvitationParams{
		TenantID: 2, InviterUserID: 20, MaxUsage: 3, ExpiresInDays: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := now.AddDate(0, 0, 5)
	if !out.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at=%s want %s", out.ExpiresAt, want)
	}
	row, err := store.GetByCode(context.Background(), out.Code)
	if err != nil {
		t.Fatal(err)
	}
	if row.ExpiresAt == nil || !row.ExpiresAt.Equal(want) {
		t.Fatalf("stored expires_at=%v want %s", row.ExpiresAt, want)
	}
}

func TestATCOMM001004MaxUsageValidationAndMigrationConstraint(t *testing.T) {
	store := newMemoryStore()
	service := NewService(store, WithCodeGenerator(&sequenceGenerator{}))
	_, err := service.Generate(context.Background(), GenerateInvitationParams{
		TenantID: 1, InviterUserID: 1, MaxUsage: 0, ExpiresInDays: 30,
	})
	if err != nil {
		t.Fatalf("zero max_usage should default: %v", err)
	}
	_, err = service.Generate(context.Background(), GenerateInvitationParams{
		TenantID: 1, InviterUserID: 1, MaxUsage: -1, ExpiresInDays: 30,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative max_usage err=%v want ErrInvalidInput", err)
	}
	_, err = service.Generate(context.Background(), GenerateInvitationParams{
		TenantID: 1, InviterUserID: 1, MaxUsage: MaxUsageLimit + 1, ExpiresInDays: 30,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized max_usage err=%v want ErrInvalidInput", err)
	}
	_, err = service.Generate(context.Background(), GenerateInvitationParams{
		TenantID: 1, InviterUserID: 1, MaxUsage: 1, ExpiresInDays: maxExpiresDays + 1,
	})
	if !errors.Is(err, ErrInvitationExpiresOverLimit) {
		t.Fatalf("oversized expires_in_days err=%v want ErrInvitationExpiresOverLimit", err)
	}

	raw, err := os.ReadFile("../../../sql/migrations/0034_community_invitation_referral.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CHECK (max_usage > 0)",
		"CHECK (usage_count <= max_usage)",
		"CONSTRAINT invitations_tenant_id_id_unique UNIQUE (tenant_id, id)",
		"client_idempotency_key TEXT",
		"CREATE UNIQUE INDEX idx_invitations_tenant_client_idempotency",
		"WHERE client_idempotency_key IS NOT NULL",
		"CONSTRAINT referrals_tenant_id_id_unique UNIQUE (tenant_id, id)",
		"CONSTRAINT referrals_invitation_tenant_fk FOREIGN KEY (tenant_id, invitation_id) REFERENCES invitations(tenant_id, id)",
		"CREATE UNIQUE INDEX idx_referrals_billing_event",
		"WHERE first_billing_event_id IS NOT NULL",
		"receipt_id BIGINT NOT NULL REFERENCES user_cost_receipts(id)",
		"CONSTRAINT referral_rewards_referral_tenant_fk FOREIGN KEY (tenant_id, referral_id) REFERENCES referrals(tenant_id, id)",
		"current_tier TEXT NOT NULL DEFAULT 'none' CHECK (current_tier IN ('none', 'silver', 'gold', 'platinum'))",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing constraint %q", want)
		}
	}
}

func TestGenerateRetriesDuplicateCode(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	generator := &sequenceGenerator{codes: []string{"00000001", "00000001", "00000002"}}
	service := NewService(store,
		WithNow(func() time.Time { return now }),
		WithCodeGenerator(generator),
	)

	if _, err := service.Generate(context.Background(), GenerateInvitationParams{
		TenantID: 1, InviterUserID: 1, MaxUsage: 1, ExpiresInDays: 30,
	}); err != nil {
		t.Fatal(err)
	}
	out, err := service.Generate(context.Background(), GenerateInvitationParams{
		TenantID: 1, InviterUserID: 1, MaxUsage: 1, ExpiresInDays: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Code != "00000002" {
		t.Fatalf("code=%s want retry code 00000002", out.Code)
	}
}

type memoryStore struct {
	mu          sync.Mutex
	nextID      int64
	rows        []Invitation
	idempotency map[memoryIdempotencyKey]string
	selfCodes   map[string]bool
}

type memoryIdempotencyKey struct {
	tenantID int64
	key      string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{nextID: 1, idempotency: map[memoryIdempotencyKey]string{}, selfCodes: map[string]bool{}}
}

func (s *memoryStore) Generate(_ context.Context, rec generateRecord) (Invitation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	code := NormalizeCode(rec.Code)
	if rec.ClientIdempotencyKey != nil {
		key := memoryIdempotencyKey{tenantID: rec.TenantID, key: *rec.ClientIdempotencyKey}
		if existingCode, ok := s.idempotency[key]; ok {
			return s.findByCodeLocked(existingCode)
		}
	}
	for _, row := range s.rows {
		if row.Code == code {
			return Invitation{}, ErrDuplicateCode
		}
	}
	expiresAt := rec.ExpiresAt.UTC()
	row := Invitation{
		ID: s.nextID, TenantID: rec.TenantID, Code: code, InviterUserID: rec.InviterUserID,
		CreatedAt: rec.CreatedAt.UTC(), ExpiresAt: &expiresAt, MaxUsage: rec.MaxUsage,
	}
	s.nextID++
	s.rows = append(s.rows, row)
	if rec.ClientIdempotencyKey != nil {
		s.idempotency[memoryIdempotencyKey{tenantID: rec.TenantID, key: *rec.ClientIdempotencyKey}] = code
	}
	if rec.QuotaExempt {
		s.selfCodes[code] = true
	}
	return row, nil
}

func (s *memoryStore) GetByCode(_ context.Context, rawCode string) (Invitation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	code := NormalizeCode(rawCode)
	for _, row := range s.rows {
		if row.Code == code {
			return row, nil
		}
	}
	return Invitation{}, ErrNotFound
}

func (s *memoryStore) GetByClientIdempotencyKey(_ context.Context, tenantID int64, key string) (Invitation, error) {
	if tenantID <= 0 || key == "" {
		return Invitation{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	code, ok := s.idempotency[memoryIdempotencyKey{tenantID: tenantID, key: key}]
	if !ok {
		return Invitation{}, ErrNotFound
	}
	return s.findByCodeLocked(code)
}

func (s *memoryStore) Preview(_ context.Context, tenantID int64, rawCode string) (InvitationPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	code := NormalizeCode(rawCode)
	for _, row := range s.rows {
		if row.TenantID == tenantID && row.Code == code {
			return InvitationPreview{
				InviterUserID: row.InviterUserID,
				ExpiresAt:     row.ExpiresAt,
				UsageCount:    row.UsageCount,
				MaxUsage:      row.MaxUsage,
			}, nil
		}
	}
	return InvitationPreview{}, ErrNotFound
}

func (s *memoryStore) CountTenantInvitationsSince(_ context.Context, tenantID int64, since time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, row := range s.rows {
		if s.selfCodes[row.Code] {
			continue // 自荐码免配额，从不计入
		}
		if row.TenantID == tenantID && !row.CreatedAt.Before(since) {
			count++
		}
	}
	return count, nil
}

func (s *memoryStore) rowCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}

func (s *memoryStore) findByCodeLocked(code string) (Invitation, error) {
	code = NormalizeCode(code)
	for _, row := range s.rows {
		if row.Code == code {
			return row, nil
		}
	}
	return Invitation{}, ErrNotFound
}

type sequenceGenerator struct {
	mu    sync.Mutex
	next  int
	codes []string
}

func (g *sequenceGenerator) Generate() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.next < len(g.codes) {
		code := g.codes[g.next]
		g.next++
		return code, nil
	}
	g.next++
	return codeForSequence(g.next), nil
}

func codeForSequence(n int) string {
	alphabet := crockfordAlphabet
	out := make([]byte, CodeLength)
	for i := CodeLength - 1; i >= 0; i-- {
		out[i] = alphabet[n&31]
		n >>= 5
	}
	return string(out)
}

// TestSelfReferralCodeExemptFromTenantMonthlyQuota 是报告者的鉴别性守卫：
// 当本月共享的单租户活动配额耗尽后，仅仅获取自己的码也必须仍然成功。
// MUTATION：把 GetOrCreateSelfReferralCode 路由经过 checkTenantMonthlyQuota
//（或丢掉 QuotaExempt 标志）会使其返回 ErrQuotaExceeded → RED。
func TestSelfReferralCodeExemptFromTenantMonthlyQuota(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	service := NewService(store,
		WithNow(func() time.Time { return now }),
		WithCodeGenerator(&sequenceGenerator{}),
	)
	for i := 0; i < MonthlyTenantQuota; i++ {
		if _, err := service.Generate(context.Background(), GenerateInvitationParams{
			TenantID: 7, InviterUserID: int64(1000 + i), MaxUsage: 1, ExpiresInDays: 30,
		}); err != nil {
			t.Fatalf("Generate filler #%d: %v", i, err)
		}
	}
	out, err := service.GetOrCreateSelfReferralCode(context.Background(), 7, 42, time.Time{})
	if err != nil {
		t.Fatalf("self code at exhausted quota: %v want nil (must not 429)", err)
	}
	if out.Code == "" {
		t.Fatal("self code empty")
	}
}

// TestSelfReferralCodeIsStableIdempotent 守护 get-OR-create 契约：
// 重复调用返回同一个码且只创建一行。幂等性依赖于 self 路径持久化稳定的
// self:<userID> 幂等键。
// MUTATION：用 nil（或每次调用都不同）的幂等键去铸造 self 行会移除该稳定
// 标记 → 第二次调用又铸造出第二个码/行 → RED。
//（仅靠 service 层的提前查找并不是本测试守护的对象——store 也会按该键去重；
// 被持久化的那个 KEY 才是保证。）
func TestSelfReferralCodeIsStableIdempotent(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	service := NewService(store,
		WithNow(func() time.Time { return now }),
		WithCodeGenerator(&sequenceGenerator{}),
	)
	first, err := service.GetOrCreateSelfReferralCode(context.Background(), 7, 42, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.GetOrCreateSelfReferralCode(context.Background(), 7, 42, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if again.Code != first.Code {
		t.Fatalf("self code not stable: first=%s again=%s", first.Code, again.Code)
	}
	if got := store.rowCount(); got != 1 {
		t.Fatalf("self code row count=%d want 1 (one stable row)", got)
	}
}

// TestSelfReferralCodeDoesNotStarveCampaignQuota 守护计数排除：
// 自荐码不得消耗活动预算，否则大量用户物化自己的码会让所有人的活动
// POST 都被 429。MUTATION：在 CountTenantInvitationsSince 中把自荐码计入
// → 下面的活动 Generate 被 429 → RED。
func TestSelfReferralCodeDoesNotStarveCampaignQuota(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	service := NewService(store,
		WithNow(func() time.Time { return now }),
		WithCodeGenerator(&sequenceGenerator{}),
	)
	for i := 0; i < MonthlyTenantQuota+50; i++ {
		if _, err := service.GetOrCreateSelfReferralCode(context.Background(), 7, int64(2000+i), time.Time{}); err != nil {
			t.Fatalf("self code #%d: %v", i, err)
		}
	}
	if _, err := service.Generate(context.Background(), GenerateInvitationParams{
		TenantID: 7, InviterUserID: 42, MaxUsage: 1, ExpiresInDays: 30,
	}); err != nil {
		t.Fatalf("campaign Generate after %d self codes: %v want nil (self codes must not consume campaign quota)", MonthlyTenantQuota+50, err)
	}
}

// TestCampaignGenerateRejectsReservedSelfPrefix 封堵绕过配额的漏洞：
// 活动调用方不得能够提供带 self 前缀的幂等键，否则那一行会逃过活动计数器。
// MUTATION：丢掉 validateGenerateParams 中的保留前缀检查 → err 为 nil → RED。
func TestCampaignGenerateRejectsReservedSelfPrefix(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	service := NewService(store,
		WithNow(func() time.Time { return now }),
		WithCodeGenerator(&sequenceGenerator{}),
	)
	forged := SelfReferralIdempotencyPrefix + "999"
	_, err := service.Generate(context.Background(), GenerateInvitationParams{
		TenantID: 7, InviterUserID: 42, MaxUsage: 1, ExpiresInDays: 30, ClientIdempotencyKey: &forged,
	})
	if !errors.Is(err, ErrReservedIdempotencyKey) {
		t.Fatalf("err=%v want ErrReservedIdempotencyKey (forged self marker must be rejected)", err)
	}
}
