// HUAKAI · iKun

package payment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// completeFulfill 的 SERIALIZABLE 事务瞬时冲突最大重试次数。
const fulfillTxRetryAttempts = 5

// 订单查询列清单 (与 scanOrder 一一对应)。
const orderSelectColumns = `
	id, tenant_id, user_id, out_trade_no, amount_cents, currency_code, status, provider_kind,
	COALESCE(provider_order_ref, ''), COALESCE(request_fingerprint, ''),
	created_by_admin_id, confirmed_by_admin_id, COALESCE(confirm_reason, ''),
	COALESCE(failure_code, ''), COALESCE(failure_message, ''),
	created_at, updated_at, expires_at, paid_at, recharging_at, completed_at, failed_at`

// PostgresStore 支付权威存储。
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore 构造 PG 支付存储。
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

var _ Store = (*PostgresStore)(nil)

// CreateOrder 建一张 pending 订单; 同 (tenant, out_trade_no) 重复则按重放/冲突分类。
func (s *PostgresStore) CreateOrder(ctx context.Context, rec createOrderRecord) (Order, bool, error) {
	if s == nil || s.pool == nil {
		return Order{}, false, ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Order{}, false, fmt.Errorf("payment: begin create order: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	order, err := insertOrderTx(ctx, tx, rec)
	if isUniqueViolation(err) {
		_ = tx.Rollback(ctx)
		return s.handleDuplicateOrder(ctx, rec)
	}
	if err != nil {
		return Order{}, false, fmt.Errorf("payment: insert order: %w", err)
	}
	if err := insertAuditTx(ctx, tx, auditInsert{
		TenantID:  rec.TenantID,
		OrderID:   order.ID,
		EventType: AuditOrderCreated,
		ActorKind: ActorKindAdmin,
		ActorID:   rec.CreatedByAdminID,
		RequestID: rec.RequestID,
		Payload:   map[string]any{"amount_cents": rec.AmountCents, "provider_kind": string(rec.ProviderKind)},
		Now:       rec.Now,
	}); err != nil {
		return Order{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, false, fmt.Errorf("payment: commit create order: %w", err)
	}
	return order, false, nil
}

func (s *PostgresStore) handleDuplicateOrder(ctx context.Context, rec createOrderRecord) (Order, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Order{}, false, fmt.Errorf("payment: begin duplicate order replay: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	existing, err := getOrderByOutTradeNoTx(ctx, tx, rec.TenantID, rec.OutTradeNo)
	if err != nil {
		return Order{}, false, err
	}
	// 关键业务字段不一致 = 复用同号但语义不同 → 冲突, 不改旧单。
	if existing.AmountCents != rec.AmountCents ||
		existing.UserID != rec.UserID ||
		existing.ProviderKind != rec.ProviderKind ||
		!strings.EqualFold(existing.CurrencyCode, rec.CurrencyCode) {
		return Order{}, false, ErrIdempotencyConflict
	}
	if err := insertAuditTx(ctx, tx, auditInsert{
		TenantID:  rec.TenantID,
		OrderID:   existing.ID,
		EventType: AuditIdempotentReplay,
		ActorKind: ActorKindAdmin,
		ActorID:   rec.CreatedByAdminID,
		RequestID: rec.RequestID,
		Now:       rec.Now,
	}); err != nil {
		return Order{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, false, fmt.Errorf("payment: commit duplicate order replay: %w", err)
	}
	return existing, true, nil
}

// GetOrder 按内部 id 读订单 (tenant-scoped)。
func (s *PostgresStore) GetOrder(ctx context.Context, tenantID, orderID int64) (Order, error) {
	if s == nil || s.pool == nil {
		return Order{}, ErrStoreNotConfigured
	}
	row := s.pool.QueryRow(ctx, `SELECT`+orderSelectColumns+`
FROM payment_orders WHERE tenant_id=$1 AND id=$2`, tenantID, orderID)
	order, err := scanOrder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrOrderNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("payment: get order: %w", err)
	}
	return order, nil
}

// GetOrderByOutTradeNo 按外部订单号读订单 (tenant-scoped)。
func (s *PostgresStore) GetOrderByOutTradeNo(ctx context.Context, tenantID int64, outTradeNo string) (Order, error) {
	if s == nil || s.pool == nil {
		return Order{}, ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Order{}, fmt.Errorf("payment: begin get by out_trade_no: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return getOrderByOutTradeNoTx(ctx, tx, tenantID, outTradeNo)
}

// ConfirmPaid CAS 把 pending 推进 paid; 已 paid/recharging/completed 幂等返回; 终态拒绝。
func (s *PostgresStore) ConfirmPaid(ctx context.Context, rec confirmRecord) (Order, error) {
	if s == nil || s.pool == nil {
		return Order{}, ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Order{}, fmt.Errorf("payment: begin confirm: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	order, err := getOrderForUpdateTx(ctx, tx, rec.TenantID, rec.OrderID)
	if err != nil {
		return Order{}, err
	}
	switch order.Status {
	case StatusPending:
		// 过期 pending 订单不得入账: 标记 expired 并拒绝 (防 stale 单被无限期履约)。
		if order.ExpiresAt != nil && order.ExpiresAt.Before(rec.Now) {
			if _, err := tx.Exec(ctx, `UPDATE payment_orders SET status='expired', updated_at=$3 WHERE tenant_id=$1 AND id=$2`,
				rec.TenantID, rec.OrderID, rec.Now); err != nil {
				return Order{}, fmt.Errorf("payment: expire stale order: %w", err)
			}
			if err := insertAuditTx(ctx, tx, auditInsert{
				TenantID:  rec.TenantID,
				OrderID:   order.ID,
				EventType: AuditOrderExpired,
				ActorKind: ActorKindSystem,
				RequestID: rec.RequestID,
				Now:       rec.Now,
			}); err != nil {
				return Order{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return Order{}, fmt.Errorf("payment: commit expire stale order: %w", err)
			}
			return Order{}, ErrOrderNotConfirmable
		}
		row := tx.QueryRow(ctx, `
UPDATE payment_orders
SET status='paid', paid_at=$3, confirmed_by_admin_id=$4, confirm_reason=$5, updated_at=$3
WHERE tenant_id=$1 AND id=$2
RETURNING`+orderSelectColumns, rec.TenantID, rec.OrderID, rec.Now, nullableInt64(rec.AdminID), nullableText(rec.ConfirmReason))
		order, err = scanOrder(row)
		if err != nil {
			return Order{}, fmt.Errorf("payment: confirm update: %w", err)
		}
		if err := insertAuditTx(ctx, tx, auditInsert{
			TenantID:    rec.TenantID,
			OrderID:     order.ID,
			EventType:   AuditPaidConfirmed,
			ActorKind:   ActorKindAdmin,
			ActorID:     rec.AdminID,
			ReasonClass: rec.ConfirmReason,
			RequestID:   rec.RequestID,
			Now:         rec.Now,
		}); err != nil {
			return Order{}, err
		}
	case StatusPaid, StatusRecharging, StatusCompleted:
		// 已确认或更靠后, 幂等返回当前状态, 不重复审计。
	default: // expired / cancelled / failed
		return Order{}, ErrOrderNotConfirmable
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, fmt.Errorf("payment: commit confirm: %w", err)
	}
	return order, nil
}

// BeginFulfill phase1 短事务: CAS paid->recharging 并持久提交 (recharging 是可崩溃恢复断点)。
func (s *PostgresStore) BeginFulfill(ctx context.Context, rec fulfillRecord) (Order, beginFulfillOutcome, error) {
	if s == nil || s.pool == nil {
		return Order{}, beginFulfillTransitioned, ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Order{}, beginFulfillTransitioned, fmt.Errorf("payment: begin fulfill phase1: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	order, err := getOrderForUpdateTx(ctx, tx, rec.TenantID, rec.OrderID)
	if err != nil {
		return Order{}, beginFulfillTransitioned, err
	}
	outcome := beginFulfillTransitioned
	switch order.Status {
	case StatusPaid:
		row := tx.QueryRow(ctx, `
UPDATE payment_orders
SET status='recharging', recharging_at=COALESCE(recharging_at, $3), updated_at=$3
WHERE tenant_id=$1 AND id=$2
RETURNING`+orderSelectColumns, rec.TenantID, rec.OrderID, rec.Now)
		order, err = scanOrder(row)
		if err != nil {
			return Order{}, beginFulfillTransitioned, fmt.Errorf("payment: begin fulfill update: %w", err)
		}
		if err := insertAuditTx(ctx, tx, auditInsert{
			TenantID:  rec.TenantID,
			OrderID:   order.ID,
			EventType: AuditFulfillmentStarted,
			ActorKind: actorKindOrDefault(rec.ActorKind),
			ActorID:   rec.ActorID,
			RequestID: rec.RequestID,
			Now:       rec.Now,
		}); err != nil {
			return Order{}, beginFulfillTransitioned, err
		}
	case StatusRecharging:
		// 断点续跑: 已在 recharging, 不重复审计, 直接进 phase2。
	case StatusCompleted:
		outcome = beginFulfillAlreadyDone
	default: // pending / expired / cancelled / failed
		return Order{}, beginFulfillTransitioned, ErrOrderNotFulfillable
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, beginFulfillTransitioned, fmt.Errorf("payment: commit fulfill phase1: %w", err)
	}
	return order, outcome, nil
}

// CompleteFulfill phase2: SERIALIZABLE 事务写 credit + billing_event(payment_credited) + completed。
// 重试瞬时序列化冲突; 并发胜者已入账时本次幂等返回 (靠 FOR UPDATE + credit unique(order))。
func (s *PostgresStore) CompleteFulfill(ctx context.Context, rec fulfillRecord) (FulfillResult, error) {
	if s == nil || s.pool == nil {
		return FulfillResult{}, ErrStoreNotConfigured
	}
	var lastErr error
	for attempt := 0; attempt < fulfillTxRetryAttempts; attempt++ {
		result, err := s.completeFulfillOnce(ctx, rec)
		if err == nil {
			return result, nil
		}
		// 序列化冲突或 credit 唯一冲突 = 并发竞争; 重试后将看到 completed → 幂等。
		if isPgRetryableTxConflict(err) || isUniqueViolation(err) {
			lastErr = err
			continue
		}
		return FulfillResult{}, err
	}
	return FulfillResult{}, fmt.Errorf("payment: complete fulfill exhausted retries: %w", lastErr)
}

func (s *PostgresStore) completeFulfillOnce(ctx context.Context, rec fulfillRecord) (FulfillResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return FulfillResult{}, fmt.Errorf("payment: begin fulfill phase2: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	order, err := getOrderForUpdateTx(ctx, tx, rec.TenantID, rec.OrderID)
	if err != nil {
		return FulfillResult{}, err
	}
	if order.Status == StatusCompleted {
		credit, err := getCreditByOrderTx(ctx, tx, rec.TenantID, order.ID)
		if err != nil {
			return FulfillResult{}, err
		}
		balance, err := userBalanceTx(ctx, tx, rec.TenantID, order.UserID)
		if err != nil {
			return FulfillResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return FulfillResult{}, fmt.Errorf("payment: commit idempotent fulfill: %w", err)
		}
		return FulfillResult{Order: order, Credit: credit, BalanceCents: balance, Idempotent: true}, nil
	}
	if order.Status != StatusRecharging {
		return FulfillResult{}, ErrOrderNotFulfillable
	}

	var credit CreditRecord
	credit.TenantID = rec.TenantID
	credit.OrderID = order.ID
	credit.UserID = order.UserID
	credit.AmountCents = order.AmountCents
	credit.CurrencyCode = order.CurrencyCode
	credit.ReasonClass = reasonClassForProvider(order.ProviderKind)
	if err := tx.QueryRow(ctx, `
INSERT INTO payment_credits (tenant_id, payment_order_id, user_id, amount_cents, currency_code, reason_class, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, created_at`,
		credit.TenantID, credit.OrderID, credit.UserID, credit.AmountCents, credit.CurrencyCode, credit.ReasonClass, rec.Now,
	).Scan(&credit.ID, &credit.CreatedAt); err != nil {
		return FulfillResult{}, err // 23505 由外层重试当幂等处理
	}

	amount := decimalFromCents(order.AmountCents)
	fingerprint := fmt.Sprintf("payment:t%d:o%d:c%d", rec.TenantID, order.ID, credit.ID)
	var billingID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO billing_events (tenant_id, event_type, actual_cost, actual_cost_signed,
	stream_state, delivered_token_count, fingerprint, payment_credit_id)
VALUES ($1, 'payment_credited', $2, $2, 2, 0, $3, $4)
RETURNING id`, rec.TenantID, amount, fingerprint, credit.ID).Scan(&billingID); err != nil {
		return FulfillResult{}, fmt.Errorf("payment: insert billing event: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_credits SET billing_event_id=$3 WHERE tenant_id=$1 AND id=$2`,
		rec.TenantID, credit.ID, billingID); err != nil {
		return FulfillResult{}, fmt.Errorf("payment: link billing event: %w", err)
	}
	credit.BillingEventID = billingID

	row := tx.QueryRow(ctx, `
UPDATE payment_orders SET status='completed', completed_at=$3, updated_at=$3
WHERE tenant_id=$1 AND id=$2
RETURNING`+orderSelectColumns, rec.TenantID, order.ID, rec.Now)
	order, err = scanOrder(row)
	if err != nil {
		return FulfillResult{}, fmt.Errorf("payment: complete order update: %w", err)
	}
	if err := insertAuditTx(ctx, tx, auditInsert{
		TenantID:  rec.TenantID,
		OrderID:   order.ID,
		EventType: AuditCredited,
		ActorKind: actorKindOrDefault(rec.ActorKind),
		ActorID:   rec.ActorID,
		RequestID: rec.RequestID,
		Payload:   map[string]any{"amount_cents": order.AmountCents, "credit_id": credit.ID, "billing_event_id": billingID},
		Now:       rec.Now,
	}); err != nil {
		return FulfillResult{}, err
	}
	balance, err := userBalanceTx(ctx, tx, rec.TenantID, order.UserID)
	if err != nil {
		return FulfillResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FulfillResult{}, fmt.Errorf("payment: commit fulfill phase2: %w", err)
	}
	return FulfillResult{Order: order, Credit: credit, BalanceCents: balance, Idempotent: false}, nil
}

// ListOrdersByUser 列某用户订单 (按创建时间倒序)。
func (s *PostgresStore) ListOrdersByUser(ctx context.Context, tenantID, userID int64, limit int) ([]Order, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT`+orderSelectColumns+`
FROM payment_orders WHERE tenant_id=$1 AND user_id=$2
ORDER BY created_at DESC, id DESC LIMIT $3`, tenantID, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("payment: list orders: %w", err)
	}
	defer rows.Close()
	var out []Order
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, order)
	}
	return out, rows.Err()
}

// UserBalanceCents 用户支付来源余额 = payment_credits 派生 SUM (tenant-scoped)。
func (s *PostgresStore) UserBalanceCents(ctx context.Context, tenantID, userID int64) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return 0, fmt.Errorf("payment: begin balance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return userBalanceTx(ctx, tx, tenantID, userID)
}

// ListAuditEvents 列某订单的操作审计轨迹 (tenant-scoped, 时间正序)。
func (s *PostgresStore) ListAuditEvents(ctx context.Context, tenantID, orderID int64) ([]AuditEvent, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, payment_order_id, event_type, actor_kind, actor_id,
	COALESCE(reason_class, ''), COALESCE(request_id, ''), redacted_payload, occurred_at
FROM payment_audit_events
WHERE tenant_id=$1 AND payment_order_id=$2
ORDER BY occurred_at, id`, tenantID, orderID)
	if err != nil {
		return nil, fmt.Errorf("payment: list audit events: %w", err)
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var ev AuditEvent
		var actorID sql.NullInt64
		var payload []byte
		if err := rows.Scan(&ev.ID, &ev.TenantID, &ev.OrderID, &ev.EventType, &ev.ActorKind,
			&actorID, &ev.ReasonClass, &ev.RequestID, &payload, &ev.OccurredAt); err != nil {
			return nil, err
		}
		if actorID.Valid {
			ev.ActorID = actorID.Int64
		}
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &ev.Payload)
		}
		ev.OccurredAt = ev.OccurredAt.UTC()
		out = append(out, ev)
	}
	return out, rows.Err()
}

// ---- 内部事务级辅助 ----

type auditInsert struct {
	TenantID    int64
	OrderID     int64
	EventType   string
	ActorKind   string
	ActorID     int64
	ReasonClass string
	RequestID   string
	Payload     map[string]any
	Now         time.Time
}

func insertAuditTx(ctx context.Context, tx pgx.Tx, ev auditInsert) error {
	var raw []byte
	if payload := sanitizeAuditPayload(ctx, ev.Payload); payload != nil {
		raw, _ = json.Marshal(payload)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO payment_audit_events (tenant_id, payment_order_id, event_type, actor_kind, actor_id, reason_class, request_id, redacted_payload, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		ev.TenantID, ev.OrderID, ev.EventType, actorKindOrDefault(ev.ActorKind),
		nullableInt64(ev.ActorID), nullableText(ev.ReasonClass), nullableText(ev.RequestID),
		nullableJSON(raw), ev.Now); err != nil {
		return fmt.Errorf("payment: insert audit event: %w", err)
	}
	return nil
}

func insertOrderTx(ctx context.Context, tx pgx.Tx, rec createOrderRecord) (Order, error) {
	row := tx.QueryRow(ctx, `
INSERT INTO payment_orders (
	tenant_id, user_id, out_trade_no, amount_cents, currency_code, status,
	provider_kind, provider_order_ref, request_fingerprint, created_by_admin_id,
	created_at, updated_at, expires_at
) VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7, $8, $9, $10, $10, $11)
RETURNING`+orderSelectColumns,
		rec.TenantID, rec.UserID, rec.OutTradeNo, rec.AmountCents, rec.CurrencyCode,
		string(providerKindOrDefault(rec.ProviderKind)), nullableText(rec.ProviderOrderRef),
		nullableText(rec.RequestFingerprint), nullableInt64(rec.CreatedByAdminID), rec.Now, rec.ExpiresAt)
	return scanOrder(row)
}

func getOrderByOutTradeNoTx(ctx context.Context, tx pgx.Tx, tenantID int64, outTradeNo string) (Order, error) {
	row := tx.QueryRow(ctx, `SELECT`+orderSelectColumns+`
FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2`, tenantID, outTradeNo)
	order, err := scanOrder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrOrderNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("payment: get order by out_trade_no: %w", err)
	}
	return order, nil
}

func getOrderForUpdateTx(ctx context.Context, tx pgx.Tx, tenantID, orderID int64) (Order, error) {
	row := tx.QueryRow(ctx, `SELECT`+orderSelectColumns+`
FROM payment_orders WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, orderID)
	order, err := scanOrder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrOrderNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("payment: lock order: %w", err)
	}
	return order, nil
}

func getCreditByOrderTx(ctx context.Context, tx pgx.Tx, tenantID, orderID int64) (CreditRecord, error) {
	var c CreditRecord
	var billingID sql.NullInt64
	err := tx.QueryRow(ctx, `
SELECT id, tenant_id, payment_order_id, user_id, amount_cents, currency_code, reason_class, billing_event_id, created_at
FROM payment_credits WHERE tenant_id=$1 AND payment_order_id=$2`, tenantID, orderID).Scan(
		&c.ID, &c.TenantID, &c.OrderID, &c.UserID, &c.AmountCents, &c.CurrencyCode, &c.ReasonClass, &billingID, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CreditRecord{}, ErrOrderNotFound
	}
	if err != nil {
		return CreditRecord{}, fmt.Errorf("payment: get credit: %w", err)
	}
	if billingID.Valid {
		c.BillingEventID = billingID.Int64
	}
	c.CurrencyCode = strings.TrimSpace(c.CurrencyCode)
	c.CreatedAt = c.CreatedAt.UTC()
	return c, nil
}

func userBalanceTx(ctx context.Context, tx pgx.Tx, tenantID, userID int64) (int64, error) {
	var balance int64
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(SUM(amount_cents), 0)::bigint
FROM payment_credits WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID).Scan(&balance); err != nil {
		return 0, fmt.Errorf("payment: read balance: %w", err)
	}
	return balance, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOrder(row rowScanner) (Order, error) {
	var o Order
	var status, providerKind string
	var createdBy, confirmedBy sql.NullInt64
	var expiresAt, paidAt, rechargingAt, completedAt, failedAt sql.NullTime
	if err := row.Scan(
		&o.ID, &o.TenantID, &o.UserID, &o.OutTradeNo, &o.AmountCents, &o.CurrencyCode, &status, &providerKind,
		&o.ProviderOrderRef, &o.RequestFingerprint,
		&createdBy, &confirmedBy, &o.ConfirmReason,
		&o.FailureCode, &o.FailureMessage,
		&o.CreatedAt, &o.UpdatedAt, &expiresAt, &paidAt, &rechargingAt, &completedAt, &failedAt,
	); err != nil {
		return Order{}, err
	}
	o.Status = OrderStatus(status)
	o.ProviderKind = ProviderKind(providerKind)
	o.CurrencyCode = strings.TrimSpace(o.CurrencyCode)
	if createdBy.Valid {
		o.CreatedByAdminID = createdBy.Int64
	}
	if confirmedBy.Valid {
		o.ConfirmedByAdminID = confirmedBy.Int64
	}
	o.CreatedAt = o.CreatedAt.UTC()
	o.UpdatedAt = o.UpdatedAt.UTC()
	o.ExpiresAt = nullTimeToPtr(expiresAt)
	o.PaidAt = nullTimeToPtr(paidAt)
	o.RechargingAt = nullTimeToPtr(rechargingAt)
	o.CompletedAt = nullTimeToPtr(completedAt)
	o.FailedAt = nullTimeToPtr(failedAt)
	return o, nil
}

func reasonClassForProvider(kind ProviderKind) string {
	if kind == ProviderTest {
		return "test_provider_paid"
	}
	return "manual_confirmed"
}

func providerKindOrDefault(kind ProviderKind) ProviderKind {
	if kind == "" {
		return ProviderManual
	}
	return kind
}

func actorKindOrDefault(kind string) string {
	switch kind {
	case ActorKindAdmin, ActorKindUser, ActorKindSystem:
		return kind
	default:
		return ActorKindSystem
	}
}

func decimalFromCents(cents int64) decimal.Decimal {
	return decimal.NewFromInt(cents).Div(decimal.NewFromInt(100))
}

func nullTimeToPtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

func nullableInt64(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

func nullableText(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullableJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isPgRetryableTxConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}
