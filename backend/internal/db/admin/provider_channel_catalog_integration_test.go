//go:build integration_pg

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// 变异:从 provider 的唯一性/作用域处理中去掉 tenant_id;租户 B 里相同的 code
// 必须仍可插入,而租户 A 的重复 code 必须仍触发唯一冲突错误。
func TestCreateProvider_TenantScopedUnique(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantA := insertCatalogTenant(t, ctx, pool, "provider-create-a-"+suffix)
	tenantB := insertCatalogTenant(t, ctx, pool, "provider-create-b-"+suffix)
	cleanupCatalogTenants(t, pool, tenantA, tenantB)

	code := "mistral-" + suffix
	created, err := q.InsertProvider(ctx, InsertProviderParams{
		TenantID: tenantA, Code: code, DisplayName: "Mistral A",
		UpstreamProtocol: "openai_chat", Enabled: true,
	})
	if err != nil {
		t.Fatalf("insert provider tenant A: %v", err)
	}
	if created.Code != code || created.DisplayName != "Mistral A" || !created.Enabled {
		t.Fatalf("created=%+v", created)
	}

	_, err = q.InsertProvider(ctx, InsertProviderParams{
		TenantID: tenantA, Code: code, DisplayName: "Mistral A duplicate",
		UpstreamProtocol: "openai_chat", Enabled: true,
	})
	if !isProviderCatalogUniqueViolation(err) {
		t.Fatalf("duplicate err=%v, want uq_providers_tenant_code violation", err)
	}

	createdB, err := q.InsertProvider(ctx, InsertProviderParams{
		TenantID: tenantB, Code: code, DisplayName: "Mistral B",
		UpstreamProtocol: "openai_chat", Enabled: true,
	})
	if err != nil {
		t.Fatalf("insert same code tenant B: %v", err)
	}
	if createdB.Code != code || createdB.ID == created.ID {
		t.Fatalf("createdB=%+v createdA=%+v", createdB, created)
	}
}

// 变异:在 UPDATE 中忽略 tenant_id/code,或把无命中行当成功处理;更新后的
// provider 必须反映出请求的字段,而不存在的 code 必须暴露 pgx.ErrNoRows。
func TestUpdateProvider(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := insertCatalogTenant(t, ctx, pool, "provider-update-"+suffix)
	cleanupCatalogTenants(t, pool, tenantID)

	code := "anthropic-update-" + suffix
	insertCatalogProvider(t, ctx, pool, tenantID, code, "Anthropic", "anthropic_messages", true, false)

	updated, err := q.UpdateProvider(ctx, UpdateProviderParams{
		TenantID: tenantID, Code: code, DisplayName: "Anthropic Updated",
		UpstreamProtocol: "openai_responses", Enabled: false,
	})
	if err != nil {
		t.Fatalf("update provider: %v", err)
	}
	if updated.Code != code || updated.DisplayName != "Anthropic Updated" ||
		updated.UpstreamProtocol != "openai_responses" || updated.Enabled {
		t.Fatalf("updated=%+v", updated)
	}

	_, err = q.UpdateProvider(ctx, UpdateProviderParams{
		TenantID: tenantID, Code: "missing-" + suffix, DisplayName: "Missing",
		UpstreamProtocol: "openai_chat", Enabled: true,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing update err=%v, want pgx.ErrNoRows", err)
	}
}

// 变异:对一个有活跃 provider_accounts 的 provider 执行硬删除或无守卫的软删除;
// 无论是删掉该 provider 还是返回成功(而非 pgx.ErrNoRows),测试都必须变红。
func TestDeleteProvider_GuardOrSoftDelete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := insertCatalogTenant(t, ctx, pool, "provider-delete-"+suffix)
	cleanupCatalogTenants(t, pool, tenantID)

	freeCode := "free-delete-" + suffix
	insertCatalogProvider(t, ctx, pool, tenantID, freeCode, "Free Delete", "openai_chat", true, false)
	deleted, err := q.SoftDeleteProvider(ctx, SoftDeleteProviderParams{TenantID: tenantID, Code: freeCode})
	if err != nil {
		t.Fatalf("soft delete provider without accounts: %v", err)
	}
	if deleted.Code != freeCode || deleted.Enabled {
		t.Fatalf("deleted=%+v want code %s enabled=false", deleted, freeCode)
	}
	providers, err := q.ListAdminProvidersByTenant(ctx, ListAdminProvidersByTenantParams{
		TenantID: tenantID, PageLimit: 10, PageOffset: 0,
	})
	if err != nil {
		t.Fatalf("list after soft delete: %v", err)
	}
	for _, provider := range providers {
		if provider.Code == freeCode {
			t.Fatalf("soft-deleted provider still listed: %+v", providers)
		}
	}

	guardedCode := "guarded-delete-" + suffix
	providerID := insertCatalogProvider(t, ctx, pool, tenantID, guardedCode, "Guarded Delete", "openai_chat", true, false)
	poolGroupID := insertCatalogPoolGroup(t, ctx, pool, tenantID, "delete-pool-"+suffix)
	channelID := insertCatalogChannel(t, ctx, pool, tenantID, poolGroupID, "delete-channel-"+suffix, []int32{429}, true, false)
	insertCatalogProviderAccount(t, ctx, pool, tenantID, providerID, channelID, "active-account-"+suffix)

	active, err := q.CountActiveProviderAccountsForProvider(ctx, CountActiveProviderAccountsForProviderParams{
		TenantID: tenantID, Code: guardedCode,
	})
	if err != nil {
		t.Fatalf("count active provider accounts: %v", err)
	}
	if active != 1 {
		t.Fatalf("active accounts=%d want 1", active)
	}

	_, err = q.SoftDeleteProvider(ctx, SoftDeleteProviderParams{TenantID: tenantID, Code: guardedCode})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("guarded soft delete err=%v, want pgx.ErrNoRows", err)
	}
	var isDeleted bool
	if err := pool.QueryRow(ctx,
		`SELECT deleted_at IS NOT NULL FROM providers WHERE tenant_id = $1 AND code = $2`,
		tenantID, guardedCode,
	).Scan(&isDeleted); err != nil {
		t.Fatalf("read guarded provider deleted_at: %v", err)
	}
	if isDeleted {
		t.Fatalf("guarded provider %s was soft-deleted despite active account", guardedCode)
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

func cleanupCatalogTenants(t *testing.T, pool *pgxpool.Pool, tenantIDs ...int64) {
	t.Helper()
	t.Cleanup(func() {
		c := context.Background()
		for _, tenantID := range tenantIDs {
			_, _ = pool.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id = $1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM channels WHERE tenant_id = $1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id = $1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM providers WHERE tenant_id = $1`, tenantID)
			_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenantID)
		}
	})
}

func isProviderCatalogUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "uq_providers_tenant_code"
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

func insertCatalogProviderAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, providerID, channelID int64, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, credentials, enabled)
		 VALUES ($1, $2, $3, $4, 'api_key', '{}'::jsonb, true) RETURNING id`,
		tenantID, providerID, channelID, name,
	).Scan(&id); err != nil {
		t.Fatalf("insert provider account %s: %v", name, err)
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

// TestChannelCRUD_TenantScopedAndUnique 针对真实 Postgres 演练 channel CRUD 的
// sqlc 查询:update/delete 上的 tenant_id 围栏、(tenant,pool_group,name) 唯一
// 约束、create/update 上的 pool-group 跨租户 EXISTS 守卫,以及软删后 name 的复用。
//
// 变异守卫:从 UpdateChannel/SoftDeleteChannel 去掉 `tenant_id = $` 会让跨租户
// 断言返回一行(变红);去掉 EXISTS pool-group 守卫会让跨租户 pool 的 create
// 成功(变红)。
func TestChannelCRUD_TenantScopedAndUnique(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantA := insertCatalogTenant(t, ctx, pool, "chcrud-a-"+suffix)
	tenantB := insertCatalogTenant(t, ctx, pool, "chcrud-b-"+suffix)
	cleanupCatalogTenants(t, pool, tenantA, tenantB)

	pgA := insertCatalogPoolGroup(t, ctx, pool, tenantA, "pg-a-"+suffix)
	pgB := insertCatalogPoolGroup(t, ctx, pool, tenantB, "pg-b-"+suffix)
	codes := []int32{401, 429}
	bodyParamStrips := []string{"drop_create"}
	paramOverride := []byte(`{"temperature":0.25,"metadata":{"source":"create"}}`)
	sensitiveWords := []string{"word_create"}

	// 在租户 A 自己的 pool group 里创建成功。
	row, err := q.CreateChannel(ctx, CreateChannelParams{
		TenantID: tenantA, PoolGroupID: pgA, Name: "primary", FailoverStatusCodes: codes,
		BodyParamStrips: bodyParamStrips, ParamOverride: paramOverride,
		SensitiveWords: sensitiveWords, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if row.ID == 0 || row.PoolGroupID != pgA || row.Name != "primary" ||
		!reflect.DeepEqual(row.BodyParamStrips, bodyParamStrips) ||
		!reflect.DeepEqual(row.SensitiveWords, sensitiveWords) ||
		!jsonBytesEqual(row.ParamOverride, paramOverride) {
		t.Fatalf("create row wrong: %+v", row)
	}
	chID := row.ID
	got, err := q.GetAdminChannel(ctx, GetAdminChannelParams{TenantID: tenantA, ID: chID})
	if err != nil {
		t.Fatalf("get created channel: %v", err)
	}
	if got.ID != chID || got.PoolGroupID != pgA || got.Name != "primary" ||
		!reflect.DeepEqual(got.BodyParamStrips, bodyParamStrips) ||
		!jsonBytesEqual(got.ParamOverride, paramOverride) ||
		!reflect.DeepEqual(got.SensitiveWords, sensitiveWords) {
		t.Fatalf("get 未精确回显 create 三门:%+v", got)
	}
	if _, err := q.GetAdminChannel(ctx, GetAdminChannelParams{TenantID: tenantB, ID: chID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-tenant get: err=%v want pgx.ErrNoRows (tenant fence)", err)
	}

	// pool-group 跨租户守卫:租户 A 不能挂载租户 B 的 pool group。
	if _, err := q.CreateChannel(ctx, CreateChannelParams{
		TenantID: tenantA, PoolGroupID: pgB, Name: "x-tenant", FailoverStatusCodes: codes, Enabled: true,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-tenant pool create: err=%v want pgx.ErrNoRows (EXISTS guard)", err)
	}

	// 在未删除行中,(tenant, pool_group, name) 唯一。
	if _, err := q.CreateChannel(ctx, CreateChannelParams{
		TenantID: tenantA, PoolGroupID: pgA, Name: "primary", FailoverStatusCodes: codes, Enabled: true,
	}); !isChannelUniqueViolation(err) {
		t.Fatalf("duplicate name create: err=%v want uq_channels_tenant_pool_name 23505", err)
	}

	// update 上的租户围栏:租户 B 不能更新租户 A 的 channel。
	if _, err := q.UpdateChannel(ctx, UpdateChannelParams{
		TenantID: tenantB, ID: chID, PoolGroupID: pgB, Name: "hijack", FailoverStatusCodes: codes, Enabled: true,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-tenant update: err=%v want pgx.ErrNoRows (tenant fence)", err)
	}

	// 属主更新成功。
	upd, err := q.UpdateChannel(ctx, UpdateChannelParams{
		TenantID: tenantA, ID: chID, PoolGroupID: pgA, Name: "renamed", FailoverStatusCodes: []int32{500},
		SetSensitiveWords: true, SensitiveWords: []string{"word_updated"}, Enabled: false,
	})
	if err != nil {
		t.Fatalf("owner update: %v", err)
	}
	if upd.Name != "renamed" || upd.Enabled ||
		!reflect.DeepEqual(upd.BodyParamStrips, bodyParamStrips) ||
		!jsonBytesEqual(upd.ParamOverride, paramOverride) ||
		!reflect.DeepEqual(upd.SensitiveWords, []string{"word_updated"}) {
		t.Fatalf("update row wrong: %+v", upd)
	}
	listed, err := q.ListAdminChannelsByTenant(ctx, ListAdminChannelsByTenantParams{
		TenantID: tenantA, PageLimit: 10, PageOffset: 0,
	})
	if err != nil {
		t.Fatalf("list updated channels: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != chID ||
		!reflect.DeepEqual(listed[0].BodyParamStrips, bodyParamStrips) ||
		!jsonBytesEqual(listed[0].ParamOverride, paramOverride) ||
		!reflect.DeepEqual(listed[0].SensitiveWords, []string{"word_updated"}) {
		t.Fatalf("list 未回显 update 改一保二结果:%+v", listed)
	}
	got, err = q.GetAdminChannel(ctx, GetAdminChannelParams{TenantID: tenantA, ID: chID})
	if err != nil {
		t.Fatalf("get updated channel: %v", err)
	}
	if !reflect.DeepEqual(got.BodyParamStrips, bodyParamStrips) ||
		!jsonBytesEqual(got.ParamOverride, paramOverride) ||
		!reflect.DeepEqual(got.SensitiveWords, []string{"word_updated"}) {
		t.Fatalf("get 未回显 update 改一保二结果:%+v", got)
	}
	// delete 上的租户围栏:租户 B 不能删除租户 A 的 channel。
	if _, err := q.SoftDeleteChannel(ctx, SoftDeleteChannelParams{TenantID: tenantB, ID: chID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-tenant delete: err=%v want pgx.ErrNoRows (tenant fence)", err)
	}

	// 属主软删除成功,随后 name 可被复用(部分唯一索引)。
	if _, err := q.SoftDeleteChannel(ctx, SoftDeleteChannelParams{TenantID: tenantA, ID: chID}); err != nil {
		t.Fatalf("owner soft delete: %v", err)
	}
	if _, err := q.CreateChannel(ctx, CreateChannelParams{
		TenantID: tenantA, PoolGroupID: pgA, Name: "renamed", FailoverStatusCodes: codes, Enabled: true,
	}); err != nil {
		t.Fatalf("name reuse after soft delete should succeed: %v", err)
	}
}

func isChannelUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "uq_channels_tenant_pool_name"
}

func jsonBytesEqual(left, right []byte) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
