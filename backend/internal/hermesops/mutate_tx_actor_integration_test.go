//go:build integration_pg

// Hermes mutation actor 归属真 PG 守卫(刚修的 S1 区):
// 通过真正的 MutateOrchestrator.Execute → 真库,断言一次已确认 mutation 落下的
// 两列真实值:
//
//	(a) hermes_tool_calls.admin_actor_token_id —— token>0 写具体 FK id、
//	    token==0(非 admin 模式)写 SQL NULL(而非把 0 当成一个真实的 admin_tokens.id,
//	    那会撞 FK 或错误归因)。
//	(b) admin_audit_events.actor_id —— 走 admin.AdminIdentity.AuditActor() 统一格式,
//	    token>0 → "admin_token:<id>"、token==0 → "admin_token:0"(与其它 handler 同格式,
//	    不被分裂成裸 "0")。
//
// 这是 DB 承重路径:单测用 fake tx 只能证明「代码把 nil 传下去了」,但只有真库能证明
// FK 列真的落成 NULL、真的接受了 token>0 的 FK 引用、CHECK 白名单真的放行了
// hermes.tool.account_pause 动作 + provider_account target_type。
//
// 需要 HUAKAI_DATABASE_URL 指向一个已迁移到 >=0146 的 DB;未设置时干净跳过。
// 除自建/自删本用例的种子行外,不迁移也不改动共享 schema。
package hermesops

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	rootdb "github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openActorPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	p, err := rootdb.Open(ctx, rootdb.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// seedTenantAndAdminToken 建一个一次性 tenant + 一个真实的 tenant_operator admin_token
// (token>0 那腿需要真实 FK 引用),并注册 cleanup(先删审计/流水行,再删 token/tenant)。
func seedTenantAndAdminToken(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (tenantID, tokenID int64) {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "hermes-actor-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	// tenant_operator 需要 scope_tenant_id(见 scope_tenant_consistency CHECK)。
	if err := pool.QueryRow(ctx,
		`INSERT INTO admin_tokens (name, key_hash, key_prefix, role, scope_tenant_id)
		 VALUES ($1, $2, $3, 'tenant_operator', $4) RETURNING id`,
		"actor-tok-"+suffix, "hash-"+suffix, "prefix-"+suffix[:8], tenantID).Scan(&tokenID); err != nil {
		t.Fatalf("seed admin_token: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM hermes_tool_calls WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM admin_tokens WHERE id = $1`, tokenID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	return tenantID, tokenID
}

// TestOrchestrator_ActorColumns_RealPG 是 TestOrchestrator_ActorAttributionByTokenID 的
// 真库对应物:同样两条腿(token>0 / token==0),但断言的是数据库里 SELECT 回来的真实列值,
// 而非 fake tx 捕获的实参。它能抓住单测 fake 掩盖不了的真回归:
//   - admin_actor_token_id 若被误当成非 NULL 的 0,真库要么 FK 违例、要么落一个错误 id;
//   - actor_id 若绕过 AuditActor,真库里就是裸 "0"/"99" 而非 "admin_token:*"。
func TestOrchestrator_ActorColumns_RealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openActorPool(t, ctx)
	tenantID, tokenID := seedTenantAndAdminToken(t, ctx, pool)

	orch := NewMutateOrchestrator(pool)

	// runOne 用给定 AdminActorTokenID 跑一次真 Execute,mutate 回调只回一个 summary(不真的改
	// provider_accounts,本测试只验审计两列),返回该次落库的 correlation_id 以便精确 SELECT 回该行。
	runOne := func(t *testing.T, actorToken int64, corr string) {
		t.Helper()
		rec := MutationAuditRecord{
			TenantID:          tenantID,
			ActorUserID:       1,
			AdminActorTokenID: actorToken,
			ToolName:          ToolAccountPause,
			Status:            ResultOK,
			CorrelationID:     corr,
			AdminAction:       "hermes.tool.account_pause",
			AdminRole:         RoleTenantOperator,
			TargetType:        "provider_account",
			TargetID:          5,
			AuditPayload:      map[string]any{"account_id": int64(5)},
		}
		_, err := orch.Execute(ctx, "hermes:actor:"+corr, rec, func(context.Context, pgx.Tx) (ToolResult, error) {
			return ToolResult{Summary: map[string]any{"enabled": false}}, nil
		})
		if err != nil {
			t.Fatalf("Execute(token=%d) err=%v", actorToken, err)
		}
	}

	// --- 腿 1:token>0 → FK 列落具体 id、actor_id="admin_token:<id>" ---
	corrToken := "corr-token-" + strconv.FormatInt(tokenID, 10)
	runOne(t, tokenID, corrToken)

	var gotTokenID *int64
	if err := pool.QueryRow(ctx,
		`SELECT admin_actor_token_id FROM hermes_tool_calls WHERE tenant_id=$1 AND correlation_id=$2`,
		tenantID, corrToken).Scan(&gotTokenID); err != nil {
		t.Fatalf("select tool_calls(token>0): %v", err)
	}
	if gotTokenID == nil {
		t.Fatalf("admin_actor_token_id=NULL want %d — token>0 必须落具体 FK id", tokenID)
	}
	if *gotTokenID != tokenID {
		t.Fatalf("admin_actor_token_id=%d want %d", *gotTokenID, tokenID)
	}
	var gotActorID string
	if err := pool.QueryRow(ctx,
		`SELECT actor_id FROM admin_audit_events WHERE tenant_id=$1 AND request_id IS NULL AND action='hermes.tool.account_pause' AND actor_id=$2`,
		tenantID, "admin_token:"+strconv.FormatInt(tokenID, 10)).Scan(&gotActorID); err != nil {
		t.Fatalf("select admin_audit(token>0) actor_id: %v", err)
	}
	if gotActorID != "admin_token:"+strconv.FormatInt(tokenID, 10) {
		t.Fatalf("actor_id=%q want admin_token:%d", gotActorID, tokenID)
	}

	// --- 腿 2:token==0(非 admin 模式)→ FK 列落 NULL、actor_id="admin_token:0" ---
	corrZero := "corr-zero-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	runOne(t, 0, corrZero)

	var gotZeroTokenID *int64
	if err := pool.QueryRow(ctx,
		`SELECT admin_actor_token_id FROM hermes_tool_calls WHERE tenant_id=$1 AND correlation_id=$2`,
		tenantID, corrZero).Scan(&gotZeroTokenID); err != nil {
		t.Fatalf("select tool_calls(token=0): %v", err)
	}
	if gotZeroTokenID != nil {
		t.Fatalf("admin_actor_token_id=%d want NULL — token=0 绝不能把 0 当真实 FK id 落库", *gotZeroTokenID)
	}
	var gotZeroActorID string
	if err := pool.QueryRow(ctx,
		`SELECT actor_id FROM admin_audit_events WHERE tenant_id=$1 AND action='hermes.tool.account_pause' AND actor_id='admin_token:0'`,
		tenantID).Scan(&gotZeroActorID); err != nil {
		t.Fatalf("select admin_audit(token=0) actor_id: %v", err)
	}
	if gotZeroActorID != "admin_token:0" {
		t.Fatalf("actor_id=%q want admin_token:0(须走 AuditActor 统一格式,不得为裸 \"0\")", gotZeroActorID)
	}
}
