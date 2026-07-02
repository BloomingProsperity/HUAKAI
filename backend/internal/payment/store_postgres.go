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

	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

// completeFulfill 的 SERIALIZABLE 事务瞬时冲突最大重试次数。
const fulfillTxRetryAttempts = 5

// 订单查询列清单 (与 scanOrder 一一对应)。
const orderSelectColumns = `
	id, tenant_id, user_id, out_trade_no, amount_cents, currency_code, status, provider_kind,
	COALESCE(provider_order_ref, ''), COALESCE(request_fingerprint, ''),
	created_by_admin_id, confirmed_by_admin_id, COALESCE(confirm_reason, ''),
	COALESCE(failure_code, ''), COALESCE(failure_message, ''),
	created_at, updated_at, expires_at, paid_at, recharging_at, completed_at, failed_at,
	order_kind, subscription_plan_id,
	COALESCE(terms_version, ''), terms_accepted_at, terms_accepted_by, COALESCE(host(terms_accepted_ip), '')`

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
		ActorKind: auditActorKind(rec),
		ActorID:   auditActorID(rec),
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
		existing.RequestFingerprint != strings.TrimSpace(rec.RequestFingerprint) ||
		existing.OrderKind != orderKindOrDefault(rec.OrderKind) ||
		!sameOptionalInt64(existing.SubscriptionPlanID, rec.SubscriptionPlanID) ||
		!strings.EqualFold(existing.CurrencyCode, rec.CurrencyCode) {
		return Order{}, false, ErrIdempotencyConflict
	}
	if err := insertAuditTx(ctx, tx, auditInsert{
		TenantID:  rec.TenantID,
		OrderID:   existing.ID,
		EventType: AuditIdempotentReplay,
		ActorKind: auditActorKind(rec),
		ActorID:   auditActorID(rec),
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

func (s *PostgresStore) GetSubscriptionPlanPriceSnapshot(ctx context.Context, tenantID, planID int64) (subscriptionPlanPriceSnapshot, error) {
	if s == nil || s.pool == nil {
		return subscriptionPlanPriceSnapshot{}, ErrStoreNotConfigured
	}
	var snapshot subscriptionPlanPriceSnapshot
	if err := s.pool.QueryRow(ctx, `
	SELECT tenant_id, id, price_cents, currency_code, enabled
	FROM subscription_plans WHERE tenant_id=$1 AND id=$2`, tenantID, planID).Scan(
		&snapshot.TenantID, &snapshot.PlanID, &snapshot.AmountCents, &snapshot.CurrencyCode, &snapshot.Enabled,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return subscriptionPlanPriceSnapshot{}, subscription.ErrPlanNotFound
		}
		return subscriptionPlanPriceSnapshot{}, fmt.Errorf("payment: get subscription plan price snapshot: %w", err)
	}
	snapshot.CurrencyCode = strings.TrimSpace(snapshot.CurrencyCode)
	return snapshot, nil
}

// ConfirmPaid CAS 把 pending 推进 paid; 已 paid/recharging/completed 幂等返回; 终态拒绝。
func (s *PostgresStore) CancelOrder(ctx context.Context, rec cancelRecord) (Order, error) {
	if s == nil || s.pool == nil {
		return Order{}, ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Order{}, fmt.Errorf("payment: begin cancel: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	order, err := getOrderForUpdateTx(ctx, tx, rec.TenantID, rec.OrderID)
	if err != nil {
		return Order{}, err
	}
	// 用户自助取消: 校验订单归属(防越权取消他人订单); 不符当作不存在, 不泄露存在性。
	if rec.UserID > 0 && order.UserID != rec.UserID {
		return Order{}, ErrOrderNotFound
	}
	switch order.Status {
	case StatusPending:
		row := tx.QueryRow(ctx, `
UPDATE payment_orders
SET status='cancelled', updated_at=$3
WHERE tenant_id=$1 AND id=$2
RETURNING`+orderSelectColumns, rec.TenantID, rec.OrderID, rec.Now)
		order, err = scanOrder(row)
		if err != nil {
			return Order{}, fmt.Errorf("payment: cancel update: %w", err)
		}
		if err := insertAuditTx(ctx, tx, auditInsert{
			TenantID:    rec.TenantID,
			OrderID:     order.ID,
			EventType:   AuditOrderCancelled,
			ActorKind:   actorKindOrDefault(rec.ActorKind),
			ActorID:     rec.ActorID,
			ReasonClass: rec.Reason,
			RequestID:   rec.RequestID,
			Now:         rec.Now,
		}); err != nil {
			return Order{}, err
		}
	case StatusCancelled:
		// 幂等: 已取消, 返回当前状态, 不重复审计。
	default: // paid / recharging / completed / expired / failed
		return Order{}, ErrOrderNotCancelable
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, fmt.Errorf("payment: commit cancel: %w", err)
	}
	return order, nil
}

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
			ActorKind:   actorKindOrDefault(rec.ActorKind), // admin=手动 / system=回调; 见 confirmRecord.ActorKind
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
		// 幂等重放: 按 order_kind 回放。订阅单读效果账本(无 credit), 充值单读 credit。
		if order.OrderKind == OrderKindSubscription {
			grant, err := s.activateOrderSubscriptionTx(ctx, tx, order, rec)
			if err != nil {
				return FulfillResult{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return FulfillResult{}, fmt.Errorf("payment: commit idempotent subscription fulfill: %w", err)
			}
			return FulfillResult{Order: order, Subscription: grant, Idempotent: true}, nil
		}
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

	// 订阅单 (零余额零 billing): 同事务激活订阅 + 写效果账本 + 标完成。
	if order.OrderKind == OrderKindSubscription {
		grant, err := s.activateOrderSubscriptionTx(ctx, tx, order, rec)
		if err != nil {
			return FulfillResult{}, err
		}
		row := tx.QueryRow(ctx, `
UPDATE payment_orders SET status='completed', completed_at=$3, updated_at=$3
WHERE tenant_id=$1 AND id=$2
RETURNING`+orderSelectColumns, rec.TenantID, order.ID, rec.Now)
		completed, err := scanOrder(row)
		if err != nil {
			return FulfillResult{}, fmt.Errorf("payment: complete subscription order update: %w", err)
		}
		if err := insertAuditTx(ctx, tx, auditInsert{
			TenantID:  rec.TenantID,
			OrderID:   completed.ID,
			EventType: AuditCredited,
			ActorKind: actorKindOrDefault(rec.ActorKind),
			ActorID:   rec.ActorID,
			RequestID: rec.RequestID,
			Payload: map[string]any{
				"order_kind":           OrderKindSubscription,
				"subscription_plan_id": grant.PlanID,
				"result_kind":          grant.ResultKind,
				"user_subscription_id": grant.UserSubscriptionID,
			},
			Now: rec.Now,
		}); err != nil {
			return FulfillResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return FulfillResult{}, fmt.Errorf("payment: commit subscription fulfill phase2: %w", err)
		}
		return FulfillResult{Order: completed, Subscription: grant, Idempotent: false}, nil
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
	if err := syncLegacyUserBalanceTx(ctx, tx, rec.TenantID, order.UserID, order.AmountCents, rec.Now); err != nil {
		return FulfillResult{}, err
	}

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

// activateOrderSubscriptionTx 订阅单分支: 在订单完成事务内调订阅履约入口 (激活/续期 + 写效果账本, 幂等),
// 回传授予摘要。零 payment_credits / 零 billing_events。subscription.ErrDowngradeNotAllowed 等错误向上传播 →
// 整事务回滚 → 订单不进 completed (留 recharging 可人工/重试)。
func (s *PostgresStore) activateOrderSubscriptionTx(ctx context.Context, tx pgx.Tx, order Order, rec fulfillRecord) (*SubscriptionGrant, error) {
	if order.SubscriptionPlanID == nil {
		// DB CHECK (payment_orders_subscription_kind_check) 已保证非空; 防御性二道闸。
		return nil, ErrInvalidInput
	}
	res, err := subscription.FulfillOrderTx(ctx, tx, subscription.FulfillOrderInput{
		TenantID:       rec.TenantID,
		UserID:         order.UserID,
		PlanID:         *order.SubscriptionPlanID,
		PaymentOrderID: order.ID,
		ActorKind:      actorKindOrDefault(rec.ActorKind),
		ActorID:        rec.ActorID,
		RequestID:      rec.RequestID,
		Now:            rec.Now,
	})
	if err != nil {
		return nil, err
	}
	return &SubscriptionGrant{
		UserSubscriptionID:  res.Subscription.ID,
		PlanID:              res.PlanID,
		ResultKind:          res.ResultKind,
		NewExpiresAt:        res.NewExpiresAt,
		AppliedValidityDays: res.AppliedValidityDays,
	}, nil
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

func (s *PostgresStore) CountPendingOrders(ctx context.Context, tenantID, userID int64, now time.Time) (int, error) {
	if s == nil || s.pool == nil {
		return 0, ErrStoreNotConfigured
	}
	var count int
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*)::int
FROM payment_orders
WHERE tenant_id=$1 AND user_id=$2 AND status='pending'
  AND (expires_at IS NULL OR expires_at > $3)`,
		tenantID, userID, now).Scan(&count); err != nil {
		return 0, fmt.Errorf("payment: count pending orders: %w", err)
	}
	return count, nil
}

func (s *PostgresStore) SumRechargeAmountSince(ctx context.Context, tenantID, userID int64, since, now time.Time) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, ErrStoreNotConfigured
	}
	var sum int64
	if err := s.pool.QueryRow(ctx, `
SELECT COALESCE(SUM(amount_cents), 0)::bigint
FROM payment_orders
WHERE tenant_id=$1
  AND user_id=$2
  AND created_at >= $3
  AND (status IN ('paid', 'recharging', 'completed')
       OR (status = 'pending' AND (expires_at IS NULL OR expires_at > $4)))`,
		tenantID, userID, since, now).Scan(&sum); err != nil {
		return 0, fmt.Errorf("payment: sum recharge orders: %w", err)
	}
	return sum, nil
}

// UserBalanceCents 用户支付来源余额 = payment_credits - payment_refunds 派生净额 (tenant-scoped)。
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

func (s *PostgresStore) RecordCallbackRejection(ctx context.Context, order Order, reason, requestID string) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("payment: begin callback rejection audit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertAuditTx(ctx, tx, auditInsert{
		TenantID:    order.TenantID,
		OrderID:     order.ID,
		EventType:   AuditFulfillmentFailed,
		ActorKind:   ActorKindSystem,
		ReasonClass: reason,
		RequestID:   requestID,
		Payload:     map[string]any{"reason": reason, "provider": orderProviderName(order)},
		Now:         time.Now().UTC(),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("payment: commit callback rejection audit: %w", err)
	}
	return nil
}

// ---- 内部事务级辅助 ----

type auditInsert struct {
	TenantID    int64
	OrderID     int64
	EventType   string
	ActorKind   string
	ActorID     int64
	ActorRef    string // 双身份归属串(AuditActor() 形态),空则列落 NULL
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
INSERT INTO payment_audit_events (tenant_id, payment_order_id, event_type, actor_kind, actor_id, actor_ref, reason_class, request_id, redacted_payload, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		ev.TenantID, ev.OrderID, ev.EventType, actorKindOrDefault(ev.ActorKind),
		nullableInt64(ev.ActorID), nullableText(ev.ActorRef), nullableText(ev.ReasonClass), nullableText(ev.RequestID),
		nullableJSON(raw), ev.Now); err != nil {
		return fmt.Errorf("payment: insert audit event: %w", err)
	}
	return nil
}

func insertOrderTx(ctx context.Context, tx pgx.Tx, rec createOrderRecord) (Order, error) {
	row := tx.QueryRow(ctx, `
INSERT INTO payment_orders (
	tenant_id, user_id, out_trade_no, amount_cents, currency_code, status,
	provider_kind, provider_order_ref, request_fingerprint, created_by_admin_id, created_by_actor,
	created_at, updated_at, expires_at, order_kind, subscription_plan_id,
	terms_version, terms_accepted_at, terms_accepted_by, terms_accepted_ip
) VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7, $8, $9, $10, $11, $11, $12, $13, $14, $15, $16, $17, $18::inet)
RETURNING`+orderSelectColumns,
		rec.TenantID, rec.UserID, rec.OutTradeNo, rec.AmountCents, rec.CurrencyCode,
		string(providerKindOrDefault(rec.ProviderKind)), nullableText(rec.ProviderOrderRef),
		nullableText(rec.RequestFingerprint), nullableInt64(rec.CreatedByAdminID), nullableText(rec.CreatedByActor),
		rec.Now, rec.ExpiresAt,
		orderKindOrDefault(rec.OrderKind), rec.SubscriptionPlanID,
		nullableText(rec.ComplianceTermsVersion), rec.ComplianceAcceptedAt, nullableInt64(rec.ComplianceAcceptedBy),
		nullableText(rec.ComplianceAcceptedIP))
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

func insertTopupCreditTx(ctx context.Context, tx pgx.Tx, order Order, actorKind string, actorID int64, requestID string, now time.Time) (CreditRecord, int64, error) {
	var credit CreditRecord
	credit.TenantID = order.TenantID
	credit.OrderID = order.ID
	credit.UserID = order.UserID
	credit.AmountCents = order.AmountCents
	credit.CurrencyCode = order.CurrencyCode
	credit.ReasonClass = reasonClassForProvider(order.ProviderKind)
	if err := tx.QueryRow(ctx, `
INSERT INTO payment_credits (tenant_id, payment_order_id, user_id, amount_cents, currency_code, reason_class, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, created_at`,
		credit.TenantID, credit.OrderID, credit.UserID, credit.AmountCents, credit.CurrencyCode, credit.ReasonClass, now,
	).Scan(&credit.ID, &credit.CreatedAt); err != nil {
		return CreditRecord{}, 0, err
	}
	amount := decimalFromCents(order.AmountCents)
	fingerprint := fmt.Sprintf("payment:t%d:o%d:c%d", order.TenantID, order.ID, credit.ID)
	var billingID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO billing_events (tenant_id, event_type, actual_cost, actual_cost_signed,
	stream_state, delivered_token_count, fingerprint, payment_credit_id)
VALUES ($1, 'payment_credited', $2, $2, 2, 0, $3, $4)
RETURNING id`, order.TenantID, amount, fingerprint, credit.ID).Scan(&billingID); err != nil {
		return CreditRecord{}, 0, fmt.Errorf("payment: insert billing event: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_credits SET billing_event_id=$3 WHERE tenant_id=$1 AND id=$2`,
		order.TenantID, credit.ID, billingID); err != nil {
		return CreditRecord{}, 0, fmt.Errorf("payment: link billing event: %w", err)
	}
	credit.BillingEventID = billingID
	if err := syncLegacyUserBalanceTx(ctx, tx, order.TenantID, order.UserID, order.AmountCents, now); err != nil {
		return CreditRecord{}, 0, err
	}
	_ = actorKind
	_ = actorID
	_ = requestID
	return credit, billingID, nil
}

func userBalanceTx(ctx context.Context, tx pgx.Tx, tenantID, userID int64) (int64, error) {
	var balance int64
	if err := tx.QueryRow(ctx, `
WITH credits AS (
	SELECT COALESCE(SUM(amount_cents), 0)::bigint AS total
	FROM payment_credits
	WHERE tenant_id=$1 AND user_id=$2
),
refunds AS (
	SELECT COALESCE(SUM(amount_cents), 0)::bigint AS total
	FROM payment_refunds
	WHERE tenant_id=$1 AND user_id=$2
)
SELECT credits.total - refunds.total
FROM credits, refunds`, tenantID, userID).Scan(&balance); err != nil {
		return 0, fmt.Errorf("payment: read balance: %w", err)
	}
	return balance, nil
}

func syncLegacyUserBalanceTx(ctx context.Context, tx pgx.Tx, tenantID, userID, amountCents int64, now time.Time) error {
	if amountCents <= 0 {
		return ErrInvalidAmount
	}
	return syncLegacyUserBalanceDeltaTx(ctx, tx, tenantID, userID, amountCents, now)
}

func syncLegacyUserBalanceDeltaTx(ctx context.Context, tx pgx.Tx, tenantID, userID, amountCents int64, now time.Time) error {
	if amountCents == 0 {
		return ErrInvalidAmount
	}
	amount := decimalFromCents(amountCents)
	if _, err := tx.Exec(ctx, `
INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
VALUES ($1, $2, $3, 0, 1, $4)
ON CONFLICT (tenant_id, user_id) DO UPDATE
SET balance = user_balances.balance + EXCLUDED.balance,
    version = user_balances.version + 1,
    updated_at = EXCLUDED.updated_at`,
		tenantID, userID, amount, now); err != nil {
		return fmt.Errorf("payment: sync legacy user balance: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOrder(row rowScanner) (Order, error) {
	var o Order
	var status, providerKind string
	var createdBy, confirmedBy, subPlanID, complianceAcceptedBy sql.NullInt64
	var expiresAt, paidAt, rechargingAt, completedAt, failedAt, complianceAcceptedAt sql.NullTime
	if err := row.Scan(
		&o.ID, &o.TenantID, &o.UserID, &o.OutTradeNo, &o.AmountCents, &o.CurrencyCode, &status, &providerKind,
		&o.ProviderOrderRef, &o.RequestFingerprint,
		&createdBy, &confirmedBy, &o.ConfirmReason,
		&o.FailureCode, &o.FailureMessage,
		&o.CreatedAt, &o.UpdatedAt, &expiresAt, &paidAt, &rechargingAt, &completedAt, &failedAt,
		&o.OrderKind, &subPlanID,
		&o.ComplianceTermsVersion, &complianceAcceptedAt, &complianceAcceptedBy, &o.ComplianceAcceptedIP,
	); err != nil {
		return Order{}, err
	}
	if subPlanID.Valid {
		o.SubscriptionPlanID = &subPlanID.Int64
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
	if complianceAcceptedBy.Valid {
		o.ComplianceAcceptedBy = complianceAcceptedBy.Int64
	}
	o.CreatedAt = o.CreatedAt.UTC()
	o.UpdatedAt = o.UpdatedAt.UTC()
	o.ExpiresAt = nullTimeToPtr(expiresAt)
	o.PaidAt = nullTimeToPtr(paidAt)
	o.RechargingAt = nullTimeToPtr(rechargingAt)
	o.CompletedAt = nullTimeToPtr(completedAt)
	o.FailedAt = nullTimeToPtr(failedAt)
	o.ComplianceAcceptedAt = nullTimeToPtr(complianceAcceptedAt)
	return o, nil
}

func reasonClassForProvider(kind ProviderKind) string {
	if kind == ProviderTest {
		return "test_provider_paid"
	}
	return "manual_confirmed"
}

// orderKindOrDefault: 空缺省充值 (向后兼容现存单)。
func orderKindOrDefault(kind string) string {
	if kind == "" {
		return OrderKindTopup
	}
	return kind
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
