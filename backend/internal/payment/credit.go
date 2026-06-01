package payment

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func creditUserBalanceTx(ctx context.Context, tx pgx.Tx, tenantID, userID int64, amount decimal.Decimal, now time.Time) (decimal.Decimal, error) {
	if tenantID <= 0 || userID <= 0 || !amount.IsPositive() || !fitsMoneyColumn(amount) {
		return decimal.Decimal{}, ErrInvalidInput
	}
	var balance decimal.Decimal
	if err := tx.QueryRow(ctx, `
INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
VALUES ($1, $2, $3, 0, 1, $4)
ON CONFLICT (tenant_id, user_id) DO UPDATE
SET balance = user_balances.balance + EXCLUDED.balance,
    version = user_balances.version + 1,
    updated_at = EXCLUDED.updated_at
RETURNING balance`,
		tenantID, userID, amount, now,
	).Scan(&balance); err != nil {
		return decimal.Decimal{}, fmt.Errorf("payment: credit user balance: %w", err)
	}
	return balance, nil
}
