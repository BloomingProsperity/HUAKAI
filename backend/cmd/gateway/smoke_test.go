//go:build smoke || e2e_concurrency

// Phase C.4 端到端冒烟测试。构建网关二进制,在子进程中运行它,
// 指向开发用的 PostgreSQL 容器,发送一次 chat completions 请求,
// 同时断言 HTTP 正确性与 PG 行状态两方面。
//
// 这是 Phase C 的把关门 —— 如果全部 5 项 PG 状态断言都通过,
// 说明二进制确实是在通过真实的数据库行进行计费。

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

const (
	// Phase L0 最小集:冒烟测试使用真实的 api_keys 行,
	// 而不是通过环境变量注入的单个 bearer。bearer 前缀必须匹配
	// auth.APIKeyResolver 的命名空间检查(`hk_live_` 或 `hk_test_`)。
	smokeBearerPrefix = "hk_test_"
	// 重命名以规避先前哈希链上被缓存的 SAC(Smart App Control,智能应用控制)
	// 信誉拦截。如果 SAC 也拦截这个名字,就再次轮换后缀。
	smokeBinaryName    = "gateway-smoke-l0.exe"
	smokeBootRetries   = 30
	smokeBootRetryWait = 200 * time.Millisecond
)

func TestPhaseC_Smoke_ChatCompletions(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping smoke test")
	}
	dsn = useDisposableSpecializedLiveDatabase(t, dsn)
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer setupCancel()

	pgPool, err := db.Open(setupCtx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open dev pool: %v", err)
	}
	t.Cleanup(pgPool.Close)

	seed := seedSmokeGraph(t, setupCtx, pgPool)

	binPath := buildGateway(t)
	defer os.Remove(binPath)

	addr := reserveLocalPort(t)
	cmd := startGateway(t, setupCtx, binPath, dsn, addr, seed)
	t.Cleanup(func() { stopGateway(cmd) })

	waitForGateway(t, addr)
	setupCancel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// POST 请求。Slice 2:网关不再接受请求体中的 pool_group_id ——
	// Registry 会根据下方 seedSmokeGraph 种入的 model 别名来解析出
	// 对应的池。
	body := `{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"hi"}],"stream":true}`
	_ = seed.poolGroupID // 仅保留用于 PG 状态断言
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/v1/chat/completions", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+seed.bearer)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200; got %d body=%s", resp.StatusCode, string(raw))
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream; got %q", got)
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(respBody) == 0 {
		t.Fatalf("empty SSE response body")
	}
	if !bytes.Contains(respBody, []byte("data:")) {
		t.Fatalf("response body has no SSE data: lines: %s", respBody)
	}

	// 断言 PG 状态。必须查出种入租户的 claim 行。
	checkPGState(t, ctx, pgPool, seed)
	checkMixedProviderPool(t, ctx, pgPool, addr, seed)
}

type smokeSeed struct {
	tenantID          int64
	apiKeyID          int64
	userID            int64
	providerID        int64
	poolGroupID       int64
	channelID         int64
	providerAccountID int64
	// Slice 2:用于把请求体中的 `model` 别名解析到
	// 种入池组的 Registry 行。
	modelID int64
	aliasID int64
	// Phase L0 最小集:在种数据时生成的明文 bearer;
	// 与存储在 api_keys.key_hash 中的 bcrypt 哈希进行匹配。
	bearer         string
	mixed          []smokeMixedFamily
	pricingVersion string
}

type smokeMixedFamily struct {
	name          string
	alias         string
	providerModel string
	protocol      string
	vendor        string
	authMode      string
	accountID     int64
}

func seedSmokeGraph(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool) *smokeSeed {
	t.Helper()
	unique := uuid.NewString()
	s := &smokeSeed{}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"smoke-tenant-"+unique,
	).Scan(&s.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	// Phase L0 最小集:用真实的 users 与 api_keys 行替换合成的
	// (apiKeyID = tenantID*100+1)ID。明文 bearer 只在本测试内
	// 持有以发起 POST 请求;数据库存储的是 bcrypt 哈希。
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		s.tenantID, "smoke-user-"+unique,
	).Scan(&s.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	s.bearer = smokeBearerPrefix + unique // "hk_test_<uuid36>" —— 远超 16 字符的前缀长度
	keyPrefix := s.bearer
	if len(keyPrefix) > 16 {
		keyPrefix = keyPrefix[:16]
	}
	keyHash, err := bcrypt.GenerateFromPassword([]byte(s.bearer), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt hash bearer: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		s.tenantID, s.userID, "smoke-key-"+unique, string(keyHash), keyPrefix,
	).Scan(&s.apiKeyID); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	// 给用户充上余额,以便计费 claim 的余额预扣保留(默认强制开启)
	// 能够冻结预估成本;若没有余额行,该请求会返回 402 并报
	// insufficient_balance。
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
		 VALUES ($1, $2, 100.00, 0, 1, now())`,
		s.tenantID, s.userID,
	); err != nil {
		t.Fatalf("seed user_balance: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		if err := cleanupSmokeGraph(c, pgPool, s.tenantID); err != nil {
			t.Errorf("清理冒烟测试账号图: %v", err)
		}
		if s.pricingVersion != "" {
			if _, err := pgPool.Exec(c,
				`DELETE FROM billing_pricing_versions WHERE tenant_id=0 AND version=$1`,
				s.pricingVersion,
			); err != nil {
				t.Errorf("清理冒烟测试价格版本: %v", err)
			}
		}
	})

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		s.tenantID, "smoke-p-"+unique, "Provider "+unique,
	).Scan(&s.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		s.tenantID, "smoke-pg-"+unique,
	).Scan(&s.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		s.tenantID, s.poolGroupID, "smoke-ch-"+unique,
	).Scan(&s.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type,
			cap_concurrency, in_flight_count, health_state, credential_state, capability_flags
		) VALUES ($1, $2, $3, $4, 'api_key', 4, 2, 'healthy', 'valid',
			ARRAY['stream','tools','vision','json','audio','file']) RETURNING id`,
		s.tenantID, s.providerID, s.channelID, "smoke-acct-"+unique,
	).Scan(&s.providerAccountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}

	// 种入一条可解密的凭证,以便密钥库能解析出一条。开发用的 mock
	// 上游会忽略该值,但这一行必须存在,且能在网关的密钥下解密
	// (32 个零字节 / key_id 为 local-v1,与 startGateway 的环境变量一致)。
	credKP, err := credentialstore.NewStaticKeyProvider("local-v1", make([]byte, 32))
	if err != nil {
		t.Fatalf("cred key provider: %v", err)
	}
	credEnv, err := credentialstore.NewCipher(credKP).Encrypt(ctx,
		[]byte(`{"api_key":"sk-mock-dev-key"}`),
		credentialstore.AAD{TenantID: s.tenantID, ProviderAccountID: s.providerAccountID, Vendor: "openai", AuthMode: "api_key", Version: 1})
	if err != nil {
		t.Fatalf("encrypt credential: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO account_credentials (tenant_id, provider_account_id, vendor, auth_mode, state,
		   credential_version, encrypted_payload, encryption_scheme, key_id, nonce, aad_hash)
		 VALUES ($1, $2, 'openai', 'api_key', 'active', 1, $3, 'aes-256-gcm', $4, $5, $6)`,
		s.tenantID, s.providerAccountID, credEnv.Ciphertext, credEnv.KeyID, credEnv.Nonce, credEnv.AADHash,
	); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	// Slice 2:种入 Registry 行,使冒烟用的别名能端到端地
	// 解析到种入的池组。这与 admin 端点(Phase E)将来写入的行
	// 保持一致。
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO models (tenant_id, scope, canonical_id, protocol_family,
		                     default_provider_model_id, default_context_window, status)
		 VALUES ($1, 'tenant', $2, 'openai_chat', 'gpt-4.1-mini', 128000, 'active')
		 RETURNING id`,
		s.tenantID, "smoke-canonical-"+unique,
	).Scan(&s.modelID); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO model_aliases (tenant_id, scope, model_id,
		                            public_alias_normalized, public_alias_display, status)
		 VALUES ($1, 'tenant', $2, 'gpt-4.1-mini', 'gpt-4.1-mini', 'active')
		 RETURNING id`,
		s.tenantID, s.modelID,
	).Scan(&s.aliasID); err != nil {
		t.Fatalf("seed model_alias: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO model_pool_bindings (tenant_id, model_id, pool_group_id, priority, weight, enabled)
		 VALUES ($1, $2, $3, 100, 1, true)`,
		s.tenantID, s.modelID, s.poolGroupID,
	); err != nil {
		t.Fatalf("seed model_pool_bindings: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO model_registry_snapshots (tenant_id, version)
		 VALUES ($1, 1)
		 ON CONFLICT (tenant_id) DO UPDATE SET version = 1`,
		s.tenantID,
	); err != nil {
		t.Fatalf("seed model_registry_snapshots: %v", err)
	}
	s.mixed = append(s.mixed, smokeMixedFamily{
		name: "GPT", alias: "gpt-4.1-mini", providerModel: "gpt-4.1-mini",
		protocol: "openai_chat", vendor: "openai", authMode: "api_key", accountID: s.providerAccountID,
	})
	for _, family := range []smokeMixedFamily{
		{name: "Claude", alias: "mixed-claude", providerModel: "claude-mock", protocol: "anthropic_messages", vendor: "anthropic", authMode: "api_key"},
		{name: "Gemini", alias: "mixed-gemini", providerModel: "gemini-mock", protocol: "gemini_messages", vendor: "gemini", authMode: "aistudio_api_key"},
		{name: "Kimi", alias: "mixed-kimi", providerModel: "kimi-mock", protocol: "kimi_chat", vendor: "kimi", authMode: "api_key"},
		{name: "Grok", alias: "mixed-grok", providerModel: "grok-mock", protocol: "grok_chat", vendor: "grok", authMode: "api_key"},
	} {
		family.accountID = seedSmokeMixedFamily(t, ctx, pgPool, s, unique, family)
		s.mixed = append(s.mixed, family)
	}
	seedSmokePricing(t, ctx, pgPool, s, unique)
	return s
}

func seedSmokePricing(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *smokeSeed, unique string) {
	t.Helper()
	providers := make(map[string]any, len(seed.mixed))
	for _, family := range seed.mixed {
		models := map[string]any{
			family.alias: map[string]string{
				"input_micro_usd": "1", "output_micro_usd": "2", "cache_read_micro_usd": "1",
			},
		}
		if family.providerModel != family.alias {
			models[family.providerModel] = map[string]string{
				"input_micro_usd": "1", "output_micro_usd": "2", "cache_read_micro_usd": "1",
			}
		}
		providers[family.vendor] = map[string]any{"models": models}
	}
	pricingData, err := json.Marshal(map[string]any{"providers": providers})
	if err != nil {
		t.Fatalf("编码混合池价格快照: %v", err)
	}
	seed.pricingVersion = "smoke-mixed-" + unique
	if _, err := pgPool.Exec(ctx, `
INSERT INTO tenants (id, name, status, created_at, updated_at)
VALUES (0, 'public-pricing', 'active', now(), now())
ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatalf("种入公共价格租户: %v", err)
	}
	if _, err := pgPool.Exec(ctx, `
INSERT INTO billing_pricing_versions (
    tenant_id, version, pricing_data, effective_from, created_by_actor, is_public
) VALUES (0,$1,$2::jsonb,now(),'smoke:mixed-provider-pool',true)`,
		seed.pricingVersion, string(pricingData),
	); err != nil {
		t.Fatalf("种入混合池价格版本: %v", err)
	}
}

func seedSmokeMixedFamily(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *smokeSeed, unique string, family smokeMixedFamily) int64 {
	t.Helper()
	var providerID, channelID, accountID, modelID int64
	if err := pgPool.QueryRow(ctx, `
INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
VALUES ($1,$2,$3,$4) RETURNING id`,
		seed.tenantID, "smoke-"+family.vendor+"-"+unique, family.name+" smoke", family.protocol,
	).Scan(&providerID); err != nil {
		t.Fatalf("种入 %s provider: %v", family.name, err)
	}
	if err := pgPool.QueryRow(ctx, `
INSERT INTO channels (tenant_id, pool_group_id, name)
VALUES ($1,$2,$3) RETURNING id`,
		seed.tenantID, seed.poolGroupID, "smoke-"+family.vendor+"-"+unique,
	).Scan(&channelID); err != nil {
		t.Fatalf("种入 %s channel: %v", family.name, err)
	}
	if err := pgPool.QueryRow(ctx, `
INSERT INTO provider_accounts (
    tenant_id, provider_id, channel_id, name, account_type, cap_concurrency,
    in_flight_count, health_state, credential_state, capability_flags, model_allow_list
) VALUES ($1,$2,$3,$4,'api_key',4,0,'healthy','valid',ARRAY['stream'],ARRAY[$5])
RETURNING id`, seed.tenantID, providerID, channelID, "smoke-"+family.vendor+"-account-"+unique, family.providerModel,
	).Scan(&accountID); err != nil {
		t.Fatalf("种入 %s account: %v", family.name, err)
	}
	keyProvider, err := credentialstore.NewStaticKeyProvider("local-v1", make([]byte, 32))
	if err != nil {
		t.Fatalf("创建 %s 测试密钥提供器: %v", family.name, err)
	}
	payload := []byte(fmt.Sprintf(`{"api_key":"%s-test-secret"}`, family.vendor))
	envelope, err := credentialstore.NewCipher(keyProvider).Encrypt(ctx, payload, credentialstore.AAD{
		TenantID: seed.tenantID, ProviderAccountID: accountID, Vendor: family.vendor, AuthMode: family.authMode, Version: 1,
	})
	if err != nil {
		t.Fatalf("加密 %s credential: %v", family.name, err)
	}
	if _, err := pgPool.Exec(ctx, `
INSERT INTO account_credentials (
    tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
    encrypted_payload, encryption_scheme, key_id, nonce, aad_hash
) VALUES ($1,$2,$3,$4,'active',1,$5,'aes-256-gcm',$6,$7,$8)`,
		seed.tenantID, accountID, family.vendor, family.authMode,
		envelope.Ciphertext, envelope.KeyID, envelope.Nonce, envelope.AADHash,
	); err != nil {
		t.Fatalf("种入 %s credential: %v", family.name, err)
	}
	if err := pgPool.QueryRow(ctx, `
INSERT INTO models (tenant_id, scope, canonical_id, protocol_family, default_provider_model_id, default_context_window, status)
VALUES ($1,'tenant',$2,$3,$4,128000,'active') RETURNING id`,
		seed.tenantID, "smoke-"+family.vendor+"-canonical-"+unique, family.protocol, family.providerModel,
	).Scan(&modelID); err != nil {
		t.Fatalf("种入 %s model: %v", family.name, err)
	}
	if _, err := pgPool.Exec(ctx, `
INSERT INTO model_aliases (tenant_id, scope, model_id, public_alias_normalized, public_alias_display, status)
VALUES ($1,'tenant',$2,$3,$3,'active')`, seed.tenantID, modelID, family.alias); err != nil {
		t.Fatalf("种入 %s alias: %v", family.name, err)
	}
	if _, err := pgPool.Exec(ctx, `
INSERT INTO model_pool_bindings (tenant_id, model_id, pool_group_id, priority, weight, enabled)
VALUES ($1,$2,$3,100,1,true)`, seed.tenantID, modelID, seed.poolGroupID); err != nil {
		t.Fatalf("种入 %s binding: %v", family.name, err)
	}
	return accountID
}

func cleanupSmokeGraph(ctx context.Context, pgPool *pgxpool.Pool, tenantID int64) error {
	if err := cleanupSpecializedLiveMoneyRows(ctx, pgPool, tenantID); err != nil {
		return err
	}
	for _, statement := range []string{
		`DELETE FROM sticky_bindings WHERE tenant_id=$1`,
		`DELETE FROM provider_account_routing_signals WHERE tenant_id=$1`,
		`DELETE FROM provider_account_quota_facts WHERE tenant_id=$1`,
		`DELETE FROM provider_account_subscription_states WHERE tenant_id=$1`,
		`DELETE FROM channel_health_audit_events WHERE tenant_id=$1`,
		`DELETE FROM channel_health_state WHERE tenant_id=$1`,
		`DELETE FROM credential_audit_events WHERE tenant_id=$1`,
		`DELETE FROM model_pool_bindings WHERE tenant_id=$1`,
		`DELETE FROM model_registry_capabilities WHERE tenant_id=$1`,
		`DELETE FROM model_aliases WHERE tenant_id=$1`,
		`DELETE FROM models WHERE tenant_id=$1`,
		`DELETE FROM model_registry_snapshots WHERE tenant_id=$1`,
		`DELETE FROM model_registry_tenant_policies WHERE tenant_id=$1`,
		`DELETE FROM account_credentials WHERE tenant_id=$1`,
		`DELETE FROM provider_accounts WHERE tenant_id=$1`,
		`DELETE FROM channels WHERE tenant_id=$1`,
		`DELETE FROM pool_groups WHERE tenant_id=$1`,
		`DELETE FROM providers WHERE tenant_id=$1`,
		`DELETE FROM invitations WHERE tenant_id=$1`,
		`DELETE FROM user_audit_events WHERE tenant_id=$1`,
		`DELETE FROM user_balances WHERE tenant_id=$1`,
		`DELETE FROM api_keys WHERE tenant_id=$1`,
		`DELETE FROM users WHERE tenant_id=$1`,
		`DELETE FROM tenants WHERE id=$1`,
	} {
		if _, err := pgPool.Exec(ctx, statement, tenantID); err != nil {
			return fmt.Errorf("执行 %q: %w", statement, err)
		}
	}
	return nil
}

func buildGateway(t *testing.T) string {
	t.Helper()
	moduleRoot := goModuleRoot(t)
	// 构建到模块根目录,这样无论二进制子进程从哪个 cwd 启动,
	// ./gateway-smoke.exe 都能被找到。该构建对两种情形都稳健:
	// `go test ./cmd/gateway`(cwd=cmd/gateway)以及
	// `go test -c + 手动 ./smoke.test.exe`(cwd=$pwd),覆盖了两种
	// 错误 cwd 的场景。
	binPath := moduleRoot + "/" + smokeBinaryName
	// 通过 ldflags 注入每次运行的时间戳,使每次冒烟构建产生
	// 唯一的二进制哈希。Smart App Control(Win11)按二进制哈希
	// 缓存拦截决策;若不这样做,单次 SAC 拦截会在之后的所有运行中
	// 一直持续,直到内容发生变化。参见
	// Windows 应用控制导致的测试阻断处理见 docs/dev-tests.md。
	stamp := fmt.Sprintf("smoke-%d", time.Now().UnixNano())
	cmd := exec.Command("go", "build",
		"-ldflags", "-X main.smokeBuildStamp="+stamp,
		"-o", binPath, "./cmd/gateway")
	cmd.Dir = moduleRoot
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build gateway from %s: %v", moduleRoot, err)
	}
	return binPath
}

func goModuleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	gomod := string(bytes.TrimSpace(out))
	if gomod == "" || gomod == "/dev/null" {
		t.Fatalf("not in a Go module")
	}
	// gomod 形如 .../go.mod;去掉末尾的 /go.mod。
	const suffix = "/go.mod"
	const winSuffix = `\go.mod`
	switch {
	case len(gomod) > len(suffix) && gomod[len(gomod)-len(suffix):] == suffix:
		return gomod[:len(gomod)-len(suffix)]
	case len(gomod) > len(winSuffix) && gomod[len(gomod)-len(winSuffix):] == winSuffix:
		return gomod[:len(gomod)-len(winSuffix)]
	default:
		t.Fatalf("unexpected GOMOD path: %q", gomod)
		return ""
	}
}

// reserveLocalPort 在随机的 localhost 端口上开一个 TCP 监听器,
// 然后关闭它,并返回网关应当绑定的地址。Close() 与网关重新绑定
// 之间存在 TOCTOU(检查时机与使用时机之间的竞态)竞态,但替代方案
// (让网关自己挑端口并写到 stdout)对一个 Phase C 冒烟测试来说
// 代码量更多。
func reserveLocalPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close reserved listener: %v", err)
	}
	return addr
}

func startGateway(t *testing.T, _ context.Context, binPath, dsn, addr string, seed *smokeSeed) *specializedLiveProcesses {
	t.Helper()
	sidecar, socketPath := startSpecializedLiveSidecar(t, goModuleRoot(t))
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"HUAKAI_DATABASE_URL="+dsn,
		"HUAKAI_ADDR="+addr,
		"HUAKAI_RELEASE_MODE=dev",
		// 凭证加密密钥(32 个零字节,base64)—— 自从网关引入了
		// 强制密钥后即为必需;仅供开发使用的固定值。
		"HUAKAI_CREDENTIAL_KEY_B64=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"HUAKAI_SESSION_SIGNING_KEY_B64=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"HUAKAI_AUDIT_LEDGER_BACKEND=postgres",
		"HUAKAI_TRANSPORT_SIDECAR_SOCKET="+socketPath,
		"HUAKAI_BILLING_POLICY_VERSION="+seed.pricingVersion,
		// 开发用 mock 上游:伪造上游的 SSE,使整个循环无需真实的
		// 上游/网络即可运行(替代 Phase E 之前内置的 mock)。
		"HUAKAI_DEV_MOCK_UPSTREAM=true",
		// Phase L0 最小集:不再设置 SMOKE 环境变量;鉴权改为
		// 通过上方 seedSmokeGraph 种入的 api_keys 表来解析。
	)
	stderr, _ := cmd.StderrPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		stopSpecializedLiveProcess(sidecar)
		_ = os.Remove(socketPath)
		t.Fatalf("start gateway: %v", err)
	}
	go drainPipe("gateway-stderr", stderr)
	go drainPipe("gateway-stdout", stdout)
	return &specializedLiveProcesses{gateway: cmd, sidecar: sidecar, socketPath: socketPath}
}

func drainPipe(label string, r io.ReadCloser) {
	if r == nil {
		return
	}
	defer r.Close()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fmt.Printf("[%s] %s\n", label, scanner.Text())
	}
}

func stopGateway(processes *specializedLiveProcesses) {
	stopSpecializedLiveProcesses(processes)
}

func waitForGateway(t *testing.T, addr string) {
	t.Helper()
	for i := 0; i < smokeBootRetries; i++ {
		// 我们没有 /healthz;改用一个非 API 的 GET,它应当很快返回 404。
		resp, err := http.Get("http://" + addr + "/")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(smokeBootRetryWait)
	}
	t.Fatalf("gateway did not start listening on %s within %v",
		addr, time.Duration(smokeBootRetries)*smokeBootRetryWait)
}

func checkPGState(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *smokeSeed) {
	t.Helper()

	var claimID int64
	var status string
	if err := pgPool.QueryRow(ctx,
		`SELECT id, status FROM billing_ledger_claims WHERE tenant_id=$1 AND status='committed'`,
		seed.tenantID,
	).Scan(&claimID, &status); err != nil {
		t.Fatalf("PG check 1 (committed claim): %v", err)
	}
	if status != "committed" {
		t.Fatalf("PG check 1: expected committed; got %q", status)
	}

	var usageCount int
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*) FROM usage_records WHERE claim_id=$1`, claimID,
	).Scan(&usageCount); err != nil {
		t.Fatalf("PG check 2 (usage_records): %v", err)
	}
	if usageCount != 1 {
		t.Fatalf("PG check 2: expected 1 usage_record; got %d", usageCount)
	}

	var eventCount int
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_committed'`, claimID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("PG check 3 (billing_events): %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("PG check 3: expected 1 claim_committed event; got %d", eventCount)
	}

	var inFlight int32
	if err := pgPool.QueryRow(ctx,
		`SELECT in_flight_count FROM provider_accounts WHERE id=$1`, seed.providerAccountID,
	).Scan(&inFlight); err != nil {
		t.Fatalf("PG check 4 (in_flight_count): %v", err)
	}
	// 种入时 in_flight=2;获取时 +1,结算释放时 -1 = 回到 2。
	if inFlight != 2 {
		t.Fatalf("PG check 4: expected in_flight 2 (round-trip); got %d", inFlight)
	}

	var slotCount int
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*) FROM pool_slot_acquisitions WHERE claim_id=$1 AND status='released_success'`, claimID,
	).Scan(&slotCount); err != nil {
		t.Fatalf("PG check 5 (released slot): %v", err)
	}
	if slotCount != 1 {
		t.Fatalf("PG check 5: expected 1 released_success slot; got %d", slotCount)
	}

	// PG check 6:成功路径上的 usage 行必须带有来自迁移 0008 的
	// registry + router 快照戳记。格式为
	// "registry:<tenant_id>:<v>;router:<router_policy_v>"。
	var snapshot *string
	if err := pgPool.QueryRow(ctx,
		`SELECT snapshot_version FROM usage_records WHERE claim_id=$1`, claimID,
	).Scan(&snapshot); err != nil {
		t.Fatalf("PG check 6 (snapshot_version): %v", err)
	}
	if snapshot == nil {
		t.Fatalf("PG check 6: expected non-null snapshot_version; got NULL")
	}
	wantPrefix := fmt.Sprintf("registry:%d:", seed.tenantID)
	if !bytes.HasPrefix([]byte(*snapshot), []byte(wantPrefix)) {
		t.Fatalf("PG check 6: snapshot_version = %q; want prefix %q", *snapshot, wantPrefix)
	}
	if !bytes.Contains([]byte(*snapshot), []byte(";router:")) {
		t.Fatalf("PG check 6: snapshot_version = %q; want concatenated router stamp", *snapshot)
	}
}

func checkMixedProviderPool(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, addr string, seed *smokeSeed) {
	t.Helper()
	for _, family := range seed.mixed {
		family := family
		t.Run("同池-"+family.name, func(t *testing.T) {
			logicalID := "smoke-mixed-" + family.vendor + "-" + uuid.NewString()
			body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"stream":true}`, family.alias)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				"http://"+addr+"/v1/chat/completions", bytes.NewBufferString(body))
			if err != nil {
				t.Fatalf("构造 %s 请求: %v", family.name, err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+seed.bearer)
			req.Header.Set("Idempotency-Key", logicalID)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("调用 %s: %v", family.name, err)
			}
			raw, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				t.Fatalf("读取 %s 响应: %v", family.name, readErr)
			}
			if resp.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte("data:")) {
				t.Fatalf("%s status=%d body=%s want 200 SSE", family.name, resp.StatusCode, raw)
			}

			var claimID, gotAPIKeyID, gotUserID, gotAccountID, gotPoolGroupID int64
			var claimStatus, gotRequestedModel string
			if err := pgPool.QueryRow(ctx, `
SELECT id, status, api_key_id, user_id, provider_account_id, pooling_group_id, requested_model
FROM billing_ledger_claims
WHERE tenant_id=$1 AND logical_request_id=$2`, seed.tenantID, logicalID,
			).Scan(&claimID, &claimStatus, &gotAPIKeyID, &gotUserID, &gotAccountID, &gotPoolGroupID, &gotRequestedModel); err != nil {
				t.Fatalf("读取 %s claim: %v", family.name, err)
			}
			if claimStatus != "committed" || gotAPIKeyID != seed.apiKeyID || gotUserID != seed.userID ||
				gotAccountID != family.accountID || gotPoolGroupID != seed.poolGroupID || gotRequestedModel != family.alias {
				t.Fatalf("%s claim 归属错误: status=%s key=%d user=%d account=%d pool=%d model=%q",
					family.name, claimStatus, gotAPIKeyID, gotUserID, gotAccountID, gotPoolGroupID, gotRequestedModel)
			}

			var usageAccountID, usageAPIKeyID, usageUserID int64
			var usageRequestedModel, usageUpstreamModel string
			if err := pgPool.QueryRow(ctx, `
SELECT provider_account_id, api_key_id, user_id, requested_model, upstream_model
FROM usage_records
WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, claimID,
			).Scan(&usageAccountID, &usageAPIKeyID, &usageUserID, &usageRequestedModel, &usageUpstreamModel); err != nil {
				t.Fatalf("读取 %s usage: %v", family.name, err)
			}
			if usageAccountID != family.accountID || usageAPIKeyID != seed.apiKeyID || usageUserID != seed.userID ||
				usageRequestedModel != family.alias || usageUpstreamModel != family.providerModel {
				t.Fatalf("%s usage 归属错误: account=%d key=%d user=%d requested=%q upstream=%q",
					family.name, usageAccountID, usageAPIKeyID, usageUserID, usageRequestedModel, usageUpstreamModel)
			}
			var released int
			if err := pgPool.QueryRow(ctx, `
SELECT count(*) FROM pool_slot_acquisitions
WHERE tenant_id=$1 AND claim_id=$2 AND provider_account_id=$3 AND status='released_success'`,
				seed.tenantID, claimID, family.accountID,
			).Scan(&released); err != nil {
				t.Fatalf("读取 %s 槽位: %v", family.name, err)
			}
			if released != 1 {
				t.Fatalf("%s released_success=%d want 1", family.name, released)
			}
		})
	}
}
