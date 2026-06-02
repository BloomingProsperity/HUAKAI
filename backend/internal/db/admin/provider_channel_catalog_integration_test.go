//go:build integration_pg

package admin

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProviderChannelCatalogQueriesAreTenantScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantA := insertCatalogTenant(t, ctx, pool, "catalog-a-"+suffix)
	tenantB := insertCatalogTenant(t, ctx, pool, "catalog-b-"+suffix)
	t.Cleanup(func() {
		c := context.Background()
		for _, tenantID := range []int64{tenantA, tenantB} {
			_, _ = pool.Exec(c, `DELETE FROM channels WHERE tenant_id = $1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id = $1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM providers WHERE tenant_id = $1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenantID)
		}
	})

	insertCatalogProvider(t, ctx, pool, tenantA, "anthropic-"+suffix, "Anthropic", "anthropic_messages", true, false)
	insertCatalogProvider(t, ctx, pool, tenantA, "deleted-"+suffix, "Deleted", "openai_chat", true, true)
	insertCatalogProvider(t, ctx, pool, tenantB, "openai-"+suffix, "OpenAI", "openai_chat", true, false)

	providers, err := q.ListAdminProvidersByTenant(ctx, ListAdminProvidersByTenantParams{
		TenantID:   tenantA,
		PageLimit:  10,
		PageOffset: 0,
	})
	if err != nil {
		t.Fatalf("list providers: %v", err)
	}
	if len(providers) != 1 || providers[0].Code != "anthropic-"+suffix {
		t.Fatalf("providers=%+v want only tenant A non-deleted provider", providers)
	}

	poolGroupA := insertCatalogPoolGroup(t, ctx, pool, tenantA, "pool-a-"+suffix)
	poolGroupB := insertCatalogPoolGroup(t, ctx, pool, tenantB, "pool-b-"+suffix)
	insertCatalogChannel(t, ctx, pool, tenantA, poolGroupA, "primary-"+suffix, []int32{401, 429}, true, false)
	insertCatalogChannel(t, ctx, pool, tenantA, poolGroupA, "secondary-"+suffix, []int32{500, 529}, false, false)
	insertCatalogChannel(t, ctx, pool, tenantA, poolGroupA, "deleted-"+suffix, []int32{403}, true, true)
	insertCatalogChannel(t, ctx, pool, tenantB, poolGroupB, "other-tenant-"+suffix, []int32{401}, true, false)

	channels, err := q.ListAdminChannelsByTenant(ctx, ListAdminChannelsByTenantParams{
		TenantID:   tenantA,
		PageLimit:  1,
		PageOffset: 1,
	})
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(channels) != 1 || channels[0].Name != "secondary-"+suffix {
		t.Fatalf("channels=%+v want second tenant A non-deleted channel", channels)
	}
	if !reflect.DeepEqual(channels[0].FailoverStatusCodes, []int32{500, 529}) {
		t.Fatalf("failover codes=%v want [500 529]", channels[0].FailoverStatusCodes)
	}
}

func insertCatalogTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert tenant %s: %v", name, err)
	}
	return id
}

func insertCatalogProvider(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, code, name, protocol string, enabled bool, deleted bool) int64 {
	t.Helper()
	var deletedExpr any
	if deleted {
		deletedExpr = time.Now().UTC()
	}
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol, enabled, deleted_at)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		tenantID, code, name, protocol, enabled, deletedExpr,
	).Scan(&id); err != nil {
		t.Fatalf("insert provider %s: %v", code, err)
	}
	return id
}

func insertCatalogPoolGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, name,
	).Scan(&id); err != nil {
		t.Fatalf("insert pool group %s: %v", name, err)
	}
	return id
}

func insertCatalogChannel(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, poolGroupID int64, name string, codes []int32, enabled bool, deleted bool) int64 {
	t.Helper()
	var deletedExpr any
	if deleted {
		deletedExpr = time.Now().UTC()
	}
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name, failover_status_codes, enabled, deleted_at)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		tenantID, poolGroupID, name, codes, enabled, deletedExpr,
	).Scan(&id); err != nil {
		t.Fatalf("insert channel %s: %v", name, err)
	}
	return id
}
