//go:build e2e_upstream

// 真实上游端到端测试。它沿用 smoke_test.go 的种库、构建二进制、
// 子进程启动、真实 HTTP 请求、PG 状态断言形态，但把上游换成
// OpenAI 兼容 Chat Completions 的真实厂商端点。
//
// 本测试默认不会运行；需要显式加 e2e_upstream tag，并提供独立测试库
// DSN 与真实上游 key。API key 只从环境变量读取，绝不写入源码。

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/pool/dispatcher"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionenforce"
)

const (
	upstreamE2EBearerPrefix = "hk_test_"
	upstreamE2EBinaryName   = "gateway-upstream-e2e.exe"

	upstreamE2EBootRetries   = 30
	upstreamE2EBootRetryWait = 200 * time.Millisecond

	upstreamE2EAccountTypeAPIKey = "api_key"

	upstreamE2EDoubaoProtocol = "doubao_chat"
	upstreamE2EDoubaoModel    = "doubao-1-5-lite-32k-250115"
	upstreamE2EARKKeyEnv      = "HUAKAI_E2E_ARK_KEY"

	upstreamE2EHunyuanProtocol = "hunyuan_chat"
	upstreamE2EHunyuanModel    = "hy3-preview"
	upstreamE2EHunyuanKeyEnv   = "HUAKAI_E2E_HUNYUAN_KEY"

	// 并发 e2e 验证主备账号槽释放，不做配额高压测试。
	upstreamE2EDefaultAccountConcurrencyCap = 1
	upstreamE2EDefaultConcurrentRequests    = 2
	upstreamE2ECostQuotaLimitUSD            = "1000.00000000"
)

type upstreamE2ECase struct {
	slug                  string
	vendor                string
	protocolFamily        string
	model                 string
	upstreamModel         string
	clientShape           upstreamE2EClientShape
	officialClaudeClient  bool
	keyEnv                string
	credentialJSONEnv     string
	authMode              string
	accountType           string
	gatewayEnv            []string
	expectImportIdentity  bool
	expectSubscription    bool
	skipConcurrency       bool
	accountConcurrencyCap int
	concurrentRequests    int
}

type upstreamE2ECredential struct {
	payload []byte
}

type upstreamE2EClientShape string

const (
	upstreamE2EClientOpenAI    upstreamE2EClientShape = "openai"
	upstreamE2EClientAnthropic upstreamE2EClientShape = "anthropic"
)

func (tc upstreamE2ECase) normalizedClientShape() upstreamE2EClientShape {
	if tc.clientShape == "" {
		return upstreamE2EClientOpenAI
	}
	return tc.clientShape
}

func (tc upstreamE2ECase) routedModel() string {
	if model := strings.TrimSpace(tc.upstreamModel); model != "" {
		return model
	}
	return tc.model
}

func TestUpstreamE2E_DoubaoChatCompletions(t *testing.T) {
	runUpstreamE2E(t, upstreamE2ECase{
		slug:           "doubao",
		vendor:         credentialstore.VendorDoubao,
		protocolFamily: upstreamE2EDoubaoProtocol,
		model:          upstreamE2EDoubaoModel,
		keyEnv:         upstreamE2EARKKeyEnv,
		authMode:       credentialstore.AuthModeAPIKey,
		accountType:    upstreamE2EAccountTypeAPIKey,
	})
}

func TestUpstreamE2E_HunyuanOfficialAPI(t *testing.T) {
	runUpstreamE2E(t, upstreamE2ECase{
		slug:           "hunyuan-official-api",
		vendor:         credentialstore.VendorHunyuan,
		protocolFamily: upstreamE2EHunyuanProtocol,
		model:          upstreamE2EHunyuanModel,
		keyEnv:         upstreamE2EHunyuanKeyEnv,
		authMode:       credentialstore.AuthModeAPIKey,
		accountType:    upstreamE2EAccountTypeAPIKey,
	})
}

func (tc upstreamE2ECase) accountCap() int {
	if tc.accountConcurrencyCap > 0 {
		return tc.accountConcurrencyCap
	}
	return upstreamE2EDefaultAccountConcurrencyCap
}

func (tc upstreamE2ECase) requestCount() int {
	if tc.concurrentRequests > 0 {
		return tc.concurrentRequests
	}
	return upstreamE2EDefaultConcurrentRequests
}

func runUpstreamE2E(t *testing.T, tc upstreamE2ECase) {
	t.Helper()
	dsn := os.Getenv("HUAKAI_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_E2E_DATABASE_URL 未设")
	}
	dsn = useDisposableSpecializedLiveDatabase(t, dsn)
	credential := loadUpstreamE2ECredential(t, tc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 e2e 数据库连接池: %v", err)
	}
	t.Cleanup(pgPool.Close)

	seed := seedUpstreamE2EGraph(t, ctx, pgPool, tc, credential)

	binPath := buildUpstreamE2EGateway(t)
	defer os.Remove(binPath)

	addr := reserveUpstreamE2ELocalPort(t)
	processes := startUpstreamE2EGateway(t, binPath, dsn, addr, seed)
	t.Cleanup(func() { stopUpstreamE2EGateway(processes) })

	waitForUpstreamE2EGateway(t, addr)
	client := &http.Client{Timeout: 90 * time.Second}
	seed.providerAccountID = importUpstreamE2EAccount(t, ctx, client, addr, pgPool, seed, credential)
	assertUpstreamE2ESeedSelectable(t, ctx, pgPool, seed)

	t.Run("single_request", func(t *testing.T) {
		logicalID := "single-" + uuid.NewString()
		result := postUpstreamE2EChat(t, ctx, client, addr, seed, logicalID)
		assertUpstreamE2ESuccessResponse(t, result)
		assertUpstreamE2ESuccessPG(t, ctx, pgPool, seed, logicalID, result)
		waitForUpstreamE2EInFlight(t, ctx, pgPool, seed.providerAccountID, 0)
		if seed.failoverProviderAccountID > 0 {
			waitForUpstreamE2EInFlight(t, ctx, pgPool, seed.failoverProviderAccountID, 0)
		}
	})

	if !tc.skipConcurrency {
		t.Run("concurrency", func(t *testing.T) {
			runUpstreamE2EConcurrency(t, ctx, client, pgPool, addr, seed)
		})
	}
}

type upstreamE2ESeed struct {
	testCase                  upstreamE2ECase
	tenantID                  int64
	apiKeyID                  int64
	userID                    int64
	providerID                int64
	poolGroupID               int64
	channelID                 int64
	providerAccountID         int64
	failoverProviderAccountID int64
	modelID                   int64
	aliasID                   int64
	costQuotaPolicyID         int64
	concurrencyQuotaPolicyID  int64
	pricingVersion            string
	bearer                    string
	adminBearer               string
	adminTokenID              int64
}

func seedUpstreamE2EGraph(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, tc upstreamE2ECase, credential upstreamE2ECredential) *upstreamE2ESeed {
	t.Helper()
	unique := uuid.NewString()
	s := &upstreamE2ESeed{
		testCase:       tc,
		pricingVersion: "e2e-" + tc.slug + "-" + unique,
	}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		tc.slug+"-e2e-tenant-"+unique,
	).Scan(&s.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		s.tenantID, tc.slug+"-e2e-user-"+unique,
	).Scan(&s.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	s.bearer = upstreamE2EBearerPrefix + unique
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
		s.tenantID, s.userID, tc.slug+"-e2e-key-"+unique, string(keyHash), keyPrefix,
	).Scan(&s.apiKeyID); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	if _, err := pgPool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
		 VALUES ($1, $2, 1000.00, 0, 1, now())`,
		s.tenantID, s.userID,
	); err != nil {
		t.Fatalf("seed user_balance: %v", err)
	}

	t.Cleanup(func() {
		if err := cleanupUpstreamE2EGraph(context.Background(), pgPool, s); err != nil {
			t.Errorf("清理真实上游测试图失败: %v", err)
		}
	})

	if _, err := pgPool.Exec(ctx,
		`INSERT INTO tenants (id, name, status, created_at, updated_at)
		 VALUES (0, 'public-pricing', 'active', now(), now())
		 ON CONFLICT (id) DO NOTHING`,
	); err != nil {
		t.Fatalf("seed public pricing tenant: %v", err)
	}
	pricingModels := map[string]map[string]string{
		tc.model: {
			"input_micro_usd": "1", "output_micro_usd": "2", "cache_read_micro_usd": "1",
		},
	}
	if tc.routedModel() != tc.model {
		pricingModels[tc.routedModel()] = map[string]string{
			"input_micro_usd": "1", "output_micro_usd": "2", "cache_read_micro_usd": "1",
		}
	}
	pricingProvider := pool.VendorFromProtocolFamily(tc.protocolFamily)
	if pricingProvider == "" {
		pricingProvider = tc.vendor
	}
	pricingDataBytes, err := json.Marshal(map[string]any{
		"providers": map[string]any{pricingProvider: map[string]any{"models": pricingModels}},
	})
	if err != nil {
		t.Fatalf("编码 e2e 定价快照: %v", err)
	}
	pricingData := string(pricingDataBytes)
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
		s.pricingVersion, pricingData, "e2e:"+tc.slug+"-upstream",
	); err != nil {
		t.Fatalf("seed billing pricing version: %v", err)
	}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		s.tenantID, tc.vendor, tc.slug+" upstream e2e "+unique, tc.protocolFamily,
	).Scan(&s.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		s.tenantID, tc.slug+"-e2e-pg-"+unique,
	).Scan(&s.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		s.tenantID, s.poolGroupID, tc.slug+"-e2e-ch-"+unique,
	).Scan(&s.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	seedUpstreamE2EImportAuthorization(t, ctx, pgPool, s, unique)

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO models (tenant_id, scope, canonical_id, protocol_family,
		                     default_provider_model_id, default_context_window, status)
		 VALUES ($1, 'tenant', $2, $3, $4, 128000, 'active')
		 RETURNING id`,
		s.tenantID, tc.routedModel(), tc.protocolFamily, tc.routedModel(),
	).Scan(&s.modelID); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO model_aliases (tenant_id, scope, model_id,
		                            public_alias_normalized, public_alias_display, status)
		 VALUES ($1, 'tenant', $2, $3, $4, 'active')
		 RETURNING id`,
		s.tenantID, s.modelID, tc.model, tc.model,
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

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO quota_policies (
			tenant_id, scope_kind, scope_id, metric, window_kind, window_seconds,
			limit_value, burst_value, mode, priority, enabled, valid_from
		 )
		 VALUES ($1, 'user', $2, 'cost_usd', 'fixed', 3600,
		         $3::numeric, 0, 'enforce', 10, true, now())
		 RETURNING id`,
		s.tenantID, strconv.FormatInt(s.userID, 10), upstreamE2ECostQuotaLimitUSD,
	).Scan(&s.costQuotaPolicyID); err != nil {
		t.Fatalf("seed cost quota policy: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO quota_policies (
			tenant_id, scope_kind, scope_id, metric, window_kind, window_seconds,
			limit_value, burst_value, mode, priority, enabled, valid_from
		 )
		 VALUES ($1, 'api_key', $2, 'concurrency', 'none', 0,
		         $3::numeric, 0, 'enforce', 10, true, now())
		 RETURNING id`,
		s.tenantID, strconv.FormatInt(s.apiKeyID, 10), s.testCase.requestCount(),
	).Scan(&s.concurrencyQuotaPolicyID); err != nil {
		t.Fatalf("seed per-key concurrency quota policy: %v", err)
	}

	return s
}

func loadUpstreamE2ECredential(t *testing.T, tc upstreamE2ECase) upstreamE2ECredential {
	t.Helper()
	switch {
	case strings.TrimSpace(tc.credentialJSONEnv) != "":
		raw := strings.TrimSpace(os.Getenv(tc.credentialJSONEnv))
		if raw == "" {
			t.Skip(tc.credentialJSONEnv + " 未设")
		}
		if !json.Valid([]byte(raw)) {
			t.Fatalf("%s 不是合法 JSON", tc.credentialJSONEnv)
		}
		return upstreamE2ECredential{payload: []byte(raw)}
	case strings.TrimSpace(tc.keyEnv) != "":
		secret := strings.TrimSpace(os.Getenv(tc.keyEnv))
		if secret == "" {
			t.Skip(tc.keyEnv + " 未设")
		}
		payload, err := json.Marshal(map[string]string{"api_key": secret})
		if err != nil {
			t.Fatalf("marshal credential payload: %v", err)
		}
		return upstreamE2ECredential{payload: payload}
	default:
		t.Fatalf("e2e case %q 没有 credential env", tc.slug)
		return upstreamE2ECredential{}
	}
}

func assertUpstreamE2ESeedSelectable(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *upstreamE2ESeed) {
	t.Helper()

	resolved, err := registry.NewPostgresRegistry(pgPool, nil).ResolveModel(ctx, seed.testCase.model, seed.tenantID)
	if err != nil {
		t.Fatalf("seed registry resolve %q: %v", seed.testCase.model, err)
	}
	if resolved.ProtocolFamily != seed.testCase.protocolFamily {
		t.Fatalf("resolved protocol_family=%q want %q", resolved.ProtocolFamily, seed.testCase.protocolFamily)
	}
	if resolved.ProviderModelID != seed.testCase.routedModel() {
		t.Fatalf("resolved provider_model_id=%q want %q", resolved.ProviderModelID, seed.testCase.routedModel())
	}
	if len(resolved.PoolCandidates) != 1 || resolved.PoolCandidates[0] != seed.poolGroupID {
		t.Fatalf("resolved pool candidates=%v want [%d]", resolved.PoolCandidates, seed.poolGroupID)
	}

	rows, err := dbbilling.New(pgPool).ListEligibleAccountsByPoolGroup(ctx, dbbilling.ListEligibleAccountsByPoolGroupParams{
		TenantID:                seed.tenantID,
		PoolGroupID:             seed.poolGroupID,
		RequestedModel:          seed.testCase.routedModel(),
		RequestedProtocolFamily: resolved.ProtocolFamily,
		RequiredCapabilities:    []string{},
	})
	if err != nil {
		t.Fatalf("seed selector eligibility query: %v", err)
	}
	got := upstreamE2EEligibleAccountIDs(rows)
	for _, want := range []int64{seed.providerAccountID, seed.failoverProviderAccountID} {
		if want <= 0 {
			continue
		}
		if upstreamE2EAccountIDInRows(rows, want) {
			continue
		}
		t.Fatalf("seed selector eligibility returned accounts %v, want provider_account_id=%d", got, want)
	}

	// 静态 SQL 命中并不等于生产可调度。这里继续跑默认 gate 与真实数据库槽位，
	// 让健康、协议、额度或容量拒绝在发真实请求前给出可判别原因。
	source := dispatcher.NewDBAccountSource(dbbilling.New(pgPool))
	selectionRequest := pool.SelectionRequest{
		TenantID: seed.tenantID, UserID: seed.userID, APIKeyID: seed.apiKeyID,
		PoolGroupID: seed.poolGroupID, RequestedModel: seed.testCase.model,
		ProviderModelID: seed.testCase.routedModel(), ProtocolFamily: resolved.ProtocolFamily,
		RequestID: "e2e-preflight-" + uuid.NewString(),
	}
	accounts, err := source.ListAccounts(ctx, selectionRequest)
	if err != nil {
		t.Fatalf("生产账号源预检失败: %v", err)
	}
	var userGroup string
	if err := pgPool.QueryRow(ctx, `SELECT COALESCE(user_group, '') FROM users WHERE tenant_id=$1 AND id=$2`,
		seed.tenantID, seed.userID).Scan(&userGroup); err != nil {
		t.Fatalf("读取生产用户分组预检输入: %v", err)
	}
	selectionRequest.UserGroup = userGroup
	healthService := channelhealth.NewService(
		channelhealth.NewPostgresStore(pgPool), channelhealth.DefaultPolicy(), nil,
	)
	gates := buildGroupRoutingGates(
		subscriptionenforce.NewPostgresRoutesRepo(pgPool), healthService, nil, nil, nil, zap.NewNop(),
	)
	var bindingID int64
	var maxParallelRequests int64
	var selectionMode string
	if err := pgPool.QueryRow(ctx, `
SELECT id, COALESCE(max_parallel_requests, 0), COALESCE(selection_mode, '')
FROM model_pool_bindings
WHERE tenant_id=$1 AND model_id=$2 AND pool_group_id=$3 AND enabled=true`,
		seed.tenantID, seed.modelID, seed.poolGroupID,
	).Scan(&bindingID, &maxParallelRequests, &selectionMode); err != nil {
		t.Fatalf("读取生产 binding 预检输入: %v", err)
	}
	selectionRequest.BindingID = bindingID
	selectionRequest.MaxParallelRequests = maxParallelRequests
	selectionRequest.SelectionMode = selectionMode

	claimGate := billing.NewClaimGate(pgPool)
	reserved, err := claimGate.Reserve(ctx, billing.ReserveRequest{
		TenantID: seed.tenantID, APIKeyID: seed.apiKeyID, UserID: seed.userID,
		LogicalRequestID: "e2e-selector-preflight-" + uuid.NewString(),
		EndpointFamily:   "chat", NormalizedPayloadHash: "e2e-selector-preflight",
		RequestedModel: seed.testCase.model, PoolingGroupID: seed.poolGroupID,
		BillingPolicyVersion: seed.pricingVersion, RequestClass: "interactive",
		PredictedCost:          decimal.RequireFromString("0.000001"),
		BalanceEnforcementMode: billing.BalanceEnforcementModeMandatory,
	})
	if err != nil {
		t.Fatalf("生产 ClaimGate 预检失败: %v", err)
	}
	selectionRequest.ClaimID = reserved.ClaimID
	selectionRequest.AttemptSeq = int(reserved.AttemptSeq)
	slotManager := dispatcher.NewDBSlotManager(pgPool)
	selector := pool.NewDefaultSelector(source,
		pool.WithGateChain(gates),
		pool.WithSlotManager(slotManager),
		pool.WithClaimGate(pool.NewDBClaimGate(dbbilling.New(pgPool))),
		pool.WithRoutingPolicySource(newBindingRoutingPolicySource(dbbilling.New(pgPool))),
	)
	selected, err := selector.Select(ctx, selectionRequest)
	if err != nil {
		var noCapacity *pool.NoCapacityError
		if errors.As(err, &noCapacity) {
			t.Fatalf("生产默认 gate 预检拒绝账号: family=%s reasons=%v", noCapacity.Exhaustion.Family, noCapacity.Exhaustion.Reasons)
		}
		t.Fatalf("生产默认 gate 预检失败: %v", err)
	}
	if selected == nil || selected.AccountID != seed.providerAccountID {
		t.Fatalf("生产默认 gate 预检选中账号=%v，期望 %d", selected, seed.providerAccountID)
	}
	if selected.Release == nil {
		t.Fatal("生产 claim 选号预检未返回槽位释放函数")
	}
	if err := selected.Release(ctx); err != nil {
		t.Fatalf("生产 claim 选号预检释放失败: %v", err)
	}
	if _, err := pgPool.Exec(ctx, `DELETE FROM balance_holds WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, reserved.ClaimID); err != nil {
		t.Fatalf("清理生产 claim 预检余额占用: %v", err)
	}
	if _, err := pgPool.Exec(ctx, `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, reserved.ClaimID); err != nil {
		t.Fatalf("清理生产 claim 预检槽位: %v", err)
	}
	if _, err := pgPool.Exec(ctx, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1 AND id=$2`, seed.tenantID, reserved.ClaimID); err != nil {
		t.Fatalf("清理生产 claim 预检: %v", err)
	}
	var target *pool.AccountSnapshot
	for _, account := range accounts {
		if account != nil && account.ID == seed.providerAccountID {
			target = account
			break
		}
	}
	if target == nil {
		t.Fatalf("生产账号源缺少 provider_account_id=%d", seed.providerAccountID)
	}
	selectionRequest.ClaimID = 0
	selectionRequest.AttemptSeq = 0
	selectionRequest.MaxParallelRequests = 0
	acquired, err := slotManager.Acquire(ctx, target, selectionRequest)
	if err != nil {
		t.Fatalf("生产数据库槽位预检失败: account=%d cap=%d load=%v err=%v", target.ID, target.MaxConcurrency, target.LoadRate, err)
	}
	if acquired == nil || acquired.Release == nil {
		t.Fatal("生产数据库槽位预检未返回释放函数")
	}
	if err := acquired.Release(ctx); err != nil {
		t.Fatalf("生产数据库槽位预检释放失败: %v", err)
	}
}

func upstreamE2EEligibleAccountIDs(rows []dbbilling.ListEligibleAccountsByPoolGroupRow) []int64 {
	out := make([]int64, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
}

func upstreamE2EAccountIDInRows(rows []dbbilling.ListEligibleAccountsByPoolGroupRow, accountID int64) bool {
	for _, row := range rows {
		if row.ID == accountID {
			return true
		}
	}
	return false
}

func buildUpstreamE2EGateway(t *testing.T) string {
	t.Helper()
	moduleRoot := goModuleRootForUpstreamE2E(t)
	binPath := moduleRoot + "/" + upstreamE2EBinaryName
	stamp := fmt.Sprintf("upstream-e2e-%d", time.Now().UnixNano())
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

func goModuleRootForUpstreamE2E(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	gomod := string(bytes.TrimSpace(out))
	if gomod == "" || gomod == "/dev/null" {
		t.Fatalf("not in a Go module")
	}
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

func reserveUpstreamE2ELocalPort(t *testing.T) string {
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

type upstreamE2EProcesses struct {
	gateway    *exec.Cmd
	sidecar    *exec.Cmd
	socketPath string
}

func startUpstreamE2EGateway(t *testing.T, binPath, dsn, addr string, seed *upstreamE2ESeed) *upstreamE2EProcesses {
	t.Helper()
	sidecarPath := buildUpstreamE2ESidecar(t)
	socketRoot := upstreamE2ERuntimeDir(t)
	if err := os.MkdirAll(socketRoot, 0o700); err != nil {
		t.Fatalf("创建 Rust sidecar 测试目录: %v", err)
	}
	socketPath := filepath.Join(socketRoot, "huakai-tls-e2e-"+uuid.NewString()+".sock")
	sidecar := exec.Command(sidecarPath, socketPath)
	sidecar.Env = upstreamE2EChildEnv()
	sidecarStderr, _ := sidecar.StderrPipe()
	sidecarStdout, _ := sidecar.StdoutPipe()
	if err := sidecar.Start(); err != nil {
		t.Fatalf("启动 Rust TLS sidecar: %v", err)
	}
	go drainUpstreamE2EPipe("sidecar-stderr", sidecarStderr)
	go drainUpstreamE2EPipe("sidecar-stdout", sidecarStdout)
	if err := waitForUpstreamE2ESidecar(sidecarPath, socketPath); err != nil {
		stopUpstreamE2EProcess(sidecar)
		_ = os.Remove(socketPath)
		t.Fatal(err)
	}

	cmd := exec.Command(binPath)
	cmd.Env = append(upstreamE2EChildEnv(),
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
	)
	cmd.Env = append(cmd.Env, seed.testCase.gatewayEnv...)
	stderr, _ := cmd.StderrPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		stopUpstreamE2EProcess(sidecar)
		t.Fatalf("start gateway: %v", err)
	}
	go drainUpstreamE2EPipe("gateway-stderr", stderr)
	go drainUpstreamE2EPipe("gateway-stdout", stdout)
	return &upstreamE2EProcesses{gateway: cmd, sidecar: sidecar, socketPath: socketPath}
}

func buildUpstreamE2ESidecar(t *testing.T) string {
	t.Helper()
	repoRoot := filepath.Dir(goModuleRootForUpstreamE2E(t))
	rustRoot := filepath.Join(repoRoot, "exploratory", "rust-core-gateway", "merged")
	targetRoot := strings.TrimSpace(os.Getenv("CARGO_TARGET_DIR"))
	if targetRoot == "" {
		targetRoot = filepath.Join(rustRoot, "target")
	} else if !filepath.IsAbs(targetRoot) {
		targetRoot = filepath.Join(rustRoot, targetRoot)
	}
	cmd := exec.Command("cargo", "build", "-p", "tls-sidecar")
	cmd.Dir = rustRoot
	cmd.Env = append(upstreamE2EChildEnv(), "CARGO_TARGET_DIR="+targetRoot)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("构建 Rust TLS sidecar: %v", err)
	}
	return filepath.Join(targetRoot, "debug", "tls-sidecar")
}

func upstreamE2ERuntimeDir(t *testing.T) string {
	t.Helper()
	if configured := strings.TrimSpace(os.Getenv("HUAKAI_E2E_RUNTIME_DIR")); configured != "" {
		return configured
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheRoot) == "" {
		t.Fatalf("定位当前用户缓存目录: %v", err)
	}
	return filepath.Join(cacheRoot, "huakai-e2e")
}

func upstreamE2EChildEnv() []string {
	blocked := make(map[string]struct{}, len(upstreamE2ESecretEnvNames))
	for _, name := range upstreamE2ESecretEnvNames {
		blocked[name] = struct{}{}
	}
	env := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		name, _, ok := strings.Cut(item, "=")
		if ok {
			if _, denied := blocked[name]; denied {
				continue
			}
		}
		env = append(env, item)
	}
	return env
}

func waitForUpstreamE2ESidecar(binaryPath, socketPath string) error {
	for i := 0; i < upstreamE2EBootRetries; i++ {
		check := exec.Command(binaryPath, "--check", socketPath)
		if err := check.Run(); err == nil {
			return nil
		}
		time.Sleep(upstreamE2EBootRetryWait)
	}
	return fmt.Errorf("Rust TLS sidecar 未在 %v 内就绪: %s",
		time.Duration(upstreamE2EBootRetries)*upstreamE2EBootRetryWait, socketPath)
}

func drainUpstreamE2EPipe(label string, r io.ReadCloser) {
	if r == nil {
		return
	}
	defer r.Close()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fmt.Printf("[%s] %s\n", label, redactUpstreamE2ESecrets(scanner.Text()))
	}
}

func stopUpstreamE2EGateway(processes *upstreamE2EProcesses) {
	if processes == nil {
		return
	}
	stopUpstreamE2EProcess(processes.gateway)
	stopUpstreamE2EProcess(processes.sidecar)
	if processes.socketPath != "" {
		_ = os.Remove(processes.socketPath)
	}
}

func stopUpstreamE2EProcess(cmd *exec.Cmd) {
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

func waitForUpstreamE2EGateway(t *testing.T, addr string) {
	t.Helper()
	for i := 0; i < upstreamE2EBootRetries; i++ {
		resp, err := http.Get("http://" + addr + "/")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(upstreamE2EBootRetryWait)
	}
	t.Fatalf("gateway did not start listening on %s within %v",
		addr, time.Duration(upstreamE2EBootRetries)*upstreamE2EBootRetryWait)
}

type upstreamE2EChatResult struct {
	statusCode int
	body       []byte
	usage      upstreamE2EUsage
	content    string
	logicalID  string
}

type upstreamE2EUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func postUpstreamE2EChat(t *testing.T, ctx context.Context, client *http.Client, addr string, seed *upstreamE2ESeed, logicalID string) upstreamE2EChatResult {
	t.Helper()
	req, err := newUpstreamE2ERequest(ctx, addr, seed, logicalID)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", req.URL.Path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	result := upstreamE2EChatResult{statusCode: resp.StatusCode, body: raw, logicalID: logicalID}
	if resp.StatusCode == http.StatusOK {
		decodeUpstreamE2ESuccessResponse(t, seed.testCase.normalizedClientShape(), raw, &result)
	}
	return result
}

func newUpstreamE2ERequest(ctx context.Context, addr string, seed *upstreamE2ESeed, logicalID string) (*http.Request, error) {
	shape := seed.testCase.normalizedClientShape()
	path := "/v1/chat/completions"
	bodyPayload := map[string]any{
		"model": seed.testCase.model,
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
		"max_tokens": 256,
		"stream":     false,
	}
	if shape == upstreamE2EClientAnthropic {
		path = "/v1/messages"
	}
	body, err := json.Marshal(bodyPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal chat body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+seed.bearer)
	req.Header.Set("Idempotency-Key", logicalID)
	if shape == upstreamE2EClientAnthropic {
		req.Header.Set("Anthropic-Version", "2023-06-01")
	}
	if seed.testCase.officialClaudeClient {
		req.Header.Set("User-Agent", "claude-cli/2.0.0")
		req.Header.Set("X-App", "cli")
		req.Header.Set("X-Stainless-Lang", "js")
		req.Header.Set("X-Stainless-Runtime", "node")
		req.Header.Set("X-Stainless-Package-Version", "2.0.0")
		req.Header.Set("X-Stainless-Retry-Count", "0")
	}
	return req, nil
}

func decodeUpstreamE2ESuccessResponse(t *testing.T, shape upstreamE2EClientShape, raw []byte, result *upstreamE2EChatResult) {
	t.Helper()
	switch shape {
	case upstreamE2EClientOpenAI:
		var decoded struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Usage upstreamE2EUsage `json:"usage"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decode success response: %v body=%s", err, safeUpstreamE2EBody(raw))
		}
		if len(decoded.Choices) > 0 {
			result.content = decoded.Choices[0].Message.Content
		}
		result.usage = decoded.Usage
	case upstreamE2EClientAnthropic:
		var decoded struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decode anthropic success response: %v body=%s", err, safeUpstreamE2EBody(raw))
		}
		var text strings.Builder
		for _, block := range decoded.Content {
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		result.content = text.String()
		result.usage = upstreamE2EUsage{
			PromptTokens:     decoded.Usage.InputTokens,
			CompletionTokens: decoded.Usage.OutputTokens,
			TotalTokens:      decoded.Usage.InputTokens + decoded.Usage.OutputTokens,
		}
	default:
		t.Fatalf("unsupported e2e client shape %q", shape)
	}
}

func assertUpstreamE2ESuccessResponse(t *testing.T, result upstreamE2EChatResult) {
	t.Helper()
	if result.statusCode != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", result.statusCode, safeUpstreamE2EBody(result.body))
	}
	if result.content == "" {
		t.Fatalf("choices[0].message.content 为空: body=%s", safeUpstreamE2EBody(result.body))
	}
	if result.usage.TotalTokens <= 0 {
		t.Fatalf("usage.total_tokens=%d want >0 body=%s", result.usage.TotalTokens, safeUpstreamE2EBody(result.body))
	}
}

func assertUpstreamE2ESuccessPG(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *upstreamE2ESeed, logicalID string, result upstreamE2EChatResult) int64 {
	t.Helper()
	claimID, actualCost := readCommittedClaimForLogicalID(t, ctx, pgPool, seed, logicalID)
	if actualCost <= 0 {
		t.Fatalf("claim %d actual_cost=%v want >0", claimID, actualCost)
	}
	assertUpstreamE2EUsageRecord(t, ctx, pgPool, claimID, result.usage)
	assertUpstreamE2ECostReceipt(t, ctx, pgPool, seed.tenantID, claimID)
	assertUpstreamE2EQuotaReservation(t, ctx, pgPool, seed.tenantID, claimID)
	assertUpstreamE2ECostQuotaWindow(t, ctx, pgPool, seed, 0)
	return claimID
}

func assertUpstreamE2ECostReceipt(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, tenantID, claimID int64) {
	t.Helper()
	var count int
	if err := pgPool.QueryRow(ctx, `
SELECT count(*)
FROM user_cost_receipts r
JOIN user_cost_receipt_owners o
  ON o.tenant_id = r.tenant_id
 AND o.request_id = r.request_id
 AND o.receipt_sequence = r.receipt_sequence
WHERE r.tenant_id=$1
  AND o.claim_id=$2
  AND octet_length(r.signed_hash) > 0`,
		tenantID, claimID,
	).Scan(&count); err != nil {
		t.Fatalf("读取 claim %d 的用户成本回执: %v", claimID, err)
	}
	if count != 1 {
		t.Fatalf("claim %d 成本回执数量=%d，期望 1", claimID, count)
	}
}

func readCommittedClaimForLogicalID(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *upstreamE2ESeed, logicalID string) (int64, float64) {
	t.Helper()
	var (
		claimID       int64
		status        string
		actualCostRaw string
	)
	if err := pgPool.QueryRow(ctx,
		`SELECT id, status, actual_cost::text
		   FROM billing_ledger_claims
		  WHERE tenant_id=$1 AND api_key_id=$2 AND logical_request_id=$3`,
		seed.tenantID, seed.apiKeyID, logicalID,
	).Scan(&claimID, &status, &actualCostRaw); err != nil {
		t.Fatalf("PG claim for logical_id=%s: %v", logicalID, err)
	}
	if status != "committed" {
		t.Fatalf("claim %d status=%q want committed", claimID, status)
	}
	actualCost := parsePositiveOrZero(t, "actual_cost", actualCostRaw)
	if actualCost <= 0 {
		t.Fatalf("claim %d actual_cost=%s want >0", claimID, actualCostRaw)
	}
	return claimID, actualCost
}

func assertUpstreamE2EUsageRecord(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, claimID int64, usage upstreamE2EUsage) {
	t.Helper()
	var (
		count        int
		tokensInput  int
		tokensOutput int
	)
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*), COALESCE(sum(tokens_input), 0)::int, COALESCE(sum(tokens_output), 0)::int
		   FROM usage_records
		  WHERE claim_id=$1`,
		claimID,
	).Scan(&count, &tokensInput, &tokensOutput); err != nil {
		t.Fatalf("PG usage_records for claim %d: %v", claimID, err)
	}
	if count != 1 {
		t.Fatalf("claim %d usage_records count=%d want 1", claimID, count)
	}
	recordedTotal := tokensInput + tokensOutput
	if recordedTotal <= 0 {
		t.Fatalf("claim %d usage token sum=%d want >0", claimID, recordedTotal)
	}
	if usage.TotalTokens > 0 {
		delta := int(math.Abs(float64(recordedTotal - usage.TotalTokens)))
		tolerance := maxInt(4, usage.TotalTokens/2)
		if delta > tolerance {
			t.Fatalf("claim %d usage token sum=%d response total=%d delta=%d tolerance=%d",
				claimID, recordedTotal, usage.TotalTokens, delta, tolerance)
		}
	}
}

func assertUpstreamE2EQuotaReservation(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, tenantID, claimID int64) {
	t.Helper()
	var (
		status         string
		settledCostRaw string
	)
	if err := pgPool.QueryRow(ctx,
		`SELECT status, settled_cost::text
		   FROM quota_reservations
		  WHERE tenant_id=$1 AND claim_id=$2`,
		tenantID, claimID,
	).Scan(&status, &settledCostRaw); err != nil {
		t.Fatalf("PG quota reservation for claim %d: %v", claimID, err)
	}
	if status != "settled" {
		t.Fatalf("claim %d quota reservation status=%q want settled", claimID, status)
	}
	if got := parsePositiveOrZero(t, "quota settled_cost", settledCostRaw); got <= 0 {
		t.Fatalf("claim %d quota settled_cost=%s want >0", claimID, settledCostRaw)
	}
}

func assertUpstreamE2ECostQuotaWindow(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *upstreamE2ESeed, minSettled float64) float64 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var reservedRaw, settledRaw string
	for {
		if err := pgPool.QueryRow(ctx,
			`SELECT reserved_value::text, settled_value::text
			   FROM quota_windows
			  WHERE tenant_id=$1 AND policy_id=$2
			  ORDER BY window_start DESC
			  LIMIT 1`,
			seed.tenantID, seed.costQuotaPolicyID,
		).Scan(&reservedRaw, &settledRaw); err != nil {
			t.Fatalf("PG cost quota window: %v", err)
		}
		reserved := parsePositiveOrZero(t, "quota reserved_value", reservedRaw)
		settled := parsePositiveOrZero(t, "quota settled_value", settledRaw)
		if reserved == 0 && settled > minSettled {
			return settled
		}
		if time.Now().After(deadline) {
			if reserved != 0 {
				t.Fatalf("quota window reserved_value=%s want 0", reservedRaw)
			}
			t.Fatalf("quota window settled_value=%s want > %v", settledRaw, minSettled)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func runUpstreamE2EConcurrency(t *testing.T, ctx context.Context, client *http.Client, pgPool *pgxpool.Pool, addr string, seed *upstreamE2ESeed) {
	t.Helper()
	beforeSettled := assertUpstreamE2ECostQuotaWindow(t, ctx, pgPool, seed, 0)
	total := seed.testCase.requestCount()

	start := make(chan struct{})
	results := make([]upstreamE2EChatResult, total)
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			logicalID := fmt.Sprintf("concurrency-%02d-%s", i, uuid.NewString())
			results[i] = postUpstreamE2EChat(t, ctx, client, addr, seed, logicalID)
		}()
	}
	close(start)
	wg.Wait()

	successCount := 0
	rateLimitedCount := 0
	for _, result := range results {
		switch result.statusCode {
		case http.StatusOK:
			successCount++
			assertUpstreamE2ESuccessResponse(t, result)
			assertUpstreamE2ESuccessPG(t, ctx, pgPool, seed, result.logicalID, result)
		case http.StatusTooManyRequests:
			rateLimitedCount++
		default:
			t.Fatalf("并发请求 logical_id=%s status=%d body=%s", result.logicalID, result.statusCode, safeUpstreamE2EBody(result.body))
		}
	}
	if successCount == 0 {
		t.Fatalf("并发请求没有任何成功结果; 200=%d 429=%d", successCount, rateLimitedCount)
	}
	if rateLimitedCount == 0 && successCount != total {
		t.Fatalf("并发请求结果不完整: 200=%d 429=%d total=%d", successCount, rateLimitedCount, total)
	}
	afterSettled := assertUpstreamE2ECostQuotaWindow(t, ctx, pgPool, seed, beforeSettled)
	if afterSettled <= beforeSettled {
		t.Fatalf("并发后 quota settled 未累加: before=%v after=%v", beforeSettled, afterSettled)
	}
	waitForUpstreamE2EInFlight(t, ctx, pgPool, seed.providerAccountID, 0)
	waitForUpstreamE2EInFlight(t, ctx, pgPool, seed.failoverProviderAccountID, 0)
}

func waitForUpstreamE2EInFlight(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, providerAccountID int64, want int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last int32
	for {
		if err := pgPool.QueryRow(ctx,
			`SELECT in_flight_count FROM provider_accounts WHERE id=$1`, providerAccountID,
		).Scan(&last); err != nil {
			t.Fatalf("read in_flight_count: %v", err)
		}
		if last == want {
			t.Logf("provider_account_id=%d in_flight_count=%d", providerAccountID, last)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("provider_accounts.in_flight_count=%d want %d", last, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

var upstreamE2EBearerRedactionRE = regexp.MustCompile(`(?i)Bearer[[:space:]]+[A-Za-z0-9._~+/=-]+`)

func safeUpstreamE2EBody(raw []byte) string {
	const maxBodyBytes = 4096
	body := string(raw)
	if len(body) > maxBodyBytes {
		body = body[:maxBodyBytes] + "...<truncated>"
	}
	return redactUpstreamE2ESecrets(body)
}

func redactUpstreamE2ESecrets(s string) string {
	for _, envName := range upstreamE2ESecretEnvNames {
		if secret := strings.TrimSpace(os.Getenv(envName)); secret != "" {
			s = strings.ReplaceAll(s, secret, "<redacted>")
			var fields any
			if json.Unmarshal([]byte(secret), &fields) == nil {
				for _, value := range upstreamE2ECredentialSecrets(fields) {
					s = strings.ReplaceAll(s, value, "<redacted>")
				}
			}
		}
	}
	return upstreamE2EBearerRedactionRE.ReplaceAllString(s, "Bearer <redacted>")
}

func parsePositiveOrZero(t *testing.T, label, raw string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("parse %s=%q: %v", label, raw, err)
	}
	if v < 0 {
		t.Fatalf("%s=%q must not be negative", label, raw)
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
