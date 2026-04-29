package pool

import (
	"encoding/json"
	"time"
)

type RoutingLayer string

const (
	RoutingLayerRoutingAffinity   RoutingLayer = "routing_affinity"
	RoutingLayerStickyWithinRoute RoutingLayer = "sticky_within_routing"
	RoutingLayerStickyStandalone  RoutingLayer = "sticky_standalone"
	RoutingLayerFresh             RoutingLayer = "fresh"
	RoutingLayerForced            RoutingLayer = "forced"
	RoutingLayerFallbackQueue     RoutingLayer = "fallback_queue"
)

type routingReason struct {
	SelectionLayer             RoutingLayer                 `json:"selection_layer"`
	AffinityKeyClass           string                       `json:"affinity_key_class"`
	StickyBreakReason          *string                      `json:"sticky_break_reason"`
	CapabilityOutcome          string                       `json:"capability_outcome"`
	CandidateCountsByExclusion map[GateFailureReason]int    `json:"candidate_counts_by_exclusion"`
	RouteID                    int64                        `json:"route_id,omitempty"`
	ChannelID                  int64                        `json:"channel_id,omitempty"`
	PoolingGroupID             int64                        `json:"pooling_group_id"`
	ProviderAccountID          int64                        `json:"provider_account_id,omitempty"`
	ScoringPolicyVersion       string                       `json:"scoring_policy_version"`
	SignalContributions        map[string]float64           `json:"signal_contributions"`
	WaitAction                 *RoutingReasonWaitAction     `json:"wait_action"`
	RetryAttemptNumber         int                          `json:"retry_attempt_number"`
	PerRequestExclusionSummary []RoutingReasonExclusionItem `json:"per_request_exclusion_summary"`
	QuotaReservationID         int64                        `json:"quota_reservation_id,omitempty"`
	BillingLedgerClaimID       int64                        `json:"billing_ledger_claim_id,omitempty"`
	ForcedRouteOverrideActor   any                          `json:"forced_route_override_actor"`
}

type RoutingReasonWaitAction struct {
	EnteredQueueAt time.Time `json:"entered_queue_at"`
	ExitedQueueAt  time.Time `json:"exited_queue_at,omitempty"`
	ExitReason     string    `json:"exit_reason,omitempty"`
}

type RoutingReasonExclusionItem struct {
	AccountID int64             `json:"account_id"`
	Reason    GateFailureReason `json:"reason"`
}

type RoutingReasonBuilder struct {
	reason routingReason
}

func NewRoutingReasonBuilder(req SelectionRequest) *RoutingReasonBuilder {
	return &RoutingReasonBuilder{reason: routingReason{
		AffinityKeyClass:           affinityKeyClass(req),
		CapabilityOutcome:          "none_required",
		CandidateCountsByExclusion: map[GateFailureReason]int{},
		PoolingGroupID:             req.PoolGroupID,
		ScoringPolicyVersion:       "1.0",
		SignalContributions:        map[string]float64{},
		RetryAttemptNumber:         req.AttemptSeq,
		BillingLedgerClaimID:       req.ClaimID,
	}}
}

func (b *RoutingReasonBuilder) Layer(layer RoutingLayer) {
	b.reason.SelectionLayer = layer
}

func (b *RoutingReasonBuilder) Account(id int64) {
	b.reason.ProviderAccountID = id
}

func (b *RoutingReasonBuilder) GateFailure(accountID int64, reason GateFailureReason) {
	if reason == "" {
		return
	}
	b.reason.CandidateCountsByExclusion[reason]++
	b.reason.PerRequestExclusionSummary = append(b.reason.PerRequestExclusionSummary, RoutingReasonExclusionItem{AccountID: accountID, Reason: reason})
}

func (b *RoutingReasonBuilder) Wait(plan *WaitPlan) {
	if plan == nil {
		return
	}
	b.reason.SelectionLayer = RoutingLayerFallbackQueue
	b.reason.WaitAction = &RoutingReasonWaitAction{EnteredQueueAt: time.Now().UTC(), ExitReason: "queued"}
}

func (b *RoutingReasonBuilder) Merge(other *RoutingReasonBuilder) {
	if other == nil {
		return
	}
	for reason, count := range other.reason.CandidateCountsByExclusion {
		b.reason.CandidateCountsByExclusion[reason] += count
	}
	b.reason.PerRequestExclusionSummary = append(b.reason.PerRequestExclusionSummary, other.reason.PerRequestExclusionSummary...)
}

func (b *RoutingReasonBuilder) JSON() []byte {
	out, err := json.Marshal(b.reason)
	if err != nil {
		return []byte(`{"selection_layer":"fresh","routing_reason_error":"marshal_failed"}`)
	}
	return out
}

func affinityKeyClass(req SelectionRequest) string {
	if req.ContinuationKey != "" {
		return "continuation_marker"
	}
	if req.SessionHash != "" {
		return "session_id"
	}
	return "none"
}
