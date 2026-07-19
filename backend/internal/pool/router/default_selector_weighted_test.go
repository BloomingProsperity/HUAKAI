package router

import (
	"context"
	"math/rand"
	"testing"
	"time"
)

func TestRankFreshWeightedReservoirHonorsWeight(t *testing.T) {
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	accounts := []*AccountSnapshot{
		snap(10, 1, 100, 0.2, now),
		snap(11, 1, 100, 0.2, now),
		snap(12, 1, 100, 0.2, now),
	}
	accounts[0].Weight = 10
	accounts[1].Weight = 1
	accounts[2].Weight = 1

	selector := NewDefaultSelector(&stubAccountSource{})
	selector.rand = rand.New(rand.NewSource(0xA11CE))
	policy := &RoutingPolicy{SelectionMode: SelectionModePriorityWeighted}

	counts := map[int64]int{}
	const draws = 12000
	for i := 0; i < draws; i++ {
		ranked := selector.rankFresh(accounts, policy)
		if len(ranked) == 0 {
			t.Fatalf("rankFresh returned no candidates")
		}
		counts[ranked[0].ID]++
	}

	// 变异:忽略 Weight 会让三个账号大致均匀胜出,于是 heavy:light 比例从接近
	// 10 坍缩到接近 1。
	lightAvg := float64(counts[11]+counts[12]) / 2
	if lightAvg == 0 {
		t.Fatalf("light accounts were never selected; counts=%v", counts)
	}
	ratio := float64(counts[10]) / lightAvg
	if ratio < 8.5 || ratio > 11.5 {
		t.Fatalf("weighted winner ratio = %.2f, want near 10; counts=%v", ratio, counts)
	}
}

func TestRankFreshWeightedUsesWholeTopPriorityBand(t *testing.T) {
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	lowLoad := snap(40, 1, 1, 0.05, now.Add(-time.Hour))
	highWeight := snap(41, 1, 1, 0.95, now)
	lowerPriority := snap(42, 1, 2, 0, now.Add(-2*time.Hour))
	lowLoad.Weight = 1
	highWeight.Weight = 100
	lowerPriority.Weight = 10000

	selector := NewDefaultSelector(&stubAccountSource{})
	selector.rand = rand.New(rand.NewSource(0xBADC0DE))
	policy := &RoutingPolicy{SelectionMode: SelectionModePriorityWeighted}

	counts := map[int64]int{}
	const draws = 500
	for i := 0; i < draws; i++ {
		ranked := selector.rankFresh([]*AccountSnapshot{lowLoad, highWeight, lowerPriority}, policy)
		counts[ranked[0].ID]++
	}

	// 旧逻辑只把负载和最后使用时间也完全相同的账号纳入加权，账号 41 会永远
	// 失去抽签资格；同时低优先级账号无论权重多大都不能越级。
	if counts[41] < 450 {
		t.Fatalf("top-priority weighted account selected %d/%d times, want >=450; counts=%v", counts[41], draws, counts)
	}
	if counts[42] != 0 {
		t.Fatalf("lower-priority account selected %d times; counts=%v", counts[42], counts)
	}
}

func TestRankFreshDefaultModeUnchanged(t *testing.T) {
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	accounts := []*AccountSnapshot{
		snap(20, 1, 100, 0.2, now),
		snap(21, 1, 100, 0.2, now),
		snap(22, 1, 100, 0.2, now),
	}
	accounts[0].Weight = 10
	accounts[1].Weight = 1
	accounts[2].Weight = 1

	const seed = 0xD3FA017
	selector := NewDefaultSelector(&stubAccountSource{})
	selector.rand = rand.New(rand.NewSource(seed))
	expectedRand := rand.New(rand.NewSource(seed))
	policy := &RoutingPolicy{SelectionMode: SelectionModeStrictPriority}

	for i := 0; i < 64; i++ {
		expected := []int64{20, 21, 22}
		expectedRand.Shuffle(len(expected), func(i, j int) {
			expected[i], expected[j] = expected[j], expected[i]
		})
		got := selector.rankFresh(accounts, policy)
		if got[0].ID != expected[0] {
			// 变异:总是套用加权选择会消费不同的随机序列并强烈偏向账号 20,
			// 因此这一精确的、改动前 Shuffle 序列的比对会变红。
			t.Fatalf("draw %d default winner = %d, want pre-change shuffle winner %d", i, got[0].ID, expected[0])
		}
	}
}

func TestAccountSnapshotWeightPopulated(t *testing.T) {
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	weighted := snap(30, 1, 100, 0.2, now)
	weighted.Weight = 5
	unset := snap(31, 1, 100, 0.2, now)
	unset.Weight = 0

	capture := &weightCaptureGate{weights: make(map[int64]int32)}
	gates := DefaultGateChain()
	gates.Tenant = capture
	selector := NewDefaultSelector(
		&stubAccountSource{accounts: []*AccountSnapshot{weighted, unset}},
		WithGateChain(gates),
		WithSlotManager(newMemSlotManager()),
	)

	if _, err := selector.Select(context.Background(), SelectionRequest{TenantID: 1, RequestedModel: "x"}); err != nil {
		t.Fatalf("Select: %v", err)
	}

	// 变异:省去 selector 侧的 Weight 填充会让账号 31 停留在零,于是「默认为
	// 1」的契约对路由 gate 不可见。
	if got := capture.weights[30]; got != 5 {
		t.Fatalf("account 30 Weight = %d, want 5", got)
	}
	if got := capture.weights[31]; got != 1 {
		t.Fatalf("account 31 Weight = %d, want default 1", got)
	}
}

func TestDefaultSelectorPrefersActiveOverDegraded(t *testing.T) {
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	degraded := snap(50, 1, 1, 0, now.Add(-time.Hour))
	active := snap(51, 1, 50, 0.9, now)
	gates := DefaultGateChain()
	gates.Health = healthStatusGate{
		degraded.ID: {State: HealthStateDegraded},
		active.ID:   {State: HealthStateActive},
	}
	selector := NewDefaultSelector(
		&stubAccountSource{accounts: []*AccountSnapshot{degraded, active}},
		WithGateChain(gates),
	)

	res, err := selector.Select(context.Background(), SelectionRequest{TenantID: 1, RequestedModel: "m", RequestID: "req-health-rank"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID != active.ID {
		t.Fatalf("selected account=%d, want active account %d despite degraded account's better static rank", res.AccountID, active.ID)
	}
}

type weightCaptureGate struct {
	weights map[int64]int32
}

func (g *weightCaptureGate) Allow(_ context.Context, account *AccountSnapshot, _ SelectionRequest) (bool, GateFailureReason, error) {
	if _, ok := g.weights[account.ID]; !ok {
		g.weights[account.ID] = account.Weight
	}
	return true, "", nil
}
