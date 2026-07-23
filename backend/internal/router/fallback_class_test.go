package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
)

// AT-BFC-001 Router 层：class 必须位于 Priority/Weight 外层。quota 候选即使
// Priority 更高、Weight 极大，也只能进入未被 executor 消费的目标 phase。
// 协议 executor 判别测试另锁 A 成功时 Q=0、normal 同类耗尽后 Q=1。
func TestATBFC001NormalClassCannotBePreemptedByFallbackPriorityOrWeight(t *testing.T) {
	r := newDefaultRouterWithSeed(0xBFC001)
	plan, err := r.Plan(context.Background(), fallbackPlanInput(
		[]int64{102, 101},
		[]PoolCandidateMeta{
			{PoolGroupID: 102, BindingID: 1002, MaxParallelRequests: 9, Priority: 0, Weight: 2_000_000_000, FallbackClass: bindingfallback.ClassQuota},
			{PoolGroupID: 101, BindingID: 1001, Priority: 100, Weight: 1, FallbackClass: bindingfallback.ClassNormal},
		},
	))
	if err != nil {
		t.Fatalf("Plan 失败：%v", err)
	}
	if plan.AttemptBudget != 2 || len(plan.Attempts) != 2 {
		t.Fatalf("normal 主预算/attempt=%d/%d，期望 2/2", plan.AttemptBudget, len(plan.Attempts))
	}
	if plan.SnapshotVersion != "registry:1:1;router:v0.3-binding-fallback-class" {
		t.Fatalf("存在真实 fallback phase 时 policy stamp=%q，期望升级到 v0.3", plan.SnapshotVersion)
	}
	for i, attempt := range plan.Attempts {
		if attempt.PoolGroupID != 101 || attempt.BindingID != 1001 || attempt.FallbackClass != bindingfallback.ClassNormal {
			t.Fatalf("normal attempt[%d]=%+v，期望只含 pool=101/binding=1001/class=normal", i, attempt)
		}
	}
	phase := requireSingleFallbackPhase(t, plan, bindingfallback.ClassQuota)
	if phase.AttemptBudget != 1 || len(phase.Attempts) != 1 {
		t.Fatalf("quota phase 预算/attempt=%d/%d，期望 1/1", phase.AttemptBudget, len(phase.Attempts))
	}
	if got := phase.Attempts[0]; got.PoolGroupID != 102 || got.BindingID != 1002 || got.MaxParallelRequests != 9 || got.FallbackClass != bindingfallback.ClassQuota {
		t.Fatalf("quota attempt=%+v，期望 pool=102/binding=1002/max_parallel=9/class=quota", got)
	}
}

// D6 Router 层：只有 fallback binding 的配置不得被暗中晋升出主类。
// TODO(第 4 步控制面)：补 UI 红色配置诊断；本步只锁运行时 fail-closed。
func TestRouterPlanRejectsFallbackOnlyConfiguration(t *testing.T) {
	r := newDefaultRouterWithSeed(0xBFC006)
	_, err := r.Plan(context.Background(), fallbackPlanInput(
		[]int64{601, 602},
		[]PoolCandidateMeta{
			{PoolGroupID: 601, FallbackClass: bindingfallback.ClassQuota},
			{PoolGroupID: 602, FallbackClass: bindingfallback.ClassManual},
		},
	))
	var planErr *PlanError
	if !errors.As(err, &planErr) || planErr.Code != "no_primary_binding" {
		t.Fatalf("fallback-only Plan error=%v，期望 no_primary_binding", err)
	}
}

func TestRouterPolicyStampIgnoresFallbackMetadataOutsideCandidateSet(t *testing.T) {
	r := newDefaultRouterWithSeed(0xBFC007)
	plan, err := r.Plan(context.Background(), fallbackPlanInput(
		[]int64{701},
		[]PoolCandidateMeta{
			{PoolGroupID: 701, FallbackClass: bindingfallback.ClassNormal},
			{PoolGroupID: 799, FallbackClass: bindingfallback.ClassQuota},
		},
	))
	if err != nil {
		t.Fatalf("Plan 失败：%v", err)
	}
	if plan.FallbackPhases != nil {
		t.Fatalf("候选集外 metadata 不得创建 phase：%+v", plan.FallbackPhases)
	}
	if plan.SnapshotVersion != "registry:1:1;router:v0.2-binding-weighted" {
		t.Fatalf("没有真实 phase 时 policy stamp=%q，期望旧版本", plan.SnapshotVersion)
	}
}

func TestRouterRejectsUnknownFallbackClass(t *testing.T) {
	r := newDefaultRouterWithSeed(0xBFC009)
	_, err := r.Plan(context.Background(), fallbackPlanInput(
		[]int64{901},
		[]PoolCandidateMeta{{PoolGroupID: 901, FallbackClass: "unexpected"}},
	))
	var planErr *PlanError
	if !errors.As(err, &planErr) || planErr.Code != "invalid_fallback_class" {
		t.Fatalf("未知 class 的 Plan error=%v，期望 invalid_fallback_class", err)
	}
}

// AT-BFC-002 Router 层：四类目标分别编译，顺序固定且各只有一次子预算。
// 共享 Coordinator 判别测试另锁同类耗尽精确转移、混合失败与恢复后零转移。
func TestATBFC002FallbackPhasesStayTypedOrderedAndBounded(t *testing.T) {
	r := newDefaultRouterWithSeed(0xBFC002)
	plan, err := r.Plan(context.Background(), fallbackPlanInput(
		[]int64{240, 230, 220, 210, 201, 202},
		[]PoolCandidateMeta{
			{PoolGroupID: 240, BindingID: 2400, Priority: 0, Weight: 99, FallbackClass: bindingfallback.ClassManual},
			{PoolGroupID: 230, BindingID: 2300, Priority: 1, Weight: 99, FallbackClass: bindingfallback.ClassSafety},
			{PoolGroupID: 220, BindingID: 2200, Priority: 2, Weight: 99, FallbackClass: bindingfallback.ClassContextWindow},
			{PoolGroupID: 210, BindingID: 2100, Priority: 3, Weight: 99, FallbackClass: bindingfallback.ClassQuota},
			{PoolGroupID: 201, BindingID: 2010, Priority: 100, Weight: 1, FallbackClass: bindingfallback.ClassNormal},
			{PoolGroupID: 202, BindingID: 2020, Priority: 200, Weight: 1, FallbackClass: bindingfallback.ClassNormal},
		},
	))
	if err != nil {
		t.Fatalf("Plan 失败：%v", err)
	}
	if got := attemptPoolIDs(plan.Attempts); !equalInt64s(got, []int64{201, 202, 201}) {
		t.Fatalf("normal attempts=%v，期望 [201 202 201]", got)
	}
	wantClasses := bindingfallback.FallbackClasses()
	wantPools := []int64{220, 230, 210, 240}
	if len(plan.FallbackPhases) != len(wantClasses) {
		t.Fatalf("FallbackPhases=%d，期望 %d", len(plan.FallbackPhases), len(wantClasses))
	}
	for i, phase := range plan.FallbackPhases {
		if phase.FallbackClass != wantClasses[i] || phase.AttemptBudget != 1 || len(phase.Attempts) != 1 {
			t.Fatalf("phase[%d]=%+v，期望 class=%q/budget=1/attempt=1", i, phase, wantClasses[i])
		}
		attempt := phase.Attempts[0]
		if attempt.PoolGroupID != wantPools[i] || attempt.FallbackClass != wantClasses[i] || attempt.Index != 0 {
			t.Fatalf("phase[%d] attempt=%+v，期望 pool=%d/class=%q/index=0", i, attempt, wantPools[i], wantClasses[i])
		}
	}
}

// AT-BFC-003 Router golden：新增 phase 表达能力后，normal-only 的既有可观察
// 字段必须逐字节不变；新增 FallbackPhases 必须保持 nil，policy stamp 不升级。
// 各协议判别测试另锁 HTTP 结果、claim 次数与 routing reason。
func TestATBFC003NormalOnlyLegacyProjectionIsByteStable(t *testing.T) {
	r := newDefaultRouterWithSeed(0xBFC003)
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "at-bfc-003", TenantID: 7},
		Model: ResolvedModel{
			ProtocolFamily:  "openai_chat",
			ProviderModelID: "default-model",
			PoolCandidates:  []int64{301, 302},
			PoolMetadata: []PoolCandidateMeta{
				{PoolGroupID: 301, ProviderModelID: "model-a", BindingID: 3001, MaxParallelRequests: 7, Priority: 10, Weight: 9, FallbackClass: bindingfallback.ClassNormal},
				{PoolGroupID: 302, ProviderModelID: "model-b", BindingID: 3002, MaxParallelRequests: 11, Priority: 20, Weight: 1, FallbackClass: ""},
			},
			SnapshotVersion: "registry:7:33",
		},
		Features: RequestFeatures{Stream: true, WantsToolUse: true},
	})
	if err != nil {
		t.Fatalf("Plan 失败：%v", err)
	}
	if plan.FallbackPhases != nil {
		t.Fatalf("normal-only FallbackPhases 必须为 nil，得到 %#v", plan.FallbackPhases)
	}
	for i, attempt := range plan.Attempts {
		if attempt.FallbackClass != bindingfallback.ClassNormal {
			t.Fatalf("normal attempt[%d].FallbackClass=%q，期望 normal", i, attempt.FallbackClass)
		}
	}

	got, err := json.Marshal(legacyRoutePlanProjection(plan))
	if err != nil {
		t.Fatalf("marshal compatibility projection：%v", err)
	}
	want := []byte(`{"Attempts":[{"Index":0,"PoolGroupID":301,"BindingID":3001,"MaxParallelRequests":7,"RequiredCapabilities":null,"MaxConcurrencyHint":0,"Reason":"primary","UpstreamModelID":"model-a"},{"Index":1,"PoolGroupID":302,"BindingID":3002,"MaxParallelRequests":11,"RequiredCapabilities":null,"MaxConcurrencyHint":0,"Reason":"cross_pool_fallback","UpstreamModelID":"model-b"},{"Index":2,"PoolGroupID":301,"BindingID":3001,"MaxParallelRequests":7,"RequiredCapabilities":null,"MaxConcurrencyHint":0,"Reason":"same_pool_account_failover","UpstreamModelID":"model-a"}],"AttemptBudget":3,"RetryableEndClasses":["upstream_error_5xx","upstream_rate_limit","first_token_timeout","inter_event_timeout"],"SnapshotVersion":"registry:7:33;router:v0.2-binding-weighted"}`)
	if !bytes.Equal(got, want) {
		t.Fatalf("normal-only legacy RoutePlan 发生字节漂移\n got: %s\nwant: %s", got, want)
	}
}

// AT-BFC-008 Router 层：class 内仍复用 Priority/Weight，binding Weight 不由
// selection_mode 开关。低优先级候选即使巨权重也不得被编入单次目标 attempt。
// executor 必须把目标 binding 自身的 selection_mode 原样交给账号选择器。
func TestATBFC008FallbackClassPreservesPriorityWeightAcrossSelectionModes(t *testing.T) {
	for _, mode := range []string{"", "strict_priority", "priority_weighted"} {
		t.Run("mode="+mode, func(t *testing.T) {
			r := newDefaultRouterWithSeed(0xBFC008)
			input := fallbackPlanInput(
				[]int64{801, 811, 812, 813},
				[]PoolCandidateMeta{
					{PoolGroupID: 801, BindingID: 8001, Priority: 100, Weight: 1, FallbackClass: bindingfallback.ClassNormal},
					{PoolGroupID: 811, BindingID: 8101, Priority: 10, Weight: 1, SelectionMode: mode, FallbackClass: bindingfallback.ClassQuota},
					{PoolGroupID: 812, BindingID: 8102, Priority: 10, Weight: 9, SelectionMode: mode, FallbackClass: bindingfallback.ClassQuota},
					{PoolGroupID: 813, BindingID: 8103, Priority: 20, Weight: 2_000_000_000, SelectionMode: mode, FallbackClass: bindingfallback.ClassQuota},
				},
			)

			const draws = 20_000
			heavyWins := 0
			for i := 0; i < draws; i++ {
				plan, err := r.Plan(context.Background(), input)
				if err != nil {
					t.Fatalf("第 %d 次 Plan 失败：%v", i, err)
				}
				if got := attemptPoolIDs(plan.Attempts); !equalInt64s(got, []int64{801, 801}) {
					t.Fatalf("第 %d 次 normal attempts=%v，期望 [801 801]", i, got)
				}
				phase := requireSingleFallbackPhase(t, plan, bindingfallback.ClassQuota)
				if phase.AttemptBudget != 1 || len(phase.Attempts) != 1 {
					t.Fatalf("第 %d 次 quota phase 未保持单次预算：%+v", i, phase)
				}
				if phase.Attempts[0].SelectionMode != mode {
					t.Fatalf("第 %d 次目标 selection_mode=%q，期望 %q", i, phase.Attempts[0].SelectionMode, mode)
				}
				switch phase.Attempts[0].PoolGroupID {
				case 811:
				case 812:
					heavyWins++
				case 813:
					t.Fatalf("第 %d 次低优先级巨权候选越层", i)
				default:
					t.Fatalf("第 %d 次未知 quota 候选：%+v", i, phase.Attempts[0])
				}
			}
			share := float64(heavyWins) / draws
			if share < 0.88 || share > 0.92 {
				t.Fatalf("重权 quota 候选命中率=%.4f，期望位于 [0.88, 0.92]", share)
			}
		})
	}
}

func fallbackPlanInput(candidates []int64, metadata []PoolCandidateMeta) PlanInput {
	return PlanInput{
		Context: RequestContext{RequestID: "binding-fallback-test", TenantID: 1},
		Model: ResolvedModel{
			ProtocolFamily:  "openai_chat",
			PoolCandidates:  candidates,
			PoolMetadata:    metadata,
			SnapshotVersion: "registry:1:1",
		},
	}
}

func requireSingleFallbackPhase(t *testing.T, plan RoutePlan, class bindingfallback.Class) FallbackPhasePlan {
	t.Helper()
	if len(plan.FallbackPhases) != 1 {
		t.Fatalf("FallbackPhases=%d，期望只有 %q：%+v", len(plan.FallbackPhases), class, plan.FallbackPhases)
	}
	phase := plan.FallbackPhases[0]
	if phase.FallbackClass != class {
		t.Fatalf("FallbackClass=%q，期望 %q", phase.FallbackClass, class)
	}
	return phase
}

func attemptPoolIDs(attempts []AttemptPlan) []int64 {
	out := make([]int64, len(attempts))
	for i, attempt := range attempts {
		out[i] = attempt.PoolGroupID
	}
	return out
}

func equalInt64s(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

type legacyAttemptProjection struct {
	Index                int
	PoolGroupID          int64
	BindingID            int64
	MaxParallelRequests  int64
	RequiredCapabilities []string
	MaxConcurrencyHint   int
	Reason               string
	UpstreamModelID      string
}

type legacyPlanProjection struct {
	Attempts            []legacyAttemptProjection
	AttemptBudget       int
	RetryableEndClasses []string
	SnapshotVersion     string
}

func legacyRoutePlanProjection(plan RoutePlan) legacyPlanProjection {
	attempts := make([]legacyAttemptProjection, len(plan.Attempts))
	for i, attempt := range plan.Attempts {
		attempts[i] = legacyAttemptProjection{
			Index:                attempt.Index,
			PoolGroupID:          attempt.PoolGroupID,
			BindingID:            attempt.BindingID,
			MaxParallelRequests:  attempt.MaxParallelRequests,
			RequiredCapabilities: attempt.RequiredCapabilities,
			MaxConcurrencyHint:   attempt.MaxConcurrencyHint,
			Reason:               attempt.Reason,
			UpstreamModelID:      attempt.UpstreamModelID,
		}
	}
	return legacyPlanProjection{
		Attempts:            attempts,
		AttemptBudget:       plan.AttemptBudget,
		RetryableEndClasses: plan.RetryableEndClasses,
		SnapshotVersion:     plan.SnapshotVersion,
	}
}
