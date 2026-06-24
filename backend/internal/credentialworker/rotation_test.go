package credentialworker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

type fakeRotationStore struct {
	due          []RotationCandidate
	dueErr       error
	gotOlderThan time.Time
	gotLimit     int
	// recovered 记录被 MarkForRefreshRecovery(可刷新→自愈)挑中的候选;
	// flagged 记录被 FlagNeedsRotation(强制下线)挑中的候选。
	recovered       []RotationCandidate
	recoveredBefore []time.Time
	flagged         []RotationCandidate
	// recoverErrOn / flagErrOn:1-based 第 N 次调用返回错误,0=从不。
	recoverErrOn int
	flagErrOn    int
}

func (f *fakeRotationStore) DueForRotation(_ context.Context, olderThan time.Time, limit int) ([]RotationCandidate, error) {
	f.gotOlderThan = olderThan
	f.gotLimit = limit
	return f.due, f.dueErr
}

func (f *fakeRotationStore) MarkForRefreshRecovery(_ context.Context, c RotationCandidate, refreshBeforeAt time.Time) error {
	if f.recoverErrOn != 0 && len(f.recovered)+1 == f.recoverErrOn {
		return errors.New("recover failed")
	}
	f.recovered = append(f.recovered, c)
	f.recoveredBefore = append(f.recoveredBefore, refreshBeforeAt)
	return nil
}

func (f *fakeRotationStore) FlagNeedsRotation(_ context.Context, c RotationCandidate) error {
	if f.flagErrOn != 0 && len(f.flagged)+1 == f.flagErrOn {
		return errors.New("flag failed")
	}
	f.flagged = append(f.flagged, c)
	return nil
}

var rotNow = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

// oauthCand / staticCand 用真实 (vendor, auth_mode) 让 DefaultRefreshClassifier
// 能判别:anthropic/claude_code 是 OAuth 可刷新,anthropic/api_key 是静态密钥。
func oauthCand(credID int64) RotationCandidate {
	return RotationCandidate{TenantID: 1, ProviderAccountID: credID, CredentialID: credID,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeCode}
}

func staticCand(credID int64) RotationCandidate {
	return RotationCandidate{TenantID: 1, ProviderAccountID: credID, CredentialID: credID,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeAPIKey}
}

// Disabled (maxAge<=0) must not touch the store at all — opt-in, default off.
// Mutation guard: if the maxAge<=0 short-circuit is removed, DueForRotation gets
// called and gotLimit becomes non-zero → red.
func TestScanRotationDue_DisabledByDefault(t *testing.T) {
	f := &fakeRotationStore{due: []RotationCandidate{oauthCand(1)}}
	for _, maxAge := range []time.Duration{0, -time.Hour} {
		n, err := ScanRotationDue(context.Background(), f, nil, nil, maxAge, rotNow, 50)
		if err != nil || n != 0 {
			t.Fatalf("maxAge=%v must be a no-op, got n=%d err=%v", maxAge, n, err)
		}
	}
	if f.gotLimit != 0 || len(f.recovered) != 0 || len(f.flagged) != 0 {
		t.Fatalf("disabled scan must not query/recover/flag, got limit=%d recovered=%d flagged=%d",
			f.gotLimit, len(f.recovered), len(f.flagged))
	}
}

// 核心判别测试(恢复闭环):一个超期的【可刷新】凭据必须被 MarkForRefreshRecovery
// 挑中(并带 refreshBeforeAt=now),从而被既有刷新流挑走→SaveRefreshSuccess→回 active。
// 它绝不能被 FlagNeedsRotation 下线。cutoff 必须是 now-maxAge。
//
// Mutation 注入(抓什么缺陷):把 ScanRotationDue 里 classifier(...) 分支删掉/恒为
// false(=退回"只置标不恢复":可刷新凭据也不进恢复路径)→ recovered 为空 → red。
func TestScanRotationDue_RefreshableRecovered(t *testing.T) {
	f := &fakeRotationStore{due: []RotationCandidate{oauthCand(10), oauthCand(11)}}
	var alerted []int64
	alert := func(_ context.Context, c RotationCandidate) { alerted = append(alerted, c.ProviderAccountID) }

	n, err := ScanRotationDue(context.Background(), f, DefaultRefreshClassifier(), alert, 90*24*time.Hour, rotNow, 50)
	if err != nil || n != 2 {
		t.Fatalf("two due refreshable credentials must process 2, got n=%d err=%v", n, err)
	}
	if len(f.recovered) != 2 {
		t.Fatalf("both refreshable candidates must be routed to refresh recovery, got %d", len(f.recovered))
	}
	if len(f.flagged) != 0 {
		t.Fatalf("refreshable credentials must NOT be flagged offline (needs_rotation), got %d flagged", len(f.flagged))
	}
	for _, ts := range f.recoveredBefore {
		if !ts.Equal(rotNow) {
			t.Fatalf("recovery must pull refresh_before_at to now=%v, got %v", rotNow, ts)
		}
	}
	if len(alerted) != 2 {
		t.Fatalf("each processed candidate must alert, got %v", alerted)
	}
	if want := rotNow.Add(-90 * 24 * time.Hour); !f.gotOlderThan.Equal(want) {
		t.Fatalf("cutoff must be now-maxAge=%v, got %v", want, f.gotOlderThan)
	}
}

// 判别测试(防假装刷新):一个超期的【静态/不可刷】API key 绝不能被 MarkForRefreshRecovery
// 当成可刷新去"刷成 active",也不能被自动 FlagNeedsRotation 下线(避免无恢复路径的 brownout)
// —— 只告警,留在 active 在线。
//
// Mutation 注入:把 classifier 分支改成"对所有候选都 MarkForRefreshRecovery"(无视
// 可刷新性)→ 静态 key 进了 recovered → red(证明分类真在区分静态 vs 可刷新)。
func TestScanRotationDue_StaticKeyAlertOnlyNotRecovered(t *testing.T) {
	f := &fakeRotationStore{due: []RotationCandidate{staticCand(20)}}
	var alerted []int64
	alert := func(_ context.Context, c RotationCandidate) { alerted = append(alerted, c.ProviderAccountID) }

	n, err := ScanRotationDue(context.Background(), f, DefaultRefreshClassifier(), alert, time.Hour, rotNow, 50)
	if err != nil || n != 1 {
		t.Fatalf("one due static credential must still be processed/alerted, got n=%d err=%v", n, err)
	}
	if len(f.recovered) != 0 {
		t.Fatalf("static API key must NOT be refresh-recovered (no fake refresh), got %d", len(f.recovered))
	}
	if len(f.flagged) != 0 {
		t.Fatalf("static API key must NOT be auto-flagged offline on age alone (brownout), got %d", len(f.flagged))
	}
	if len(alerted) != 1 || alerted[0] != 20 {
		t.Fatalf("static key must be alerted for operator handling, got %v", alerted)
	}
}

// 混合批:可刷新走恢复、静态走告警,二者在同一次扫描里被正确分流。
func TestScanRotationDue_MixedBatchSplitsByRefreshability(t *testing.T) {
	f := &fakeRotationStore{due: []RotationCandidate{oauthCand(30), staticCand(31), oauthCand(32)}}
	n, err := ScanRotationDue(context.Background(), f, DefaultRefreshClassifier(), nil, time.Hour, rotNow, 50)
	if err != nil || n != 3 {
		t.Fatalf("mixed batch must process all 3, got n=%d err=%v", n, err)
	}
	if len(f.recovered) != 2 {
		t.Fatalf("only the 2 OAuth credentials must be recovered, got %d", len(f.recovered))
	}
	for _, c := range f.recovered {
		if c.CredentialID == 31 {
			t.Fatalf("static key 31 must never be in the recovery set")
		}
	}
}

// A recovery error stops the scan and surfaces — a transient DB fault is not
// swallowed, and the count reflects only what was actually processed.
func TestScanRotationDue_StopsOnRecoverError(t *testing.T) {
	f := &fakeRotationStore{
		due:          []RotationCandidate{oauthCand(1), oauthCand(2), oauthCand(3)},
		recoverErrOn: 2,
	}
	n, err := ScanRotationDue(context.Background(), f, DefaultRefreshClassifier(), nil, time.Hour, rotNow, 50)
	if err == nil {
		t.Fatal("recovery error must surface, got nil")
	}
	if n != 1 {
		t.Fatalf("only the first candidate processed before the error, want n=1 got %d", n)
	}
}

// A DueForRotation error surfaces without processing anything.
func TestScanRotationDue_QueryErrorSurfaces(t *testing.T) {
	f := &fakeRotationStore{dueErr: errors.New("db down")}
	n, err := ScanRotationDue(context.Background(), f, nil, nil, time.Hour, rotNow, 50)
	if err == nil || n != 0 || len(f.recovered) != 0 || len(f.flagged) != 0 {
		t.Fatalf("query error must surface with nothing processed, got n=%d err=%v", n, err)
	}
}

// nil store is a safe no-op (never panics).
func TestScanRotationDue_NilStore(t *testing.T) {
	if n, err := ScanRotationDue(context.Background(), nil, nil, nil, time.Hour, rotNow, 50); err != nil || n != 0 {
		t.Fatalf("nil store must be a no-op, got n=%d err=%v", n, err)
	}
}

// A non-positive limit is clamped to a safe default rather than fetching 0 rows.
func TestScanRotationDue_DefaultLimit(t *testing.T) {
	f := &fakeRotationStore{}
	ScanRotationDue(context.Background(), f, nil, nil, time.Hour, rotNow, 0)
	if f.gotLimit <= 0 {
		t.Fatalf("non-positive limit must be clamped to a positive default, got %d", f.gotLimit)
	}
}

// nil classifier falls back to DefaultRefreshClassifier: an OAuth candidate is
// still recovered without the caller passing a classifier explicitly.
func TestScanRotationDue_NilClassifierDefaults(t *testing.T) {
	f := &fakeRotationStore{due: []RotationCandidate{oauthCand(40)}}
	if _, err := ScanRotationDue(context.Background(), f, nil, nil, time.Hour, rotNow, 50); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(f.recovered) != 1 {
		t.Fatalf("nil classifier must default to registry-backed refreshability, got recovered=%d", len(f.recovered))
	}
}

// DefaultRefreshClassifier discriminates: OAuth modes are refreshable, static
// secret modes are not, and an unknown mode is conservatively non-refreshable.
func TestDefaultRefreshClassifier_Discriminates(t *testing.T) {
	classify := DefaultRefreshClassifier()
	if !classify(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeCode) {
		t.Fatal("anthropic/claude_code (OAuth) must classify as refreshable")
	}
	if classify(credentialstore.VendorAnthropic, credentialstore.AuthModeAPIKey) {
		t.Fatal("anthropic/api_key (static) must classify as non-refreshable")
	}
	if classify("nonsense-vendor", "nonsense-mode") {
		t.Fatal("unknown mode must classify as non-refreshable (conservative)")
	}
}
