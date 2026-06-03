package pricingeval

import (
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/billingdsl"
)

func isTieredPricingData(raw json.RawMessage) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false
	}
	if hasTieredMarker(obj) {
		return true
	}
	for _, key := range []string{"input", "output", "cache_creation", "cache_read", "tiers", "buckets"} {
		if _, ok := obj[key]; ok {
			return true
		}
	}
	return false
}

func hasTieredMarker(obj map[string]json.RawMessage) bool {
	raw, ok := obj["pricing_model"]
	if !ok {
		return false
	}
	var marker string
	if err := json.Unmarshal(raw, &marker); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(marker), "tiered")
}

func hasTieredBucket(spec billingdsl.ExpressionSpec) bool {
	return len(spec.Input) > 0 ||
		len(spec.Output) > 0 ||
		len(spec.CacheCreation) > 0 ||
		len(spec.CacheRead) > 0
}
