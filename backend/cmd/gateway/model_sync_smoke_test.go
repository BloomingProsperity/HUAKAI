//go:build smoke

// 账号级模型同步全链冒烟:从正式管理员鉴权开始,经真实路由、DI、凭据解密、
// 统一 Dispatcher、上游模型响应、落 model_allow_list,到后续选号真被命中。
// 判别夹具:公开别名(gpt-4.1-mini)≠ 上游模型 ID(dev-mock-model),证明
// 同步落库值与选号用的 ProviderModelID 完全一致,而不是拿别名过白名单。
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestSmoke_AccountModelSyncFullChain(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping smoke test")
	}
	dsn = useDisposableSpecializedLiveDatabase(t, dsn)
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer setupCancel()

	pgPool, err := db.Open(setupCtx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开冒烟数据库: %v", err)
	}
	t.Cleanup(pgPool.Close)

	seed := seedSmokeGraph(t, setupCtx, pgPool)
	// LIFO:先删同步写下的审计行,再让 seedSmokeGraph 的租户清理走 FK。
	t.Cleanup(func() {
		_, _ = pgPool.Exec(context.Background(), `DELETE FROM admin_audit_events WHERE tenant_id=$1`, seed.tenantID)
	})
	applyModelSyncFixture(t, setupCtx, pgPool, seed)
	adminBearer := seedModelSyncAdminToken(t, setupCtx, pgPool, seed.tenantID)
	foreignBearer := seedModelSyncForeignTenant(t, setupCtx, pgPool)

	binPath := buildGateway(t)
	defer os.Remove(binPath)
	addr := reserveLocalPort(t)
	cmd := startGateway(t, setupCtx, binPath, dsn, addr, seed)
	t.Cleanup(func() { stopGateway(cmd) })
	waitForGateway(t, addr)
	setupCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 30 * time.Second}

	// 一、同步前:白名单只有陈旧模型,选号必须把该账号排除出池。
	if status, _ := postModelSyncChat(t, ctx, client, addr, seed); status == http.StatusOK {
		t.Fatal("同步前白名单只含陈旧模型,选号不应命中该账号")
	}
	assertModelSyncSlotCount(t, ctx, pgPool, seed.providerAccountID, 0)

	// 二、跨租户隔离:别的租户管理员操作该账号必须 404,且白名单不被动过。
	status, body := postModelSyncAdmin(t, ctx, client, addr, foreignBearer, seed.providerAccountID)
	if status != http.StatusNotFound {
		t.Fatalf("跨租户同步 status=%d body=%s,期望 404", status, body)
	}
	assertModelSyncAllowList(t, ctx, pgPool, seed.providerAccountID, []string{"stale-model-before-sync"})

	// 三、正式同步:管理入口 -> 凭据解密 -> Dispatcher -> mock 上游目录 -> 落库。
	status, body = postModelSyncAdmin(t, ctx, client, addr, adminBearer, seed.providerAccountID)
	if status != http.StatusOK {
		t.Fatalf("模型同步 status=%d body=%s", status, body)
	}
	var syncResponse struct {
		Models  []string `json:"models"`
		Changed bool     `json:"changed"`
		Vendor  string   `json:"vendor"`
	}
	if err := json.Unmarshal([]byte(body), &syncResponse); err != nil {
		t.Fatalf("解析同步响应: %v body=%s", err, body)
	}
	wantModels := []string{"dev-mock-model", "dev-mock-model-secondary"}
	if !syncResponse.Changed || strings.Join(syncResponse.Models, ",") != strings.Join(wantModels, ",") {
		t.Fatalf("同步响应=%+v,期望 changed 且模型为上游目录", syncResponse)
	}
	assertModelSyncAllowList(t, ctx, pgPool, seed.providerAccountID, wantModels)
	assertModelSyncAuditTrail(t, ctx, pgPool, seed.tenantID, seed.providerAccountID)

	// 四、同步后选号命中:同一别名请求现在必须路由到该账号并完成计费闭环。
	status, chatBody := postModelSyncChat(t, ctx, client, addr, seed)
	if status != http.StatusOK {
		t.Fatalf("同步后 chat status=%d body=%s", status, chatBody)
	}
	assertModelSyncSlotCount(t, ctx, pgPool, seed.providerAccountID, 1)
}

// applyModelSyncFixture 把注册表出站模型改成与公开别名不同的上游 ID,并把账号
// 白名单预置为陈旧值;同时给价格快照补上上游 ID 的价目,保证同步后计费可结算。
func applyModelSyncFixture(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *smokeSeed) {
	t.Helper()
	if _, err := pgPool.Exec(ctx,
		`UPDATE models SET default_provider_model_id='dev-mock-model' WHERE id=$1`, seed.modelID); err != nil {
		t.Fatalf("改写注册表出站模型: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`UPDATE provider_accounts SET model_allow_list=ARRAY['stale-model-before-sync'] WHERE id=$1`,
		seed.providerAccountID); err != nil {
		t.Fatalf("预置陈旧白名单: %v", err)
	}
	price := `{"input_micro_usd":"1","output_micro_usd":"2","cache_read_micro_usd":"1"}`
	if _, err := pgPool.Exec(ctx, `
UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(pricing_data, '{providers,openai,models,dev-mock-model}', $2::jsonb, true)
WHERE tenant_id=0 AND version=$1`, seed.pricingVersion, price); err != nil {
		t.Fatalf("给上游模型补价目: %v", err)
	}
}

func seedModelSyncAdminToken(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, tenantID int64) string {
	t.Helper()
	bearer := "hk_admin_smk_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	prefix := bearer[:16]
	hash, err := bcrypt.GenerateFromPassword([]byte(bearer), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("生成管理令牌哈希: %v", err)
	}
	var tokenID int64
	if err := pgPool.QueryRow(ctx, `
INSERT INTO admin_tokens (name, key_hash, key_prefix, role, scope_tenant_id, status)
VALUES ($1,$2,$3,'tenant_operator',$4,'active') RETURNING id`,
		"model-sync-smoke-"+prefix, string(hash), prefix, tenantID).Scan(&tokenID); err != nil {
		t.Fatalf("写入租户管理员令牌: %v", err)
	}
	// 先于 seedSmokeGraph 的租户清理执行,否则外键阻塞租户删除。
	t.Cleanup(func() {
		_, _ = pgPool.Exec(context.Background(), `DELETE FROM admin_tokens WHERE id=$1`, tokenID)
	})
	return bearer
}

// seedModelSyncForeignTenant 建一个无关租户及其管理员,用来证明账号级模型
// 同步的租户隔离(对别人的账号只能拿到 404)。
func seedModelSyncForeignTenant(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool) string {
	t.Helper()
	var foreignTenantID int64
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"model-sync-foreign-"+uuid.NewString(),
	).Scan(&foreignTenantID); err != nil {
		t.Fatalf("建无关租户: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pgPool.Exec(c, `DELETE FROM admin_tokens WHERE scope_tenant_id=$1`, foreignTenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM tenants WHERE id=$1`, foreignTenantID)
	})
	return seedModelSyncAdminToken(t, ctx, pgPool, foreignTenantID)
}

func postModelSyncAdmin(t *testing.T, ctx context.Context, client *http.Client, addr, bearer string, accountID int64) (int, string) {
	t.Helper()
	url := fmt.Sprintf("http://%s/admin/v1/provider-accounts/%d/upstream-models/sync", addr, accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
		strings.NewReader(`{"reason":"冒烟全链模型同步"}`))
	if err != nil {
		t.Fatalf("构造同步请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		t.Fatalf("发送同步请求: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatalf("读取同步响应: %v", err)
	}
	return response.StatusCode, string(body)
}

func postModelSyncChat(t *testing.T, ctx context.Context, client *http.Client, addr string, seed *smokeSeed) (int, string) {
	t.Helper()
	body := `{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("构造 chat 请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+seed.bearer)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		t.Fatalf("发送 chat 请求: %v", err)
	}
	defer response.Body.Close()
	var collected strings.Builder
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 1<<20))
	for scanner.Scan() {
		collected.WriteString(scanner.Text())
		collected.WriteString("\n")
	}
	return response.StatusCode, collected.String()
}

func assertModelSyncAllowList(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, accountID int64, want []string) {
	t.Helper()
	var got []string
	if err := pgPool.QueryRow(ctx,
		`SELECT model_allow_list FROM provider_accounts WHERE id=$1`, accountID).Scan(&got); err != nil {
		t.Fatalf("读取账号白名单: %v", err)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("账号白名单=%v,期望 %v", got, want)
	}
}

func assertModelSyncAuditTrail(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, tenantID, accountID int64) {
	t.Helper()
	var count int
	if err := pgPool.QueryRow(ctx, `
SELECT count(*) FROM admin_audit_events
WHERE tenant_id=$1 AND target_type='provider_account' AND target_id=$2
  AND payload->>'operation'='sync_account_models' AND payload->>'result'='updated'`,
		tenantID, accountID).Scan(&count); err != nil {
		t.Fatalf("查询同步审计轨迹: %v", err)
	}
	if count != 1 {
		t.Fatalf("同步审计轨迹条数=%d,期望 1", count)
	}
}

func assertModelSyncSlotCount(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, accountID int64, want int) {
	t.Helper()
	var count int
	if err := pgPool.QueryRow(ctx, `
SELECT count(*) FROM pool_slot_acquisitions
WHERE provider_account_id=$1 AND status='released_success'`, accountID).Scan(&count); err != nil {
		t.Fatalf("查询账号槽记录: %v", err)
	}
	if count != want {
		t.Fatalf("账号成功槽数=%d,期望 %d(选号命中证据)", count, want)
	}
}
