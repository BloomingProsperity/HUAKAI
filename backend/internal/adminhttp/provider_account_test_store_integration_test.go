//go:build integration_pg

package adminhttp

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/accountprobe"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestProviderAccountProbeObservationAndLogCommitAtomically(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openProviderAccountProbePool(t, ctx)
	defer pool.Close()

	tenantID, accountID := seedProviderAccountProbe(t, ctx, pool)
	store := NewProviderAccountTestStoreAdapter(pool)
	probeAt := time.Date(2026, 7, 24, 12, 34, 56, 0, time.UTC)
	base := providerAccountTestRecordInput{
		Identity: admin.AdminIdentity{TokenID: 305, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID},
		TenantID: tenantID, AccountID: accountID, RequestID: "req-probe-atomic",
		Result: accountprobe.Result{
			OK: true, Attempted: true, Model: "probe-model", ProtocolFamily: "openai_chat",
			StatusCode: 200, LatencyMS: 73, TestedAt: probeAt,
			HealthSignal: channelhealth.SignalSuccess, HealthSignalRecorded: true,
		},
	}

	invalid := base
	invalid.Identity.Role = "invalid_role"
	if err := store.RecordProviderAccountTest(ctx, invalid); err == nil {
		t.Fatal("非法日志角色应让事务失败")
	}
	assertProviderAccountProbeState(t, ctx, pool, tenantID, accountID, false, 0, 0)

	if err := store.RecordProviderAccountTest(ctx, base); err != nil {
		t.Fatalf("记录主动探测: %v", err)
	}
	assertProviderAccountProbeState(t, ctx, pool, tenantID, accountID, true, 73, 1)
}

func openProviderAccountProbePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Fatal("integration_pg 必须显式设置 HUAKAI_DATABASE_URL")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 PostgreSQL: %v", err)
	}
	return pool
}

func seedProviderAccountProbe(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, int64) {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var tenantID, providerID, poolID, channelID, accountID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants(name) VALUES($1) RETURNING id`, "probe-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("插入租户: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO providers(tenant_id, code, display_name, upstream_protocol)
VALUES($1,$2,$3,'openai_chat') RETURNING id`,
		tenantID, "probe-provider-"+suffix, "Probe "+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("插入 provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups(tenant_id,name) VALUES($1,$2) RETURNING id`,
		tenantID, "probe-pool-"+suffix,
	).Scan(&poolID); err != nil {
		t.Fatalf("插入池: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels(tenant_id,pool_group_id,name) VALUES($1,$2,$3) RETURNING id`,
		tenantID, poolID, "probe-channel-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("插入渠道: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO provider_accounts(tenant_id,provider_id,channel_id,name,account_type,priority,enabled)
VALUES($1,$2,$3,$4,'api_key',100,true) RETURNING id`,
		tenantID, providerID, channelID, "probe-account-"+suffix,
	).Scan(&accountID); err != nil {
		t.Fatalf("插入账号: %v", err)
	}
	return tenantID, accountID
}

func assertProviderAccountProbeState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, accountID int64,
	wantProbe bool,
	wantLatency int32,
	wantLogs int,
) {
	t.Helper()
	var probeAt pgtype.Timestamptz
	var latency *int32
	if err := pool.QueryRow(ctx, `
SELECT last_probe_at, last_probe_latency_ms
FROM provider_accounts
WHERE tenant_id=$1 AND id=$2`, tenantID, accountID).Scan(&probeAt, &latency); err != nil {
		t.Fatalf("读取主动探测状态: %v", err)
	}
	if probeAt.Valid != wantProbe {
		t.Fatalf("last_probe_at.Valid=%v want %v", probeAt.Valid, wantProbe)
	}
	if wantProbe {
		if latency == nil || *latency != wantLatency {
			t.Fatalf("last_probe_latency_ms=%v want %d", latency, wantLatency)
		}
	} else if latency != nil {
		t.Fatalf("事务回滚后 latency 应为空，实际 %v", *latency)
	}
	var logs int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM admin_audit_events
WHERE tenant_id=$1 AND target_id=$2 AND action='test_provider_account'`,
		tenantID, accountID,
	).Scan(&logs); err != nil {
		t.Fatalf("读取探测日志: %v", err)
	}
	if logs != wantLogs {
		t.Fatalf("探测日志数=%d want %d", logs, wantLogs)
	}
}
