package checkin

import (
	"errors"
	"time"
)

type Config struct {
	Enabled  bool
	MinCents int64
	MaxCents int64
}

type Result struct {
	RewardCents    int64
	CheckinDate    time.Time
	NewBalance     int64
	CheckinID      int64
	BillingEventID int64
}

type Status struct {
	Enabled        bool
	MinCents       int64
	MaxCents       int64
	Month          string
	CheckedInToday bool
	Records        []Record
}

type Record struct {
	ID             int64
	CheckinDate    time.Time
	RewardCents    int64
	CurrencyCode   string
	BillingEventID int64
	CreatedAt      time.Time
}

var (
	ErrDisabled           = errors.New("checkin: disabled")
	ErrAlreadyCheckedIn   = errors.New("checkin: already checked in today")
	ErrInvalidConfig      = errors.New("checkin: invalid config")
	ErrInvalidInput       = errors.New("checkin: invalid input")
	ErrStoreNotConfigured = errors.New("checkin: store not configured")
)
