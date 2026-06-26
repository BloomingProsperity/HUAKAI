//go:build smoke

// 前端接线测试:启动真实网关(dev-mock 上游),并精确地驱动
// 用户门户前端所调用的那些端点 + 请求形状,断言前端解析的
// 那些精确响应字段。每个前端模块一个子测试
//(login / api-keys / usage / playground)。如果后端把前端
// 依赖的某个字段改名(例如 api-key 创建的 `plaintext`、login 的 `session`),
// 对应的子测试就会变红——也就是说,它会因其守护的真实接线缺陷而失败。
//
// 复用 smoke_test.go 里的 smoke 测试脚手架
//(buildGateway/startGateway/seedSmokeGraph/...)。运行:go test -tags smoke -run TestFrontendWiring ./cmd/gateway

package main

import (
	"bytes"
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
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestFrontendWiring(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping frontend wiring test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open dev pool: %v", err)
	}
	defer pgPool.Close()

	seed := seedSmokeGraph(t, ctx, pgPool)

	// session/verification 这几张表不在 seedSmokeGraph 的租户清理范围内。
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pgPool.Exec(c, `DELETE FROM session_tokens WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM session_families WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM email_verification_tokens WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM admin_tokens WHERE name LIKE 'wire-admin-%'`)
	})

	// 播种一个限定在 seed 租户内的 tenant_operator admin token,
	// 这样管理控制台端点(走 admin-token 轨道而非 session)无需 ?tenant_id 即可做接线测试。
	adminBearer := "hk_admin_" + uuid.NewString()[:12]
	adminPrefix := adminBearer
	if len(adminPrefix) > 16 {
		adminPrefix = adminPrefix[:16]
	}
	adminHash, err := bcrypt.GenerateFromPassword([]byte(adminBearer), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash admin token: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO admin_tokens (name, key_hash, key_prefix, role, scope_tenant_id, status)
		 VALUES ($1,$2,$3,'tenant_operator',$4,'active')`,
		"wire-admin-"+uuid.NewString()[:8], string(adminHash), adminPrefix, seed.tenantID); err != nil {
		t.Fatalf("seed admin_token: %v", err)
	}

	// 某些管理面(全局 channel-health / ops usage)要求 platform_admin
	//(scope_tenant_id 为 NULL)。也为这些子测试播种一个。
	adminPlatformBearer := "hk_admin_" + uuid.NewString()[:12]
	adminPlatPrefix := adminPlatformBearer
	if len(adminPlatPrefix) > 16 {
		adminPlatPrefix = adminPlatPrefix[:16]
	}
	platHash, err := bcrypt.GenerateFromPassword([]byte(adminPlatformBearer), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash platform admin token: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO admin_tokens (name, key_hash, key_prefix, role, status)
		 VALUES ($1,$2,$3,'platform_admin','active')`,
		"wire-admin-plat-"+uuid.NewString()[:8], string(platHash), adminPlatPrefix); err != nil {
		t.Fatalf("seed platform admin_token: %v", err)
	}

	// 全局 platform-setting 的默认值会拦住这个密码认证测试:
	// registration_enabled 默认 false、invitation_required 为 true、
	// two_factor_enabled 为 true,而本次网关组装并未接线 2FA 服务
	//(authTwoFactorRequired → 503)。配置成对密码登录友好的
	// 值(global 作用域;在 dev DB 上无害)。
	for k, v := range map[string]string{
		"registration_enabled": "true",
		"invitation_required":  "false",
		"two_factor_enabled":   "false",
		"captcha_enabled":      "false",
	} {
		if _, err := pgPool.Exec(ctx,
			`INSERT INTO platform_settings (scope, setting_key, setting_value, updated_by, updated_at)
			 VALUES ('global',$1,$2,'frontend-wiring-test', now())
			 ON CONFLICT (scope, setting_key) DO UPDATE SET setting_value=$2, updated_by='frontend-wiring-test', updated_at=now()`, k, v); err != nil {
			t.Fatalf("seed platform_setting %s: %v", k, err)
		}
	}

	binPath := buildGateway(t)
	defer os.Remove(binPath)

	addr := reserveLocalPort(t)
	cmd := startGateway(t, ctx, binPath, dsn, addr, seed)
	t.Cleanup(func() { stopGateway(cmd) })
	waitForGateway(t, addr)
	base := "http://" + addr

	unique := uuid.NewString()[:8]
	email := "wire-" + unique + "@example.com"
	password := "Huakai-Wire-Test-123!"

	var sessionToken string // 由 login 子测试捕获,供下游消费
	var createdKey string   // 来自 api-keys 子测试的 hk_ 明文
	var createdKeyID int64  // 它的 api_key_id(用于 usage-summary 接线)

	// ---- 模块:login 页(register → login → me)----
	t.Run("login_page_auth_ring", func(t *testing.T) {
		// register(public)。前端 POST {tenant_id,email,display_name,password}。
		st, body, _ := doJSON(t, ctx, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
			"tenant_id": seed.tenantID, "email": email, "display_name": "Wire User", "password": password,
		})
		if st != http.StatusOK && st != http.StatusCreated {
			t.Fatalf("register expected 200/201; got %d body=%s", st, body)
		}
		// 让该账号无论租户的邮箱验证策略如何都能登录
		//(我们测的是认证「接线」契约,而非验证闸门)。
		if _, err := pgPool.Exec(ctx,
			`UPDATE users SET email_verified=true, status='active' WHERE tenant_id=$1 AND email=$2`,
			seed.tenantID, email); err != nil {
			t.Fatalf("flip email_verified: %v", err)
		}

		// login(public)。前端解析 resp.session.session_token + resp.user。
		st, body, obj := doJSON(t, ctx, http.MethodPost, base+"/v1/auth/login", "", map[string]any{
			"tenant_id": seed.tenantID, "email": email, "password": password,
		})
		if st != http.StatusOK {
			t.Fatalf("login expected 200; got %d body=%s", st, body)
		}
		session, ok := obj["session"].(map[string]any)
		if !ok {
			t.Fatalf("login: response missing `session` object (frontend auth.ts reads resp.session.session_token); body=%s", body)
		}
		tok, _ := session["session_token"].(string)
		if tok == "" {
			t.Fatalf("login: session.session_token empty; body=%s", body)
		}
		if _, ok := session["refresh_token"].(string); !ok {
			t.Fatalf("login: session.refresh_token missing (userClient refresh ring depends on it); body=%s", body)
		}
		sessionToken = tok

		// 带 session token 调 GET /v1/auth/me(Header.tsx + fetchMe())。
		// 真实契约:{ panel, user_id, tenant_id, display_name }——注意是 user_id
		//(而非 id)且「没有」email。fetchMe() 会把 user_id→id 并保留登录时的 email。
		st, body, me := doJSON(t, ctx, http.MethodGet, base+"/v1/auth/me", sessionToken, nil)
		if st != http.StatusOK {
			t.Fatalf("/v1/auth/me expected 200; got %d body=%s", st, body)
		}
		if got, _ := me["display_name"].(string); got != "Wire User" {
			t.Fatalf("/v1/auth/me display_name = %q; want %q (body=%s)", got, "Wire User", body)
		}
		if _, ok := me["user_id"]; !ok {
			t.Fatalf("/v1/auth/me missing `user_id` (fetchMe maps user_id→SessionUser.id); body=%s", body)
		}
	})

	if sessionToken == "" {
		t.Fatal("login subtest did not yield a session token; downstream session modules cannot run")
	}

	// ---- 模块:API Keys 页(create → list)----
	t.Run("api_keys_page", func(t *testing.T) {
		// create。前端 apiKeys.ts 读取一次性的 `plaintext` 字段。
		st, body, obj := doJSON(t, ctx, http.MethodPost, base+"/v1/api-keys", sessionToken, map[string]any{
			"name": "wire-key-" + unique, "environment": "test",
		})
		if st != http.StatusOK && st != http.StatusCreated {
			t.Fatalf("create api-key expected 200/201; got %d body=%s", st, body)
		}
		pt, _ := obj["plaintext"].(string)
		if pt == "" {
			t.Fatalf("create api-key: `plaintext` empty — the one-time-secret modal would show nothing; body=%s", body)
		}
		if !strings.HasPrefix(pt, "hk_") {
			t.Fatalf("create api-key: plaintext %q lacks hk_ prefix", pt)
		}
		createdKey = pt
		if idf, ok := obj["api_key_id"].(float64); ok {
			createdKeyID = int64(idf)
		}

		// list。前端读取 { api_keys: [...], count }。
		st, body, list := doJSON(t, ctx, http.MethodGet, base+"/v1/api-keys", sessionToken, nil)
		if st != http.StatusOK {
			t.Fatalf("list api-keys expected 200; got %d body=%s", st, body)
		}
		arr, ok := list["api_keys"].([]any)
		if !ok {
			t.Fatalf("list api-keys: missing `api_keys` array (frontend maps over it); body=%s", body)
		}
		if len(arr) == 0 {
			t.Fatalf("list api-keys: expected the just-created key; got empty array")
		}
		// 刚创建的 key 必须能按前缀匹配到
		found := false
		for _, it := range arr {
			row, _ := it.(map[string]any)
			if kp, _ := row["key_prefix"].(string); kp != "" && strings.HasPrefix(createdKey, kp) {
				found = true
			}
		}
		if !found {
			t.Fatalf("list api-keys: created key not found in list; body=%s", body)
		}

		// 单 key 的用量汇总——api-keys 页的行展开面板会调用
		// GET /v1/me/keys/{id}/usage-summary(SESSION 认证)。读取 api_key_id/total_cost/request_count。
		if createdKeyID != 0 {
			st, body, sum := doJSON(t, ctx, http.MethodGet,
				fmt.Sprintf("%s/v1/me/keys/%d/usage-summary", base, createdKeyID), sessionToken, nil)
			if st != http.StatusOK {
				t.Fatalf("usage-summary expected 200; got %d body=%s", st, body)
			}
			for _, f := range []string{"api_key_id", "total_cost", "request_count"} {
				if _, ok := sum[f]; !ok {
					t.Fatalf("usage-summary missing %q (api-keys expand panel reads it); body=%s", f, body)
				}
			}
		}
	})

	// ---- 模块:usage 页(quota[session] + usage[apikey] + time-series[apikey])----
	t.Run("usage_page", func(t *testing.T) {
		// /v1/me/quota —— SESSION 认证。前端读取 { items: [...] }。
		st, body, q := doJSON(t, ctx, http.MethodGet, base+"/v1/me/quota", sessionToken, nil)
		if st != http.StatusOK {
			t.Fatalf("/v1/me/quota expected 200; got %d body=%s", st, body)
		}
		if _, ok := q["items"]; !ok {
			t.Fatalf("/v1/me/quota: missing `items` (QuotaWindowsCard maps over items); body=%s", body)
		}

		// /v1/me/usage —— API-KEY 认证(seed bearer)。前端读取 { items, next_cursor }。
		st, body, u := doJSON(t, ctx, http.MethodGet, base+"/v1/me/usage?limit=20", seed.bearer, nil)
		if st != http.StatusOK {
			t.Fatalf("/v1/me/usage expected 200; got %d body=%s", st, body)
		}
		if _, ok := u["items"]; !ok {
			t.Fatalf("/v1/me/usage: missing `items`; body=%s", body)
		}
		if _, ok := u["next_cursor"]; !ok {
			t.Fatalf("/v1/me/usage: missing `next_cursor` (cursor pagination depends on it); body=%s", body)
		}

		// /v1/me/analytics/time-series —— API-KEY 认证,from/to 必填(<=31d)。读取 { items, period }。
		now := time.Now().UTC()
		from := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)
		to := now.Format(time.RFC3339)
		st, body, ts := doJSON(t, ctx, http.MethodGet,
			base+"/v1/me/analytics/time-series?granularity=day&from="+from+"&to="+to, seed.bearer, nil)
		if st != http.StatusOK {
			t.Fatalf("/v1/me/analytics/time-series expected 200; got %d body=%s", st, body)
		}
		if _, ok := ts["items"]; !ok {
			t.Fatalf("time-series: missing `items` (aggregateTimeSeries reads items); body=%s", body)
		}
		if _, ok := ts["period"]; !ok {
			t.Fatalf("time-series: missing `period`; body=%s", body)
		}

		// CSV 导出 —— usage 页的「导出」按钮请求 /v1/me/usage/export.csv
		//(SESSION 认证,账号维度——而非 api-key 路径)。断言 200。
		exReq, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			base+"/v1/me/usage/export.csv?format=csv&from="+from+"&to="+to, nil)
		exReq.Header.Set("Authorization", "Bearer "+sessionToken)
		exResp, err := http.DefaultClient.Do(exReq)
		if err != nil {
			t.Fatalf("export.csv: %v", err)
		}
		defer exResp.Body.Close()
		if exResp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(exResp.Body)
			t.Fatalf("/v1/me/usage/export.csv expected 200; got %d body=%s", exResp.StatusCode, raw)
		}
	})

	// ---- 模块:playground(models[apikey] + chat stream[apikey])----
	t.Run("playground_page", func(t *testing.T) {
		// 用 seed key 调 GET /v1/models —— 前端 models.ts 读取 { object:"list", data:[{id}] }。
		st, body, m := doJSON(t, ctx, http.MethodGet, base+"/v1/models", seed.bearer, nil)
		if st != http.StatusOK {
			t.Fatalf("/v1/models expected 200; got %d body=%s", st, body)
		}
		data, ok := m["data"].([]any)
		if !ok {
			t.Fatalf("/v1/models: missing `data` array; body=%s", body)
		}
		seenAlias := false
		for _, it := range data {
			row, _ := it.(map[string]any)
			if id, _ := row["id"].(string); id == "gpt-4.1-mini" {
				seenAlias = true
			}
		}
		if !seenAlias {
			t.Fatalf("/v1/models: seeded alias gpt-4.1-mini not listed (model select would be empty); body=%s", body)
		}

		// POST /v1/chat/completions 流式 —— playground 的发送路径(SSE + usage)。
		chatBody := `{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", strings.NewReader(chatBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+seed.bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("chat stream: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("chat stream expected 200; got %d body=%s", resp.StatusCode, raw)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
			t.Fatalf("chat stream Content-Type = %q; want text/event-stream", ct)
		}
		raw, _ := io.ReadAll(resp.Body)
		if !bytes.Contains(raw, []byte("data:")) {
			t.Fatalf("chat stream has no SSE `data:` lines; body=%s", raw)
		}
	})

	// ---- 衔接:刚创建的 key 是一个真实、可用的推理凭证 ----
	t.Run("created_key_is_usable", func(t *testing.T) {
		if createdKey == "" {
			t.Skip("no created key (api_keys subtest failed)")
		}
		st, body, m := doJSON(t, ctx, http.MethodGet, base+"/v1/models", createdKey, nil)
		if st != http.StatusOK {
			t.Fatalf("/v1/models with freshly-created key expected 200; got %d body=%s", st, body)
		}
		if _, ok := m["data"].([]any); !ok {
			t.Fatalf("/v1/models with created key: missing data array; body=%s", body)
		}
	})

	// ============ 批次 1:门户补全模块(session 认证)============
	// 刚注册的用户 → 数据为空,但每个端点都必须返回 200 + 前端 lib
	// 解析的那种 envelope 形状。为 redeem / subscriptions / notifications / account
	// 断言接线(路由已挂载、认证被接受、形状正确)。

	// 模块:redeem(vouchers)—— history GET + redeem 错误路径(路由已接线)。
	t.Run("redeem_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/me/voucher-redemptions", sessionToken, "redemptions")
		st, body, obj := doJSON(t, ctx, http.MethodPost, base+"/v1/users/me/vouchers/redeem", sessionToken,
			map[string]any{"code": "WIRE-NOPE-" + unique})
		// 没有这张 voucher → 期望一个「结构化」的 {error}(说明 handler 已执行),而非 chi 的 404 路由未命中。
		if st != http.StatusOK {
			if _, ok := obj["error"].(map[string]any); !ok {
				t.Fatalf("redeem bad-code: expected structured {error} (route wired); got %d body=%s", st, body)
			}
		}
	})

	// 模块:subscriptions —— current / list / plans(若 quota 未配置,progress 可能返回 503)。
	t.Run("subscriptions_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/users/me/subscriptions/", sessionToken, "subscriptions")
		getOK(t, ctx, base+"/v1/users/me/subscriptions/me", sessionToken, "auto_renew")
		getOK(t, ctx, base+"/v1/users/me/subscriptions/plans", sessionToken, "plans")
		st, _, _ := doJSON(t, ctx, http.MethodGet, base+"/v1/users/me/subscriptions/me/progress", sessionToken, nil)
		if st != http.StatusOK && st != http.StatusServiceUnavailable {
			t.Fatalf("subscriptions progress expected 200 or 503; got %d", st)
		}
	})

	// 模块:notifications + announcements + 每用户的 notify 设置。
	t.Run("notifications_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/notifications", sessionToken, "items")
		getOK(t, ctx, base+"/v1/notifications/unread-count", sessionToken, "count")
		getOK(t, ctx, base+"/v1/announcements", sessionToken, "items")
		getOK(t, ctx, base+"/v1/users/me/notifications", sessionToken, "notify_type")
	})

	// 模块:account(groups / invitations / invite-code / checkin / referrals / rewards)。
	// invite-code/referrals/rewards 依赖 invitation+referral 功能配置,
	// 而这套最小 dev 组装并未配置它 → 结构化 503。我们断言
	// 接线(路由已挂载、认证被接受、响应结构化),并容忍该 503。
	t.Run("account_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/me/groups", sessionToken, "items")
		getOK(t, ctx, base+"/v1/me/invitations", sessionToken, "qualified_count")
		getOK(t, ctx, base+"/v1/me/checkin", sessionToken, "checked_in_today")
		getOKorUnavailable(t, ctx, base+"/v1/me/invitation-code", sessionToken, "code")
		getOKorUnavailable(t, ctx, base+"/v1/me/referrals", sessionToken, "items")
		getOKorUnavailable(t, ctx, base+"/v1/me/referrals/rewards", sessionToken, "total_reward_usd")
	})

	// ============ 批次 2:门户纵深模块 ============

	// 模块:billing(balance / config / orders —— 均为 session)。
	t.Run("billing_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/users/me/payments/balance", sessionToken, "balance")
		getOK(t, ctx, base+"/v1/users/me/payments/config", sessionToken, "config")
		getOK(t, ctx, base+"/v1/users/me/payments/orders", sessionToken, "orders")
	})

	// 模块:account security(2FA status / passkeys / oauth-bindings —— 均为 session)。
	t.Run("security_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/auth/2fa/status", sessionToken, "available")
		getOK(t, ctx, base+"/v1/me/passkeys", sessionToken, "passkeys")
		getOK(t, ctx, base+"/v1/users/me/oauth-bindings", sessionToken, "bindings")
	})

	// 模块:pricing(public;page 是一个「数组」,若 dev 中目录未配置则可能返回 503)。
	t.Run("pricing_page", func(t *testing.T) {
		assertReachable(t, ctx, base+"/v1/pricing/page", "")
		assertReachable(t, ctx, base+"/v1/pricing/snapshots", "")
	})

	// 模块:audit & receipts(HUAKAI 护城河 —— receipt get / disputes / audit pubkey)。
	t.Run("audit_page", func(t *testing.T) {
		// 用一个不存在的 id 取 receipt → 结构化错误(路由已接线),而非 chi 路由未命中。
		st, body, obj := doJSON(t, ctx, http.MethodGet, base+"/v1/receipts/wire-nope-"+unique, sessionToken, nil)
		if st != http.StatusOK {
			if _, ok := obj["error"].(map[string]any); !ok {
				t.Fatalf("receipt get bad-id: expected structured {error} (route wired); got %d body=%s", st, body)
			}
		}
		getOK(t, ctx, base+"/v1/me/disputes", sessionToken)        // 列表可达(200,形状不固定)
		assertReachable(t, ctx, base+"/v1/audit/pubkey", "")       // public;200 或结构化 503(signer)
	})

	// ============ 批次 3:管理控制台核心(admin-token 轨道)============
	// 使用播种的 tenant_operator admin token(而非 session)。证明管理
	// 页面的接线确实命中真实的 /admin/v1 + /v1/admin 路由,且 admin 认证被接受。

	t.Run("admin_users_page", func(t *testing.T) {
		getOK(t, ctx, base+"/admin/v1/users", adminBearer, "items")
	})

	t.Run("admin_accounts_page", func(t *testing.T) {
		getOK(t, ctx, base+"/admin/v1/provider-accounts", adminBearer, "items")
	})

	// channel-health + ops/usage 是 platform_admin 的全局管理面。
	t.Run("admin_channels_page", func(t *testing.T) {
		assertReachable(t, ctx, fmt.Sprintf("%s/v1/admin/channel-health/?tenant_id=%d", base, seed.tenantID), adminPlatformBearer)
	})

	t.Run("admin_ops_page", func(t *testing.T) {
		assertReachable(t, ctx, base+"/v1/admin/usage/overview?window=24h", adminPlatformBearer)
	})

	// ============ 批次 4:管理控制台纵深 ============
	t.Run("admin_credentials_page", func(t *testing.T) {
		getOK(t, ctx, base+"/admin/v1/credentials/renew-status", adminPlatformBearer, "items")
	})
	t.Run("admin_settings_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/admin/platform-settings", adminPlatformBearer, "items")
	})
	t.Run("admin_operations_page", func(t *testing.T) {
		assertWired(t, ctx, fmt.Sprintf("%s/v1/admin/subscriptions/plans?tenant_id=%d", base, seed.tenantID), adminPlatformBearer)
		assertWired(t, ctx, fmt.Sprintf("%s/v1/admin/vouchers?tenant_id=%d", base, seed.tenantID), adminPlatformBearer)
	})
	t.Run("admin_system_page", func(t *testing.T) {
		assertWired(t, ctx, base+"/admin/v1/system/health", adminPlatformBearer)
		assertWired(t, ctx, base+"/admin/v1/modules", adminPlatformBearer)
	})

	// ============ 收尾模块 ============
	//(overview /dashboard 复用已断言过的端点:balance/quota/api-keys/
	//  checkin/time-series —— 没有新的接线需要断言。)

	// 模块:sessions —— 活跃 session family 列表(POST,session 认证)。
	t.Run("sessions_page", func(t *testing.T) {
		st, body, obj := doJSON(t, ctx, http.MethodPost, base+"/v1/sessions/list", sessionToken, map[string]any{})
		if st != http.StatusOK {
			t.Fatalf("POST /v1/sessions/list expected 200; got %d body=%s", st, body)
		}
		if _, ok := obj["families"]; !ok {
			t.Fatalf("sessions list missing `families` (page maps over it); body=%s", body)
		}
	})

	// 模块:hermes(管理助手)。/v1/hermes 仅在 hermesService
	// 「与」hermesRunner 都已接线时才挂载(routes.go)—— 最小 dev 网关二者皆未接线,
	// 因此该路由不存在(404)。这种情况下如实 skip;在已挂载处则断言接线。
	t.Run("hermes_page", func(t *testing.T) {
		url := fmt.Sprintf("%s/v1/hermes/settings?as_user_id=%d&tenant_id=%d", base, seed.userID, seed.tenantID)
		st, _, _ := doJSON(t, ctx, http.MethodGet, url, adminPlatformBearer, nil)
		if st == http.StatusNotFound {
			t.Skip("hermes not mounted in minimal dev gateway (needs hermesService+hermesRunner); frontend page uses verified contract")
		}
		assertWired(t, ctx, url, adminPlatformBearer)
	})

	// 模块:inference console —— embeddings 路由已接线(未播种 embeddings 模型 → 结构化错误亦可接受)。
	t.Run("console_page", func(t *testing.T) {
		st, body, obj := doJSON(t, ctx, http.MethodPost, base+"/v1/embeddings", seed.bearer,
			map[string]any{"model": "gpt-4.1-mini", "input": "wire test"})
		if st != http.StatusOK {
			if _, ok := obj["error"].(map[string]any); !ok {
				t.Fatalf("POST /v1/embeddings: expected 200 or structured error (route wired); got %d body=%s", st, body)
			}
		}
	})
}

// doJSON 发送一个可选的 JSON body 并附带可选的 Bearer token,返回
//(status、rawBody、parsedObject)。对于非 object 的响应,parsedObject 为 nil。
func doJSON(t *testing.T, ctx context.Context, method, url, bearer string, body any) (int, []byte, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var obj map[string]any
	_ = json.Unmarshal(raw, &obj) // 对数组/非 object 为 nil;由调用方按需断言
	return resp.StatusCode, raw, obj
}

// getOK 断言 GET url(带 bearer)返回 200,且解析出的 object 包含
// 每一个必需的 key,然后返回该 object。用于覆盖面层面的接线检查。
func getOK(t *testing.T, ctx context.Context, url, bearer string, keys ...string) map[string]any {
	t.Helper()
	st, body, obj := doJSON(t, ctx, http.MethodGet, url, bearer, nil)
	if st != http.StatusOK {
		t.Fatalf("GET %s expected 200; got %d body=%s", url, st, body)
	}
	for _, k := range keys {
		if _, ok := obj[k]; !ok {
			t.Fatalf("GET %s missing key %q (frontend lib parses it); body=%s", url, k, body)
		}
	}
	return obj
}

// getOKorUnavailable 是同时也接受「结构化」503 的 getOK —— 用于那些
// 后端功能在最小 dev 组装中未配置的端点。它依然能证明
// 接线(路由已挂载、认证被接受、响应结构化形状);而 chi 的
// 路由未命中(404、无 {error})或形状错误仍会失败。
func getOKorUnavailable(t *testing.T, ctx context.Context, url, bearer string, keys ...string) {
	t.Helper()
	st, body, obj := doJSON(t, ctx, http.MethodGet, url, bearer, nil)
	if st == http.StatusServiceUnavailable {
		if _, ok := obj["error"].(map[string]any); !ok {
			t.Fatalf("GET %s 503 but not structured {error} (route may be unmounted); body=%s", url, body)
		}
		t.Logf("GET %s → 503 (feature unconfigured in dev assembly; wire OK)", url)
		return
	}
	if st != http.StatusOK {
		t.Fatalf("GET %s expected 200 or structured 503; got %d body=%s", url, st, body)
	}
	for _, k := range keys {
		if _, ok := obj[k]; !ok {
			t.Fatalf("GET %s missing key %q; body=%s", url, k, body)
		}
	}
}

// assertReachable 接受 200(任意 body 形状 —— 数组或 object)「或」一个结构化
// 503。用于那些不适用 key 检查、但我们仍想证明路由已挂载
// 而非 chi 404 路由未命中的 public/数组端点。
func assertReachable(t *testing.T, ctx context.Context, url, bearer string) {
	t.Helper()
	st, body, obj := doJSON(t, ctx, http.MethodGet, url, bearer, nil)
	if st == http.StatusOK {
		return
	}
	if st == http.StatusServiceUnavailable {
		if _, ok := obj["error"].(map[string]any); !ok {
			t.Fatalf("GET %s 503 but not structured (route may be unmounted); body=%s", url, body)
		}
		t.Logf("GET %s → 503 (unconfigured in dev; wire OK)", url)
		return
	}
	t.Fatalf("GET %s expected 200 or structured 503; got %d body=%s", url, st, body)
}

// assertWired 是最宽松的接线证明:200,「或」任何携带
// 「结构化」{error} 的 4xx/5xx(路由已挂载 + handler 已执行 + 响应结构化 —— 而非 chi
// 路由未命中)。用于那些精确的角色/租户细节在本最小播种中
// 未能完全满足、但我们仍想证明其接线的管理纵深端点。
func assertWired(t *testing.T, ctx context.Context, url, bearer string) {
	t.Helper()
	st, body, obj := doJSON(t, ctx, http.MethodGet, url, bearer, nil)
	if st == http.StatusOK {
		return
	}
	if _, ok := obj["error"].(map[string]any); ok {
		t.Logf("GET %s → %d structured (route wired; auth/param nuance)", url, st)
		return
	}
	t.Fatalf("GET %s expected 200 or structured error (route wired, not route-miss); got %d body=%s", url, st, body)
}
