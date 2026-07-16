package channelhealth

import (
	"context"
	"errors"
	"strings"
)

func (s *Service) ClearRateLimitByProviderAccount(ctx context.Context, tenantID, providerAccountID int64, actorID string) (Record, bool, error) {
	if tenantID <= 0 {
		return Record{}, false, errors.New("tenant_id must be positive")
	}
	if providerAccountID <= 0 {
		return Record{}, false, errors.New("provider_account_id must be positive")
	}
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return Record{}, false, errors.New("actor_id is required")
	}
	changed := false
	rec, err := s.withMutation(ctx, func(tx *Service) (Record, error) {
		current, err := tx.store.LatestByProviderAccount(ctx, tenantID, providerAccountID)
		if err != nil {
			return Record{}, err
		}
		if current.State != StateCoolingDown || !isRateLimitReason(current.ReasonClass) {
			return current, nil
		}

		now := tx.clock.Now()
		prev := current.State
		current.SampleWindow = clearRateLimitSamples(current.SampleWindow)
		if latest, ok := latestSignalSample(current.SampleWindow); ok {
			current.LastSignalClass = latest.Class
			at := latest.At.UTC()
			current.LastSignalAt = &at
		} else {
			current.LastSignalClass = SignalNone
			current.LastSignalAt = nil
		}
		current.State = StateRamping
		current.ReasonClass = SignalNone
		current.Confidence = ConfidenceObserved
		current.CooldownUntil = nil
		current.RampStagePct = 1
		current.RampStartedAt = &now
		current.RecoveryBlockedReason = ""
		current.StateEnteredAt = now
		current.LastTransitionAt = now
		current.PolicyVersion = tx.policy.Version
		current.UpdatedAt = now
		current, err = tx.store.UpsertRecord(ctx, current)
		if err != nil {
			return Record{}, err
		}
		if err := tx.emitTransitionEvents(ctx, prev, current, "", actorID, decision{
			eventTypes: []AuditEventType{EventManualOverride, EventRampStarted},
		}); err != nil {
			return Record{}, err
		}
		changed = true
		return current, nil
	})
	return rec, changed, err
}

func isRateLimitReason(reason SignalClass) bool {
	value := string(normalizeSignalClass(reason))
	return value == string(SignalRateLimit) || strings.HasPrefix(value, string(SignalRateLimit)+"_")
}
