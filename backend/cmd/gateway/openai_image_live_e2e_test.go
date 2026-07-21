//go:build e2e_openai_image_live || e2e_grok_image_live || e2e_grok_video_live || e2e_gemini_video_live

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

const (
	openAIImageLiveBearerPrefix = "hk_test_"
	openAIImageLiveBinaryName   = "gateway-openai-image-live-e2e.exe"
	openAIImageLiveModel        = "gpt-image-1"
	openAIImageLiveProtocol     = registrydefault.ProtocolOpenAIChat
	openAIImageLiveVendor       = credentialstore.VendorOpenAI
	openAIImageLiveAuthMode     = credentialstore.AuthModeAPIKey
	openAIImageLiveQuotaLimit   = "1000.00000000"

	openAIImageLiveBootRetries   = 30
	openAIImageLiveBootRetryWait = 200 * time.Millisecond

	openAIImageLivePricingData = `{"providers":{"openai":{"models":{"gpt-image-1":{"pricing_scheme":"token_image","input_micro_usd":"5","output_micro_usd":"40","image_output_token_upper_bound":{"1024x1024":4160,"1024x1536":6240,"1536x1024":6208,"auto":6240},"image_size_multipliers":{"1024x1024":"1","1024x1536":"1","1536x1024":"1","auto":"1"},"image_amount_range":{"min":1,"max":10},"image_prompt_max_chars":32000}}}}}`
	grokImageLivePricingData   = `{"providers":{"grok":{"models":{"grok-imagine-image":{"pricing_scheme":"per_image","image_base_micro_usd":"1","image_size_multipliers":{"1024x1024":"1"},"image_quality_multipliers":{"standard":"1"},"image_amount_range":{"min":1,"max":1},"image_prompt_max_chars":32000}}}}}`
)

type imageLiveCase struct {
	slug         string
	model        string
	protocol     string
	vendor       string
	authMode     string
	accountType  string
	keyEnv       string
	pricingData  string
	requestExtra map[string]any
	capabilities []string
}

var openAIImageLiveCase = imageLiveCase{
	slug: "openai-image", model: openAIImageLiveModel,
	protocol: openAIImageLiveProtocol, vendor: openAIImageLiveVendor,
	authMode: openAIImageLiveAuthMode, keyEnv: "HUAKAI_E2E_OPENAI_IMAGE_KEY",
	pricingData:  openAIImageLivePricingData,
	requestExtra: map[string]any{"size": "1024x1024"},
	capabilities: []string{"image_output", "images"},
}

var grokImageLiveCase = imageLiveCase{
	slug: "grok-image", model: "grok-imagine-image",
	protocol: registrydefault.ProtocolGrokChat, vendor: credentialstore.VendorGrok,
	authMode: credentialstore.AuthModeAPIKey, keyEnv: "HUAKAI_E2E_GROK_KEY",
	pricingData: grokImageLivePricingData, capabilities: []string{"image_output", "images"},
}

func TestOpenAIImageLiveFormalImportWiring(t *testing.T) {
	if !imageLiveTestsEnabled {
		t.Skip("当前只运行 Grok 视频活体测试")
	}
	dsn := firstOpenAIImageLiveNonEmpty(
		os.Getenv("HUAKAI_E2E_DATABASE_URL"),
		os.Getenv("HUAKAI_DATABASE_URL"),
	)
	if dsn == "" {
		t.Skip("HUAKAI_E2E_DATABASE_URL/HUAKAI_DATABASE_URL 未设置，跳过 OpenAI 图片正式导入接线测试")
	}
	dsn = useDisposableSpecializedLiveDatabase(t, dsn)

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	pgPool, err := db.Open(setupCtx, db.PoolConfig{DSN: dsn})
	if err != nil {
		setupCancel()
		t.Fatalf("打开 OpenAI 图片正式导入测试数据库: %v", err)
	}
	t.Cleanup(pgPool.Close)
	seed := seedOpenAIImageLiveGraph(t, setupCtx, pgPool, openAIImageLiveCase)
	setupCancel()
	binPath := buildOpenAIImageLiveGateway(t)
	addr := reserveOpenAIImageLiveLocalPort(t)
	const syntheticKey = "synthetic-openai-image-live-key"
	processes := startOpenAIImageLiveGateway(t, binPath, dsn, addr, seed, syntheticKey)
	t.Cleanup(func() { stopSpecializedLiveProcesses(processes) })
	waitForOpenAIImageLiveGateway(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)
	seed.providerAccountID = importOpenAIImageLiveAccount(
		t, ctx, &http.Client{Timeout: 30 * time.Second}, addr, seed, syntheticKey,
	)
	assertOpenAIImageLiveImportedAccount(t, ctx, pgPool, seed)
	assertOpenAIImageLiveSeedSelectable(t, ctx, pgPool, seed)
}

func TestOpenAIImageLiveGenerations(t *testing.T) {
	if !imageLiveTestsEnabled {
		t.Skip("当前只运行 Grok 视频活体测试")
	}
	runImageLiveGenerations(t, openAIImageLiveCase)
}

func TestGrokImageLiveGenerations(t *testing.T) {
	if !imageLiveTestsEnabled {
		t.Skip("当前只运行 Grok 视频活体测试")
	}
	runImageLiveGenerations(t, grokImageLiveCase)
}

func runImageLiveGenerations(t *testing.T, testCase imageLiveCase) {
	t.Helper()
	dsn := firstOpenAIImageLiveNonEmpty(
		os.Getenv("HUAKAI_E2E_DATABASE_URL"),
		os.Getenv("HUAKAI_DATABASE_URL"),
	)
	if dsn == "" {
		t.Skip("HUAKAI_E2E_DATABASE_URL/HUAKAI_DATABASE_URL 未设置，跳过 OpenAI 图片 live e2e")
	}
	dsn = useDisposableSpecializedLiveDatabase(t, dsn)
	upstreamKey := strings.TrimSpace(os.Getenv(testCase.keyEnv))
	if upstreamKey == "" {
		t.Skip(testCase.keyEnv + " 未设置")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 OpenAI 图片 live e2e 数据库连接池: %v", err)
	}
	// 先注册关池，后注册的数据清理会按 LIFO 在连接池关闭前执行。
	t.Cleanup(pgPool.Close)

	seed := seedOpenAIImageLiveGraph(t, ctx, pgPool, testCase)

	binPath := buildOpenAIImageLiveGateway(t)

	addr := reserveOpenAIImageLiveLocalPort(t)
	processes := startOpenAIImageLiveGateway(t, binPath, dsn, addr, seed, upstreamKey)
	t.Cleanup(func() { stopSpecializedLiveProcesses(processes) })
	waitForOpenAIImageLiveGateway(t, addr)
	seed.providerAccountID = importOpenAIImageLiveAccount(t, ctx, &http.Client{Timeout: 30 * time.Second}, addr, seed, upstreamKey)
	assertOpenAIImageLiveSeedSelectable(t, ctx, pgPool, seed)

	requestPayload := map[string]any{
		"model": testCase.model, "prompt": "a tiny solid red circle centered on a white background",
		"n": 1, "response_format": "b64_json",
	}
	for key, value := range testCase.requestExtra {
		requestPayload[key] = value
	}
	requestBody, err := json.Marshal(requestPayload)
	if err != nil {
		t.Fatalf("编码图片请求: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/v1/images/generations", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatalf("构造 OpenAI 图片请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+seed.bearer)
	req.Header.Set("Content-Type", "application/json")
	logicalID := testCase.slug + "-live-" + uuid.NewString()
	req.Header.Set("Idempotency-Key", logicalID)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/images/generations: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 OpenAI 图片响应: %v", err)
	}

	// 变异证伪：
	// ① 网关漏掉 Authorization Bearer 时，上游返回 401，HTTP 200 断言转红。
	// ② 误路由或换错端点时，响应为 404 或不含图片，状态或 b64_json 断言转红。
	// ③ gpt-image-1 计价缺失时，图片入口 FAIL CLOSED 返回 503，HTTP 200 断言转红。
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status=%d want 200 body=%s", resp.StatusCode, openAIImageLiveBodyPreview(raw))
	}
	var decoded struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("解析 OpenAI 图片响应 JSON: %v body=%s", err, openAIImageLiveBodyPreview(raw))
	}
	if len(decoded.Data) == 0 {
		t.Fatalf("OpenAI 图片响应 data 为空: body=%s", openAIImageLiveBodyPreview(raw))
	}
	encoded := strings.TrimSpace(decoded.Data[0].B64JSON)
	if encoded == "" {
		t.Fatalf("OpenAI 图片响应 data[0].b64_json 为空: body=%s", openAIImageLiveBodyPreview(raw))
	}
	imageBytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("解码 data[0].b64_json: %v", err)
	}
	if len(imageBytes) == 0 {
		t.Fatal("data[0].b64_json 解码后没有图片字节")
	}
	assertImageLiveMoneyAndRelease(t, ctx, pgPool, seed, logicalID)
}

type openAIImageLiveSeed struct {
	testCase          imageLiveCase
	tenantID          int64
	apiKeyID          int64
	userID            int64
	providerID        int64
	poolGroupID       int64
	channelID         int64
	providerAccountID int64
	modelID           int64
	aliasID           int64
	costQuotaPolicyID int64
	pricingVersion    string
	bearer            string
	adminBearer       string
	adminTokenID      int64
}

func seedOpenAIImageLiveGraph(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, testCase imageLiveCase) *openAIImageLiveSeed {
	t.Helper()
	unique := uuid.NewString()
	seed := &openAIImageLiveSeed{testCase: testCase, pricingVersion: "e2e-" + testCase.slug + "-live-" + unique}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		testCase.slug+"-live-e2e-tenant-"+unique,
	).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	registerOpenAIImageLiveCleanup(t, pgPool, seed)

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, testCase.slug+"-live-e2e-user-"+unique,
	).Scan(&seed.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	seed.bearer = openAIImageLiveBearerPrefix + unique
	keyPrefix := seed.bearer
	if len(keyPrefix) > 16 {
		keyPrefix = keyPrefix[:16]
	}
	keyHash, err := bcrypt.GenerateFromPassword([]byte(seed.bearer), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt hash bearer: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		seed.tenantID, seed.userID, testCase.slug+"-live-e2e-key-"+unique, string(keyHash), keyPrefix,
	).Scan(&seed.apiKeyID); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
		 VALUES ($1, $2, 1000.00, 0, 1, now())`,
		seed.tenantID, seed.userID,
	); err != nil {
		t.Fatalf("seed user_balance: %v", err)
	}

	if _, err := pgPool.Exec(ctx,
		`INSERT INTO tenants (id, name, status, created_at, updated_at)
		 VALUES (0, 'public-pricing', 'active', now(), now())
		 ON CONFLICT (id) DO NOTHING`,
	); err != nil {
		t.Fatalf("seed public pricing tenant: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO billing_pricing_versions (
		    tenant_id, version, pricing_data, effective_from, created_by_actor, is_public
		  )
		  VALUES (0, $1, $2::jsonb, now(), $3, true)
		  ON CONFLICT (tenant_id, version) DO UPDATE
		  SET pricing_data = EXCLUDED.pricing_data,
		      effective_from = EXCLUDED.effective_from,
		      created_by_actor = EXCLUDED.created_by_actor,
		      is_public = true`,
		seed.pricingVersion, testCase.pricingData, "e2e:"+testCase.slug+"-live",
	); err != nil {
		t.Fatalf("seed billing pricing version: %v", err)
	}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		seed.tenantID, testCase.vendor, testCase.slug+" live e2e "+unique, testCase.protocol,
	).Scan(&seed.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, testCase.slug+"-live-e2e-pg-"+unique,
	).Scan(&seed.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		seed.tenantID, seed.poolGroupID, testCase.slug+"-live-e2e-ch-"+unique,
	).Scan(&seed.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	seed.adminBearer, seed.adminTokenID = seedSpecializedLiveImportAuthorization(
		t, ctx, pgPool, seed.tenantID, testCase.slug+"-live-import-"+unique,
	)

	capabilitiesJSON, err := json.Marshal(capabilityMap(testCase.capabilities))
	if err != nil {
		t.Fatalf("编码模型能力: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO models (tenant_id, scope, canonical_id, protocol_family,
		                     default_provider_model_id, default_context_window, capabilities, status)
		 VALUES ($1, 'tenant', $2, $3, $4, 128000, $5::jsonb, 'active')
		 RETURNING id`,
		seed.tenantID, testCase.model, testCase.protocol, testCase.model, capabilitiesJSON,
	).Scan(&seed.modelID); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	for _, capability := range testCase.capabilities {
		if _, err := pgPool.Exec(ctx, `
INSERT INTO model_registry_capabilities (
    tenant_id, scope, model_id, capability, enabled, source
) VALUES ($1, 'tenant', $2, $3, true, 'e2e')`, seed.tenantID, seed.modelID, capability); err != nil {
			t.Fatalf("seed model capability %q: %v", capability, err)
		}
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO model_aliases (tenant_id, scope, model_id,
		                            public_alias_normalized, public_alias_display, status)
		 VALUES ($1, 'tenant', $2, $3, $4, 'active')
		 RETURNING id`,
		seed.tenantID, seed.modelID, testCase.model, testCase.model,
	).Scan(&seed.aliasID); err != nil {
		t.Fatalf("seed model_alias: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO model_pool_bindings (tenant_id, model_id, pool_group_id, priority, weight, enabled)
		 VALUES ($1, $2, $3, 100, 1, true)`,
		seed.tenantID, seed.modelID, seed.poolGroupID,
	); err != nil {
		t.Fatalf("seed model_pool_bindings: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO model_registry_snapshots (tenant_id, version)
		 VALUES ($1, 1)
		 ON CONFLICT (tenant_id) DO UPDATE SET version = 1`,
		seed.tenantID,
	); err != nil {
		t.Fatalf("seed model_registry_snapshots: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO quota_policies (
			tenant_id, scope_kind, scope_id, metric, window_kind, window_seconds,
			limit_value, burst_value, mode, priority, enabled, valid_from
		 )
		 VALUES ($1, 'user', $2, 'cost_usd', 'fixed', 3600,
		         $3::numeric, 0, 'enforce', 10, true, now())
		 RETURNING id`,
		seed.tenantID, strconv.FormatInt(seed.userID, 10), openAIImageLiveQuotaLimit,
	).Scan(&seed.costQuotaPolicyID); err != nil {
		t.Fatalf("seed cost quota policy: %v", err)
	}
	return seed
}

func capabilityMap(capabilities []string) map[string]bool {
	out := make(map[string]bool, len(capabilities))
	for _, capability := range capabilities {
		if capability = strings.TrimSpace(capability); capability != "" {
			out[capability] = true
		}
	}
	return out
}

func importOpenAIImageLiveAccount(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	addr string,
	seed *openAIImageLiveSeed,
	upstreamKey string,
) int64 {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"api_key": upstreamKey})
	if err != nil {
		t.Fatalf("序列化 OpenAI API key 凭据: %v", err)
	}
	capConcurrency := int32(1)
	priority := int32(100)
	accountType := strings.TrimSpace(seed.testCase.accountType)
	if accountType == "" {
		accountType = seed.testCase.authMode
	}
	result := executeSpecializedLiveAccountImport(
		t, ctx, client, addr,
		"/admin/v1/credentials/account-imports/plan",
		"/admin/v1/credentials/account-imports/execute",
		seed.adminBearer,
		specializedLiveAccountImportRequest{
			TenantID:        seed.tenantID,
			SourceKind:      intake.SourceJSON,
			DefaultVendor:   seed.testCase.vendor,
			DefaultAuthMode: seed.testCase.authMode,
			Content:         string(payload),
			Account: accountintake.AccountDefaults{
				ProviderID:      seed.providerID,
				ChannelID:       seed.channelID,
				ExactName:       seed.testCase.slug + "-live-e2e-正式导入-" + uuid.NewString(),
				AccountType:     accountType,
				CapConcurrency:  &capConcurrency,
				Priority:        &priority,
				ModelAllowList:  []string{seed.testCase.model},
				CapabilityFlags: append([]string(nil), seed.testCase.capabilities...),
			},
		},
		upstreamKey,
	)
	return result.AccountID
}

func registerOpenAIImageLiveCleanup(t *testing.T, pgPool *pgxpool.Pool, seed *openAIImageLiveSeed) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		execCleanup := func(label, statement string, args ...any) bool {
			if _, err := pgPool.Exec(ctx, statement, args...); err != nil {
				t.Errorf("清理图片活体测试%s: %v", label, err)
				return false
			}
			return true
		}
		for _, step := range []struct {
			label     string
			statement string
		}{
			{"渠道健康告警", `DELETE FROM channel_health_admin_alerts WHERE tenant_id=$1`},
			{"渠道健康日志", `DELETE FROM channel_health_audit_events WHERE tenant_id=$1`},
			{"渠道健康状态", `DELETE FROM channel_health_state WHERE tenant_id=$1`},
			{"凭据流程", `DELETE FROM credential_acquisition_flow_sessions WHERE tenant_id=$1`},
			{"凭据日志", `DELETE FROM credential_audit_events WHERE tenant_id=$1`},
			{"管理日志", `DELETE FROM admin_audit_events WHERE tenant_id=$1`},
		} {
			if !execCleanup(step.label, step.statement, seed.tenantID) {
				return
			}
		}
		if err := cleanupSpecializedLiveSubscriptionObservations(ctx, pgPool, seed.tenantID); err != nil {
			t.Errorf("清理 OpenAI 图片 live e2e 套餐观测: %v", err)
			return
		}
		for _, step := range []struct {
			label     string
			statement string
		}{
			{"加密凭据", `DELETE FROM account_credentials WHERE tenant_id=$1`},
			{"配额日志", `DELETE FROM quota_audit_events WHERE tenant_id=$1`},
			{"配额并发槽", `DELETE FROM quota_concurrency_slots WHERE tenant_id=$1`},
			{"配额作用域锁", `DELETE FROM quota_concurrency_scope_locks WHERE tenant_id=$1`},
			{"配额恢复任务", `DELETE FROM quota_reconciliation_jobs WHERE tenant_id=$1`},
			{"配额预留", `DELETE FROM quota_reservations WHERE tenant_id=$1`},
			{"配额窗口", `DELETE FROM quota_windows WHERE tenant_id=$1`},
			{"配额策略", `DELETE FROM quota_policies WHERE tenant_id=$1`},
			{"幂等记录", `DELETE FROM idempotency_replay_records WHERE tenant_id=$1`},
		} {
			if !execCleanup(step.label, step.statement, seed.tenantID) {
				return
			}
		}
		if err := cleanupSpecializedLiveMoneyRows(ctx, pgPool, seed.tenantID); err != nil {
			t.Errorf("清理 OpenAI 图片 live e2e 钱路记录: %v", err)
			return
		}
		if !execCleanup("管理令牌", `DELETE FROM admin_tokens WHERE id=$1`, seed.adminTokenID) {
			return
		}
		for _, step := range []struct {
			label     string
			statement string
		}{
			{"模型池绑定", `DELETE FROM model_pool_bindings WHERE tenant_id=$1`},
			{"模型能力", `DELETE FROM model_registry_capabilities WHERE tenant_id=$1`},
			{"模型别名", `DELETE FROM model_aliases WHERE tenant_id=$1`},
			{"模型", `DELETE FROM models WHERE tenant_id=$1`},
			{"模型快照", `DELETE FROM model_registry_snapshots WHERE tenant_id=$1`},
			{"模型租户策略", `DELETE FROM model_registry_tenant_policies WHERE tenant_id=$1`},
			{"粘性绑定", `DELETE FROM sticky_bindings WHERE tenant_id=$1`},
			{"上游账号", `DELETE FROM provider_accounts WHERE tenant_id=$1`},
			{"渠道", `DELETE FROM channels WHERE tenant_id=$1`},
			{"账号池", `DELETE FROM pool_groups WHERE tenant_id=$1`},
			{"供应商", `DELETE FROM providers WHERE tenant_id=$1`},
			{"租户能力授权", `DELETE FROM tenant_admin_capability_grants WHERE tenant_id=$1`},
			{"用户余额", `DELETE FROM user_balances WHERE tenant_id=$1`},
			{"客户 Key", `DELETE FROM api_keys WHERE tenant_id=$1`},
			{"用户", `DELETE FROM users WHERE tenant_id=$1`},
			{"租户钱包", `DELETE FROM tenant_wallets WHERE tenant_id=$1`},
			{"租户", `DELETE FROM tenants WHERE id=$1`},
		} {
			if !execCleanup(step.label, step.statement, seed.tenantID) {
				return
			}
		}
		_ = execCleanup("公开计价版本", `DELETE FROM billing_pricing_versions WHERE tenant_id=0 AND version=$1`, seed.pricingVersion)
	})
}

func assertOpenAIImageLiveSeedSelectable(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *openAIImageLiveSeed) {
	t.Helper()
	resolved, err := registry.NewPostgresRegistry(pgPool, nil).ResolveModel(ctx, seed.testCase.model, seed.tenantID)
	if err != nil {
		t.Fatalf("seed registry resolve %q: %v", seed.testCase.model, err)
	}
	if resolved.ProtocolFamily != seed.testCase.protocol {
		t.Fatalf("resolved protocol_family=%q want %q", resolved.ProtocolFamily, seed.testCase.protocol)
	}
	if len(resolved.PoolCandidates) != 1 || resolved.PoolCandidates[0] != seed.poolGroupID {
		t.Fatalf("resolved pool candidates=%v want [%d]", resolved.PoolCandidates, seed.poolGroupID)
	}
	rows, err := dbbilling.New(pgPool).ListEligibleAccountsByPoolGroup(ctx, dbbilling.ListEligibleAccountsByPoolGroupParams{
		TenantID:                seed.tenantID,
		PoolGroupID:             seed.poolGroupID,
		RequestedModel:          seed.testCase.model,
		RequestedProtocolFamily: resolved.ProtocolFamily,
		RequiredCapabilities:    append([]string(nil), seed.testCase.capabilities...),
	})
	if err != nil {
		t.Fatalf("seed selector eligibility query: %v", err)
	}
	for _, row := range rows {
		if row.ID == seed.providerAccountID {
			return
		}
	}
	t.Fatalf("selector eligibility 未返回 provider_account_id=%d; rows=%v", seed.providerAccountID, rows)
}

func assertOpenAIImageLiveImportedAccount(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *openAIImageLiveSeed) {
	t.Helper()
	var enabled bool
	var credentialState, healthState string
	var activeCredentials int
	var legacyCredentialBytes int
	if err := pgPool.QueryRow(ctx, `
SELECT pa.enabled, pa.credential_state, pa.health_state,
       count(ac.id) FILTER (WHERE ac.state='active')::int,
       octet_length(COALESCE(pa.credentials, '{}'::jsonb)::text)
FROM provider_accounts pa
LEFT JOIN account_credentials ac
  ON ac.tenant_id=pa.tenant_id AND ac.provider_account_id=pa.id
WHERE pa.tenant_id=$1 AND pa.id=$2
GROUP BY pa.id`, seed.tenantID, seed.providerAccountID).Scan(
		&enabled, &credentialState, &healthState, &activeCredentials, &legacyCredentialBytes,
	); err != nil {
		t.Fatalf("读取 OpenAI 图片正式导入账号投影: %v", err)
	}
	if !enabled || credentialState != "valid" || healthState != "healthy" || activeCredentials != 1 {
		t.Fatalf("OpenAI 图片正式导入账号不可运行: enabled=%t credential=%s health=%s active_credentials=%d",
			enabled, credentialState, healthState, activeCredentials)
	}
	if legacyCredentialBytes > len(`{}`) {
		t.Fatalf("OpenAI 图片正式导入仍写入 legacy 明文凭据列: bytes=%d", legacyCredentialBytes)
	}
}

func assertImageLiveMoneyAndRelease(
	t *testing.T,
	ctx context.Context,
	pgPool *pgxpool.Pool,
	seed *openAIImageLiveSeed,
	logicalID string,
) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var claimID int64
	var status, actualCost string
	for {
		err := pgPool.QueryRow(ctx, `
SELECT id, status, actual_cost::text
FROM billing_ledger_claims
WHERE tenant_id=$1 AND api_key_id=$2 AND logical_request_id=$3`,
			seed.tenantID, seed.apiKeyID, logicalID,
		).Scan(&claimID, &status, &actualCost)
		if err == nil && status == "committed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("图片请求 claim 未提交: logical_id=%s status=%q err=%v", logicalID, status, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	cost, err := strconv.ParseFloat(actualCost, 64)
	if err != nil || cost <= 0 {
		t.Fatalf("图片请求 claim actual_cost=%q，期望正数", actualCost)
	}

	var usageCount, imageCount int
	var endClass string
	if err := pgPool.QueryRow(ctx, `
SELECT count(*)::int, COALESCE(sum(image_count), 0)::int, COALESCE(max(end_class), '')
FROM usage_records
WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, claimID).Scan(
		&usageCount, &imageCount, &endClass,
	); err != nil {
		t.Fatalf("读取图片 usage_records: %v", err)
	}
	if usageCount != 1 || imageCount != 1 || endClass != "non_streaming" {
		t.Fatalf("图片 usage_records count/image_count/end_class=%d/%d/%q，期望 1/1/non_streaming",
			usageCount, imageCount, endClass)
	}

	var reservationStatus, settledCost string
	if err := pgPool.QueryRow(ctx, `
SELECT status, settled_cost::text
FROM quota_reservations
WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, claimID).Scan(
		&reservationStatus, &settledCost,
	); err != nil {
		t.Fatalf("读取图片 quota_reservation: %v", err)
	}
	settled, parseErr := strconv.ParseFloat(settledCost, 64)
	if reservationStatus != "settled" || parseErr != nil || settled <= 0 {
		t.Fatalf("图片配额预留 status/cost=%q/%q，期望 settled/正数", reservationStatus, settledCost)
	}

	var held string
	var inFlight int
	if err := pgPool.QueryRow(ctx, `
SELECT ub.held::text, pa.in_flight_count
FROM user_balances ub
JOIN provider_accounts pa ON pa.tenant_id=ub.tenant_id
WHERE ub.tenant_id=$1 AND ub.user_id=$2 AND pa.id=$3`,
		seed.tenantID, seed.userID, seed.providerAccountID,
	).Scan(&held, &inFlight); err != nil {
		t.Fatalf("读取图片余额与账号槽位: %v", err)
	}
	if held != "0.00000000" || inFlight != 0 {
		t.Fatalf("图片完成后 held/in_flight=%s/%d，期望 0/0", held, inFlight)
	}

	var receiptCount int
	if err := pgPool.QueryRow(ctx, `
SELECT count(*)::int
FROM user_cost_receipt_owners o
JOIN user_cost_receipts r
  ON r.tenant_id=o.tenant_id
 AND r.request_id=o.request_id
 AND r.receipt_sequence=o.receipt_sequence
WHERE o.tenant_id=$1 AND o.claim_id=$2 AND octet_length(r.signed_hash)>0`,
		seed.tenantID, claimID,
	).Scan(&receiptCount); err != nil {
		t.Fatalf("读取图片成本回执: %v", err)
	}
	if receiptCount != 1 {
		t.Fatalf("图片请求成本回执数量=%d，期望 1", receiptCount)
	}
}

func buildOpenAIImageLiveGateway(t *testing.T) string {
	t.Helper()
	moduleRoot := goModuleRootForOpenAIImageLive(t)
	binPath := specializedLiveArtifactPath(t, openAIImageLiveBinaryName)
	t.Cleanup(func() { _ = os.Remove(binPath) })
	stamp := fmt.Sprintf("openai-image-live-e2e-%d", time.Now().UnixNano())
	cmd := exec.Command("go", "build",
		"-ldflags", "-X main.smokeBuildStamp="+stamp,
		"-o", binPath, "./cmd/gateway")
	cmd.Dir = moduleRoot
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("从 %s 构建 gateway: %v", moduleRoot, err)
	}
	return binPath
}

func goModuleRootForOpenAIImageLive(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("读取 go env GOMOD: %v", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		t.Fatal("当前目录不在 Go module 中")
	}
	return filepath.Dir(gomod)
}

func reserveOpenAIImageLiveLocalPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("预留本地端口: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("关闭预留端口监听器: %v", err)
	}
	return addr
}

func startOpenAIImageLiveGateway(t *testing.T, binPath, dsn, addr string, seed *openAIImageLiveSeed, upstreamKey string) *specializedLiveProcesses {
	t.Helper()
	blockedEnvNames := []string{seed.testCase.keyEnv}
	sidecar, socketPath := startSpecializedLiveSidecar(t, goModuleRootForOpenAIImageLive(t), blockedEnvNames...)
	cmd := exec.Command(binPath)
	cmd.Env = append(specializedLiveChildEnv(blockedEnvNames...),
		"HUAKAI_DATABASE_URL="+dsn,
		"HUAKAI_ADDR="+addr,
		"HUAKAI_RELEASE_MODE=dev",
		"HUAKAI_CREDENTIAL_KEY_B64=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"HUAKAI_SESSION_SIGNING_KEY_B64=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"HUAKAI_AUDIT_LEDGER_BACKEND=postgres",
		"HUAKAI_DEV_MOCK_UPSTREAM=false",
		"HUAKAI_BILLING_POLICY_VERSION="+seed.pricingVersion,
		"HUAKAI_QUOTA_ENFORCE=true",
		"HUAKAI_CACHE_L2_ENABLED=0",
		"HUAKAI_EVENTBUS_ENABLED=0",
		"HUAKAI_RATE_PRECHECK_ENABLED=false",
		"HUAKAI_BINDING_RATE_LIMIT_ENABLED=false",
		"HUAKAI_TRANSPORT_SIDECAR_SOCKET="+socketPath,
		"HUAKAI_KEY_RPM_LIMIT=0",
		"HUAKAI_KEY_TPM_LIMIT=0",
		"HUAKAI_DISPATCH_HCSF=1",
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("连接 gateway stderr: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("连接 gateway stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		stopSpecializedLiveProcess(sidecar)
		_ = os.Remove(socketPath)
		t.Fatalf("启动 gateway: %v", err)
	}
	go drainOpenAIImageLivePipe("gateway-stderr", stderr, upstreamKey, seed.bearer)
	go drainOpenAIImageLivePipe("gateway-stdout", stdout, upstreamKey, seed.bearer)
	return &specializedLiveProcesses{gateway: cmd, sidecar: sidecar, socketPath: socketPath}
}

func drainOpenAIImageLivePipe(label string, reader io.ReadCloser, secrets ...string) {
	if reader == nil {
		return
	}
	defer reader.Close()
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fmt.Printf("[%s] %s\n", label, redactOpenAIImageLiveSecrets(scanner.Text(), secrets...))
	}
}

func redactOpenAIImageLiveSecrets(value string, secrets ...string) string {
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}

func waitForOpenAIImageLiveGateway(t *testing.T, addr string) {
	t.Helper()
	for i := 0; i < openAIImageLiveBootRetries; i++ {
		resp, err := http.Get("http://" + addr + "/")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(openAIImageLiveBootRetryWait)
	}
	t.Fatalf("gateway 未在 %v 内监听 %s",
		time.Duration(openAIImageLiveBootRetries)*openAIImageLiveBootRetryWait, addr)
}

func openAIImageLiveBodyPreview(raw []byte) string {
	const maxBytes = 4096
	if len(raw) <= maxBytes {
		return string(raw)
	}
	return string(raw[:maxBytes]) + "...[已截断]"
}

func firstOpenAIImageLiveNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
