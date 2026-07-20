package quotaprobe

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotConfigured = errors.New("quota probe 未配置")

type PostgresAccountLister struct {
	pool *pgxpool.Pool
}

func NewPostgresAccountLister(pool *pgxpool.Pool) *PostgresAccountLister {
	return &PostgresAccountLister{pool: pool}
}

func (l *PostgresAccountLister) ListQuotaProbeAccounts(ctx context.Context) ([]Account, error) {
	if l == nil || l.pool == nil {
		return nil, ErrNotConfigured
	}
	rows, err := l.pool.Query(ctx, `
SELECT DISTINCT pa.tenant_id, pa.id
FROM provider_accounts pa
JOIN account_credentials ac
  ON ac.tenant_id = pa.tenant_id
 AND ac.provider_account_id = pa.id
WHERE pa.enabled = true
  AND pa.deleted_at IS NULL
  AND ac.deleted_at IS NULL
	  AND (
	      (ac.vendor = 'anthropic' AND ac.auth_mode = 'claude_ai_oauth')
	      OR (ac.vendor = 'antigravity' AND ac.auth_mode = 'oauth')
	      OR (ac.vendor = 'gemini' AND ac.auth_mode IN ('oauth', 'code_assist', 'google_one', 'antigravity'))
	      OR (ac.vendor = 'openai' AND ac.auth_mode IN ('chatgpt_oauth', 'codex_cli_oauth', 'codex_web_oauth'))
	      OR (ac.vendor = 'grok' AND ac.auth_mode = 'xai_oauth')
	  )
  AND (
      ac.state = 'active'
      OR (ac.state = 'refreshing_with_grace' AND (ac.grace_until IS NULL OR ac.grace_until > NOW()))
  )
ORDER BY pa.tenant_id, pa.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]Account, 0)
	for rows.Next() {
		var account Account
		if err := rows.Scan(&account.TenantID, &account.ProviderAccountID); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}
