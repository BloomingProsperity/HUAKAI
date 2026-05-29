// HUAKAI · iKun

package payment

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// MemoryStore 内存支付存储, 仅用于 service 单测与本地开发 — 不得作为钱路径验收依据
// (钱路径正确性靠真 PG 约束 + SERIALIZABLE, 见 store_postgres_integration_test.go)。
type MemoryStore struct {
	mu            sync.Mutex
	orders        map[int64]*Order
	byTrade       map[string]int64
	credits       map[int64]*CreditRecord // key = order id
	audits        []AuditEvent
	nextOrderID   int64
	nextCreditID  int64
	nextBillingID int64
	nextAuditID   int64
}

// NewMemoryStore 构造内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		orders:  map[int64]*Order{},
		byTrade: map[string]int64{},
		credits: map[int64]*CreditRecord{},
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
			!strings.EqualFold(existing.CurrencyCode, rec.CurrencyCode) {
			return Order{}, false, ErrIdempotencyConflict
		}
		m.appendAudit(rec.TenantID, existing.ID, AuditIdempotentReplay, ActorKindAdmin, rec.CreatedByAdminID, "", rec.RequestID)
		return *existing, true, nil
	}
	m.nextOrderID++
	o := &Order{
		ID:                 m.nextOrderID,
		TenantID:           rec.TenantID,
		UserID:             rec.UserID,
		OutTradeNo:         rec.OutTradeNo,
		AmountCents:        rec.AmountCents,
		CurrencyCode:       rec.CurrencyCode,
		Status:             StatusPending,
		ProviderKind:       providerKindOrDefault(rec.ProviderKind),
		ProviderOrderRef:   rec.ProviderOrderRef,
		RequestFingerprint: rec.RequestFingerprint,
		CreatedByAdminID:   rec.CreatedByAdminID,
		CreatedAt:          rec.Now,
		UpdatedAt:          rec.Now,
		ExpiresAt:          rec.ExpiresAt,
	}
	m.orders[o.ID] = o
	m.byTrade[key] = o.ID
	m.appendAudit(rec.TenantID, o.ID, AuditOrderCreated, ActorKindAdmin, rec.CreatedByAdminID, "", rec.RequestID)
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
		m.appendAudit(rec.TenantID, o.ID, AuditPaidConfirmed, ActorKindAdmin, rec.AdminID, rec.ConfirmReason, rec.RequestID)
	case StatusPaid, StatusRecharging, StatusCompleted:
		// 幂等
	default:
		return Order{}, ErrOrderNotConfirmable
	}
	return *o, nil
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

func (m *MemoryStore) balanceLocked(tenantID, userID int64) int64 {
	var sum int64
	for _, c := range m.credits {
		if c.TenantID == tenantID && c.UserID == userID {
			sum += c.AmountCents
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
