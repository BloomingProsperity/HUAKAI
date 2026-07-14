package router

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// DefaultRouter 生成保守路由计划：保留 registry 的 Priority 硬分层，
// 仅在连续同优先级段内按绑定 Weight 洗序，并把账号选择与健康 gate 留给
// executor/pool 层。
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
	poolCandidates := r.weightedPoolCandidateOrder(req.Model.PoolCandidates, metaByPool)
	budget := attemptBudgetForPools(len(poolCandidates))
	seenPools := make(map[int64]struct{}, len(poolCandidates))
	attempts := make([]AttemptPlan, 0, budget)
	for i := 0; i < budget; i++ {
		poolGroupID := poolCandidates[i%len(poolCandidates)]
		reason := "cross_pool_fallback"
		if i == 0 {
			reason = "primary"
		} else if _, seen := seenPools[poolGroupID]; seen {
			reason = "same_pool_account_failover"
		}
		attempts = append(attempts, AttemptPlan{
			Index:                i,
			PoolGroupID:          poolGroupID,
			RequiredCapabilities: copyStrings(caps),
			MaxConcurrencyHint:   0,
			Reason:               reason,
			UpstreamModelID:      upstreamModelIDForPool(req.Model, metaByPool, poolGroupID),
		})
		seenPools[poolGroupID] = struct{}{}
	}

	return RoutePlan{
		Attempts:            attempts,
		AttemptBudget:       budget,
		RetryableEndClasses: copyStrings(retryablePreDeliveryEndClasses),
		SnapshotVersion:     stampSnapshot(req.Model.SnapshotVersion, r.SnapshotVersion),
	}, nil
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

// requiredCapabilities 把 Features 映射成 Pool 的池内 gate 所使用的
// capability 字符串。顺序是稳定的，以便审计比对能正常工作。
func requiredCapabilities(f RequestFeatures) []string {
	caps := make([]string, 0, 5)
	if f.Stream {
		caps = append(caps, "stream")
	}
	if f.WantsToolUse {
		caps = append(caps, "tools")
	}
	if f.WantsVision {
		caps = append(caps, "vision")
	}
	if f.WantsJSON {
		caps = append(caps, "json")
	}
	if f.WantsAudio {
		caps = append(caps, "audio")
	}
	return caps
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
