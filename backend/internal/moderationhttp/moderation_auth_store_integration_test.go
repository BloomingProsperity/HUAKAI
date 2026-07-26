//go:build integration_pg

package moderationhttp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
)

func TestModerationAdmin_RealAuthHTTPAndPostgresEnforceRoleAndTenant(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接 PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	fixture := seedModerationHTTPFixture(t, ctx, pool)
	store := moderation.NewSQLStoreWithPool(pool)
	banModerationHTTPKey(t, ctx, store, fixture.tenantB, fixture.userB, fixture.keyB, "vertical-b")

	router := chi.NewRouter()
	router.Route("/admin/v1/moderation", func(r chi.Router) {
		MountModerationAdminRoutes(r, ModerationAdminDeps{
			Auth: admin.NewAdminResolver(admindb.New(pool)), Store: store,
		})
	})

	configResponse := invokeModerationWithBearer(
		router, fixture.platformBearer, http.MethodPut, "/admin/v1/moderation/config",
		fmt.Sprintf(`{"tenant_id":%d,"enabled":true,"fail_closed":true,"sample_rate_pct":100,"ban_threshold":2,"ban_window_seconds":600,"auto_disable_key_on_ban":false}`, fixture.tenantB),
	)
	if configResponse.Code != http.StatusOK {
		t.Fatalf("部署者代租户配置规则 status=%d body=%s",
			configResponse.Code, configResponse.Body.String())
	}
	var configuredTenant int64
	if err := pool.QueryRow(ctx,
		`SELECT tenant_id FROM moderation_config WHERE tenant_id=$1 AND enabled=true AND auto_disable_key_on_ban=false`,
		fixture.tenantB,
	).Scan(&configuredTenant); err != nil || configuredTenant != fixture.tenantB {
		t.Fatalf("部署者配置未落到目标租户: tenant=%d err=%v", configuredTenant, err)
	}

	ruleResponse := invokeModerationWithBearer(
		router, fixture.platformBearer, http.MethodPost, "/admin/v1/moderation/keywords",
		fmt.Sprintf(`{"tenant_id":%d,"keyword":"违规","reason_code":"vertical_keyword","enabled":true}`, fixture.tenantA),
	)
	if ruleResponse.Code != http.StatusCreated {
		t.Fatalf("部署者代租户添加规则 status=%d body=%s",
			ruleResponse.Code, ruleResponse.Body.String())
	}
	tenantRuleAttempt := invokeModerationWithBearer(
		router, fixture.operatorBearer, http.MethodPost, "/admin/v1/moderation/keywords",
		fmt.Sprintf(`{"tenant_id":%d,"keyword":"租户无权添加","enabled":true}`, fixture.tenantA),
	)
	if tenantRuleAttempt.Code != http.StatusForbidden {
		t.Fatalf("租户自行写规则 status=%d want 403 body=%s",
			tenantRuleAttempt.Code, tenantRuleAttempt.Body.String())
	}
	configAResponse := invokeModerationWithBearer(
		router, fixture.platformBearer, http.MethodPut, "/admin/v1/moderation/config",
		fmt.Sprintf(`{"tenant_id":%d,"enabled":true,"fail_closed":true,"sample_rate_pct":100,"ban_threshold":1,"ban_window_seconds":600,"auto_disable_key_on_ban":true}`, fixture.tenantA),
	)
	if configAResponse.Code != http.StatusOK {
		t.Fatalf("部署者配置租户 A status=%d body=%s",
			configAResponse.Code, configAResponse.Body.String())
	}
	screener := moderation.NewScreener(moderation.ScreenerDeps{
		Config: store, Keywords: store, Hashes: store, Ban: moderation.NewBanCounter(store),
	})
	screenResult, err := screener.Screen(ctx, moderation.ScreenRequest{
		TenantID: fixture.tenantA, UserID: fixture.userA, APIKeyID: fixture.keyA,
		RequestID: "vertical-a", ClientProtocol: "openai_chat",
		Body: []byte(`{"messages":[{"role":"user","content":"违规 Authorization: Bearer secret-value user@example.com"}]}`),
	})
	if err != nil || screenResult.Decision != moderation.DecisionBlockKeyword {
		t.Fatalf("真实审核链 result=%+v err=%v", screenResult, err)
	}
	tenantViolations := invokeModerationWithBearer(
		router, fixture.operatorBearer, http.MethodGet,
		fmt.Sprintf("/admin/v1/moderation/violations?tenant_id=%d", fixture.tenantA), "",
	)
	if tenantViolations.Code != http.StatusOK {
		t.Fatalf("租户读取自身违规 status=%d body=%s",
			tenantViolations.Code, tenantViolations.Body.String())
	}
	violationBody := tenantViolations.Body.String()
	if !strings.Contains(violationBody, "违规") ||
		strings.Contains(violationBody, "secret-value") ||
		strings.Contains(violationBody, "user@example.com") {
		t.Fatalf("租户违规摘录脱敏不符合合同: %s", violationBody)
	}
	platformLogs := invokeModerationWithBearer(
		router, fixture.platformBearer, http.MethodGet,
		fmt.Sprintf("/admin/v1/moderation/logs?tenant_id=%d", fixture.tenantA), "",
	)
	if platformLogs.Code != http.StatusOK ||
		!strings.Contains(platformLogs.Body.String(), "vertical_keyword") {
		t.Fatalf("部署者完整日志 status=%d body=%s",
			platformLogs.Code, platformLogs.Body.String())
	}
	tenantLogs := invokeModerationWithBearer(
		router, fixture.operatorBearer, http.MethodGet,
		fmt.Sprintf("/admin/v1/moderation/logs?tenant_id=%d", fixture.tenantA), "",
	)
	if tenantLogs.Code != http.StatusForbidden {
		t.Fatalf("租户读取完整日志 status=%d want 403 body=%s",
			tenantLogs.Code, tenantLogs.Body.String())
	}

	crossTenant := invokeModerationWithBearer(
		router, fixture.operatorBearer, http.MethodPost,
		fmt.Sprintf("/admin/v1/moderation/api-keys/%d/unban", fixture.keyB),
		fmt.Sprintf(`{"tenant_id":%d,"idempotency_key":"vertical-cross-unban"}`, fixture.tenantB),
	)
	if crossTenant.Code != http.StatusForbidden {
		t.Fatalf("租户跨域解封 status=%d want 403 body=%s",
			crossTenant.Code, crossTenant.Body.String())
	}
	if status := moderationHTTPKeyStatus(t, ctx, pool, fixture.tenantB, fixture.keyB); status != "disabled" {
		t.Fatalf("跨租户请求改变了 Key B 状态: %s", status)
	}

	ownTenant := invokeModerationWithBearer(
		router, fixture.operatorBearer, http.MethodPost,
		fmt.Sprintf("/admin/v1/moderation/api-keys/%d/unban", fixture.keyA),
		fmt.Sprintf(`{"tenant_id":%d,"idempotency_key":"vertical-own-unban","reason":"租户复核通过"}`, fixture.tenantA),
	)
	if ownTenant.Code != http.StatusOK {
		t.Fatalf("租户解封自身 Key status=%d body=%s", ownTenant.Code, ownTenant.Body.String())
	}
	if status := moderationHTTPKeyStatus(t, ctx, pool, fixture.tenantA, fixture.keyA); status != "active" {
		t.Fatalf("自身 Key 状态=%s want active", status)
	}
	var operationCount int64
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM moderation_key_operations
WHERE tenant_id=$1 AND api_key_id=$2
  AND actor_role='tenant_operator'
  AND idempotency_key='vertical-own-unban'`,
		fixture.tenantA, fixture.keyA,
	).Scan(&operationCount); err != nil || operationCount != 1 {
		t.Fatalf("租户解封永久幂等事实=%d err=%v", operationCount, err)
	}
}

type moderationHTTPFixture struct {
	tenantA, tenantB int64
	userA, userB     int64
	keyA, keyB       int64
	platformTokenID  int64
	operatorTokenID  int64
	platformBearer   string
	operatorBearer   string
}

func seedModerationHTTPFixture(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) moderationHTTPFixture {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var f moderationHTTPFixture
	insertTenantAndKey := func(label string) (int64, int64, int64) {
		t.Helper()
		var tenantID, userID, keyID int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
			"moderation-http-"+label+"-"+suffix,
		).Scan(&tenantID); err != nil {
			t.Fatalf("插入租户 %s: %v", label, err)
		}
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
			tenantID, "moderation-user-"+label+"-"+suffix,
		).Scan(&userID); err != nil {
			t.Fatalf("插入用户 %s: %v", label, err)
		}
		if err := pool.QueryRow(ctx, `
INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
			tenantID, userID, "moderation-key-"+label+"-"+suffix,
			"$2a$10$moderation-http", "hk_mod_"+label+"_"+suffix,
		).Scan(&keyID); err != nil {
			t.Fatalf("插入 Key %s: %v", label, err)
		}
		return tenantID, userID, keyID
	}
	f.tenantA, f.userA, f.keyA = insertTenantAndKey("a")
	f.tenantB, f.userB, f.keyB = insertTenantAndKey("b")
	f.platformBearer, f.platformTokenID = seedModerationAdminBearer(
		t, ctx, pool, suffix+"-platform", admin.RolePlatformAdmin, nil,
	)
	f.operatorBearer, f.operatorTokenID = seedModerationAdminBearer(
		t, ctx, pool, suffix+"-operator", admin.RoleTenantOperator, &f.tenantA,
	)
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		for _, tenantID := range []int64{f.tenantA, f.tenantB} {
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM moderation_key_operations WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM moderation_key_states WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM moderation_log WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM moderation_violation_events WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM moderation_keywords WHERE tenant_id=$1`, tenantID)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM moderation_config WHERE tenant_id=$1`, tenantID)
		}
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_tokens WHERE id = ANY($1::bigint[])`,
			[]int64{f.platformTokenID, f.operatorTokenID})
		for _, keyID := range []int64{f.keyA, f.keyB} {
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_keys WHERE id=$1`, keyID)
		}
		for _, userID := range []int64{f.userA, f.userB} {
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID)
		}
		for _, tenantID := range []int64{f.tenantA, f.tenantB} {
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, tenantID)
		}
	})
	return f
}

func seedModerationAdminBearer(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	name string,
	role string,
	scopeTenantID *int64,
) (string, int64) {
	t.Helper()
	bearer, prefix, err := admin.GenerateBearer(admin.EnvAdmin)
	if err != nil {
		t.Fatalf("生成管理员令牌: %v", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(bearer), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("哈希管理员令牌: %v", err)
	}
	var id int64
	if err := pool.QueryRow(ctx, `
INSERT INTO admin_tokens (name, key_hash, key_prefix, role, scope_tenant_id, status)
VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		name, string(hash), prefix, role, scopeTenantID,
	).Scan(&id); err != nil {
		t.Fatalf("插入管理员令牌: %v", err)
	}
	return bearer, id
}

func banModerationHTTPKey(
	t *testing.T,
	ctx context.Context,
	store *moderation.SQLStore,
	tenantID int64,
	userID int64,
	apiKeyID int64,
	requestID string,
) {
	t.Helper()
	result, err := store.RecordModerationViolation(ctx, moderation.ModerationEvent{
		TenantID: tenantID, UserID: userID, APIKeyID: apiKeyID,
		RequestID: requestID, InputExcerpt: "违规测试摘录",
		Decision: moderation.DecisionBlockKeyword, ReasonCode: "vertical_fixture",
	}, moderation.ModerationConfig{
		TenantID: tenantID, BanThreshold: 1, BanWindowSeconds: 3600,
		AutoDisableKeyOnBan: true,
	})
	if err != nil || !result.Disabled {
		t.Fatalf("禁用测试 Key: result=%+v err=%v", result, err)
	}
}

func invokeModerationWithBearer(
	handler http.Handler,
	bearer string,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func moderationHTTPKeyStatus(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID int64,
	apiKeyID int64,
) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM api_keys WHERE tenant_id=$1 AND id=$2`,
		tenantID, apiKeyID,
	).Scan(&status); err != nil {
		t.Fatalf("读取 Key 状态: %v", err)
	}
	return status
}
