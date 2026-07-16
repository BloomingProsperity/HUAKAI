package router

import (
	"context"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// BW-AT-02：同一 Priority 的候选按绑定 Weight 选择首池，且绑定层权重不受
// 池内账号 SelectionMode 开关影响。
// 变异证红：忽略 Weight、只做均匀洗序、固定保留输入顺序，或仅在
// priority_weighted 模式启用绑定层加权，都会让至少一个子用例离开 88%～92%。
func TestDefaultRouterBindingWeightDistributionAcrossSelectionModes(t *testing.T) {
	for _, mode := range []string{"", "strict_priority", "priority_weighted"} {
		t.Run("mode="+mode, func(t *testing.T) {
			r := newDefaultRouterWithSeed(0xB17D1A6)
			input := bindingWeightPlanInput(
				[]int64{101, 102},
				[]PoolCandidateMeta{
					{PoolGroupID: 101, Priority: 10, Weight: 1, SelectionMode: mode},
					{PoolGroupID: 102, Priority: 10, Weight: 9, SelectionMode: mode},
				},
			)

			const draws = 20_000
			heavyWins := 0
			for i := 0; i < draws; i++ {
				plan, err := r.Plan(context.Background(), input)
				if err != nil {
					t.Fatalf("第 %d 次 Plan 失败：%v", i, err)
				}
				switch plan.Attempts[0].PoolGroupID {
				case 101:
				case 102:
					heavyWins++
				default:
					t.Fatalf("第 %d 次首候选=%d，不属于输入候选", i, plan.Attempts[0].PoolGroupID)
				}
			}

			share := float64(heavyWins) / draws
			if share < 0.88 || share > 0.92 {
				t.Fatalf("重权候选命中率=%.4f，期望位于 [0.88, 0.92]", share)
			}
		})
	}
}

// BW-AT-02 补充：完整回退顺序也必须按剩余权重继续无放回抽取，而不是只挑
// primary 后把余项恢复成原顺序。
// 变异证红：只加权第一个位置时，在候选 201 首先命中的条件下，202 会固定
// 占据第二位，重权候选 203 的条件命中率会从约 90% 跌到 0。
func TestDefaultRouterBindingWeightOrdersFallbackWithoutReplacement(t *testing.T) {
	r := newDefaultRouterWithSeed(0xFA11BAC)
	input := bindingWeightPlanInput(
		[]int64{201, 202, 203},
		[]PoolCandidateMeta{
			{PoolGroupID: 201, Priority: 10, Weight: 4},
			{PoolGroupID: 202, Priority: 10, Weight: 1},
			{PoolGroupID: 203, Priority: 10, Weight: 9},
		},
	)

	const draws = 30_000
	primary201 := 0
	second203 := 0
	for i := 0; i < draws; i++ {
		plan, err := r.Plan(context.Background(), input)
		if err != nil {
			t.Fatalf("第 %d 次 Plan 失败：%v", i, err)
		}
		if plan.Attempts[0].PoolGroupID != 201 {
			continue
		}
		primary201++
		if plan.Attempts[1].PoolGroupID == 203 {
			second203++
		}
	}
	if primary201 == 0 {
		t.Fatal("候选 201 从未命中首位，条件分布样本无效")
	}
	share := float64(second203) / float64(primary201)
	if share < 0.87 || share > 0.93 {
		t.Fatalf("候选 201 首位时，重权余项 203 的第二位命中率=%.4f，期望位于 [0.87, 0.93]", share)
	}
}

// BW-AT-03：Priority 是不可跨越的硬分层。较低优先级候选即使权重极大，
// 也只能排在整个高优先级段之后。
// 变异证红：若把所有候选放进一个全局加权集合，候选 303 几乎必然越层。
func TestDefaultRouterBindingWeightDoesNotCrossPriorityBands(t *testing.T) {
	r := newDefaultRouterWithSeed(0xA11CE)
	input := bindingWeightPlanInput(
		[]int64{301, 302, 303},
		[]PoolCandidateMeta{
			{PoolGroupID: 301, Priority: 10, Weight: 1},
			{PoolGroupID: 302, Priority: 10, Weight: 1},
			{PoolGroupID: 303, Priority: 20, Weight: 2_000_000_000},
		},
	)

	firstWins := map[int64]int{}
	for i := 0; i < 2_000; i++ {
		plan, err := r.Plan(context.Background(), input)
		if err != nil {
			t.Fatalf("第 %d 次 Plan 失败：%v", i, err)
		}
		got := []int64{
			plan.Attempts[0].PoolGroupID,
			plan.Attempts[1].PoolGroupID,
			plan.Attempts[2].PoolGroupID,
		}
		if !reflect.DeepEqual(got[0:2], []int64{301, 302}) && !reflect.DeepEqual(got[0:2], []int64{302, 301}) {
			t.Fatalf("第 %d 次高优先级段被破坏：%v", i, got)
		}
		if got[2] != 303 {
			t.Fatalf("第 %d 次低优先级候选位置=%v，期望固定在第 3 位", i, got)
		}
		firstWins[got[0]]++
	}
	if firstWins[301] == 0 || firstWins[302] == 0 {
		t.Fatalf("高优先级段未发生层内随机化：%v", firstWins)
	}
}

// BW-AT-04：Weight<=0 归一为 1，避免已启用绑定被永久饿死。
// 变异证红：若零值或负值按 0 参与抽样，候选 401 命中率会变成 0；若改为
// 均匀洗序，则命中率会接近 50%，两者都不在 8%～12%。
func TestDefaultRouterBindingWeightNormalizesNonPositiveWeight(t *testing.T) {
	for _, tc := range []struct {
		name   string
		weight int32
	}{
		{name: "zero", weight: 0},
		{name: "negative", weight: -7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newDefaultRouterWithSeed(0x0DEFACE)
			input := bindingWeightPlanInput(
				[]int64{401, 402},
				[]PoolCandidateMeta{
					{PoolGroupID: 401, Priority: 10, Weight: tc.weight},
					{PoolGroupID: 402, Priority: 10, Weight: 9},
				},
			)

			const draws = 20_000
			nonPositiveWins := 0
			for i := 0; i < draws; i++ {
				plan, err := r.Plan(context.Background(), input)
				if err != nil {
					t.Fatalf("第 %d 次 Plan 失败：%v", i, err)
				}
				if plan.Attempts[0].PoolGroupID == 401 {
					nonPositiveWins++
				}
			}
			share := float64(nonPositiveWins) / draws
			if share < 0.08 || share > 0.12 {
				t.Fatalf("非正权重 %d 候选命中率=%.4f，期望位于 [0.08, 0.12]", tc.weight, share)
			}
		})
	}
}

// BW-AT-04 边界：多个接近 int32 上限的 Weight 相加仍须保持正数并正常抽样。
// 变异证红：把累计权重收窄为 int32 会在第二个候选加入时溢出，导致随机
// 上界非法或候选永久不可达。
func TestDefaultRouterBindingWeightUsesInt64Accumulator(t *testing.T) {
	r := newDefaultRouterWithSeed(0x1A64)
	input := bindingWeightPlanInput(
		[]int64{451, 452},
		[]PoolCandidateMeta{
			{PoolGroupID: 451, Priority: 10, Weight: 2_000_000_000},
			{PoolGroupID: 452, Priority: 10, Weight: 2_000_000_000},
		},
	)
	wins := map[int64]int{}
	for i := 0; i < 2_000; i++ {
		plan, err := r.Plan(context.Background(), input)
		if err != nil {
			t.Fatalf("第 %d 次 Plan 失败：%v", i, err)
		}
		wins[plan.Attempts[0].PoolGroupID]++
	}
	if wins[451] == 0 || wins[452] == 0 {
		t.Fatalf("大权重累计后候选不可达：%v", wins)
	}
}

// BW-AT-05：单候选的预算、reason 与池内 SelectionMode 语义保持不变。
// 变异证红：若加权逻辑改变 attempt 预算、重复池识别或错误地按模式禁用候选，
// 任一精确 attempt 断言都会失败。
func TestDefaultRouterBindingWeightSingleCandidateSelectionModeRegression(t *testing.T) {
	for _, mode := range []string{"", "strict_priority", "priority_weighted"} {
		t.Run("mode="+mode, func(t *testing.T) {
			r := newDefaultRouterWithSeed(0x51061E)
			plan, err := r.Plan(context.Background(), bindingWeightPlanInput(
				[]int64{501},
				[]PoolCandidateMeta{{PoolGroupID: 501, Priority: 10, Weight: 99, SelectionMode: mode}},
			))
			if err != nil {
				t.Fatalf("Plan 失败：%v", err)
			}
			assertAttempts(t, plan, []wantAttempt{
				{index: 0, poolGroupID: 501, reason: "primary"},
				{index: 1, poolGroupID: 501, reason: "same_pool_account_failover"},
			})
			if plan.AttemptBudget != 2 {
				t.Fatalf("AttemptBudget=%d，期望 2", plan.AttemptBudget)
			}
		})
	}
}

// BW-AT-06：Router 只能洗序候选副本；缺元数据候选保持原位单例，并切断
// 两侧同 Priority 段。合法的连续同层段仍可独立洗序。
// 变异证红：原地洗序会改坏 candidates；把缺元数据零值当成有效层或跨缺口
// 合并会改变中间缺口用例；遇缺口后全量关闭加权则看不到前段两种首位。
func TestDefaultRouterBindingWeightPreservesInputAndMetadataGaps(t *testing.T) {
	r := newDefaultRouterWithSeed(0xC0FFEE)
	candidates := []int64{601, 602, 699}
	metadata := []PoolCandidateMeta{
		{PoolGroupID: 601, Priority: 10, Weight: 1},
		{PoolGroupID: 602, Priority: 10, Weight: 9},
	}
	wantCandidates := append([]int64(nil), candidates...)
	wantMetadata := append([]PoolCandidateMeta(nil), metadata...)
	firstWins := map[int64]int{}
	for i := 0; i < 256; i++ {
		plan, err := r.Plan(context.Background(), bindingWeightPlanInput(candidates, metadata))
		if err != nil {
			t.Fatalf("第 %d 次 Plan 失败：%v", i, err)
		}
		got := []int64{plan.Attempts[0].PoolGroupID, plan.Attempts[1].PoolGroupID, plan.Attempts[2].PoolGroupID}
		if got[2] != 699 {
			t.Fatalf("缺元数据候选离开原位：%v", got)
		}
		if !reflect.DeepEqual(got[0:2], []int64{601, 602}) && !reflect.DeepEqual(got[0:2], []int64{602, 601}) {
			t.Fatalf("连续同层段候选集合被破坏：%v", got)
		}
		firstWins[got[0]]++
	}
	if firstWins[601] == 0 || firstWins[602] == 0 {
		t.Fatalf("缺元数据候选错误地关闭了合法前段洗序：%v", firstWins)
	}
	if !reflect.DeepEqual(candidates, wantCandidates) {
		t.Fatalf("PoolCandidates 被原地修改：got %v want %v", candidates, wantCandidates)
	}
	if !reflect.DeepEqual(metadata, wantMetadata) {
		t.Fatalf("PoolMetadata 被原地修改：got %+v want %+v", metadata, wantMetadata)
	}

	middleGap := bindingWeightPlanInput(
		[]int64{611, 699, 612},
		[]PoolCandidateMeta{
			{PoolGroupID: 611, Priority: 0, Weight: 1},
			{PoolGroupID: 612, Priority: 0, Weight: 99},
		},
	)
	for i := 0; i < 128; i++ {
		plan, err := r.Plan(context.Background(), middleGap)
		if err != nil {
			t.Fatalf("中间缺口第 %d 次 Plan 失败：%v", i, err)
		}
		got := []int64{plan.Attempts[0].PoolGroupID, plan.Attempts[1].PoolGroupID, plan.Attempts[2].PoolGroupID}
		if !reflect.DeepEqual(got, []int64{611, 699, 612}) {
			t.Fatalf("中间缺口未保持原位单例：got %v", got)
		}
	}
}

// BW-AT-07：一个生产 Router 会被多个请求 goroutine 共享，随机源访问必须串行。
// 变异证红：删除 rand mutex 后，本测试在 go test -race 下报告数据竞争；若
// 无放回排列损坏，还会被候选集合与 attempt reason 的精确断言直接抓住。
func TestDefaultRouterBindingWeightConcurrentPlansAreSafe(t *testing.T) {
	r := newDefaultRouterWithSeed(0xC011AB1E)
	input := bindingWeightPlanInput(
		[]int64{701, 702, 703},
		[]PoolCandidateMeta{
			{PoolGroupID: 701, Priority: 10, Weight: 1},
			{PoolGroupID: 702, Priority: 10, Weight: 3},
			{PoolGroupID: 703, Priority: 10, Weight: 9},
		},
	)

	const workers = 24
	const plansPerWorker = 250
	errCh := make(chan string, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < plansPerWorker; i++ {
				plan, err := r.Plan(context.Background(), input)
				if err != nil {
					errCh <- "并发 Plan 返回错误"
					return
				}
				if len(plan.Attempts) != 3 || plan.AttemptBudget != 3 {
					errCh <- "并发 Plan 的 attempt 长度或预算错误"
					return
				}
				seen := map[int64]bool{}
				for index, attempt := range plan.Attempts {
					if attempt.Index != index || (index == 0 && attempt.Reason != "primary") || (index > 0 && attempt.Reason != "cross_pool_fallback") {
						errCh <- "并发 Plan 的 index 或 reason 错误"
						return
					}
					if attempt.PoolGroupID < 701 || attempt.PoolGroupID > 703 || seen[attempt.PoolGroupID] {
						errCh <- "并发 Plan 未形成合法无放回排列"
						return
					}
					seen[attempt.PoolGroupID] = true
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for message := range errCh {
		t.Error(message)
	}
}

func newDefaultRouterWithSeed(seed int64) *DefaultRouter {
	r := NewDefaultRouter()
	r.rand = rand.New(rand.NewSource(seed))
	return r
}

func bindingWeightPlanInput(candidates []int64, metadata []PoolCandidateMeta) PlanInput {
	return PlanInput{
		Context: RequestContext{RequestID: "binding-weight-test", TenantID: 1},
		Model: ResolvedModel{
			ProtocolFamily: "openai_chat",
			PoolCandidates: candidates,
			PoolMetadata:   metadata,
		},
	}
}
