package channelhealth

import "time"

func (s *Service) creditsExhaustedDecision(sig Signal, now time.Time) decision {
	if normalizeSignalClass(sig.Class) != SignalCreditsExhausted {
		return decision{}
	}
	until := now.Add(s.policy.CreditsExhaustedCooldown)
	if sig.RateLimitResetAt != nil && sig.RateLimitResetAt.After(now) {
		until = sig.RateLimitResetAt.UTC()
	}
	return decision{
		state:         StateCoolingDown,
		reason:        SignalCreditsExhausted,
		cooldownUntil: &until,
		confidence:    ConfidenceObserved,
		eventTypes:    []AuditEventType{EventDegraded, EventDisabled},
	}
}
