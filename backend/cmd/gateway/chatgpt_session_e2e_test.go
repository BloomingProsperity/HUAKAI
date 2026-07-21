//go:build e2e_chatgpt_session

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

const (
	chatgptSessionE2EBearerPrefix = "hk_test_"
	chatgptSessionE2EBinaryName   = "gateway-chatgpt-session-e2e.exe"
	chatgptSessionE2EModel        = "gpt-5-codex-e2e"
	chatgptSessionE2EProtocol     = "openai_codex"
	chatgptSessionE2EVendor       = credentialstore.VendorOpenAI
	chatgptSessionE2EAuthMode     = credentialstore.AuthModeChatGPTOAuth
	chatgptSessionE2EToken        = "session-token-e2e-fake"
	chatgptSessionE2EAccountID    = "acct-chatgpt-session-e2e"
	chatgptSessionE2EVersion      = "0.99.0-e2e"
	chatgptSessionE2EDeviceID     = "device-e2e-codex-123"
	chatgptSessionE2EUserAgent    = "codex_cli_rs/0.5.0 (Linux x86_64)"
	chatgptSessionE2EQuotaLimit   = "1000.00000000"

	chatgptSessionE2EBootRetries   = 30
	chatgptSessionE2EBootRetryWait = 200 * time.Millisecond
)

func TestChatGPTSessionRelayE2E_CodexEndpointBaseURL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("HUAKAI_DATABASE_URL"))
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 chatgpt session e2e")
	}
	dsn = useDisposableSpecializedLiveDatabase(t, dsn)

	upstream := newChatGPTSessionE2EFakeUpstream(t)
	defer upstream.Close()

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	pgPool, err := db.Open(setupCtx, db.PoolConfig{DSN: dsn})
	if err != nil {
		setupCancel()
		t.Fatalf("打开 e2e 数据库连接池: %v", err)
	}
	t.Cleanup(pgPool.Close)

	seed := seedChatGPTSessionE2EGraph(t, setupCtx, pgPool, upstream.endpoint())
	setupCancel()

	binPath := buildChatGPTSessionE2EGateway(t)
	defer os.Remove(binPath)

	addr := reserveChatGPTSessionE2ELocalPort(t)
	processes := startChatGPTSessionE2EGateway(t, binPath, dsn, addr, seed)
	t.Cleanup(func() { stopSpecializedLiveProcesses(processes) })
	waitForChatGPTSessionE2EGateway(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 90 * time.Second}
	seed.providerAccountID = importChatGPTSessionE2EAccount(t, ctx, client, addr, seed).AccountID
	assertChatGPTSessionE2ESeedSelectable(t, ctx, pgPool, seed)
	logicalID := "chatgpt-session-" + uuid.NewString()
	result := postChatGPTSessionE2EChat(t, ctx, client, addr, seed, logicalID)
	assertChatGPTSessionE2ESuccessResponse(t, result)
	upstream.assertSingleRequest(t)

	claimID := assertChatGPTSessionE2ESuccessPG(t, ctx, pgPool, seed, logicalID, result)
	assertChatGPTSessionE2EBalanceHoldCaptured(t, ctx, pgPool, claimID)
	assertChatGPTSessionE2ENoLeaks(t, ctx, pgPool, seed)
	waitForChatGPTSessionE2EInFlight(t, ctx, pgPool, seed.providerAccountID, 0)
}

type chatgptSessionE2EFakeUpstream struct {
	server *httptest.Server
	mu     sync.Mutex
	seen   int
	errs   []string
}

func newChatGPTSessionE2EFakeUpstream(t *testing.T) *chatgptSessionE2EFakeUpstream {
	t.Helper()
	f := &chatgptSessionE2EFakeUpstream{}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *chatgptSessionE2EFakeUpstream) Close() {
	if f != nil && f.server != nil {
		f.server.Close()
	}
}

func (f *chatgptSessionE2EFakeUpstream) endpoint() string {
	return f.server.URL + "/backend-api/codex/responses"
}

func (f *chatgptSessionE2EFakeUpstream) handle(w http.ResponseWriter, r *http.Request) {
	rawBody, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	var errs []string
	if r.Method != http.MethodPost {
		errs = append(errs, "method 不是 POST")
	}
	if r.URL.Path != "/backend-api/codex/responses" {
		errs = append(errs, "path 不是 codex responses")
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+chatgptSessionE2EToken {
		errs = append(errs, "Authorization 未按 session token 注入")
	}
	if got := r.Header.Get("Accept"); got != "text/event-stream" {
		errs = append(errs, "Accept 不是 text/event-stream")
	}
	if got := r.Header.Get("OAI-Device-Id"); got != chatgptSessionE2EDeviceID {
		errs = append(errs, "OAI-Device-Id 未按账号 extra 注入")
	}
	if got := r.Header.Get("originator"); got != "codex_cli_rs" {
		errs = append(errs, "originator 未按 Codex 默认值注入")
	}
	if got := r.Header.Get("chatgpt-account-id"); got != chatgptSessionE2EAccountID {
		errs = append(errs, "chatgpt-account-id 未按账号 extra 注入")
	}
	if got := r.Header.Get("version"); got != chatgptSessionE2EVersion {
		errs = append(errs, "version 未按账号 extra 注入")
	}
	ua := r.Header.Get("User-Agent")
	if ua == "" || strings.Contains(strings.ToLower(ua), "mozilla") || !strings.Contains(strings.ToLower(ua), "codex") {
		errs = append(errs, "User-Agent 不是 Codex CLI 风格")
	}
	if got := r.Header.Get("OAI-Language"); got != "en-US" {
		errs = append(errs, "OAI-Language 不是 en-US")
	}
	var body map[string]any
	if err := json.Unmarshal(rawBody, &body); err != nil {
		errs = append(errs, "请求体不是 JSON object")
	} else {
		if body["stream"] != true {
			errs = append(errs, "stream 未强制为 true")
		}
		if body["store"] != false {
			errs = append(errs, "store 未强制为 false")
		}
		if _, ok := body["max_output_tokens"]; ok {
			errs = append(errs, "max_output_tokens 未剥离")
		}
		if body["model"] != chatgptSessionE2EModel {
			errs = append(errs, "model 未保留")
		}
	}

	f.mu.Lock()
	f.seen++
	f.errs = append(f.errs, errs...)
	f.mu.Unlock()

	if len(errs) > 0 {
		http.Error(w, "fake upstream header assertion failed", http.StatusTeapot)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	writeChatGPTSessionE2ESSE(w, "response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":     "resp-chatgpt-session-e2e",
			"model":  chatgptSessionE2EModel,
			"status": "in_progress",
		},
	})
	writeChatGPTSessionE2ESSE(w, "response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": 0,
		"item": map[string]any{
			"id":   "msg_1",
			"type": "message",
			"role": "assistant",
		},
	})
	writeChatGPTSessionE2ESSE(w, "response.output_text.delta", map[string]any{
		"type":          "response.output_text.delta",
		"item_id":       "msg_1",
		"output_index":  0,
		"content_index": 0,
		"delta":         "pong from fake codex responses backend",
	})
	writeChatGPTSessionE2ESSE(w, "response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": 0,
		"item": map[string]any{
			"id":   "msg_1",
			"type": "message",
			"role": "assistant",
			"content": []map[string]string{{
				"type": "output_text",
				"text": "pong from fake codex responses backend",
			}},
		},
	})
	writeChatGPTSessionE2ESSE(w, "response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp-chatgpt-session-e2e",
			"model":  chatgptSessionE2EModel,
			"status": "completed",
			"usage": map[string]int{
				"input_tokens":  7,
				"output_tokens": 5,
				"total_tokens":  12,
			},
			"output": []map[string]any{{
				"id":   "msg_1",
				"type": "message",
				"role": "assistant",
				"content": []map[string]string{{
					"type": "output_text",
					"text": "pong from fake codex responses backend",
				}},
			}},
		},
	})
}

func writeChatGPTSessionE2ESSE(w http.ResponseWriter, event string, payload map[string]any) {
	raw, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (f *chatgptSessionE2EFakeUpstream) assertSingleRequest(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen != 1 {
		t.Fatalf("fake upstream 请求次数=%d want 1; errs=%v", f.seen, f.errs)
	}
	if len(f.errs) > 0 {
		t.Fatalf("fake upstream header 断言失败: %v", f.errs)
	}
}

type chatgptSessionE2ESeed struct {
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
	upstreamEndpoint  string
}

func seedChatGPTSessionE2EGraph(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, upstreamEndpoint string) *chatgptSessionE2ESeed {
	t.Helper()
	unique := uuid.NewString()
	seed := &chatgptSessionE2ESeed{
		pricingVersion:   "e2e-chatgpt-session-" + unique,
		upstreamEndpoint: upstreamEndpoint,
	}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"chatgpt-session-e2e-tenant-"+unique,
	).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "chatgpt-session-e2e-user-"+unique,
	).Scan(&seed.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	seed.bearer = chatgptSessionE2EBearerPrefix + unique
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
		seed.tenantID, seed.userID, "chatgpt-session-e2e-key-"+unique, string(keyHash), keyPrefix,
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

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pgPool.Exec(c, `DELETE FROM quota_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM quota_concurrency_slots WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM quota_concurrency_scope_locks WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM quota_reconciliation_jobs WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM quota_reservations WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM quota_windows WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM quota_policies WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM idempotency_replay_records WHERE tenant_id=$1`, seed.tenantID)
		if err := cleanupSpecializedLiveMoneyRows(c, pgPool, seed.tenantID); err != nil {
			t.Errorf("清理 ChatGPT session 端到端测试钱路记录: %v", err)
		}
		_, _ = pgPool.Exec(c, `DELETE FROM model_pool_bindings WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM model_registry_capabilities WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM model_aliases WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM models WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM model_registry_snapshots WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM model_registry_tenant_policies WHERE tenant_id=$1`, seed.tenantID)
		if err := cleanupSpecializedLiveSubscriptionObservations(c, pgPool, seed.tenantID); err != nil {
			t.Errorf("清理 ChatGPT session 端到端测试套餐观测: %v", err)
		}
		_, _ = pgPool.Exec(c, `DELETE FROM channel_health_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM channel_health_state WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM credential_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM account_credentials WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM channels WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM providers WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM user_balances WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM tenant_admin_capability_grants WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM admin_tokens WHERE id=$1`, seed.adminTokenID)
		if result, err := pgPool.Exec(c, `DELETE FROM tenants WHERE id=$1`, seed.tenantID); err != nil {
			t.Errorf("删除 ChatGPT session 端到端测试租户: %v", err)
		} else if result.RowsAffected() != 1 {
			t.Errorf("删除 ChatGPT session 端到端测试租户影响行数=%d，期望 1", result.RowsAffected())
		}
		_, _ = pgPool.Exec(c, `DELETE FROM billing_pricing_versions WHERE tenant_id=0 AND version=$1`, seed.pricingVersion)
	})

	if _, err := pgPool.Exec(ctx,
		`INSERT INTO tenants (id, name, status, created_at, updated_at)
		 VALUES (0, 'public-pricing', 'active', now(), now())
		 ON CONFLICT (id) DO NOTHING`,
	); err != nil {
		t.Fatalf("seed public pricing tenant: %v", err)
	}
	pricingData := fmt.Sprintf(
		`{"providers":{"codex":{"models":{"%[1]s":{"input_micro_usd":"1","output_micro_usd":"2","cache_read_micro_usd":"1"}}},"openai":{"models":{"%[1]s":{"input_micro_usd":"1","output_micro_usd":"2","cache_read_micro_usd":"1"}}},"openai_codex":{"models":{"%[1]s":{"input_micro_usd":"1","output_micro_usd":"2","cache_read_micro_usd":"1"}}}}}`,
		chatgptSessionE2EModel,
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
		seed.pricingVersion, pricingData, "e2e:chatgpt-session",
	); err != nil {
		t.Fatalf("seed billing pricing version: %v", err)
	}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		seed.tenantID, chatgptSessionE2EVendor, "chatgpt session e2e "+unique, chatgptSessionE2EProtocol,
	).Scan(&seed.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "chatgpt-session-e2e-pg-"+unique,
	).Scan(&seed.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		seed.tenantID, seed.poolGroupID, "chatgpt-session-e2e-ch-"+unique,
	).Scan(&seed.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	seed.adminBearer, seed.adminTokenID = seedSpecializedLiveImportAuthorization(
		t, ctx, pgPool, seed.tenantID, "chatgpt-session-e2e-admin-"+unique,
	)

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO models (tenant_id, scope, canonical_id, protocol_family,
		                     default_provider_model_id, default_context_window, status)
		 VALUES ($1, 'tenant', $2, $3, $4, 128000, 'active')
		 RETURNING id`,
		seed.tenantID, chatgptSessionE2EModel, chatgptSessionE2EProtocol, chatgptSessionE2EModel,
	).Scan(&seed.modelID); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO model_aliases (tenant_id, scope, model_id,
		                            public_alias_normalized, public_alias_display, status)
		 VALUES ($1, 'tenant', $2, $3, $4, 'active')
		 RETURNING id`,
		seed.tenantID, seed.modelID, chatgptSessionE2EModel, chatgptSessionE2EModel,
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
		seed.tenantID, strconv.FormatInt(seed.userID, 10), chatgptSessionE2EQuotaLimit,
	).Scan(&seed.costQuotaPolicyID); err != nil {
		t.Fatalf("seed cost quota policy: %v", err)
	}
	return seed
}

func importChatGPTSessionE2EAccount(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	addr string,
	seed *chatgptSessionE2ESeed,
) specializedLiveImportResult {
	t.Helper()
	extra := map[string]string{
		"base_url":      seed.upstreamEndpoint,
		"oai_device_id": chatgptSessionE2EDeviceID,
		"user_agent":    chatgptSessionE2EUserAgent,
		"account_id":    chatgptSessionE2EAccountID,
		"codex_version": chatgptSessionE2EVersion,
		"originator":    "codex_cli_rs",
		"oai_country":   "US",
	}
	extraJSON, err := json.Marshal(extra)
	if err != nil {
		t.Fatalf("编码 ChatGPT session 账号运行元数据: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"session_token": chatgptSessionE2EToken,
		"account_id":    chatgptSessionE2EAccountID,
	})
	if err != nil {
		t.Fatalf("编码 ChatGPT session 正式导入凭据: %v", err)
	}
	capConcurrency := int32(1)
	priority := int32(100)
	request := specializedLiveAccountImportRequest{
		TenantID:        seed.tenantID,
		SourceKind:      intake.SourceJSON,
		DefaultVendor:   chatgptSessionE2EVendor,
		DefaultAuthMode: chatgptSessionE2EAuthMode,
		Content:         string(payload),
		Account: accountintake.AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			ExactName:       "chatgpt-session-e2e-正式导入-" + uuid.NewString(),
			AccountType:     "session",
			CapConcurrency:  &capConcurrency,
			Priority:        &priority,
			Extra:           extraJSON,
			ModelAllowList:  []string{chatgptSessionE2EModel},
			CapabilityFlags: []string{"stream", "tools", "vision", "json", "audio", "file"},
		},
	}
	return executeSpecializedLiveAccountImport(
		t, ctx, client, addr,
		"/admin/v1/credentials/account-imports/plan",
		"/admin/v1/credentials/account-imports/execute",
		seed.adminBearer, request, chatgptSessionE2EToken,
	)
}

func assertChatGPTSessionE2ESeedSelectable(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *chatgptSessionE2ESeed) {
	t.Helper()
	resolved, err := registry.NewPostgresRegistry(pgPool, nil).ResolveModel(ctx, chatgptSessionE2EModel, seed.tenantID)
	if err != nil {
		t.Fatalf("seed registry resolve %q: %v", chatgptSessionE2EModel, err)
	}
	if resolved.ProtocolFamily != chatgptSessionE2EProtocol {
		t.Fatalf("resolved protocol_family=%q want %q", resolved.ProtocolFamily, chatgptSessionE2EProtocol)
	}
	rows, err := dbbilling.New(pgPool).ListEligibleAccountsByPoolGroup(ctx, dbbilling.ListEligibleAccountsByPoolGroupParams{
		TenantID:                seed.tenantID,
		PoolGroupID:             seed.poolGroupID,
		RequestedModel:          chatgptSessionE2EModel,
		RequestedProtocolFamily: resolved.ProtocolFamily,
		RequiredCapabilities:    []string{},
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

func buildChatGPTSessionE2EGateway(t *testing.T) string {
	t.Helper()
	moduleRoot := goModuleRootForChatGPTSessionE2E(t)
	binPath := moduleRoot + "/" + chatgptSessionE2EBinaryName
	stamp := fmt.Sprintf("chatgpt-session-e2e-%d", time.Now().UnixNano())
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

func goModuleRootForChatGPTSessionE2E(t *testing.T) string {
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

func reserveChatGPTSessionE2ELocalPort(t *testing.T) string {
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

func startChatGPTSessionE2EGateway(t *testing.T, binPath, dsn, addr string, seed *chatgptSessionE2ESeed) *specializedLiveProcesses {
	t.Helper()
	sidecar, socketPath := startSpecializedLiveSidecar(t, goModuleRootForChatGPTSessionE2E(t))
	cmd := exec.Command(binPath)
	cmd.Env = append(specializedLiveChildEnv(),
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
	stderr, _ := cmd.StderrPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		stopSpecializedLiveProcess(sidecar)
		_ = os.Remove(socketPath)
		t.Fatalf("start gateway: %v", err)
	}
	go drainChatGPTSessionE2EPipe("gateway-stderr", stderr)
	go drainChatGPTSessionE2EPipe("gateway-stdout", stdout)
	return &specializedLiveProcesses{gateway: cmd, sidecar: sidecar, socketPath: socketPath}
}

func drainChatGPTSessionE2EPipe(label string, r io.ReadCloser) {
	if r == nil {
		return
	}
	defer r.Close()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fmt.Printf("[%s] %s\n", label, redactChatGPTSessionE2ESecrets(scanner.Text()))
	}
}

func waitForChatGPTSessionE2EGateway(t *testing.T, addr string) {
	t.Helper()
	for i := 0; i < chatgptSessionE2EBootRetries; i++ {
		resp, err := http.Get("http://" + addr + "/")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(chatgptSessionE2EBootRetryWait)
	}
	t.Fatalf("gateway did not start listening on %s within %v",
		addr, time.Duration(chatgptSessionE2EBootRetries)*chatgptSessionE2EBootRetryWait)
}

type chatgptSessionE2EChatResult struct {
	statusCode int
	body       []byte
	usage      chatgptSessionE2EUsage
	content    string
	logicalID  string
}

type chatgptSessionE2EUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func postChatGPTSessionE2EChat(t *testing.T, ctx context.Context, client *http.Client, addr string, seed *chatgptSessionE2ESeed, logicalID string) chatgptSessionE2EChatResult {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model":             chatgptSessionE2EModel,
		"instructions":      "Reply briefly.",
		"input":             "ping",
		"stream":            true,
		"store":             true,
		"max_output_tokens": 16,
	})
	if err != nil {
		t.Fatalf("marshal responses body: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+seed.bearer)
	req.Header.Set("Idempotency-Key", logicalID)
	req.Header.Set("User-Agent", chatgptSessionE2EUserAgent)
	req.Header.Set("X-Client-Name", "codex-cli")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/responses: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	result := chatgptSessionE2EChatResult{statusCode: resp.StatusCode, body: raw, logicalID: logicalID}
	if resp.StatusCode == http.StatusOK {
		result.content, result.usage = parseChatGPTSessionE2EResponsesSSE(t, raw)
	}
	return result
}

func parseChatGPTSessionE2EResponsesSSE(t *testing.T, raw []byte) (string, chatgptSessionE2EUsage) {
	t.Helper()
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	var currentEvent string
	var content strings.Builder
	var usage chatgptSessionE2EUsage
	seenCreated := false
	seenCompleted := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "event:"):
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			if currentEvent == "response.created" {
				seenCreated = true
			}
			if currentEvent == "response.completed" {
				seenCompleted = true
			}
		case strings.HasPrefix(line, "data:"):
			var evt struct {
				Type     string `json:"type"`
				Delta    string `json:"delta"`
				Response struct {
					Usage chatgptSessionE2EUsage `json:"usage"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &evt); err != nil {
				t.Fatalf("decode responses SSE data: %v line=%s body=%s", err, line, safeChatGPTSessionE2EBody(raw))
			}
			if evt.Type == "response.output_text.delta" {
				content.WriteString(evt.Delta)
			}
			if evt.Type == "response.completed" || currentEvent == "response.completed" {
				usage = evt.Response.Usage
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan responses SSE: %v", err)
	}
	if !seenCreated || !seenCompleted {
		t.Fatalf("Responses SSE 缺关键事件: created=%v completed=%v body=%s", seenCreated, seenCompleted, safeChatGPTSessionE2EBody(raw))
	}
	return content.String(), usage
}

func assertChatGPTSessionE2ESuccessResponse(t *testing.T, result chatgptSessionE2EChatResult) {
	t.Helper()
	if result.statusCode != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", result.statusCode, safeChatGPTSessionE2EBody(result.body))
	}
	if result.content == "" {
		t.Fatalf("choices[0].message.content 为空: body=%s", safeChatGPTSessionE2EBody(result.body))
	}
	if result.usage.TotalTokens <= 0 {
		t.Fatalf("usage.total_tokens=%d want >0 body=%s", result.usage.TotalTokens, safeChatGPTSessionE2EBody(result.body))
	}
}

func assertChatGPTSessionE2ESuccessPG(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *chatgptSessionE2ESeed, logicalID string, result chatgptSessionE2EChatResult) int64 {
	t.Helper()
	claimID, actualCost := readChatGPTSessionE2ECommittedClaim(t, ctx, pgPool, seed, logicalID)
	if actualCost <= 0 {
		t.Fatalf("claim %d actual_cost=%v want >0", claimID, actualCost)
	}
	assertChatGPTSessionE2EUsageRecord(t, ctx, pgPool, claimID, result.usage)
	assertChatGPTSessionE2ECostReceipt(t, ctx, pgPool, seed.tenantID, claimID)
	assertChatGPTSessionE2EQuotaReservation(t, ctx, pgPool, seed.tenantID, claimID)
	assertChatGPTSessionE2ECostQuotaWindow(t, ctx, pgPool, seed, 0)
	return claimID
}

func assertChatGPTSessionE2ECostReceipt(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, tenantID, claimID int64) {
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
		t.Fatalf("读取 claim %d 的用户成本凭证: %v", claimID, err)
	}
	if count != 1 {
		t.Fatalf("claim %d 成本凭证数量=%d，期望 1", claimID, count)
	}
}

func readChatGPTSessionE2ECommittedClaim(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *chatgptSessionE2ESeed, logicalID string) (int64, float64) {
	t.Helper()
	var claimID int64
	var status string
	var actualCostRaw string
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := pgPool.QueryRow(ctx,
			`SELECT id, status, COALESCE(actual_cost, 0)::text
			   FROM billing_ledger_claims
			  WHERE tenant_id=$1 AND api_key_id=$2 AND logical_request_id=$3`,
			seed.tenantID, seed.apiKeyID, logicalID,
		).Scan(&claimID, &status, &actualCostRaw); err != nil {
			t.Fatalf("PG claim for logical_id=%s: %v", logicalID, err)
		}
		if status == "committed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("claim %d logical_id=%s status=%q want committed", claimID, logicalID, status)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return claimID, parseChatGPTSessionE2ENonNegativeFloat(t, "actual_cost", actualCostRaw)
}

func assertChatGPTSessionE2EUsageRecord(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, claimID int64, usage chatgptSessionE2EUsage) {
	t.Helper()
	var count, tokensInput, tokensOutput int
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
		tolerance := maxChatGPTSessionE2EInt(4, usage.TotalTokens/2)
		if delta > tolerance {
			t.Fatalf("claim %d usage token sum=%d response total=%d delta=%d tolerance=%d",
				claimID, recordedTotal, usage.TotalTokens, delta, tolerance)
		}
	}
}

func assertChatGPTSessionE2EQuotaReservation(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, tenantID, claimID int64) {
	t.Helper()
	var status string
	var settledCostRaw string
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
	if got := parseChatGPTSessionE2ENonNegativeFloat(t, "quota settled_cost", settledCostRaw); got <= 0 {
		t.Fatalf("claim %d quota settled_cost=%s want >0", claimID, settledCostRaw)
	}
}

func assertChatGPTSessionE2ECostQuotaWindow(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *chatgptSessionE2ESeed, minSettled float64) {
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
		reserved := parseChatGPTSessionE2ENonNegativeFloat(t, "quota reserved_value", reservedRaw)
		settled := parseChatGPTSessionE2ENonNegativeFloat(t, "quota settled_value", settledRaw)
		if reserved == 0 && settled > minSettled {
			return
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

func assertChatGPTSessionE2EBalanceHoldCaptured(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, claimID int64) {
	t.Helper()
	var state string
	if err := pgPool.QueryRow(ctx,
		`SELECT state FROM balance_holds WHERE claim_id=$1`, claimID,
	).Scan(&state); err != nil {
		t.Fatalf("read balance hold claim=%d: %v", claimID, err)
	}
	if state != "captured" {
		t.Fatalf("balance hold claim=%d state=%q want captured", claimID, state)
	}
}

func assertChatGPTSessionE2ENoLeaks(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *chatgptSessionE2ESeed) {
	t.Helper()
	assertChatGPTSessionE2ECount(t, ctx, pgPool,
		`SELECT count(*) FROM balance_holds WHERE tenant_id=$1 AND state='held'`,
		0, "held balance_holds after completion", seed.tenantID)
	assertChatGPTSessionE2ECount(t, ctx, pgPool,
		`SELECT count(*) FROM pool_slot_acquisitions WHERE tenant_id=$1 AND status='acquired'`,
		0, "acquired pool slots after completion", seed.tenantID)
}

func assertChatGPTSessionE2ECount(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, query string, want int, label string, args ...any) {
	t.Helper()
	var got int
	if err := pgPool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("read %s: %v", label, err)
	}
	if got != want {
		t.Fatalf("%s=%d want %d", label, got, want)
	}
}

func waitForChatGPTSessionE2EInFlight(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, providerAccountID int64, want int32) {
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
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("provider_accounts.in_flight_count=%d want %d", last, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

var chatgptSessionE2EBearerRedactionRE = regexp.MustCompile(`(?i)Bearer[[:space:]]+[A-Za-z0-9._~+/=-]+`)

func safeChatGPTSessionE2EBody(raw []byte) string {
	const maxBodyBytes = 4096
	body := string(raw)
	if len(body) > maxBodyBytes {
		body = body[:maxBodyBytes] + "...<truncated>"
	}
	return redactChatGPTSessionE2ESecrets(body)
}

func redactChatGPTSessionE2ESecrets(s string) string {
	s = strings.ReplaceAll(s, chatgptSessionE2EToken, "<redacted>")
	return chatgptSessionE2EBearerRedactionRE.ReplaceAllString(s, "Bearer <redacted>")
}

func parseChatGPTSessionE2ENonNegativeFloat(t *testing.T, label, raw string) float64 {
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

func maxChatGPTSessionE2EInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
