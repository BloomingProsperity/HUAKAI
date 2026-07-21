package credentialstore

import (
	"context"
	"testing"
	"time"
)

// TestEffectiveRefreshLead_NullPreservesGlobal 证明 nil 的 per-account 覆盖值
// 产出的时长与全局 RefreshWindow 完全一致(DEFAULT SAFETY 要求)。
func TestEffectiveRefreshLead_NullPreservesGlobal(t *testing.T) {
	got := effectiveRefreshLead(nil, RefreshWindow)
	if got != RefreshWindow {
		t.Fatalf("effectiveRefreshLead(nil, RefreshWindow) = %v, want %v", got, RefreshWindow)
	}
}

// TestEffectiveRefreshLead_ZeroPreservesGlobal 证明零值(非 nil 但 <= 0)
// 同样回退到全局 window。
func TestEffectiveRefreshLead_ZeroPreservesGlobal(t *testing.T) {
	zero := int32(0)
	got := effectiveRefreshLead(&zero, RefreshWindow)
	if got != RefreshWindow {
		t.Fatalf("effectiveRefreshLead(&0, RefreshWindow) = %v, want %v", got, RefreshWindow)
	}
}

// TestEffectiveRefreshLead_PerAccountOverride 证明正的 per-account 值
// 会覆盖全局 window。
func TestEffectiveRefreshLead_PerAccountOverride(t *testing.T) {
	n := int32(300) // 5 分钟,以秒计
	got := effectiveRefreshLead(&n, RefreshWindow)
	want := 300 * time.Second
	if got != want {
		t.Fatalf("effectiveRefreshLead(&300, RefreshWindow) = %v, want %v", got, want)
	}
}

// TestEffectiveRefreshLead_NullRefreshBeforeAt 证明 NULL ->
// refresh_before_at == accessExpiresAt - RefreshWindow(与当前行为一致)。
func TestEffectiveRefreshLead_NullRefreshBeforeAt(t *testing.T) {
	expiry := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	lead := effectiveRefreshLead(nil, RefreshWindow)
	got := expiry.Add(-lead)
	want := expiry.Add(-RefreshWindow)
	if !got.Equal(want) {
		t.Fatalf("NULL lead: refreshBeforeAt = %v, want %v", got, want)
	}
}

// TestEffectiveRefreshLead_PerAccountRefreshBeforeAt 证明 per-account 的
// N 秒 -> refresh_before_at = expiry - N*秒。
func TestEffectiveRefreshLead_PerAccountRefreshBeforeAt(t *testing.T) {
	expiry := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	n := int32(600) // 10 分钟
	lead := effectiveRefreshLead(&n, RefreshWindow)
	got := expiry.Add(-lead)
	want := expiry.Add(-600 * time.Second)
	if !got.Equal(want) {
		t.Fatalf("per-account 600s lead: refreshBeforeAt = %v, want %v", got, want)
	}
	// 同时验证它与全局默认值不同
	globalResult := expiry.Add(-RefreshWindow)
	if got.Equal(globalResult) {
		t.Fatalf("per-account result should differ from global result when N != RefreshWindow")
	}
}

func TestPrepareEnvelopeLeavesUnknownExpiryUsableCredentialUnscheduled(t *testing.T) {
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	store := NewStore(nil, mustTestKeyProvider(t), DefaultHandlerRegistry())
	store.now = func() time.Time { return now }

	for _, tc := range []struct {
		name     string
		vendor   string
		authMode string
		payload  string
	}{
		{name: "Codex 导入访问令牌", vendor: VendorOpenAI, authMode: AuthModeCodexCLIOAuth, payload: `{"access_token":"codex-access","refresh_token":"codex-refresh"}`},
		{name: "Antigravity 导入会话令牌", vendor: VendorAntigravity, authMode: AuthModeOAuth, payload: `{"session_token":"ag-access","refresh_token":"ag-refresh"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, err := store.registry.MustLookup(tc.vendor, tc.authMode)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := store.prepareEnvelope(context.Background(), 1, 2, tc.vendor, tc.authMode, 1, []byte(tc.payload), handler)
			if err != nil {
				t.Fatalf("prepareEnvelope 失败：%v", err)
			}
			if !prepared.refreshBeforeAt.IsZero() {
				t.Fatalf("未知到期但可调用的凭证被安排立即刷新：%s", prepared.refreshBeforeAt)
			}
		})
	}
}

func TestPrepareEnvelopeSchedulesUnusableBootstrapCredentialImmediately(t *testing.T) {
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	store := NewStore(nil, mustTestKeyProvider(t), DefaultHandlerRegistry())
	store.now = func() time.Time { return now }
	handler, err := store.registry.MustLookup(VendorGemini, AuthModeVertexSA)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.prepareEnvelope(context.Background(), 1, 2, VendorGemini, AuthModeVertexSA, 1,
		[]byte(`{"client_email":"svc@example.test","private_key":"private-key","project_id":"project"}`), handler)
	if err != nil {
		t.Fatalf("prepareEnvelope 失败：%v", err)
	}
	if !prepared.refreshBeforeAt.Equal(now) {
		t.Fatalf("尚不可调用的引导凭证刷新时间=%s，期望 %s", prepared.refreshBeforeAt, now)
	}
}
