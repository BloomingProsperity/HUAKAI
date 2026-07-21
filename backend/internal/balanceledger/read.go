package balanceledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// TenantWalletSnapshot 是下级租户经营钱包的当前投影。
type TenantWalletSnapshot struct {
	TenantID     int64
	Balance      decimal.Decimal
	CurrencyCode string
	UpdatedAt    time.Time
}

// BalanceTransaction 是人工额度下发、收回与租户内分发的永久事实。
type BalanceTransaction struct {
	ID           int64
	TenantID     int64
	Operation    string
	TargetUserID int64
	SignedAmount decimal.Decimal
	CurrencyCode string
	ActorRole    string
	ActorRef     string
	Reason       string
	RequestID    string
	CreatedAt    time.Time
}

type ListTransactionsInput struct {
	TenantID int64
	UserID   int64
	Limit    int
	Offset   int
}

type AdminBalanceReader interface {
	GetTenantWallet(context.Context, int64) (TenantWalletSnapshot, error)
	ListBalanceTransactions(context.Context, ListTransactionsInput) ([]BalanceTransaction, error)
}

func (s *Service) GetTenantWallet(ctx context.Context, tenantID int64) (TenantWalletSnapshot, error) {
	reader, ok := s.balanceReader()
	if !ok || tenantID <= 0 {
		if tenantID <= 0 {
			return TenantWalletSnapshot{}, ErrInvalidInput
		}
		return TenantWalletSnapshot{}, ErrStoreNotConfigured
	}
	return reader.GetTenantWallet(ctx, tenantID)
}

func (s *Service) ListBalanceTransactions(ctx context.Context, input ListTransactionsInput) ([]BalanceTransaction, error) {
	reader, ok := s.balanceReader()
	if !ok {
		return nil, ErrStoreNotConfigured
	}
	if input.TenantID <= 0 || input.UserID < 0 || input.Offset < 0 {
		return nil, ErrInvalidInput
	}
	if input.Limit <= 0 {
		input.Limit = 50
	}
	if input.Limit > 200 {
		return nil, ErrInvalidInput
	}
	return reader.ListBalanceTransactions(ctx, input)
}

func (s *Service) balanceReader() (AdminBalanceReader, bool) {
	if s == nil || s.store == nil {
		return nil, false
	}
	reader, ok := s.store.(AdminBalanceReader)
	return reader, ok
}

func (s *PostgresStore) GetTenantWallet(ctx context.Context, tenantID int64) (TenantWalletSnapshot, error) {
	if s == nil || s.pool == nil || s.platformTenantID <= 0 {
		return TenantWalletSnapshot{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 {
		return TenantWalletSnapshot{}, ErrInvalidInput
	}
	if tenantID == s.platformTenantID {
		return TenantWalletSnapshot{}, ErrBalanceAdjustmentForbidden
	}
	var snapshot TenantWalletSnapshot
	var status string
	var deleted bool
	err := s.pool.QueryRow(ctx, `
SELECT tenant.id, tenant.status, tenant.deleted_at IS NOT NULL,
       COALESCE(wallet.balance, 0), COALESCE(wallet.updated_at, tenant.updated_at)
FROM tenants tenant
LEFT JOIN tenant_wallets wallet ON wallet.tenant_id=tenant.id
WHERE tenant.id=$1`, tenantID).Scan(
		&snapshot.TenantID, &status, &deleted, &snapshot.Balance, &snapshot.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return TenantWalletSnapshot{}, ErrTenantNotFound
	}
	if err != nil {
		return TenantWalletSnapshot{}, fmt.Errorf("balance ledger: read tenant wallet: %w", err)
	}
	if status != "active" || deleted {
		return TenantWalletSnapshot{}, ErrAccountInactive
	}
	snapshot.CurrencyCode = "USD"
	snapshot.UpdatedAt = snapshot.UpdatedAt.UTC()
	return snapshot, nil
}

func (s *PostgresStore) ListBalanceTransactions(ctx context.Context, input ListTransactionsInput) ([]BalanceTransaction, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, operation, COALESCE(target_user_id, 0), amount,
       currency_code, actor_role, actor_ref, reason, COALESCE(request_id, ''), created_at
FROM balance_ledger_transactions
WHERE tenant_id=$1 AND ($2::bigint=0 OR target_user_id=$2)
ORDER BY created_at DESC, id DESC
LIMIT $3 OFFSET $4`, input.TenantID, input.UserID, input.Limit, input.Offset)
	if err != nil {
		return nil, fmt.Errorf("balance ledger: list transactions: %w", err)
	}
	defer rows.Close()
	items := make([]BalanceTransaction, 0)
	for rows.Next() {
		var item BalanceTransaction
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.Operation, &item.TargetUserID, &item.SignedAmount,
			&item.CurrencyCode, &item.ActorRole, &item.ActorRef, &item.Reason, &item.RequestID, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("balance ledger: scan transaction: %w", err)
		}
		if strings.HasSuffix(item.Operation, "_debit") {
			item.SignedAmount = item.SignedAmount.Neg()
		}
		item.CurrencyCode = strings.TrimSpace(item.CurrencyCode)
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("balance ledger: iterate transactions: %w", err)
	}
	return items, nil
}
