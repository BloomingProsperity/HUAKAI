package invitation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxExpiresDays = 90

type Store interface {
	Generate(context.Context, generateRecord) (Invitation, error)
	GetByCode(context.Context, string) (Invitation, error)
	GetByClientIdempotencyKey(context.Context, int64, string) (Invitation, error)
	Preview(context.Context, int64, string) (InvitationPreview, error)
	CountTenantInvitationsSince(context.Context, int64, time.Time) (int, error)
}

type Service struct {
	store     Store
	generator CodeGenerator
	now       func() time.Time
}

type Option func(*Service)

func WithCodeGenerator(generator CodeGenerator) Option {
	return func(s *Service) { s.generator = generator }
}

func WithNow(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func NewService(store Store, opts ...Option) *Service {
	s := &Service{
		store:     store,
		generator: RandomCodeGenerator{},
		now:       func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if s.generator == nil {
		s.generator = RandomCodeGenerator{}
	}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	return s
}

func (s *Service) Generate(ctx context.Context, params GenerateInvitationParams) (GenerateInvitationOutput, error) {
	if s == nil || s.store == nil {
		return GenerateInvitationOutput{}, ErrStoreNotConfigured
	}
	params = normalizeGenerateParams(params, s.now())
	if err := validateGenerateParams(params); err != nil {
		return GenerateInvitationOutput{}, err
	}
	if params.ClientIdempotencyKey != nil {
		row, err := s.store.GetByClientIdempotencyKey(ctx, params.TenantID, *params.ClientIdempotencyKey)
		if err == nil {
			return outputFromInvitation(row), nil
		}
		if !errors.Is(err, ErrNotFound) {
			return GenerateInvitationOutput{}, err
		}
	}
	if err := checkTenantMonthlyQuota(ctx, s.store, params.TenantID, params.Now); err != nil {
		return GenerateInvitationOutput{}, err
	}
	expiresAt := params.Now.AddDate(0, 0, params.ExpiresInDays)
	for attempt := 0; attempt < MaxGenerateAttempts; attempt++ {
		code, err := s.generator.Generate()
		if err != nil {
			return GenerateInvitationOutput{}, fmt.Errorf("invitation: generate code: %w", err)
		}
		code = NormalizeCode(code)
		if !ValidCode(code) {
			return GenerateInvitationOutput{}, ErrInvalidInput
		}
		row, err := s.store.Generate(ctx, generateRecord{
			TenantID:             params.TenantID,
			InviterUserID:        params.InviterUserID,
			Code:                 code,
			CreatedAt:            params.Now,
			ExpiresAt:            expiresAt,
			MaxUsage:             params.MaxUsage,
			ClientIdempotencyKey: params.ClientIdempotencyKey,
		})
		if err == nil {
			return outputFromInvitation(row), nil
		}
		if !errors.Is(err, ErrDuplicateCode) {
			return GenerateInvitationOutput{}, err
		}
	}
	return GenerateInvitationOutput{}, ErrDuplicateCode
}

func normalizeGenerateParams(params GenerateInvitationParams, fallbackNow time.Time) GenerateInvitationParams {
	if params.Now.IsZero() {
		params.Now = fallbackNow
	}
	params.Now = params.Now.UTC()
	if params.MaxUsage == 0 {
		params.MaxUsage = DefaultMaxUsage
	}
	if params.ExpiresInDays == 0 {
		params.ExpiresInDays = DefaultExpiryDays
	}
	if params.ClientIdempotencyKey != nil {
		key := strings.TrimSpace(*params.ClientIdempotencyKey)
		if key == "" {
			params.ClientIdempotencyKey = nil
		} else {
			params.ClientIdempotencyKey = &key
		}
	}
	return params
}

func validateGenerateParams(params GenerateInvitationParams) error {
	if params.TenantID <= 0 || params.InviterUserID <= 0 {
		return ErrInvalidInput
	}
	if params.MaxUsage <= 0 || params.MaxUsage > MaxUsageLimit {
		return ErrInvalidInput
	}
	if params.ExpiresInDays <= 0 {
		return ErrInvalidInput
	}
	if params.ExpiresInDays > maxExpiresDays {
		return ErrInvitationExpiresOverLimit
	}
	return nil
}

func outputFromInvitation(row Invitation) GenerateInvitationOutput {
	var expiresAt time.Time
	if row.ExpiresAt != nil {
		expiresAt = row.ExpiresAt.UTC()
	}
	return GenerateInvitationOutput{
		Code:          row.Code,
		InviterUserID: row.InviterUserID,
		ExpiresAt:     expiresAt,
		MaxUsage:      row.MaxUsage,
	}
}
