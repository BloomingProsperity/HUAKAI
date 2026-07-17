// pasr_selector_slot_test.go — PASR-lite main-wire M3 atomic 单测。
//
// 覆盖 slot parity + 错误分类:
//
//	T-M3-1: Slots=nil → 兼容路径 (shadow / 老测试) 不持 slot, 仅 token
//	T-M3-2: Slots=nilSlotManager{} → ErrSlotManagerUnavailable 走兼容路径
//	T-M3-3: Slots=memSlotManager (健康) → 真持 slot + Claims 写入, token 来自 slot
//	T-M3-4: Slots.Acquire 返 ErrNoSlotAvailable → ErrPASRPreMutationFail (无副作用)
//	T-M3-5: Slot OK + Claims 写失败 → release slot + ErrPASRPostMutationFail
//	T-M3-6: ClaimID=0 + 无 binding 上限 → token-only 短路, **不调** Slots.Acquire
//	T-M3-6b: ClaimID=0 + 正 binding 上限 → 真占槽并向短端点返回 Release
//	T-M3-7: 真 Slots + Claims=nil → ErrPASRPreMutationFail fail-fast (HIGH-1)
//	T-M3-8: Acquire 返 token=Nil + Release 非 nil → release + PostMutationFail (MEDIUM-1)
//	T-M3-9: Claim 写失败 + Release 也失败 → 错误链含 release 错误 (HIGH-2)
//	T-M3-10: errors.Is 链保留 ErrNoSlotAvailable 根因 (MEDIUM-2)
//	T-M3-11: token=Nil + Release 失败 → 错误链含 release 错误 (MEDIUM round-2)
//
// M3 不变量: 任何错误返回时 in_flight_count 与 billing_claims 均还原到入函数前状态。
package router

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// spySlotManager 包 inner SlotManager + 计数 Acquire 调用次数，验证 ClaimID=0
// 是否按 binding 上限选择短路或真实占槽。
type spySlotManager struct {
	inner        SlotManager
	mu           sync.Mutex
	acquireCalls int
}

func (s *spySlotManager) Acquire(ctx context.Context, snap *AccountSnapshot, req SelectionRequest) (*AcquireResult, error) {
	s.mu.Lock()
	s.acquireCalls++
	s.mu.Unlock()
	return s.inner.Acquire(ctx, snap, req)
}

func (s *spySlotManager) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acquireCalls
}

// brokenTokenSlotManager 模拟 Acquire 返 *AcquireResult 但 token=Nil + Release
// 非 nil — 测试 MEDIUM-1 修复 (token=Nil 也走 release 路径)。 releaseFails 控制
// release 是否失败 (T-M3-11 验证错误链)。
type brokenTokenSlotManager struct {
	releaseCalls int
	releaseFails bool
	mu           sync.Mutex
}

func (b *brokenTokenSlotManager) Acquire(_ context.Context, _ *AccountSnapshot, _ SelectionRequest) (*AcquireResult, error) {
	rel := func(_ context.Context) error {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.releaseCalls++
		if b.releaseFails {
			return errors.New("simulated release failure on broken token")
		}
		return nil
	}
	return &AcquireResult{AcquisitionToken: uuid.Nil, Release: rel}, nil
}

func (b *brokenTokenSlotManager) releases() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.releaseCalls
}

// failingReleaseSlotManager Acquire 成功 + Release 必失败 — 测试 HIGH-2 修复
// (release 失败时错误链含 "slot release failed")。
type failingReleaseSlotManager struct{}

func (failingReleaseSlotManager) Acquire(_ context.Context, _ *AccountSnapshot, _ SelectionRequest) (*AcquireResult, error) {
	tok := uuid.New()
	rel := func(_ context.Context) error {
		return errors.New("simulated release failure")
	}
	return &AcquireResult{AcquisitionToken: tok, Release: rel}, nil
}

// erroringSlotManager 强制 Acquire 返预定错误, 用于 T-M3-4。
type erroringSlotManager struct {
	err error
}

func (e *erroringSlotManager) Acquire(_ context.Context, _ *AccountSnapshot, _ SelectionRequest) (*AcquireResult, error) {
	return nil, e.err
}

// failingClaimGate Claims.WriteAcquisition 一次失败, 用于 T-M3-5。
type failingClaimGate struct {
	calls int
}

func (g *failingClaimGate) WriteAcquisition(_ context.Context, _ int64, _ int64, _ int64, _ uuid.UUID) error {
	g.calls++
	return errors.New("simulated claim write failure")
}

// newPASRSlotRig 同 newPASRTestRig 但允许注入 SlotManager + 自定义 Claims。
func newPASRSlotRig(t *testing.T, accountIDs []int64, slots SlotManager, claims ClaimGate) (*PASRSelector, *fakeAccountSource) {
	t.Helper()
	ring := NewAccountRing(accountIDs, 0xCAFEBABE)
	tbl := NewSegmentTable(SegmentTableConfig{})
	snaps := make([]*AccountSnapshot, 0, len(accountIDs))
	for _, id := range accountIDs {
		snaps = append(snaps, &AccountSnapshot{
			ID:       id,
			LoadRate: 0.1,
			Priority: 1,
		})
	}
	src := &fakeAccountSource{snapshots: snaps}
	sel, err := NewPASRSelector(PASRSelectorConfig{
		Accounts:     src,
		Claims:       claims,
		Slots:        slots,
		RingProvider: func() *AccountRing { return ring },
		Segments:     tbl,
		LoadCap:      0.95,
	})
	if err != nil {
		t.Fatalf("NewPASRSelector: %v", err)
	}
	return sel, src
}

func TestPASRSlot_NilSlotManager_TokenOnlyPath(t *testing.T) {
	cg := &fakeClaimGate{}
	sel, _ := newPASRSlotRig(t, []int64{1, 2, 3}, nil, cg)
	req := SelectionRequest{TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "slot-nil"}

	res, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("Slots=nil 兼容路径应返成功 result, 实际 err=%v", err)
	}
	if res.AcquisitionToken == uuid.Nil {
		t.Errorf("token 不应为 nil")
	}
	if cg.calls != 1 {
		t.Errorf("Claims 应被调一次 (老路径仍写 claim), 实际 %d", cg.calls)
	}
}

func TestPASRSlot_NilSlotManagerImpl_FallbackToTokenOnly(t *testing.T) {
	cg := &fakeClaimGate{}
	sel, _ := newPASRSlotRig(t, []int64{1, 2, 3}, nilSlotManager{}, cg)
	req := SelectionRequest{TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "slot-unavail"}

	res, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("nilSlotManager 应被识别为兼容信号, 实际 err=%v", err)
	}
	if res.AcquisitionToken == uuid.Nil {
		t.Errorf("token 不应为 nil")
	}
	if cg.calls != 1 {
		t.Errorf("Claims 应仍被调一次, 实际 %d", cg.calls)
	}
}

func TestPASRSlot_RealSlotManager_AcquiresAndWritesClaim(t *testing.T) {
	mm := newMemSlotManager()
	cg := &fakeClaimGate{}
	sel, _ := newPASRSlotRig(t, []int64{1, 2, 3}, mm, cg)
	req := SelectionRequest{TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "slot-ok"}

	res, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("健康 SlotManager 应成功 acquire + claim, 实际 err=%v", err)
	}
	if res.AcquisitionToken == uuid.Nil {
		t.Fatalf("token 不应为 nil")
	}
	if cg.calls != 1 {
		t.Errorf("Claims 应被调一次, 实际 %d", cg.calls)
	}
	if cg.lastTok != res.AcquisitionToken {
		t.Errorf("Claims 收到的 token 应等于返回 token (slot 出, claim 收同一 token)")
	}
	if mm.releaseCount(res.AcquisitionToken) != 0 {
		t.Errorf("成功路径不应触发 release, 实际 release count=%d", mm.releaseCount(res.AcquisitionToken))
	}
}

func TestPASRSlot_AcquireFailsBeforeClaim_ErrPreMutation(t *testing.T) {
	noslot := &erroringSlotManager{err: ErrNoSlotAvailable}
	cg := &fakeClaimGate{}
	sel, _ := newPASRSlotRig(t, []int64{1, 2, 3}, noslot, cg)
	req := SelectionRequest{TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "slot-pre-fail"}

	res, err := sel.Select(context.Background(), req)
	if err == nil {
		t.Fatalf("ErrNoSlotAvailable 应包装成 ErrPASRPreMutationFail, 实际成功 res=%+v", res)
	}
	if !errors.Is(err, ErrPASRPreMutationFail) {
		t.Errorf("err=%v want wraps ErrPASRPreMutationFail", err)
	}
	if cg.calls != 0 {
		t.Errorf("pre-mutation 失败时 Claims 不应被调, 实际 %d", cg.calls)
	}
}

func TestPASRSlot_ClaimFailsAfterAcquire_ErrPostMutation_ReleaseSlot(t *testing.T) {
	mm := newMemSlotManager()
	failClaim := &failingClaimGate{}
	sel, _ := newPASRSlotRig(t, []int64{1, 2, 3}, mm, failClaim)
	req := SelectionRequest{TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "slot-post-fail"}

	res, err := sel.Select(context.Background(), req)
	if err == nil {
		t.Fatalf("Claims 失败应包装成 ErrPASRPostMutationFail, 实际成功 res=%+v", res)
	}
	if !errors.Is(err, ErrPASRPostMutationFail) {
		t.Errorf("err=%v want wraps ErrPASRPostMutationFail", err)
	}
	if failClaim.calls != 1 {
		t.Errorf("Claims 应被调一次 (失败), 实际 %d", failClaim.calls)
	}
	// release 必须发生 (in_flight_count 还原) — 验证 release 被调了恰好一次
	mm.mu.Lock()
	releaseCount := 0
	for _, c := range mm.releases {
		releaseCount += c
	}
	mm.mu.Unlock()
	if releaseCount != 1 {
		t.Errorf("post-mutation 失败时应 release slot 1 次, 实际累计 %d", releaseCount)
	}
}

func TestPASRSlot_ZeroClaimID_ShortCircuitsBeforeAcquire(t *testing.T) {
	// req.ClaimID=0 且上限未启用时继续走 tokenOnlyResult，不制造无意义槽行。
	mm := newMemSlotManager()
	spy := &spySlotManager{inner: mm}
	failClaim := &failingClaimGate{}
	sel, _ := newPASRSlotRig(t, []int64{1, 2, 3}, spy, failClaim)
	req := SelectionRequest{TenantID: 1, ClaimID: 0, RequestedModel: "m", SessionHash: "slot-no-claimid"}

	res, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("ClaimID=0 应走 tokenOnlyResult, 实际 err=%v", err)
	}
	if res.AcquisitionToken == uuid.Nil {
		t.Fatalf("token 不应为 nil")
	}
	if spy.calls() != 0 {
		t.Errorf("ClaimID=0 短路应跳过 Slots.Acquire, 实际 acquire 调 %d 次 (HIGH-1 泄漏隐患)", spy.calls())
	}
	if failClaim.calls != 0 {
		t.Errorf("ClaimID=0 时 Claims 不应被调, 实际 %d", failClaim.calls)
	}
}

func TestPASRSlot_ZeroClaimIDWithBindingCapAcquiresAndExposesRelease(t *testing.T) {
	mm := newMemSlotManager()
	spy := &spySlotManager{inner: mm}
	failClaim := &failingClaimGate{}
	sel, _ := newPASRSlotRig(t, []int64{1, 2, 3}, spy, failClaim)
	req := SelectionRequest{
		TenantID: 1, ClaimID: 0, BindingID: 7, MaxParallelRequests: 1,
		RequestedModel: "m", SessionHash: "slot-no-claimid-with-binding-cap",
	}

	res, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("正 binding 上限的无 claim 请求应真实占槽，err=%v", err)
	}
	if res == nil || res.AcquisitionToken == uuid.Nil || res.Release == nil {
		t.Fatalf("result=%+v want token 与 Release", res)
	}
	if spy.calls() != 1 {
		t.Fatalf("Slots.Acquire calls=%d want 1", spy.calls())
	}
	if failClaim.calls != 0 {
		t.Fatalf("ClaimID=0 时 Claims calls=%d want 0", failClaim.calls)
	}
	if err := res.Release(context.Background()); err != nil {
		t.Fatalf("短端点释放槽失败: %v", err)
	}
	mm.mu.Lock()
	releaseCount := 0
	for _, count := range mm.releases {
		releaseCount += count
	}
	mm.mu.Unlock()
	if releaseCount != 1 {
		t.Fatalf("release calls=%d want 1", releaseCount)
	}
}

func TestPASRSlot_RealSlotsWithoutClaims_FailFast(t *testing.T) {
	// HIGH-1 fix: 真 SlotManager 注入但 Claims=nil 是 misconfigure —
	// acquire 后无法写 acquisition, slot 会泄漏到 sweeper。 启动期就 fail-fast。
	mm := newMemSlotManager()
	spy := &spySlotManager{inner: mm}
	sel, _ := newPASRSlotRig(t, []int64{1, 2, 3}, spy, nil)
	req := SelectionRequest{TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "slot-no-claims"}

	res, err := sel.Select(context.Background(), req)
	if err == nil {
		t.Fatalf("Slots 注入但 Claims=nil 应 fail-fast, 实际成功 res=%+v", res)
	}
	if !errors.Is(err, ErrPASRPreMutationFail) {
		t.Errorf("err=%v want wraps ErrPASRPreMutationFail", err)
	}
	if spy.calls() != 0 {
		t.Errorf("misconfigure 应在 acquire 前 fail, 实际 acquire 调 %d 次", spy.calls())
	}
}

func TestPASRSlot_BrokenTokenAcquire_ReleasesAndPostMutationFail(t *testing.T) {
	// MEDIUM-1 fix: Acquire 返 *AcquireResult 但 AcquisitionToken=Nil + Release
	// 非 nil → 表示 SlotManager 已 mutate 但状态损坏, 必须 release 不可静默泄漏。
	bad := &brokenTokenSlotManager{}
	cg := &fakeClaimGate{}
	sel, _ := newPASRSlotRig(t, []int64{1, 2, 3}, bad, cg)
	req := SelectionRequest{TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "slot-broken-token"}

	res, err := sel.Select(context.Background(), req)
	if err == nil {
		t.Fatalf("token=Nil 应包装 ErrPASRPostMutationFail, 实际成功 res=%+v", res)
	}
	if !errors.Is(err, ErrPASRPostMutationFail) {
		t.Errorf("err=%v want wraps ErrPASRPostMutationFail", err)
	}
	if bad.releases() != 1 {
		t.Errorf("token=Nil + Release 非 nil 时应触发 release, 实际 %d", bad.releases())
	}
	if cg.calls != 0 {
		t.Errorf("token=Nil 时 Claims 不应被调, 实际 %d", cg.calls)
	}
}

func TestPASRSlot_PostMutationFail_ReleaseAlsoFails_ErrorChainCarriesBoth(t *testing.T) {
	// HIGH-2 fix: post-mutation 失败时 release 也失败, 错误链必须同时含 claim
	// 错误 + release 错误, dispatcher / ops 才能定位"slot 没真还原"事件。
	failSlot := failingReleaseSlotManager{}
	failClaim := &failingClaimGate{}
	sel, _ := newPASRSlotRig(t, []int64{1, 2, 3}, failSlot, failClaim)
	req := SelectionRequest{TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "slot-double-fail"}

	res, err := sel.Select(context.Background(), req)
	if err == nil {
		t.Fatalf("claim+release 双失败应返 ErrPASRPostMutationFail, 实际成功 res=%+v", res)
	}
	if !errors.Is(err, ErrPASRPostMutationFail) {
		t.Errorf("err=%v want wraps ErrPASRPostMutationFail", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "claim write") {
		t.Errorf("err msg %q 应含 claim 错误根因", msg)
	}
	if !strings.Contains(msg, "slot release failed") {
		t.Errorf("err msg %q 应含 'slot release failed' (HIGH-2: release 错误不应被吞)", msg)
	}
}

func TestPASRSlot_BrokenTokenAcquire_ReleaseFails_ErrorChainCarriesBoth(t *testing.T) {
	// MEDIUM round-2 fix: token=Nil + Release 失败时, 错误链必须含 release 错误,
	// 不可静默吞 (跟 HIGH-2 同语义, dispatcher / ops 才能定位泄漏事件)。
	bad := &brokenTokenSlotManager{releaseFails: true}
	cg := &fakeClaimGate{}
	sel, _ := newPASRSlotRig(t, []int64{1, 2, 3}, bad, cg)
	req := SelectionRequest{TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "slot-broken-release-fail"}

	_, err := sel.Select(context.Background(), req)
	if err == nil {
		t.Fatalf("token=Nil + release 失败应返 ErrPASRPostMutationFail")
	}
	if !errors.Is(err, ErrPASRPostMutationFail) {
		t.Errorf("err=%v want wraps ErrPASRPostMutationFail", err)
	}
	if !strings.Contains(err.Error(), "slot release failed") {
		t.Errorf("err msg %q 应含 'slot release failed' (release 错误不应被吞)", err.Error())
	}
	if bad.releases() != 1 {
		t.Errorf("应触发一次 release, 实际 %d", bad.releases())
	}
}

func TestPASRSlot_PreMutationFail_ErrorChainPreservesRoot(t *testing.T) {
	// MEDIUM-2 fix: 用 %w 双包装, errors.Is 链保留 ErrNoSlotAvailable 根因,
	// dispatcher 可针对不同 sentinel 决策 (pre-mutation fallback)。
	noslot := &erroringSlotManager{err: ErrNoSlotAvailable}
	cg := &fakeClaimGate{}
	sel, _ := newPASRSlotRig(t, []int64{1, 2, 3}, noslot, cg)
	req := SelectionRequest{TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "slot-chain"}

	_, err := sel.Select(context.Background(), req)
	if err == nil {
		t.Fatalf("应返错误")
	}
	if !errors.Is(err, ErrPASRPreMutationFail) {
		t.Errorf("err=%v want wraps ErrPASRPreMutationFail", err)
	}
	if !errors.Is(err, ErrNoSlotAvailable) {
		t.Errorf("err=%v 应保留 ErrNoSlotAvailable 根因 (MEDIUM-2: %%w 多层包装)", err)
	}
}
