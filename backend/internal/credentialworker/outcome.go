package credentialworker

import "github.com/BloomingProsperity/HUAKAI/internal/auth"

type RefreshOutcome = auth.RefreshOutcome

const (
	OutcomeSuccess         RefreshOutcome = auth.OutcomeSuccess
	OutcomeAuthExpired     RefreshOutcome = auth.OutcomeAuthExpired
	OutcomeRateLimit       RefreshOutcome = auth.OutcomeRateLimit
	OutcomeRiskControl     RefreshOutcome = auth.OutcomeRiskControl
	OutcomeAccountDisabled RefreshOutcome = auth.OutcomeAccountDisabled
	OutcomeTransientError  RefreshOutcome = auth.OutcomeTransientError
	OutcomeUnknown         RefreshOutcome = auth.OutcomeUnknown
)

func ClassifyRefreshError(err error, vendor string, statusCode int) RefreshOutcome {
	return auth.ClassifyRefreshError(err, vendor, statusCode)
}
