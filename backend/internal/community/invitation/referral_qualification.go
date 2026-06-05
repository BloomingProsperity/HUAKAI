package invitation

import (
	"context"
	"time"
)

type referralQualificationStore interface {
	qualifyPendingReferral(context.Context, int64, int64, int64, time.Time) error
}

type referralRewardQualificationStore interface {
	qualifyPendingReferralWithReward(context.Context, qualifyReferralInput) error
}

type qualifyReferralInput struct {
	TenantID        int64
	RefereeUserID   int64
	BillingEventID  int64
	RewardUSDMicros int64
	QualifiedAt     time.Time
	TierThresholds  referralTierThresholds
}

type referralTierThresholds struct {
	Silver   int
	Gold     int
	Platinum int
}

type referralRewardConfig struct {
	RewardUSDMicros int64
	TierThresholds  referralTierThresholds
}

func (s *Service) QualifyPendingReferral(ctx context.Context, tenantID, refereeUserID, billingEventID int64) error {
	if tenantID <= 0 || refereeUserID <= 0 || billingEventID <= 0 {
		return ErrInvalidInput
	}
	if s == nil || s.store == nil {
		return ErrStoreNotConfigured
	}
	cfg, err := referralRewardConfigFromEnv()
	if err != nil {
		return err
	}
	if store, ok := s.store.(referralRewardQualificationStore); ok {
		return store.qualifyPendingReferralWithReward(ctx, qualifyReferralInput{
			TenantID:        tenantID,
			RefereeUserID:   refereeUserID,
			BillingEventID:  billingEventID,
			RewardUSDMicros: cfg.RewardUSDMicros,
			QualifiedAt:     s.now().UTC(),
			TierThresholds:  cfg.TierThresholds,
		})
	}
	store, ok := s.store.(referralQualificationStore)
	if !ok {
		return ErrStoreNotConfigured
	}
	return store.qualifyPendingReferral(ctx, tenantID, refereeUserID, billingEventID, s.now().UTC())
}

func (s *PostgresStore) qualifyPendingReferral(ctx context.Context, tenantID, refereeUserID, billingEventID int64, qualifiedAt time.Time) error {
	if tenantID <= 0 || refereeUserID <= 0 || billingEventID <= 0 {
		return ErrInvalidInput
	}
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	cfg, err := referralRewardConfigFromEnv()
	if err != nil {
		return err
	}
	return s.qualifyPendingReferralWithReward(ctx, qualifyReferralInput{
		TenantID:        tenantID,
		RefereeUserID:   refereeUserID,
		BillingEventID:  billingEventID,
		RewardUSDMicros: cfg.RewardUSDMicros,
		QualifiedAt:     qualifiedAt,
		TierThresholds:  cfg.TierThresholds,
	})
}
