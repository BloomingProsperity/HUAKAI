package checkin

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

type Store interface {
	CheckedInOn(context.Context, int64, int64, time.Time) (bool, error)
	ListRecords(context.Context, int64, int64, time.Time) ([]Record, error)
}

type Settings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type PaymentRewarder interface {
	ApplyCheckinReward(context.Context, payment.CheckinRewardInput) (payment.CheckinRewardResult, error)
}

type Deps struct {
	Store    Store
	Payment  PaymentRewarder
	Settings Settings
}

type Service struct {
	store    Store
	payment  PaymentRewarder
	settings Settings
	now      func() time.Time
	reward   func(int64, int64) (int64, error)
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func WithRewardGenerator(gen func(int64, int64) (int64, error)) Option {
	return func(s *Service) {
		if gen != nil {
			s.reward = gen
		}
	}
}

func NewService(d Deps, opts ...Option) *Service {
	s := &Service{
		store:    d.Store,
		payment:  d.Payment,
		settings: d.Settings,
		now:      func() time.Time { return time.Now().UTC() },
		reward:   randomRewardCents,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) DoCheckin(ctx context.Context, tenantID, userID int64) (Result, error) {
	if s == nil || s.payment == nil || s.settings == nil {
		return Result{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return Result{}, ErrInvalidInput
	}
	cfg, err := s.Config(ctx)
	if err != nil {
		return Result{}, err
	}
	if !cfg.Enabled {
		return Result{}, ErrDisabled
	}
	reward, err := s.reward(cfg.MinCents, cfg.MaxCents)
	if err != nil {
		return Result{}, err
	}
	now := s.now().UTC()
	date := normalizeDate(now)
	res, err := s.payment.ApplyCheckinReward(ctx, payment.CheckinRewardInput{
		TenantID:     tenantID,
		UserID:       userID,
		Date:         date,
		RewardCents:  reward,
		CurrencyCode: "USD",
		Now:          now,
	})
	if err != nil {
		return Result{}, err
	}
	if res.AlreadyCheckedIn {
		return Result{}, ErrAlreadyCheckedIn
	}
	return Result{
		RewardCents:    res.RewardCents,
		CheckinDate:    date,
		NewBalance:     res.NewBalance,
		CheckinID:      res.CheckinID,
		BillingEventID: res.BillingEventID,
	}, nil
}

func (s *Service) GetStatus(ctx context.Context, tenantID, userID int64, month string) (Status, error) {
	if s == nil || s.store == nil || s.settings == nil {
		return Status{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return Status{}, ErrInvalidInput
	}
	cfg, err := s.Config(ctx)
	if err != nil {
		return Status{}, err
	}
	today := normalizeDate(s.now())
	monthStart, err := parseMonthOrCurrent(month, today)
	if err != nil {
		return Status{}, err
	}
	checked, err := s.store.CheckedInOn(ctx, tenantID, userID, today)
	if err != nil {
		return Status{}, err
	}
	records, err := s.store.ListRecords(ctx, tenantID, userID, monthStart)
	if err != nil {
		return Status{}, err
	}
	return Status{
		Enabled:        cfg.Enabled,
		MinCents:       cfg.MinCents,
		MaxCents:       cfg.MaxCents,
		Month:          monthStart.Format("2006-01"),
		CheckedInToday: checked,
		Records:        records,
	}, nil
}

func (s *Service) Config(ctx context.Context) (Config, error) {
	if s == nil || s.settings == nil {
		return Config{}, ErrStoreNotConfigured
	}
	enabled, err := readBoolSetting(ctx, s.settings, platformsettings.KeyCheckinEnabled)
	if err != nil {
		return Config{}, err
	}
	minCents, err := readIntSetting(ctx, s.settings, platformsettings.KeyCheckinMinCents)
	if err != nil {
		return Config{}, err
	}
	maxCents, err := readIntSetting(ctx, s.settings, platformsettings.KeyCheckinMaxCents)
	if err != nil {
		return Config{}, err
	}
	if minCents <= 0 || maxCents <= 0 || minCents > maxCents {
		return Config{}, ErrInvalidConfig
	}
	return Config{Enabled: enabled, MinCents: minCents, MaxCents: maxCents}, nil
}

func readBoolSetting(ctx context.Context, settings Settings, key platformsettings.SettingKey) (bool, error) {
	row, err := settings.Get(ctx, key)
	if err != nil {
		return false, err
	}
	value := strings.TrimSpace(row.Value)
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, ErrInvalidConfig
	}
}

func readIntSetting(ctx context.Context, settings Settings, key platformsettings.SettingKey) (int64, error) {
	row, err := settings.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(row.Value), 10, 64)
	if err != nil {
		return 0, ErrInvalidConfig
	}
	return n, nil
}

func randomRewardCents(minCents, maxCents int64) (int64, error) {
	if minCents <= 0 || maxCents <= 0 || minCents > maxCents {
		return 0, ErrInvalidConfig
	}
	if minCents == maxCents {
		return minCents, nil
	}
	span := maxCents - minCents + 1
	n, err := rand.Int(rand.Reader, big.NewInt(span))
	if err != nil {
		return 0, fmt.Errorf("checkin: random reward: %w", err)
	}
	return minCents + n.Int64(), nil
}

func parseMonthOrCurrent(raw string, today time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		y, m, _ := today.UTC().Date()
		return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC), nil
	}
	month, err := time.Parse("2006-01-02", raw+"-01")
	if err != nil {
		return time.Time{}, errors.Join(ErrInvalidInput, err)
	}
	return month.UTC(), nil
}

func normalizeDate(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
