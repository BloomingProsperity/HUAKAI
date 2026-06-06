package invitation

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestQualifyPendingReferralQualifiesPendingAndRecordsBillingEvent(t *testing.T) {
	store := &qualificationStore{
		referrals: []qualificationReferral{{
			tenantID:      7,
			refereeUserID: 7001,
			status:        "pending",
		}},
	}
	service := NewService(store)

	if err := service.QualifyPendingReferral(context.Background(), 7, 7001, 4242); err != nil {
		t.Fatalf("QualifyPendingReferral: %v", err)
	}

	got := store.referral(7, 7001)
	if got.status != "qualified" {
		t.Fatalf("referral status=%q want qualified; deleting the qualify update leaves this pending", got.status)
	}
	if got.firstBillingEventID != 4242 {
		t.Fatalf("first_billing_event_id=%d want 4242", got.firstBillingEventID)
	}
	if got.qualifiedAt.IsZero() {
		t.Fatal("qualified_at was not recorded")
	}
}

func TestQualifyPendingReferralNoPendingReferralIsNoOp(t *testing.T) {
	store := &qualificationStore{
		referrals: []qualificationReferral{{
			tenantID:            7,
			refereeUserID:       7002,
			status:              "rejected",
			firstBillingEventID: 0,
		}},
	}
	service := NewService(store)

	if err := service.QualifyPendingReferral(context.Background(), 7, 7001, 4242); err != nil {
		t.Fatalf("QualifyPendingReferral without pending row: %v", err)
	}

	got := store.referral(7, 7002)
	if got.status != "rejected" || got.firstBillingEventID != 0 {
		t.Fatalf("unrelated referral changed: %+v", got)
	}
}

func TestQualifyPendingReferralAlreadyQualifiedIsIdempotent(t *testing.T) {
	qualifiedAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	store := &qualificationStore{
		referrals: []qualificationReferral{{
			tenantID:            7,
			refereeUserID:       7001,
			status:              "qualified",
			firstBillingEventID: 1111,
			qualifiedAt:         qualifiedAt,
		}},
	}
	service := NewService(store)

	if err := service.QualifyPendingReferral(context.Background(), 7, 7001, 9999); err != nil {
		t.Fatalf("QualifyPendingReferral already qualified: %v", err)
	}

	got := store.referral(7, 7001)
	if got.status != "qualified" || got.firstBillingEventID != 1111 || !got.qualifiedAt.Equal(qualifiedAt) {
		t.Fatalf("qualified referral was not idempotent: %+v", got)
	}
}

func TestQualifyPendingReferralRemainsQualifyOnly(t *testing.T) {
	// Mutation: calling the old invitation-local reward path from QualifyPendingReferral makes rewardCalls nonzero.
	store := &qualificationStore{
		referrals: []qualificationReferral{{
			id:             91,
			tenantID:       7,
			refereeUserID:  7001,
			referrerUserID: 42,
			status:         "pending",
		}},
		rewardedReferralIDs: map[int64]bool{},
		referrerBalances:    map[int64]int64{},
		tierProgress:        map[int64]qualificationTierProgress{},
	}
	service := NewService(store, WithNow(func() time.Time {
		return time.Date(2026, 6, 5, 9, 0, 0, 0, time.UTC)
	}))

	if err := service.QualifyPendingReferral(context.Background(), 7, 7001, 4242); err != nil {
		t.Fatalf("QualifyPendingReferral: %v", err)
	}

	got := store.referral(7, 7001)
	if got.status != "qualified" {
		t.Fatalf("referral status=%q want qualified", got.status)
	}
	if store.rewardCalls != 0 || len(store.rewards) != 0 || store.referrerBalances[42] != 0 {
		t.Fatalf("qualify-only touched reward path: rewardCalls=%d rewards=%+v balances=%+v", store.rewardCalls, store.rewards, store.referrerBalances)
	}
}

func TestReferralSummaryCountsQualifiedRewardedAndRewardCents(t *testing.T) {
	store := &summaryStore{summary: ReferralSummary{
		QualifiedCount:     2,
		RewardedCount:      1,
		RewardsEarnedCents: 73,
	}}
	service := NewService(store)

	got, err := service.ReferralSummary(context.Background(), 7, 42)
	if err != nil {
		t.Fatalf("ReferralSummary: %v", err)
	}
	if store.tenantID != 7 || store.referrerUserID != 42 {
		t.Fatalf("summary scope=(tenant=%d, referrer=%d) want (7,42)", store.tenantID, store.referrerUserID)
	}
	if got.QualifiedCount != 2 || got.RewardedCount != 1 || got.RewardsEarnedCents != 73 {
		t.Fatalf("summary=%+v want qualified=2 rewarded=1 rewards_cents=73", got)
	}
}

func TestReferralRewardMigrationContracts(t *testing.T) {
	raw, err := os.ReadFile("../../../sql/migrations/0100_referral_reward_issuance.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"ALTER COLUMN receipt_id DROP NOT NULL",
		"ADD COLUMN IF NOT EXISTS billing_event_id BIGINT",
		"ADD COLUMN IF NOT EXISTS currency_code TEXT NOT NULL DEFAULT 'USD'",
		"referral_rewards_tenant_referral_unique UNIQUE (tenant_id, referral_id)",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("0100 migration missing %q", want)
		}
	}
}

type qualificationReferral struct {
	id                  int64
	tenantID            int64
	refereeUserID       int64
	referrerUserID      int64
	status              string
	firstBillingEventID int64
	qualifiedAt         time.Time
}

type qualificationStore struct {
	referrals           []qualificationReferral
	rewardCalls         int
	rewards             []qualificationReward
	rewardAudits        []qualificationRewardAudit
	rewardedReferralIDs map[int64]bool
	referrerBalances    map[int64]int64
	tierProgress        map[int64]qualificationTierProgress
}

type qualificationReward struct {
	referralID      int64
	referrerUserID  int64
	amountUSDMicros int64
}

type qualificationRewardAudit struct {
	eventType string
	reason    string
}

type qualificationTierProgress struct {
	totalQualifiedReferrals int
	currentTier             string
}

type summaryStore struct {
	summary        ReferralSummary
	tenantID       int64
	referrerUserID int64
}

func (s *summaryStore) Generate(context.Context, generateRecord) (Invitation, error) {
	return Invitation{}, ErrStoreNotConfigured
}

func (s *summaryStore) GetByCode(context.Context, string) (Invitation, error) {
	return Invitation{}, ErrStoreNotConfigured
}

func (s *summaryStore) GetByClientIdempotencyKey(context.Context, int64, string) (Invitation, error) {
	return Invitation{}, ErrStoreNotConfigured
}

func (s *summaryStore) Preview(context.Context, int64, string) (InvitationPreview, error) {
	return InvitationPreview{}, ErrStoreNotConfigured
}

func (s *summaryStore) CountTenantInvitationsSince(context.Context, int64, time.Time) (int, error) {
	return 0, ErrStoreNotConfigured
}

func (s *summaryStore) GetReferralSummary(_ context.Context, tenantID, referrerUserID int64) (ReferralSummary, error) {
	s.tenantID = tenantID
	s.referrerUserID = referrerUserID
	return s.summary, nil
}

func (s *qualificationStore) Generate(context.Context, generateRecord) (Invitation, error) {
	return Invitation{}, ErrStoreNotConfigured
}

func (s *qualificationStore) GetByCode(context.Context, string) (Invitation, error) {
	return Invitation{}, ErrStoreNotConfigured
}

func (s *qualificationStore) GetByClientIdempotencyKey(context.Context, int64, string) (Invitation, error) {
	return Invitation{}, ErrStoreNotConfigured
}

func (s *qualificationStore) Preview(context.Context, int64, string) (InvitationPreview, error) {
	return InvitationPreview{}, ErrStoreNotConfigured
}

func (s *qualificationStore) CountTenantInvitationsSince(context.Context, int64, time.Time) (int, error) {
	return 0, ErrStoreNotConfigured
}

func (s *qualificationStore) qualifyPendingReferral(_ context.Context, tenantID, refereeUserID, billingEventID int64, qualifiedAt time.Time) error {
	for i := range s.referrals {
		row := &s.referrals[i]
		if row.tenantID == tenantID && row.refereeUserID == refereeUserID && row.status == "pending" {
			row.status = "qualified"
			row.firstBillingEventID = billingEventID
			row.qualifiedAt = qualifiedAt
			return nil
		}
	}
	return nil
}

func (s *qualificationStore) qualifyPendingReferralWithReward(_ context.Context, input qualifyReferralInput) error {
	s.rewardCalls++
	for i := range s.referrals {
		row := &s.referrals[i]
		if row.tenantID != input.TenantID || row.refereeUserID != input.RefereeUserID || row.status != "pending" {
			continue
		}
		row.status = "qualified"
		row.firstBillingEventID = input.BillingEventID
		row.qualifiedAt = input.QualifiedAt
		if input.RewardUSDMicros <= 0 || row.referrerUserID <= 0 {
			s.rewardAudits = append(s.rewardAudits, qualificationRewardAudit{eventType: "REWARD_SKIPPED", reason: "reward_not_issued"})
			return nil
		}
		if s.rewardedReferralIDs == nil {
			s.rewardedReferralIDs = map[int64]bool{}
		}
		if s.rewardedReferralIDs[row.id] {
			return nil
		}
		s.rewardedReferralIDs[row.id] = true
		row.status = "rewarded"
		s.rewards = append(s.rewards, qualificationReward{
			referralID:      row.id,
			referrerUserID:  row.referrerUserID,
			amountUSDMicros: input.RewardUSDMicros,
		})
		if s.referrerBalances == nil {
			s.referrerBalances = map[int64]int64{}
		}
		s.referrerBalances[row.referrerUserID] += input.RewardUSDMicros
		if s.tierProgress == nil {
			s.tierProgress = map[int64]qualificationTierProgress{}
		}
		progress := s.tierProgress[row.referrerUserID]
		progress.totalQualifiedReferrals++
		progress.currentTier = tierForQualifiedReferralCount(progress.totalQualifiedReferrals, input.TierThresholds)
		s.tierProgress[row.referrerUserID] = progress
		s.rewardAudits = append(s.rewardAudits, qualificationRewardAudit{eventType: "REWARD_ISSUED"})
		return nil
	}
	return nil
}

func (s *qualificationStore) referral(tenantID, refereeUserID int64) qualificationReferral {
	for _, row := range s.referrals {
		if row.tenantID == tenantID && row.refereeUserID == refereeUserID {
			return row
		}
	}
	return qualificationReferral{}
}
