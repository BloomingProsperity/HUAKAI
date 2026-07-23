package router

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
)

// DefaultRouter 生成保守路由计划：先按 binding class 分桶，再保留各桶的
// Priority 硬分层，仅在连续同优先级段内按 Weight 洗序，并把账号选择与
// 健康 gate 留给 executor/pool 层。
type DefaultRouter struct {
	// SnapshotVersion 标识规划时刻的 Router 策略版本；它会拼接到
	// plan.SnapshotVersion 里的 Registry stamp 之后，使审计回放
	// 能从一列里重建两层信息。
	SnapshotVersion string
	rand            *rand.Rand
	randMu          sync.Mutex // 保护 rand；math/rand.Rand 不支持并发调用
}

// NewDefaultRouter 返回标准保守 planner。Registry 负责过滤候选并给出硬分层
// 顺序，Router 在层内生成非固定顺序后扩展成有界 attempt 序列。
func NewDefaultRouter() *DefaultRouter {
	return &DefaultRouter{
		SnapshotVersion: "v0.2-binding-weighted",
		rand:            newDefaultRouterRand(),
	}
}

const fallbackClassPolicyVersion = "v0.3-binding-fallback-class"

const (
	retryableEndClassUpstreamError5xx  = "upstream_error_5xx"
	retryableEndClassUpstreamRateLimit = "upstream_rate_limit"
	retryableEndClassFirstTokenTimeout = "first_token_timeout"
	retryableEndClassInterEventTimeout = "inter_event_timeout"
)

// retryablePreDeliveryEndClasses 只列出现有 F-GW-002 end_class 中能表达
// 交付前换号/换池的失败类。inter_event_timeout 纳入这里，是因为 gateway
// 当前把 network_timeout / upstream_timeout 映射到该 end_class；executor
// 仍必须用 delivery tracker 禁止已交付后的重试。
var retryablePreDeliveryEndClasses = []string{
	retryableEndClassUpstreamError5xx,
	retryableEndClassUpstreamRateLimit,
	retryableEndClassFirstTokenTimeout,
	retryableEndClassInterEventTimeout,
}

// Plan 实现 Router。
func (r *DefaultRouter) Plan(_ context.Context, req PlanInput) (RoutePlan, error) {
	if req.Context.RequestID == "" {
		return RoutePlan{}, &PlanError{
			Code:    "missing_request_id",
			Message: "RequestContext.RequestID must be set by chi middleware before Router.Plan",
		}
	}
	if req.Context.TenantID == 0 {
		return RoutePlan{}, &PlanError{
			Code:    "missing_tenant",
			Message: "RequestContext.TenantID must be resolved by Auth before Router.Plan",
		}
	}
	if req.Model.ProtocolFamily == "" {
		return RoutePlan{}, &PlanError{
			Code:    "model_unsupported",
			Message: "ResolvedModel.ProtocolFamily required (Phase C: 'anthropic_messages' or 'openai_chat')",
		}
	}

	if len(req.Model.PoolCandidates) == 0 {
		return RoutePlan{}, &PlanError{
			Code:    "no_eligible_pool",
			Message: "ResolvedModel.PoolCandidates is empty; Registry should have surfaced ErrTenantNoAccess upstream",
		}
	}

	caps := requiredCapabilities(req.Features)
	metaByPool := poolMetadataByGroup(req.Model.PoolMetadata)
	classCandidates, err := poolCandidatesByFallbackClass(req.Model.PoolCandidates, metaByPool)
	if err != nil {
		return RoutePlan{}, err
	}
	poolCandidates := r.weightedPoolCandidateOrder(classCandidates[bindingfallback.ClassNormal], metaByPool)
	if len(poolCandidates) == 0 {
		return RoutePlan{}, &PlanError{
			Code:    "no_primary_binding",
			Message: "ResolvedModel has fallback bindings but no normal binding",
		}
	}
	budget := attemptBudgetForPools(len(poolCandidates))
	seenPools := make(map[int64]struct{}, len(poolCandidates))
	attempts := make([]AttemptPlan, 0, budget)
	for i := 0; i < budget; i++ {
		poolGroupID := poolCandidates[i%len(poolCandidates)]
		bindingMeta := metaByPool[poolGroupID]
		reason := "cross_pool_fallback"
		if i == 0 {
			reason = "primary"
		} else if _, seen := seenPools[poolGroupID]; seen {
			reason = "same_pool_account_failover"
		}
		attempts = append(attempts, AttemptPlan{
			Index:                i,
			PoolGroupID:          poolGroupID,
			BindingID:            bindingMeta.BindingID,
			BindingRPMLimit:      bindingMeta.BindingRPMLimit,
			BindingTPMLimit:      bindingMeta.BindingTPMLimit,
			MaxParallelRequests:  bindingMeta.MaxParallelRequests,
			SelectionMode:        bindingMeta.SelectionMode,
			FallbackClass:        bindingfallback.ClassNormal,
			RequiredCapabilities: copyStrings(caps),
			MaxConcurrencyHint:   0,
			Reason:               reason,
			UpstreamModelID:      upstreamModelIDForPool(req.Model, metaByPool, poolGroupID),
		})
		seenPools[poolGroupID] = struct{}{}
	}
	fallbackPhases := r.compileFallbackPhases(req.Model, caps, metaByPool, classCandidates)
	routerPolicyVersion := r.SnapshotVersion
	if len(fallbackPhases) > 0 {
		routerPolicyVersion = fallbackClassPolicyVersion
	}

	return RoutePlan{
		Attempts:            attempts,
		FallbackPhases:      fallbackPhases,
		AttemptBudget:       budget,
		RetryableEndClasses: copyStrings(retryablePreDeliveryEndClasses),
		SnapshotVersion:     stampSnapshot(req.Model.SnapshotVersion, routerPolicyVersion),
	}, nil
}

// compileFallbackPhases 按固定 class 顺序编译非 normal 候选。每类只从最高
// Priority 段按 Weight 选出一个 binding，因此目标子预算始终为 1。
func (r *DefaultRouter) compileFallbackPhases(
	model ResolvedModel,
	caps []string,
	metadata map[int64]PoolCandidateMeta,
	classCandidates map[bindingfallback.Class][]int64,
) []FallbackPhasePlan {
	var phases []FallbackPhasePlan
	for _, class := range bindingfallback.FallbackClasses() {
		ordered := r.weightedPoolCandidateOrder(classCandidates[class], metadata)
		if len(ordered) == 0 {
			continue
		}
		poolGroupID := ordered[0]
		bindingMeta := metadata[poolGroupID]
		attempt := AttemptPlan{
			Index:                0,
			PoolGroupID:          poolGroupID,
			BindingID:            bindingMeta.BindingID,
			BindingRPMLimit:      bindingMeta.BindingRPMLimit,
			BindingTPMLimit:      bindingMeta.BindingTPMLimit,
			MaxParallelRequests:  bindingMeta.MaxParallelRequests,
			SelectionMode:        bindingMeta.SelectionMode,
			FallbackClass:        class,
			RequiredCapabilities: copyStrings(caps),
			MaxConcurrencyHint:   0,
			Reason:               "binding_fallback_" + string(class),
			UpstreamModelID:      upstreamModelIDForPool(model, metadata, poolGroupID),
		}
		phases = append(phases, FallbackPhasePlan{
			FallbackClass: class,
			Attempts:      []AttemptPlan{attempt},
			AttemptBudget: 1,
		})
	}
	return phases
}

func newDefaultRouterRand() *rand.Rand {
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

// weightedPoolCandidateOrder 始终复制输入。缺元数据候选保留为原位单例；
// 只有连续、元数据完整且 Priority 相等的段会发生交换。
func (r *DefaultRouter) weightedPoolCandidateOrder(candidates []int64, metadata map[int64]PoolCandidateMeta) []int64 {
	out := append([]int64(nil), candidates...)
	if len(out) < 2 || len(metadata) == 0 {
		return out
	}

	// 一次规划的全部随机抽取共用同一临界区，既避免 rand 数据竞争，也避免
	// 同一次无放回排列的随机序列被另一请求穿插。
	r.randMu.Lock()
	defer r.randMu.Unlock()
	if r.rand == nil {
		// 保持 DefaultRouter 零值构造的防御性可用；标准生产接线走构造器。
		r.rand = newDefaultRouterRand()
	}

	for start := 0; start < len(out); {
		firstMeta, ok := metadata[out[start]]
		if !ok {
			start++
			continue
		}

		end := start + 1
		for end < len(out) {
			nextMeta, found := metadata[out[end]]
			if !found || nextMeta.Priority != firstMeta.Priority {
				break
			}
			end++
		}
		if end-start > 1 {
			r.weightedShufflePoolSegment(out[start:end], metadata)
		}
		start = end
	}
	return out
}

// weightedShufflePoolSegment 对同一 Priority 段生成加权无放回排列。
// 每确定一个位置，就只在尚未放置的候选中按剩余权重抽取。
func (r *DefaultRouter) weightedShufflePoolSegment(candidates []int64, metadata map[int64]PoolCandidateMeta) {
	for target := 0; target < len(candidates)-1; target++ {
		selected := target
		var totalWeight int64
		for index := target; index < len(candidates); index++ {
			weight := normalizedPoolCandidateWeight(metadata[candidates[index]].Weight)
			totalWeight += weight
			if r.rand.Int63n(totalWeight) < weight {
				selected = index
			}
		}
		candidates[target], candidates[selected] = candidates[selected], candidates[target]
	}
}

func normalizedPoolCandidateWeight(weight int32) int64 {
	if weight <= 0 {
		return 1
	}
	return int64(weight)
}

func attemptBudgetForPools(poolCount int) int {
	if poolCount <= 0 {
		return 0
	}
	if poolCount == 1 {
		return 2
	}
	return 3
}

func poolMetadataByGroup(metadata []PoolCandidateMeta) map[int64]PoolCandidateMeta {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[int64]PoolCandidateMeta, len(metadata))
	for _, meta := range metadata {
		if meta.PoolGroupID == 0 {
			continue
		}
		if _, exists := out[meta.PoolGroupID]; !exists {
			out[meta.PoolGroupID] = meta
		}
	}
	return out
}

// poolCandidatesByFallbackClass 只按 class 分桶并保持原相对顺序。Priority 与
// Weight 随后在各自桶内由既有算法处理，phase 之间从不比较两者。
func poolCandidatesByFallbackClass(
	candidates []int64,
	metadata map[int64]PoolCandidateMeta,
) (map[bindingfallback.Class][]int64, error) {
	out := make(map[bindingfallback.Class][]int64, len(bindingfallback.FallbackClasses())+1)
	for _, poolGroupID := range candidates {
		class := bindingfallback.ClassNormal
		if meta, ok := metadata[poolGroupID]; ok {
			class = bindingfallback.NormalizeClass(string(meta.FallbackClass))
		}
		if !bindingfallback.IsKnownClass(class) {
			return nil, &PlanError{
				Code:    "invalid_fallback_class",
				Message: "PoolCandidateMeta.FallbackClass is outside the binding contract",
			}
		}
		out[class] = append(out[class], poolGroupID)
	}
	return out, nil
}

func upstreamModelIDForPool(model ResolvedModel, metadata map[int64]PoolCandidateMeta, poolGroupID int64) string {
	if meta, ok := metadata[poolGroupID]; ok && meta.ProviderModelID != "" {
		return meta.ProviderModelID
	}
	return model.ProviderModelID
}

// stampSnapshot 把 Registry stamp（例如 "registry:42:7"）与 Router 策略
// 版本（例如 "v0.2-binding-weighted"）拼接成 migration 0008 中记录的审计回放格式
// （registry:<tid>:<v>;router:<router_policy_v>）。当 model stamp 为空时
// 回退为 "registry:unknown"，从而保证格式永远不会被破坏。
func stampSnapshot(modelStamp, routerVersion string) string {
	if modelStamp == "" {
		modelStamp = "registry:unknown"
	}
	return modelStamp + ";router:" + routerVersion
}

// requiredCapabilities 把 chat 请求特性映射成【账号池选号】的 capability gate。
//
// 设计决定(2026-07-23):stream/tools/vision/json/audio-input 这些 chat 特性【不再】作为账号级选号门。
// 它们是模型/上游的属性,由 handler 与上游处理,选不到就失败转移;做成账号级 `capability_flags @> required`
// 门有两个真实缺陷:①账号默认空标记时流式请求(最常用)被误滤成 no_capacity,逼每个建号路径手工同步标记;
// ②账号能力是多模型并集→在不支持某特性的模型上放行(跨模型误授权)。故这里返回空:chat 特性不参与账号选号。
//
// 媒体特性(图片/embeddings/rerank/countTokens/audio/video)同样不做账号级能力门:modality 是
// 模型属性,由各媒体 handler 用请求模型的注册表能力(internal/modality.Supports)判定;账号侧
// 由选号 SQL 的 model_allow_list 清单门把关(媒体端点族要求显式命中,见 pool_accounts.sql
// require_model_listed)。capability_flags 不再参与任何请求路径的选号过滤。
func requiredCapabilities(_ RequestFeatures) []string {
	return nil
}

func copyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// 编译期断言：DefaultRouter 实现了 Router 接口。
var _ Router = (*DefaultRouter)(nil)
