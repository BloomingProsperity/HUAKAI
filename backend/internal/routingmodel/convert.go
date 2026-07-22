// Package routingmodel 把模型注册表投影转换为统一路由输入。
package routingmodel

import (
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

// FromRegistry 保留模型、池绑定和绑定级调度合同，供各协议入口共用。
func FromRegistry(resolved registry.Resolved) router.ResolvedModel {
	out := router.ResolvedModel{
		PublicAlias:     resolved.PublicAlias,
		InternalModelID: resolved.CanonicalModelID,
		ProviderModelID: resolved.ProviderModelID,
		ContextWindow:   resolved.ContextWindow,
		Capabilities:    resolved.Capabilities,
		PricingClass:    resolved.PricingClass,
		ProtocolFamily:  resolved.ProtocolFamily,
		PoolCandidates:  resolved.PoolCandidates,
		SnapshotVersion: resolved.SnapshotVersion,
	}
	defaultProviderModelID := resolved.DefaultProviderModelID
	if defaultProviderModelID == "" {
		defaultProviderModelID = resolved.ProviderModelID
	}
	for _, binding := range resolved.BindingMetadata {
		if binding.PoolGroupID == 0 {
			continue
		}
		providerModelID := defaultProviderModelID
		if binding.ProviderModelIDOverride != nil && *binding.ProviderModelIDOverride != "" {
			providerModelID = *binding.ProviderModelIDOverride
		}
		out.PoolMetadata = append(out.PoolMetadata, router.PoolCandidateMeta{
			PoolGroupID:         binding.PoolGroupID,
			ProviderModelID:     providerModelID,
			BindingID:           binding.BindingID,
			BindingRPMLimit:     int64Value(binding.RPMLimit),
			BindingTPMLimit:     int64Value(binding.TPMLimit),
			MaxParallelRequests: int64Value(binding.MaxParallelRequests),
			Priority:            binding.Priority,
			Weight:              binding.Weight,
			SelectionMode:       binding.SelectionMode,
			FallbackClass:       bindingfallback.NormalizeClass(binding.FallbackClass),
		})
	}
	return out
}

// ConstrainPool 把解析结果收紧到身份合同允许的单一池；不匹配时拒绝继续路由。
func ConstrainPool(resolved registry.Resolved, allowedPoolGroupID *int64) (registry.Resolved, bool) {
	if allowedPoolGroupID == nil {
		return resolved, true
	}
	poolGroupID := *allowedPoolGroupID
	if poolGroupID <= 0 {
		return registry.Resolved{}, false
	}
	filteredPools := make([]int64, 0, 1)
	for _, candidate := range resolved.PoolCandidates {
		if candidate == poolGroupID {
			filteredPools = append(filteredPools, candidate)
		}
	}
	if len(filteredPools) == 0 {
		return registry.Resolved{}, false
	}
	filteredBindings := make([]registry.BindingMetadata, 0, len(resolved.BindingMetadata))
	for _, binding := range resolved.BindingMetadata {
		if binding.PoolGroupID == poolGroupID {
			filteredBindings = append(filteredBindings, binding)
		}
	}
	if len(resolved.BindingMetadata) > 0 && len(filteredBindings) == 0 {
		return registry.Resolved{}, false
	}
	resolved.PoolCandidates = filteredPools
	resolved.BindingMetadata = filteredBindings
	return resolved, true
}

func int64Value(value *int32) int64 {
	if value == nil {
		return 0
	}
	return int64(*value)
}
