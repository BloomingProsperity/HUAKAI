//go:build integration_pg

package requesttracedb_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	rootdb "github.com/BloomingProsperity/HUAKAI/internal/db"
	. "github.com/BloomingProsperity/HUAKAI/internal/db/requesttracedb"
	"github.com/BloomingProsperity/HUAKAI/internal/requesttracehttp"
)

// traceFixture 是一条 3 次尝试(2 次中止、1 次成功)的请求及其相关事实。
type traceFixture struct {
	tenantID, otherTenantID int64
	userID, otherUserID     int64
	apiKeyID                int64
	poolID                  int64
	accounts                []int64
	claimID                 int64
	requestID               string // 账本逻辑标识(幂等键)
	httpRequestID           string // 网关 HTTP 请求标识(响应头 / 收据 / 审计)
	now                     time.Time
}

func openTracePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	p, err := rootdb.Open(ctx, rootdb.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func seedTraceFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) traceFixture {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	fx := traceFixture{now: time.Now().UTC().Truncate(time.Microsecond), requestID: "idem-trace-" + suffix, httpRequestID: "http-trace-" + suffix}
	scan := func(dst *int64, sql string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql[:50], err)
		}
	}
	scan(&fx.tenantID, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "trace-"+suffix)
	scan(&fx.otherTenantID, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "trace-other-"+suffix)
	scan(&fx.userID, `INSERT INTO users (tenant_id, email, display_name) VALUES ($1, $2, 'trace user') RETURNING id`, fx.tenantID, "trace-"+suffix+"@example.test")
	scan(&fx.otherUserID, `INSERT INTO users (tenant_id, email, display_name) VALUES ($1, $2, 'other user') RETURNING id`, fx.tenantID, "trace-other-"+suffix+"@example.test")
	scan(&fx.apiKeyID, `INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix) VALUES ($1, $2, 'trace key', $3, 'hk_tr') RETURNING id`, fx.tenantID, fx.userID, "hash-"+suffix)
	var providerID, channelID int64
	scan(&providerID, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, 'Trace Provider', 'openai_chat') RETURNING id`, fx.tenantID, "tr-"+suffix)
	scan(&fx.poolID, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, fx.tenantID, "tr-pool-"+suffix)
	scan(&channelID, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, fx.tenantID, fx.poolID, "tr-chan-"+suffix)
	for i := 0; i < 3; i++ {
		var id int64
		scan(&id, `INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, enabled, health_state)
			VALUES ($1, $2, $3, $4, 'api_key', true, 'healthy') RETURNING id`, fx.tenantID, providerID, channelID, fmt.Sprintf("tr-acct-%s-%d", suffix, i))
		fx.accounts = append(fx.accounts, id)
	}
	scan(&fx.claimID, `INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id, logical_request_id, endpoint_family,
			requested_model, pooling_group_id, attempt_seq, billing_policy_version, request_class, provider_account_id,
			predicted_cost, actual_cost, currency_code, status, aborted_reason, reserved_at, settled_at, lease_expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'chat', 'trace-model', $7, 3, 'bp-test', 'standard', $8,
			0.01, 0.0042, 'USD', 'committed', 'upstream_error_5xx', $9, $10, $11) RETURNING id`,
		fx.tenantID, "idem-"+suffix, "fp-"+suffix, fx.apiKeyID, fx.userID, fx.requestID, fx.poolID, fx.accounts[2],
		fx.now.Add(-time.Minute), fx.now, fx.now.Add(time.Hour))
	// 尝试 1:5xx 部分流(有用量);尝试 2:零用量中止(只留账本事件);尝试 3:成功。
	for _, u := range []struct {
		seq     int32
		account int64
		end     string
		src     string
		out     int32
		cost    string
		at      time.Duration
	}{
		{1, fx.accounts[0], "upstream_error_5xx", "partial", 5, "0.0002", -50 * time.Second},
		{3, fx.accounts[2], "stream_end_graceful", "reported", 300, "0.004", -5 * time.Second},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO usage_records (
				tenant_id, claim_id, api_key_id, user_id, provider_account_id, acquisition_token, attempt_seq,
				tokens_input, tokens_output, actual_cost, input_cost, output_cost, end_class, usage_source,
				pending_reconciliation, routing_reason, requested_at, upstream_request_at, settled_at, requested_model,
				upstream_model, stream, settlement_source
			) VALUES ($1, $2, $3, $4, $5, gen_random_uuid(), $6, 100, $7, $8, 0.0001, $8, $9, $10, $11,
				$12::jsonb, $13, $14, $15, 'trace-model', 'trace-model-2026', true, 'provider_upstream')`,
			fx.tenantID, fx.claimID, fx.apiKeyID, fx.userID, u.account, u.seq, u.out, u.cost, u.end, u.src, u.src == "partial",
			fmt.Sprintf(`{"selection_layer":"fresh","retry_attempt_number":%d,"candidate_counts_by_exclusion":{"health":1},"per_request_exclusion_summary":[{"account_id":%d,"reason":"health"}]}`, u.seq, fx.accounts[1]),
			fx.now.Add(u.at), fx.now.Add(u.at+time.Second), fx.now.Add(u.at+10*time.Second)); err != nil {
			t.Fatalf("insert usage attempt %d: %v", u.seq, err)
		}
	}
	for _, be := range []struct {
		typ  string
		end  string
		cost string
		at   time.Duration
	}{
		{"claim_aborted", "upstream_error_5xx", "0", -40 * time.Second},
		{"claim_aborted", "upstream_rate_limit", "0", -20 * time.Second},
		{"claim_committed", "stream_end_graceful", "0.0042", 5 * time.Second},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO billing_events (tenant_id, claim_id, event_type, actual_cost, actual_cost_signed, end_class, usage_source, fingerprint, audit_request_id, occurred_at)
			VALUES ($1, $2, $3, $4, $4, $5, 'reported', $6, $7, $8)`, fx.tenantID, fx.claimID, be.typ, be.cost, be.end, "fp-"+suffix, fx.httpRequestID, fx.now.Add(be.at)); err != nil {
			t.Fatalf("insert billing event %s: %v", be.typ, err)
		}
	}
	var credID int64
	scan(&credID, `INSERT INTO account_credentials (tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
			encrypted_payload, key_id, nonce, aad_hash) VALUES ($1, $2, 'openai', 'api_key', 'active', 1, $3, 'test-key', $4, $5) RETURNING id`,
		fx.tenantID, fx.accounts[0], []byte("ciphertext"), []byte("nonce-12345678"), "aad-trace-"+suffix)
	if _, err := pool.Exec(ctx, `INSERT INTO channel_health_audit_events (tenant_id, event_type, channel_id, vendor, provider_account_id,
			account_credential_id, credential_version, previous_state, new_state, reason_class, policy_version, request_id, occurred_at)
		VALUES ($1, 'channel_health_degraded', $2, 'openai', $3, $4, 1, 'active', 'degraded', 'upstream_5xx', 'test', $5, $6)`,
		fx.tenantID, strconv.FormatInt(channelID, 10), fx.accounts[0], credID, fx.httpRequestID, fx.now.Add(-39*time.Second)); err != nil {
		t.Fatalf("insert channel health audit: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pool_routing_audit_events (tenant_id, event_type, pool_group_id, provider_account_id, request_id, reason, created_at)
		VALUES ($1, 'pool_exhausted', $2, NULL, $3, 'all candidates excluded', $4)`, fx.tenantID, fx.poolID, fx.httpRequestID, fx.now.Add(-19*time.Second)); err != nil {
		t.Fatalf("insert pool routing audit: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO rate_limit_audit_events (tenant_id, provider_account_id, event_type, rate_limit_reason, upstream_status_code, upstream_request_id, occurred_at)
		VALUES ($1, $2, 'rate_limited_set', 'upstream_429', 429, $3, $4)`, fx.tenantID, fx.accounts[1], fx.httpRequestID, fx.now.Add(-21*time.Second)); err != nil {
		t.Fatalf("insert rate limit audit: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO oauth_refresh_audit_events (tenant_id, provider_account_id, outcome, request_id, occurred_at)
		VALUES ($1, $2, 'oauth_401_force_refresh', $3, $4)`, fx.tenantID, fx.accounts[0], fx.httpRequestID, fx.now.Add(-38*time.Second)); err != nil {
		t.Fatalf("insert oauth refresh audit: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO audit_ledger_entries (ledger_id, request_id, tenant_id, hop_chain, model_chain, prev_merkle_root, merkle_root, pubkey_fingerprint, signature, occurred_at)
		VALUES ($1, $2, $3, '[{"hop":"upstream"}]'::jsonb, '["trace-model","trace-model-2026"]'::jsonb, $4, $5, '0123456789abcdef', 'sig', $6)`,
		"ledger-"+suffix, fx.httpRequestID, fx.tenantID, make([]byte, 32), make([]byte, 32), fx.now.Add(6*time.Second)); err != nil {
		t.Fatalf("insert audit ledger entry: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_cost_receipts (tenant_id, request_id, model, input_tokens, output_tokens, cached_tokens, cost_usd_micros, rate_table_snapshot_id, signer_fingerprint, signed_hash, created_at)
		VALUES ($1, $2, 'trace-model', 200, 305, 20, 4200, 1, $3, $4, $5)`, fx.tenantID, fx.httpRequestID, []byte("fp"), []byte("hash"), fx.now.Add(7*time.Second)); err != nil {
		t.Fatalf("insert user cost receipt: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// usage_records / billing_events / user_cost_receipts 由数据库触发器保证 append-only,不能删除;
		// 它们引用的租户、claim 与账号也就无法删除。integration_pg 为每个包克隆纯净库,残留只影响本库,
		// 这里只清理可删的审计事件,让重复运行同一库时断言仍以 fixture 自己的标识收敛。
		for _, tenant := range []int64{fx.tenantID, fx.otherTenantID} {
			_, _ = pool.Exec(ctx, `DELETE FROM audit_ledger_entries WHERE tenant_id = $1`, tenant)
			_, _ = pool.Exec(ctx, `DELETE FROM oauth_refresh_audit_events WHERE tenant_id = $1`, tenant)
			_, _ = pool.Exec(ctx, `DELETE FROM rate_limit_audit_events WHERE tenant_id = $1`, tenant)
			_, _ = pool.Exec(ctx, `DELETE FROM pool_routing_audit_events WHERE tenant_id = $1`, tenant)
			_, _ = pool.Exec(ctx, `DELETE FROM channel_health_audit_events WHERE tenant_id = $1`, tenant)
		}
	})
	return fx
}

// TestRequestTraceQueriesAssembleTimeline:五条查询在真实 schema 上按网关标识拼出时间线——
// 头部(池名、最后一跳、状态)、已结算尝试按 attempt_seq 升序且含账号代号/厂商/渠道、账本事件 3 条、
// 四类审计事件按时间升序且带账号、收据摘要;租户/用户谓词收敛(他租户 / 他用户 → no rows;
// 部署者 tenant_id=0 跨租户可查);删除用量后头部与账本仍在(明细过期降级的数据基础)。
// 两把请求标识(账本逻辑标识 vs 网关 HTTP 标识)在 fixture 中刻意不同:头部任一可查并回传 HTTP 标识,
// 审计/收据按 HTTP 标识落账、只带逻辑标识查不到。
// 变异守卫:去掉任一 UNION 分支 → 审计事件计数红;去掉租户谓词 → 他租户断言红;
// 用量排序改成 DESC → 第一条尝试断言红;头部去掉 audit_request_id 分支 → 按 HTTP 标识查找断言红。
func TestRequestTraceQueriesAssembleTimeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openTracePool(t, ctx)
	fx := seedTraceFixture(t, ctx, pool)
	q := New(pool)

	claim, err := q.GetRequestTraceClaim(ctx, GetRequestTraceClaimParams{RequestID: fx.requestID, TenantID: fx.tenantID, UserID: 0})
	if err != nil {
		t.Fatalf("GetRequestTraceClaim: %v", err)
	}
	if claim.ID != fx.claimID || claim.PoolName == nil || *claim.PoolName == "" || claim.ProviderAccountID == nil || *claim.ProviderAccountID != fx.accounts[2] ||
		claim.Status != "committed" || claim.AttemptSeq != 3 || !claim.ActualCost.Valid {
		t.Fatalf("头部不符: %+v", claim)
	}
	if len(claim.HttpRequestIds) != 1 || claim.HttpRequestIds[0] != fx.httpRequestID {
		t.Fatalf("头部必须回传账本事件记录的 HTTP 请求标识: %v", claim.HttpRequestIds)
	}
	// 用响应头里的 HTTP 请求标识也必须找到同一 claim(两把键任一可查)。
	if byHTTP, err := q.GetRequestTraceClaim(ctx, GetRequestTraceClaimParams{RequestID: fx.httpRequestID, TenantID: fx.tenantID}); err != nil || byHTTP.ID != fx.claimID {
		t.Fatalf("按 HTTP 请求标识应找到同一 claim: %+v err=%v", byHTTP, err)
	}
	if _, err := q.GetRequestTraceClaim(ctx, GetRequestTraceClaimParams{RequestID: fx.httpRequestID, TenantID: fx.otherTenantID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("按 HTTP 标识的他租户查询也必须查不到: %v", err)
	}
	ids := []string{fx.requestID, fx.httpRequestID}
	if _, err := q.GetRequestTraceClaim(ctx, GetRequestTraceClaimParams{RequestID: fx.requestID, TenantID: fx.otherTenantID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("他租户必须查不到: %v", err)
	}
	if _, err := q.GetRequestTraceClaim(ctx, GetRequestTraceClaimParams{RequestID: fx.requestID, TenantID: fx.tenantID, UserID: fx.otherUserID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("他用户必须查不到: %v", err)
	}
	if own, err := q.GetRequestTraceClaim(ctx, GetRequestTraceClaimParams{RequestID: fx.requestID, TenantID: fx.tenantID, UserID: fx.userID}); err != nil || own.ID != fx.claimID {
		t.Fatalf("本人应查到: %+v err=%v", own, err)
	}
	if all, err := q.GetRequestTraceClaim(ctx, GetRequestTraceClaimParams{RequestID: fx.requestID, TenantID: 0}); err != nil || all.TenantID != fx.tenantID {
		t.Fatalf("部署者跨租户应查到并回传所属租户: %+v err=%v", all, err)
	}

	usage, err := q.ListRequestTraceUsageRecords(ctx, ListRequestTraceUsageRecordsParams{ClaimID: fx.claimID, TenantID: fx.tenantID})
	if err != nil || len(usage) != 2 {
		t.Fatalf("用量行数=%d err=%v", len(usage), err)
	}
	if usage[0].AttemptSeq != 1 || usage[0].EndClass != "upstream_error_5xx" || usage[0].ProviderAccountID == nil || *usage[0].ProviderAccountID != fx.accounts[0] ||
		usage[0].ProviderAccountName == nil || usage[0].ProviderCode == nil || usage[0].ChannelID == nil || !usage[0].PendingReconciliation {
		t.Fatalf("第一次尝试不符: %+v", usage[0])
	}
	if usage[1].AttemptSeq != 3 || usage[1].EndClass != "stream_end_graceful" || usage[1].TokensOutput != 300 || usage[1].ActualCost.String() != "0.004" {
		t.Fatalf("第三次尝试不符: %+v", usage[1])
	}
	if other, _ := q.ListRequestTraceUsageRecords(ctx, ListRequestTraceUsageRecordsParams{ClaimID: fx.claimID, TenantID: fx.otherTenantID}); len(other) != 0 {
		t.Fatalf("他租户谓词必须过滤用量: %d", len(other))
	}

	billing, err := q.ListRequestTraceBillingEvents(ctx, ListRequestTraceBillingEventsParams{ClaimID: fx.claimID, TenantID: fx.tenantID})
	if err != nil || len(billing) != 3 || billing[0].EventType != "claim_aborted" || billing[2].EventType != "claim_committed" || billing[2].ActualCostSigned.String() != "0.0042" {
		t.Fatalf("账本事件不符: %+v err=%v", billing, err)
	}

	audit, err := q.ListRequestTraceAuditEvents(ctx, ListRequestTraceAuditEventsParams{RequestIds: ids, TenantID: fx.tenantID})
	if err != nil || len(audit) != 4 {
		t.Fatalf("审计事件数=%d err=%v: %+v", len(audit), err, audit)
	}
	wantOrder := []string{"channel_health", "oauth_refresh", "rate_limit", "pool_routing"}
	for i, src := range wantOrder {
		if audit[i].Source != src {
			t.Fatalf("审计事件第 %d 条来源=%s,期望 %s(按时间升序): %+v", i, audit[i].Source, src, audit)
		}
	}
	if audit[0].PreviousState != "active" || audit[0].NewState != "degraded" || audit[0].ProviderAccountID == nil || audit[2].Reason != "upstream_429" {
		t.Fatalf("审计事件字段不符: %+v", audit)
	}
	if other, _ := q.ListRequestTraceAuditEvents(ctx, ListRequestTraceAuditEventsParams{RequestIds: ids, TenantID: fx.otherTenantID}); len(other) != 0 {
		t.Fatalf("他租户谓词必须过滤审计事件: %d", len(other))
	}

	// 只带逻辑标识查审计 → 空(它们按 HTTP 标识落账),证明投影必须带上 HTTP 标识。
	if onlyLogical, _ := q.ListRequestTraceAuditEvents(ctx, ListRequestTraceAuditEventsParams{RequestIds: []string{fx.requestID}, TenantID: fx.tenantID}); len(onlyLogical) != 0 {
		t.Fatalf("只带逻辑标识不应命中按 HTTP 标识落账的审计事件: %d", len(onlyLogical))
	}
	receipt, err := q.GetRequestTraceReceipt(ctx, GetRequestTraceReceiptParams{RequestIds: ids, TenantID: fx.tenantID})
	if err != nil || receipt.PubkeyFingerprint != "0123456789abcdef" || len(receipt.ModelChain) == 0 || len(receipt.HopChain) == 0 {
		t.Fatalf("收据不符: %+v err=%v", receipt, err)
	}
	if _, err := q.GetRequestTraceReceipt(ctx, GetRequestTraceReceiptParams{RequestIds: ids, TenantID: fx.otherTenantID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("他租户不得读到收据: %v", err)
	}
	cost, err := q.GetRequestTraceCostReceipt(ctx, GetRequestTraceCostReceiptParams{RequestIds: ids, TenantID: fx.tenantID})
	if err != nil || cost.RequestID != fx.httpRequestID || cost.CostUsdMicros != 4200 || cost.Model != "trace-model" {
		t.Fatalf("用户面费用收据不符: %+v err=%v", cost, err)
	}
	if _, err := q.GetRequestTraceCostReceipt(ctx, GetRequestTraceCostReceiptParams{RequestIds: ids, TenantID: fx.otherTenantID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("他租户不得读到费用收据: %v", err)
	}

	// 只有账本事实、没有任何用量行的请求(异步用量落盘未完成或明细已不在):头部与账本事件仍可查,
	// 用量为空——这是投影层打 final_only / expired 降级标记的数据基础。usage_records 由数据库触发器
	// 保证 append-only,这里用第二条请求模拟而不是删除。
	ledgerOnlyID := fx.requestID + "-ledger-only"
	var ledgerOnlyClaim int64
	if err := pool.QueryRow(ctx, `INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id, logical_request_id, endpoint_family,
			requested_model, pooling_group_id, attempt_seq, billing_policy_version, request_class, provider_account_id,
			predicted_cost, actual_cost, currency_code, status, reserved_at, settled_at, lease_expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'chat', 'trace-model', $7, 1, 'bp-test', 'standard', $8,
			0.01, 0.001, 'USD', 'committed', $9, $10, $11) RETURNING id`,
		fx.tenantID, "idem-lo-"+ledgerOnlyID, "fp-lo-"+ledgerOnlyID, fx.apiKeyID, fx.userID, ledgerOnlyID, fx.poolID, fx.accounts[2],
		fx.now.Add(-time.Minute), fx.now, fx.now.Add(time.Hour)).Scan(&ledgerOnlyClaim); err != nil {
		t.Fatalf("insert ledger-only claim: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO billing_events (tenant_id, claim_id, event_type, actual_cost, actual_cost_signed, end_class, usage_source, fingerprint, occurred_at)
		VALUES ($1, $2, 'claim_committed', 0.001, 0.001, 'non_streaming', 'reported', $3, $4)`, fx.tenantID, ledgerOnlyClaim, "fp-lo-"+ledgerOnlyID, fx.now); err != nil {
		t.Fatalf("insert ledger-only billing event: %v", err)
	}
	head, err := q.GetRequestTraceClaim(ctx, GetRequestTraceClaimParams{RequestID: ledgerOnlyID, TenantID: fx.tenantID})
	if err != nil || head.ID != ledgerOnlyClaim || head.ProviderAccountID == nil {
		t.Fatalf("仅账本的请求头部仍应可查并带最后一跳: %+v err=%v", head, err)
	}
	if usage, _ := q.ListRequestTraceUsageRecords(ctx, ListRequestTraceUsageRecordsParams{ClaimID: ledgerOnlyClaim, TenantID: fx.tenantID}); len(usage) != 0 {
		t.Fatal("仅账本的请求不应有用量行")
	}
	if billing, _ := q.ListRequestTraceBillingEvents(ctx, ListRequestTraceBillingEventsParams{ClaimID: ledgerOnlyClaim, TenantID: fx.tenantID}); len(billing) != 1 || billing[0].EventType != "claim_committed" {
		t.Fatalf("仅账本的请求应有提交事件: %+v", billing)
	}
}

// TestRequestTraceHandlersAgainstRealStore:用真实 sqlc 存储驱动管理面与用户面 handler,证明两层之间的
// 标识语义没有断裂——路径给账本逻辑标识或网关 HTTP 标识都能拿到含审计事件与两份收据的完整时间线;
// 用户面按会话收敛并脱敏;他租户管理员 404。
// 判别:去掉头部查询的 audit_request_id 分支 → 按 HTTP 标识查询红;投影不把 HTTP 标识带进审计查询 →
// has_audit_events 断言红。
func TestRequestTraceHandlersAgainstRealStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openTracePool(t, ctx)
	fx := seedTraceFixture(t, ctx, pool)
	store := New(pool)

	adminRouter := chi.NewRouter()
	adminRouter.Get("/admin/v1/requests/{request_id}/trace", requesttracehttp.NewAdminHandler(requesttracehttp.Deps{
		Auth: staticAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, UserID: 1}}, Store: store,
	}))
	for _, id := range []string{fx.requestID, fx.httpRequestID} {
		rec := httptest.NewRecorder()
		adminRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/requests/"+id+"/trace", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("按 %s 查询状态=%d body=%s", id, rec.Code, rec.Body.String())
		}
		var body requesttracehttp.Response
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Request.LogicalRequestID != fx.requestID || len(body.Request.HTTPRequestIDs) != 1 || body.Request.HTTPRequestIDs[0] != fx.httpRequestID {
			t.Fatalf("两把标识必须都回传: %+v", body.Request)
		}
		if len(body.Attempts) != 2 || !body.Coverage.HasAuditEvents || !body.Coverage.HasReceipt || !body.Coverage.HasCostReceipt {
			t.Fatalf("时间线缺半边: attempts=%d cov=%+v", len(body.Attempts), body.Coverage)
		}
		if body.Coverage.AttemptsDetail != requesttracehttp.AttemptsDetailPartial || body.Coverage.UnknownAttemptAccount != 1 || body.Coverage.AbortedAttempts != 2 || body.Coverage.CommittedAttempts != 1 {
			t.Fatalf("3 次尝试(1 次拿到账号前中止)的降级标记不符: %+v", body.Coverage)
		}
		audit := 0
		for _, ev := range body.Events {
			if ev.Source != "billing" {
				audit++
			}
		}
		if audit != 4 || len(body.Events) != 7 {
			t.Fatalf("事件应含 3 条账本 + 4 条审计: %d/%d", audit, len(body.Events))
		}
	}

	tenantRouter := chi.NewRouter()
	tenantRouter.Get("/admin/v1/requests/{request_id}/trace", requesttracehttp.NewAdminHandler(requesttracehttp.Deps{
		Auth: staticAdminAuth{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: fx.otherTenantID, UserID: 2}}, Store: store,
	}))
	rec := httptest.NewRecorder()
	tenantRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/requests/"+fx.httpRequestID+"/trace", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("他租户管理员应 404: %d %s", rec.Code, rec.Body.String())
	}

	userRouter := chi.NewRouter()
	userRouter.Get("/v1/me/requests/{request_id}/trace", requesttracehttp.NewUserHandler(requesttracehttp.Deps{Store: store}))
	req := httptest.NewRequest(http.MethodGet, "/v1/me/requests/"+fx.httpRequestID+"/trace", nil)
	req = req.WithContext(auth.ContextWithSession(req.Context(), auth.SessionIdentity{TenantID: fx.tenantID, UserID: fx.userID}))
	rec = httptest.NewRecorder()
	userRouter.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("本人用户应 200: %d %s", rec.Code, rec.Body.String())
	}
	var userBody requesttracehttp.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &userBody); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if userBody.Viewer != requesttracehttp.ViewerUser || len(userBody.Attempts) != 2 || userBody.Attempts[0].Account != nil || userBody.Coverage.HasAuditEvents || userBody.CostReceipt == nil {
		t.Fatalf("用户面脱敏或收据不符: %+v", userBody)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/me/requests/"+fx.httpRequestID+"/trace", nil)
	req = req.WithContext(auth.ContextWithSession(req.Context(), auth.SessionIdentity{TenantID: fx.tenantID, UserID: fx.otherUserID}))
	rec = httptest.NewRecorder()
	userRouter.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("他用户应 404: %d", rec.Code)
	}
}

type staticAdminAuth struct{ ident admin.AdminIdentity }

func (a staticAdminAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return a.ident, nil
}
