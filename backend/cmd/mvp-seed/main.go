// 命令 mvp-seed 为本地 MVP 演示预置一个完整且持久的路由目标:一个 tenant +
// user(+ balance)+ API key,以及一个 chat 请求解析与路由所需的完整
// provider → channel → provider_account → pool_group → model → alias →
// model_pool_binding → registry snapshot 链条。与 smoke 测试的临时种子不同,
// 它**不会**做清理,并且会打印明文 hk_ key,以便人工(或前端)调用网关。
// 对 tenant 名称幂等:重跑会复用演示 tenant。
//
// 运行:HUAKAI_DATABASE_URL=... go run ./cmd/mvp-seed
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/BloomingProsperity/HUAKAI/internal/apikeyns"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const (
	demoTenantName = "mvp-demo"
	demoAlias      = "gpt-4.1-mini"
	demoBalance    = "100.00"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mvp-seed:", err)
		os.Exit(1)
	}
}

func run() error {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("HUAKAI_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	unique := uuid.NewString()[:8]

	// 每次运行都是一个全新隔离的 tenant,这样重跑时 alias/model/binding 永远不会
	// 冲突。(后续前端集成改为在注册 tenant 下预置路由目标。)
	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`, demoTenantName+"-"+unique).Scan(&tenantID); err != nil {
		return fmt.Errorf("tenant: %w", err)
	}

	var userID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "mvp-demo-seed-"+unique).Scan(&userID); err != nil {
		return fmt.Errorf("user: %w", err)
	}

	// 用配置前缀同源签发,免得运维设了 HUAKAI_API_KEY_PREFIX 后 seed 出的 key 被
	// 入站校验拒掉(与 admin/keygen、auth/resolver 同一真相源)。
	bearer := apikeyns.TestPrefix() + uuid.NewString()
	keyPrefix := bearer
	if len(keyPrefix) > 16 {
		keyPrefix = keyPrefix[:16]
	}
	keyHash, err := bcrypt.GenerateFromPassword([]byte(bearer), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash key: %w", err)
	}
	var apiKeyID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		tenantID, userID, "mvp-seed-key-"+unique, string(keyHash), keyPrefix).Scan(&apiKeyID); err != nil {
		return fmt.Errorf("api_key: %w", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
		 VALUES ($1, $2, $3, 0, 1, now())
		 ON CONFLICT (tenant_id, user_id) DO UPDATE SET balance = EXCLUDED.balance`,
		tenantID, userID, demoBalance); err != nil {
		return fmt.Errorf("balance: %w", err)
	}

	var providerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		tenantID, "mvp-openai-"+unique, "MVP OpenAI").Scan(&providerID); err != nil {
		return fmt.Errorf("provider: %w", err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "mvp-pool-"+unique).Scan(&poolGroupID); err != nil {
		return fmt.Errorf("pool_group: %w", err)
	}
	var channelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name, enabled) VALUES ($1, $2, $3, true) RETURNING id`,
		tenantID, poolGroupID, "mvp-channel-"+unique).Scan(&channelID); err != nil {
		return fmt.Errorf("channel: %w", err)
	}
	var accountID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type,
			cap_concurrency, in_flight_count, health_state, enabled, credential_state, capability_flags
		) VALUES ($1, $2, $3, $4, 'api_key', 8, 0, 'healthy', true, 'valid',
			ARRAY['stream','tools','vision','json','audio','file']) RETURNING id`,
		tenantID, providerID, channelID, "mvp-account-"+unique).Scan(&accountID); err != nil {
		return fmt.Errorf("provider_account: %w", err)
	}

	// 用网关的密钥种入一份可解密的 credential,以便 vault 能解析出一份。dev mock
	// 上游会忽略其值。该密钥(material + id)在运行时**必须**与网关的
	// HUAKAI_CREDENTIAL_KEY_B64 / _ID 匹配。
	if err := seedCredential(ctx, pool, tenantID, accountID); err != nil {
		return fmt.Errorf("credential: %w", err)
	}

	var modelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO models (tenant_id, scope, canonical_id, protocol_family,
		                     default_provider_model_id, default_context_window, status)
		 VALUES ($1, 'tenant', $2, 'openai_chat', $3, 128000, 'active') RETURNING id`,
		tenantID, "mvp-canonical-"+unique, demoAlias).Scan(&modelID); err != nil {
		return fmt.Errorf("model: %w", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO model_aliases (tenant_id, scope, model_id,
		                            public_alias_normalized, public_alias_display, status)
		 VALUES ($1, 'tenant', $2, $3, $3, 'active')`,
		tenantID, modelID, demoAlias); err != nil {
		return fmt.Errorf("model_alias: %w", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO model_pool_bindings (tenant_id, model_id, pool_group_id, priority, weight, enabled)
		 VALUES ($1, $2, $3, 100, 1, true)`,
		tenantID, modelID, poolGroupID); err != nil {
		return fmt.Errorf("model_pool_binding: %w", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO model_registry_snapshots (tenant_id, version) VALUES ($1, 1)
		 ON CONFLICT (tenant_id) DO UPDATE SET version = model_registry_snapshots.version + 1`,
		tenantID); err != nil {
		return fmt.Errorf("registry_snapshot: %w", err)
	}

	fmt.Printf("✅ MVP routing target seeded (no cleanup).\n")
	fmt.Printf("  tenant_id=%d user_id=%d api_key_id=%d\n", tenantID, userID, apiKeyID)
	fmt.Printf("  provider_id=%d channel_id=%d account_id=%d pool_group_id=%d\n", providerID, channelID, accountID, poolGroupID)
	fmt.Printf("  model_id=%d alias=%q balance=%s USD\n", modelID, demoAlias, demoBalance)
	fmt.Printf("  HK_KEY=%s\n", bearer)
	return nil
}

// seedCredential 用网关的 credential 密钥(HUAKAI_CREDENTIAL_KEY_B64 / _ID,
// 默认为 32 个零字节 / "local-v1")加密一份虚拟的 openai api_key credential,
// 并插入一行 active 状态的 account_credentials 记录,以便 vault 能为种入的
// account 解析出一份可解密的 credential。
func seedCredential(ctx context.Context, pool *pgxpool.Pool, tenantID, accountID int64) error {
	keyID := strings.TrimSpace(os.Getenv("HUAKAI_CREDENTIAL_KEY_ID"))
	if keyID == "" {
		keyID = "local-v1"
	}
	material := make([]byte, 32)
	if raw := strings.TrimSpace(os.Getenv("HUAKAI_CREDENTIAL_KEY_B64")); raw != "" {
		m, err := credentialstore.DecodeKeyMaterial(raw)
		if err != nil {
			return fmt.Errorf("decode key: %w", err)
		}
		material = m
	}
	kp, err := credentialstore.NewStaticKeyProvider(keyID, material)
	if err != nil {
		return err
	}
	env, err := credentialstore.NewCipher(kp).Encrypt(ctx,
		[]byte(`{"api_key":"sk-mock-dev-key"}`),
		credentialstore.AAD{TenantID: tenantID, ProviderAccountID: accountID, Vendor: "openai", AuthMode: "api_key", Version: 1})
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO account_credentials (tenant_id, provider_account_id, vendor, auth_mode, state,
		   credential_version, encrypted_payload, encryption_scheme, key_id, nonce, aad_hash)
		 VALUES ($1, $2, 'openai', 'api_key', 'active', 1, $3, 'aes-256-gcm', $4, $5, $6)`,
		tenantID, accountID, env.Ciphertext, env.KeyID, env.Nonce, env.AADHash)
	return err
}
