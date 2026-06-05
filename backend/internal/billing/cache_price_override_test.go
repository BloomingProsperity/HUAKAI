package billing

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func newTestCacheOverrideStore(t *testing.T) *CacheOverrideStore {
	t.Helper()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("sign.GenerateKey: %v", err)
	}
	fixed := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	return NewCacheOverrideStore(signer, func() time.Time { return fixed })
}

// TestResolveMultiplier_NoOverrideIsOfficialPrice 锁定默认行为零变化:
// 不设任何覆盖时倍率必须是 1.0(官方价)。
func TestResolveMultiplier_NoOverrideIsOfficialPrice(t *testing.T) {
	s := newTestCacheOverrideStore(t)
	got := s.ResolveMultiplier(7, "gpt-4o")
	if !got.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("no-override multiplier=%s want 1", got)
	}
}

// TestResolveMultiplier_GlobalApplies global=1.5 时无更高优先级覆盖应返回 1.5。
func TestResolveMultiplier_GlobalApplies(t *testing.T) {
	s := newTestCacheOverrideStore(t)
	if _, err := s.Set("admin:1", CacheOverrideKey{Scope: CacheOverrideScopeGlobal}, decimal.RequireFromString("1.5")); err != nil {
		t.Fatalf("Set global: %v", err)
	}
	got := s.ResolveMultiplier(7, "gpt-4o")
	if !got.Equal(decimal.RequireFromString("1.5")) {
		t.Fatalf("global multiplier=%s want 1.5", got)
	}
}

// TestResolveMultiplier_ModelOverridesGlobal model 覆盖优先于 global。
func TestResolveMultiplier_ModelOverridesGlobal(t *testing.T) {
	s := newTestCacheOverrideStore(t)
	if _, err := s.Set("admin:1", CacheOverrideKey{Scope: CacheOverrideScopeGlobal}, decimal.RequireFromString("1.5")); err != nil {
		t.Fatalf("Set global: %v", err)
	}
	if _, err := s.Set("admin:1", CacheOverrideKey{Scope: CacheOverrideScopeModel, Model: "gpt-4o"}, decimal.RequireFromString("2")); err != nil {
		t.Fatalf("Set model: %v", err)
	}
	got := s.ResolveMultiplier(7, "gpt-4o")
	if !got.Equal(decimal.RequireFromString("2")) {
		t.Fatalf("model multiplier=%s want 2 (model must override global)", got)
	}
	// 不同模型仍落回 global。
	other := s.ResolveMultiplier(7, "claude-3")
	if !other.Equal(decimal.RequireFromString("1.5")) {
		t.Fatalf("non-matching model multiplier=%s want global 1.5", other)
	}
}

// TestResolveMultiplier_TenantOverridesModelAndGlobal tenant 优先于 model 与 global。
// 这是判别性核心:若优先级写反(global/model 盖 tenant),本测试变红。
func TestResolveMultiplier_TenantOverridesModelAndGlobal(t *testing.T) {
	s := newTestCacheOverrideStore(t)
	if _, err := s.Set("admin:1", CacheOverrideKey{Scope: CacheOverrideScopeGlobal}, decimal.RequireFromString("1.5")); err != nil {
		t.Fatalf("Set global: %v", err)
	}
	if _, err := s.Set("admin:1", CacheOverrideKey{Scope: CacheOverrideScopeModel, Model: "gpt-4o"}, decimal.RequireFromString("2")); err != nil {
		t.Fatalf("Set model: %v", err)
	}
	if _, err := s.Set("admin:1", CacheOverrideKey{Scope: CacheOverrideScopeTenant, TenantID: 7}, decimal.RequireFromString("3")); err != nil {
		t.Fatalf("Set tenant: %v", err)
	}
	got := s.ResolveMultiplier(7, "gpt-4o")
	if !got.Equal(decimal.RequireFromString("3")) {
		t.Fatalf("tenant multiplier=%s want 3 (tenant must override model+global)", got)
	}
	// 另一个租户不受影响,落回 model。
	other := s.ResolveMultiplier(8, "gpt-4o")
	if !other.Equal(decimal.RequireFromString("2")) {
		t.Fatalf("other-tenant multiplier=%s want model 2", other)
	}
}

// TestDelete_RevertsToLowerScope 删除 tenant 覆盖后落回 model;删除 model 后落回 global。
func TestDelete_RevertsToLowerScope(t *testing.T) {
	s := newTestCacheOverrideStore(t)
	mustSet(t, s, CacheOverrideKey{Scope: CacheOverrideScopeGlobal}, "1.5")
	mustSet(t, s, CacheOverrideKey{Scope: CacheOverrideScopeModel, Model: "gpt-4o"}, "2")
	mustSet(t, s, CacheOverrideKey{Scope: CacheOverrideScopeTenant, TenantID: 7}, "3")

	if err := s.Delete("admin:1", CacheOverrideKey{Scope: CacheOverrideScopeTenant, TenantID: 7}); err != nil {
		t.Fatalf("Delete tenant: %v", err)
	}
	if got := s.ResolveMultiplier(7, "gpt-4o"); !got.Equal(decimal.RequireFromString("2")) {
		t.Fatalf("after tenant delete multiplier=%s want model 2", got)
	}
	if err := s.Delete("admin:1", CacheOverrideKey{Scope: CacheOverrideScopeModel, Model: "gpt-4o"}); err != nil {
		t.Fatalf("Delete model: %v", err)
	}
	if got := s.ResolveMultiplier(7, "gpt-4o"); !got.Equal(decimal.RequireFromString("1.5")) {
		t.Fatalf("after model delete multiplier=%s want global 1.5", got)
	}
	if err := s.Delete("admin:1", CacheOverrideKey{Scope: CacheOverrideScopeGlobal}); err != nil {
		t.Fatalf("Delete global: %v", err)
	}
	if got := s.ResolveMultiplier(7, "gpt-4o"); !got.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("after all deletes multiplier=%s want official 1", got)
	}
}

// TestVerifyChain_HappyPathAndTamperDetected 审计 hash-chain 在多次变更后 VerifyChain 通过,
// 并在篡改某条 entry 的倍率后检出。
func TestVerifyChain_HappyPathAndTamperDetected(t *testing.T) {
	s := newTestCacheOverrideStore(t)
	mustSet(t, s, CacheOverrideKey{Scope: CacheOverrideScopeGlobal}, "1.5")
	mustSet(t, s, CacheOverrideKey{Scope: CacheOverrideScopeModel, Model: "gpt-4o"}, "2")
	if err := s.Delete("admin:1", CacheOverrideKey{Scope: CacheOverrideScopeModel, Model: "gpt-4o"}); err != nil {
		t.Fatalf("Delete model: %v", err)
	}
	if ok, reason := s.VerifyChain(); !ok {
		t.Fatalf("VerifyChain ok=false reason=%q want pass", reason)
	}
	if got := len(s.AuditChain()); got != 3 {
		t.Fatalf("audit chain len=%d want 3", got)
	}

	// 篡改链中一条 entry 的 new_ratio,重算应失配。
	s.mu.Lock()
	tampered := *cloneStr(s.chain[0].NewRatio)
	_ = tampered
	newRatio := "9.99"
	s.chain[0].NewRatio = &newRatio
	s.mu.Unlock()
	if ok, _ := s.VerifyChain(); ok {
		t.Fatal("VerifyChain ok=true after tamper want detection")
	}
}

// TestSet_RejectsNonPositiveMultiplier money 路径保守:非正倍率必须被拒。
func TestSet_RejectsNonPositiveMultiplier(t *testing.T) {
	s := newTestCacheOverrideStore(t)
	for _, bad := range []string{"0", "-1", "-0.5"} {
		if _, err := s.Set("admin:1", CacheOverrideKey{Scope: CacheOverrideScopeGlobal}, decimal.RequireFromString(bad)); err == nil {
			t.Fatalf("Set multiplier=%s err=nil want rejected", bad)
		}
	}
	// 被拒后仍是官方价。
	if got := s.ResolveMultiplier(7, "gpt-4o"); !got.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("after rejected set multiplier=%s want official 1", got)
	}
}

func mustSet(t *testing.T, s *CacheOverrideStore, key CacheOverrideKey, mult string) {
	t.Helper()
	if _, err := s.Set("admin:1", key, decimal.RequireFromString(mult)); err != nil {
		t.Fatalf("Set %v=%s: %v", key, mult, err)
	}
}
