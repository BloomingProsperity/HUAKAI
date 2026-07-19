//go:build e2e_grok_live

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

const (
	grokLiveBearerPrefix        = "hk_test_"
	grokLiveBinaryName          = "gateway-grok-live-e2e.exe"
	grokLiveProtocol            = registrydefault.ProtocolGrokChat
	grokLiveVendor              = credentialstore.VendorGrok
	grokLiveAuthMode            = credentialstore.AuthModeXAIOAuth
	grokLiveProviderAccountType = "oauth"
	grokLiveModel               = "grok-3"
	grokLiveUpstreamEndpoint    = "https://api.x.ai/v1/chat/completions"
	grokLiveQuotaLimit          = "1000.00000000"
	grokLiveInitialBalance      = "1000.00000000"

	grokLiveBootRetries   = 30
	grokLiveBootRetryWait = 200 * time.Millisecond

	grokLiveRequestBody = `{"model":"grok-3","messages":[{"role":"user","content":"Reply with exactly the word OK"}],"max_tokens":10}`
)

type grokLiveAuth struct {
	accessToken  string
	refreshToken string
}

type grokLiveSeed struct {
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

type grokLiveHTTPResult struct {
	logicalID string
	status    int
	body      []byte
}

type grokLiveClaimEvidence struct {
	claimID    int64
	actualCost decimal.Decimal
}

type grokLiveBalance struct {
	balance decimal.Decimal
	held    decimal.Decimal
}

type grokLiveQuotaSnapshot struct {
	reserved decimal.Decimal
	settled  decimal.Decimal
	overage  decimal.Decimal
	requests int64
}

func TestGrokOAuthLiveRelayChain(t *testing.T) {
	dsn := firstGrokLiveNonEmpty(os.Getenv("HUAKAI_DATABASE_URL"), os.Getenv("HUAKAI_E2E_DATABASE_URL"))
	if strings.TrimSpace(dsn) == "" {
		t.Skip("HUAKAI_DATABASE_URL/HUAKAI_E2E_DATABASE_URL 未设置，跳过 Grok live e2e")
	}
	auth := grokLiveAuth{
		accessToken:  strings.TrimSpace(os.Getenv("HUAKAI_E2E_GROK_ACCESS_TOKEN")),
		refreshToken: strings.TrimSpace(os.Getenv("HUAKAI_E2E_GROK_REFRESH_TOKEN")),
	}
	if auth.accessToken == "" || auth.refreshToken == "" {
		t.Skip("未提供 Grok OAuth 凭据")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 Grok live e2e 数据库连接池: %v", err)
	}
	// 连接池先注册清理，后注册的数据清理按 LIFO 在关池前执行。
	t.Cleanup(pgPool.Close)

	seed := seedGrokLiveGraph(t, ctx, pgPool, auth)
	materialized := assertGrokLiveSeedSelectableAndMaterializable(t, ctx, pgPool, seed, auth)
	assertGrokLiveDefaultAdapter(t, materialized)

	binPath := buildGrokLiveGateway(t)
	addr := reserveGrokLiveLocalPort(t)
	cmd := startGrokLiveGateway(t, binPath, dsn, addr, seed, auth)
	t.Cleanup(func() { stopGrokLiveGateway(cmd) })
	waitForGrokLiveGateway(t, addr)

	client := &http.Client{Timeout: 60 * time.Second}

	t.Run("单请求整条链路", func(t *testing.T) {
		beforeBalance := readGrokLiveBalance(t, ctx, pgPool, seed)
		if !beforeBalance.held.IsZero() {
			t.Fatalf("请求前 user_balances.held=%s want 0", beforeBalance.held)
		}
		beforeQuota := readGrokLiveQuotaSnapshot(t, ctx, pgPool, seed)
		logicalID := "grok-live-single-" + uuid.NewString()

		res, err := postGrokLiveChat(ctx, client, addr, seed, logicalID)
		if err != nil {
			t.Fatalf("POST /v1/chat/completions: %v", err)
		}

		// 变异 1：若选号、OAuth Bearer 注入、api.x.ai 端点或响应回传任一断链，
		// HTTP 200 或 choices[0].message.content 非空断言会转红。
		assertGrokLiveResponse(t, res, auth)

		// 变异 3：若结算没有把 claim 提交，或定价/用量未形成正成本，
		// billing_ledger_claims.status/actual_cost 断言会转红。
		claim := waitForGrokLiveCommittedClaim(t, ctx, pgPool, seed, logicalID)

		// 变异 2：若结算不写 usage_records、漏写输出 token、成功终态、请求时序
		// 或 Grok provider 关联，下面对每一列的断言会转红。
		assertGrokLiveUsageRecord(t, ctx, pgPool, seed, claim)

		// 变异 4：若 hold 未捕获、重复捕获、余额未按 actual_cost 扣减或 held 泄漏，
		// balance_holds 与 user_balances 的精确守恒断言会转红。
		assertGrokLiveCapturedHold(t, ctx, pgPool, claim)
		afterBalance := readGrokLiveBalance(t, ctx, pgPool, seed)
		assertGrokLiveBalanceDelta(t, beforeBalance, afterBalance, claim.actualCost)

		// 变异 5：若成功结算漏掉槽释放，provider_accounts.in_flight_count 最终不为 0。
		assertGrokLiveReleasedSlot(t, ctx, pgPool, seed, claim.claimID)
		waitForGrokLiveInFlight(t, ctx, pgPool, seed.providerAccountID, 0)

		// 变异 6：若 quota reserve/settle 未接线，quota_windows 的 settled_value 不会
		// 精确增加 actual_cost，断言转红。注:seed 为 cost_usd 策略,生产按 metric 计数
		// (quota/service.go MetricCostUSD 的 RequestCountDelta 恒 0)——cost 窗口不计
		// request_count,故期望 request_count 增量为 0(只有 MetricRequests 策略才 +1)。
		waitForGrokLiveQuotaDelta(t, ctx, pgPool, seed, beforeQuota, claim.actualCost, 0)
		assertGrokLiveQuotaReservation(t, ctx, pgPool, claim)
	})

	t.Run("三请求并发无泄漏", func(t *testing.T) {
		beforeBalance := readGrokLiveBalance(t, ctx, pgPool, seed)
		if !beforeBalance.held.IsZero() {
			t.Fatalf("并发请求前 user_balances.held=%s want 0", beforeBalance.held)
		}
		beforeQuota := readGrokLiveQuotaSnapshot(t, ctx, pgPool, seed)

		const requestCount = 3
		type outcome struct {
			index int
			res   grokLiveHTTPResult
			err   error
		}
		ready := make(chan struct{}, requestCount)
		start := make(chan struct{})
		outcomes := make(chan outcome, requestCount)
		for i := 0; i < requestCount; i++ {
			logicalID := fmt.Sprintf("grok-live-concurrent-%d-%s", i, uuid.NewString())
			go func(index int, id string) {
				ready <- struct{}{}
				<-start
				res, err := postGrokLiveChat(ctx, client, addr, seed, id)
				outcomes <- outcome{index: index, res: res, err: err}
			}(i, logicalID)
		}
		for i := 0; i < requestCount; i++ {
			<-ready
		}
		close(start)

		results := make([]grokLiveHTTPResult, requestCount)
		var requestErrors []string
		for i := 0; i < requestCount; i++ {
			got := <-outcomes
			if got.err != nil {
				requestErrors = append(requestErrors, fmt.Sprintf("请求 %d: %v", got.index, got.err))
				continue
			}
			results[got.index] = got.res
		}
		if len(requestErrors) > 0 {
			t.Fatalf("三请求未全部完成: %s", strings.Join(requestErrors, "; "))
		}

		claims := make(map[int64]grokLiveClaimEvidence, requestCount)
		actualSum := decimal.Zero
		for i, res := range results {
			// 并发响应仍逐个判别，避免“只要任意一个成功”掩盖部分失败。
			assertGrokLiveResponse(t, res, auth)
			claim := waitForGrokLiveCommittedClaim(t, ctx, pgPool, seed, res.logicalID)
			if _, exists := claims[claim.claimID]; exists {
				t.Fatalf("并发请求 %d 复用了 claim_id=%d；三个不同幂等键应有三个 claim", i, claim.claimID)
			}
			claims[claim.claimID] = claim
			assertGrokLiveUsageRecord(t, ctx, pgPool, seed, claim)
			assertGrokLiveCapturedHold(t, ctx, pgPool, claim)
			assertGrokLiveReleasedSlot(t, ctx, pgPool, seed, claim.claimID)
			actualSum = actualSum.Add(claim.actualCost)
		}
		if len(claims) != requestCount {
			t.Fatalf("并发 distinct claim 数=%d want %d", len(claims), requestCount)
		}

		// 变异：任一请求漏释放槽会使最终计数非零；任一请求重复释放则数据库原子
		// 释放守卫会令对应 claim 无法完整 committed/usage 闭环。
		waitForGrokLiveInFlight(t, ctx, pgPool, seed.providerAccountID, 0)

		// 变异：并发结算丢扣或重扣时，余额差不会精确等于三个 claim.actual_cost 之和；
		// 任一 hold 泄漏时 held 也不会回到 0。
		afterBalance := readGrokLiveBalance(t, ctx, pgPool, seed)
		assertGrokLiveBalanceDelta(t, beforeBalance, afterBalance, actualSum)

		// 变异：并发 quota 更新若丢失或重复，窗口 settled_value 不会精确增加三次实际
		// 成本之和;逐 claim reservation 也不会全部 settled。cost_usd 策略不计 request_count
		// (同上,生产按 metric 计数),故期望 request_count 增量为 0。
		waitForGrokLiveQuotaDelta(t, ctx, pgPool, seed, beforeQuota, actualSum, 0)
		for _, claim := range claims {
			assertGrokLiveQuotaReservation(t, ctx, pgPool, claim)
		}
	})
}

func seedGrokLiveGraph(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, auth grokLiveAuth) *grokLiveSeed {
	t.Helper()
	unique := uuid.NewString()
	seed := &grokLiveSeed{pricingVersion: "e2e-grok-live-" + unique}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"grok-live-e2e-tenant-"+unique,
	).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	registerGrokLiveCleanup(t, pgPool, seed)

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "grok-live-e2e-user-"+unique,
	).Scan(&seed.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	seed.bearer = grokLiveBearerPrefix + unique
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
		seed.tenantID, seed.userID, "grok-live-e2e-key-"+unique, string(keyHash), keyPrefix,
	).Scan(&seed.apiKeyID); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
		 VALUES ($1, $2, $3::numeric, 0, 1, now())`,
		seed.tenantID, seed.userID, grokLiveInitialBalance,
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
	pricingData := fmt.Sprintf(
		`{"providers":{"grok":{"models":{"%s":{"input_micro_usd":"1","output_micro_usd":"2","cache_read_micro_usd":"1"}}}}}`,
		grokLiveModel,
	)
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
		seed.pricingVersion, pricingData, "e2e:grok-live",
	); err != nil {
		t.Fatalf("seed billing pricing version: %v", err)
	}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		seed.tenantID, grokLiveVendor, "grok live e2e "+unique, grokLiveProtocol,
	).Scan(&seed.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "grok-live-e2e-pg-"+unique,
	).Scan(&seed.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		seed.tenantID, seed.poolGroupID, "grok-live-e2e-ch-"+unique,
	).Scan(&seed.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	seed.providerAccountID = seedGrokLiveProviderAccount(t, ctx, pgPool, seed, auth, unique)

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO models (tenant_id, scope, canonical_id, protocol_family,
		                     default_provider_model_id, default_context_window, status)
		 VALUES ($1, 'tenant', $2, $3, $4, 131072, 'active')
		 RETURNING id`,
		seed.tenantID, grokLiveModel, grokLiveProtocol, grokLiveModel,
	).Scan(&seed.modelID); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO model_aliases (tenant_id, scope, model_id,
		                            public_alias_normalized, public_alias_display, status)
		 VALUES ($1, 'tenant', $2, $3, $4, 'active')
		 RETURNING id`,
		seed.tenantID, seed.modelID, grokLiveModel, grokLiveModel,
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
		seed.tenantID, strconv.FormatInt(seed.userID, 10), grokLiveQuotaLimit,
	).Scan(&seed.costQuotaPolicyID); err != nil {
		t.Fatalf("seed cost quota policy: %v", err)
	}
	return seed
}

func seedGrokLiveProviderAccount(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *grokLiveSeed, auth grokLiveAuth, unique string) int64 {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"access_token":  auth.accessToken,
		"refresh_token": auth.refreshToken,
	})
	if err != nil {
		t.Fatalf("序列化 Grok OAuth 凭据: %v", err)
	}

	var accountID int64
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type,
			cap_concurrency, in_flight_count, priority, health_state, credential_state,
			model_allow_list, capability_flags, credentials, extra
		) VALUES ($1, $2, $3, $4, $5,
			3, 0, 100, 'healthy', 'valid',
			ARRAY[$6]::text[], ARRAY['json']::text[], $7::jsonb, '{}'::jsonb) RETURNING id`,
		seed.tenantID, seed.providerID, seed.channelID, "grok-live-e2e-acct-"+unique,
		// provider_accounts 只记录通用账号形态；Grok 特化 auth_mode 写在 v2 加密凭据行。
		grokLiveProviderAccountType, grokLiveModel, string(payload),
	).Scan(&accountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}

	keyProvider, err := grokLiveCredentialKeyProvider()
	if err != nil {
		t.Fatalf("创建 Grok live 凭据密钥提供器: %v", err)
	}
	credentialEnvelope, err := credentialstore.NewCipher(keyProvider).Encrypt(ctx,
		payload,
		credentialstore.AAD{
			TenantID:          seed.tenantID,
			ProviderAccountID: accountID,
			Vendor:            grokLiveVendor,
			AuthMode:          grokLiveAuthMode,
			Version:           1,
		})
	if err != nil {
		t.Fatalf("加密 provider account %d 的 Grok OAuth 凭据: %v", accountID, err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO account_credentials (tenant_id, provider_account_id, vendor, auth_mode, state,
		   credential_version, encrypted_payload, encryption_scheme, key_id, nonce, aad_hash)
		 VALUES ($1, $2, $3, $4, 'active', 1, $5, 'aes-256-gcm', $6, $7, $8)`,
		seed.tenantID, accountID, grokLiveVendor, grokLiveAuthMode,
		credentialEnvelope.Ciphertext, credentialEnvelope.KeyID, credentialEnvelope.Nonce, credentialEnvelope.AADHash,
	); err != nil {
		t.Fatalf("seed Grok account credential: %v", err)
	}
	return accountID
}

func registerGrokLiveCleanup(t *testing.T, pgPool *pgxpool.Pool, seed *grokLiveSeed) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// append-only 账务记录可能阻止完整删除；无论后续清理是否受阻，先抹掉 legacy 明文凭据。
		if _, err := pgPool.Exec(ctx,
			`UPDATE provider_accounts SET credentials='{}'::jsonb WHERE tenant_id=$1`,
			seed.tenantID,
		); err != nil {
			t.Errorf("清除 Grok live e2e legacy 明文凭据: %v", err)
		}

		_, _ = pgPool.Exec(ctx, `DELETE FROM channel_health_admin_alerts WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM channel_health_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM channel_health_state WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM credential_acquisition_flow_sessions WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM credential_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM oauth_refresh_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM oauth_storm_budget WHERE tenant_id=$1`, seed.tenantID)
		if _, err := pgPool.Exec(ctx, `DELETE FROM account_credentials WHERE tenant_id=$1`, seed.tenantID); err != nil {
			t.Errorf("删除 Grok live e2e 加密凭据: %v", err)
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
		_, _ = pgPool.Exec(ctx, `DELETE FROM sticky_bindings WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM pool_routing_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM rate_limit_audit_events WHERE tenant_id=$1`, seed.tenantID)
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

func assertGrokLiveSeedSelectableAndMaterializable(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *grokLiveSeed, auth grokLiveAuth) provider.Credential {
	t.Helper()
	resolved, err := registry.NewPostgresRegistry(pgPool, nil).ResolveModel(ctx, grokLiveModel, seed.tenantID)
	if err != nil {
		t.Fatalf("seed registry resolve %q: %v", grokLiveModel, err)
	}
	if resolved.ProtocolFamily != grokLiveProtocol {
		t.Fatalf("resolved protocol_family=%q want %q", resolved.ProtocolFamily, grokLiveProtocol)
	}
	if len(resolved.PoolCandidates) != 1 || resolved.PoolCandidates[0] != seed.poolGroupID {
		t.Fatalf("resolved pool candidates=%v want [%d]", resolved.PoolCandidates, seed.poolGroupID)
	}
	rows, err := dbbilling.New(pgPool).ListEligibleAccountsByPoolGroup(ctx, dbbilling.ListEligibleAccountsByPoolGroupParams{
		TenantID:                seed.tenantID,
		PoolGroupID:             seed.poolGroupID,
		RequestedModel:          grokLiveModel,
		RequestedProtocolFamily: resolved.ProtocolFamily,
		RequiredCapabilities:    []string{},
	})
	if err != nil {
		t.Fatalf("seed selector eligibility query: %v", err)
	}
	selectable := false
	for _, row := range rows {
		if row.ID == seed.providerAccountID {
			selectable = true
			break
		}
	}
	if !selectable {
		t.Fatalf("selector eligibility 未返回 provider_account_id=%d; rows=%v", seed.providerAccountID, rows)
	}

	keyProvider, err := grokLiveCredentialKeyProvider()
	if err != nil {
		t.Fatalf("创建 Grok live 凭据密钥提供器: %v", err)
	}
	store := credentialstore.NewStore(pgPool, keyProvider, credentialstore.DefaultHandlerRegistry())
	vault := provider.NewPostgresCredentialVaultWithStore(pgPool, store)
	credential, account, err := vault.Resolve(ctx, seed.tenantID, seed.providerAccountID)
	if err != nil {
		t.Fatalf("物化 Grok OAuth 凭据: %v", err)
	}
	// 变异：若 v2 行未优先、XAIOAuth handler 映射错误或密文 AAD 不匹配，
	// 类型/值/account 元数据任一断言都会在请求前精确转红。
	if credential.Type != provider.CredentialTypeOAuthAccessToken {
		t.Fatalf("Grok 物化 credential type=%q want %q", credential.Type, provider.CredentialTypeOAuthAccessToken)
	}
	if credential.Value != auth.accessToken {
		t.Fatal("Grok 物化 access_token 与环境凭据不一致")
	}
	if strings.TrimSpace(credential.Extra["base_url"]) != "" {
		t.Fatalf("Grok 物化凭据意外携带 base_url=%q；live 测试必须走协议注册表默认端点",
			credential.Extra["base_url"])
	}
	if account.Platform != grokLiveVendor || account.AccountType != grokLiveAuthMode {
		t.Fatalf("Grok 物化 account platform/type=%q/%q want %q/%q",
			account.Platform, account.AccountType, grokLiveVendor, grokLiveAuthMode)
	}
	if account.AccountCredentialID == 0 || account.CredentialVersion != 1 {
		t.Fatalf("Grok 物化 credential id/version=%d/%d want 非零/1",
			account.AccountCredentialID, account.CredentialVersion)
	}
	return credential
}

func assertGrokLiveDefaultAdapter(t *testing.T, credential provider.Credential) {
	t.Helper()
	adapter, err := registrydefault.Build().For(grokLiveProtocol)
	if err != nil {
		t.Fatalf("解析 Grok 默认出站适配器: %v", err)
	}
	compat, ok := adapter.(*provider.OpenAICompatPassthroughAdapter)
	if !ok {
		t.Fatalf("Grok adapter type=%T want *provider.OpenAICompatPassthroughAdapter", adapter)
	}
	if compat.Platform() != grokLiveVendor || compat.Endpoint != grokLiveUpstreamEndpoint {
		t.Fatalf("Grok adapter platform/endpoint=%q/%q want %q/%q",
			compat.Platform(), compat.Endpoint, grokLiveVendor, grokLiveUpstreamEndpoint)
	}
	accepted := false
	for _, allowed := range compat.AcceptableCredentialTypes() {
		if allowed == credential.Type {
			accepted = true
			break
		}
	}
	// 变异：若默认适配器移除 OAuth access token，真实网关启动前立即转红。
	if !accepted {
		t.Fatalf("Grok 默认 adapter 不接受物化后的 credential type=%q；允许=%v",
			credential.Type, compat.AcceptableCredentialTypes())
	}
}

func postGrokLiveChat(ctx context.Context, client *http.Client, addr string, seed *grokLiveSeed, logicalID string) (grokLiveHTTPResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/v1/chat/completions", strings.NewReader(grokLiveRequestBody))
	if err != nil {
		return grokLiveHTTPResult{}, fmt.Errorf("构造请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+seed.bearer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Idempotency-Key", logicalID)

	resp, err := client.Do(req)
	if err != nil {
		return grokLiveHTTPResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return grokLiveHTTPResult{}, fmt.Errorf("读取响应: %w", err)
	}
	return grokLiveHTTPResult{logicalID: logicalID, status: resp.StatusCode, body: body}, nil
}

func assertGrokLiveResponse(t *testing.T, res grokLiveHTTPResult, auth grokLiveAuth) {
	t.Helper()
	if res.status != http.StatusOK {
		t.Fatalf("logical_id=%s HTTP status=%d want 200 body=%s",
			res.logicalID, res.status, grokLiveBodyPreview(res.body, auth))
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(res.body, &decoded); err != nil {
		t.Fatalf("logical_id=%s 解析 Grok JSON 响应: %v body=%s",
			res.logicalID, err, grokLiveBodyPreview(res.body, auth))
	}
	if len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		t.Fatalf("logical_id=%s choices[0].message.content 为空: body=%s",
			res.logicalID, grokLiveBodyPreview(res.body, auth))
	}
}

func waitForGrokLiveCommittedClaim(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *grokLiveSeed, logicalID string) grokLiveClaimEvidence {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var claimID int64
	var status string
	var actualCostRaw string
	var providerAccountID *int64
	for {
		err := pgPool.QueryRow(ctx,
			`SELECT id, status, COALESCE(actual_cost, 0)::text, provider_account_id
			   FROM billing_ledger_claims
			  WHERE tenant_id=$1 AND api_key_id=$2 AND logical_request_id=$3`,
			seed.tenantID, seed.apiKeyID, logicalID,
		).Scan(&claimID, &status, &actualCostRaw, &providerAccountID)
		if err == nil && status == "committed" {
			break
		}
		if err == nil && status == "aborted" {
			t.Fatalf("claim %d logical_id=%s 已 aborted，未走完成功结算", claimID, logicalID)
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("查询 logical_id=%s 的 billing claim: %v", logicalID, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("logical_id=%s claim status=%q want committed", logicalID, status)
		}
		time.Sleep(100 * time.Millisecond)
	}
	var count int
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*) FROM billing_ledger_claims
		  WHERE tenant_id=$1 AND api_key_id=$2 AND logical_request_id=$3`,
		seed.tenantID, seed.apiKeyID, logicalID,
	).Scan(&count); err != nil {
		t.Fatalf("统计 logical_id=%s 的 billing claim: %v", logicalID, err)
	}
	if count != 1 {
		t.Fatalf("logical_id=%s billing_ledger_claims count=%d want 1", logicalID, count)
	}
	if providerAccountID == nil || *providerAccountID != seed.providerAccountID {
		t.Fatalf("claim %d provider_account_id=%v want %d", claimID, providerAccountID, seed.providerAccountID)
	}
	actualCost := parseGrokLiveDecimal(t, "billing_ledger_claims.actual_cost", actualCostRaw)
	if !actualCost.IsPositive() {
		t.Fatalf("claim %d actual_cost=%s want >0", claimID, actualCost)
	}
	return grokLiveClaimEvidence{claimID: claimID, actualCost: actualCost}
}

func assertGrokLiveUsageRecord(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *grokLiveSeed, claim grokLiveClaimEvidence) {
	t.Helper()
	var count int
	var tokensOutput int64
	var requestedPresent bool
	var firstBytePresent, firstByteOrdered, settledOrdered bool
	var endClass, providerCode, providerProtocol string
	var accountMatched bool
	var actualCostRaw string
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*),
		        COALESCE(sum(ur.tokens_output), 0)::bigint,
		        COALESCE(bool_and(ur.requested_at IS NOT NULL), false),
		        COALESCE(bool_and(ur.first_byte_at IS NOT NULL), false),
		        COALESCE(bool_and(ur.first_byte_at IS NULL OR ur.first_byte_at >= ur.requested_at), false),
		        COALESCE(bool_and(ur.settled_at >= ur.requested_at), false),
		        COALESCE(min(ur.end_class), ''),
		        COALESCE(bool_and(ur.provider_account_id=$2), false),
		        COALESCE(min(p.code), ''),
		        COALESCE(min(p.upstream_protocol), ''),
		        COALESCE(sum(ur.actual_cost), 0)::text
		   FROM usage_records ur
		   JOIN provider_accounts pa ON pa.id=ur.provider_account_id AND pa.tenant_id=ur.tenant_id
		   JOIN providers p ON p.id=pa.provider_id AND p.tenant_id=pa.tenant_id
		  WHERE ur.claim_id=$1`,
		claim.claimID, seed.providerAccountID,
	).Scan(
		&count,
		&tokensOutput,
		&requestedPresent,
		&firstBytePresent,
		&firstByteOrdered,
		&settledOrdered,
		&endClass,
		&accountMatched,
		&providerCode,
		&providerProtocol,
		&actualCostRaw,
	); err != nil {
		t.Fatalf("查询 claim %d usage_records: %v", claim.claimID, err)
	}
	if count != 1 {
		t.Fatalf("claim %d usage_records count=%d want 1", claim.claimID, count)
	}
	if tokensOutput <= 0 {
		t.Fatalf("claim %d usage_records.tokens_output=%d want >0", claim.claimID, tokensOutput)
	}
	// 非流式路径没有 SSE flush，当前实现允许 first_byte_at 为 NULL；因此本用例钉住
	// requested_at、settled_at 顺序和正成本。若未来写入首字，
	// 同时要求 first_byte_at 不早于 requested_at。
	if !requestedPresent || !firstByteOrdered || !settledOrdered {
		t.Fatalf("claim %d usage 时序 requested/first_byte_order/settled_order=%v/%v/%v",
			claim.claimID, requestedPresent, firstByteOrdered, settledOrdered)
	}
	if firstBytePresent {
		t.Logf("claim %d 非流式 usage_records 捕获到 first_byte_at", claim.claimID)
	}
	// 成功终态集(生产口径 usage_analytics 一致):非流式客户端请求经上游流式聚合后
	// 终态是 stream_end_graceful,纯非流式上游则 non_streaming——两者均属成功。
	// 变异:若结算把成功记成错误终态(如 upstream_error_*),两值都不匹配→红。
	if endClass != "non_streaming" && endClass != "stream_end_graceful" {
		t.Fatalf("claim %d usage_records.end_class=%q want 成功终态(non_streaming/stream_end_graceful)", claim.claimID, endClass)
	}
	if !accountMatched || providerCode != grokLiveVendor || providerProtocol != grokLiveProtocol {
		t.Fatalf("claim %d usage provider account/code/protocol=%v/%q/%q want true/%q/%q",
			claim.claimID, accountMatched, providerCode, providerProtocol, grokLiveVendor, grokLiveProtocol)
	}
	usageCost := parseGrokLiveDecimal(t, "usage_records.actual_cost", actualCostRaw)
	if !usageCost.Equal(claim.actualCost) {
		t.Fatalf("claim %d usage actual_cost=%s want claim actual_cost=%s",
			claim.claimID, usageCost, claim.actualCost)
	}
}

func assertGrokLiveCapturedHold(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, claim grokLiveClaimEvidence) {
	t.Helper()
	var count int
	var state, capturedRaw string
	var resolved bool
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*), COALESCE(min(state), ''), COALESCE(sum(captured), 0)::text,
		        COALESCE(bool_and(resolved_at IS NOT NULL), false)
		   FROM balance_holds
		  WHERE claim_id=$1`,
		claim.claimID,
	).Scan(&count, &state, &capturedRaw, &resolved); err != nil {
		t.Fatalf("查询 claim %d balance_holds: %v", claim.claimID, err)
	}
	if count != 1 {
		t.Fatalf("claim %d balance_holds count=%d want 1", claim.claimID, count)
	}
	captured := parseGrokLiveDecimal(t, "balance_holds.captured", capturedRaw)
	if state != "captured" || !resolved || !captured.Equal(claim.actualCost) {
		t.Fatalf("claim %d hold state/resolved/captured=%q/%v/%s want captured/true/%s",
			claim.claimID, state, resolved, captured, claim.actualCost)
	}
}

func assertGrokLiveReleasedSlot(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *grokLiveSeed, claimID int64) {
	t.Helper()
	var count int
	var status string
	var accountMatched, released bool
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*), COALESCE(min(status), ''),
		        COALESCE(bool_and(provider_account_id=$2), false),
		        COALESCE(bool_and(released_at IS NOT NULL), false)
		   FROM pool_slot_acquisitions
		  WHERE claim_id=$1`,
		claimID, seed.providerAccountID,
	).Scan(&count, &status, &accountMatched, &released); err != nil {
		t.Fatalf("查询 claim %d pool_slot_acquisitions: %v", claimID, err)
	}
	// 单看最终 in_flight_count=0 无法排除“槽从未取得”的假阳性；逐 claim 的
	// released_success 行证明本请求确实取得过槽且只留下一个成功释放终态。
	if count != 1 || status != "released_success" || !accountMatched || !released {
		t.Fatalf("claim %d slot count/status/account/released=%d/%q/%v/%v want 1/released_success/true/true",
			claimID, count, status, accountMatched, released)
	}
}

func readGrokLiveBalance(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *grokLiveSeed) grokLiveBalance {
	t.Helper()
	var balance, held decimal.Decimal
	if err := pgPool.QueryRow(ctx,
		`SELECT balance, held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		seed.tenantID, seed.userID,
	).Scan(&balance, &held); err != nil {
		t.Fatalf("查询 Grok live user_balances: %v", err)
	}
	return grokLiveBalance{balance: balance, held: held}
}

func assertGrokLiveBalanceDelta(t *testing.T, before, after grokLiveBalance, wantDebit decimal.Decimal) {
	t.Helper()
	if !after.held.IsZero() {
		t.Fatalf("结算后 user_balances.held=%s want 0", after.held)
	}
	if !after.balance.LessThan(before.balance) {
		t.Fatalf("结算后 balance=%s 未低于请求前 %s", after.balance, before.balance)
	}
	debit := before.balance.Sub(after.balance)
	if !debit.Equal(wantDebit) {
		t.Fatalf("user_balances 扣减=%s want claims actual_cost 之和=%s", debit, wantDebit)
	}
}

func readGrokLiveQuotaSnapshot(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *grokLiveSeed) grokLiveQuotaSnapshot {
	t.Helper()
	var reservedRaw, settledRaw, overageRaw string
	var requests int64
	if err := pgPool.QueryRow(ctx,
		`SELECT COALESCE(sum(reserved_value), 0)::text,
		        COALESCE(sum(settled_value), 0)::text,
		        COALESCE(sum(overage_value), 0)::text,
		        COALESCE(sum(request_count), 0)::bigint
		   FROM quota_windows
		  WHERE tenant_id=$1 AND policy_id=$2`,
		seed.tenantID, seed.costQuotaPolicyID,
	).Scan(&reservedRaw, &settledRaw, &overageRaw, &requests); err != nil {
		t.Fatalf("查询 Grok live quota_windows: %v", err)
	}
	return grokLiveQuotaSnapshot{
		reserved: parseGrokLiveDecimal(t, "quota_windows.reserved_value", reservedRaw),
		settled:  parseGrokLiveDecimal(t, "quota_windows.settled_value", settledRaw),
		overage:  parseGrokLiveDecimal(t, "quota_windows.overage_value", overageRaw),
		requests: requests,
	}
}

func waitForGrokLiveQuotaDelta(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *grokLiveSeed, before grokLiveQuotaSnapshot, wantCost decimal.Decimal, wantRequests int64) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var after grokLiveQuotaSnapshot
	for {
		after = readGrokLiveQuotaSnapshot(t, ctx, pgPool, seed)
		settledDelta := after.settled.Sub(before.settled)
		requestDelta := after.requests - before.requests
		if settledDelta.Equal(wantCost) && requestDelta == wantRequests &&
			after.reserved.Equal(before.reserved) && after.overage.Equal(before.overage) {
			usedDelta := after.reserved.Add(after.settled).Sub(before.reserved.Add(before.settled))
			if !usedDelta.IsPositive() {
				t.Fatalf("quota_windows used 增量=%s want >0", usedDelta)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("quota_windows delta reserved/settled/overage/requests=%s/%s/%s/%d want 0/%s/0/%d",
				after.reserved.Sub(before.reserved), settledDelta, after.overage.Sub(before.overage), requestDelta,
				wantCost, wantRequests)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func assertGrokLiveQuotaReservation(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, claim grokLiveClaimEvidence) {
	t.Helper()
	var count int
	var status, settledCostRaw string
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*), COALESCE(min(status), ''), COALESCE(sum(settled_cost), 0)::text
		   FROM quota_reservations
		  WHERE claim_id=$1`,
		claim.claimID,
	).Scan(&count, &status, &settledCostRaw); err != nil {
		t.Fatalf("查询 claim %d quota_reservations: %v", claim.claimID, err)
	}
	settledCost := parseGrokLiveDecimal(t, "quota_reservations.settled_cost", settledCostRaw)
	if count != 1 || status != "settled" || !settledCost.Equal(claim.actualCost) {
		t.Fatalf("claim %d quota reservation count/status/cost=%d/%q/%s want 1/settled/%s",
			claim.claimID, count, status, settledCost, claim.actualCost)
	}
}

func waitForGrokLiveInFlight(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, providerAccountID int64, want int32) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last int32
	for {
		if err := pgPool.QueryRow(ctx,
			`SELECT in_flight_count FROM provider_accounts WHERE id=$1`,
			providerAccountID,
		).Scan(&last); err != nil {
			t.Fatalf("读取 provider_accounts.in_flight_count: %v", err)
		}
		if last == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("provider_accounts.in_flight_count=%d want %d", last, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func buildGrokLiveGateway(t *testing.T) string {
	t.Helper()
	moduleRoot := goModuleRootForGrokLive(t)
	binPath := filepath.Join(t.TempDir(), grokLiveBinaryName)
	stamp := fmt.Sprintf("grok-live-e2e-%d", time.Now().UnixNano())
	cmd := exec.Command("go", "build",
		"-ldflags", "-X main.smokeBuildStamp="+stamp,
		"-o", binPath, "./cmd/gateway")
	cmd.Dir = moduleRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("从 %s 构建 gateway: %v", moduleRoot, err)
	}
	return binPath
}

func goModuleRootForGrokLive(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	gomod := string(bytes.TrimSpace(out))
	if gomod == "" || gomod == "/dev/null" {
		t.Fatal("当前目录不在 Go module 中")
	}
	return filepath.Dir(gomod)
}

func reserveGrokLiveLocalPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("预留 Grok live 本地端口: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("关闭 Grok live 预留端口: %v", err)
	}
	return addr
}

func startGrokLiveGateway(t *testing.T, binPath, dsn, addr string, seed *grokLiveSeed, auth grokLiveAuth) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(binPath)
	cmd.Env = append(grokLiveChildEnv(),
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
	stderr, _ := cmd.StderrPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 Grok live gateway: %v", err)
	}
	go drainGrokLivePipe("gateway-stderr", stderr, auth)
	go drainGrokLivePipe("gateway-stdout", stdout, auth)
	return cmd
}

func grokLiveChildEnv() []string {
	blocked := []string{
		"HUAKAI_E2E_GROK_ACCESS_TOKEN=",
		"HUAKAI_E2E_GROK_REFRESH_TOKEN=",
	}
	env := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		skip := false
		for _, prefix := range blocked {
			if strings.HasPrefix(item, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			env = append(env, item)
		}
	}
	return env
}

func drainGrokLivePipe(label string, pipe io.ReadCloser, auth grokLiveAuth) {
	if pipe == nil {
		return
	}
	defer pipe.Close()
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		fmt.Printf("[%s] %s\n", label, redactGrokLiveSecrets(scanner.Text(), auth))
	}
}

func stopGrokLiveGateway(cmd *exec.Cmd) {
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

func waitForGrokLiveGateway(t *testing.T, addr string) {
	t.Helper()
	for i := 0; i < grokLiveBootRetries; i++ {
		resp, err := http.Get("http://" + addr + "/")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(grokLiveBootRetryWait)
	}
	t.Fatalf("gateway 未在 %s 的 %v 内启动监听",
		addr, time.Duration(grokLiveBootRetries)*grokLiveBootRetryWait)
}

func grokLiveCredentialKeyProvider() (credentialstore.KeyProvider, error) {
	return credentialstore.NewStaticKeyProvider("local-v1", make([]byte, 32))
}

var grokLiveBearerRedactionRE = regexp.MustCompile(`(?i)Bearer[[:space:]]+[A-Za-z0-9._~+/=-]+`)

func grokLiveBodyPreview(raw []byte, auth grokLiveAuth) string {
	const maxBodyBytes = 4096
	body := string(raw)
	if len(body) > maxBodyBytes {
		body = body[:maxBodyBytes] + "...<truncated>"
	}
	return redactGrokLiveSecrets(body, auth)
}

func redactGrokLiveSecrets(value string, auth grokLiveAuth) string {
	for _, secret := range []string{auth.accessToken, auth.refreshToken} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "<redacted>")
		}
	}
	return grokLiveBearerRedactionRE.ReplaceAllString(value, "Bearer <redacted>")
}

func parseGrokLiveDecimal(t *testing.T, label, raw string) decimal.Decimal {
	t.Helper()
	value, err := decimal.NewFromString(raw)
	if err != nil {
		t.Fatalf("解析 %s=%q: %v", label, raw, err)
	}
	return value
}

func firstGrokLiveNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
