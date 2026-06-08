package rate

import (
	"bytes"
	"encoding/json"
	"time"
)

// TempUnschedulableRule is the per-account error-ban rule stored as JSONB.
// Schema from sql/migrations/0004_rate_limiting.up.sql:
//
//	{ error_code, keywords[], duration_minutes, description }
//
// A rule matches when error_code equals the upstream status code AND
// at least one keyword (if any) appears as a case-insensitive substring
// of the response body. An empty keywords list means "any body".
type TempUnschedulableRule struct {
	ErrorCode       int      `json:"error_code"`
	Keywords        []string `json:"keywords"`
	DurationMinutes int      `json:"duration_minutes"`
	Description     string   `json:"description"`
}

// AccountErrorRulesProvider supplies per-account error-ban config to the
// rate service. Implementations may use an in-process cache. A nil provider
// is treated as "no rules" (zero-config no-op).
//
// The provider is responsible for applying both enable flags
// (temp_unschedulable_enabled and custom_error_codes_enabled): it returns
// empty slices for any feature that is disabled, so the caller never needs
// a separate enabled bool.
type AccountErrorRulesProvider interface {
	// GetAccountErrorRules returns the effective temp-unschedulable rules and
	// custom error codes for the given account, with both enable flags already
	// applied. Empty slices mean "feature off / no config" (no-op).
	GetAccountErrorRules(accountID int64) (rules []TempUnschedulableRule, customErrorCodes []int32)
}

const maxBodyBytesForMatch = 8 * 1024 // 8 KB cap — never log this slice

// ParseTempUnschedulableRules deserialises the raw JSONB bytes from the DB.
// Returns nil on empty or invalid input (treated as no rules).
func ParseTempUnschedulableRules(raw []byte) []TempUnschedulableRule {
	if len(raw) == 0 {
		return nil
	}
	var rules []TempUnschedulableRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil
	}
	return rules
}

// evalAccountErrorRules is a pure function that checks whether the upstream
// response matches any operator-configured ban-signal rule.
//
// Matching semantics (F-RATE-001 §1.6):
//  1. custom_error_codes: if statusCode is a member → StateTempUnsched / ReasonCustomErrorCode.
//  2. temp_unschedulable_rules: first rule whose error_code == statusCode AND
//     (keywords is empty OR any keyword is a case-insensitive substring of body) → match.
//
// Returns zero Decision (StateNoChange) when nothing matches.
// PURE: no I/O, no logging, no side effects.
func evalAccountErrorRules(
	statusCode int,
	respBody []byte,
	rules []TempUnschedulableRule,
	customErrorCodes []int32,
	durationFromRule func(minutes int) time.Duration,
	defaultCooldown time.Duration,
	now time.Time,
	disableCooling bool,
) Decision {
	// 1. Custom error codes (membership check).
	// Uses defaultCooldown so CooldownUntil is always set (never zero).
	for _, code := range customErrorCodes {
		if int(code) == statusCode {
			return makeTempUnschedDecision(defaultCooldown, now, disableCooling, ReasonCustomErrorCode)
		}
	}

	// Prepare a length-capped, lowercased copy of the body for substring matching.
	// This slice is for matching ONLY — never logged.
	body := respBody
	if len(body) > maxBodyBytesForMatch {
		body = body[:maxBodyBytesForMatch]
	}
	lowerBody := bytes.ToLower(body)

	// 2. Temp-unschedulable rules (first-match wins).
	for _, r := range rules {
		if r.ErrorCode != statusCode {
			continue
		}
		if matchesKeywords(lowerBody, r.Keywords) {
			dur := durationFromRule(r.DurationMinutes)
			return makeTempUnschedDecision(dur, now, disableCooling, ReasonTempUnschedRule)
		}
	}
	return Decision{}
}

// matchesKeywords returns true when keywords is empty (wildcard) or any
// keyword is found as a case-insensitive substring of lowerBody.
// lowerBody must already be lowercased by the caller.
func matchesKeywords(lowerBody []byte, keywords []string) bool {
	if len(keywords) == 0 {
		return true
	}
	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		if bytes.Contains(lowerBody, bytes.ToLower([]byte(kw))) {
			return true
		}
	}
	return false
}

func makeTempUnschedDecision(dur time.Duration, now time.Time, disableCooling bool, reason Reason) Decision {
	dec := Decision{
		StateChange:    StateTempUnsched,
		Reason:         reason,
		ShouldFailover: true,
	}
	if dur > 0 {
		dec.RetryAfterSeconds = durationSeconds(dur)
		if !disableCooling {
			dec.CooldownUntil = now.Add(dur).UTC()
		}
	}
	return dec
}
