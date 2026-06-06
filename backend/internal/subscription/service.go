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
	fields, err := normalizePlanFields(planFieldsInput{
		Name:          in.Name,
		Description:   in.Description,
		PriceCents:    in.PriceCents,
		CurrencyCode:  in.CurrencyCode,
		ValidityDays:  in.ValidityDays,
		GrantedGroup:  in.GrantedGroup,
		DailyCapUSD:   in.DailyCapUSD,
		WeeklyCapUSD:  in.WeeklyCapUSD,
		MonthlyCapUSD: in.MonthlyCapUSD,
		ForSale:       in.ForSale,
		SortOrder:     in.SortOrder,
	})
	if err != nil {
		return Plan{}, err
	}
	return s.store.CreatePlan(ctx, createPlanRecord{
		TenantID:      in.TenantID,
		Name:          fields.Name,
		Description:   fields.Description,
		PriceCents:    fields.PriceCents,
		CurrencyCode:  fields.CurrencyCode,
		ValidityDays:  fields.ValidityDays,
		GrantedGroup:  fields.GrantedGroup,
		DailyCapUSD:   fields.DailyCapUSD,
		WeeklyCapUSD:  fields.WeeklyCapUSD,
		MonthlyCapUSD: fields.MonthlyCapUSD,
		ForSale:       fields.ForSale,
		SortOrder:     fields.SortOrder,
		Now:           s.now(),
	})
}

// UpdatePlan updates mutable admin-owned plan catalog fields. Existing
// user_subscriptions keep their plan snapshot.
func (s *Service) UpdatePlan(ctx context.Context, in UpdatePlanInput) (Plan, error) {
	if in.TenantID <= 0 || in.PlanID <= 0 {
		return Plan{}, ErrInvalidInput
	}
	fields, err := normalizePlanFields(planFieldsInput{
		Name:          in.Name,
		Description:   in.Description,
		PriceCents:    in.PriceCents,
		CurrencyCode:  in.CurrencyCode,
		ValidityDays:  in.ValidityDays,
		GrantedGroup:  in.GrantedGroup,
		DailyCapUSD:   in.DailyCapUSD,
		WeeklyCapUSD:  in.WeeklyCapUSD,
		MonthlyCapUSD: in.MonthlyCapUSD,
		ForSale:       in.ForSale,
		SortOrder:     in.SortOrder,
	})
	if err != nil {
		return Plan{}, err
	}
	return s.store.UpdatePlan(ctx, updatePlanRecord{
		TenantID:      in.TenantID,
		PlanID:        in.PlanID,
		Name:          fields.Name,
		Description:   fields.Description,
		PriceCents:    fields.PriceCents,
		CurrencyCode:  fields.CurrencyCode,
		ValidityDays:  fields.ValidityDays,
		GrantedGroup:  fields.GrantedGroup,
		DailyCapUSD:   fields.DailyCapUSD,
		WeeklyCapUSD:  fields.WeeklyCapUSD,
		MonthlyCapUSD: fields.MonthlyCapUSD,
		ForSale:       fields.ForSale,
		SortOrder:     fields.SortOrder,
		ActorAdminID:  in.ActorAdminID,
		RequestID:     strings.TrimSpace(in.RequestID),
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

// BulkAssign grants a plan to many users. Each user is processed independently;
// one failure never rolls back earlier/later successful assignments.
func (s *Service) BulkAssign(ctx context.Context, in BulkAssignInput) (BulkAssignResult, error) {
	if in.TenantID <= 0 || in.PlanID <= 0 || len(in.UserIDs) == 0 {
		return BulkAssignResult{}, ErrInvalidInput
	}
	out := BulkAssignResult{Results: make([]BulkAssignUserResult, 0, len(in.UserIDs))}
	for _, userID := range in.UserIDs {
		item := BulkAssignUserResult{UserID: userID}
		if userID <= 0 {
			item.Error = ErrInvalidInput.Error()
			out.Results = append(out.Results, item)
			continue
		}
		res, err := s.AssignSubscription(ctx, AssignSubscriptionInput{
			TenantID:     in.TenantID,
			UserID:       userID,
			PlanID:       in.PlanID,
			ActorAdminID: in.ActorAdminID,
			RequestID:    strings.TrimSpace(in.RequestID),
		})
		if err != nil {
			item.Error = err.Error()
		} else {
			item.OK = true
			item.Subscription = res.Subscription
			item.Idempotent = res.Idempotent
		}
		out.Results = append(out.Results, item)
	}
	return out, nil
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

// ExtendSubscription pushes an active, non-expired assignment later. Retries
// with the same request_id are no-ops.
func (s *Service) ExtendSubscription(ctx context.Context, in ExtendSubscriptionInput) (UserSubscription, error) {
	if in.TenantID <= 0 || in.SubscriptionID <= 0 {
		return UserSubscription{}, ErrInvalidInput
	}
	hasDays := in.Days > 0
	hasUntil := in.Until != nil
	if hasDays == hasUntil {
		return UserSubscription{}, ErrInvalidInput
	}
	var until *time.Time
	if in.Until != nil {
		u := in.Until.UTC()
		until = &u
	}
	return s.store.ExtendSubscription(ctx, extendRecord{
		TenantID:       in.TenantID,
		SubscriptionID: in.SubscriptionID,
		ActorAdminID:   in.ActorAdminID,
		RequestID:      strings.TrimSpace(in.RequestID),
		Days:           in.Days,
		Until:          until,
		Now:            s.now(),
	})
}

// ResetQuota clears current quota consumption by rebuilding the subscription's
// active quota policies from its stored plan snapshot.
func (s *Service) ResetQuota(ctx context.Context, in ResetQuotaInput) (UserSubscription, error) {
	if in.TenantID <= 0 || in.SubscriptionID <= 0 {
		return UserSubscription{}, ErrInvalidInput
	}
	return s.store.ResetQuota(ctx, lifecycleRecord{
		TenantID:       in.TenantID,
		SubscriptionID: in.SubscriptionID,
		ActorKind:      ActorKindAdmin,
		ActorID:        in.ActorAdminID,
		RequestID:      strings.TrimSpace(in.RequestID),
		Now:            s.now(),
	})
}

// RevokeSubscription hard-ends an active assignment and closes entitlements.
func (s *Service) RevokeSubscription(ctx context.Context, in RevokeSubscriptionInput) (UserSubscription, error) {
	if in.TenantID <= 0 || in.SubscriptionID <= 0 {
		return UserSubscription{}, ErrInvalidInput
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return UserSubscription{}, ErrInvalidInput
	}
	return s.store.RevokeSubscription(ctx, revokeRecord{
		TenantID:       in.TenantID,
		SubscriptionID: in.SubscriptionID,
		ActorAdminID:   in.ActorAdminID,
		Reason:         reason,
		RequestID:      strings.TrimSpace(in.RequestID),
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

type planFieldsInput struct {
	Name          string
	Description   string
	PriceCents    int64
	CurrencyCode  string
	ValidityDays  int
	GrantedGroup  string
	DailyCapUSD   *decimal.Decimal
	WeeklyCapUSD  *decimal.Decimal
	MonthlyCapUSD *decimal.Decimal
	ForSale       bool
	SortOrder     int
}

func normalizePlanFields(in planFieldsInput) (planFieldsInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return planFieldsInput{}, ErrInvalidInput
	}
	if in.ValidityDays <= 0 || in.ValidityDays > maxValidityDays {
		return planFieldsInput{}, ErrPlanInvalid
	}
	if in.PriceCents < 0 {
		return planFieldsInput{}, ErrPlanInvalid
	}
	if !capNonNegative(in.DailyCapUSD) || !capNonNegative(in.WeeklyCapUSD) || !capNonNegative(in.MonthlyCapUSD) {
		return planFieldsInput{}, ErrPlanInvalid
	}
	in.Description = strings.TrimSpace(in.Description)
	in.CurrencyCode = normalizeCurrency(in.CurrencyCode)
	in.GrantedGroup = strings.TrimSpace(in.GrantedGroup)
	// 套餐必须至少授予一个分组或设一档配额上限, 否则毫无权益意义。
	if in.GrantedGroup == "" && in.DailyCapUSD == nil && in.WeeklyCapUSD == nil && in.MonthlyCapUSD == nil {
		return planFieldsInput{}, ErrPlanInvalid
	}
	return in, nil
}
