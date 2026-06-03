package invitation

import (
	"context"
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

type qualificationReferral struct {
	tenantID            int64
	refereeUserID       int64
	status              string
	firstBillingEventID int64
	qualifiedAt         time.Time
}

type qualificationStore struct {
	referrals []qualificationReferral
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

func (s *qualificationStore) referral(tenantID, refereeUserID int64) qualificationReferral {
	for _, row := range s.referrals {
		if row.tenantID == tenantID && row.refereeUserID == refereeUserID {
			return row
		}
	}
	return qualificationReferral{}
}
