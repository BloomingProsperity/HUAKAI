package pricingeval

import "github.com/BloomingProsperity/HUAKAI/internal/billingdsl"

func evalInput(usage Usage) billingdsl.EvalInput {
	cacheCreation := usage.CacheCreationTokens
	if cacheCreation == 0 {
		cacheCreation = usage.CacheCreation5mTokens + usage.CacheCreation1hTokens
	}
	return billingdsl.EvalInput{
		InputTokens:         usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		CacheCreationTokens: cacheCreation,
		CacheReadTokens:     usage.CacheReadTokens,
	}
}

func (f FlatRateFallback) billingdsl() billingdsl.FlatRateFallback {
	return billingdsl.FlatRateFallback{
		Input:            f.Input,
		Output:           f.Output,
		CacheCreation:    f.CacheCreation,
		CacheRead:        f.CacheRead,
		Multiplier:       f.Multiplier,
		HasInput:         f.HasInput,
		HasOutput:        f.HasOutput,
		HasCacheCreation: f.HasCacheCreation,
		HasCacheRead:     f.HasCacheRead,
	}
}

func tieredSnapshot(version string) string {
	if version == "" {
		return "tiered"
	}
	return "tiered:v" + version
}
