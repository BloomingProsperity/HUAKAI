package proxyhealth

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxStore 用 raw pgx 实现 Lister + StatusStore(仿 proxy_resolver,不依赖
// admindb/sqlc)。
type pgxStore struct {
	pool *pgxpool.Pool
}

// NewPostgresLister / NewPostgresStatusStore 共用一个 pgx 后端。
func NewPostgresLister(pool *pgxpool.Pool) Lister           { return &pgxStore{pool: pool} }
func NewPostgresStatusStore(pool *pgxpool.Pool) StatusStore { return &pgxStore{pool: pool} }

func (p *pgxStore) List(ctx context.Context) ([]ProxyTarget, error) {
	const q = `
		SELECT p.id, p.tenant_id, p.status, p.host, p.port
		FROM proxies p
		JOIN tenants t
		  ON t.id = p.tenant_id
		 AND t.status = 'active'
		 AND t.deleted_at IS NULL
		WHERE p.deleted_at IS NULL AND p.status IN ('active','dead')
		ORDER BY COALESCE(p.last_check_at, to_timestamp(0)) ASC
		LIMIT $1`
	rows, err := p.pool.Query(ctx, q, maxPerTick)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProxyTarget
	for rows.Next() {
		var t ProxyTarget
		if err := rows.Scan(&t.ID, &t.TenantID, &t.Status, &t.Host, &t.Port); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *pgxStore) Touch(ctx context.Context, tenantID, id int64, expectedStatus string) (bool, error) {
	tag, err := p.pool.Exec(ctx, `
		UPDATE proxies
		SET last_check_at = NOW()
		WHERE id = $1
		  AND tenant_id = $2
		  AND status = $3
		  AND deleted_at IS NULL`,
		id, tenantID, expectedStatus)
	return tag.RowsAffected() == 1, err
}

func (p *pgxStore) SetStatus(ctx context.Context, tenantID, id int64, expectedStatus, status string) (bool, error) {
	tag, err := p.pool.Exec(ctx, `
		UPDATE proxies
		SET status = $1, last_check_at = NOW()
		WHERE id = $2
		  AND tenant_id = $3
		  AND status = $4
		  AND deleted_at IS NULL`,
		status, id, tenantID, expectedStatus)
	return tag.RowsAffected() == 1, err
}

// tcpProber 用 TCP 连通性判代理存活:连得上 host:port 即视为活。它【只碰代理】、
// 绝不碰上游,故不会触发上游 rate-limit;也是代理最常见故障(宕机/不可达)的检出。
type tcpProber struct {
	timeout time.Duration
}

func NewTCPProber(timeout time.Duration) Prober {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &tcpProber{timeout: timeout}
}

func (p *tcpProber) Probe(ctx context.Context, t ProxyTarget) bool {
	proxyURL := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(t.Host, strconv.Itoa(t.Port)),
	}
	addresses, err := provider.ResolveProxyEndpointIPs(ctx, proxyURL)
	if err != nil {
		return false
	}
	d := net.Dialer{Timeout: p.timeout}
	for _, address := range addresses {
		conn, dialErr := d.DialContext(ctx, "tcp", net.JoinHostPort(address.String(), strconv.Itoa(t.Port)))
		if dialErr != nil {
			continue
		}
		_ = conn.Close()
		return true
	}
	return false
}
