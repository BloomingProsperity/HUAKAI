// Package gateway provider error normalization (A13 ERROR_RULES rule table).
// Spec: docs/specs/rate-limiting.md A13 / DR-009 1 Q1.
//
// This file is the synthesis of two parallel-draft implementations
// (Claude lane + Codex lane) per CLAUDE.md #10 + 2026-05-04 directive
// expanding parallel-draft to all code. Synthesis notes:
// docs/plans/2026-05-04-r6-codeparallel-synthesis.md.
//
// Hard floor (DR-009 6.6): the FSM must never auto-reach `disabled`
// on an `ambiguous` signal alone. Enforced structurally: `ambiguous`
// rules can only emit RetryActionCountedDisable / Cooldown / WarnOnly,
// never RetryActionPermanentDisable.
package gateway

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrorClass enumerates the 12 normalized error categories from A13.
type ErrorClass string

const (
	ErrorClassOAuthInvalidGrant    ErrorClass = "oauth_invalid_grant"
	ErrorClassTokenRevoked         ErrorClass = "token_revoked"
	ErrorClassKYCRequired          ErrorClass = "kyc_required"
	ErrorClassOrgDisabled          ErrorClass = "org_disabled"
	ErrorClassWorkspaceDeactivated ErrorClass = "workspace_deactivated"
	ErrorClassCreditExhausted      ErrorClass = "credit_exhausted"
	ErrorClassPlatformPolicy       ErrorClass = "platform_policy"
	ErrorClassRateLimited          ErrorClass = "upstream_rate_limited"
	ErrorClassOverloaded           ErrorClass = "upstream_overloaded"
	ErrorClassServerError          ErrorClass = "upstream_5xx"
	ErrorClassNetworkTimeout       ErrorClass = "network_timeout"
	ErrorClassUnknown              ErrorClass = "unknown_upstream"
)

// Confidence is a coarse signal-quality indicator carried in the Classification.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// DisableTier encodes DR-009 Q1: iron_clad = unambiguous proof of permanent
// invalidity (5 keywords); ambiguous = transient/unknown.
type DisableTier string

const (
	TierIronClad  DisableTier = "iron_clad"
	TierAmbiguous DisableTier = "ambiguous"
	TierNone      DisableTier = ""
)

// RetryAction is the prescribed action for the caller.
type RetryAction string

const (
	RetryActionPermanentDisable RetryAction = "permanent_disable"
	RetryActionCountedDisable   RetryAction = "counted_disable"
	RetryActionCooldown         RetryAction = "cooldown"
	RetryActionWarnOnly         RetryAction = "warn_only"
	RetryActionPassThrough      RetryAction = "pass_through"
)

// FsmTransition is the suggested A22 FSM target state. The classifier does NOT
// mutate FSM state; this field is a hint for the FSM caller.
type FsmTransition string

const (
	FsmTransitionDisabled   FsmTransition = "disabled"
	FsmTransitionDegraded   FsmTransition = "degraded"
	FsmTransitionCooling    FsmTransition = "cooling_down"
	FsmTransitionNoChange   FsmTransition = "no_transition"
	FsmTransitionManualOnly FsmTransition = "operator_review"
)

// HeaderMatch optionally constrains a rule on a response header.
type HeaderMatch struct {
	Name     string
	Equals   string
	Contains string
}

// ErrorRule is a single row in the ERROR_RULES table.
type ErrorRule struct {
	RuleID      string
	Version     int
	Priority    int    // ascending = higher priority
	Provider    string // "*" = wildcard
	HTTPStatus  string // "*" = wildcard, "5xx" = range, otherwise exact int
	BodyKeyword string // case-insensitive substring match; "" = no constraint
	HeaderMatch HeaderMatch
	Class       ErrorClass
	Action      RetryAction
	Tier        DisableTier
}

// Classification is the output of Classify(). It carries everything A22 (FSM)
// and A11 (audit) need without re-parsing the upstream response.
type Classification struct {
	Class         ErrorClass
	Confidence    Confidence
	RuleID        string
	RuleVersion   int
	Tier          DisableTier
	RetryAction   RetryAction
	FsmTransition FsmTransition
	RetryAfterMs  int64
}

// IronCladKeywords is the exactly-5 keyword set per DR-009 1 Q1.
// External callers (custom rule loaders, audit re-classification) should
// consult this set rather than hardcoding the list locally.
var IronCladKeywords = map[string]struct{}{
	"invalid_grant":         {},
	"identity verification": {},
	"org_disabled":          {},
	"token_revoked":         {},
	"deactivated_workspace": {},
}

// IsIronCladKeyword reports whether a keyword belongs to the exactly-5
// iron_clad set per DR-009 1 Q1 / synthesis 6.6.
func IsIronCladKeyword(keyword string) bool {
	_, ok := IronCladKeywords[strings.ToLower(strings.TrimSpace(keyword))]
	return ok
}

const (
	keywordInvalidGrant                = "invalid_grant"
	keywordIdentityVerification        = "identity verification"
	keywordOrgDisabled                 = "org_disabled"
	keywordTokenRevoked                = "token_revoked"
	keywordDeactivatedWorkspace        = "deactivated_workspace"
	keywordTokenInvalidated            = "token_invalidated"
	keywordCredit                      = "credit"
	keywordCreditBalance               = "credit balance"
	keywordValidation                  = "validation"
	keywordPermissionDenied            = "permission denied"
	keywordThrottling                  = "throttling"
	keywordThrottlingException         = "ThrottlingException"
	keywordServiceUnavailableException = "ServiceUnavailableException"
	keywordTimeout                     = "timeout"
)

// errorRules is the A13 rule table, evaluated in priority-then-specificity order.
var errorRules = []ErrorRule{
	// Priority 10 - iron_clad permanent signals (5 mandated + 1 alias)
	{RuleID: "R-001", Version: 1, Priority: 10, Provider: "*", HTTPStatus: "401",
		BodyKeyword: keywordInvalidGrant, Class: ErrorClassOAuthInvalidGrant,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	{RuleID: "R-002", Version: 1, Priority: 10, Provider: "*", HTTPStatus: "400",
		BodyKeyword: keywordIdentityVerification, Class: ErrorClassKYCRequired,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	{RuleID: "R-003", Version: 1, Priority: 10, Provider: "*", HTTPStatus: "400",
		BodyKeyword: keywordOrgDisabled, Class: ErrorClassOrgDisabled,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	{RuleID: "R-004", Version: 1, Priority: 10, Provider: "*", HTTPStatus: "401",
		BodyKeyword: keywordTokenRevoked, Class: ErrorClassTokenRevoked,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	// Drift D3 (docs/reference_delta/2026-05-06/vendor-drift-audit.md):
	// OpenAI docs no longer publish 402 for billing/deactivation. Keep the
	// deactivated_workspace keyword as an OpenAI-scoped defensive match across
	// statuses. Fetch URLs: https://developers.openai.com/api/docs/guides/error-codes
	// and https://platform.claude.com/docs/en/api/errors (fetched 2026-05-06).
	{RuleID: "R-005", Version: 2, Priority: 10, Provider: "openai", HTTPStatus: "*",
		BodyKeyword: keywordDeactivatedWorkspace, Class: ErrorClassWorkspaceDeactivated,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	// R-006 token_invalidated is treated as token_revoked equivalent (vendor synonym).
	{RuleID: "R-006", Version: 1, Priority: 10, Provider: "*", HTTPStatus: "401",
		BodyKeyword: keywordTokenInvalidated, Class: ErrorClassTokenRevoked,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},

	// Priority 20 - credit / billing iron_clad.
	// Drift D3 scopes legacy 402/400 credit keywords to Anthropic only; OpenAI
	// current docs use 429 for rate_limit_error and do not document 402.
	{RuleID: "R-007", Version: 2, Priority: 20, Provider: "anthropic", HTTPStatus: "402",
		BodyKeyword: keywordCredit, Class: ErrorClassCreditExhausted,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	{RuleID: "R-008", Version: 2, Priority: 20, Provider: "anthropic", HTTPStatus: "400",
		BodyKeyword: keywordCreditBalance, Class: ErrorClassCreditExhausted,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},

	// Priority 30 - generic 401 (no keyword): permanent disable per spec
	{RuleID: "R-009", Version: 1, Priority: 30, Provider: "*", HTTPStatus: "401",
		Class:  ErrorClassOAuthInvalidGrant,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},

	// Priority 35 - Gemini-specific 403 with permission_denied (counted, not iron_clad)
	{RuleID: "R-017", Version: 1, Priority: 35, Provider: "gemini", HTTPStatus: "403",
		BodyKeyword: keywordPermissionDenied, Class: ErrorClassPlatformPolicy,
		Action: RetryActionCountedDisable, Tier: TierAmbiguous},

	// Priority 40 - 403 platform-specific
	{RuleID: "R-010", Version: 1, Priority: 40, Provider: "openai", HTTPStatus: "403",
		Class:  ErrorClassPlatformPolicy,
		Action: RetryActionCountedDisable, Tier: TierAmbiguous},
	{RuleID: "R-011", Version: 1, Priority: 40, Provider: "anthropic", HTTPStatus: "403",
		BodyKeyword: keywordValidation, Class: ErrorClassPlatformPolicy,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	{RuleID: "R-012", Version: 1, Priority: 40, Provider: "*", HTTPStatus: "403",
		Class:  ErrorClassPlatformPolicy,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},

	// Priority 45 - Bedrock drift D2 (2026-05-06 vendor audit):
	// 429 ThrottlingException is quota/rate limiting; 503
	// ServiceUnavailableException is capacity/overload, not rate limiting.
	// Fetch URL: https://docs.aws.amazon.com/bedrock/latest/userguide/troubleshooting-api-error-codes.html
	{RuleID: "R-018", Version: 2, Priority: 45, Provider: "bedrock", HTTPStatus: "429",
		BodyKeyword: keywordThrottlingException, Class: ErrorClassRateLimited,
		Action: RetryActionCooldown, Tier: TierAmbiguous},
	{RuleID: "R-020", Version: 1, Priority: 45, Provider: "bedrock", HTTPStatus: "503",
		BodyKeyword: keywordServiceUnavailableException, Class: ErrorClassOverloaded,
		Action: RetryActionCooldown, Tier: TierAmbiguous},

	// Priority 50 - rate limit and overload (always ambiguous, cooldown only)
	{RuleID: "R-013", Version: 1, Priority: 50, Provider: "*", HTTPStatus: "429",
		Class:  ErrorClassRateLimited,
		Action: RetryActionCooldown, Tier: TierAmbiguous},
	{RuleID: "R-014", Version: 1, Priority: 50, Provider: "*", HTTPStatus: "529",
		Class:  ErrorClassOverloaded,
		Action: RetryActionCooldown, Tier: TierAmbiguous},

	// Priority 55 - synthesized network timeout (status 0 + body hint)
	{RuleID: "R-019", Version: 1, Priority: 55, Provider: "*", HTTPStatus: "0",
		BodyKeyword: keywordTimeout, Class: ErrorClassNetworkTimeout,
		Action: RetryActionCooldown, Tier: TierAmbiguous},

	// Priority 60 - generic 5xx (warn only)
	{RuleID: "R-015", Version: 1, Priority: 60, Provider: "*", HTTPStatus: "5xx",
		Class:  ErrorClassServerError,
		Action: RetryActionWarnOnly, Tier: TierAmbiguous},

	// Priority 70 - wildcard catch-all
	{RuleID: "R-016", Version: 1, Priority: 70, Provider: "*", HTTPStatus: "*",
		Class:  ErrorClassUnknown,
		Action: RetryActionPassThrough, Tier: TierNone},
}

// ErrNoMatchingRule is returned by Classify when no rule (including the wildcard
// catch-all) matches. In practice unreachable because R-016 matches everything.
var ErrNoMatchingRule = errors.New("no matching error normalization rule")

// Classify evaluates the ERROR_RULES table against an upstream response and
// returns a Classification. The classifier never mutates state; FsmTransition
// is a hint, the FSM caller (A22) owns the actual transition.
//
// httpStatus 0 represents a synthesized response (no upstream reply, e.g.
// network timeout) - combined with BodyKeyword "timeout" matches R-019.
func Classify(httpStatus int, headers http.Header, body []byte, provider string) (Classification, error) {
	if httpStatus < 0 {
		return Classification{}, errors.New("http status must be non-negative")
	}

	rule, ok := matchRule(httpStatus, headers, body, provider)
	if !ok {
		return Classification{}, ErrNoMatchingRule
	}

	return Classification{
		Class:         rule.Class,
		Confidence:    confidenceForTier(rule.Tier),
		RuleID:        rule.RuleID,
		RuleVersion:   rule.Version,
		Tier:          rule.Tier,
		RetryAction:   rule.Action,
		FsmTransition: transitionFor(rule.Action, rule.Tier),
		RetryAfterMs:  retryAfterMillis(headers),
	}, nil
}

func matchRule(httpStatus int, headers http.Header, body []byte, provider string) (ErrorRule, bool) {
	normalizedProvider := normalizeProvider(provider)
	normalizedBody := strings.ToLower(string(body))

	var best ErrorRule
	found := false
	for _, rule := range errorRules {
		if !providerMatches(rule.Provider, normalizedProvider) {
			continue
		}
		if !statusMatches(rule.HTTPStatus, httpStatus) {
			continue
		}
		if rule.BodyKeyword != "" && !strings.Contains(normalizedBody, strings.ToLower(rule.BodyKeyword)) {
			continue
		}
		if !headerMatches(headers, rule.HeaderMatch) {
			continue
		}
		if !found || betterRule(rule, best, normalizedProvider) {
			best = rule
			found = true
		}
	}
	return best, found
}

func normalizeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "*":
		return "*"
	case "anthropic_messages":
		return "anthropic"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func providerMatches(ruleProvider, provider string) bool {
	ruleProvider = strings.ToLower(strings.TrimSpace(ruleProvider))
	return ruleProvider == "*" || ruleProvider == provider
}

func statusMatches(ruleStatus string, httpStatus int) bool {
	switch strings.ToLower(strings.TrimSpace(ruleStatus)) {
	case "*":
		return true
	case "5xx":
		return httpStatus >= 500 && httpStatus <= 599
	case "0":
		return httpStatus == 0
	default:
		want, err := strconv.Atoi(ruleStatus)
		return err == nil && want == httpStatus
	}
}

func headerMatches(headers http.Header, match HeaderMatch) bool {
	if match.Name == "" {
		return true
	}
	values, ok := headers[http.CanonicalHeaderKey(match.Name)]
	if !ok {
		values = headers[match.Name]
	}
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		normalized := strings.ToLower(value)
		if match.Equals != "" && normalized == strings.ToLower(match.Equals) {
			return true
		}
		if match.Contains != "" && strings.Contains(normalized, strings.ToLower(match.Contains)) {
			return true
		}
	}
	return match.Equals == "" && match.Contains == ""
}

func betterRule(candidate, current ErrorRule, provider string) bool {
	if candidate.Priority != current.Priority {
		return candidate.Priority < current.Priority
	}
	if candidate.Version != current.Version {
		return candidate.Version > current.Version
	}
	return providerSpecificity(candidate.Provider, provider) > providerSpecificity(current.Provider, provider)
}

func providerSpecificity(ruleProvider, provider string) int {
	if strings.EqualFold(ruleProvider, provider) && provider != "*" {
		return 1
	}
	return 0
}

func confidenceForTier(tier DisableTier) Confidence {
	switch tier {
	case TierIronClad:
		return ConfidenceHigh
	case TierAmbiguous:
		return ConfidenceMedium
	default:
		return ConfidenceLow
	}
}

// transitionFor enforces the DR-009 6.6 hard floor structurally:
// ambiguous-tier rules cannot reach FsmTransitionDisabled regardless of action.
func transitionFor(action RetryAction, tier DisableTier) FsmTransition {
	switch action {
	case RetryActionPermanentDisable:
		if tier == TierIronClad {
			return FsmTransitionDisabled
		}
		return FsmTransitionManualOnly
	case RetryActionCountedDisable, RetryActionWarnOnly:
		return FsmTransitionDegraded
	case RetryActionCooldown:
		return FsmTransitionCooling
	default:
		return FsmTransitionNoChange
	}
}

// retryAfterMillis parses RFC 7231 Retry-After (delta-seconds OR HTTP-date).
func retryAfterMillis(headers http.Header) int64 {
	if headers == nil {
		return 0
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return int64(seconds * 1000)
	}
	if when, err := http.ParseTime(raw); err == nil {
		delta := time.Until(when)
		if delta <= 0 {
			return 0
		}
		return delta.Milliseconds()
	}
	return 0
}
