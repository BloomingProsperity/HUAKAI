// Package pool tests F-POOL-001 implementation against the contract
// in docs/specs/pool-routing.md.
package pool

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// =====================================================================
// Sub2API-inheritable scenarios
// =====================================================================

// AT-POOL-001: Layer 1 routing-config hit.
// Routing config maps requested_model → [101, 102]; HealthGate marks 102 unhealthy.
// Selector MUST return 101 (in routing list AND healthy), NOT 102 (in list but unhealthy)
// nor 999 (healthier but outside routing list).
func TestAT_POOL_001_RoutingConfigHit(t *testing.T) {
	now := time.Now()
	src := &stubAccountSource{accounts: []*AccountSnapshot{
		snap(101, 1, 100, 0.1, now.Add(-1*time.Hour)),
		snap(102, 1, 100, 0.5, now.Add(-30*time.Minute)), // unhealthy via HealthGate
		snap(999, 1, 50, 0.05, now.Add(-2*time.Hour)),    // healthier but NOT in routing list
	}}
	policy := &stubPolicy{p: &RoutingPolicy{
		ModelAccountIDs: map[string][]int64{"claude-3-5-sonnet": {101, 102}},
		TopKDefault:     1,
	}}
	claims := &captureClaimGate{}
	slots := newMemSlotManager()
	gates := DefaultGateChain()
	gates.Health = unhealthyAccountsGate{102: {}}

	sel := NewDefaultSelector(src,
		WithRoutingPolicySource(policy),
		WithSlotManager(slots),
		WithClaimGate(claims),
		WithGateChain(gates),
	)
	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 50, RequestedModel: "claude-3-5-sonnet",
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID != 101 {
		t.Fatalf("expected only-healthy routing-list winner 101; got %d", res.AccountID)
	}
}

// AT-POOL-011: routing reason JSON schema-conformant on every selection result.
func TestAT_POOL_011_RoutingReasonSchema(t *testing.T) {
	now := time.Now()
	src := &stubAccountSource{accounts: []*AccountSnapshot{
		snap(40, 1, 100, 0.1, now.Add(-1*time.Hour)),
		snap(41, 1, 100, 0.2, now.Add(-30*time.Minute)),
	}}
	policy := &stubPolicy{p: &RoutingPolicy{TopKDefault: 1}}
	gates := DefaultGateChain()
	gates.Health = unhealthyAccountsGate{41: {}}

	sel := NewDefaultSelector(src,
		WithRoutingPolicySource(policy),
		WithSlotManager(newMemSlotManager()),
		WithClaimGate(&captureClaimGate{}),
		WithGateChain(gates),
	)
	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 70, RequestedModel: "x",
		ExcludedAccounts: nil,
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(res.RoutingReasonJSON) == 0 {
		t.Fatalf("RoutingReasonJSON must always be populated; got empty")
	}
	var got map[string]any
	if err := json.Unmarshal(res.RoutingReasonJSON, &got); err != nil {
		t.Fatalf("RoutingReasonJSON not valid JSON: %v", err)
	}
	for _, key := range []string{
		"selection_layer", "affinity_key_class", "capability_outcome",
		"candidate_counts_by_exclusion", "pooling_group_id",
		"scoring_policy_version", "signal_contributions",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("RoutingReasonJSON missing required key %q; got %+v", key, got)
		}
	}
	if got["selection_layer"] != "fresh" {
		t.Errorf("selection_layer should be 'fresh' for Layer-2 win; got %v", got["selection_layer"])
	}
	exclSummary, _ := got["candidate_counts_by_exclusion"].(map[string]any)
	if v, ok := exclSummary["health"]; !ok || v.(float64) < 1 {
		t.Errorf("expected at least 1 health-gate exclusion (account 41); got %+v", exclSummary)
	}
}

// unhealthyAccountsGate rejects accounts in its set; used to model HealthGate in tests.
type unhealthyAccountsGate map[int64]struct{}

func (u unhealthyAccountsGate) Allow(_ context.Context, account *AccountSnapshot, _ SelectionRequest) (bool, GateFailureReason, error) {
	if _, bad := u[account.ID]; bad {
		return false, GateFailureHealth, nil
	}
	return true, "", nil
}

// AT-POOL-003: sticky-standalone hit.
func TestAT_POOL_003_StickyStandaloneHit(t *testing.T) {
	now := time.Now()
	src := &stubAccountSource{accounts: []*AccountSnapshot{
		snap(7, 1, 100, 0.5, now.Add(-1*time.Hour)),
		snap(8, 1, 50, 0.1, now.Add(-2*time.Hour)), // would win Layer 2 lex-sort
	}}
	policy := &stubPolicy{p: &RoutingPolicy{TopKDefault: 1}}
	sticky := &stubSticky{bindings: map[string]int64{"sess-abc": 7}}

	sel := NewDefaultSelector(src,
		WithRoutingPolicySource(policy),
		WithStickyStore(sticky),
		WithSlotManager(newMemSlotManager()),
		WithClaimGate(&captureClaimGate{}),
	)
	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 51, SessionHash: "sess-abc", RequestedModel: "any",
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID != 7 {
		t.Fatalf("expected sticky binding to account 7; got %d", res.AccountID)
	}
}

// AT-POOL-004: Layer 2 fresh tier-by-tier filter.
// Lower priority value wins; among same priority, lower load_rate; among same priority+load, older last_used wins.
func TestAT_POOL_004_Layer2TierFilter(t *testing.T) {
	now := time.Now()
	src := &stubAccountSource{accounts: []*AccountSnapshot{
		snap(1, 1, 200, 0.10, now.Add(-1*time.Hour)),
		snap(2, 1, 100, 0.50, now.Add(-2*time.Hour)),
		snap(3, 1, 100, 0.10, now.Add(-3*time.Hour)), // expected winner: lowest-tier priority=100 + lowest load=0.10 + oldest last_used
		snap(4, 1, 100, 0.10, now.Add(-30*time.Minute)),
		snap(5, 1, 300, 0.05, now.Add(-1*time.Hour)), // lowest load BUT highest priority value loses tier 1
	}}
	policy := &stubPolicy{p: &RoutingPolicy{TopKDefault: 1}}

	sel := NewDefaultSelector(src,
		WithRoutingPolicySource(policy),
		WithSlotManager(newMemSlotManager()),
		WithClaimGate(&captureClaimGate{}),
	)
	res, err := sel.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 52, RequestedModel: "x"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID != 3 {
		t.Fatalf("expected lex-sort winner Account 3; got %d", res.AccountID)
	}
}

// AT-POOL-006: per-request exclusion list honored.
func TestAT_POOL_006_PerRequestExclusion(t *testing.T) {
	now := time.Now()
	src := &stubAccountSource{accounts: []*AccountSnapshot{
		snap(11, 1, 100, 0.1, now.Add(-1*time.Hour)),
		snap(12, 1, 100, 0.5, now.Add(-30*time.Minute)),
	}}
	policy := &stubPolicy{p: &RoutingPolicy{TopKDefault: 1}}
	sel := NewDefaultSelector(src,
		WithRoutingPolicySource(policy),
		WithSlotManager(newMemSlotManager()),
		WithClaimGate(&captureClaimGate{}),
	)
	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 53, RequestedModel: "x",
		ExcludedAccounts: map[int64]struct{}{11: {}},
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID != 12 {
		t.Fatalf("excluded 11 yet selector returned %d (want 12)", res.AccountID)
	}
}

// =====================================================================
// HUAKAI-design scenarios
// =====================================================================

// AT-POOL-008: Pattern B placeholder writeback — selector calls ClaimGate
// with provider_account_id + acquisition_token after acquire.
func TestAT_POOL_008_PatternBWriteback(t *testing.T) {
	now := time.Now()
	src := &stubAccountSource{accounts: []*AccountSnapshot{snap(7, 1, 100, 0.1, now.Add(-1*time.Hour))}}
	policy := &stubPolicy{p: &RoutingPolicy{TopKDefault: 1}}
	claims := &captureClaimGate{}
	slots := newMemSlotManager()

	sel := NewDefaultSelector(src,
		WithRoutingPolicySource(policy),
		WithSlotManager(slots),
		WithClaimGate(claims),
	)
	res, err := sel.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 54, RequestedModel: "x"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	claims.mu.Lock()
	defer claims.mu.Unlock()
	if len(claims.calls) != 1 {
		t.Fatalf("expected exactly 1 ClaimGate.WriteAcquisition call; got %d", len(claims.calls))
	}
	c := claims.calls[0]
	if c.ClaimID != 54 || c.AccountID != res.AccountID {
		t.Fatalf("ClaimGate writeback mismatch: claim=%d account=%d res=%d", c.ClaimID, c.AccountID, res.AccountID)
	}
	if c.Token != res.AcquisitionToken {
		t.Fatalf("acquisition_token mismatch in writeback: claim=%v res=%v", c.Token, res.AcquisitionToken)
	}
}

// AT-POOL-009: acquisition-token idempotent release — release twice only decrements once.
func TestAT_POOL_009_AcquisitionTokenIdempotent(t *testing.T) {
	now := time.Now()
	src := &stubAccountSource{accounts: []*AccountSnapshot{snap(20, 1, 100, 0.1, now.Add(-1*time.Hour))}}
	policy := &stubPolicy{p: &RoutingPolicy{TopKDefault: 1}}
	slots := newMemSlotManager()

	sel := NewDefaultSelector(src,
		WithRoutingPolicySource(policy),
		WithSlotManager(slots),
		WithClaimGate(&captureClaimGate{}),
	)
	res, err := sel.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 55, RequestedModel: "x"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AcquisitionToken == ([16]byte{}) {
		t.Fatalf("Selector must surface AcquisitionToken in result for idempotent release contract")
	}
	// Look up the release fn the slot manager handed out.
	release := slots.releaseFor(res.AcquisitionToken)
	if release == nil {
		t.Fatalf("memSlotManager has no release fn for token %v", res.AcquisitionToken)
	}
	if err := release(context.Background()); err != nil {
		t.Fatalf("release #1: %v", err)
	}
	if err := release(context.Background()); err != nil {
		t.Fatalf("release #2: %v", err)
	}
	if got := slots.releaseCount(res.AcquisitionToken); got != 1 {
		t.Fatalf("idempotent release violated: token released %d times (must be exactly 1)", got)
	}
}

// AT-POOL-010: tenant isolation — Tenant 1's selection NEVER returns Tenant 2's accounts.
func TestAT_POOL_010_TenantIsolation(t *testing.T) {
	now := time.Now()
	src := &stubAccountSource{accounts: []*AccountSnapshot{
		snap(1, 1, 100, 0.5, now.Add(-1*time.Hour)),
		snap(2, 2, 50, 0.05, now.Add(-2*time.Hour)), // Tenant 2 higher priority — should NOT be picked for Tenant 1
	}}
	policy := &stubPolicy{p: &RoutingPolicy{TopKDefault: 1}}

	sel := NewDefaultSelector(src,
		WithRoutingPolicySource(policy),
		WithSlotManager(newMemSlotManager()),
		WithClaimGate(&captureClaimGate{}),
	)
	res, err := sel.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 56, RequestedModel: "x"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID != 1 {
		t.Fatalf("tenant 1 must select its OWN account 1; got %d (cross-tenant leak risk)", res.AccountID)
	}
}

// AT-POOL-013: Default Top-K compatibility (K=1 unless tie group).
// 5 accounts with distinct (priority, load, last_used); selector returns the
// unique top candidate, NOT a random pick from a wider band.
func TestAT_POOL_013_DefaultTopKCompatibility(t *testing.T) {
	now := time.Now()
	src := &stubAccountSource{accounts: []*AccountSnapshot{
		snap(1, 1, 200, 0.10, now.Add(-1*time.Hour)),
		snap(2, 1, 100, 0.30, now.Add(-2*time.Hour)),
		snap(3, 1, 100, 0.10, now.Add(-3*time.Hour)), // unique top by lex-sort
		snap(4, 1, 100, 0.20, now.Add(-30*time.Minute)),
		snap(5, 1, 300, 0.99, now.Add(-1*time.Hour)),
	}}
	policy := &stubPolicy{p: &RoutingPolicy{TopKDefault: 1, BroadTopK: false}}

	sel := NewDefaultSelector(src,
		WithRoutingPolicySource(policy),
		WithSlotManager(newMemSlotManager()),
		WithClaimGate(&captureClaimGate{}),
	)
	// Run 50 trials to confirm deterministic pick (compatibility mode).
	for i := 0; i < 50; i++ {
		res, err := sel.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: int64(60 + i), RequestedModel: "x"})
		if err != nil {
			t.Fatalf("trial %d: Select: %v", i, err)
		}
		if res.AccountID != 3 {
			t.Fatalf("trial %d: K=1 compatibility mode should always pick Account 3; got %d", i, res.AccountID)
		}
	}
}

// AT-POOL-019: Cross-feature with F-OBS-001; deferred.
func TestAT_POOL_019_Tx2Atomicity(t *testing.T) {
	t.Skip("Cross-feature with F-OBS-001 settler; awaits slice 5 implementation.")
}

// =====================================================================
// Smoke
// =====================================================================

func TestPackageCompiles(t *testing.T) {
	if time.Now().Year() < 2026 {
		t.Fatalf("clock skew")
	}
}

// TestVendorFromProtocolFamily 锁定 4-vendor 真实账号 metric 切片映射
// (memory: project_real_vendor_account_scope)。 表驱动覆盖 gateway/
// protocol_selector.go 当前注册的全部 ProtocolFamily, 防止再出现 prefix
// 误判 (历史 bug: openai_codex 被 prefix "openai" 抢走, 导致 codex 切片
// 永远 0、 openai 切片双重计数)。
func TestVendorFromProtocolFamily(t *testing.T) {
	cases := []struct {
		pf   string
		want string
	}{
		// 4-vendor 真实账号集合 — 必须正确分流
		{"anthropic_messages", "anthropic"},
		{"openai_chat", "openai"},
		{"openai_responses", "openai"},
		{"openai_codex", "codex"}, // 不能落到 openai (反转 ChatGPT Plus / Codex CLI session)
		{"gemini_messages", "gemini"},
		{"gemini_advanced_session", "gemini"},

		// 已注册但不在 4-vendor 真账号集合 — 静默返空
		{"bedrock_invoke", ""},
		{"openrouter_chat", ""},
		{"grok_chat", ""},
		{"deepseek_chat", ""},
		{"mistral_chat", ""},
		{"groqcloud_chat", ""},
		{"together_chat", ""},
		{"perplexity_chat", ""},
		{"fireworks_chat", ""},
		{"copilot_session", ""},
		{"cursor_session", ""},
		{"antigravity_session", ""},
		{"kiro_session", ""},
		{"windsurf_session", ""},

		// 边界 — 空字符串 / 未注册字面量 / 大小写敏感 / 裸 vendor 名
		{"", ""},
		{"unknown_family", ""},
		{"OPENAI_CHAT", ""}, // exact-match case-sensitive
		{"openai", ""},      // 裸 vendor 名 (无 family suffix) 不许通过
		{"codex", ""},
		{"anthropic", ""},
	}
	for _, tc := range cases {
		name := tc.pf
		if name == "" {
			name = "(empty)"
		}
		t.Run(name, func(t *testing.T) {
			if got := VendorFromProtocolFamily(tc.pf); got != tc.want {
				t.Fatalf("VendorFromProtocolFamily(%q) = %q, want %q", tc.pf, got, tc.want)
			}
		})
	}
}
