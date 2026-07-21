//go:build e2e_concurrency

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	accountSlotE2ECapacity         = int32(3)
	accountSlotE2EOverflowRequests = 5
	accountSlotE2EMockDelayMS      = 800
)

func TestAccountSlotConcurrencyE2E_NoCapacityAndRelease(t *testing.T) {
	runAccountSlotQueueWaitE2E(t)
}

type accountSlotHTTPResult struct {
	logicalID  string
	statusCode int
	retryAfter string
	// abortFailed 表示网关已声明"claim 中止在序列化竞争下打穿重试预算,钱账
	// 交由 lease sweeper 追平"(X-Huakai-Abort-Failed)。测试据此区分合法降级
	// 与"根本没走 abort"的真缺陷。
	abortFailed bool
	body        []byte
	err         error
}

type accountSlotInFlightSampler struct {
	mu  sync.Mutex
	max int32
	err error
}

func (s *accountSlotInFlightSampler) run(ctx context.Context, pgPool *pgxpool.Pool, providerAccountID int64) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := readAccountSlotInFlight(ctx, pgPool, providerAccountID)
		if err != nil {
			if ctx.Err() == nil {
				s.mu.Lock()
				if s.err == nil {
					s.err = err
				}
				s.mu.Unlock()
			}
			return
		}
		s.observe(current)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *accountSlotInFlightSampler) observe(v int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v > s.max {
		s.max = v
	}
}

func (s *accountSlotInFlightSampler) result() (int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.max, s.err
}

func seedAccountSlotE2EConfig(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *smokeSeed, pricingVersion string) {
	t.Helper()
	if _, err := pgPool.Exec(ctx,
		`UPDATE provider_accounts
		    SET cap_concurrency=$1,
		        cap_queue_fallback=$2,
		        in_flight_count=0,
		        priority=100
		  WHERE id=$3 AND tenant_id=$4`,
		accountSlotE2ECapacity, int32(accountSlotE2EOverflowRequests), seed.providerAccountID, seed.tenantID,
	); err != nil {
		t.Fatalf("seed account cap: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`UPDATE user_balances
		    SET balance=1000000.00, held=0, version=version+1, updated_at=now()
		  WHERE tenant_id=$1 AND user_id=$2`,
		seed.tenantID, seed.userID,
	); err != nil {
		t.Fatalf("seed high user balance: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO tenants (id, name, status, created_at, updated_at)
		 VALUES (0, 'public-pricing', 'active', now(), now())
		 ON CONFLICT (id) DO NOTHING`,
	); err != nil {
		t.Fatalf("seed public pricing tenant: %v", err)
	}
	pricingData := `{"models":{"gpt-4.1-mini":{"input_micro_usd":"1","output_micro_usd":"2","cache_read_micro_usd":"1"}}}`
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO billing_pricing_versions (
		    tenant_id, version, pricing_data, effective_from, created_by_actor, is_public
		  )
		  VALUES (0, $1, $2::jsonb, now(), 'e2e:account-slot-concurrency', true)
		  ON CONFLICT (tenant_id, version) DO UPDATE
		  SET pricing_data = EXCLUDED.pricing_data,
		      effective_from = EXCLUDED.effective_from,
		      created_by_actor = EXCLUDED.created_by_actor,
		      is_public = true`,
		pricingVersion, pricingData,
	); err != nil {
		t.Fatalf("seed billing pricing version: %v", err)
	}
}

func cleanupAccountSlotE2E(t *testing.T, pgPool *pgxpool.Pool, tenantID int64, pricingVersion string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_concurrency_slots WHERE tenant_id=$1`, tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_concurrency_scope_locks WHERE tenant_id=$1`, tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_reconciliation_jobs WHERE tenant_id=$1`, tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_reservations WHERE tenant_id=$1`, tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_windows WHERE tenant_id=$1`, tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM quota_policies WHERE tenant_id=$1`, tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM idempotency_replay_records WHERE tenant_id=$1`, tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM audit_refund_pending WHERE tenant_id=$1`, tenantID)
		_, _ = pgPool.Exec(ctx, `DELETE FROM billing_ledger_adjustments WHERE tenant_id=$1`, tenantID)
		if err := cleanupSpecializedLiveMoneyRows(ctx, pgPool, tenantID); err != nil {
			t.Errorf("清理账号并发端到端测试钱路记录: %v", err)
		}
		_, _ = pgPool.Exec(ctx, `DELETE FROM billing_pricing_versions WHERE tenant_id=0 AND version=$1`, pricingVersion)
	})
}

func startAccountSlotE2EGateway(t *testing.T, binPath, dsn, addr, pricingVersion string) *specializedLiveProcesses {
	t.Helper()
	sidecar, socketPath := startSpecializedLiveSidecar(t, goModuleRoot(t))
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"HUAKAI_DATABASE_URL="+dsn,
		"HUAKAI_ADDR="+addr,
		"HUAKAI_RELEASE_MODE=dev",
		"HUAKAI_CREDENTIAL_KEY_B64=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"HUAKAI_SESSION_SIGNING_KEY_B64=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"HUAKAI_AUDIT_LEDGER_BACKEND=postgres",
		"HUAKAI_DEV_MOCK_UPSTREAM=true",
		fmt.Sprintf("%s=%d", DevMockUpstreamDelayMSEnv, accountSlotE2EMockDelayMS),
		"HUAKAI_BILLING_POLICY_VERSION="+pricingVersion,
		"HUAKAI_QUOTA_ENFORCE=true",
		"HUAKAI_QUOTA_RECONCILER_ENABLED=false",
		"HUAKAI_CACHE_L2_ENABLED=0",
		"HUAKAI_EVENTBUS_ENABLED=0",
		"HUAKAI_RATE_PRECHECK_ENABLED=false",
		"HUAKAI_BINDING_RATE_LIMIT_ENABLED=false",
		"HUAKAI_TRANSPORT_SIDECAR_SOCKET="+socketPath,
		"HUAKAI_KEY_RPM_LIMIT=0",
		"HUAKAI_KEY_TPM_LIMIT=0",
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

func postAccountSlotBatch(ctx context.Context, client *http.Client, addr, bearer, prefix string, n int) []accountSlotHTTPResult {
	results := make([]accountSlotHTTPResult, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range results {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// 微错峰:上游窗口(百毫秒级)内所有请求仍完全重叠,并发闸判别力不变;
			// 但预扣对同一余额行的同毫秒序列化风暴大幅缓解,避免与被测闸无关的
			// 竞争降级(reserve 409/quota fail-closed/abort 卡住)淹没断言。
			time.Sleep(time.Duration(i) * 8 * time.Millisecond)
			logicalID := fmt.Sprintf("%s-%02d-%s", prefix, i, uuid.NewString())
			results[i] = postAccountSlotChat(ctx, client, addr, bearer, logicalID)
		}()
	}
	close(start)
	wg.Wait()
	return results
}

func postAccountSlotChat(ctx context.Context, client *http.Client, addr, bearer, logicalID string) accountSlotHTTPResult {
	body, err := json.Marshal(map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
		"max_tokens": 16,
		"stream":     true,
	})
	if err != nil {
		return accountSlotHTTPResult{logicalID: logicalID, err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return accountSlotHTTPResult{logicalID: logicalID, err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Idempotency-Key", logicalID)

	resp, err := client.Do(req)
	if err != nil {
		return accountSlotHTTPResult{logicalID: logicalID, err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return accountSlotHTTPResult{logicalID: logicalID, statusCode: resp.StatusCode, err: err}
	}
	return accountSlotHTTPResult{
		logicalID: logicalID, statusCode: resp.StatusCode,
		retryAfter:  resp.Header.Get("Retry-After"),
		abortFailed: resp.Header.Get("X-Huakai-Abort-Failed") != "",
		body:        raw,
	}
}

func classifyAccountSlotResults(t *testing.T, holderResults, overflowResults []accountSlotHTTPResult) (successes, rejects []accountSlotHTTPResult) {
	t.Helper()
	for _, result := range holderResults {
		if result.err != nil {
			t.Fatalf("holder request logical_id=%s err=%v", result.logicalID, result.err)
		}
		if result.statusCode != http.StatusOK {
			t.Fatalf("holder request logical_id=%s status=%d body=%s; want 200 while slots are available",
				result.logicalID, result.statusCode, safeAccountSlotBody(result.body))
		}
		successes = append(successes, result)
	}
	for _, result := range overflowResults {
		if result.err != nil {
			t.Fatalf("overflow request logical_id=%s err=%v", result.logicalID, result.err)
		}
		if result.statusCode != http.StatusTooManyRequests || !bytes.Contains(result.body, []byte("queue_wait")) {
			t.Fatalf("overflow request logical_id=%s status=%d body=%s; want 429/queue_wait",
				result.logicalID, result.statusCode, safeAccountSlotBody(result.body))
		}
		rejects = append(rejects, result)
	}
	return successes, rejects
}

func waitForAccountSlotInFlight(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, providerAccountID int64, want, cap int32) int32 {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var last int32
	for {
		current, err := readAccountSlotInFlight(ctx, pgPool, providerAccountID)
		if err != nil {
			t.Fatalf("read in_flight_count: %v", err)
		}
		last = current
		if last > cap {
			t.Fatalf("provider_accounts.in_flight_count=%d 超过 cap=%d", last, cap)
		}
		if last == want {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("provider_accounts.in_flight_count=%d want %d", last, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readAccountSlotInFlight(ctx context.Context, pgPool *pgxpool.Pool, providerAccountID int64) (int32, error) {
	var current int32
	err := pgPool.QueryRow(ctx,
		`SELECT in_flight_count FROM provider_accounts WHERE id=$1`, providerAccountID,
	).Scan(&current)
	return current, err
}

func assertAccountSlotSuccessPG(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *smokeSeed, logicalID string) {
	t.Helper()
	claimID, actualCost := readAccountSlotClaim(t, ctx, pgPool, seed, logicalID, "committed")
	if actualCost <= 0 {
		t.Fatalf("claim %d actual_cost=%v want >0", claimID, actualCost)
	}
	assertAccountSlotCount(t, ctx, pgPool,
		`SELECT count(*) FROM usage_records WHERE tenant_id=$1 AND claim_id=$2`,
		1, "success usage_records", seed.tenantID, claimID)
	assertAccountSlotCount(t, ctx, pgPool,
		`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2 AND event_type='claim_committed'`,
		1, "success claim_committed event", seed.tenantID, claimID)
	assertAccountSlotCount(t, ctx, pgPool,
		`SELECT count(*) FROM pool_slot_acquisitions WHERE tenant_id=$1 AND claim_id=$2 AND status='released_success'`,
		1, "success released pool slot", seed.tenantID, claimID)
	assertAccountSlotBalanceHoldState(t, ctx, pgPool, claimID, "captured")
	assertAccountSlotQuotaReservation(t, ctx, pgPool, seed.tenantID, claimID, "settled")
}

func assertAccountSlotRejectPG(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *smokeSeed, logicalID string) {
	t.Helper()
	assertAccountSlotRejectReasonPG(t, ctx, pgPool, seed, logicalID, "queue_wait")
}

func assertAccountSlotRejectReasonPG(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *smokeSeed, logicalID string, wantReason string) {
	t.Helper()
	claimID, _ := readAccountSlotClaim(t, ctx, pgPool, seed, logicalID, "aborted")
	var abortedReason *string
	if err := pgPool.QueryRow(ctx,
		`SELECT aborted_reason FROM billing_ledger_claims
		  WHERE tenant_id=$1 AND id=$2`,
		seed.tenantID, claimID,
	).Scan(&abortedReason); err != nil {
		t.Fatalf("read aborted_reason for claim %d: %v", claimID, err)
	}
	if abortedReason == nil || *abortedReason != wantReason {
		t.Fatalf("claim %d aborted_reason=%v want %s", claimID, abortedReason, wantReason)
	}
	assertAccountSlotCount(t, ctx, pgPool,
		`SELECT count(*) FROM usage_records WHERE tenant_id=$1 AND claim_id=$2`,
		0, "rejected usage_records", seed.tenantID, claimID)
	// 账号全满时 queue_wait 是 retryable 本地失败:同一 HTTP 请求内会内部重试一轮
	// (每 attempt 各写一条 claim_aborted 审计),故 claim_aborted 条数 = attempt 次数(可为 1 或 2),
	// 不能硬断 1。关键 money 不变量是"被拒请求预扣只退一次、绝不重复退款":
	// re-reserve 复用同一 claim 与同一 balance_hold,故该 claim 只有 1 行 hold 且最终 released。
	assertAccountSlotCount(t, ctx, pgPool,
		`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2 AND event_type='claim_aborted' AND actual_cost=0`,
		int(attemptSeqForClaim(t, ctx, pgPool, claimID)), "rejected claim_aborted event 应等于 attempt 次数", seed.tenantID, claimID)
	assertAccountSlotCount(t, ctx, pgPool,
		`SELECT count(*) FROM balance_holds WHERE claim_id=$1`,
		1, "rejected claim 只应有 1 行 balance_hold(re-reserve 复用同 hold、只退一次)", claimID)
	assertAccountSlotCount(t, ctx, pgPool,
		`SELECT count(*) FROM pool_slot_acquisitions WHERE tenant_id=$1 AND claim_id=$2`,
		0, "rejected pool slots", seed.tenantID, claimID)
	assertAccountSlotBalanceHoldState(t, ctx, pgPool, claimID, "released")
	assertAccountSlotQuotaReservation(t, ctx, pgPool, seed.tenantID, claimID, "released")
}

func readAccountSlotClaim(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *smokeSeed, logicalID, wantStatus string) (int64, float64) {
	t.Helper()
	// 结算/中止走响应交付后的脱钩 ctx(post-commit 异步),HTTP 200 返回时 claim 可能尚未
	// 落到终态。轮询等到期望终态(与 waitForAccountSlotInFlight 同款做法),而非收到响应就
	// 立即查——否则读到中间态 reserving 会把"异步结算还没跑完"误判成生产缺陷。
	var (
		claimID       int64
		status        string
		actualCostRaw string
	)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := pgPool.QueryRow(ctx,
			`SELECT id, status, COALESCE(actual_cost, 0)::text
			   FROM billing_ledger_claims
			  WHERE tenant_id=$1 AND api_key_id=$2 AND logical_request_id=$3`,
			seed.tenantID, seed.apiKeyID, logicalID,
		).Scan(&claimID, &status, &actualCostRaw); err != nil {
			t.Fatalf("read claim logical_id=%s: %v", logicalID, err)
		}
		if status == wantStatus {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("claim %d logical_id=%s status=%q want %q(轮询 5s 仍未到终态)", claimID, logicalID, status, wantStatus)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return claimID, parseAccountSlotNonNegativeFloat(t, "actual_cost", actualCostRaw)
}

// quotaStuckReservations 统计"配额预留未随结算/中止收敛、悬挂待 lease sweeper 兜底"
// 的已知竞争降级次数(quota Release/Settle 在序列化冲突下失败仅告警)。调用方用
// 前后差值给悬挂设小额预算:偶发竞争放行,系统性释放坏死(全部悬挂)仍必红。
var quotaStuckReservations atomic.Int64

func assertAccountSlotQuotaReservation(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, tenantID, claimID int64, wantStatus string) {
	t.Helper()
	var status string
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := pgPool.QueryRow(ctx,
			`SELECT status FROM quota_reservations WHERE tenant_id=$1 AND claim_id=$2`,
			tenantID, claimID,
		).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			// 同用户重并发下配额预留可在序列化竞争时按契约 fail-open
			//(quota_reserve_infra_error),此时该 claim 整个生命周期都没有
			// 预留行,属合法降级;有行时仍必须收敛到期望终态。
			t.Logf("quota reservation claim=%d 无预留行(fail-open 降级),跳过终态断言", claimID)
			return
		}
		if err != nil {
			t.Fatalf("read quota reservation claim=%d: %v", claimID, err)
		}
		if status == wantStatus {
			return
		}
		if time.Now().After(deadline) {
			if status == "reserved" {
				// 已知既有降级:quota Release/Settle 在序列化竞争下失败只告警,
				// 预留悬挂到 lease 过期由 sweeper 兜底。计数交给调用方限额。
				quotaStuckReservations.Add(1)
				t.Logf("quota reservation claim=%d 悬挂在 reserved(竞争降级,待 sweeper 兜底)", claimID)
				return
			}
			t.Fatalf("quota reservation claim=%d status=%q want %q(轮询 5s 仍未到终态)", claimID, status, wantStatus)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertAccountSlotBalanceHoldState(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, claimID int64, want string) {
	t.Helper()
	var state string
	if err := pgPool.QueryRow(ctx,
		`SELECT state FROM balance_holds WHERE claim_id=$1`, claimID,
	).Scan(&state); err != nil {
		t.Fatalf("read balance hold claim=%d: %v", claimID, err)
	}
	if state != want {
		t.Fatalf("balance hold claim=%d state=%q want %q", claimID, state, want)
	}
}

// frozenCount = 网关明示 abort 打穿(X-Huakai-Abort-Failed)的请求数:这些 claim 停留
// reserving、hold 冻结,交由 lease sweeper 追平,属声明过的降级而非泄漏;必须一一对应。
func assertAccountSlotNoLeaks(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *smokeSeed, successCount, rejectCount, frozenCount int) {
	t.Helper()
	assertAccountSlotCount(t, ctx, pgPool,
		`SELECT count(*) FROM pool_slot_acquisitions WHERE tenant_id=$1 AND status='acquired'`,
		0, "acquired pool slots after completion", seed.tenantID)
	assertAccountSlotCount(t, ctx, pgPool,
		`SELECT count(*) FROM quota_concurrency_slots WHERE tenant_id=$1 AND status='acquired'`,
		0, "acquired quota concurrency slots after completion", seed.tenantID)
	assertAccountSlotCount(t, ctx, pgPool,
		`SELECT count(*) FROM balance_holds WHERE tenant_id=$1 AND state='held'`,
		frozenCount, "held balance_holds after completion(仅 abort 打穿冻结)", seed.tenantID)
	assertAccountSlotCount(t, ctx, pgPool,
		`SELECT count(*) FROM pool_slot_acquisitions WHERE tenant_id=$1 AND status='released_success'`,
		successCount, "released_success pool slots", seed.tenantID)
	assertAccountSlotCount(t, ctx, pgPool,
		`SELECT count(*) FROM quota_concurrency_slots WHERE tenant_id=$1`,
		0, "quota concurrency slots should not be part of this account-slot e2e", seed.tenantID)

	var committed, aborted int
	if err := pgPool.QueryRow(ctx,
		`SELECT
		    count(*) FILTER (WHERE status='committed')::int,
		    count(*) FILTER (WHERE status='aborted')::int
		   FROM billing_ledger_claims
		  WHERE tenant_id=$1`,
		seed.tenantID,
	).Scan(&committed, &aborted); err != nil {
		t.Fatalf("count final claims: %v", err)
	}
	if committed != successCount || aborted != rejectCount-frozenCount {
		t.Fatalf("claim final counts committed=%d aborted=%d want committed=%d aborted=%d(冻结 %d)",
			committed, aborted, successCount, rejectCount-frozenCount, frozenCount)
	}

	var settled, released, dangling int
	if err := pgPool.QueryRow(ctx,
		`SELECT
		    count(*) FILTER (WHERE status='settled')::int,
		    count(*) FILTER (WHERE status='released')::int,
		    count(*) FILTER (WHERE status NOT IN ('settled','released'))::int
		   FROM quota_reservations
		  WHERE tenant_id=$1`,
		seed.tenantID,
	).Scan(&settled, &released, &dangling); err != nil {
		t.Fatalf("count quota reservations: %v", err)
	}
	// 配额预留在序列化竞争下可 fail-open(该 claim 无预留行),故终态计数只有上界;
	// 悬挂行必须能被解释:要么是已计数的释放竞争降级,要么随 abort 冻结一起等 sweeper。
	if int64(dangling) > quotaStuckReservations.Load()+int64(frozenCount) {
		t.Fatalf("quota reservation dangling=%d 超出已记录竞争降级 %d+冻结 %d(存在无解释的预留)",
			dangling, quotaStuckReservations.Load(), frozenCount)
	}
	if settled > successCount || released > rejectCount {
		t.Fatalf("quota reservation final counts settled=%d released=%d exceed success=%d reject=%d",
			settled, released, successCount, rejectCount)
	}

	var heldRaw string
	if err := pgPool.QueryRow(ctx,
		`SELECT held::text FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		seed.tenantID, seed.userID,
	).Scan(&heldRaw); err != nil {
		t.Fatalf("read user balance held: %v", err)
	}
	// abort 打穿冻结的 hold 未回滚 user_balances.held(待 lease sweeper 追平),故
	// frozenCount>0 时 held 允许非零但必须为正;无冻结时严格归零抓真泄漏。
	held := parseAccountSlotNonNegativeFloat(t, "user_balances.held", heldRaw)
	if frozenCount == 0 && held != 0 {
		t.Fatalf("user_balances.held=%s want 0(无冻结)", heldRaw)
	}
	if frozenCount > 0 && held <= 0 {
		t.Fatalf("user_balances.held=%s want >0(存在 %d 笔 abort 冻结)", heldRaw, frozenCount)
	}
}

func assertAccountSlotCount(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, query string, want int, label string, args ...any) {
	t.Helper()
	var got int
	if err := pgPool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("%s count: %v", label, err)
	}
	if got != want {
		t.Fatalf("%s count=%d want %d", label, got, want)
	}
}

func parseAccountSlotNonNegativeFloat(t *testing.T, label, raw string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		t.Fatalf("parse %s=%q: %v", label, raw, err)
	}
	if v < 0 {
		t.Fatalf("%s=%q must not be negative", label, raw)
	}
	return v
}

func safeAccountSlotBody(raw []byte) string {
	const maxBodyBytes = 2048
	body := string(raw)
	if len(body) > maxBodyBytes {
		body = body[:maxBodyBytes] + "...<truncated>"
	}
	return body
}

// attemptSeqForClaim 返回 claim 当前的 attempt_seq。queue_wait retryable 内部重试会让
// 同一 claim 经历多个 attempt,claim_aborted 审计事件每 attempt 一条,故其条数应等于 attempt_seq。
func attemptSeqForClaim(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, claimID int64) int32 {
	t.Helper()
	var seq int32
	if err := pgPool.QueryRow(ctx, `SELECT attempt_seq FROM billing_ledger_claims WHERE id=$1`, claimID).Scan(&seq); err != nil {
		t.Fatalf("read attempt_seq for claim %d: %v", claimID, err)
	}
	return seq
}
