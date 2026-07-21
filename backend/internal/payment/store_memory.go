// HUAKAI · iKun

package payment

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MemoryStore 内存支付存储, 仅用于 service 单测与本地开发 — 不得作为钱路径验收依据
// (钱路径正确性靠真 PG 约束 + SERIALIZABLE, 见 store_postgres_integration_test.go)。
type MemoryStore struct {
	mu            sync.Mutex
	orders        map[int64]*Order
	byTrade       map[string]int64
	credits       map[int64]*CreditRecord // key = order id
	refunds       map[int64]*RefundRecord // key = refund id
	refundsByKey  map[string]int64        // key = tenant|idempotency_key, value = refund id
	audits        []AuditEvent
	nextOrderID   int64
	nextCreditID  int64
	nextRefundID  int64
	nextBillingID int64
	nextAuditID   int64
}

// NewMemoryStore 构造内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		orders:       map[int64]*Order{},
		byTrade:      map[string]int64{},
		credits:      map[int64]*CreditRecord{},
		refunds:      map[int64]*RefundRecord{},
		refundsByKey: map[string]int64{},
	}
}

var _ Store = (*MemoryStore)(nil)

func tradeKey(tenantID int64, outTradeNo string) string {
	return stringJoin(tenantID, outTradeNo)
}

func (m *MemoryStore) CreateOrder(_ context.Context, rec createOrderRecord) (Order, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := tradeKey(rec.TenantID, rec.OutTradeNo)
	if id, ok := m.byTrade[key]; ok {
		existing := m.orders[id]
		if existing.AmountCents != rec.AmountCents ||
			existing.UserID != rec.UserID ||
			existing.ProviderKind != providerKindOrDefault(rec.ProviderKind) ||
			existing.RequestFingerprint != strings.TrimSpace(rec.RequestFingerprint) ||
			existing.OrderKind != orderKindOrDefault(rec.OrderKind) ||
			!sameOptionalInt64(existing.SubscriptionPlanID, rec.SubscriptionPlanID) ||
			!strings.EqualFold(existing.CurrencyCode, rec.CurrencyCode) {
			return Order{}, false, ErrIdempotencyConflict
		}
		m.appendAudit(rec.TenantID, existing.ID, AuditIdempotentReplay, auditActorKind(rec), auditActorID(rec), "", rec.RequestID)
		return *existing, true, nil
	}
	if rec.RechargeMaxPending > 0 {
		pending := 0
		for _, order := range m.orders {
			if order.TenantID == rec.TenantID && order.UserID == rec.UserID &&
				order.Status == StatusPending && !paymentOrderExpired(order, rec.Now) {
				pending++
			}
		}
		if pending >= rec.RechargeMaxPending {
			return Order{}, false, ErrPendingLimit
		}
	}
	if rec.RechargeDailyLimitCents > 0 {
		start := time.Date(rec.Now.Year(), rec.Now.Month(), rec.Now.Day(), 0, 0, 0, 0, time.UTC)
		used := int64(0)
		for _, order := range m.orders {
			if order.TenantID != rec.TenantID || order.UserID != rec.UserID || order.CreatedAt.Before(start) {
				continue
			}
			switch order.Status {
			case StatusPending:
				if !paymentOrderExpired(order, rec.Now) {
					used += order.AmountCents
				}
			case StatusPaid, StatusRecharging, StatusCompleted:
				used += order.AmountCents
			}
		}
		if used+rec.AmountCents > rec.RechargeDailyLimitCents {
			return Order{}, false, ErrDailyAmountLimit
		}
	}
	m.nextOrderID++
	o := &Order{
		ID:                     m.nextOrderID,
		TenantID:               rec.TenantID,
		UserID:                 rec.UserID,
		OutTradeNo:             rec.OutTradeNo,
		AmountCents:            rec.AmountCents,
		CurrencyCode:           rec.CurrencyCode,
		Status:                 StatusPending,
		ProviderKind:           providerKindOrDefault(rec.ProviderKind),
		ProviderOrderRef:       rec.ProviderOrderRef,
		RequestFingerprint:     rec.RequestFingerprint,
		CreatedByAdminID:       rec.CreatedByAdminID,
		OrderKind:              orderKindOrDefault(rec.OrderKind),
		SubscriptionPlanID:     rec.SubscriptionPlanID,
		ComplianceTermsVersion: rec.ComplianceTermsVersion,
		ComplianceAcceptedAt:   rec.ComplianceAcceptedAt,
		ComplianceAcceptedBy:   rec.ComplianceAcceptedBy,
		ComplianceAcceptedIP:   rec.ComplianceAcceptedIP,
		CreatedAt:              rec.Now,
		UpdatedAt:              rec.Now,
		ExpiresAt:              rec.ExpiresAt,
	}
	m.orders[o.ID] = o
	m.byTrade[key] = o.ID
	m.appendAudit(rec.TenantID, o.ID, AuditOrderCreated, auditActorKind(rec), auditActorID(rec), "", rec.RequestID)
	return *o, false, nil
}

func (m *MemoryStore) GetOrder(_ context.Context, tenantID, orderID int64) (Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.orders[orderID]
	if o == nil || o.TenantID != tenantID {
		return Order{}, ErrOrderNotFound
	}
	return *o, nil
}

func (m *MemoryStore) GetOrderByOutTradeNo(_ context.Context, tenantID int64, outTradeNo string) (Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byTrade[tradeKey(tenantID, outTradeNo)]
	if !ok {
		return Order{}, ErrOrderNotFound
	}
	return *m.orders[id], nil
}

func (m *MemoryStore) ConfirmPaid(_ context.Context, rec confirmRecord) (Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.orders[rec.OrderID]
	if o == nil || o.TenantID != rec.TenantID {
		return Order{}, ErrOrderNotFound
	}
	switch o.Status {
	case StatusPending:
		if o.ExpiresAt != nil && o.ExpiresAt.Before(rec.Now) {
			o.Status = StatusExpired
			o.UpdatedAt = rec.Now
			m.appendAudit(rec.TenantID, o.ID, AuditOrderExpired, ActorKindSystem, 0, "", rec.RequestID)
			return Order{}, ErrOrderNotConfirmable
		}
		o.Status = StatusPaid
		t := rec.Now
		o.PaidAt = &t
		o.ConfirmedByAdminID = rec.AdminID
		o.ConfirmReason = rec.ConfirmReason
		o.UpdatedAt = rec.Now
		m.appendAudit(rec.TenantID, o.ID, AuditPaidConfirmed, actorKindOrDefault(rec.ActorKind), rec.AdminID, rec.ConfirmReason, rec.RequestID)
	case StatusPaid, StatusRecharging, StatusCompleted:
		// 幂等
	default:
		return Order{}, ErrOrderNotConfirmable
	}
	return *o, nil
}

func (m *MemoryStore) CancelOrder(_ context.Context, rec cancelRecord) (Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.orders[rec.OrderID]
	if o == nil || o.TenantID != rec.TenantID {
		return Order{}, ErrOrderNotFound
	}
	if rec.UserID > 0 && o.UserID != rec.UserID {
		return Order{}, ErrOrderNotFound
	}
	switch o.Status {
	case StatusPending:
		o.Status = StatusCancelled
		o.UpdatedAt = rec.Now
		m.appendAudit(rec.TenantID, o.ID, AuditOrderCancelled, actorKindOrDefault(rec.ActorKind), rec.ActorID, rec.Reason, rec.RequestID)
	case StatusCancelled:
		// 幂等
	default:
		return Order{}, ErrOrderNotCancelable
	}
	return *o, nil
}

func (m *MemoryStore) RefundOrder(_ context.Context, rec refundRecord) (RefundResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	refundTotalForOrder := func(tenantID, orderID int64) int64 {
		var total int64
		for _, refund := range m.refunds {
			if refund.TenantID == tenantID && refund.OrderID == orderID {
				total += refund.AmountCents
			}
		}
		return total
	}
	if strings.TrimSpace(rec.IdempotencyKey) == "" {
		return RefundResult{}, ErrInvalidInput
	}
	key := stringJoin(rec.TenantID, rec.IdempotencyKey)
	if existingRefundID, ok := m.refundsByKey[key]; ok {
		refund := m.refunds[existingRefundID]
		if refund == nil {
			return RefundResult{}, ErrRefundFactInvalid
		}
		if !paymentRefundMatchesRequest(*refund, rec) {
			return RefundResult{}, ErrRefundIdempotencyConflict
		}
		order := m.orders[refund.OrderID]
		if order == nil {
			return RefundResult{}, ErrRefundFactInvalid
		}
		credit := m.credits[order.ID]
		if credit == nil || refund.TenantID != order.TenantID || refund.UserID != order.UserID ||
			refund.CurrencyCode != order.CurrencyCode {
			return RefundResult{}, ErrRefundFactInvalid
		}
		total := refundTotalForOrder(order.TenantID, order.ID)
		status, remaining, err := paymentRefundProgress(credit.AmountCents, total)
		if err != nil || order.Status != status || (refund.RequireExact && total < refund.RequestedAmountCents) {
			return RefundResult{}, ErrRefundFactInvalid
		}
		return RefundResult{
			Order: *order, Refund: *refund, BalanceCents: m.balanceLocked(rec.TenantID, refund.UserID),
			CumulativeRefundedCents: total, RemainingRefundableCents: remaining, Idempotent: true,
		}, nil
	}
	o := m.orders[rec.OrderID]
	if o == nil || o.TenantID != rec.TenantID {
		return RefundResult{}, ErrOrderNotFound
	}
	if o.OrderKind != OrderKindTopup {
		return RefundResult{}, ErrRefundUnsupportedKind
	}
	if o.Status != StatusCompleted && o.Status != StatusRefunded {
		return RefundResult{}, ErrOrderNotRefundable
	}
	credit := m.credits[o.ID]
	if credit == nil {
		return RefundResult{}, ErrOrderNotRefundable
	}
	totalBefore := refundTotalForOrder(o.TenantID, o.ID)
	currentStatus, currentRemaining, progressErr := paymentRefundProgress(credit.AmountCents, totalBefore)
	if progressErr != nil || o.Status != currentStatus {
		return RefundResult{}, ErrRefundFactInvalid
	}
	amount, alreadySatisfied, err := paymentRefundAmount(credit.AmountCents, totalBefore, rec.AmountCents, rec.RequireExact)
	if err != nil {
		return RefundResult{}, err
	}
	if alreadySatisfied {
		return RefundResult{
			Order: *o, BalanceCents: m.balanceLocked(rec.TenantID, o.UserID),
			CumulativeRefundedCents: totalBefore, RemainingRefundableCents: currentRemaining,
			Idempotent: true, AlreadySatisfied: true,
		}, nil
	}
	if m.balanceLocked(rec.TenantID, o.UserID) < amount {
		return RefundResult{}, ErrRefundExceedsAvailable
	}

	m.nextRefundID++
	m.nextBillingID++
	refund := &RefundRecord{
		ID:                   m.nextRefundID,
		TenantID:             rec.TenantID,
		OrderID:              o.ID,
		UserID:               o.UserID,
		AmountCents:          amount,
		RequestedAmountCents: rec.AmountCents,
		RequireExact:         rec.RequireExact,
		CurrencyCode:         o.CurrencyCode,
		IdempotencyKey:       rec.IdempotencyKey,
		Reason:               rec.Reason,
		ActorKind:            actorKindOrDefault(rec.ActorKind),
		ActorID:              rec.ActorID,
		ActorRef:             strings.TrimSpace(rec.ActorRef),
		BillingEventID:       m.nextBillingID,
		CreatedAt:            rec.Now,
	}
	m.refunds[refund.ID] = refund
	m.refundsByKey[key] = refund.ID
	totalAfter := totalBefore + amount
	status, remaining, err := paymentRefundProgress(credit.AmountCents, totalAfter)
	if err != nil {
		return RefundResult{}, err
	}
	o.Status = status
	o.UpdatedAt = rec.Now
	m.appendAudit(rec.TenantID, o.ID, AuditOrderRefunded, refund.ActorKind, rec.ActorID, rec.Reason, rec.RequestID)
	return RefundResult{
		Order: *o, Refund: *refund, BalanceCents: m.balanceLocked(rec.TenantID, o.UserID),
		CumulativeRefundedCents: totalAfter, RemainingRefundableCents: remaining,
	}, nil
}

func (m *MemoryStore) BeginFulfill(_ context.Context, rec fulfillRecord) (Order, beginFulfillOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.orders[rec.OrderID]
	if o == nil || o.TenantID != rec.TenantID {
		return Order{}, beginFulfillTransitioned, ErrOrderNotFound
	}
	switch o.Status {
	case StatusPaid:
		o.Status = StatusRecharging
		t := rec.Now
		o.RechargingAt = &t
		o.UpdatedAt = rec.Now
		m.appendAudit(rec.TenantID, o.ID, AuditFulfillmentStarted, actorKindOrDefault(rec.ActorKind), rec.ActorID, "", rec.RequestID)
		return *o, beginFulfillTransitioned, nil
	case StatusRecharging:
		return *o, beginFulfillTransitioned, nil
	case StatusCompleted:
		return *o, beginFulfillAlreadyDone, nil
	default:
		return Order{}, beginFulfillTransitioned, ErrOrderNotFulfillable
	}
}

func (m *MemoryStore) CompleteFulfill(_ context.Context, rec fulfillRecord) (FulfillResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.orders[rec.OrderID]
	if o == nil || o.TenantID != rec.TenantID {
		return FulfillResult{}, ErrOrderNotFound
	}
	if o.OrderKind == OrderKindSubscription {
		// 订阅单履约依赖真订阅/配额表, 内存 store 不镜像 (见 P3b-4 计划 §5 D3); 真路径 PG-only。
		return FulfillResult{}, ErrSubscriptionOrderRequiresPG
	}
	if o.Status == StatusCompleted {
		c := m.credits[o.ID]
		if c == nil {
			return FulfillResult{}, ErrOrderNotFound
		}
		return FulfillResult{Order: *o, Credit: *c, BalanceCents: m.balanceLocked(rec.TenantID, o.UserID), Idempotent: true}, nil
	}
	if o.Status != StatusRecharging {
		return FulfillResult{}, ErrOrderNotFulfillable
	}
	m.nextCreditID++
	m.nextBillingID++
	c := &CreditRecord{
		ID:             m.nextCreditID,
		TenantID:       rec.TenantID,
		OrderID:        o.ID,
		UserID:         o.UserID,
		AmountCents:    o.AmountCents,
		CurrencyCode:   o.CurrencyCode,
		ReasonClass:    reasonClassForProvider(o.ProviderKind),
		BillingEventID: m.nextBillingID,
		CreatedAt:      rec.Now,
	}
	m.credits[o.ID] = c
	o.Status = StatusCompleted
	t := rec.Now
	o.CompletedAt = &t
	o.UpdatedAt = rec.Now
	m.appendAudit(rec.TenantID, o.ID, AuditCredited, actorKindOrDefault(rec.ActorKind), rec.ActorID, "", rec.RequestID)
	return FulfillResult{Order: *o, Credit: *c, BalanceCents: m.balanceLocked(rec.TenantID, o.UserID), Idempotent: false}, nil
}

func (m *MemoryStore) ListOrdersByUser(_ context.Context, tenantID, userID int64, limit int) ([]Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Order
	for _, o := range m.orders {
		if o.TenantID == tenantID && o.UserID == userID {
			out = append(out, *o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) CountPendingOrders(_ context.Context, tenantID, userID int64, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var count int
	for _, o := range m.orders {
		if o.TenantID == tenantID && o.UserID == userID && o.Status == StatusPending && !paymentOrderExpired(o, now) {
			count++
		}
	}
	return count, nil
}

func (m *MemoryStore) SumRechargeAmountSince(_ context.Context, tenantID, userID int64, since, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sum int64
	for _, o := range m.orders {
		if o.TenantID != tenantID || o.UserID != userID || o.CreatedAt.Before(since) {
			continue
		}
		switch o.Status {
		case StatusPending:
			if !paymentOrderExpired(o, now) {
				sum += o.AmountCents
			}
		case StatusPaid, StatusRecharging, StatusCompleted:
			sum += o.AmountCents
		}
	}
	return sum, nil
}

func (m *MemoryStore) UserBalanceCents(_ context.Context, tenantID, userID int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.balanceLocked(tenantID, userID), nil
}

func (m *MemoryStore) ListAuditEvents(_ context.Context, tenantID, orderID int64) ([]AuditEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AuditEvent
	for _, ev := range m.audits {
		if ev.TenantID == tenantID && ev.OrderID == orderID {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (m *MemoryStore) ExpireStalePendingOrders(_ context.Context, now time.Time, limit int) (int, error) {
	if m == nil {
		return 0, ErrStoreNotConfigured
	}
	if limit <= 0 {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int64, 0, len(m.orders))
	for id, o := range m.orders {
		if o == nil || o.Status != StatusPending || o.ExpiresAt == nil || !o.ExpiresAt.Before(now) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > limit {
		ids = ids[:limit]
	}
	for _, id := range ids {
		o := m.orders[id]
		o.Status = StatusExpired
		o.UpdatedAt = now
		m.appendAudit(o.TenantID, o.ID, AuditOrderExpired, ActorKindSystem, 0, "", "")
	}
	return len(ids), nil
}

func (m *MemoryStore) balanceLocked(tenantID, userID int64) int64 {
	var sum int64
	for _, c := range m.credits {
		if c.TenantID == tenantID && c.UserID == userID {
			sum += c.AmountCents
		}
	}
	for _, r := range m.refunds {
		if r.TenantID == tenantID && r.UserID == userID {
			sum -= r.AmountCents
		}
	}
	return sum
}

func (m *MemoryStore) appendAudit(tenantID, orderID int64, eventType, actorKind string, actorID int64, reasonClass, requestID string) {
	m.nextAuditID++
	m.audits = append(m.audits, AuditEvent{
		ID:          m.nextAuditID,
		TenantID:    tenantID,
		OrderID:     orderID,
		EventType:   eventType,
		ActorKind:   actorKindOrDefault(actorKind),
		ActorID:     actorID,
		ReasonClass: reasonClass,
		RequestID:   requestID,
	})
}

func stringJoin(tenantID int64, s string) string {
	return strconv.FormatInt(tenantID, 10) + "|" + s
}

func auditActorKind(rec createOrderRecord) string {
	if rec.CreatedActorKind != "" {
		return rec.CreatedActorKind
	}
	if rec.CreatedByAdminID > 0 {
		return ActorKindAdmin
	}
	return ActorKindSystem
}

func auditActorID(rec createOrderRecord) int64 {
	if rec.CreatedActorID > 0 {
		return rec.CreatedActorID
	}
	if rec.CreatedByAdminID > 0 {
		return rec.CreatedByAdminID
	}
	return 0
}

// paymentOrderExpired 判定 pending 单是否已过期(用于把过期未付单排除出 pending 数/日额上限,
// 否则用户的过期废单会一直占名额、堵正常充值)。
func paymentOrderExpired(o *Order, now time.Time) bool {
	return o != nil && o.ExpiresAt != nil && !o.ExpiresAt.After(now)
}
