//go:build integration_pg

package proxyadmin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// 针对 proxyadmin 闭环的强真实 Postgres 测试。单元测试用桩 Querier;这些测试
// 针对真实 DB + 真实加密 + 真实租户过滤来证明安全属性——即单元桩触达不到的危害面。
// 运行:HUAKAI_DATABASE_URL=<gate dsn> go test -tags=integration_pg
//   -p 1 ./internal/proxyadmin/...

func openProxyPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open PG: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedProxyTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"proxyadmin-"+label+"-"+uuid.NewString(),
	).Scan(&id); err != nil {
		t.Fatalf("seed tenant %s: %v", label, err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, id)
		_, _ = pool.Exec(c, `DELETE FROM proxies WHERE tenant_id=$1`, id)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, id)
	})
	return id
}

func strptr(s string) *string { return &s }

// TestProxy_SecretEncryptedAtRest 证明代理 auth_secret 在落列之前已被加密——
// 明文凭据在 proxies 表里绝不可被读出。变异:绕过 encryptAuthSecret(存原文)→
// 该列原文等于明文 → 转红。
func TestProxy_SecretEncryptedAtRest(t *testing.T) {
	ctx := context.Background()
	pool := openProxyPool(t, ctx)
	tenantID := seedProxyTenant(t, ctx, pool, "enc")
	svc := New(admindb.New(pool), testKeys(t))

	const plaintext = "SUPER-SECRET-PROXY-CREDENTIAL-9f3a"
	p, err := svc.Create(ctx, CreateInput{
		TenantID: tenantID, Name: "enc-proxy", Protocol: "http", Host: "proxy.example.com", Port: 3128,
		AuthUsername: strptr("puser"), AuthSecret: strptr(plaintext),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var stored *string
	if err := pool.QueryRow(ctx, `SELECT auth_secret FROM proxies WHERE id=$1 AND tenant_id=$2`, p.ID, tenantID).Scan(&stored); err != nil {
		t.Fatalf("read raw auth_secret: %v", err)
	}
	if stored == nil || *stored == "" {
		t.Fatalf("auth_secret must be persisted (encrypted), got empty/nil")
	}
	if *stored == plaintext {
		t.Fatalf("auth_secret stored as PLAINTEXT at rest — encryption bypassed")
	}
	if strings.Contains(*stored, plaintext) {
		t.Fatalf("ciphertext contains the plaintext substring — not encrypted")
	}
}

// TestProxy_CrossTenantIsolation 是核心安全测试:租户 B 绝不能看到、读取、修改
// 或删除租户 A 的代理。变异:从任一代理查询中删掉 tenant_id 谓词 → B 泄露/修改
// A 的行 → 转红。
func TestProxy_CrossTenantIsolation(t *testing.T) {
	ctx := context.Background()
	pool := openProxyPool(t, ctx)
	tenantA := seedProxyTenant(t, ctx, pool, "a")
	tenantB := seedProxyTenant(t, ctx, pool, "b")
	svc := New(admindb.New(pool), testKeys(t))

	a, err := svc.Create(ctx, CreateInput{
		TenantID: tenantA, Name: "a-proxy", Protocol: "socks5", Host: "proxy.example.com", Port: 1080,
		AuthSecret: strptr("a-secret"),
	})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}

	// B 的列表绝不能含 A 的代理。
	bList, err := svc.List(ctx, tenantB)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	for _, p := range bList {
		if p.ID == a.ID {
			t.Fatalf("cross-tenant leak: tenant B list contains tenant A proxy %d", a.ID)
		}
	}

	// B 无法读取 A 的代理。
	if _, err := svc.Get(ctx, tenantB, a.ID); err != ErrNotFound {
		t.Fatalf("cross-tenant Get must be ErrNotFound, got %v", err)
	}

	// B 删除 A 的 id 与普通不存在对象同为 ErrNotFound；A 的代理存活。
	if err := svc.Delete(ctx, tenantB, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant delete err=%v want ErrNotFound", err)
	}
	if _, err := svc.Get(ctx, tenantA, a.ID); err != nil {
		t.Fatalf("tenant A proxy must survive B's delete attempt, got %v", err)
	}

	// B 翻转 A 的 id 状态按租户域表现为不存在；A 的状态仍为 active。
	if err := svc.SetStatus(ctx, tenantB, a.ID, "disabled"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant set-status err=%v want ErrNotFound", err)
	}
	got, err := svc.Get(ctx, tenantA, a.ID)
	if err != nil {
		t.Fatalf("get A after B status attempt: %v", err)
	}
	if got.Status != "active" {
		t.Fatalf("tenant B must not flip tenant A status; got %q want active", got.Status)
	}
}

// PATCH 省略凭据时，SQL 必须原子保留原密文；该测试直接比较数据库列，能抓住
// “改名称顺手把 auth_secret 清空”的真实回归。
func TestProxy_PatchPreservesOmittedSecret(t *testing.T) {
	ctx := context.Background()
	pool := openProxyPool(t, ctx)
	tenantID := seedProxyTenant(t, ctx, pool, "patch-secret")
	svc := New(admindb.New(pool), testKeys(t))

	created, err := svc.Create(ctx, CreateInput{
		TenantID: tenantID, Name: "before", Protocol: "http", Host: "proxy.example.com", Port: 3128,
		AuthUsername: strptr("user"), AuthSecret: strptr("secret"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var before *string
	if err := pool.QueryRow(ctx, `SELECT auth_secret FROM proxies WHERE id=$1`, created.ID).Scan(&before); err != nil {
		t.Fatalf("read before: %v", err)
	}

	if _, err := svc.Patch(ctx, PatchInput{
		TenantID: tenantID,
		ID:       created.ID,
		Name:     PatchField[string]{Set: true, Value: "after"},
	}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	var name string
	var after *string
	if err := pool.QueryRow(ctx, `SELECT name, auth_secret FROM proxies WHERE id=$1`, created.ID).Scan(&name, &after); err != nil {
		t.Fatalf("read after: %v", err)
	}
	if name != "after" {
		t.Fatalf("name=%q want after", name)
	}
	if before == nil || after == nil || *after != *before {
		t.Fatalf("省略凭据后密文发生变化: before=%v after=%v", before, after)
	}
}

// TestProxy_LifecycleAndSecretFreeReads 演练真实的 create->status->delete 状态机,
// 并证明读取结构体暴露的是非凭据字段。Proxy 类型在结构上不含凭据(无该字段),
// 故返回的结构体不可能发生泄露;本测试确认这些值经真实 DB 正确往返。
func TestProxy_LifecycleAndSecretFreeReads(t *testing.T) {
	ctx := context.Background()
	pool := openProxyPool(t, ctx)
	tenantID := seedProxyTenant(t, ctx, pool, "life")
	svc := New(admindb.New(pool), testKeys(t))

	p, err := svc.Create(ctx, CreateInput{
		TenantID: tenantID, Name: "life-proxy", Protocol: "https", Host: "proxy.internal", Port: 8443,
		AuthUsername: strptr("u"), AuthSecret: strptr("s"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Name != "life-proxy" || p.Protocol != "https" || p.Host != "proxy.internal" || p.Port != 8443 || p.Status != "active" {
		t.Fatalf("create round-trip mismatch: %+v", p)
	}

	if err := svc.SetStatus(ctx, tenantID, p.ID, "disabled"); err != nil {
		t.Fatalf("set-status: %v", err)
	}
	got, err := svc.Get(ctx, tenantID, p.ID)
	if err != nil || got.Status != "disabled" {
		t.Fatalf("status must be disabled after SetStatus; got %+v err=%v", got, err)
	}

	if err := svc.Delete(ctx, tenantID, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(ctx, tenantID, p.ID); err != ErrNotFound {
		t.Fatalf("soft-deleted proxy must read ErrNotFound, got %v", err)
	}
	list, err := svc.List(ctx, tenantID)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	for _, lp := range list {
		if lp.ID == p.ID {
			t.Fatalf("soft-deleted proxy must not appear in list")
		}
	}
}

func TestProxy_DeleteGuardCoversDirectDefaultAndGroupReferences(t *testing.T) {
	ctx := context.Background()
	pool := openProxyPool(t, ctx)
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	suffix := uuid.NewString()
	var tenantID, poolGroupID, providerID, channelID, directAccountID, groupAccountID int64
	if err := tx.QueryRow(ctx, `INSERT INTO tenants(name) VALUES($1) RETURNING id`, "proxy-delete-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO pool_groups(tenant_id,name) VALUES($1,$2) RETURNING id`,
		tenantID, "proxy-delete-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO providers(tenant_id,code,display_name,upstream_protocol) VALUES($1,$2,$3,'openai_chat') RETURNING id`,
		tenantID, "proxy-delete-"+suffix, "Proxy delete "+suffix).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO channels(tenant_id,pool_group_id,name) VALUES($1,$2,$3) RETURNING id`,
		tenantID, poolGroupID, "proxy-delete-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO provider_accounts(tenant_id,provider_id,channel_id,name,account_type) VALUES($1,$2,$3,$4,'api_key') RETURNING id`,
		tenantID, providerID, channelID, "direct-"+suffix).Scan(&directAccountID); err != nil {
		t.Fatalf("seed direct account: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO provider_accounts(tenant_id,provider_id,channel_id,name,account_type) VALUES($1,$2,$3,$4,'api_key') RETURNING id`,
		tenantID, providerID, channelID, "group-"+suffix).Scan(&groupAccountID); err != nil {
		t.Fatalf("seed group account: %v", err)
	}

	svc := New(admindb.New(tx), testKeys(t))
	create := func(name string, groupID *string) Proxy {
		t.Helper()
		proxy, createErr := svc.Create(ctx, CreateInput{
			TenantID: tenantID, Name: name + "-" + suffix, Protocol: "http",
			Host: "proxy.example.com", Port: 3128, GroupID: groupID,
		})
		if createErr != nil {
			t.Fatalf("create %s: %v", name, createErr)
		}
		return proxy
	}

	direct := create("direct", nil)
	if _, err := tx.Exec(ctx, `UPDATE provider_accounts SET proxy_id=$1 WHERE tenant_id=$2 AND id=$3`,
		direct.ID, tenantID, directAccountID); err != nil {
		t.Fatalf("bind direct proxy: %v", err)
	}
	directImpact, err := svc.DeleteImpact(ctx, tenantID, direct.ID)
	if err != nil || directImpact.DirectAccountCount != 1 || directImpact.CanDelete() {
		t.Fatalf("direct impact=%+v err=%v", directImpact, err)
	}
	if err := svc.Delete(ctx, tenantID, direct.ID); !errors.Is(err, ErrInUse) {
		t.Fatalf("delete direct-bound proxy=%v; want ErrInUse", err)
	}

	tenantDefault := create("default", nil)
	if _, err := tx.Exec(ctx, `UPDATE tenants SET default_proxy_id=$1 WHERE id=$2`, tenantDefault.ID, tenantID); err != nil {
		t.Fatalf("bind tenant default: %v", err)
	}
	defaultImpact, err := svc.DeleteImpact(ctx, tenantID, tenantDefault.ID)
	if err != nil || defaultImpact.DefaultTenantCount != 1 || defaultImpact.CanDelete() {
		t.Fatalf("default impact=%+v err=%v", defaultImpact, err)
	}
	if err := svc.Delete(ctx, tenantID, tenantDefault.ID); !errors.Is(err, ErrInUse) {
		t.Fatalf("delete default proxy=%v; want ErrInUse", err)
	}

	groupID := "group_" + strings.ReplaceAll(suffix[:8], "-", "_")
	groupA := create("group-a", &groupID)
	groupB := create("group-b", &groupID)
	if _, err := tx.Exec(ctx, `UPDATE provider_accounts SET proxy_group_id=$1 WHERE tenant_id=$2 AND id=$3`,
		groupID, tenantID, groupAccountID); err != nil {
		t.Fatalf("bind proxy group: %v", err)
	}
	firstImpact, err := svc.DeleteImpact(ctx, tenantID, groupA.ID)
	if err != nil || firstImpact.GroupAccountCount != 1 || firstImpact.GroupRemainingActiveCount != 1 || !firstImpact.CanDelete() {
		t.Fatalf("redundant group impact=%+v err=%v", firstImpact, err)
	}
	if err := svc.Delete(ctx, tenantID, groupA.ID); err != nil {
		t.Fatalf("delete redundant group member: %v", err)
	}
	lastImpact, err := svc.DeleteImpact(ctx, tenantID, groupB.ID)
	if err != nil || lastImpact.GroupAccountCount != 1 || lastImpact.GroupRemainingActiveCount != 0 || lastImpact.CanDelete() {
		t.Fatalf("last group impact=%+v err=%v", lastImpact, err)
	}
	if err := svc.Delete(ctx, tenantID, groupB.ID); !errors.Is(err, ErrInUse) {
		t.Fatalf("delete last used group member=%v; want ErrInUse", err)
	}
}

// TestProxy_GroupRoundTripAndClear 通过真实 PostgreSQL 钉住 create/get/list/update
// 的 group_id 列、参数与 Scan 顺序，并验证 nil 更新会把列清为 NULL。变异:删掉
// INSERT/SELECT/UPDATE/RETURNING 中任一 group_id，至少一个精确往返断言会转红。
func TestProxy_GroupRoundTripAndClear(t *testing.T) {
	ctx := context.Background()
	pool := openProxyPool(t, ctx)
	tenantID := seedProxyTenant(t, ctx, pool, "group")
	svc := New(admindb.New(pool), testKeys(t))

	firstGroup := "us-residential_1"
	created, err := svc.Create(ctx, CreateInput{
		TenantID: tenantID, Name: "group-proxy", Protocol: "http", Host: "proxy.example.com", Port: 3128,
		GroupID: &firstGroup,
	})
	if err != nil {
		t.Fatalf("create grouped proxy: %v", err)
	}
	if created.GroupID == nil || *created.GroupID != firstGroup {
		t.Fatalf("create group_id=%v want %q", created.GroupID, firstGroup)
	}

	got, err := svc.Get(ctx, tenantID, created.ID)
	if err != nil {
		t.Fatalf("get grouped proxy: %v", err)
	}
	if got.GroupID == nil || *got.GroupID != firstGroup {
		t.Fatalf("get group_id=%v want %q", got.GroupID, firstGroup)
	}

	listed, err := svc.List(ctx, tenantID)
	if err != nil {
		t.Fatalf("list grouped proxy: %v", err)
	}
	found := false
	for _, proxy := range listed {
		if proxy.ID != created.ID {
			continue
		}
		found = true
		if proxy.GroupID == nil || *proxy.GroupID != firstGroup {
			t.Fatalf("list group_id=%v want %q", proxy.GroupID, firstGroup)
		}
	}
	if !found {
		t.Fatalf("list did not return created proxy %d", created.ID)
	}

	secondGroup := "eu-egress"
	updated, err := svc.Update(ctx, UpdateInput{
		TenantID: tenantID, ID: created.ID, Name: created.Name, Protocol: created.Protocol,
		Host: created.Host, Port: created.Port, GroupID: &secondGroup,
	})
	if err != nil {
		t.Fatalf("update proxy group: %v", err)
	}
	if updated.GroupID == nil || *updated.GroupID != secondGroup {
		t.Fatalf("updated group_id=%v want %q", updated.GroupID, secondGroup)
	}

	cleared, err := svc.Update(ctx, UpdateInput{
		TenantID: tenantID, ID: created.ID, Name: created.Name, Protocol: created.Protocol,
		Host: created.Host, Port: created.Port, GroupID: nil,
	})
	if err != nil {
		t.Fatalf("clear proxy group: %v", err)
	}
	if cleared.GroupID != nil {
		t.Fatalf("clear response group_id=%v want nil", cleared.GroupID)
	}
	var storedGroup *string
	if err := pool.QueryRow(ctx,
		`SELECT group_id FROM proxies WHERE tenant_id=$1 AND id=$2`, tenantID, created.ID,
	).Scan(&storedGroup); err != nil {
		t.Fatalf("read cleared group_id: %v", err)
	}
	if storedGroup != nil {
		t.Fatalf("stored group_id=%v want SQL NULL", storedGroup)
	}
}

// TestProxy_AdminMutationsAndLogsCommitTogether 用真实触发器拒绝日志插入，证明新增、
// 字段修改、状态修改和删除都不会留下业务半状态。若任何一路退化成先提交业务再写
// 日志，对应精确数据库断言都会转红。
func TestProxy_AdminMutationsAndLogsCommitTogether(t *testing.T) {
	ctx := context.Background()
	pool := openProxyPool(t, ctx)
	tenantID := seedProxyTenant(t, ctx, pool, "atomic-log")
	svc := NewPostgres(pool, testKeys(t))
	goodAudit := MutationAudit{
		ActorID:   "admin_token:proxy-atomic-good",
		ActorRole: "platform_admin",
		RequestID: "proxy-atomic-good",
	}
	proxy, err := svc.CreateWithAudit(ctx, CreateInput{
		TenantID: tenantID,
		Name:     "atomic-before",
		Protocol: "http",
		Host:     "proxy.example.com",
		Port:     3128,
	}, goodAudit)
	if err != nil {
		t.Fatalf("create baseline proxy: %v", err)
	}
	var loggedActor, loggedAction, loggedTarget string
	var loggedTargetID int64
	if err := pool.QueryRow(ctx, `
SELECT actor_id, action, target_type, target_id
FROM admin_audit_events
WHERE tenant_id=$1 AND action='create_proxy' AND target_id=$2`,
		tenantID, proxy.ID,
	).Scan(&loggedActor, &loggedAction, &loggedTarget, &loggedTargetID); err != nil {
		t.Fatalf("read create log: %v", err)
	}
	if loggedActor != goodAudit.ActorID || loggedAction != "create_proxy" ||
		loggedTarget != "proxy" || loggedTargetID != proxy.ID {
		t.Fatalf("create log mismatch actor=%q action=%q target=%q id=%d",
			loggedActor, loggedAction, loggedTarget, loggedTargetID)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "reject_proxy_log_" + suffix
	triggerName := "reject_proxy_log_trigger_" + suffix
	ddl := fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'proxy log rejected for atomicity test';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER %s
BEFORE INSERT ON admin_audit_events
FOR EACH ROW EXECUTE FUNCTION %s()`, functionName, triggerName, functionName)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatalf("install reject trigger: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON admin_audit_events`, triggerName))
		_, _ = pool.Exec(c, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	})
	rejectedAudit := MutationAudit{
		ActorID:   "admin_token:proxy-atomic-rejected",
		ActorRole: "platform_admin",
		RequestID: "proxy-atomic-rejected",
	}

	if _, err := svc.CreateWithAudit(ctx, CreateInput{
		TenantID: tenantID,
		Name:     "must-not-exist",
		Protocol: "http",
		Host:     "proxy.example.com",
		Port:     3128,
	}, rejectedAudit); err == nil {
		t.Fatal("日志失败时新增代理必须失败")
	}
	var count int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM proxies WHERE tenant_id=$1 AND name='must-not-exist'`,
		tenantID,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("日志失败后新增代理残留 count=%d err=%v", count, err)
	}

	if _, err := svc.PatchWithAudit(ctx, PatchInput{
		TenantID: tenantID,
		ID:       proxy.ID,
		Name:     PatchField[string]{Set: true, Value: "must-not-stick"},
	}, rejectedAudit); err == nil {
		t.Fatal("日志失败时修改代理必须失败")
	}
	var name, status string
	var deletedAt any
	if err := pool.QueryRow(ctx,
		`SELECT name, status, deleted_at FROM proxies WHERE tenant_id=$1 AND id=$2`,
		tenantID, proxy.ID,
	).Scan(&name, &status, &deletedAt); err != nil {
		t.Fatalf("read proxy after rejected patch: %v", err)
	}
	if name != "atomic-before" || status != "active" || deletedAt != nil {
		t.Fatalf("修改日志失败留下半状态 name=%q status=%q deleted_at=%v", name, status, deletedAt)
	}

	if err := svc.SetStatusWithAudit(ctx, tenantID, proxy.ID, "disabled", rejectedAudit); err == nil {
		t.Fatal("日志失败时状态修改必须失败")
	}
	if err := pool.QueryRow(ctx,
		`SELECT status FROM proxies WHERE tenant_id=$1 AND id=$2`,
		tenantID, proxy.ID,
	).Scan(&status); err != nil || status != "active" {
		t.Fatalf("状态日志失败留下半状态 status=%q err=%v", status, err)
	}

	if err := svc.DeleteWithAudit(ctx, tenantID, proxy.ID, rejectedAudit); err == nil {
		t.Fatal("日志失败时删除必须失败")
	}
	if err := pool.QueryRow(ctx,
		`SELECT deleted_at FROM proxies WHERE tenant_id=$1 AND id=$2`,
		tenantID, proxy.ID,
	).Scan(&deletedAt); err != nil || deletedAt != nil {
		t.Fatalf("删除日志失败留下半状态 deleted_at=%v err=%v", deletedAt, err)
	}
}
