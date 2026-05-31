package payment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type Status string

const (
	StatusPending   Status = "PENDING"
	StatusPaid      Status = "PAID"
	StatusCrediting Status = "CREDITING"
	StatusCompleted Status = "COMPLETED"
	StatusFailed    Status = "FAILED"
	StatusExpired   Status = "EXPIRED"
	StatusCancelled Status = "CANCELLED"
)

var (
	ErrInvalidInput          = errors.New("payment: invalid input")
	ErrStoreNotConfigured    = errors.New("payment: store not configured")
	ErrUserNotFound          = errors.New("payment: user not found")
	ErrAccountInactive       = errors.New("payment: account inactive")
	ErrPendingLimit          = errors.New("payment: pending order limit reached")
	ErrDailyAmountLimit      = errors.New("payment: daily amount limit reached")
	ErrExternalTradeConflict = errors.New("payment: external trade conflict")
)

var moneyNumericUpperBound = decimal.NewFromInt(1_000_000_000_000)

type ExternalTradeNoGenerator func(context.Context) (string, error)

type Store interface {
	OpenRecharge(context.Context, OpenInput) (Order, error)
}

type Service struct {
	store       Store
	tradeNoGen  ExternalTradeNoGenerator
	maxAttempts int
}

type Option func(*Service)

func WithExternalTradeNoGenerator(gen ExternalTradeNoGenerator) Option {
	return func(s *Service) {
		if gen != nil {
			s.tradeNoGen = gen
		}
	}
}

func NewService(store Store, opts ...Option) *Service {
	s := &Service{
		store:       store,
		tradeNoGen:  randomExternalTradeNo,
		maxAttempts: 3,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.tradeNoGen == nil {
		s.tradeNoGen = randomExternalTradeNo
	}
	return s
}

type Order struct {
	ID              int64
	TenantID        int64
	UserID          int64
	ExternalTradeNo string
	RechargeRef     string
	Status          Status
	CreditedAmount  decimal.Decimal
	CurrencyCode    string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type OpenInput struct {
	TenantID          int64
	UserID            int64
	ExternalTradeNo   string
	Amount            decimal.Decimal
	CurrencyCode      string
	MaxPendingPerUser int
	DailyAmountLimit  decimal.Decimal
	Now               time.Time
}

type OpenResult struct {
	Order Order
}

func (s *Service) OpenRecharge(ctx context.Context, input OpenInput) (OpenResult, error) {
	if s == nil || s.store == nil {
		return OpenResult{}, ErrStoreNotConfigured
	}
	input = normalizeOpenInput(input)
	if err := validateOpenInput(input, false); err != nil {
		return OpenResult{}, err
	}
	if strings.TrimSpace(input.ExternalTradeNo) != "" {
		order, err := s.store.OpenRecharge(ctx, input)
		return OpenResult{Order: order}, err
	}
	attempts := s.maxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		tradeNo, err := s.tradeNoGen(ctx)
		if err != nil {
			return OpenResult{}, fmt.Errorf("payment: generate external trade no: %w", err)
		}
		next := input
		next.ExternalTradeNo = strings.TrimSpace(tradeNo)
		if err := validateOpenInput(next, true); err != nil {
			return OpenResult{}, err
		}
		order, err := s.store.OpenRecharge(ctx, next)
		if errors.Is(err, ErrExternalTradeConflict) {
			continue
		}
		return OpenResult{Order: order}, err
	}
	return OpenResult{}, ErrExternalTradeConflict
}

func normalizeOpenInput(input OpenInput) OpenInput {
	input.CurrencyCode = strings.ToUpper(strings.TrimSpace(input.CurrencyCode))
	if input.CurrencyCode == "" {
		input.CurrencyCode = "USD"
	}
	input.ExternalTradeNo = strings.TrimSpace(input.ExternalTradeNo)
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	} else {
		input.Now = input.Now.UTC()
	}
	return input
}

func validateOpenInput(input OpenInput, requireExternal bool) error {
	if input.TenantID <= 0 || input.UserID <= 0 || !input.Amount.IsPositive() || input.MaxPendingPerUser <= 0 {
		return ErrInvalidInput
	}
	if !fitsMoneyColumn(input.Amount) {
		return ErrInvalidInput
	}
	if len(input.CurrencyCode) != 3 {
		return ErrInvalidInput
	}
	if input.CurrencyCode != "USD" {
		return ErrInvalidInput
	}
	if input.DailyAmountLimit.IsNegative() {
		return ErrInvalidInput
	}
	if input.DailyAmountLimit.IsPositive() && !fitsMoneyColumn(input.DailyAmountLimit) {
		return ErrInvalidInput
	}
	if requireExternal && input.ExternalTradeNo == "" {
		return ErrInvalidInput
	}
	return nil
}

func fitsMoneyColumn(value decimal.Decimal) bool {
	if value.IsNegative() {
		return false
	}
	if !value.Equal(value.Truncate(8)) {
		return false
	}
	return value.Abs().LessThan(moneyNumericUpperBound)
}

func rechargeRef(tenantID, userID int64, externalTradeNo string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", tenantID, userID, externalTradeNo)))
	return "rch_" + hex.EncodeToString(sum[:8])
}

func randomExternalTradeNo(context.Context) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "rech_" + hex.EncodeToString(b[:]), nil
}
