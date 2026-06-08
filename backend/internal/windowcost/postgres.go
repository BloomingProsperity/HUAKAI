package windowcost

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresLister implements Lister over a pgx pool.
type PostgresLister struct {
	pool *pgxpool.Pool
}

// NewPostgresLister constructs a PostgresLister.
func NewPostgresLister(pool *pgxpool.Pool) *PostgresLister {
	return &PostgresLister{pool: pool}
}

// ListLimitedAccounts returns accounts with window_cost_limit_cents > 0 and
// a non-null session_window_5h_start (active window).
func (l *PostgresLister) ListLimitedAccounts(ctx context.Context) ([]AccountRecord, error) {
	const q = `
SELECT id, tenant_id, session_window_5h_start
FROM provider_accounts
WHERE window_cost_limit_cents > 0
  AND session_window_5h_start IS NOT NULL
  AND deleted_at IS NULL
`
	rows, err := l.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("windowcost: query limited accounts: %w", err)
	}
	defer rows.Close()

	var out []AccountRecord
	for rows.Next() {
		var r AccountRecord
		var windowStart time.Time
		if err := rows.Scan(&r.ID, &r.TenantID, &windowStart); err != nil {
			return nil, fmt.Errorf("windowcost: scan limited account: %w", err)
		}
		r.SessionWindow5hStart = windowStart
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("windowcost: iterate limited accounts: %w", err)
	}
	return out, nil
}

// PostgresAggregator implements Aggregator over a pgx pool.
type PostgresAggregator struct {
	pool *pgxpool.Pool
}

// NewPostgresAggregator constructs a PostgresAggregator.
func NewPostgresAggregator(pool *pgxpool.Pool) *PostgresAggregator {
	return &PostgresAggregator{pool: pool}
}

// SumWindowCost sums usage_records.actual_cost for the account since
// windowStart. actual_cost is numeric(20,8) in USD; we convert to cents
// (multiply by 100, truncate) for integer comparison in the gate.
func (a *PostgresAggregator) SumWindowCost(ctx context.Context, accountID int64, windowStart time.Time) (int64, error) {
	const q = `
SELECT COALESCE(SUM(actual_cost), 0)::numeric(20,8)::text
FROM usage_records
WHERE provider_account_id = $1
  AND settled_at >= $2
`
	var raw string
	if err := a.pool.QueryRow(ctx, q, accountID, windowStart).Scan(&raw); err != nil {
		return 0, fmt.Errorf("windowcost: sum window cost account=%d: %w", accountID, err)
	}
	// Parse the numeric text and convert to cents (truncate, not round).
	f, ok := new(big.Float).SetString(raw)
	if !ok {
		return 0, fmt.Errorf("windowcost: parse cost %q for account=%d", raw, accountID)
	}
	// Multiply by 100 to get cents.
	cents, _ := new(big.Float).Mul(f, big.NewFloat(100)).Int64()
	return cents, nil
}
