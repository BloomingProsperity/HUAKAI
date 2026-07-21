package geminihttp

import "github.com/BloomingProsperity/HUAKAI/internal/router"

const countTokensCapability = "countTokens"

func hasCountTokensModelCapability(capabilities []string) bool {
	if len(capabilities) == 0 {
		return true
	}
	for _, capability := range capabilities {
		if capability == countTokensCapability {
			return true
		}
	}
	return false
}

func requireCountTokensCapability(plan *router.RoutePlan) {
	if plan == nil {
		return
	}
	for index := range plan.Attempts {
		plan.Attempts[index].RequiredCapabilities = appendCountTokensCapability(
			plan.Attempts[index].RequiredCapabilities,
			countTokensCapability,
		)
	}
	for phaseIndex := range plan.FallbackPhases {
		for attemptIndex := range plan.FallbackPhases[phaseIndex].Attempts {
			attempt := &plan.FallbackPhases[phaseIndex].Attempts[attemptIndex]
			attempt.RequiredCapabilities = appendCountTokensCapability(attempt.RequiredCapabilities, countTokensCapability)
		}
	}
}

func appendCountTokensCapability(capabilities []string, required string) []string {
	for _, capability := range capabilities {
		if capability == required {
			return capabilities
		}
	}
	return append(capabilities, required)
}
