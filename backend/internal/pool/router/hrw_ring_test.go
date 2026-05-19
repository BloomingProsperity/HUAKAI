// hrw_ring_test.go — PASR-lite A1 HRW + AccountRing 单测。
//
// 验证项:
//  1. NewAccountRing 去重 + 升序排序
//  2. HRWScore deterministic（同输入永远同输出）
//  3. HRWScore seed 改 → 输出改
//  4. TopK 段内顺序按 score 降序
//  5. TopK 边界（k>N, k=0, 空 ring）
//  6. Top3 等价 TopK(.,3)
//  7. Contains 二分正确
//  8. **HRW 关键性质: 增减 1 个账号时，约 1/N 段被影响**
//  9. AffectedSegments 增/减场景
package router

import (
	"fmt"
	"math/rand"
	"testing"
)

// fixtureRing 200 个账号 + seed 0xCAFEBABE，模拟万级 prefix 场景。
func fixtureRing() *AccountRing {
	accs := make([]int64, 200)
	for i := range accs {
		accs[i] = int64(i + 1) // 1..200
	}
	return NewAccountRing(accs, 0xCAFEBABE)
}

// fixturePrefixes 1000 条 prefix hash, 用 i 序生成确定性 byte 块。
func fixturePrefixes(n int) [][]byte {
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		// 16-byte deterministic prefix hash; 与 cache_routing.ComputePromptHash
		// 的输出形态一致（sha256 hex 截断）。
		var b [16]byte
		for j := range b {
			b[j] = byte((i + j*7) & 0xFF)
		}
		out[i] = b[:]
	}
	return out
}

func TestNewAccountRing_DedupesAndSorts(t *testing.T) {
	r := NewAccountRing([]int64{5, 1, 3, 1, 5, 0, 2}, 42) // 含 0 + 重复
	want := []int64{1, 2, 3, 5}                           // 0 被滤, 重复去掉, 升序
	if !int64SliceEqual(r.Accounts, want) {
		t.Errorf("got %v want %v", r.Accounts, want)
	}
}

func TestNewAccountRing_EmptyInput(t *testing.T) {
	r := NewAccountRing(nil, 1)
	if r.Accounts != nil {
		t.Errorf("空输入应返 nil Accounts, 得 %v", r.Accounts)
	}
}

func TestHRWScore_Deterministic(t *testing.T) {
	r := fixtureRing()
	prefix := []byte("test-prompt-prefix-1")
	a := r.HRWScore(prefix, 42)
	b := r.HRWScore(prefix, 42)
	if a != b {
		t.Errorf("HRWScore 同输入两次结果不同: %d vs %d", a, b)
	}
}

func TestHRWScore_SeedAffects(t *testing.T) {
	r1 := NewAccountRing([]int64{1, 2, 3}, 100)
	r2 := NewAccountRing([]int64{1, 2, 3}, 200)
	prefix := []byte("p1")
	if r1.HRWScore(prefix, 1) == r2.HRWScore(prefix, 1) {
		t.Error("seed 改了 score 应不同")
	}
}

func TestATPoolPASR001_HRWSeedDiversity(t *testing.T) {
	// AT-POOL-PASR-001: 同一账号集下, 不同请求 seed 必须稳定改变 HRW 排序,
	// 避免所有请求因排序重复而压到同一账号顺序。
	accs := make([]int64, 32)
	for i := range accs {
		accs[i] = int64(i + 1)
	}
	rng := rand.New(rand.NewSource(0xA11CE))
	ring := NewAccountRing(accs, 0xCAFEBABE)
	orders := make(map[string]struct{})
	for i := 0; i < 10; i++ {
		seed := fmt.Sprintf("request-seed-%016x", rng.Uint64())
		got := ring.TopK([]byte(seed), len(accs))
		orders[fmt.Sprint(got)] = struct{}{}
	}
	if len(orders) < 7 {
		t.Fatalf("不同 seed 未充分改变 HRW 排序: unique_orders=%d want >=7", len(orders))
	}
}

func TestTopK_DescendingByScore(t *testing.T) {
	r := fixtureRing()
	prefix := []byte("descending-test")
	top10 := r.TopK(prefix, 10)
	if len(top10) != 10 {
		t.Fatalf("len=%d want 10", len(top10))
	}
	// 验证段内顺序: top10[0] 的 score 必须 >= top10[1] >= ... >= top10[9]
	for i := 0; i < len(top10)-1; i++ {
		s1 := r.HRWScore(prefix, top10[i])
		s2 := r.HRWScore(prefix, top10[i+1])
		if s1 < s2 {
			t.Errorf("段内顺序错: position %d score=%d < position %d score=%d", i, s1, i+1, s2)
		}
	}
}

func TestTopK_Boundaries(t *testing.T) {
	r := NewAccountRing([]int64{10, 20, 30}, 1)
	if got := r.TopK(nil, 0); got != nil {
		t.Errorf("k=0 应返 nil, 得 %v", got)
	}
	if got := r.TopK([]byte{}, 100); len(got) != 3 {
		t.Errorf("k>N 应返全部 N=3, 得 %v", got)
	}
	empty := NewAccountRing(nil, 1)
	if got := empty.TopK([]byte{}, 5); got != nil {
		t.Errorf("空 ring 应返 nil, 得 %v", got)
	}
}

func TestTop3_EquivalentToTopK3(t *testing.T) {
	r := fixtureRing()
	prefix := []byte("equiv-test")
	a := r.Top3(prefix)
	b := r.TopK(prefix, 3)
	if !int64SliceEqual(a, b) {
		t.Errorf("Top3 != TopK(.,3): %v vs %v", a, b)
	}
}

func TestContains_BinarySearch(t *testing.T) {
	r := NewAccountRing([]int64{1, 5, 10, 100, 999}, 1)
	cases := map[int64]bool{
		1: true, 5: true, 10: true, 100: true, 999: true,
		0: false, 2: false, 50: false, 500: false, 1000: false,
	}
	for id, want := range cases {
		if got := r.Contains(id); got != want {
			t.Errorf("Contains(%d) = %v want %v", id, got, want)
		}
	}
}

// TestHRW_ReshuffleProperty 关键: HRW 数学性质验证。
//
// 加 1 个账号到 N-账号 ring，期望约 1/(N+1) 段（考虑 K=3 时大约 K/(N+1)）
// 因新账号挤入而变 top-3 成员。设容差 5x 期望保证测试稳定 (HRW 是分布
// 性质, 单次运行可能略偏)。
func TestHRW_ReshuffleProperty_AddOne(t *testing.T) {
	const k = 3
	const numPrefixes = 1000
	prefixes := fixturePrefixes(numPrefixes)

	r1 := fixtureRing()                                        // 200 accounts
	r2 := NewAccountRing(append(r1.Accounts, 999), 0xCAFEBABE) // +1 account = 201
	// 注: 共享 seed 才比较有意义

	affected := r2.AffectedSegments(prefixes, r1, k)
	// 期望 ≈ k / (N+1) * numPrefixes ≈ 3/201 * 1000 ≈ 14.9
	expected := numPrefixes * k / (len(r1.Accounts) + 1)
	upper := expected * 5
	lower := expected / 5
	if len(affected) > upper || len(affected) < lower {
		t.Errorf("HRW reshuffle 性质违反: 增 1 账号影响 %d 段, 期望 ~%d (容差 [%d, %d])",
			len(affected), expected, lower, upper)
	}
}

func TestHRW_ReshuffleProperty_RemoveOne(t *testing.T) {
	const k = 3
	const numPrefixes = 1000
	prefixes := fixturePrefixes(numPrefixes)

	r1 := fixtureRing()                                                // 200
	r2 := NewAccountRing(r1.Accounts[:len(r1.Accounts)-1], 0xCAFEBABE) // 199 (删最后一个)

	affected := r2.AffectedSegments(prefixes, r1, k)
	// 期望 ≈ k / N * numPrefixes ≈ 3/200 * 1000 = 15
	expected := numPrefixes * k / len(r1.Accounts)
	upper := expected * 5
	lower := expected / 5
	if len(affected) > upper || len(affected) < lower {
		t.Errorf("HRW reshuffle 性质违反: 减 1 账号影响 %d 段, 期望 ~%d (容差 [%d, %d])",
			len(affected), expected, lower, upper)
	}
}

func TestAffectedSegments_NilOldRing(t *testing.T) {
	r := fixtureRing()
	prefixes := fixturePrefixes(10)
	got := r.AffectedSegments(prefixes, nil, 3)
	if len(got) != 10 {
		t.Errorf("nil oldRing 应返全部 prefix, 得 %d", len(got))
	}
}

// TestHRW_DeterministicAcrossRuns 同样 (seed, prefix, accounts) 必须每次得相同 top-3。
// 防止后续重构引入 random 来源破坏 cache locality 保证。
func TestHRW_DeterministicAcrossRuns(t *testing.T) {
	r := fixtureRing()
	prefix := []byte("deterministic-check")
	first := r.Top3(prefix)
	for i := 0; i < 100; i++ {
		got := r.Top3(prefix)
		if !int64SliceEqual(first, got) {
			t.Fatalf("run %d Top3 漂移: first=%v now=%v", i, first, got)
		}
	}
}

// TestHRW_DistributionRoughlyUniform sanity: 1000 prefix 的 top-1 在 200 个账号
// 上的分布应近似均匀 (每账号被选中 ~5 次, 容差宽松)。
func TestHRW_DistributionRoughlyUniform(t *testing.T) {
	r := fixtureRing()
	prefixes := fixturePrefixes(1000)
	count := make(map[int64]int)
	for _, p := range prefixes {
		top1 := r.TopK(p, 1)
		if len(top1) > 0 {
			count[top1[0]]++
		}
	}
	if len(count) < 100 {
		t.Errorf("top-1 仅命中 %d 个账号 (200 个里), 分布太集中", len(count))
	}
	// 检查最大命中是否离谱: 期望 5, 容许 5x
	for id, c := range count {
		if c > 25 {
			t.Errorf("account %d 被选 %d 次 (期望 ~5), 分布异常", id, c)
		}
	}
}
