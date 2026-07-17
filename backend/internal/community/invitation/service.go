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

type referralSummaryStore interface {
	GetReferralSummary(context.Context, int64, int64) (ReferralSummary, error)
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

func (s *Service) ReferralSummary(ctx context.Context, tenantID, referrerUserID int64) (ReferralSummary, error) {
	if s == nil || s.store == nil {
		return ReferralSummary{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || referrerUserID <= 0 {
		return ReferralSummary{}, ErrInvalidInput
	}
	store, ok := s.store.(referralSummaryStore)
	if !ok {
		return ReferralSummary{}, ErrStoreNotConfigured
	}
	return store.GetReferralSummary(ctx, tenantID, referrerUserID)
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
	// 自荐标记由服务端保留：活动调用方不得能够提供带 self 前缀的幂等键，
	// 否则生成的那一行会逃过活动配额计数器（绕过配额）。
	if params.ClientIdempotencyKey != nil && strings.HasPrefix(*params.ClientIdempotencyKey, SelfReferralIdempotencyPrefix) {
		return ErrReservedIdempotencyKey
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

// GetOrCreateSelfReferralCode 返回调用方那唯一且稳定的自助推荐码，首次调用
// 时惰性铸造。与活动 Generate 不同，它不受每月租户配额的限制：个人推荐码是
// 用户身份，而不是活动量，因此当某个共享的单租户部署已耗尽本月活动上限后，
// 仅仅获取自己的码也绝不能被活动配额拦截；HUAKAI 保留活动上限，
// 同时为稳定的个人身份码提供豁免路径。
// 幂等：重复调用会通过保留的 self:<userID> 幂等键返回同一个码。
func (s *Service) GetOrCreateSelfReferralCode(ctx context.Context, tenantID, inviterUserID int64, now time.Time) (GenerateInvitationOutput, error) {
	if s == nil || s.store == nil {
		return GenerateInvitationOutput{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || inviterUserID <= 0 {
		return GenerateInvitationOutput{}, ErrInvalidInput
	}
	if now.IsZero() {
		now = s.now()
	}
	now = now.UTC()
	idemKey := fmt.Sprintf("%s%d", SelfReferralIdempotencyPrefix, inviterUserID)

	existing, err := s.store.GetByClientIdempotencyKey(ctx, tenantID, idemKey)
	if err == nil {
		return outputFromInvitation(existing), nil
	}
	if !errors.Is(err, ErrNotFound) {
		return GenerateInvitationOutput{}, err
	}

	expiresAt := now.AddDate(selfReferralExpiryYears, 0, 0)
	for attempt := 0; attempt < MaxGenerateAttempts; attempt++ {
		code, gerr := s.generator.Generate()
		if gerr != nil {
			return GenerateInvitationOutput{}, fmt.Errorf("invitation: generate self code: %w", gerr)
		}
		code = NormalizeCode(code)
		if !ValidCode(code) {
			return GenerateInvitationOutput{}, ErrInvalidInput
		}
		row, serr := s.store.Generate(ctx, generateRecord{
			TenantID:             tenantID,
			InviterUserID:        inviterUserID,
			Code:                 code,
			CreatedAt:            now,
			ExpiresAt:            expiresAt,
			MaxUsage:             MaxUsageLimit,
			ClientIdempotencyKey: &idemKey,
			QuotaExempt:          true,
		})
		if serr == nil {
			return outputFromInvitation(row), nil
		}
		// 并发的第一次调用可能已用同一个幂等键插入了 self 行；
		// 重新解析它，使两个调用方都收敛到同一个码。
		if reread, rerr := s.store.GetByClientIdempotencyKey(ctx, tenantID, idemKey); rerr == nil {
			return outputFromInvitation(reread), nil
		}
		if !errors.Is(serr, ErrDuplicateCode) {
			return GenerateInvitationOutput{}, serr
		}
	}
	return GenerateInvitationOutput{}, ErrDuplicateCode
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
