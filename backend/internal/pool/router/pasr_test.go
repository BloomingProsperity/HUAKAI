// pasr_selector_test.go — PASR-lite A3 PASRSelector 单测。
package router

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeAccountSource 实现 AccountSource 测试用; 持有固定快照集。
type fakeAccountSource struct {
	snapshots []*AccountSnapshot
	err       error
}

func (f *fakeAccountSource) ListAccounts(_ context.Context, _ SelectionRequest) ([]*AccountSnapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.snapshots, nil
}

// fakeClaimGate 记录 acquisition 调用次数。
type fakeClaimGate struct {
	calls    int
	lastAcc  int64
	lastTok  uuid.UUID
	failNext bool
}

func (g *fakeClaimGate) WriteAcquisition(_ context.Context, _ int64, _ int64, accID int64, tok uuid.UUID) error {
	if g.failNext {
		g.failNext = false
		return errors.New("simulated claim error")
	}
	g.calls++
	g.lastAcc = accID
	g.lastTok = tok
	return nil
}

func newPASRTestRig(t *testing.T, accountIDs []int64) (*PASRSelector, *SegmentTable, *AccountRing, *fakeAccountSource, *fakeClaimGate) {
	t.Helper()
	ring := NewAccountRing(accountIDs, 0xCAFEBABE)
	tbl := NewSegmentTable(SegmentTableConfig{})
	now := time.Now()
	snaps := make([]*AccountSnapshot, 0, len(accountIDs))
	for _, id := range accountIDs {
		snaps = append(snaps, &AccountSnapshot{
			ID:         id,
			LoadRate:   0.1,
			Priority:   1,
			LastUsedAt: now.Add(-time.Duration(id) * time.Second),
		})
	}
	src := &fakeAccountSource{snapshots: snaps}
	cg := &fakeClaimGate{}
	sel, err := NewPASRSelector(PASRSelectorConfig{
		Accounts:     src,
		Claims:       cg,
		RingProvider: func() *AccountRing { return ring },
		Segments:     tbl,
		LoadCap:      0.95,
	})
	if err != nil {
		t.Fatalf("NewPASRSelector: %v", err)
	}
	return sel, tbl, ring, src, cg
}

func TestPASR_Select_HappyPath_PicksHRWTop(t *testing.T) {
	accs := []int64{10, 20, 30, 40, 50}
	sel, _, ring, _, cg := newPASRTestRig(t, accs)

	req := SelectionRequest{
		TenantID:       1,
		ClaimID:        100,
		RequestedModel: "claude-3-5",
		SessionHash:    "prefix-1",
	}
	res, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("Select err=%v", err)
	}

	// 选出的账号必在 HRW Top3 段内
	top3 := ring.Top3([]byte("prefix-1"))
	found := false
	for _, m := range top3 {
		if m == res.AccountID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("选中 %d 不在 HRW Top3 %v 内", res.AccountID, top3)
	}
	if cg.calls != 1 || cg.lastAcc != res.AccountID {
		t.Errorf("ClaimGate 应被调用一次 acc=%d, 实际 calls=%d acc=%d",
			res.AccountID, cg.calls, cg.lastAcc)
	}
	if res.AcquisitionToken == uuid.Nil {
		t.Error("AcquisitionToken 不应为 nil")
	}
}

func TestPASR_Select_RespectsModelAccountRoute(t *testing.T) {
	src := &fakeAccountSource{snapshots: []*AccountSnapshot{
		{ID: 10, TenantID: 1, LoadRate: 0.1},
		{ID: 20, TenantID: 1, LoadRate: 0.1},
	}}
	sel, err := NewPASRSelector(PASRSelectorConfig{
		Accounts: src,
		Segments: NewSegmentTable(SegmentTableConfig{}),
		Policies: &stubPolicy{p: &RoutingPolicy{
			ModelAccountIDs: map[string][]int64{"gpt-pin": {20}},
		}},
	})
	if err != nil {
		t.Fatalf("NewPASRSelector: %v", err)
	}

	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID:       1,
		RequestedModel: "gpt-pin",
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res == nil || res.AccountID != 20 {
		t.Fatalf("PASR 绕过模型账号强制路由：got=%+v want account=20", res)
	}
}

func TestPASRSelect_PopulatesRoutingReason(t *testing.T) {
	claims := &fakeClaimGate{}
	sel, _ := newPASRSlotRig(t, []int64{10, 20, 30, 40, 50}, newMemSlotManager(), claims)
	req := SelectionRequest{
		TenantID:       1,
		ClaimID:        9001,
		PoolGroupID:    77,
		RequestedModel: "claude-3-5",
		SessionHash:    "routing-reason-test",
		AttemptSeq:     2,
	}
	res, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("Select err=%v", err)
	}
	if len(res.RoutingReasonJSON) == 0 {
		t.Fatal("RoutingReasonJSON must be populated on successful PASR selection")
	}
	if string(res.RoutingReasonJSON) == "{}" {
		t.Fatal("RoutingReasonJSON must not be an empty object")
	}

	var reason map[string]any
	if err := json.Unmarshal(res.RoutingReasonJSON, &reason); err != nil {
		t.Fatalf("RoutingReasonJSON must be valid JSON object: %v; body=%s", err, res.RoutingReasonJSON)
	}
	if got, ok := reason["provider_account_id"].(float64); !ok || int64(got) != res.AccountID {
		t.Fatalf("provider_account_id must match selected account: got=%v want=%d body=%s",
			reason["provider_account_id"], res.AccountID, res.RoutingReasonJSON)
	}
	if got, ok := reason["billing_ledger_claim_id"].(float64); !ok || int64(got) != req.ClaimID {
		t.Fatalf("billing_ledger_claim_id must match request claim: got=%v want=%d body=%s",
			reason["billing_ledger_claim_id"], req.ClaimID, res.RoutingReasonJSON)
	}
}

func TestPASR_Select_PrefersCachedSegmentMember(t *testing.T) {
	accs := []int64{10, 20, 30, 40, 50}
	sel, tbl, ring, _, _ := newPASRTestRig(t, accs)

	prefix := "cache-prefer-test"
	// 第一次请求 → 创建段
	req := SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: prefix,
	}
	res1, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	// 模拟 res1 命中并标记 cache: 找出 res1 在段内的 idx, set bitmap
	seg := tbl.Lookup(1, []byte(prefix))
	if seg == nil {
		t.Fatal("段未被创建")
	}
	idx := seg.IndexOf(res1.AccountID)
	if idx < 0 {
		t.Fatalf("res1 acc %d 不在段成员 %v 内", res1.AccountID, seg.Members)
	}
	// 标记非 res1 的另一个段员有 cache
	otherIdx := (idx + 1) % PASRSegmentSize
	if seg.Members[otherIdx] == 0 {
		t.Skip("段无第二成员可测")
	}
	seg.MarkCacheSeen(otherIdx)

	// 第二次请求同 prefix → 应选 otherIdx 那个 (有 cache 的优先)
	req.ClaimID = 2
	res2, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res2.AccountID != seg.Members[otherIdx] {
		t.Errorf("应选有 cache 的段员 %d, 实选 %d", seg.Members[otherIdx], res2.AccountID)
	}

	// 防止 ring 未参与
	_ = ring
}

func TestPASR_Select_FallbackHRWWhenSegmentAllUnhealthy(t *testing.T) {
	accs := []int64{10, 20, 30, 40, 50}
	sel, _, ring, src, _ := newPASRTestRig(t, accs)
	prefix := "fallback-test"

	// 找出该 prefix 的段成员, 把它们全部超载
	top3 := ring.Top3([]byte(prefix))
	for _, m := range top3 {
		for _, s := range src.snapshots {
			if s.ID == m {
				s.LoadRate = 0.99 // 超载
			}
		}
	}

	req := SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: prefix,
	}
	res, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("Fallback err=%v", err)
	}
	// res.AccountID 应是段外的某个账号 (HRW 全 ring 接力)
	for _, m := range top3 {
		if res.AccountID == m {
			t.Errorf("Fallback 应选段外账号, 实选段内 %d", res.AccountID)
		}
	}
}

func TestPASR_Select_AllAccountsUnhealthy_ErrNoEligibleAccount(t *testing.T) {
	accs := []int64{10, 20, 30}
	sel, _, _, src, _ := newPASRTestRig(t, accs)
	for _, s := range src.snapshots {
		s.LoadRate = 0.99
	}
	req := SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "p",
	}
	_, err := sel.Select(context.Background(), req)
	if !errors.Is(err, ErrNoEligibleAccount) {
		t.Errorf("全超载应返 ErrNoEligibleAccount, 得 %v", err)
	}
}

func TestPASR_Select_AllHealthRejected_ErrAllChannelsDegraded(t *testing.T) {
	accs := []int64{10, 20, 30}
	sel, _, _, _, _ := newPASRTestRig(t, accs)
	gates := DefaultGateChain()
	gates.Health = healthStatusGate{
		10: {State: HealthStateCoolingDown},
		20: {State: HealthStateDisabled},
		30: {State: HealthStatePaused},
	}
	sel.gates = gates

	_, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "p",
	})
	if !errors.Is(err, ErrAllChannelsDegraded) {
		t.Fatalf("err=%v want ErrAllChannelsDegraded", err)
	}
}

func TestPASR_HRWFallback_DeprioritizesDegradedChannel(t *testing.T) {
	accs := []int64{10, 20, 30}
	sel, _, ring, src, _ := newPASRTestRig(t, accs)
	snapshots := make(map[int64]*AccountSnapshot, len(src.snapshots))
	for _, snap := range src.snapshots {
		snapshots[snap.ID] = snap
	}
	prefix := "degraded-priority"
	sorted := ring.TopK([]byte(prefix), len(accs))
	degraded := sorted[0]
	gates := DefaultGateChain()
	gates.Health = healthStatusGate{
		degraded: {State: HealthStateDegraded},
	}
	sel.gates = gates

	res, err := sel.scheduleHRWFullRing(context.Background(), gates, SelectionRequest{
		TenantID: 1, RequestedModel: "m", SessionHash: prefix,
	}, ring, snapshots, [PASRSegmentSize]int64{}, selectionFailures{}, nil)
	if err != nil {
		t.Fatalf("scheduleHRWFullRing: %v", err)
	}
	if res.AccountID == degraded {
		t.Fatalf("selected degraded account %d despite active alternatives; sorted=%v", degraded, sorted)
	}
}

func TestPASR_Select_RespectExcludedAccounts(t *testing.T) {
	accs := []int64{10, 20, 30}
	sel, _, ring, _, _ := newPASRTestRig(t, accs)
	prefix := "excluded-test"
	top3 := ring.Top3([]byte(prefix))

	req := SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m",
		SessionHash: prefix,
		ExcludedAccounts: map[int64]struct{}{
			top3[0]: {}, // 排除首选
		},
	}
	res, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.AccountID == top3[0] {
		t.Errorf("应跳过被 excluded 的 %d, 实选 %d", top3[0], res.AccountID)
	}
}

type healthStatusGate map[int64]HealthStatus

func (g healthStatusGate) Allow(_ context.Context, account *AccountSnapshot, _ SelectionRequest) (bool, GateFailureReason, error) {
	status := g.status(account)
	switch status.State {
	case HealthStateCoolingDown, HealthStateDisabled, HealthStatePaused:
		return false, GateFailureHealth, nil
	default:
		return true, "", nil
	}
}

func (g healthStatusGate) HealthStatus(_ context.Context, account *AccountSnapshot, _ SelectionRequest) (HealthStatus, error) {
	return g.status(account), nil
}

func (g healthStatusGate) status(account *AccountSnapshot) HealthStatus {
	if account == nil {
		return HealthStatus{State: HealthStateActive}
	}
	status, ok := g[account.ID]
	if !ok || status.State == "" {
		return HealthStatus{State: HealthStateActive}
	}
	return status
}

func TestPASR_Select_NoSessionHash_FallsThroughToModel(t *testing.T) {
	accs := []int64{10, 20, 30, 40}
	sel, _, _, _, _ := newPASRTestRig(t, accs)
	req := SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "claude-3", // 仅 model
	}
	res, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("无 SessionHash 也应能选: %v", err)
	}
	if res.AccountID == 0 {
		t.Error("应选到一个账号")
	}
}

func TestPASR_Select_EmptyRing_Errors(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	sel, err := NewPASRSelector(PASRSelectorConfig{
		Accounts:     &fakeAccountSource{},
		Claims:       &fakeClaimGate{},
		RingProvider: func() *AccountRing { return NewAccountRing(nil, 1) },
		Segments:     tbl,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sel.Select(context.Background(), SelectionRequest{TenantID: 1})
	if !errors.Is(err, ErrNoEligibleAccount) {
		t.Errorf("空 ring 应返 ErrNoEligibleAccount, 得 %v", err)
	}
}

func TestPASR_Select_RingProvider_HotSwap(t *testing.T) {
	accs1 := []int64{10, 20, 30}
	accs2 := []int64{40, 50, 60}
	tbl := NewSegmentTable(SegmentTableConfig{})

	now := time.Now()
	snaps1 := []*AccountSnapshot{
		{ID: 10, LoadRate: 0.1, LastUsedAt: now},
		{ID: 20, LoadRate: 0.1, LastUsedAt: now},
		{ID: 30, LoadRate: 0.1, LastUsedAt: now},
	}
	snaps2 := []*AccountSnapshot{
		{ID: 40, LoadRate: 0.1, LastUsedAt: now},
		{ID: 50, LoadRate: 0.1, LastUsedAt: now},
		{ID: 60, LoadRate: 0.1, LastUsedAt: now},
	}
	src := &fakeAccountSource{snapshots: snaps1}

	currentRing := NewAccountRing(accs1, 0xC0FFEE)
	sel, _ := NewPASRSelector(PASRSelectorConfig{
		Accounts:     src,
		Claims:       &fakeClaimGate{},
		RingProvider: func() *AccountRing { return currentRing },
		Segments:     tbl,
	})

	// 第一次用 ring1
	req := SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: "swap-test",
	}
	res, _ := sel.Select(context.Background(), req)
	if res.AccountID != 10 && res.AccountID != 20 && res.AccountID != 30 {
		t.Errorf("ring1 阶段应选 ring1 内账号, 选到 %d", res.AccountID)
	}

	// 热替换 ring + accounts
	currentRing = NewAccountRing(accs2, 0xC0FFEE)
	src.snapshots = snaps2

	// 必须先清段表 (ring 内成员变了, 旧段引用的 acc 已不存在)
	tbl.Delete(1, []byte("swap-test"))

	res, _ = sel.Select(context.Background(), req)
	if res.AccountID != 40 && res.AccountID != 50 && res.AccountID != 60 {
		t.Errorf("ring2 阶段应选 ring2 内账号, 选到 %d", res.AccountID)
	}
}

func TestPASR_Select_LoadCap_FiltersOverloaded(t *testing.T) {
	accs := []int64{10, 20, 30}
	sel, _, ring, src, _ := newPASRTestRig(t, accs)

	prefix := "loadcap-test"
	top3 := ring.Top3([]byte(prefix))

	// 把段内 top1 超载, top2/top3 健康
	for _, s := range src.snapshots {
		if s.ID == top3[0] {
			s.LoadRate = 0.99
		}
	}

	req := SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: prefix,
	}
	res, _ := sel.Select(context.Background(), req)
	if res.AccountID == top3[0] {
		t.Errorf("超载首选应被跳过, 实选 %d", res.AccountID)
	}
	// 应选 top2 或 top3
	if res.AccountID != top3[1] && res.AccountID != top3[2] {
		// load 完全相等时按 LastUsedAt 提前来选, 故段内 top2/top3 都可能
		// 严格不要求 top2 >> top3
		ids := []int64{top3[1], top3[2]}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		t.Errorf("应选 top2 或 top3 %v, 实选 %d", ids, res.AccountID)
	}
}

func TestPASR_NewPASRSelector_Validates(t *testing.T) {
	// M5 起 RingProvider 不再 mandatory — RingProvider=nil 时 Select 走
	// request-scoped ring (synthesis D3)。 M5 单测在 pasr_selector_ring_test.go
	// 单独验证 RingProvider=nil 场景, 此处只检查剩余必填字段。
	tbl := NewSegmentTable(SegmentTableConfig{})
	cases := []struct {
		name string
		cfg  PASRSelectorConfig
	}{
		{"no Accounts", PASRSelectorConfig{Segments: tbl, RingProvider: func() *AccountRing { return nil }}},
		{"no Segments", PASRSelectorConfig{Accounts: &fakeAccountSource{}, RingProvider: func() *AccountRing { return nil }}},
	}
	for _, tc := range cases {
		_, err := NewPASRSelector(tc.cfg)
		if err == nil {
			t.Errorf("%s: 应返 error", tc.name)
		}
	}
}

func TestPASR_DefaultLoadCap(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	sel, err := NewPASRSelector(PASRSelectorConfig{
		Accounts:     &fakeAccountSource{},
		Segments:     tbl,
		RingProvider: func() *AccountRing { return NewAccountRing(nil, 1) },
		// LoadCap 未设
	})
	if err != nil {
		t.Fatal(err)
	}
	if sel.loadCap != 0.95 {
		t.Errorf("默认 LoadCap 应 0.95, 得 %v", sel.loadCap)
	}
}

// =====================================================================
// 感知缓存的 A2:排序分数 = locality + headroom
// =====================================================================

// loadOf 在 src 里找 acc 对应 snapshot 设 LoadRate(测试辅助函数)。
func setLoadRate(src *fakeAccountSource, accID int64, rate float64) {
	for _, s := range src.snapshots {
		if s.ID == accID {
			s.LoadRate = rate
			return
		}
	}
}

// setLastUsed 同上, 设 LastUsedAt.
func setLastUsed(src *fakeAccountSource, accID int64, t time.Time) {
	for _, s := range src.snapshots {
		if s.ID == accID {
			s.LastUsedAt = t
			return
		}
	}
}

// TestA2_LocalityBeatsHeadroom 段内 hasCache=true LoadRate=0.9 vs
// hasCache=false LoadRate=0.0 → hasCache 必胜 (locality 1.0 > 最大 headroom 0.3).
func TestA2_LocalityBeatsHeadroom(t *testing.T) {
	accs := []int64{10, 20, 30, 40, 50}
	sel, tbl, _, src, _ := newPASRTestRig(t, accs)
	prefix := "a2-locality"

	// 先创段 (一次 Select)
	res1, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: prefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	seg := tbl.Lookup(1, []byte(prefix))
	if seg == nil {
		t.Fatal("段未创建")
	}
	idxRes1 := seg.IndexOf(res1.AccountID)
	otherIdx := (idxRes1 + 1) % PASRSegmentSize
	if seg.Members[otherIdx] == 0 {
		t.Skip("段无第二成员")
	}

	// 标 res1 acc hasCache=true, LoadRate=0.9 (高负载)
	seg.MarkCacheSeen(idxRes1)
	setLoadRate(src, res1.AccountID, 0.9)
	// otherIdx acc hasCache=false (默认), LoadRate=0.0 (空闲)
	setLoadRate(src, seg.Members[otherIdx], 0.0)

	// 第二次 Select: 即使 res1 高负载 (0.9), hasCache 强信号让它胜
	res2, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 2, RequestedModel: "m", SessionHash: prefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.AccountID != res1.AccountID {
		t.Errorf("locality 强信号 (hasCache+0.9 LoadRate) 应胜过 headroom (空闲但无 cache); 实选 %d, want %d",
			res2.AccountID, res1.AccountID)
	}
}

// TestA2_HeadroomBreaksTieAmongCached 两个段员都 hasCache=true,
// LoadRate 0.9 vs 0.1 → headroom 决胜, 选 0.1 那个.
func TestA2_HeadroomBreaksTieAmongCached(t *testing.T) {
	accs := []int64{10, 20, 30, 40, 50}
	sel, tbl, _, src, _ := newPASRTestRig(t, accs)
	prefix := "a2-tie-cached"

	res1, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: prefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	seg := tbl.Lookup(1, []byte(prefix))
	if seg == nil {
		t.Fatal("段未创建")
	}
	idxRes1 := seg.IndexOf(res1.AccountID)
	otherIdx := (idxRes1 + 1) % PASRSegmentSize
	if seg.Members[otherIdx] == 0 {
		t.Skip("段无第二成员")
	}

	// 两个都 hasCache=true
	seg.MarkCacheSeen(idxRes1)
	seg.MarkCacheSeen(otherIdx)
	// res1 高负载, otherIdx 空闲
	setLoadRate(src, res1.AccountID, 0.9)
	setLoadRate(src, seg.Members[otherIdx], 0.1)

	res2, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 2, RequestedModel: "m", SessionHash: prefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.AccountID != seg.Members[otherIdx] {
		t.Errorf("两 hasCache 段员中 headroom 高的胜; 实选 %d, want %d",
			res2.AccountID, seg.Members[otherIdx])
	}
}

// TestA2_HeadroomDecidesAmongUncached 全段员 hasCache=false,
// LoadRate 0.9 vs 0.1 → 选 0.1 (headroom 高).
func TestA2_HeadroomDecidesAmongUncached(t *testing.T) {
	accs := []int64{10, 20, 30, 40, 50}
	sel, tbl, _, src, _ := newPASRTestRig(t, accs)
	prefix := "a2-tie-uncached"

	res1, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: prefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	seg := tbl.Lookup(1, []byte(prefix))
	if seg == nil {
		t.Fatal("段未创建")
	}
	// 不 set 任何 hasCache bit (全 false)
	idxRes1 := seg.IndexOf(res1.AccountID)
	otherIdx := (idxRes1 + 1) % PASRSegmentSize
	thirdIdx := (idxRes1 + 2) % PASRSegmentSize
	if seg.Members[otherIdx] == 0 {
		t.Skip("段无第二成员")
	}
	// res1 + 第三段员高负载, otherIdx 唯一空闲, 让 headroom 决胜
	setLoadRate(src, res1.AccountID, 0.9)
	if seg.Members[thirdIdx] != 0 {
		setLoadRate(src, seg.Members[thirdIdx], 0.9)
	}
	setLoadRate(src, seg.Members[otherIdx], 0.1)

	res2, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 2, RequestedModel: "m", SessionHash: prefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.AccountID != seg.Members[otherIdx] {
		t.Errorf("全 hasCache=false 段中 headroom 高胜; 实选 %d, want %d",
			res2.AccountID, seg.Members[otherIdx])
	}
}

// TestA2_TieBreaksByLastUsed score 完全相等 (同 hasCache + 同 LoadRate)
// → LastUsedAt 久的胜 (round-robin 兜底).
func TestA2_TieBreaksByLastUsed(t *testing.T) {
	accs := []int64{10, 20, 30, 40, 50}
	sel, tbl, _, src, _ := newPASRTestRig(t, accs)
	prefix := "a2-tie-lastused"

	res1, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 1, RequestedModel: "m", SessionHash: prefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	seg := tbl.Lookup(1, []byte(prefix))
	if seg == nil {
		t.Fatal("段未创建")
	}
	idxRes1 := seg.IndexOf(res1.AccountID)
	otherIdx := (idxRes1 + 1) % PASRSegmentSize
	if seg.Members[otherIdx] == 0 {
		t.Skip("段无第二成员")
	}
	// 同 hasCache=true + 同 LoadRate=0.5
	seg.MarkCacheSeen(idxRes1)
	seg.MarkCacheSeen(otherIdx)
	setLoadRate(src, res1.AccountID, 0.5)
	setLoadRate(src, seg.Members[otherIdx], 0.5)
	// res1 LastUsedAt 较新, otherIdx LastUsedAt 较老 (该胜)
	now := time.Now()
	setLastUsed(src, res1.AccountID, now)
	setLastUsed(src, seg.Members[otherIdx], now.Add(-1*time.Hour))

	res2, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 1, ClaimID: 2, RequestedModel: "m", SessionHash: prefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.AccountID != seg.Members[otherIdx] {
		t.Errorf("score tie + LastUsedAt 久的胜; 实选 %d, want %d",
			res2.AccountID, seg.Members[otherIdx])
	}
}
