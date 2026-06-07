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

	// MUTATION: ignoring Weight makes all three accounts win roughly uniformly,
	// so the heavy:light ratio collapses near 1 instead of near 10.
	lightAvg := float64(counts[11]+counts[12]) / 2
	if lightAvg == 0 {
		t.Fatalf("light accounts were never selected; counts=%v", counts)
	}
	ratio := float64(counts[10]) / lightAvg
	if ratio < 8.5 || ratio > 11.5 {
		t.Fatalf("weighted winner ratio = %.2f, want near 10; counts=%v", ratio, counts)
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
			// MUTATION: always applying weighted selection consumes a different
			// random sequence and strongly favors account 20, so this exact
			// pre-change Shuffle sequence comparison turns red.
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

	// MUTATION: dropping selector-side Weight population leaves account 31 at
	// zero, so the default-as-1 contract is not visible to routing gates.
	if got := capture.weights[30]; got != 5 {
		t.Fatalf("account 30 Weight = %d, want 5", got)
	}
	if got := capture.weights[31]; got != 1 {
		t.Fatalf("account 31 Weight = %d, want default 1", got)
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
