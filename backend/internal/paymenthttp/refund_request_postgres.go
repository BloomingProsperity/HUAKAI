// HUAKAI · iKun

package paymenthttp

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

// PostgresRefundRequestRecorder 持久化用户退款申请与 admin 决策。
type PostgresRefundRequestRecorder struct {
	pool   *pgxpool.Pool
	refund refundRequestMoneyService
	now    func() time.Time
}

// NewPostgresRefundRequestRecorder 构造生产用的退款申请记录器。
func NewPostgresRefundRequestRecorder(pool *pgxpool.Pool, refund refundRequestMoneyService) *PostgresRefundRequestRecorder {
	return &PostgresRefundRequestRecorder{
		pool:   pool,
		refund: refund,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

var _ RefundRequestRecorder = (*PostgresRefundRequestRecorder)(nil)

func (s *PostgresRefundRequestRecorder) CreateRefundRequest(ctx context.Context, in RefundRequestInput) (RefundRequest, error) {
	if s == nil || s.pool == nil {
		return RefundRequest{}, ErrRefundRequestUnavailable
	}
	if in.TenantID <= 0 || in.UserID <= 0 || in.OrderID <= 0 {
		return RefundRequest{}, ErrRefundRequestInvalidInput
	}
	now := in.Now
	if now.IsZero() {
		now = s.now()
	}
	row := s.pool.QueryRow(ctx, `
INSERT INTO payment_refund_requests (tenant_id, order_id, user_id, reason, status, created_at)
VALUES ($1, $2, $3, $4, 'pending', $5)
ON CONFLICT (tenant_id, order_id) DO UPDATE
SET order_id = payment_refund_requests.order_id
RETURNING id, tenant_id, order_id, user_id, COALESCE(reason, ''), status, created_at, decided_at, decided_by`,
		in.TenantID, in.OrderID, in.UserID, nullableRefundRequestText(in.Reason), now.UTC())
	req, err := scanRefundRequest(row)
	if err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: create refund request: %w", err)
	}
	return req, nil
}

func (s *PostgresRefundRequestRecorder) ListPendingRefundRequests(ctx context.Context, tenantID int64) ([]RefundRequest, error) {
	if s == nil || s.pool == nil {
		return nil, ErrRefundRequestUnavailable
	}
	if tenantID <= 0 {
		return nil, ErrRefundRequestInvalidInput
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, order_id, user_id, COALESCE(reason, ''), status, created_at, decided_at, decided_by
FROM payment_refund_requests
WHERE tenant_id=$1 AND status='pending'
ORDER BY created_at ASC, id ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("paymenthttp: list refund requests: %w", err)
	}
	defer rows.Close()
	var out []RefundRequest
	for rows.Next() {
		req, err := scanRefundRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("paymenthttp: scan refund request: %w", err)
		}
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("paymenthttp: iterate refund requests: %w", err)
	}
	return out, nil
}

func (s *PostgresRefundRequestRecorder) ApproveRefundRequest(ctx context.Context, tenantID, requestID, adminActorID int64, actorRef string) (RefundRequest, error) {
	if s == nil || s.pool == nil || s.refund == nil {
		return RefundRequest{}, ErrRefundRequestUnavailable
	}
	refundInTx, ok := s.refund.(refundRequestMoneyServiceInTx)
	if !ok {
		return RefundRequest{}, ErrRefundRequestUnavailable
	}
	actorRef = strings.TrimSpace(actorRef)
	// session-admin 的 TokenID=0:有 actorRef 即有已认证 admin 身份,不再用 int>0 硬拒(role 制单登录)。
	if tenantID <= 0 || requestID <= 0 || (adminActorID <= 0 && actorRef == "") {
		return RefundRequest{}, ErrRefundRequestInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: begin approve refund request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	req, err := getRefundRequestForUpdateTx(ctx, tx, tenantID, requestID)
	if err != nil {
		return RefundRequest{}, err
	}
	if req.Status != RefundRequestPending {
		if err := tx.Commit(ctx); err != nil {
			return RefundRequest{}, fmt.Errorf("paymenthttp: commit approved replay: %w", err)
		}
		return req, nil
	}
	amountCents, err := refundRequestOrderAmountTx(ctx, tx, req)
	if err != nil {
		return RefundRequest{}, err
	}
	requestedAmountCents := amountCents
	requireExact := true
	moneyActorKind := payment.ActorKindAdmin
	moneyActorID := adminActorID
	moneyActorRef := actorRef
	if replay, found, err := refundRequestReplayTx(ctx, tx, req); err != nil {
		return RefundRequest{}, err
	} else if found {
		requestedAmountCents = replay.RequestedAmountCents
		requireExact = replay.RequireExact
		moneyActorKind = replay.Actor.Kind
		moneyActorID = replay.Actor.ID
		moneyActorRef = replay.Actor.Ref
	}
	result, err := refundInTx.RefundOrderInTx(ctx, tx, payment.RefundOrderInput{
		TenantID:       tenantID,
		OrderID:        req.OrderID,
		AmountCents:    requestedAmountCents,
		RequireExact:   requireExact,
		IdempotencyKey: refundRequestIdempotencyKey(req.ID),
		Reason:         req.Reason,
		ActorKind:      moneyActorKind,
		ActorID:        moneyActorID,
		ActorRef:       moneyActorRef,
	})
	if err != nil {
		return RefundRequest{}, err
	}
	if result.Order.Status != payment.StatusRefunded || result.RemainingRefundableCents != 0 {
		return RefundRequest{}, payment.ErrRefundFactInvalid
	}
	decidedAt := s.now()
	req, err = updateRefundRequestDecisionTx(ctx, tx, tenantID, requestID, RefundRequestApproved, req.Reason, decidedAt, adminActorID, actorRef)
	if err != nil {
		return RefundRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: commit approve refund request: %w", err)
	}
	return req, nil
}

func (s *PostgresRefundRequestRecorder) RejectRefundRequest(ctx context.Context, tenantID, requestID int64, reason string, adminActorID int64, actorRef string) (RefundRequest, error) {
	if s == nil || s.pool == nil {
		return RefundRequest{}, ErrRefundRequestUnavailable
	}
	actorRef = strings.TrimSpace(actorRef)
	// session-admin 的 TokenID=0:有 actorRef 即有已认证 admin 身份,不再用 int>0 硬拒(role 制单登录)。
	if tenantID <= 0 || requestID <= 0 || (adminActorID <= 0 && actorRef == "") {
		return RefundRequest{}, ErrRefundRequestInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: begin reject refund request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	req, err := getRefundRequestForUpdateTx(ctx, tx, tenantID, requestID)
	if err != nil {
		return RefundRequest{}, err
	}
	if req.Status != RefundRequestPending {
		if err := tx.Commit(ctx); err != nil {
			return RefundRequest{}, fmt.Errorf("paymenthttp: commit reject replay: %w", err)
		}
		return req, nil
	}
	// 兼容历史拆分事务遗留：如果资金事实已存在，pending 申请不能被改标为 rejected。
	// 管理员应通过批准路径做完整幂等复核并收敛状态。
	alreadyRefunded, err := refundRequestHasRefundFactTx(ctx, tx, req)
	if err != nil {
		return RefundRequest{}, err
	}
	if alreadyRefunded {
		return RefundRequest{}, ErrRefundRequestAlreadyResolved
	}
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		req.Reason = trimmed
	}
	req, err = updateRefundRequestDecisionTx(ctx, tx, tenantID, requestID, RefundRequestRejected, req.Reason, s.now(), adminActorID, actorRef)
	if err != nil {
		return RefundRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: commit reject refund request: %w", err)
	}
	return req, nil
}

type refundRequestMoneyActor struct {
	Kind string
	ID   int64
	Ref  string
}

type refundRequestMoneyReplay struct {
	Actor                refundRequestMoneyActor
	RequestedAmountCents int64
	RequireExact         bool
}

func refundRequestOrderAmountTx(ctx context.Context, tx pgx.Tx, req RefundRequest) (int64, error) {
	var amountCents int64
	err := tx.QueryRow(ctx, `
SELECT amount_cents
FROM payment_orders
WHERE tenant_id=$1
  AND id=$2
  AND user_id=$3
  AND order_kind='topup'`, req.TenantID, req.OrderID, req.UserID).Scan(&amountCents)
	if err == pgx.ErrNoRows {
		return 0, payment.ErrOrderNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("paymenthttp: read refund request order amount: %w", err)
	}
	return amountCents, nil
}

// refundRequestReplayTx 只在恢复“资金已提交、申请仍 pending”的历史窗口时使用。
// 它保留原请求语义和原资金操作者，当前接管管理员只记录为申请决策者。
func refundRequestReplayTx(ctx context.Context, tx pgx.Tx, req RefundRequest) (refundRequestMoneyReplay, bool, error) {
	var replay refundRequestMoneyReplay
	err := tx.QueryRow(ctx, `
SELECT requested_amount_cents, require_exact,
       actor_kind, COALESCE(actor_id, 0), COALESCE(actor_ref, '')
FROM payment_refunds
WHERE tenant_id=$1 AND idempotency_key=$2`, req.TenantID, refundRequestIdempotencyKey(req.ID)).Scan(
		&replay.RequestedAmountCents, &replay.RequireExact,
		&replay.Actor.Kind, &replay.Actor.ID, &replay.Actor.Ref,
	)
	if err == pgx.ErrNoRows {
		return refundRequestMoneyReplay{}, false, nil
	}
	if err != nil {
		return refundRequestMoneyReplay{}, false, fmt.Errorf("paymenthttp: read refund request replay fact: %w", err)
	}
	replay.Actor.Kind = strings.TrimSpace(replay.Actor.Kind)
	replay.Actor.Ref = strings.TrimSpace(replay.Actor.Ref)
	if replay.RequestedAmountCents <= 0 ||
		replay.Actor.Kind != payment.ActorKindAdmin ||
		(replay.Actor.ID <= 0 && replay.Actor.Ref == "") {
		return refundRequestMoneyReplay{}, false, payment.ErrRefundFactInvalid
	}
	return replay, true, nil
}

func refundRequestHasRefundFactTx(ctx context.Context, tx pgx.Tx, req RefundRequest) (bool, error) {
	key := refundRequestIdempotencyKey(req.ID)
	var exists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM payment_refunds
	WHERE tenant_id=$1
	  AND idempotency_key=$2
) OR EXISTS (
	SELECT 1
	FROM payment_orders
	WHERE tenant_id=$1
	  AND id=$3
	  AND status='refunded'
)`, req.TenantID, key, req.OrderID).Scan(&exists); err != nil {
		return false, fmt.Errorf("paymenthttp: check refund request refund fact: %w", err)
	}
	return exists, nil
}

func getRefundRequestForUpdateTx(ctx context.Context, tx pgx.Tx, tenantID, requestID int64) (RefundRequest, error) {
	req, err := scanRefundRequest(tx.QueryRow(ctx, `
SELECT id, tenant_id, order_id, user_id, COALESCE(reason, ''), status, created_at, decided_at, decided_by
FROM payment_refund_requests
WHERE tenant_id=$1 AND id=$2
FOR UPDATE`, tenantID, requestID))
	if err == pgx.ErrNoRows {
		return RefundRequest{}, ErrRefundRequestNotFound
	}
	if err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: get refund request: %w", err)
	}
	return req, nil
}

func updateRefundRequestDecisionTx(ctx context.Context, tx pgx.Tx, tenantID, requestID int64, status RefundRequestStatus, reason string, decidedAt time.Time, adminActorID int64, actorRef string) (RefundRequest, error) {
	// session-admin 的 adminActorID=0 → decided_by 落 NULL(不误归 id 0),归属靠 decided_by_actor。
	var decidedBy any
	if adminActorID > 0 {
		decidedBy = adminActorID
	}
	req, err := scanRefundRequest(tx.QueryRow(ctx, `
UPDATE payment_refund_requests
SET status=$3, reason=$4, decided_at=$5, decided_by=$6, decided_by_actor=$7
WHERE tenant_id=$1 AND id=$2
RETURNING id, tenant_id, order_id, user_id, COALESCE(reason, ''), status, created_at, decided_at, decided_by`,
		tenantID, requestID, status, nullableRefundRequestText(reason), decidedAt.UTC(), decidedBy, nullableRefundRequestText(actorRef)))
	if err != nil {
		return RefundRequest{}, fmt.Errorf("paymenthttp: update refund request decision: %w", err)
	}
	return req, nil
}

func scanRefundRequest(row interface{ Scan(...any) error }) (RefundRequest, error) {
	var req RefundRequest
	var status string
	var decidedAt sql.NullTime
	var decidedBy sql.NullInt64
	if err := row.Scan(&req.ID, &req.TenantID, &req.OrderID, &req.UserID, &req.Reason, &status, &req.CreatedAt, &decidedAt, &decidedBy); err != nil {
		return RefundRequest{}, err
	}
	req.Status = RefundRequestStatus(status)
	req.CreatedAt = req.CreatedAt.UTC()
	if decidedAt.Valid {
		t := decidedAt.Time.UTC()
		req.DecidedAt = &t
	}
	if decidedBy.Valid {
		req.DecidedBy = decidedBy.Int64
	}
	return req, nil
}

func nullableRefundRequestText(s string) any {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	return trimmed
}
