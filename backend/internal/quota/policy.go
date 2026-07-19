package quota

import (
	"context"
	"sort"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

type policyListStore interface {
	ListActivePolicies(ctx context.Context, filter PolicyFilter) ([]Policy, error)
}

// PolicyGroupKey 按 scope kind、模型选择器与 metric 归组,便于运营面解释 mode 分类。
type PolicyGroupKey struct {
	ScopeKind     ScopeKind
	ModelSelector string
	Metric        Metric
}

// PolicyModeGroup 保存同一 scope kind/metric 下的策略模式分类。
type PolicyModeGroup struct {
	Enforce     []Policy
	Observe     []Policy
	ManualFirst []Policy
}

// ResolvedPolicies 是 Reserve 使用的策略解析结果。
type ResolvedPolicies struct {
	Ordered []Policy
	Groups  map[PolicyGroupKey]PolicyModeGroup
}

// ResolvePolicies 读取当前激活策略, 再做一层精确 scope/metric 防御过滤和模式分类。
func ResolvePolicies(ctx context.Context, store policyListStore, tenantID int64, scopes []Scope, requestedModel string, metrics []Metric, atTime time.Time) (ResolvedPolicies, error) {
	if store == nil {
		return ResolvedPolicies{}, ErrStoreNotConfigured
	}
	policies, err := store.ListActivePolicies(ctx, PolicyFilter{
		TenantID:       tenantID,
		Scopes:         normalizeScopes(tenantID, scopes),
		RequestedModel: normalizeRequestedModel(requestedModel),
		Metrics:        normalizeMetrics(metrics),
		At:             atTime.UTC(),
		ForUpdate:      true,
	})
	if err != nil {
		return ResolvedPolicies{}, err
	}

	scopeSet := scopeTupleSet(tenantID, scopes)
	metricSet := metricSet(metrics)
	requestedModel = normalizeRequestedModel(requestedModel)
	resolved := ResolvedPolicies{
		Groups: make(map[PolicyGroupKey]PolicyModeGroup),
	}
	for _, policy := range policies {
		if policy.TenantID != tenantID || policy.Mode == ModeDisabled {
			continue
		}
		if !scopeSet[scopeTuple{kind: policy.Scope.Kind, id: normalizeScopeID(policy.Scope.Kind, policy.Scope.ID)}] {
			continue
		}
		if !metricSet[policy.Metric] {
			continue
		}
		policy.ModelSelector = normalizeModelSelector(policy.ModelSelector)
		if policy.ModelSelector != ModelSelectorAll && policy.ModelSelector != requestedModel {
			continue
		}
		policy.Scope.TenantID = tenantID
		policy.Scope.ID = normalizeScopeID(policy.Scope.Kind, policy.Scope.ID)
		policy.Window = resolvePolicyWindow(policy, atTime)
		resolved.Ordered = append(resolved.Ordered, policy)

		key := PolicyGroupKey{ScopeKind: policy.Scope.Kind, ModelSelector: policy.ModelSelector, Metric: policy.Metric}
		group := resolved.Groups[key]
		switch policy.Mode {
		case ModeEnforce:
			group.Enforce = append(group.Enforce, policy)
		case ModeObserve:
			group.Observe = append(group.Observe, policy)
		case ModeManualFirst:
			group.ManualFirst = append(group.ManualFirst, policy)
		}
		resolved.Groups[key] = group
	}

	sort.SliceStable(resolved.Ordered, func(i, j int) bool {
		left, right := resolved.Ordered[i], resolved.Ordered[j]
		if leftScope, rightScope := scopeLockOrder(left.Scope.Kind), scopeLockOrder(right.Scope.Kind); leftScope != rightScope {
			return leftScope < rightScope
		}
		if left.Scope.ID != right.Scope.ID {
			return left.Scope.ID < right.Scope.ID
		}
		if leftMetric, rightMetric := metricOrder(left.Metric), metricOrder(right.Metric); leftMetric != rightMetric {
			return leftMetric < rightMetric
		}
		if left.ModelSelector != right.ModelSelector {
			if left.ModelSelector == ModelSelectorAll {
				return true
			}
			if right.ModelSelector == ModelSelectorAll {
				return false
			}
			return left.ModelSelector < right.ModelSelector
		}
		if left.Priority != right.Priority {
			return left.Priority < right.Priority
		}
		return left.ID < right.ID
	})
	return resolved, nil
}

func normalizeRequestedModel(model string) string {
	return registry.AliasNormalize(model)
}

func normalizeModelSelector(selector string) string {
	if selector == "" || selector == ModelSelectorAll {
		return ModelSelectorAll
	}
	return registry.AliasNormalize(selector)
}

type scopeTuple struct {
	kind ScopeKind
	id   string
}

func scopeTupleSet(tenantID int64, scopes []Scope) map[scopeTuple]bool {
	out := make(map[scopeTuple]bool, len(scopes)+1)
	for _, scope := range normalizeScopes(tenantID, scopes) {
		out[scopeTuple{kind: scope.Kind, id: scope.ID}] = true
	}
	return out
}

func metricSet(metrics []Metric) map[Metric]bool {
	normalized := normalizeMetrics(metrics)
	out := make(map[Metric]bool, len(normalized))
	for _, metric := range normalized {
		out[metric] = true
	}
	return out
}

func normalizeScopes(tenantID int64, scopes []Scope) []Scope {
	out := make([]Scope, 0, len(scopes))
	seen := map[scopeTuple]bool{}
	add := func(scope Scope) {
		if scope.Kind == "" {
			return
		}
		scope.TenantID = tenantID
		scope.ID = normalizeScopeID(scope.Kind, scope.ID)
		key := scopeTuple{kind: scope.Kind, id: scope.ID}
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, scope)
	}
	for _, scope := range scopes {
		add(scope)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if left, right := scopeLockOrder(out[i].Kind), scopeLockOrder(out[j].Kind); left != right {
			return left < right
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func normalizeMetrics(metrics []Metric) []Metric {
	if len(metrics) == 0 {
		return []Metric{MetricRequests, MetricCostUSD, MetricTokensEstimated, MetricConcurrency}
	}
	out := make([]Metric, 0, len(metrics))
	seen := map[Metric]bool{}
	for _, metric := range metrics {
		if metric == "" || seen[metric] {
			continue
		}
		seen[metric] = true
		out = append(out, metric)
	}
	return out
}

func scopeLockOrder(kind ScopeKind) int {
	switch kind {
	case ScopeGlobal:
		return 0
	case ScopeUser:
		return 1
	case ScopeAPIKey:
		return 2
	case ScopeChannel:
		return 3
	case ScopePoolGroup:
		return 4
	default:
		return 100
	}
}

func metricOrder(metric Metric) int {
	switch metric {
	case MetricRequests:
		return 0
	case MetricCostUSD:
		return 1
	case MetricTokensEstimated:
		return 2
	case MetricConcurrency:
		return 3
	default:
		return 100
	}
}

func resolvePolicyWindow(policy Policy, at time.Time) Window {
	if policy.Window.Kind == WindowManual {
		start := policy.ValidFrom.UTC()
		if start.IsZero() {
			start = manualWindowStart
		}
		policy.Window.Start = start
		policy.Window.End = manualWindowEnd
		return policy.Window
	}
	start, end, ok := ComputeWindow(policy.Window.Kind, policy.Window.Seconds, at)
	if ok {
		policy.Window.Start = start
		policy.Window.End = end
		return policy.Window
	}
	if policy.Window.Kind == WindowNone {
		start = policy.ValidFrom.UTC()
		if start.IsZero() {
			start = manualWindowStart
		}
		policy.Window.Start = start
		policy.Window.End = manualWindowEnd
	}
	return policy.Window
}
