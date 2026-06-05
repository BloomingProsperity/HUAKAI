// HUAKAI · iKun

package subscription

import (
	"context"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Service 订阅外观: 规范化/校验输入, 编排 store 事务, 暴露 worker 可调的到期处理。
type Service struct {
	store Store
	now   func() time.Time
}

// Option 配置 Service。
type Option func(*Service)

// WithClock 注入时钟 (测试用)。
func WithClock(clock func() time.Time) Option {
	return func(s *Service) {
		if clock != nil {
			s.now = clock
		}
	}
}

// NewService 构造订阅 service。
func NewService(store Store, opts ...Option) *Service {
	s := &Service{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// CreatePlan 建订阅套餐 (admin)。
func (s *Service) CreatePlan(ctx context.Context, in CreatePlanInput) (Plan, error) {
	if in.TenantID <= 0 {
		return Plan{}, ErrInvalidInput
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Plan{}, ErrInvalidInput
	}
	if in.ValidityDays <= 0 || in.ValidityDays > maxValidityDays {
		return Plan{}, ErrPlanInvalid
	}
	if in.PriceCents < 0 {
		return Plan{}, ErrPlanInvalid
	}
	if !capNonNegative(in.DailyCapUSD) || !capNonNegative(in.WeeklyCapUSD) || !capNonNegative(in.MonthlyCapUSD) {
		return Plan{}, ErrPlanInvalid
	}
	group := strings.TrimSpace(in.GrantedGroup)
	// 套餐必须至少授予一个分组或设一档配额上限, 否则毫无权益意义。
	if group == "" && in.DailyCapUSD == nil && in.WeeklyCapUSD == nil && in.MonthlyCapUSD == nil {
		return Plan{}, ErrPlanInvalid
	}
	return s.store.CreatePlan(ctx, createPlanRecord{
		TenantID:      in.TenantID,
		Name:          name,
		Description:   strings.TrimSpace(in.Description),
		PriceCents:    in.PriceCents,
		CurrencyCode:  normalizeCurrency(in.CurrencyCode),
		ValidityDays:  in.ValidityDays,
		GrantedGroup:  group,
		DailyCapUSD:   in.DailyCapUSD,
		WeeklyCapUSD:  in.WeeklyCapUSD,
		MonthlyCapUSD: in.MonthlyCapUSD,
		ForSale:       in.ForSale,
		SortOrder:     in.SortOrder,
		Now:           s.now(),
	})
}

// GetPlan 读套餐。
func (s *Service) GetPlan(ctx context.Context, tenantID, planID int64) (Plan, error) {
	if tenantID <= 0 || planID <= 0 {
		return Plan{}, ErrInvalidInput
	}
	return s.store.GetPlan(ctx, tenantID, planID)
}

// ListPlans 列套餐 (onlyForSale=true 只列在售启用的)。
func (s *Service) ListPlans(ctx context.Context, tenantID int64, onlyForSale bool) ([]Plan, error) {
	if tenantID <= 0 {
		return nil, ErrInvalidInput
	}
	return s.store.ListPlans(ctx, tenantID, onlyForSale)
}

// DisablePlan 停用套餐 (不再可售/分配; 不影响已购订阅)。
func (s *Service) DisablePlan(ctx context.Context, tenantID, planID int64) error {
	if tenantID <= 0 || planID <= 0 {
		return ErrInvalidInput
	}
	return s.store.DisablePlan(ctx, tenantID, planID)
}

// AssignSubscription 管理员给用户分配订阅 (幂等: 同组已 active 不重复授予)。
func (s *Service) AssignSubscription(ctx context.Context, in AssignSubscriptionInput) (AssignResult, error) {
	if in.TenantID <= 0 || in.UserID <= 0 || in.PlanID <= 0 {
		return AssignResult{}, ErrInvalidInput
	}
	return s.store.AssignSubscription(ctx, assignRecord{
		TenantID:     in.TenantID,
		UserID:       in.UserID,
		PlanID:       in.PlanID,
		ActorAdminID: in.ActorAdminID,
		RequestID:    strings.TrimSpace(in.RequestID),
		Now:          s.now(),
	})
}

// CancelSubscription 管理员取消订阅 (关配额 + 降级)。
func (s *Service) CancelSubscription(ctx context.Context, tenantID, subscriptionID, actorAdminID int64, requestID string) (UserSubscription, error) {
	if tenantID <= 0 || subscriptionID <= 0 {
		return UserSubscription{}, ErrInvalidInput
	}
	return s.store.CancelSubscription(ctx, lifecycleRecord{
		TenantID:       tenantID,
		SubscriptionID: subscriptionID,
		ActorKind:      ActorKindAdmin,
		ActorID:        actorAdminID,
		RequestID:      strings.TrimSpace(requestID),
		Now:            s.now(),
	})
}

// GetSubscription 读订阅实例。
func (s *Service) GetSubscription(ctx context.Context, tenantID, subscriptionID int64) (UserSubscription, error) {
	if tenantID <= 0 || subscriptionID <= 0 {
		return UserSubscription{}, ErrInvalidInput
	}
	return s.store.GetSubscription(ctx, tenantID, subscriptionID)
}

// ListUserSubscriptions 列某用户全部订阅 (任意状态)。
func (s *Service) ListUserSubscriptions(ctx context.Context, tenantID, userID int64) ([]UserSubscription, error) {
	if tenantID <= 0 || userID <= 0 {
		return nil, ErrInvalidInput
	}
	return s.store.ListUserSubscriptions(ctx, tenantID, userID)
}

// SetAutoRenew 设置某用户当前 active 订阅的续订开关; false 只关续订, 不取消当前权益。
func (s *Service) SetAutoRenew(ctx context.Context, tenantID, userID int64, autoRenew bool) (UserSubscription, error) {
	if tenantID <= 0 || userID <= 0 {
		return UserSubscription{}, ErrInvalidInput
	}
	return s.store.SetAutoRenew(ctx, tenantID, userID, autoRenew)
}

// ListAuditEvents 列某订阅操作审计轨迹。
func (s *Service) ListAuditEvents(ctx context.Context, tenantID, subscriptionID int64) ([]AuditEvent, error) {
	if tenantID <= 0 || subscriptionID <= 0 {
		return nil, ErrInvalidInput
	}
	return s.store.ListAuditEvents(ctx, tenantID, subscriptionID)
}

// ProcessDueExpiries 扫一批到点订阅并逐条到期 (system 触发); 返回成功处理条数。
// 单条失败不阻断其余 (记 lastErr 返回), 由 worker 下个 tick 重试。
func (s *Service) ProcessDueExpiries(ctx context.Context, limit int) (int, error) {
	now := s.now()
	due, err := s.store.ListDueExpiry(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	var lastErr error
	for _, sub := range due {
		if _, err := s.store.ExpireSubscription(ctx, lifecycleRecord{
			TenantID:       sub.TenantID,
			SubscriptionID: sub.ID,
			ActorKind:      ActorKindSystem,
			Now:            s.now(),
		}); err != nil {
			lastErr = err
			continue
		}
		processed++
	}
	return processed, lastErr
}

func normalizeCurrency(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	if c == "" {
		return "USD"
	}
	return c
}

// capNonNegative: 未设 (nil) 视为合法 (不设限); 设了则必须 >= 0。
func capNonNegative(d *decimal.Decimal) bool {
	return d == nil || !d.IsNegative()
}
