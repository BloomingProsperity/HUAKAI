package invitation

import (
	"context"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	ReferralRecordsDefaultLimit = 50
	ReferralRecordsMaxLimit     = 100

	ReferralStatusPending   = "pending"
	ReferralStatusQualified = "qualified"
	ReferralStatusRewarded  = "rewarded"
	ReferralStatusRejected  = "rejected"
)

type ListUserReferralsInput struct {
	TenantID       int64
	ReferrerUserID int64
	Limit          int
	Offset         int
}

type ListUserReferralRewardsInput struct {
	TenantID       int64
	ReferrerUserID int64
	Limit          int
	Offset         int
}

type ListReferralsAdminInput struct {
	TenantID int64
	Status   *string
	Limit    int
	Offset   int
}

type ReferralRecord struct {
	ID             int64
	ReferrerUserID int64
	RefereeUserID  int64
	Status         string
	CreatedAt      time.Time
	QualifiedAt    *time.Time
	RewardedAt     *time.Time
}

type ReferralRecordPage struct {
	Items  []ReferralRecord
	Total  int64
	Limit  int
	Offset int
}

type ReferralRewardLedgerEntry struct {
	ID         int64
	ReferralID int64
	RewardType string
	AmountUSD  decimal.Decimal
	CreatedAt  time.Time
}

type ReferralRewardPage struct {
	Items          []ReferralRewardLedgerEntry
	Total          int64
	TotalRewardUSD decimal.Decimal
	Limit          int
	Offset         int
}

type ReferralOverview struct {
	CountsByStatus map[string]int64
	TotalRewardUSD decimal.Decimal
	RewardCount    int64
}

type referralRecordsStore interface {
	ListUserReferrals(context.Context, ListUserReferralsInput) (ReferralRecordPage, error)
	ListUserReferralRewards(context.Context, ListUserReferralRewardsInput) (ReferralRewardPage, error)
	ListReferralsAdmin(context.Context, ListReferralsAdminInput) (ReferralRecordPage, error)
	ListReferralRewardsAdmin(context.Context, ListReferralRewardsAdminInput) (AdminReferralRewardPage, error)
	GetReferralOverview(context.Context, int64) (ReferralOverview, error)
}

func (s *Service) ListUserReferrals(ctx context.Context, input ListUserReferralsInput) (ReferralRecordPage, error) {
	if s == nil || s.store == nil {
		return ReferralRecordPage{}, ErrStoreNotConfigured
	}
	if input.TenantID <= 0 || input.ReferrerUserID <= 0 {
		return ReferralRecordPage{}, ErrInvalidInput
	}
	input.Limit, input.Offset = normalizeReferralPage(input.Limit, input.Offset)
	store, ok := s.store.(referralRecordsStore)
	if !ok {
		return ReferralRecordPage{}, ErrStoreNotConfigured
	}
	return store.ListUserReferrals(ctx, input)
}

func (s *Service) ListUserReferralRewards(ctx context.Context, input ListUserReferralRewardsInput) (ReferralRewardPage, error) {
	if s == nil || s.store == nil {
		return ReferralRewardPage{}, ErrStoreNotConfigured
	}
	if input.TenantID <= 0 || input.ReferrerUserID <= 0 {
		return ReferralRewardPage{}, ErrInvalidInput
	}
	input.Limit, input.Offset = normalizeReferralPage(input.Limit, input.Offset)
	store, ok := s.store.(referralRecordsStore)
	if !ok {
		return ReferralRewardPage{}, ErrStoreNotConfigured
	}
	return store.ListUserReferralRewards(ctx, input)
}

func (s *Service) ListReferralsAdmin(ctx context.Context, input ListReferralsAdminInput) (ReferralRecordPage, error) {
	if s == nil || s.store == nil {
		return ReferralRecordPage{}, ErrStoreNotConfigured
	}
	if input.TenantID <= 0 {
		return ReferralRecordPage{}, ErrInvalidInput
	}
	if input.Status != nil {
		status := strings.TrimSpace(*input.Status)
		if !ValidReferralStatus(status) {
			return ReferralRecordPage{}, ErrInvalidInput
		}
		input.Status = &status
	}
	input.Limit, input.Offset = normalizeReferralPage(input.Limit, input.Offset)
	store, ok := s.store.(referralRecordsStore)
	if !ok {
		return ReferralRecordPage{}, ErrStoreNotConfigured
	}
	return store.ListReferralsAdmin(ctx, input)
}

func (s *Service) ReferralOverview(ctx context.Context, tenantID int64) (ReferralOverview, error) {
	if s == nil || s.store == nil {
		return ReferralOverview{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 {
		return ReferralOverview{}, ErrInvalidInput
	}
	store, ok := s.store.(referralRecordsStore)
	if !ok {
		return ReferralOverview{}, ErrStoreNotConfigured
	}
	return store.GetReferralOverview(ctx, tenantID)
}

func normalizeReferralPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = ReferralRecordsDefaultLimit
	}
	if limit > ReferralRecordsMaxLimit {
		limit = ReferralRecordsMaxLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func ValidReferralStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case ReferralStatusPending, ReferralStatusQualified, ReferralStatusRewarded, ReferralStatusRejected:
		return true
	default:
		return false
	}
}

func referralMicrosToUSD(micros int64) decimal.Decimal {
	return decimal.NewFromInt(micros).Div(decimal.NewFromInt(1_000_000))
}

func zeroReferralStatusCounts() map[string]int64 {
	return map[string]int64{
		ReferralStatusPending:   0,
		ReferralStatusQualified: 0,
		ReferralStatusRewarded:  0,
		ReferralStatusRejected:  0,
	}
}

// ListReferralRewardsAdminInput 是租户级的管理员奖励账本过滤条件
//（ReferrerUserID 为 nil = 该租户内的全部 referrer）。F-RES-2 / AFF-019。
type ListReferralRewardsAdminInput struct {
	TenantID       int64
	ReferrerUserID *int64
	Limit          int
	Offset         int
}

type AdminReferralRewardEntry struct {
	ID             int64
	ReferralID     int64
	ReferrerUserID int64
	RewardType     string
	AmountUSD      decimal.Decimal
	CreatedAt      time.Time
}

type AdminReferralRewardPage struct {
	Items          []AdminReferralRewardEntry
	Total          int64
	TotalRewardUSD decimal.Decimal
	Limit          int
	Offset         int
}

// ListReferralRewardsAdmin 列出某租户已发放的推荐奖励（只读的管理员账本），
// 可选地按单个 referrer 过滤。不涉及任何金额变更。
func (s *Service) ListReferralRewardsAdmin(ctx context.Context, input ListReferralRewardsAdminInput) (AdminReferralRewardPage, error) {
	if s == nil || s.store == nil {
		return AdminReferralRewardPage{}, ErrStoreNotConfigured
	}
	if input.TenantID <= 0 {
		return AdminReferralRewardPage{}, ErrInvalidInput
	}
	if input.ReferrerUserID != nil && *input.ReferrerUserID <= 0 {
		return AdminReferralRewardPage{}, ErrInvalidInput
	}
	input.Limit, input.Offset = normalizeReferralPage(input.Limit, input.Offset)
	store, ok := s.store.(referralRecordsStore)
	if !ok {
		return AdminReferralRewardPage{}, ErrStoreNotConfigured
	}
	return store.ListReferralRewardsAdmin(ctx, input)
}
