package checkin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestCheckinRewardBounds(t *testing.T) {
	// Mutation: replacing randomRewardCents with `return max + 1` makes this bounds test red.
	settings := fakeSettings{values: map[platformsettings.SettingKey]string{
		platformsettings.KeyCheckinEnabled:  "true",
		platformsettings.KeyCheckinMinCents: "7",
		platformsettings.KeyCheckinMaxCents: "13",
	}}
	rewarder := &recordingRewarder{}
	svc := NewService(Deps{
		Store:    &fakeStore{},
		Payment:  rewarder,
		Settings: settings,
	}, WithClock(func() time.Time {
		return time.Date(2026, 6, 6, 15, 4, 5, 0, time.UTC)
	}))

	for i := 0; i < 200; i++ {
		if _, err := svc.DoCheckin(context.Background(), 1, int64(i+1)); err != nil {
			t.Fatalf("DoCheckin iteration %d: %v", i, err)
		}
		got := rewarder.last.RewardCents
		if got < 7 || got > 13 {
			t.Fatalf("reward_cents=%d outside [7,13]", got)
		}
	}
}

func TestCheckinConfigRejectsInvertedBoundsBeforePayment(t *testing.T) {
	settings := fakeSettings{values: map[platformsettings.SettingKey]string{
		platformsettings.KeyCheckinEnabled:  "true",
		platformsettings.KeyCheckinMinCents: "20",
		platformsettings.KeyCheckinMaxCents: "10",
	}}
	rewarder := &recordingRewarder{}
	svc := NewService(Deps{Store: &fakeStore{}, Payment: rewarder, Settings: settings})

	if _, err := svc.DoCheckin(context.Background(), 1, 2); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("DoCheckin err=%v want ErrInvalidConfig", err)
	}
	if rewarder.calls != 0 {
		t.Fatalf("payment called %d times for invalid config, want 0", rewarder.calls)
	}
}

func TestGetStatusUsesCurrentUTCMonthWhenMonthOmitted(t *testing.T) {
	store := &fakeStore{
		checked: true,
		records: []Record{{
			ID:             7,
			CheckinDate:    time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
			RewardCents:    11,
			BillingEventID: 99,
			CreatedAt:      time.Date(2026, 6, 6, 1, 2, 3, 0, time.UTC),
		}},
	}
	svc := NewService(Deps{
		Store:   store,
		Payment: &recordingRewarder{},
		Settings: fakeSettings{values: map[platformsettings.SettingKey]string{
			platformsettings.KeyCheckinEnabled:  "true",
			platformsettings.KeyCheckinMinCents: "1",
			platformsettings.KeyCheckinMaxCents: "20",
		}},
	}, WithClock(func() time.Time {
		return time.Date(2026, 6, 30, 23, 59, 0, 0, time.UTC)
	}))

	status, err := svc.GetStatus(context.Background(), 1, 2, "")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.Month != "2026-06" || !status.CheckedInToday || len(status.Records) != 1 {
		t.Fatalf("status=%+v want month 2026-06 checked today with one record", status)
	}
	if !store.lastMonthStart.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("month start=%s want 2026-06-01 UTC", store.lastMonthStart)
	}
}

type fakeSettings struct {
	values map[platformsettings.SettingKey]string
}

func (s fakeSettings) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	if value, ok := s.values[key]; ok {
		return platformsettings.StoredSetting{Key: key, Value: value, Source: platformsettings.SourceDB}, nil
	}
	value, _ := platformsettings.DefaultValue(key)
	return platformsettings.StoredSetting{Key: key, Value: value, Source: platformsettings.SourceDefault}, nil
}

type recordingRewarder struct {
	calls int
	last  payment.CheckinRewardInput
	err   error
}

func (r *recordingRewarder) ApplyCheckinReward(_ context.Context, in payment.CheckinRewardInput) (payment.CheckinRewardResult, error) {
	r.calls++
	r.last = in
	if r.err != nil {
		return payment.CheckinRewardResult{}, r.err
	}
	return payment.CheckinRewardResult{
		NewBalance:     in.RewardCents,
		CheckinID:      int64(r.calls),
		BillingEventID: int64(100 + r.calls),
		RewardCents:    in.RewardCents,
	}, nil
}

type fakeStore struct {
	checked        bool
	records        []Record
	lastMonthStart time.Time
}

func (s *fakeStore) CheckedInOn(_ context.Context, _, _ int64, _ time.Time) (bool, error) {
	return s.checked, nil
}

func (s *fakeStore) ListRecords(_ context.Context, _, _ int64, monthStart time.Time) ([]Record, error) {
	s.lastMonthStart = monthStart
	return append([]Record(nil), s.records...), nil
}
