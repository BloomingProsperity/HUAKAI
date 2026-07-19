package channelprobe

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
)

const defaultActiveChannelLimit = 500

type PostgresActiveChannelLister struct {
	pool  *pgxpool.Pool
	limit int
}

func NewPostgresActiveChannelLister(pool *pgxpool.Pool, limit int) *PostgresActiveChannelLister {
	if limit <= 0 {
		limit = defaultActiveChannelLimit
	}
	return &PostgresActiveChannelLister{pool: pool, limit: limit}
}

func (l *PostgresActiveChannelLister) ListActiveChannels(ctx context.Context) ([]ActiveChannel, error) {
	if l == nil || l.pool == nil {
		return nil, ErrNotConfigured
	}
	limit := l.limit
	if limit <= 0 {
		limit = defaultActiveChannelLimit
	}
	rows, err := l.pool.Query(ctx, `
SELECT
    ac.tenant_id,
    ac.vendor,
    pa.id AS provider_account_id,
    ac.id AS account_credential_id,
    ac.credential_version
FROM channels ch
JOIN provider_accounts pa
  ON pa.tenant_id = ch.tenant_id
 AND pa.channel_id = ch.id
JOIN account_credentials ac
  ON ac.tenant_id = pa.tenant_id
 AND ac.provider_account_id = pa.id
WHERE ch.enabled = true
  AND ch.deleted_at IS NULL
  AND pa.enabled = true
  AND pa.deleted_at IS NULL
  AND pa.health_state = 'healthy'
  AND pa.credential_state IN ('valid', 'refreshing_with_grace')
  AND ac.deleted_at IS NULL
  AND ac.state IN ('active', 'refreshing_with_grace')
ORDER BY ac.tenant_id, pa.id, ac.id
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanActiveChannels(rows)
}

// PostgresRampingChannelLister 列出当前处于 ramping，或 cooling_down 且已到期的渠道，
// 供后台恢复协调器处理。它不会扫描正常活跃渠道，也不会凭空创建健康记录。
type PostgresRampingChannelLister struct {
	pool  *pgxpool.Pool
	limit int
}

func NewPostgresRampingChannelLister(pool *pgxpool.Pool, limit int) *PostgresRampingChannelLister {
	if limit <= 0 {
		limit = defaultActiveChannelLimit
	}
	return &PostgresRampingChannelLister{pool: pool, limit: limit}
}

func (l *PostgresRampingChannelLister) ListActiveChannels(ctx context.Context) ([]ActiveChannel, error) {
	if l == nil || l.pool == nil {
		return nil, ErrNotConfigured
	}
	limit := l.limit
	if limit <= 0 {
		limit = defaultActiveChannelLimit
	}
	rows, err := l.pool.Query(ctx, `
SELECT
    tenant_id,
    vendor,
    provider_account_id,
    account_credential_id,
    credential_version
FROM channel_health_state
WHERE (state = 'ramping'
       OR (state = 'cooling_down' AND cooldown_until IS NOT NULL AND cooldown_until <= now()))
  AND provider_account_id IS NOT NULL
ORDER BY tenant_id, provider_account_id
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanActiveChannels(rows)
}

func scanActiveChannels(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]ActiveChannel, error) {
	out := make([]ActiveChannel, 0)
	for rows.Next() {
		var key channelhealth.ChannelKey
		if err := rows.Scan(
			&key.TenantID,
			&key.Vendor,
			&key.ProviderAccountID,
			&key.AccountCredentialID,
			&key.CredentialVersion,
		); err != nil {
			return nil, err
		}
		key.ChannelID = key.StableChannelID()
		out = append(out, ActiveChannel{ChannelID: key.ChannelID, Key: key})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
