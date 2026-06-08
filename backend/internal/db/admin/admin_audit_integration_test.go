//go:build integration_pg

package admin

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	rootdb "github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openAdminAuditIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	p, err := rootdb.Open(ctx, rootdb.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestInsertAdminAuditEventPoolGroupCheckConstraints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	actorID := "admin-audit-pool-group-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	targetID := int64(77)
	requestID := actorID + "-request"

	_, err := q.InsertAdminAuditEvent(ctx, InsertAdminAuditEventParams{
		ActorID:    actorID,
		ActorRole:  "platform_admin",
		Action:     "create_pool_group",
		TargetType: "pool_group",
		TargetID:   &targetID,
		RequestID:  &requestID,
		Payload:    []byte(`{"source":"integration_pg"}`),
	})
	if err != nil {
		t.Fatalf("insert pool_group audit event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_audit_events WHERE actor_id = $1`, actorID)
	})

	_, err = q.InsertAdminAuditEvent(ctx, InsertAdminAuditEventParams{
		ActorID:    actorID,
		ActorRole:  "platform_admin",
		Action:     "bogus_pool_group_action",
		TargetType: "pool_group",
		TargetID:   &targetID,
		RequestID:  &requestID,
		Payload:    []byte(`{"source":"integration_pg"}`),
	})
	if err == nil {
		t.Fatalf("bogus action was accepted")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("bogus action returned non-Postgres error: %T %v", err, err)
	}
	if pgErr.Code != "23514" || pgErr.ConstraintName != "admin_audit_events_action_check" {
		t.Fatalf("bogus action error code=%s constraint=%s want CHECK admin_audit_events_action_check",
			pgErr.Code, pgErr.ConstraintName)
	}
}

func TestListAdminProviderAccounts_StateFilterMatchesPost0056Enum(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var tenantID, providerID, poolGroupID, channelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"admin-state-filter-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM channels WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM providers WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, 'Admin State Provider', 'openai_chat') RETURNING id`,
		tenantID, "admin-state-"+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "admin-state-pool-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("insert pool group: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "admin-state-channel-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	healthyID := insertAdminStateFilterProviderAccount(t, ctx, pool, tenantID, providerID, channelID, suffix, "healthy", true)
	revokedID := insertAdminStateFilterProviderAccount(t, ctx, pool, tenantID, providerID, channelID, suffix, "revoked", true)

	activeRows, err := q.ListAdminProviderAccounts(ctx, ListAdminProviderAccountsParams{
		TenantID:    tenantID,
		LimitCount:  10,
		StateFilter: "active",
	})
	if err != nil {
		t.Fatalf("list active provider accounts: %v", err)
	}
	if got := providerAccountIDs(activeRows); len(got) != 1 || got[0] != healthyID {
		t.Fatalf("active ids=%v want only healthy provider account %d; revoked id %d must not match", got, healthyID, revokedID)
	}

	errorRows, err := q.ListAdminProviderAccounts(ctx, ListAdminProviderAccountsParams{
		TenantID:    tenantID,
		LimitCount:  10,
		StateFilter: "error",
	})
	if err != nil {
		t.Fatalf("list error provider accounts: %v", err)
	}
	if got := providerAccountIDs(errorRows); len(got) != 1 || got[0] != revokedID {
		t.Fatalf("error ids=%v want only revoked provider account %d; healthy id %d must not match", got, revokedID, healthyID)
	}
}

func TestTagFilter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var tenantID, providerID, poolGroupID, channelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"admin-tag-filter-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM channels WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM providers WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, 'Admin Tag Provider', 'openai_chat') RETURNING id`,
		tenantID, "admin-tag-"+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "admin-tag-pool-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("insert pool group: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "admin-tag-channel-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	prodA := insertAdminTaggedProviderAccount(t, ctx, pool, tenantID, providerID, channelID, "prod-a-"+suffix, []string{"prod", "blue"})
	prodB := insertAdminTaggedProviderAccount(t, ctx, pool, tenantID, providerID, channelID, "prod-b-"+suffix, []string{"prod"})
	dev := insertAdminTaggedProviderAccount(t, ctx, pool, tenantID, providerID, channelID, "dev-"+suffix, []string{"dev"})

	rows, err := q.ListAdminProviderAccounts(ctx, ListAdminProviderAccountsParams{
		TenantID:   tenantID,
		LimitCount: 10,
		TagFilter:  "prod",
	})
	if err != nil {
		t.Fatalf("list provider accounts by tag: %v", err)
	}
	got := providerAccountIDs(rows)
	if len(got) != 2 || got[0] != prodA || got[1] != prodB {
		t.Fatalf("tag=prod ids=%v want exactly [%d %d]; dev id %d must be excluded", got, prodA, prodB, dev)
	}
}

func insertAdminStateFilterProviderAccount(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID int64,
	providerID int64,
	channelID int64,
	suffix string,
	healthState string,
	enabled bool,
) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type, enabled, health_state
		) VALUES ($1, $2, $3, $4, 'api_key', $5, $6) RETURNING id`,
		tenantID, providerID, channelID, "admin-state-"+healthState+"-"+suffix, enabled, healthState,
	).Scan(&id); err != nil {
		t.Fatalf("insert provider account health_state=%s: %v", healthState, err)
	}
	return id
}

func insertAdminTaggedProviderAccount(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID int64,
	providerID int64,
	channelID int64,
	name string,
	tags []string,
) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type, tags
		) VALUES ($1, $2, $3, $4, 'api_key', $5) RETURNING id`,
		tenantID, providerID, channelID, name, tags,
	).Scan(&id); err != nil {
		t.Fatalf("insert tagged provider account %s: %v", name, err)
	}
	return id
}

func providerAccountIDs(rows []AdminProviderAccountRow) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}
