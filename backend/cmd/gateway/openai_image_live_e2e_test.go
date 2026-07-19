//go:build e2e_openai_image_live

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
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
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
)

func TestOpenAIImageLiveGenerations(t *testing.T) {
	dsn := firstOpenAIImageLiveNonEmpty(
		os.Getenv("HUAKAI_E2E_DATABASE_URL"),
		os.Getenv("HUAKAI_DATABASE_URL"),
	)
	if dsn == "" {
		t.Skip("HUAKAI_E2E_DATABASE_URL/HUAKAI_DATABASE_URL 未设置，跳过 OpenAI 图片 live e2e")
	}
	upstreamKey := strings.TrimSpace(os.Getenv("HUAKAI_E2E_OPENAI_IMAGE_KEY"))
	if upstreamKey == "" {
		t.Skip("未提供 sk- 图片 key")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 OpenAI 图片 live e2e 数据库连接池: %v", err)
	}
	// 先注册关池，后注册的数据清理会按 LIFO 在连接池关闭前执行。
	t.Cleanup(pgPool.Close)

	seed := seedOpenAIImageLiveGraph(t, ctx, pgPool, upstreamKey)
	assertOpenAIImageLiveSeedSelectable(t, ctx, pgPool, seed)

	binPath := buildOpenAIImageLiveGateway(t)
	t.Cleanup(func() { _ = os.Remove(binPath) })

	addr := reserveOpenAIImageLiveLocalPort(t)
	cmd := startOpenAIImageLiveGateway(t, binPath, dsn, addr, seed, upstreamKey)
	t.Cleanup(func() { stopOpenAIImageLiveGateway(cmd) })
	waitForOpenAIImageLiveGateway(t, addr)

	requestBody := []byte(`{"model":"gpt-image-1","prompt":"a tiny solid red circle centered on a white background","size":"1024x1024","n":1}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/v1/images/generations", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatalf("构造 OpenAI 图片请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+seed.bearer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "openai-image-live-"+uuid.NewString())

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
}

type openAIImageLiveSeed struct {
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
}

func seedOpenAIImageLiveGraph(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, upstreamKey string) *openAIImageLiveSeed {
	t.Helper()
	unique := uuid.NewString()
	seed := &openAIImageLiveSeed{pricingVersion: "e2e-openai-image-live-" + unique}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"openai-image-live-e2e-tenant-"+unique,
	).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	registerOpenAIImageLiveCleanup(t, pgPool, seed)

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "openai-image-live-e2e-user-"+unique,
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
		seed.tenantID, seed.userID, "openai-image-live-e2e-key-"+unique, string(keyHash), keyPrefix,
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
		seed.pricingVersion, openAIImageLivePricingData, "e2e:openai-image-live",
	); err != nil {
		t.Fatalf("seed billing pricing version: %v", err)
	}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		seed.tenantID, openAIImageLiveVendor, "openai image live e2e "+unique, openAIImageLiveProtocol,
	).Scan(&seed.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "openai-image-live-e2e-pg-"+unique,
	).Scan(&seed.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		seed.tenantID, seed.poolGroupID, "openai-image-live-e2e-ch-"+unique,
	).Scan(&seed.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	seed.providerAccountID = seedOpenAIImageLiveProviderAccount(t, ctx, pgPool, seed, upstreamKey, unique)

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO models (tenant_id, scope, canonical_id, protocol_family,
		                     default_provider_model_id, default_context_window, status)
		 VALUES ($1, 'tenant', $2, $3, $4, 128000, 'active')
		 RETURNING id`,
		seed.tenantID, openAIImageLiveModel, openAIImageLiveProtocol, openAIImageLiveModel,
	).Scan(&seed.modelID); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO model_aliases (tenant_id, scope, model_id,
		                            public_alias_normalized, public_alias_display, status)
		 VALUES ($1, 'tenant', $2, $3, $4, 'active')
		 RETURNING id`,
		seed.tenantID, seed.modelID, openAIImageLiveModel, openAIImageLiveModel,
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

func seedOpenAIImageLiveProviderAccount(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *openAIImageLiveSeed, upstreamKey, unique string) int64 {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"api_key": upstreamKey})
	if err != nil {
		t.Fatalf("序列化 OpenAI API key 凭据: %v", err)
	}

	var accountID int64
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type,
			cap_concurrency, in_flight_count, priority, health_state, credential_state,
			model_allow_list, capability_flags, credentials, extra
		) VALUES ($1, $2, $3, $4, $5,
			1, 0, 100, 'healthy', 'valid',
			ARRAY[$6]::text[], ARRAY['image_output']::text[], $7::jsonb, '{}'::jsonb) RETURNING id`,
		seed.tenantID, seed.providerID, seed.channelID, "openai-image-live-e2e-acct-"+unique,
		openAIImageLiveAuthMode, openAIImageLiveModel, string(payload),
	).Scan(&accountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}

	credKP, err := credentialstore.NewStaticKeyProvider("local-v1", make([]byte, 32))
	if err != nil {
		t.Fatalf("创建凭据密钥提供器: %v", err)
	}
	credEnv, err := credentialstore.NewCipher(credKP).Encrypt(ctx,
		payload,
		credentialstore.AAD{
			TenantID:          seed.tenantID,
			ProviderAccountID: accountID,
			Vendor:            openAIImageLiveVendor,
			AuthMode:          openAIImageLiveAuthMode,
			Version:           1,
		})
	if err != nil {
		t.Fatalf("加密 provider account %d 的凭据: %v", accountID, err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO account_credentials (tenant_id, provider_account_id, vendor, auth_mode, state,
		   credential_version, encrypted_payload, encryption_scheme, key_id, nonce, aad_hash)
		 VALUES ($1, $2, $3, $4, 'active', 1, $5, 'aes-256-gcm', $6, $7, $8)`,
		seed.tenantID, accountID, openAIImageLiveVendor, openAIImageLiveAuthMode,
		credEnv.Ciphertext, credEnv.KeyID, credEnv.Nonce, credEnv.AADHash,
	); err != nil {
		t.Fatalf("seed account credential: %v", err)
	}
	return accountID
}

func registerOpenAIImageLiveCleanup(t *testing.T, pgPool *pgxpool.Pool, seed *openAIImageLiveSeed) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// 成功请求的账务记录是 append-only，可能阻止删除账号；先清除 legacy 明文 key。
		if _, err := pgPool.Exec(ctx,
			`UPDATE provider_accounts SET credentials='{}'::jsonb WHERE tenant_id=$1`,
			seed.tenantID,
		); err != nil {
			t.Errorf("清除 OpenAI 图片 live e2e legacy 明文凭据: %v", err)
		}

		_, _ = pgPool.Exec(ctx, `DELETE FROM channel_health_admin_alerts WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM channel_health_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM channel_health_state WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM credential_acquisition_flow_sessions WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM credential_audit_events WHERE tenant_id=$1`, seed.tenantID)
		if _, err := pgPool.Exec(ctx, `DELETE FROM account_credentials WHERE tenant_id=$1`, seed.tenantID); err != nil {
			t.Errorf("删除 OpenAI 图片 live e2e 加密凭据: %v", err)
		}
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_concurrency_slots WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_concurrency_scope_locks WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_reconciliation_jobs WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_reservations WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_windows WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_policies WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM idempotency_replay_records WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM usage_records WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM billing_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM balance_holds WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM model_pool_bindings WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM model_registry_capabilities WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM model_aliases WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM models WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM model_registry_snapshots WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM model_registry_tenant_policies WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM provider_accounts WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM channels WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM pool_groups WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM providers WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM user_balances WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM api_keys WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM billing_pricing_versions WHERE tenant_id=0 AND version=$1`, seed.pricingVersion)
	})
}

func assertOpenAIImageLiveSeedSelectable(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *openAIImageLiveSeed) {
	t.Helper()
	resolved, err := registry.NewPostgresRegistry(pgPool, nil).ResolveModel(ctx, openAIImageLiveModel, seed.tenantID)
	if err != nil {
		t.Fatalf("seed registry resolve %q: %v", openAIImageLiveModel, err)
	}
	if resolved.ProtocolFamily != openAIImageLiveProtocol {
		t.Fatalf("resolved protocol_family=%q want %q", resolved.ProtocolFamily, openAIImageLiveProtocol)
	}
	if len(resolved.PoolCandidates) != 1 || resolved.PoolCandidates[0] != seed.poolGroupID {
		t.Fatalf("resolved pool candidates=%v want [%d]", resolved.PoolCandidates, seed.poolGroupID)
	}
	rows, err := dbbilling.New(pgPool).ListEligibleAccountsByPoolGroup(ctx, dbbilling.ListEligibleAccountsByPoolGroupParams{
		TenantID:                seed.tenantID,
		PoolGroupID:             seed.poolGroupID,
		RequestedModel:          openAIImageLiveModel,
		RequestedProtocolFamily: resolved.ProtocolFamily,
		RequiredCapabilities:    []string{"image_output"},
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

func buildOpenAIImageLiveGateway(t *testing.T) string {
	t.Helper()
	moduleRoot := goModuleRootForOpenAIImageLive(t)
	binPath := filepath.Join(moduleRoot, openAIImageLiveBinaryName)
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

func startOpenAIImageLiveGateway(t *testing.T, binPath, dsn, addr string, seed *openAIImageLiveSeed, upstreamKey string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"HUAKAI_DATABASE_URL="+dsn,
		"HUAKAI_ADDR="+addr,
		"HUAKAI_RELEASE_MODE=dev",
		"HUAKAI_CREDENTIAL_KEY_B64=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"HUAKAI_SESSION_SIGNING_KEY_B64=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"HUAKAI_DEV_MOCK_UPSTREAM=false",
		"HUAKAI_BILLING_POLICY_VERSION="+seed.pricingVersion,
		"HUAKAI_QUOTA_ENFORCE=true",
		"HUAKAI_CACHE_L2_ENABLED=0",
		"HUAKAI_EVENTBUS_ENABLED=0",
		"HUAKAI_RATE_PRECHECK_ENABLED=false",
		"HUAKAI_BINDING_RATE_LIMIT_ENABLED=false",
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
		t.Fatalf("启动 gateway: %v", err)
	}
	go drainOpenAIImageLivePipe("gateway-stderr", stderr, upstreamKey, seed.bearer)
	go drainOpenAIImageLivePipe("gateway-stdout", stdout, upstreamKey, seed.bearer)
	return cmd
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

func stopOpenAIImageLiveGateway(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
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
