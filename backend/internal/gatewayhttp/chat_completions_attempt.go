package gatewayhttp

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

type attemptInput struct {
	Plan             router.AttemptPlan
	AttemptSeq       int
	ExcludedAccounts map[int64]struct{}
	ReplayableBody   bool
	FinalAttempt     bool
}

type attemptOutcome struct {
	AttemptSeq       int
	Attempt          router.AttemptPlan
	AccountID        int64
	AcquisitionToken uuid.UUID
	Selection        *pool.SelectionResult

	Success         *attemptSuccess
	Failure         *classifiedAttemptFailure
	DeliveryStarted bool
	UsageDraft      gateway.UsageRecordDraft
	StreamAttempt   *billing.Attempt
}

type attemptSuccess struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Streamed   bool
}

type classifiedAttemptFailure struct {
	ClientStatus  int
	ClientCode    string
	ClientMessage string

	Classification gateway.Classification
	TransportClass gateway.TransportErrorClass
	// Decision 是 executor retry/failover 的单一事实来源。见综合稿 §3
	// override-1: 401 的 upstream_auth_failure 不在
	// RoutePlan.RetryableEndClasses 中，executor 必须同时检查
	// Decision.CountsAgainstAuthFailoverBudget。
	Decision gateway.AttemptRetryDecision

	DeliveredToClient bool
	AbortReason       string
	Cause             error
}

func classifiedFailureFromDecision(code, message string, classification gateway.Classification, decision gateway.AttemptRetryDecision, cause error) *classifiedAttemptFailure {
	return &classifiedAttemptFailure{
		ClientStatus:   decision.ClientStatus,
		ClientCode:     code,
		ClientMessage:  message,
		Classification: classification,
		TransportClass: decision.TransportClass,
		Decision:       decision,
		AbortReason:    decision.AbortReason,
		Cause:          cause,
	}
}
