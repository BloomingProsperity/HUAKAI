// R6 helper: bridge Classification → UsageRecordDraft routing-reason + end-class.
// Spec: docs/specs/rate-limiting.md §A13 / DR-009 §1 Q1 / F-GW-002 Phase D.
//
// Synthesis of two parallel-draft lanes (CLAUDE.md #10 + 2026-05-04 directive).
// Synthesis notes: docs/process/plans/2026-05-04-r6-wire-codeparallel-synthesis.md.
package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
)

// RoutingReasonPayload is the JSON schema written to UsageRecordDraft.RoutingReason.
// Field shape per §A13 audit payload contract; ordering preserved for
// stable on-disk format. All values are strings (Go marshals `type X string` as
// string) which keeps the wire format vendor-agnostic.
type RoutingReasonPayload struct {
	RuleID        string `json:"rule_id"`
	RuleVersion   int    `json:"rule_version"`
	ErrorClass    string `json:"error_class"`
	Tier          string `json:"tier"`
	RetryAction   string `json:"retry_action"`
	FsmTransition string `json:"fsm_transition"`
	RetryAfterMs  int64  `json:"retry_after_ms"`
	Confidence    string `json:"confidence"`
}

// streamEndClassForErrorClass maps the 12 A13 ErrorClass values to F-GW-002
// StreamEndClass. A switch (rather than a map) lets `go vet` flag a missing
// case if a new ErrorClass is added without updating the mapping.
func streamEndClassForErrorClass(class ErrorClass) StreamEndClass {
	switch class {
	case ErrorClassOAuthInvalidGrant,
		ErrorClassTokenRevoked,
		ErrorClassKYCRequired,
		ErrorClassOrgDisabled,
		ErrorClassWorkspaceDeactivated,
		ErrorClassCreditExhausted:
		return UpstreamAuthFailure
	case ErrorClassRateLimited:
		return UpstreamRateLimit
	case ErrorClassServerError, ErrorClassOverloaded:
		return UpstreamError5xx
	case ErrorClassPlatformPolicy, ErrorClassRequestTooLarge:
		// 413 request_too_large is a 4xx-class client error; same end-class.
		return UpstreamError4xx
	case ErrorClassNetworkTimeout, ErrorClassUpstreamTimeout:
		// upstream_timeout (504) and network_timeout (gateway-side) both map to
		// the inter-event timeout end-class for retry-budget accounting.
		return InterEventTimeout
	default:
		return UnknownTermination
	}
}

// ApplyClassificationToDraft writes Classification metadata into a UsageRecordDraft:
//
//  1. Serializes a RoutingReasonPayload JSON blob into d.RoutingReason.
//  2. Maps c.Class to a StreamEndClass and writes d.EndClass — but ONLY when
//     d.EndClass is currently the zero value ("") or UnknownTermination.
//     Prior determinations by the forwarder (e.g. ClientDisconnect, FirstTokenTimeout)
//     are preserved.
//
// nil draft is a silent no-op so that defensive callers do not have to nil-check.
func ApplyClassificationToDraft(d *UsageRecordDraft, c Classification) {
	if d == nil {
		return
	}

	payload := RoutingReasonPayload{
		RuleID:        c.RuleID,
		RuleVersion:   c.RuleVersion,
		ErrorClass:    string(c.Class),
		Tier:          string(c.Tier),
		RetryAction:   string(c.RetryAction),
		FsmTransition: string(c.FsmTransition),
		RetryAfterMs:  c.RetryAfterMs,
		Confidence:    string(c.Confidence),
	}
	if encoded, err := json.Marshal(payload); err == nil {
		d.RoutingReason = encoded
	}

	if d.EndClass == "" || d.EndClass == UnknownTermination {
		d.EndClass = streamEndClassForErrorClass(c.Class)
	}
}

// ClassifyAndApply is the one-shot combinator: Classify the upstream response
// and apply to the draft. Returns the Classification (so callers can act on it
// for retry / cooldown decisions) plus any Classify error.
//
// Returns an error without mutating d when:
//   - d is nil
//   - Classify returns an error (e.g. negative status)
func ClassifyAndApply(d *UsageRecordDraft, httpStatus int, headers http.Header, body []byte, provider string) (Classification, error) {
	if d == nil {
		return Classification{}, errors.New("gateway: ClassifyAndApply called with nil draft")
	}

	c, err := Classify(httpStatus, headers, body, provider)
	if err != nil {
		return Classification{}, err
	}
	ApplyClassificationToDraft(d, c)
	return c, nil
}
