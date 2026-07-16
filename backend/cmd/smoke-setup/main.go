package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

const (
	smokeDSN        = "postgres:///huakai_smoke_2026_05_15?host=/var/run/postgresql"
	credKeyPath     = "/tmp/huakai-smoke.cred_key"
	openAIKeyPath   = "/tmp/secrets/openai/api_key.txt"
	apiKeyOutPath   = "/tmp/huakai-smoke.api_key"
	tenantID        = int64(1)
	userID          = int64(1)
	smokeActor      = "smoke-setup"
	smokeAPIKeyName = "huakai-smoke-user-key"
)

type seedResult struct {
	TenantID            int64
	UserID              int64
	APIKeyID            int64
	APIKeyPlaintext     string
	APIKeyPrefix        string
	ProviderID          int64
	PoolGroupID         int64
	ChannelID           int64
	ProviderAccountID   int64
	AccountCredentialID int64
	ModelID             int64
	AliasID             int64
	RegistryVersion     int64
	OpenAISecretPath    string
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "smoke_setup_error=%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("tenant_id=%d\n", result.TenantID)
	fmt.Printf("user_id=%d\n", result.UserID)
	fmt.Printf("api_key_id=%d\n", result.APIKeyID)
	fmt.Printf("api_key_prefix=%s\n", result.APIKeyPrefix)
	fmt.Printf("api_key_plaintext=%s\n", result.APIKeyPlaintext)
	fmt.Printf("api_key_file=%s\n", apiKeyOutPath)
	fmt.Printf("provider_id=%d\n", result.ProviderID)
	fmt.Printf("pool_group_id=%d\n", result.PoolGroupID)
	fmt.Printf("channel_id=%d\n", result.ChannelID)
	fmt.Printf("provider_account_id=%d\n", result.ProviderAccountID)
	fmt.Printf("account_credential_id=%d\n", result.AccountCredentialID)
	fmt.Printf("model_id=%d\n", result.ModelID)
	fmt.Printf("alias_id=%d\n", result.AliasID)
	fmt.Printf("registry_version=%d\n", result.RegistryVersion)
	fmt.Printf("openai_secret_path=%s\n", result.OpenAISecretPath)
}

func run(ctx context.Context) (seedResult, error) {
	keyMaterial, err := loadCredentialKey()
	if err != nil {
		return seedResult{}, err
	}
	openAIKey, openAISecretPath, err := loadOpenAIKey()
	if err != nil {
		return seedResult{}, err
	}

	pool, err := db.Open(ctx, db.PoolConfig{DSN: smokeDSN, MaxConns: 4, MinConns: 1})
	if err != nil {
		return seedResult{}, err
	}
	defer pool.Close()

	result, err := seedGraph(ctx, pool)
	if err != nil {
		return seedResult{}, err
	}

	credentialID, err := upsertOpenAICredential(ctx, pool, keyMaterial, result.ProviderAccountID, openAIKey)
	if err != nil {
		return seedResult{}, err
	}
	result.AccountCredentialID = credentialID

	issued, err := issueHUAKAIAPIKey(ctx, pool)
	if err != nil {
		return seedResult{}, err
	}
	result.APIKeyID = issued.APIKeyID
	result.APIKeyPlaintext = issued.Plaintext
	result.APIKeyPrefix = issued.KeyPrefix
	result.OpenAISecretPath = openAISecretPath

	if err := os.WriteFile(apiKeyOutPath, []byte(issued.Plaintext+"\n"), 0o600); err != nil {
		return seedResult{}, fmt.Errorf("write api key file: %w", err)
	}
	if err := revokeOldSmokeKeys(ctx, pool, issued.APIKeyID); err != nil {
		return seedResult{}, err
	}
	return result, nil
}

func loadCredentialKey() ([]byte, error) {
	raw, err := readRequiredSecret(credKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read credential key: %w", err)
	}
	material, err := credentialstore.DecodeKeyMaterial(raw)
	if err != nil {
		return nil, fmt.Errorf("decode credential key: %w", err)
	}
	return material, nil
}

func loadOpenAIKey() (string, string, error) {
	// 优先按 Owner 指定的 /tmp 路径读取；本地烟测环境也允许 ~/secrets 兜底。
	paths := []string{openAIKeyPath}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, "secrets", "openai", "api_key.txt"))
	}
	var lastErr error
	for _, path := range paths {
		raw, err := readRequiredSecret(path)
		if err == nil {
			if !strings.HasPrefix(raw, "sk-") {
				return "", "", fmt.Errorf("openai api key at %s does not start with sk-", path)
			}
			return raw, path, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("read openai api key: %w", err)
		}
		lastErr = err
	}
	return "", "", fmt.Errorf("read openai api key: %w", lastErr)
}

func readRequiredSecret(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return value, nil
}

func seedGraph(ctx context.Context, pool *pgxpool.Pool) (seedResult, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return seedResult{}, fmt.Errorf("begin seed tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var out seedResult
	out.TenantID = tenantID
	out.UserID = userID

	// 固定 tenant_id=1，便于 curl 和人工 psql 检查。
	if _, err := tx.Exec(ctx, `
INSERT INTO tenants (id, name, status)
VALUES ($1, 'huakai-smoke-tenant', 'active')
ON CONFLICT (id) DO UPDATE
SET name = EXCLUDED.name,
    status = 'active',
    deleted_at = NULL,
    updated_at = NOW()`, tenantID); err != nil {
		return seedResult{}, fmt.Errorf("upsert tenant: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO users (id, tenant_id, email, display_name, status)
VALUES ($1, $2, 'smoke-admin@huakai.local', 'HUAKAI smoke admin', 'active')
ON CONFLICT (id) DO UPDATE
SET tenant_id = EXCLUDED.tenant_id,
    email = EXCLUDED.email,
    display_name = EXCLUDED.display_name,
    status = 'active',
    deleted_at = NULL,
    updated_at = NOW()`, userID, tenantID); err != nil {
		return seedResult{}, fmt.Errorf("upsert user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
SELECT setval(pg_get_serial_sequence('tenants', 'id'), GREATEST((SELECT max(id) FROM tenants), 1), true)`,
	); err != nil {
		return seedResult{}, fmt.Errorf("advance tenants sequence: %w", err)
	}
	if _, err := tx.Exec(ctx, `
SELECT setval(pg_get_serial_sequence('users', 'id'), GREATEST((SELECT max(id) FROM users), 1), true)`,
	); err != nil {
		return seedResult{}, fmt.Errorf("advance users sequence: %w", err)
	}

	if err := tx.QueryRow(ctx, `
INSERT INTO providers (tenant_id, code, display_name, upstream_protocol, enabled)
VALUES ($1, 'openai', 'OpenAI smoke provider', 'openai_chat', true)
ON CONFLICT (tenant_id, code) WHERE deleted_at IS NULL DO UPDATE
SET display_name = EXCLUDED.display_name,
    upstream_protocol = EXCLUDED.upstream_protocol,
    enabled = true,
    updated_at = NOW()
RETURNING id`, tenantID).Scan(&out.ProviderID); err != nil {
		return seedResult{}, fmt.Errorf("upsert provider: %w", err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO pool_groups (tenant_id, name, enabled)
VALUES ($1, 'huakai-smoke-openai-pool', true)
ON CONFLICT (tenant_id, name) WHERE deleted_at IS NULL DO UPDATE
SET enabled = true,
    updated_at = NOW()
RETURNING id`, tenantID).Scan(&out.PoolGroupID); err != nil {
		return seedResult{}, fmt.Errorf("upsert pool group: %w", err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO channels (tenant_id, pool_group_id, name, enabled)
VALUES ($1, $2, 'huakai-smoke-openai-channel', true)
ON CONFLICT (tenant_id, pool_group_id, name) WHERE deleted_at IS NULL DO UPDATE
SET enabled = true,
    updated_at = NOW()
RETURNING id`, tenantID, out.PoolGroupID).Scan(&out.ChannelID); err != nil {
		return seedResult{}, fmt.Errorf("upsert channel: %w", err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO provider_accounts (
    tenant_id, provider_id, channel_id, name, account_type,
    enabled, health_state, credential_state, credentials,
    cap_concurrency, in_flight_count, queue_depth,
    cap_queue_sticky, cap_queue_fallback, priority,
    created_by_actor, last_modified_by_actor
) VALUES (
    $1, $2, $3, 'huakai-smoke-openai-account', 'api_key',
    true, 'healthy', 'valid', '{}'::jsonb,
    4, 0, 0,
    2, 8, 10,
    $4, $4
)
ON CONFLICT (tenant_id, name) WHERE deleted_at IS NULL DO UPDATE
SET provider_id = EXCLUDED.provider_id,
    channel_id = EXCLUDED.channel_id,
    account_type = 'api_key',
    enabled = true,
    health_state = 'healthy',
    credential_state = 'valid',
    credentials = '{}'::jsonb,
    cap_concurrency = 4,
    in_flight_count = 0,
    queue_depth = 0,
    cap_queue_sticky = 2,
    cap_queue_fallback = 8,
    priority = 10,
    updated_at = NOW(),
    last_modified_by_actor = EXCLUDED.last_modified_by_actor
RETURNING id`, tenantID, out.ProviderID, out.ChannelID, smokeActor).Scan(&out.ProviderAccountID); err != nil {
		return seedResult{}, fmt.Errorf("upsert provider account: %w", err)
	}

	if err := tx.QueryRow(ctx, `
INSERT INTO models (
    tenant_id, scope, canonical_id, protocol_family,
    default_provider_model_id, default_context_window, status
) VALUES (
    $1, 'tenant', 'huakai-smoke-openai-gpt-4.1-mini', 'openai_chat',
    'gpt-4.1-mini', 128000, 'active'
)
ON CONFLICT (tenant_id, canonical_id) WHERE deleted_at IS NULL AND scope = 'tenant' DO UPDATE
SET protocol_family = EXCLUDED.protocol_family,
    default_provider_model_id = EXCLUDED.default_provider_model_id,
    default_context_window = EXCLUDED.default_context_window,
    status = 'active',
    updated_at = NOW()
RETURNING id`, tenantID).Scan(&out.ModelID); err != nil {
		return seedResult{}, fmt.Errorf("upsert model: %w", err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO model_aliases (
    tenant_id, scope, model_id, public_alias_normalized,
    public_alias_display, status, source
) VALUES (
    $1, 'tenant', $2, 'gpt-4.1-mini',
    'gpt-4.1-mini', 'active', 'operator'
)
ON CONFLICT (tenant_id, public_alias_normalized) WHERE deleted_at IS NULL AND scope = 'tenant' DO UPDATE
SET model_id = EXCLUDED.model_id,
    public_alias_display = EXCLUDED.public_alias_display,
    status = 'active',
    updated_at = NOW()
RETURNING id`, tenantID, out.ModelID).Scan(&out.AliasID); err != nil {
		return seedResult{}, fmt.Errorf("upsert model alias: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO model_pool_bindings (tenant_id, model_id, pool_group_id, priority, weight, enabled, reason)
VALUES ($1, $2, $3, 10, 1, true, 'smoke setup')
ON CONFLICT (tenant_id, model_id, pool_group_id) WHERE deleted_at IS NULL DO UPDATE
SET priority = 10,
    weight = 1,
    enabled = true,
    reason = EXCLUDED.reason,
    updated_at = NOW()`, tenantID, out.ModelID, out.PoolGroupID); err != nil {
		return seedResult{}, fmt.Errorf("upsert model pool binding: %w", err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO model_registry_snapshots (tenant_id, version, reason, updated_by_actor)
VALUES ($1, 1, 'smoke setup', $2)
ON CONFLICT (tenant_id) DO UPDATE
SET version = model_registry_snapshots.version + 1,
    reason = EXCLUDED.reason,
    updated_by_actor = EXCLUDED.updated_by_actor,
    updated_at = NOW()
RETURNING version`, tenantID, smokeActor).Scan(&out.RegistryVersion); err != nil {
		return seedResult{}, fmt.Errorf("upsert registry snapshot: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return seedResult{}, fmt.Errorf("commit seed tx: %w", err)
	}
	return out, nil
}

func upsertOpenAICredential(ctx context.Context, pool *pgxpool.Pool, keyMaterial []byte, providerAccountID int64, openAIKey string) (int64, error) {
	keyProvider, err := credentialstore.NewStaticKeyProvider("local-v1", keyMaterial)
	if err != nil {
		return 0, fmt.Errorf("create key provider: %w", err)
	}
	store := credentialstore.NewStore(pool, keyProvider, credentialstore.DefaultHandlerRegistry())
	payload, err := json.Marshal(map[string]string{"api_key": openAIKey})
	if err != nil {
		return 0, fmt.Errorf("marshal openai payload: %w", err)
	}

	meta, err := store.Create(ctx, credentialstore.CreateCredentialInput{
		TenantID: tenantID, ProviderAccountID: providerAccountID,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey,
		Payload: payload, ActorID: smokeActor,
	})
	if err == nil {
		return meta.ID, nil
	}
	if !isUniqueViolation(err) {
		return 0, fmt.Errorf("create account credential: %w", err)
	}

	rows, err := store.ListByAccount(ctx, tenantID, providerAccountID)
	if err != nil {
		return 0, fmt.Errorf("list existing credentials after unique conflict: %w", err)
	}
	for _, row := range rows {
		if row.Vendor == credentialstore.VendorOpenAI && row.AuthMode == credentialstore.AuthModeAPIKey {
			meta, err := store.Rotate(ctx, credentialstore.RotateCredentialInput{
				TenantID: tenantID, ProviderAccountID: providerAccountID, CredentialID: row.ID,
				Payload: payload, ActorID: smokeActor,
			})
			if err != nil {
				return 0, fmt.Errorf("rotate existing openai credential: %w", err)
			}
			return meta.ID, nil
		}
	}
	return 0, errors.New("openai credential unique conflict but no matching credential row found")
}

func issueHUAKAIAPIKey(ctx context.Context, pool *pgxpool.Pool) (admin.IssueResult, error) {
	issuer := admin.NewKeyIssuer(pool)
	caller, err := admin.NewAdminIdentity(ctx, admin.IdentityClaims{
		TokenID: time.Now().UnixNano(),
		Role:    admin.RolePlatformAdmin,
	}, nil)
	if err != nil {
		return admin.IssueResult{}, fmt.Errorf("construct smoke admin identity: %w", err)
	}
	result, err := issuer.Issue(ctx, admin.IssueRequest{
		Caller:      caller,
		TenantID:    tenantID,
		UserID:      userID,
		Name:        smokeAPIKeyName,
		Environment: admin.EnvLive,
		Reason:      "smoke setup",
		RequestID:   "smoke-setup",
	})
	if err != nil {
		return admin.IssueResult{}, fmt.Errorf("issue huakai api key: %w", err)
	}
	return result, nil
}

func revokeOldSmokeKeys(ctx context.Context, pool *pgxpool.Pool, keepID int64) error {
	_, err := pool.Exec(ctx, `
UPDATE api_keys
SET status = 'revoked',
    revoked_at = COALESCE(revoked_at, NOW()),
    revoked_reason = 'replaced by smoke setup',
    updated_at = NOW()
WHERE tenant_id = $1
  AND name = $2
  AND id <> $3
  AND status = 'active'
  AND deleted_at IS NULL`, tenantID, smokeAPIKeyName, keepID)
	if err != nil {
		return fmt.Errorf("revoke old smoke api keys: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
