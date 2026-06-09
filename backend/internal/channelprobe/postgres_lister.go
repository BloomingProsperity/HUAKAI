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
