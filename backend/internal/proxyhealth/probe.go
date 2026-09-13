package proxyhealth

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"time"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyquality"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxStore 用 raw pgx 实现 Lister + StatusStore；质量快照走与人工探测同一条
// RecordProxyQuality 写入，避免周期路径另写一套 UPDATE。
type pgxStore struct {
	pool *pgxpool.Pool
}

var _ QualityWriter = (*pgxStore)(nil)

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

func (p *pgxStore) RecordQuality(ctx context.Context, tenantID, id int64, obs Observation) (bool, error) {
	grade := proxyquality.Grade(obs.Reachable, obs.LatencyMS)
	source := proxyquality.SourcePeriodic
	ok := obs.Reachable
	latency := obs.LatencyMS
	_, err := admindb.New(p.pool).RecordProxyQuality(ctx, admindb.RecordProxyQualityParams{
		QualityOk:         &ok,
		QualityLatencyMs:  &latency,
		QualityErrorClass: obs.ErrorClass,
		QualityGrade:      &grade,
		QualitySource:     &source,
		TenantID:          tenantID,
		ID:                id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
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

func (p *tcpProber) Probe(ctx context.Context, t ProxyTarget) Observation {
	proxyURL := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(t.Host, strconv.Itoa(t.Port)),
	}
	addresses, err := provider.ResolveProxyEndpointIPs(ctx, proxyURL)
	if err != nil {
		return Observation{ErrorClass: ErrClassTCPUnreachable}
	}
	d := net.Dialer{Timeout: p.timeout}
	start := time.Now()
	for _, address := range addresses {
		conn, dialErr := d.DialContext(ctx, "tcp", net.JoinHostPort(address.String(), strconv.Itoa(t.Port)))
		if dialErr != nil {
			continue
		}
		_ = conn.Close()
		return Observation{Reachable: true, LatencyMS: time.Since(start).Milliseconds()}
	}
	return Observation{LatencyMS: time.Since(start).Milliseconds(), ErrorClass: ErrClassTCPUnreachable}
}
