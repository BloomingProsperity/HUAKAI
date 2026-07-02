// HUAKAI · iKun

package subscription

import (
	"context"
	"sort"
	"sync"
	"time"
)

// memoryStore 是订阅 Store 的内存实现, 用于 service 层逻辑/校验单测 (快速、无 PG)。
// 它忠实镜像 PG 的关键语义 (按 granted_group 幂等 / 分组升级 / 到期降级守卫 / 状态机),
// 但不装真 quota_policies (仅记 link 占位) —— 配额强制、并发、跨租户 FK 等真风险由 PG 集成测试覆盖。
type memoryStore struct {
	mu         sync.Mutex
	planSeq    int64
	subSeq     int64
	policySeq  int64
	auditSeq   int64
	plans      map[planKey]Plan
	subs       map[subKey]UserSubscription
	links      map[int64][]PolicyLink
	audits     map[int64][]AuditEvent
	users      map[userKey]bool
	userGroups map[userKey]string
	userEmails map[userKey]string
	reminders  map[reminderMemKey]reminderRecord
}

type planKey struct{ tenant, id int64 }
type subKey struct{ tenant, id int64 }
type userKey struct{ tenant, user int64 }
type reminderMemKey struct {
	tenant, sub int64
	key         string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		plans:      map[planKey]Plan{},
		subs:       map[subKey]UserSubscription{},
		links:      map[int64][]PolicyLink{},
		audits:     map[int64][]AuditEvent{},
		users:      map[userKey]bool{},
		userGroups: map[userKey]string{},
		userEmails: map[userKey]string{},
		reminders:  map[reminderMemKey]reminderRecord{},
	}
}

// setUserEmail 预置用户邮箱 (镜像 users.email), 供提醒测试。空串表示无收件人。
func (m *memoryStore) setUserEmail(tenantID, userID int64, email string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userEmails[userKey{tenantID, userID}] = email
}

var _ Store = (*memoryStore)(nil)

// seedUser 注册一个用户 (镜像 PG 的 users 行存在 + 当前分组), 供测试预置。
func (m *memoryStore) seedUser(tenantID, userID int64, group string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if group == "" {
		group = DefaultUserGroup
	}
	m.users[userKey{tenantID, userID}] = true
	m.userGroups[userKey{tenantID, userID}] = group
}

func (m *memoryStore) userGroupOf(k userKey) string {
	if g, ok := m.userGroups[k]; ok {
		return g
	}
	return DefaultUserGroup
}

func (m *memoryStore) CreatePlan(_ context.Context, rec createPlanRecord) (Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.planSeq++
	plan := Plan{
		ID:            m.planSeq,
		TenantID:      rec.TenantID,
		Name:          rec.Name,
		Description:   rec.Description,
		PriceCents:    rec.PriceCents,
		CurrencyCode:  rec.CurrencyCode,
		ValidityDays:  rec.ValidityDays,
		GrantedGroup:  rec.GrantedGroup,
		DailyCapUSD:   rec.DailyCapUSD,
		WeeklyCapUSD:  rec.WeeklyCapUSD,
		MonthlyCapUSD: rec.MonthlyCapUSD,
		ForSale:       rec.ForSale,
		Enabled:       true,
		SortOrder:     rec.SortOrder,
		CreatedAt:     rec.Now.UTC(),
		UpdatedAt:     rec.Now.UTC(),
	}
	m.plans[planKey{rec.TenantID, plan.ID}] = plan
	return plan, nil
}

func (m *memoryStore) GetPlan(_ context.Context, tenantID, planID int64) (Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	plan, ok := m.plans[planKey{tenantID, planID}]
	if !ok {
		return Plan{}, ErrPlanNotFound
	}
	return plan, nil
}

func (m *memoryStore) ListPlans(_ context.Context, tenantID int64, onlyForSale bool) ([]Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Plan
	for k, p := range m.plans {
		if k.tenant != tenantID {
			continue
		}
		if onlyForSale && (!p.ForSale || !p.Enabled) {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *memoryStore) DisablePlan(_ context.Context, tenantID, planID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := planKey{tenantID, planID}
	plan, ok := m.plans[k]
	if !ok {
		return ErrPlanNotFound
	}
	plan.Enabled = false
	plan.ForSale = false
	m.plans[k] = plan
	return nil
}

func (m *memoryStore) UpdatePlan(_ context.Context, rec updatePlanRecord) (Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := planKey{rec.TenantID, rec.PlanID}
	plan, ok := m.plans[k]
	if !ok {
		return Plan{}, ErrPlanNotFound
	}
	plan.Name = rec.Name
	plan.Description = rec.Description
	plan.PriceCents = rec.PriceCents
	plan.CurrencyCode = rec.CurrencyCode
	plan.ValidityDays = rec.ValidityDays
	plan.GrantedGroup = rec.GrantedGroup
	plan.DailyCapUSD = rec.DailyCapUSD
	plan.WeeklyCapUSD = rec.WeeklyCapUSD
	plan.MonthlyCapUSD = rec.MonthlyCapUSD
	plan.ForSale = rec.ForSale
	plan.SortOrder = rec.SortOrder
	plan.UpdatedAt = rec.Now.UTC()
	m.plans[k] = plan
	return plan, nil
}

func (m *memoryStore) AssignSubscription(_ context.Context, rec assignRecord) (AssignResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	plan, ok := m.plans[planKey{rec.TenantID, rec.PlanID}]
	if !ok {
		return AssignResult{}, ErrPlanNotFound
	}
	if !plan.Enabled {
		return AssignResult{}, ErrPlanDisabled
	}
	uk := userKey{rec.TenantID, rec.UserID}
	if !m.users[uk] {
		return AssignResult{}, ErrInvalidInput
	}

	// 幂等: 同 (tenant, user, granted_group) 已有 active。
	if existing, found := m.findActiveByGroup(rec.TenantID, rec.UserID, plan.GrantedGroup); found {
		m.appendAudit(existing.ID, AuditEvent{
			TenantID: rec.TenantID, UserSubscriptionID: existing.ID, EventType: AuditIdempotentReplay,
			ActorKind: ActorKindAdmin, ActorID: rec.ActorAdminID, RequestID: rec.RequestID,
			Payload: map[string]any{"plan_id": rec.PlanID}, OccurredAt: rec.Now.UTC(),
		})
		return AssignResult{Subscription: existing, Idempotent: true}, nil
	}

	prevGroup := m.userGroupOf(uk)
	m.subSeq++
	sub := UserSubscription{
		ID: m.subSeq, TenantID: rec.TenantID, UserID: rec.UserID, PlanID: plan.ID,
		GrantedGroup: plan.GrantedGroup, DailyCapUSD: plan.DailyCapUSD, WeeklyCapUSD: plan.WeeklyCapUSD,
		MonthlyCapUSD: plan.MonthlyCapUSD, Status: StatusActive, Source: SourceAdmin,
		AutoRenew: true, AssignedByAdminID: rec.ActorAdminID, PrevUserGroup: prevGroup,
		StartsAt: rec.Now.UTC(), ExpiresAt: rec.Now.AddDate(0, 0, plan.ValidityDays).UTC(),
		CreatedAt: rec.Now.UTC(), UpdatedAt: rec.Now.UTC(),
	}
	m.subs[subKey{rec.TenantID, sub.ID}] = sub

	for _, cap := range sub.Caps() {
		m.policySeq++
		m.links[sub.ID] = append(m.links[sub.ID], PolicyLink{
			ID: m.policySeq, TenantID: rec.TenantID, UserSubscriptionID: sub.ID,
			QuotaPolicyID: m.policySeq, WindowKind: string(cap.Window), Status: "active",
			CreatedAt: rec.Now.UTC(),
		})
	}

	if plan.GrantedGroup != "" && plan.GrantedGroup != prevGroup {
		m.userGroups[uk] = plan.GrantedGroup
		m.appendAudit(sub.ID, AuditEvent{
			TenantID: rec.TenantID, UserSubscriptionID: sub.ID, EventType: AuditGroupUpgraded,
			ActorKind: ActorKindAdmin, ActorID: rec.ActorAdminID, RequestID: rec.RequestID,
			Payload: map[string]any{"from": prevGroup, "to": plan.GrantedGroup}, OccurredAt: rec.Now.UTC(),
		})
	}
	m.appendAudit(sub.ID, AuditEvent{
		TenantID: rec.TenantID, UserSubscriptionID: sub.ID, EventType: AuditSubscriptionCreated,
		ActorKind: ActorKindAdmin, ActorID: rec.ActorAdminID, RequestID: rec.RequestID,
		Payload: assignAuditPayload(sub), OccurredAt: rec.Now.UTC(),
	})
	return AssignResult{Subscription: sub, Idempotent: false}, nil
}

func (m *memoryStore) GetSubscription(_ context.Context, tenantID, subscriptionID int64) (UserSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sub, ok := m.subs[subKey{tenantID, subscriptionID}]
	if !ok {
		return UserSubscription{}, ErrSubscriptionNotFound
	}
	return sub, nil
}

func (m *memoryStore) ListUserSubscriptions(_ context.Context, tenantID, userID int64) ([]UserSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []UserSubscription
	for k, sub := range m.subs {
		if k.tenant == tenantID && sub.UserID == userID {
			out = append(out, sub)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (m *memoryStore) ListUserSubscriptionsByGroup(_ context.Context, tenantID int64, grantedGroup string, limit int) ([]UserSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []UserSubscription
	for k, sub := range m.subs {
		if k.tenant == tenantID && sub.GrantedGroup == grantedGroup {
			out = append(out, sub)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memoryStore) SetAutoRenew(_ context.Context, tenantID, userID int64, autoRenew bool) (UserSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sub, found := m.currentActiveSubscriptionLocked(tenantID, userID)
	if !found {
		return UserSubscription{}, ErrSubscriptionNotFound
	}
	sub.AutoRenew = autoRenew
	sub.UpdatedAt = time.Now().UTC()
	m.subs[subKey{tenantID, sub.ID}] = sub
	return sub, nil
}

func (m *memoryStore) CancelSubscription(_ context.Context, rec lifecycleRecord) (UserSubscription, error) {
	return m.closeMem(rec, StatusCancelled, AuditCancelled)
}

func (m *memoryStore) ExpireSubscription(_ context.Context, rec lifecycleRecord) (UserSubscription, error) {
	return m.closeMem(rec, StatusExpired, AuditExpired)
}

func (m *memoryStore) closeMem(rec lifecycleRecord, terminal SubscriptionStatus, event string) (UserSubscription, error) {
	return m.closeMemWithAudit(rec, terminal, event, "", nil, false)
}

func (m *memoryStore) closeMemWithAudit(rec lifecycleRecord, terminal SubscriptionStatus, event, reason string, payload map[string]any, revokeStrict bool) (UserSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := subKey{rec.TenantID, rec.SubscriptionID}
	sub, ok := m.subs[k]
	if !ok {
		return UserSubscription{}, ErrSubscriptionNotFound
	}
	if sub.Status != StatusActive {
		if revokeStrict && sub.Status != StatusRevoked {
			return UserSubscription{}, ErrSubscriptionNotActive
		}
		return sub, nil // 幂等
	}
	if revokeStrict && !sub.ExpiresAt.After(rec.Now) {
		return UserSubscription{}, ErrSubscriptionNotActive
	}
	sub.Status = terminal
	sub.UpdatedAt = rec.Now.UTC()
	if terminal == StatusCancelled {
		t := rec.Now.UTC()
		sub.CancelledAt = &t
	}
	m.subs[k] = sub

	for i := range m.links[sub.ID] {
		if m.links[sub.ID][i].Status == "active" {
			m.links[sub.ID][i].Status = "closed"
			t := rec.Now.UTC()
			m.links[sub.ID][i].ClosedAt = &t
		}
	}

	if sub.GrantedGroup != "" {
		uk := userKey{rec.TenantID, sub.UserID}
		current := m.userGroupOf(uk)
		if current == sub.GrantedGroup {
			// 从剩余 active 订阅重算目标组 (镜像 PG resolveGroupFromActiveTx), 无则 default。
			target := m.resolveGroupFromActive(rec.TenantID, sub.UserID)
			if target != current {
				m.userGroups[uk] = target
				m.appendAudit(sub.ID, AuditEvent{
					TenantID: rec.TenantID, UserSubscriptionID: sub.ID, EventType: AuditGroupDowngraded,
					ActorKind: actorKindOrDefault(rec.ActorKind), ActorID: rec.ActorID, RequestID: rec.RequestID,
					Payload: map[string]any{"from": current, "to": target}, OccurredAt: rec.Now.UTC(),
				})
			}
		}
	}
	m.appendAudit(sub.ID, AuditEvent{
		TenantID: rec.TenantID, UserSubscriptionID: sub.ID, EventType: event,
		ActorKind: actorKindOrDefault(rec.ActorKind), ActorID: rec.ActorID, RequestID: rec.RequestID,
		ReasonClass: reason, Payload: payload, OccurredAt: rec.Now.UTC(),
	})
	return sub, nil
}

func (m *memoryStore) ExtendSubscription(_ context.Context, rec extendRecord) (UserSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := subKey{rec.TenantID, rec.SubscriptionID}
	sub, ok := m.subs[k]
	if !ok {
		return UserSubscription{}, ErrSubscriptionNotFound
	}
	if rec.RequestID != "" && m.hasAuditRequestLocked(sub.ID, AuditSubscriptionExtended, rec.RequestID) {
		return sub, nil
	}
	if sub.Status != StatusActive || !sub.ExpiresAt.After(rec.Now) {
		return UserSubscription{}, ErrSubscriptionNotActive
	}
	var newExpires time.Time
	if rec.Until != nil {
		newExpires = rec.Until.UTC()
	} else {
		newExpires = sub.ExpiresAt.AddDate(0, 0, rec.Days)
	}
	newExpires = capExpiry(newExpires)
	if !newExpires.After(sub.ExpiresAt) {
		return UserSubscription{}, ErrInvalidInput
	}
	prev := sub.ExpiresAt
	sub.ExpiresAt = newExpires.UTC()
	sub.UpdatedAt = rec.Now.UTC()
	m.subs[k] = sub
	m.appendAudit(sub.ID, AuditEvent{
		TenantID: rec.TenantID, UserSubscriptionID: sub.ID, EventType: AuditSubscriptionExtended,
		ActorKind: ActorKindAdmin, ActorID: rec.ActorAdminID, RequestID: rec.RequestID,
		Payload:    map[string]any{"from_expires": prev.UTC(), "to_expires": newExpires.UTC()},
		OccurredAt: rec.Now.UTC(),
	})
	return sub, nil
}

func (m *memoryStore) ResetQuota(_ context.Context, rec lifecycleRecord) (UserSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := subKey{rec.TenantID, rec.SubscriptionID}
	sub, ok := m.subs[k]
	if !ok {
		return UserSubscription{}, ErrSubscriptionNotFound
	}
	if rec.RequestID != "" && m.hasAuditRequestLocked(sub.ID, AuditSubscriptionQuotaReset, rec.RequestID) {
		return sub, nil
	}
	if sub.Status != StatusActive || !sub.ExpiresAt.After(rec.Now) {
		return UserSubscription{}, ErrSubscriptionNotActive
	}
	for i := range m.links[sub.ID] {
		if m.links[sub.ID][i].Status == "active" {
			m.links[sub.ID][i].Status = "closed"
			t := rec.Now.UTC()
			m.links[sub.ID][i].ClosedAt = &t
		}
	}
	for _, cap := range sub.Caps() {
		m.policySeq++
		m.links[sub.ID] = append(m.links[sub.ID], PolicyLink{
			ID: m.policySeq, TenantID: rec.TenantID, UserSubscriptionID: sub.ID,
			QuotaPolicyID: m.policySeq, WindowKind: string(cap.Window), Status: "active",
			CreatedAt: rec.Now.UTC(),
		})
	}
	sub.UpdatedAt = rec.Now.UTC()
	m.subs[k] = sub
	m.appendAudit(sub.ID, AuditEvent{
		TenantID: rec.TenantID, UserSubscriptionID: sub.ID, EventType: AuditSubscriptionQuotaReset,
		ActorKind: actorKindOrDefault(rec.ActorKind), ActorID: rec.ActorID, RequestID: rec.RequestID,
		OccurredAt: rec.Now.UTC(),
	})
	return sub, nil
}

func (m *memoryStore) ChangePlan(_ context.Context, rec changePlanRecord) (UserSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := UserSubscription{}, false
	if rec.SubscriptionID > 0 {
		sub, ok = m.subs[subKey{rec.TenantID, rec.SubscriptionID}]
	} else {
		sub, ok = m.currentActiveSubscriptionLocked(rec.TenantID, rec.UserID)
	}
	if !ok {
		return UserSubscription{}, ErrSubscriptionNotFound
	}
	if rec.RequestID != "" && m.hasAuditRequestLocked(sub.ID, AuditSubscriptionRenewed, rec.RequestID) {
		return sub, nil
	}
	if sub.Status != StatusActive || !sub.ExpiresAt.After(rec.Now) {
		return UserSubscription{}, ErrSubscriptionNotActive
	}
	plan, ok := m.plans[planKey{rec.TenantID, rec.NewPlanID}]
	if !ok {
		return UserSubscription{}, ErrPlanNotFound
	}
	if !plan.Enabled {
		return UserSubscription{}, ErrPlanDisabled
	}
	if !rec.AllowDowngrade && !capsDominate(planCapsTriple(plan), subCapsTriple(sub)) {
		return UserSubscription{}, ErrDowngradeNotAllowed
	}

	prevPlanID := sub.PlanID
	prevExpires := sub.ExpiresAt
	prevGroup := sub.GrantedGroup
	before := sub
	uk := userKey{rec.TenantID, sub.UserID}
	currentGroup := m.userGroupOf(uk)
	base := rec.Now
	if sub.ExpiresAt.After(base) {
		base = sub.ExpiresAt
	}
	newExpires := capExpiry(base.AddDate(0, 0, plan.ValidityDays))

	for i := range m.links[sub.ID] {
		if m.links[sub.ID][i].Status == "active" {
			m.links[sub.ID][i].Status = "closed"
			t := rec.Now.UTC()
			m.links[sub.ID][i].ClosedAt = &t
		}
	}
	sub.PlanID = plan.ID
	sub.GrantedGroup = plan.GrantedGroup
	sub.DailyCapUSD = plan.DailyCapUSD
	sub.WeeklyCapUSD = plan.WeeklyCapUSD
	sub.MonthlyCapUSD = plan.MonthlyCapUSD
	sub.ExpiresAt = newExpires.UTC()
	sub.UpdatedAt = rec.Now.UTC()
	m.subs[subKey{rec.TenantID, sub.ID}] = sub
	for _, cap := range sub.Caps() {
		m.policySeq++
		m.links[sub.ID] = append(m.links[sub.ID], PolicyLink{
			ID: m.policySeq, TenantID: rec.TenantID, UserSubscriptionID: sub.ID,
			QuotaPolicyID: m.policySeq, WindowKind: string(cap.Window), Status: "active",
			CreatedAt: rec.Now.UTC(),
		})
	}

	actorKind, actorID, _ := changePlanActor(rec, sub) // 内存实现不落 actor_ref 列,丢弃
	if plan.GrantedGroup != prevGroup {
		currentOwnedByTarget := false
		if prevGroup != "" {
			currentOwnedByTarget = currentGroup == prevGroup
		} else {
			currentOwnedByTarget = currentGroup == DefaultUserGroup
		}
		if currentOwnedByTarget {
			targetGroup := plan.GrantedGroup
			if targetGroup == "" {
				targetGroup = m.resolveGroupFromActive(rec.TenantID, sub.UserID)
			}
			if targetGroup != currentGroup {
				m.userGroups[uk] = targetGroup
				eventType := AuditGroupUpgraded
				if targetGroup == DefaultUserGroup || !capsDominate(planCapsTriple(plan), subCapsTriple(before)) {
					eventType = AuditGroupDowngraded
				}
				m.appendAudit(sub.ID, AuditEvent{
					TenantID: rec.TenantID, UserSubscriptionID: sub.ID, EventType: eventType,
					ActorKind: actorKind, ActorID: actorID, RequestID: rec.RequestID,
					Payload: map[string]any{"from": currentGroup, "to": targetGroup}, OccurredAt: rec.Now.UTC(),
				})
			}
		}
	}
	m.appendAudit(sub.ID, AuditEvent{
		TenantID: rec.TenantID, UserSubscriptionID: sub.ID, EventType: AuditSubscriptionRenewed,
		ActorKind: actorKind, ActorID: actorID, RequestID: rec.RequestID,
		Payload: map[string]any{
			"source":          "change_plan",
			"from_plan_id":    prevPlanID,
			"to_plan_id":      plan.ID,
			"from_expires":    prevExpires.UTC(),
			"to_expires":      newExpires.UTC(),
			"allow_downgrade": rec.AllowDowngrade,
		},
		OccurredAt: rec.Now.UTC(),
	})
	return sub, nil
}

func (m *memoryStore) RevokeSubscription(_ context.Context, rec revokeRecord) (UserSubscription, error) {
	return m.closeMemWithAudit(lifecycleRecord{
		TenantID: rec.TenantID, SubscriptionID: rec.SubscriptionID, ActorKind: ActorKindAdmin,
		ActorID: rec.ActorAdminID, RequestID: rec.RequestID, Now: rec.Now,
	}, StatusRevoked, AuditSubscriptionRevoked, rec.Reason, map[string]any{"reason": rec.Reason}, true)
}

func (m *memoryStore) ListDueExpiry(_ context.Context, now time.Time, limit int) ([]UserSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	var out []UserSubscription
	for _, sub := range m.subs {
		if sub.Status == StatusActive && !sub.ExpiresAt.After(now) {
			out = append(out, sub)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ExpiresAt.Equal(out[j].ExpiresAt) {
			return out[i].ExpiresAt.Before(out[j].ExpiresAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListAutoRenewDue 内存实现: 过滤 active + auto_renew=true + 到点 (expires_at<=now)。
// 与 PG 同语义的过滤逻辑可在此被单测覆盖 (mutation: 去掉 auto_renew 过滤 → 含 false 行 → 红)。
func (m *memoryStore) ListAutoRenewDue(_ context.Context, now time.Time, limit int) ([]UserSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var out []UserSubscription
	for _, sub := range m.subs {
		if sub.Status == StatusActive && sub.AutoRenew && !sub.ExpiresAt.After(now) {
			out = append(out, sub)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ExpiresAt.Equal(out[j].ExpiresAt) {
			return out[i].ExpiresAt.Before(out[j].ExpiresAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// TryAutoRenewSubscription 内存实现: 钱包余额扣减 + 幂等锚是 PG (user_balances / 唯一索引) 专属,
// 内存无法忠实复现 money 不变量, 故不在此假实现 (假实现会成 §14 非判别测试)。返回 not_due 跳过,
// 续费 money 路径以 integration_pg 真 PG 测试为准。
func (m *memoryStore) TryAutoRenewSubscription(_ context.Context, _ autoRenewRecord) (AutoRenewResult, error) {
	return AutoRenewResult{Renewed: false, SkipReason: AutoRenewSkipNotDue}, nil
}

func (m *memoryStore) ListAuditEvents(_ context.Context, tenantID, subscriptionID int64) ([]AuditEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AuditEvent
	for _, ev := range m.audits[subscriptionID] {
		if ev.TenantID == tenantID {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (m *memoryStore) ListDueReminder(_ context.Context, now time.Time, within time.Duration, after ReminderCursor, limit int) ([]ReminderCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	if within <= 0 {
		within = 7 * 24 * time.Hour
	}
	upper := now.Add(within)
	var out []ReminderCandidate
	for k, sub := range m.subs {
		if sub.Status != StatusActive {
			continue
		}
		// 镜像 PG: expires_at 在 (now, now+within]。
		if !sub.ExpiresAt.After(now) || sub.ExpiresAt.After(upper) {
			continue
		}
		// 镜像 PG 行值游标 (expires_at, id) > (after.ExpiresAt, after.ID)。
		if !afterCursor(sub.ExpiresAt, sub.ID, after) {
			continue
		}
		uk := userKey{k.tenant, sub.UserID}
		if !m.users[uk] { // 镜像 INNER JOIN users (已删/不存在用户跳过)
			continue
		}
		out = append(out, ReminderCandidate{
			TenantID:       k.tenant,
			SubscriptionID: sub.ID,
			UserID:         sub.UserID,
			ExpiresAt:      sub.ExpiresAt.UTC(),
			RecipientEmail: m.userEmails[uk],
			PlanName:       m.plans[planKey{k.tenant, sub.PlanID}].Name,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ExpiresAt.Equal(out[j].ExpiresAt) {
			return out[i].ExpiresAt.Before(out[j].ExpiresAt)
		}
		return out[i].SubscriptionID < out[j].SubscriptionID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// afterCursor 判断 (expiresAt, id) 是否严格大于游标 (镜像 PG 行值比较)。
func afterCursor(expiresAt time.Time, id int64, after ReminderCursor) bool {
	if expiresAt.After(after.ExpiresAt) {
		return true
	}
	if expiresAt.Equal(after.ExpiresAt) {
		return id > after.ID
	}
	return false
}

func (m *memoryStore) SentReminderKeys(_ context.Context, tenantID, subscriptionID int64) (map[string]struct{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]struct{})
	for k := range m.reminders {
		if k.tenant == tenantID && k.sub == subscriptionID {
			out[k.key] = struct{}{}
		}
	}
	return out, nil
}

func (m *memoryStore) RecordReminder(_ context.Context, rec reminderRecord) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := reminderMemKey{rec.TenantID, rec.SubscriptionID, rec.ReminderKey}
	if _, exists := m.reminders[k]; exists {
		return false, nil // ON CONFLICT DO NOTHING
	}
	m.reminders[k] = rec
	return true, nil
}

// resolveGroupFromActive 返回剩余 active 订阅中最新 (starts_at 最晚, 平局取大 ID) 的非空 granted_group; 无则 default。
func (m *memoryStore) resolveGroupFromActive(tenantID, userID int64) string {
	best := UserSubscription{}
	found := false
	for k, sub := range m.subs {
		if k.tenant != tenantID || sub.UserID != userID || sub.Status != StatusActive || sub.GrantedGroup == "" {
			continue
		}
		if !found || sub.StartsAt.After(best.StartsAt) || (sub.StartsAt.Equal(best.StartsAt) && sub.ID > best.ID) {
			best = sub
			found = true
		}
	}
	if !found {
		return DefaultUserGroup
	}
	return best.GrantedGroup
}

func (m *memoryStore) findActiveByGroup(tenantID, userID int64, group string) (UserSubscription, bool) {
	for k, sub := range m.subs {
		if k.tenant == tenantID && sub.UserID == userID && sub.GrantedGroup == group && sub.Status == StatusActive {
			return sub, true
		}
	}
	return UserSubscription{}, false
}

func (m *memoryStore) currentActiveSubscriptionLocked(tenantID, userID int64) (UserSubscription, bool) {
	var best UserSubscription
	found := false
	for k, sub := range m.subs {
		if k.tenant != tenantID || sub.UserID != userID || sub.Status != StatusActive {
			continue
		}
		if !found || sub.ExpiresAt.After(best.ExpiresAt) || (sub.ExpiresAt.Equal(best.ExpiresAt) && sub.ID > best.ID) {
			best = sub
			found = true
		}
	}
	return best, found
}

func (m *memoryStore) appendAudit(subID int64, ev AuditEvent) {
	m.auditSeq++
	ev.ID = m.auditSeq
	m.audits[subID] = append(m.audits[subID], ev)
}

func (m *memoryStore) hasAuditRequestLocked(subID int64, eventType, requestID string) bool {
	for _, ev := range m.audits[subID] {
		if ev.EventType == eventType && ev.RequestID == requestID {
			return true
		}
	}
	return false
}
