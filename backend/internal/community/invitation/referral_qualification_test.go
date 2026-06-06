package invitation

import (
	"context"
	"errors"
	"os"
	"regexp"
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

func TestQualifyPendingReferralIssuesRewardCreditTierAndAudit(t *testing.T) {
	t.Setenv("HUAKAI_REFERRAL_REWARD_USD_MICROS", "1250000")
	t.Setenv("HUAKAI_REFERRAL_TIER_SILVER_THRESHOLD", "1")
	t.Setenv("HUAKAI_REFERRAL_TIER_GOLD_THRESHOLD", "2")
	t.Setenv("HUAKAI_REFERRAL_TIER_PLATINUM_THRESHOLD", "3")
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
		t.Fatalf("QualifyPendingReferral reward: %v", err)
	}

	got := store.referral(7, 7001)
	if got.status != "rewarded" {
		t.Fatalf("referral status=%q want rewarded after positive reward", got.status)
	}
	if len(store.rewards) != 1 {
		t.Fatalf("rewards=%d want 1; removing reward issuance leaves this at zero", len(store.rewards))
	}
	reward := store.rewards[0]
	if reward.referralID != 91 || reward.referrerUserID != 42 || reward.amountUSDMicros != 1250000 {
		t.Fatalf("reward=%+v want referral=91 referrer=42 amount=1250000", reward)
	}
	if got := store.referrerBalances[42]; got != 1250000 {
		t.Fatalf("referrer balance micros=%d want 1250000", got)
	}
	progress := store.tierProgress[42]
	if progress.totalQualifiedReferrals != 1 || progress.currentTier != "silver" {
		t.Fatalf("tier progress=%+v want total=1 current=silver", progress)
	}
	if len(store.rewardAudits) != 1 || store.rewardAudits[0].eventType != "REWARD_ISSUED" {
		t.Fatalf("reward audits=%+v want one REWARD_ISSUED", store.rewardAudits)
	}
}

func TestQualifyPendingReferralRewardReplayIsSingleIssue(t *testing.T) {
	t.Setenv("HUAKAI_REFERRAL_REWARD_USD_MICROS", "2500000")
	store := &qualificationStore{
		referrals: []qualificationReferral{{
			id:             92,
			tenantID:       7,
			refereeUserID:  7002,
			referrerUserID: 43,
			status:         "pending",
		}},
		rewardedReferralIDs: map[int64]bool{},
		referrerBalances:    map[int64]int64{},
		tierProgress:        map[int64]qualificationTierProgress{},
	}
	service := NewService(store)

	if err := service.QualifyPendingReferral(context.Background(), 7, 7002, 4243); err != nil {
		t.Fatalf("first QualifyPendingReferral reward: %v", err)
	}
	if err := service.QualifyPendingReferral(context.Background(), 7, 7002, 4243); err != nil {
		t.Fatalf("replay QualifyPendingReferral reward: %v", err)
	}

	if len(store.rewards) != 1 {
		t.Fatalf("rewards=%d want 1; deleting the reward idempotency guard double-issues", len(store.rewards))
	}
	if got := store.referrerBalances[43]; got != 2500000 {
		t.Fatalf("referrer balance micros=%d want one 2500000 credit", got)
	}
	if progress := store.tierProgress[43]; progress.totalQualifiedReferrals != 1 {
		t.Fatalf("tier progress total=%d want 1 after replay", progress.totalQualifiedReferrals)
	}
}

func TestQualifyPendingReferralRewardZeroQualifiesAndSkipsMoney(t *testing.T) {
	t.Setenv("HUAKAI_REFERRAL_REWARD_USD_MICROS", "0")
	store := &qualificationStore{
		referrals: []qualificationReferral{{
			id:             93,
			tenantID:       7,
			refereeUserID:  7003,
			referrerUserID: 44,
			status:         "pending",
		}},
		rewardedReferralIDs: map[int64]bool{},
		referrerBalances:    map[int64]int64{},
		tierProgress:        map[int64]qualificationTierProgress{},
	}
	service := NewService(store)

	if err := service.QualifyPendingReferral(context.Background(), 7, 7003, 4244); err != nil {
		t.Fatalf("QualifyPendingReferral reward disabled: %v", err)
	}

	got := store.referral(7, 7003)
	if got.status != "qualified" {
		t.Fatalf("referral status=%q want qualified when reward amount is zero", got.status)
	}
	if len(store.rewards) != 0 || store.referrerBalances[44] != 0 {
		t.Fatalf("disabled reward touched money: rewards=%+v balances=%+v", store.rewards, store.referrerBalances)
	}
	if progress := store.tierProgress[44]; progress.totalQualifiedReferrals != 0 || progress.currentTier != "" {
		t.Fatalf("disabled reward advanced tier: %+v", progress)
	}
	if len(store.rewardAudits) != 1 || store.rewardAudits[0].eventType != "REWARD_SKIPPED" {
		t.Fatalf("reward audits=%+v want one REWARD_SKIPPED", store.rewardAudits)
	}
}

func TestQualifyPendingReferralRewardTenantScoped(t *testing.T) {
	t.Setenv("HUAKAI_REFERRAL_REWARD_USD_MICROS", "3000000")
	store := &qualificationStore{
		referrals: []qualificationReferral{
			{id: 94, tenantID: 7, refereeUserID: 7004, referrerUserID: 45, status: "pending"},
			{id: 95, tenantID: 8, refereeUserID: 8004, referrerUserID: 46, status: "pending"},
		},
		rewardedReferralIDs: map[int64]bool{},
		referrerBalances:    map[int64]int64{},
		tierProgress:        map[int64]qualificationTierProgress{},
	}
	service := NewService(store)

	if err := service.QualifyPendingReferral(context.Background(), 7, 7004, 4245); err != nil {
		t.Fatalf("QualifyPendingReferral tenant A: %v", err)
	}

	if got := store.referral(7, 7004).status; got != "rewarded" {
		t.Fatalf("tenant A status=%q want rewarded", got)
	}
	if got := store.referral(8, 8004).status; got != "pending" {
		t.Fatalf("tenant B status=%q want pending; tenant scope leaked", got)
	}
	if store.referrerBalances[45] != 3000000 || store.referrerBalances[46] != 0 {
		t.Fatalf("balances=%+v want only tenant A referrer credited", store.referrerBalances)
	}
}

func TestQualifyPendingReferralRejectsUnsafeRewardConfigBeforeStore(t *testing.T) {
	t.Setenv("HUAKAI_REFERRAL_REWARD_USD_MICROS", "-1")
	store := &qualificationStore{
		referrals: []qualificationReferral{{
			id:             96,
			tenantID:       7,
			refereeUserID:  7005,
			referrerUserID: 47,
			status:         "pending",
		}},
	}
	service := NewService(store)

	err := service.QualifyPendingReferral(context.Background(), 7, 7005, 4246)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("QualifyPendingReferral err=%v want ErrInvalidInput for negative reward config", err)
	}
	if store.rewardCalls != 0 {
		t.Fatalf("invalid reward config reached store %d times", store.rewardCalls)
	}
}

func TestReferralRewardMigrationContracts(t *testing.T) {
	raw, err := os.ReadFile("../../../sql/migrations/0095_referral_reward_issuance.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_referral_rewards_referral",
		"ON referral_rewards (tenant_id, referral_id)",
		"ALTER COLUMN receipt_id DROP NOT NULL",
		"'referral_reward'",
		"CREATE TABLE IF NOT EXISTS referral_reward_audit_events",
		"'REWARD_ISSUED', 'REWARD_FAILED', 'REWARD_SKIPPED'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("0095 migration missing %q", want)
		}
	}
}

func TestReferralRewardSQLContainsIdempotencyGuards(t *testing.T) {
	raw, err := os.ReadFile("referral_reward_store.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	guards := []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{
			name:    "pending referral qualification status guard",
			pattern: regexp.MustCompile(`AND\s+status\s*=\s*'pending'`),
		},
		{
			name:    "referral reward insert conflict guard",
			pattern: regexp.MustCompile(`ON\s+CONFLICT\s*\(\s*tenant_id\s*,\s*referral_id\s*\)\s+DO\s+NOTHING`),
		},
		{
			name:    "rewarded referral status update guard",
			pattern: regexp.MustCompile(`UPDATE\s+referrals\s+SET\s+status\s*=\s*'rewarded'`),
		},
		{
			name:    "payment order out_trade_no uniqueness guard",
			pattern: regexp.MustCompile(`uq_payment_orders_out_trade_no`),
		},
	}
	for _, guard := range guards {
		if !guard.pattern.MatchString(src) {
			t.Fatalf("referral reward implementation missing idempotency guard %q matching %q", guard.name, guard.pattern.String())
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
