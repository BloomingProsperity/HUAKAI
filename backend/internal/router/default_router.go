package router

import (
	"context"
)

// DefaultRouter 生成保守路由计划：保留 registry 候选顺序，
// 将 attempt 上限压到 3，并把账号选择与健康 gate 留给 executor/pool 层。
type DefaultRouter struct {
	// SnapshotVersion identifies the Router policy at planning time;
	// concatenated onto the Registry stamp in plan.SnapshotVersion so
	// audit replay can reconstruct both layers from one column.
	SnapshotVersion string
}

// NewDefaultRouter 返回标准保守 planner。Registry 负责排序/过滤候选，
// Router 只把该顺序扩展成有界 attempt 序列。
func NewDefaultRouter() *DefaultRouter {
	return &DefaultRouter{SnapshotVersion: "v0.1-phase-c"}
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

	budget := attemptBudgetForPools(len(req.Model.PoolCandidates))
	caps := requiredCapabilities(req.Features)
	metaByPool := poolMetadataByGroup(req.Model.PoolMetadata)
	seenPools := make(map[int64]struct{}, len(req.Model.PoolCandidates))
	attempts := make([]AttemptPlan, 0, budget)
	for i := 0; i < budget; i++ {
		poolGroupID := req.Model.PoolCandidates[i%len(req.Model.PoolCandidates)]
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

// stampSnapshot concatenates the Registry stamp (e.g. "registry:42:7")
// with the Router policy version (e.g. "v0.1-phase-c") into the audit
// replay format documented in migration 0008
// (registry:<tid>:<v>;router:<router_policy_v>). Empty model stamp falls
// back to "registry:unknown" so the format is never broken.
func stampSnapshot(modelStamp, routerVersion string) string {
	if modelStamp == "" {
		modelStamp = "registry:unknown"
	}
	return modelStamp + ";router:" + routerVersion
}

// requiredCapabilities maps Features into the capability strings the
// Pool's intra-pool gate uses. Order is stable so audit comparisons work.
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

// Compile-time assertion that DefaultRouter implements Router.
var _ Router = (*DefaultRouter)(nil)
