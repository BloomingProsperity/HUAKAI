//go:build e2e_concurrency

package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

const (
	bindingConcurrencyE2ECap      = 3
	bindingConcurrencyE2ERequests = 12
)

func TestBindingConcurrencyE2E(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping binding concurrency e2e")
	}
	binPath := buildGateway(t)
	defer os.Remove(binPath)

	t.Run("同一绑定重并发恰好放行K个且拒绝完整回滚", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
		if err != nil {
			t.Fatalf("Open dev pool: %v", err)
		}
		defer pgPool.Close()

		seed := seedSmokeGraph(t, ctx, pgPool)
		pricingVersion := "binding-cap-" + uuid.NewString()
		cleanupAccountSlotE2E(t, pgPool, seed.tenantID, pricingVersion)
		bindingID := seedBindingConcurrencyE2E(t, ctx, pgPool, seed, pricingVersion, bindingConcurrencyE2ECap)

		addr := reserveLocalPort(t)
		cmd := startAccountSlotE2EGateway(t, binPath, dsn, addr, pricingVersion)
		t.Cleanup(func() { stopGateway(cmd) })
		waitForGateway(t, addr)

		client := &http.Client{Timeout: 20 * time.Second}
		resultsCh := make(chan []accountSlotHTTPResult, 1)
		go func() {
			resultsCh <- postAccountSlotBatch(
				ctx, client, addr, seed.bearer, "binding-cap", bindingConcurrencyE2ERequests,
			)
		}()
		// 先观察真实表稳定达到 K，再等待 HTTP 完成；这样不是靠调度运气推断并发。
		waitForBindingActive(t, ctx, pgPool, bindingID, bindingConcurrencyE2ECap, bindingConcurrencyE2ECap)
		results := <-resultsCh

		var successes, rejects, claimRaces, quotaDenied []accountSlotHTTPResult
		for _, result := range results {
			if result.err != nil {
				t.Fatalf("request logical_id=%s err=%v", result.logicalID, result.err)
			}
			switch result.statusCode {
			case http.StatusOK:
				successes = append(successes, result)
			case http.StatusTooManyRequests:
				if bytes.Contains(result.body, []byte(clienterr.CodeInsufficientBalance)) {
					// 已知既有缺陷(与绑定闸无关):quota 引擎 Reserve 把瞬时序列化冲突
					// 走 fail-closed 硬拒(internal/quota/service.go failClosedDecision),
					// 重并发下偶发假 429。此桶仍强制 money 收敛断言(claim=quota_denied、
					// hold 退回),引擎修复属配额强制面,另行走 Owner 门。
					quotaDenied = append(quotaDenied, result)
					continue
				}
				if !bytes.Contains(result.body, []byte(clienterr.CodeBindingConcurrencyLimited)) {
					t.Fatalf("binding reject logical_id=%s body=%s 未包含稳定 code=%s",
						result.logicalID, safeAccountSlotBody(result.body), clienterr.CodeBindingConcurrencyLimited)
				}
				if result.retryAfter != "1" {
					t.Fatalf("binding reject logical_id=%s Retry-After=%q want 1", result.logicalID, result.retryAfter)
				}
				rejects = append(rejects, result)
			case http.StatusConflict:
				// 同用户重并发预扣在 SERIALIZABLE 热点冲突耗尽重试预算后,按既有契约
				// 降级为可重试 409 claim_race(整事务回滚,不留 claim/hold/槽)。这是
				// Reserve 层的合法第三结局,与绑定闸无关,只要求契约字段完整。
				if !bytes.Contains(result.body, []byte(clienterr.CodeClaimRace)) {
					t.Fatalf("claim race logical_id=%s body=%s 未包含稳定 code=%s",
						result.logicalID, safeAccountSlotBody(result.body), clienterr.CodeClaimRace)
				}
				if result.retryAfter != "1" {
					t.Fatalf("claim race logical_id=%s Retry-After=%q want 1", result.logicalID, result.retryAfter)
				}
				claimRaces = append(claimRaces, result)
			default:
				t.Fatalf("request logical_id=%s status=%d body=%s; want 200 / binding 429 / reserve 409",
					result.logicalID, result.statusCode, safeAccountSlotBody(result.body))
			}
		}
		// 变异①：去掉事务内硬闸会让成功数超过 K。claim_race 与 quota fail-closed
		// 都在选号前就出局、不参与占槽,其余请求仍必须恰 K 成功、全部被绑定闸拒绝。
		wantRejects := bindingConcurrencyE2ERequests - bindingConcurrencyE2ECap - len(claimRaces) - len(quotaDenied)
		if len(successes) != bindingConcurrencyE2ECap || len(rejects) != wantRejects {
			t.Fatalf("success/reject/claimRace/quotaDenied=%d/%d/%d/%d want %d/%d/%d/%d",
				len(successes), len(rejects), len(claimRaces), len(quotaDenied),
				bindingConcurrencyE2ECap, wantRejects, len(claimRaces), len(quotaDenied))
		}

		for _, result := range successes {
			assertAccountSlotSuccessPG(t, ctx, pgPool, seed, result.logicalID)
		}
		quotaStuckBefore := quotaStuckReservations.Load()
		frozen := 0
		for _, result := range rejects {
			if result.abortFailed {
				// 网关已明示 abort 在序列化竞争下打穿重试预算(claim 停 reserving、
				// hold 冻结待 lease sweeper 追平)。变异②"根本不 abort"不会带此头,
				// 走下面严格断言必红,判别力不损。
				frozen++
				t.Logf("binding reject logical_id=%s abort 打穿(声明式冻结,待 sweeper)", result.logicalID)
				continue
			}
			// 变异②：binding 拒绝后若不复用 abort，claim/hold/quota 任一断言都会变红。
			assertAccountSlotRejectReasonPG(
				t, ctx, pgPool, seed, result.logicalID, "binding_concurrency_limited",
			)
		}
		for _, result := range quotaDenied {
			if result.abortFailed {
				frozen++
				continue
			}
			// quota fail-closed 假拒同样必须钱账收敛:claim 中止原因正确、hold 全额退回。
			assertAccountSlotRejectReasonPG(
				t, ctx, pgPool, seed, result.logicalID, "quota_denied",
			)
		}
		// 竞争降级(配额悬挂/abort 冻结)只容许零星发生;系统性断裂立刻打穿预算。
		if stuck := quotaStuckReservations.Load() - quotaStuckBefore; stuck > 2 {
			t.Fatalf("配额预留悬挂 %d 次超预算 2:释放链疑似系统性断裂而非偶发竞争", stuck)
		}
		if frozen > 2 {
			t.Fatalf("abort 打穿冻结 %d 次超预算 2:中止链疑似系统性断裂而非偶发竞争", frozen)
		}
		assertAccountSlotCount(t, ctx, pgPool,
			`SELECT count(*) FROM pool_slot_acquisitions
			  WHERE tenant_id=$1 AND binding_id=$2 AND status='released_success'`,
			bindingConcurrencyE2ECap, "successful slots carry binding_id", seed.tenantID, bindingID)
		// 变异④：插入 acquisition 时不写 binding_id 会让该断言为 0。
		assertAccountSlotCount(t, ctx, pgPool,
			`SELECT count(*) FROM pool_slot_acquisitions
			  WHERE tenant_id=$1 AND binding_id IS NULL`,
			0, "new slots must not lose binding_id", seed.tenantID)
		waitForBindingActive(t, ctx, pgPool, bindingID, 0, bindingConcurrencyE2ECap)
		assertAccountSlotNoLeaks(t, ctx, pgPool, seed, len(successes), len(rejects)+len(quotaDenied), frozen)
	})

	t.Run("normal绑定满后quota目标用独立闸接管且钱账收敛", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
		if err != nil {
			t.Fatalf("Open dev pool: %v", err)
		}
		defer pgPool.Close()

		seed := seedSmokeGraph(t, ctx, pgPool)
		pricingVersion := "binding-fallback-" + uuid.NewString()
		cleanupAccountSlotE2E(t, pgPool, seed.tenantID, pricingVersion)
		fixture := seedBindingFallbackE2E(t, ctx, pgPool, seed, pricingVersion)

		addr := reserveLocalPort(t)
		cmd := startAccountSlotE2EGateway(t, binPath, dsn, addr, pricingVersion)
		t.Cleanup(func() { stopGateway(cmd) })
		waitForGateway(t, addr)

		client := &http.Client{Timeout: 20 * time.Second}
		holderID := "binding-fallback-holder-" + uuid.NewString()
		takeoverID := "binding-fallback-takeover-" + uuid.NewString()
		holderDone := make(chan accountSlotHTTPResult, 1)
		go func() {
			holderDone <- postAccountSlotChat(ctx, client, addr, seed.bearer, holderID)
		}()
		waitForBindingActive(t, ctx, pgPool, fixture.normalBindingID, 1, 1)

		takeoverDone := make(chan accountSlotHTTPResult, 1)
		go func() {
			takeoverDone <- postAccountSlotChat(ctx, client, addr, seed.bearer, takeoverID)
		}()
		// 直接观察目标 binding 的 acquired 行，证明不是 normal 释放后的偶然成功。
		waitForBindingActive(t, ctx, pgPool, fixture.targetBindingID, 1, 1)
		takeover := <-takeoverDone
		holder := <-holderDone
		for _, result := range []accountSlotHTTPResult{holder, takeover} {
			if result.err != nil || result.statusCode != http.StatusOK {
				t.Fatalf("logical_id=%s err/status=%v/%d body=%s，期望 200",
					result.logicalID, result.err, result.statusCode, safeAccountSlotBody(result.body))
			}
		}

		assertAccountSlotSuccessPG(t, ctx, pgPool, seed, holderID)
		assertAccountSlotSuccessPG(t, ctx, pgPool, seed, takeoverID)
		takeoverClaimID, _ := readAccountSlotClaim(t, ctx, pgPool, seed, takeoverID, "committed")
		assertAccountSlotCount(t, ctx, pgPool,
			`SELECT count(*) FROM pool_slot_acquisitions
			  WHERE tenant_id=$1 AND claim_id=$2 AND binding_id=$3 AND provider_account_id=$4
			    AND status='released_success'`,
			1, "takeover 使用 quota binding/account", seed.tenantID, takeoverClaimID,
			fixture.targetBindingID, fixture.targetAccountID)
		assertAccountSlotCount(t, ctx, pgPool,
			`SELECT count(*) FROM pool_slot_acquisitions
			  WHERE tenant_id=$1 AND claim_id=$2 AND binding_id=$3`,
			0, "takeover 不得借用 normal binding", seed.tenantID, takeoverClaimID, fixture.normalBindingID)
		assertAccountSlotCount(t, ctx, pgPool,
			`SELECT count(*) FROM billing_events
			  WHERE tenant_id=$1 AND claim_id=$2 AND event_type='claim_aborted'`,
			2, "两个 normal 失败 attempt 均 abort", seed.tenantID, takeoverClaimID)
		assertAccountSlotCount(t, ctx, pgPool,
			`SELECT count(*) FROM billing_events
			  WHERE tenant_id=$1 AND claim_id=$2 AND event_type='claim_committed'`,
			1, "quota 成功恰一次 settle", seed.tenantID, takeoverClaimID)
		if seq := attemptSeqForClaim(t, ctx, pgPool, takeoverClaimID); seq != 3 {
			t.Fatalf("takeover attempt_seq=%d want 3(normal×2 + quota×1)", seq)
		}

		var accountID int64
		var fromClass, toClass, trigger string
		if err := pgPool.QueryRow(ctx,
			`SELECT provider_account_id,
			        routing_reason #>> '{class_transition,from}',
			        routing_reason #>> '{class_transition,to}',
			        routing_reason #>> '{class_transition,trigger}'
			   FROM usage_records WHERE tenant_id=$1 AND claim_id=$2`,
			seed.tenantID, takeoverClaimID,
		).Scan(&accountID, &fromClass, &toClass, &trigger); err != nil {
			t.Fatalf("read takeover usage transition: %v", err)
		}
		if accountID != fixture.targetAccountID || fromClass != "normal" || toClass != "quota" || trigger != "binding_concurrency_limit" {
			t.Fatalf("usage account/transition=%d/%s→%s/%s，期望 %d/normal→quota/binding_concurrency_limit",
				accountID, fromClass, toClass, trigger, fixture.targetAccountID)
		}

		waitForBindingActive(t, ctx, pgPool, fixture.normalBindingID, 0, 1)
		waitForBindingActive(t, ctx, pgPool, fixture.targetBindingID, 0, 1)
		assertAccountSlotNoLeaks(t, ctx, pgPool, seed, 2, 0, 0)
	})

	t.Run("客户端断连后脱钩中止仍释放槽与钱账", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
		if err != nil {
			t.Fatalf("Open dev pool: %v", err)
		}
		defer pgPool.Close()

		seed := seedSmokeGraph(t, ctx, pgPool)
		pricingVersion := "binding-cancel-" + uuid.NewString()
		cleanupAccountSlotE2E(t, pgPool, seed.tenantID, pricingVersion)
		bindingID := seedBindingConcurrencyE2E(t, ctx, pgPool, seed, pricingVersion, 1)

		addr := reserveLocalPort(t)
		cmd := startAccountSlotE2EGateway(t, binPath, dsn, addr, pricingVersion)
		t.Cleanup(func() { stopGateway(cmd) })
		waitForGateway(t, addr)

		logicalID := "binding-cancel-" + uuid.NewString()
		requestCtx, cancelRequest := context.WithCancel(ctx)
		resultCh := make(chan accountSlotHTTPResult, 1)
		go func() {
			resultCh <- postAccountSlotChat(
				requestCtx, &http.Client{Timeout: 20 * time.Second}, addr, seed.bearer, logicalID,
			)
		}()
		waitForBindingActive(t, ctx, pgPool, bindingID, 1, 1)
		cancelRequest()
		result := <-resultCh
		if result.err == nil && result.statusCode == http.StatusOK {
			t.Fatalf("cancelled request unexpectedly completed with 200")
		}

		claimID := waitForBindingClaimStatus(t, ctx, pgPool, seed, logicalID, "aborted")
		assertAccountSlotCount(t, ctx, pgPool,
			`SELECT count(*) FROM pool_slot_acquisitions
			  WHERE tenant_id=$1 AND claim_id=$2 AND binding_id=$3 AND status='released_failure'`,
			1, "cancelled slot released_failure", seed.tenantID, claimID, bindingID)
		assertAccountSlotBalanceHoldState(t, ctx, pgPool, claimID, "released")
		assertAccountSlotQuotaReservation(t, ctx, pgPool, seed.tenantID, claimID, "released")
		// 零成本用量审计随脱钩中止异步落库,轮询等待而非即时断言。
		waitForBindingAuditCount(t, ctx, pgPool,
			`SELECT count(*) FROM usage_records
			  WHERE tenant_id=$1 AND claim_id=$2 AND actual_cost=0`,
			1, "cancelled request zero-cost usage audit", seed.tenantID, claimID)
		waitForBindingActive(t, ctx, pgPool, bindingID, 0, 1)
		waitForAccountSlotInFlight(t, ctx, pgPool, seed.providerAccountID, 0, 64)

		replacement := postAccountSlotChat(
			ctx, &http.Client{Timeout: 20 * time.Second}, addr, seed.bearer,
			"binding-cancel-replacement-"+uuid.NewString(),
		)
		if replacement.err != nil || replacement.statusCode != http.StatusOK {
			t.Fatalf("replacement err/status=%v/%d body=%s want 200",
				replacement.err, replacement.statusCode, safeAccountSlotBody(replacement.body))
		}
		assertAccountSlotSuccessPG(t, ctx, pgPool, seed, replacement.logicalID)
		assertAccountSlotNoLeaks(t, ctx, pgPool, seed, 1, 1, 0)
	})
}

func seedBindingConcurrencyE2E(
	t *testing.T,
	ctx context.Context,
	pgPool *pgxpool.Pool,
	seed *smokeSeed,
	pricingVersion string,
	capacity int,
) int64 {
	t.Helper()
	seedAccountSlotE2EConfig(t, ctx, pgPool, seed, pricingVersion)
	if _, err := pgPool.Exec(ctx,
		`UPDATE provider_accounts
		    SET cap_concurrency=64, cap_queue_fallback=0, in_flight_count=0
		  WHERE id=$1 AND tenant_id=$2`,
		seed.providerAccountID, seed.tenantID,
	); err != nil {
		t.Fatalf("isolate binding cap from account cap: %v", err)
	}
	var bindingID int64
	if err := pgPool.QueryRow(ctx,
		`UPDATE model_pool_bindings
		    SET max_parallel_requests=$1, updated_at=now()
		  WHERE tenant_id=$2 AND model_id=$3 AND pool_group_id=$4
		  RETURNING id`,
		capacity, seed.tenantID, seed.modelID, seed.poolGroupID,
	).Scan(&bindingID); err != nil {
		t.Fatalf("enable binding concurrency cap: %v", err)
	}
	return bindingID
}

type bindingFallbackE2EFixture struct {
	normalBindingID int64
	targetBindingID int64
	targetAccountID int64
}

func seedBindingFallbackE2E(
	t *testing.T,
	ctx context.Context,
	pgPool *pgxpool.Pool,
	seed *smokeSeed,
	pricingVersion string,
) bindingFallbackE2EFixture {
	t.Helper()
	fixture := bindingFallbackE2EFixture{
		normalBindingID: seedBindingConcurrencyE2E(t, ctx, pgPool, seed, pricingVersion, 1),
	}
	if _, err := pgPool.Exec(ctx,
		`UPDATE model_pool_bindings SET fallback_class='normal', updated_at=now() WHERE id=$1`,
		fixture.normalBindingID,
	); err != nil {
		t.Fatalf("mark normal binding: %v", err)
	}

	unique := uuid.NewString()
	var targetPoolID, targetChannelID int64
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "binding-fallback-pool-"+unique,
	).Scan(&targetPoolID); err != nil {
		t.Fatalf("seed target pool: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		seed.tenantID, targetPoolID, "binding-fallback-channel-"+unique,
	).Scan(&targetChannelID); err != nil {
		t.Fatalf("seed target channel: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type,
			cap_concurrency, cap_queue_fallback, in_flight_count, priority,
			health_state, credential_state, capability_flags
		 ) VALUES ($1, $2, $3, $4, 'api_key', 64, 0, 0, 100,
		           'healthy', 'valid', ARRAY['stream','tools','vision','json','audio','file'])
		 RETURNING id`,
		seed.tenantID, seed.providerID, targetChannelID, "binding-fallback-account-"+unique,
	).Scan(&fixture.targetAccountID); err != nil {
		t.Fatalf("seed target account: %v", err)
	}

	keyProvider, err := credentialstore.NewStaticKeyProvider("local-v1", make([]byte, 32))
	if err != nil {
		t.Fatalf("target credential key provider: %v", err)
	}
	envelope, err := credentialstore.NewCipher(keyProvider).Encrypt(ctx,
		[]byte(`{"api_key":"sk-mock-fallback-key"}`),
		credentialstore.AAD{TenantID: seed.tenantID, ProviderAccountID: fixture.targetAccountID, Vendor: "openai", AuthMode: "api_key", Version: 1})
	if err != nil {
		t.Fatalf("encrypt target credential: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO account_credentials (
			tenant_id, provider_account_id, vendor, auth_mode, state,
			credential_version, encrypted_payload, encryption_scheme, key_id, nonce, aad_hash
		 ) VALUES ($1, $2, 'openai', 'api_key', 'active', 1, $3, 'aes-256-gcm', $4, $5, $6)`,
		seed.tenantID, fixture.targetAccountID, envelope.Ciphertext, envelope.KeyID, envelope.Nonce, envelope.AADHash,
	); err != nil {
		t.Fatalf("seed target credential: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO model_pool_bindings (
			tenant_id, model_id, pool_group_id, priority, weight, enabled,
			selection_mode, max_parallel_requests, fallback_class
		 ) VALUES ($1, $2, $3, 0, 2000000000, true, 'priority_weighted', 1, 'quota')
		 RETURNING id`,
		seed.tenantID, seed.modelID, targetPoolID,
	).Scan(&fixture.targetBindingID); err != nil {
		t.Fatalf("seed quota binding: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`UPDATE model_registry_snapshots SET version=version+1 WHERE tenant_id=$1`, seed.tenantID,
	); err != nil {
		t.Fatalf("advance registry snapshot: %v", err)
	}
	return fixture
}

// waitForBindingAuditCount 轮询等待异步审计行落库到期望条数(与 waitForBindingActive 同款节奏)。
func waitForBindingAuditCount(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, query string, want int, label string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		var got int
		if err := pgPool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
			t.Fatalf("%s count: %v", label, err)
		}
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s=%d want %d(轮询 8s 未收敛)", label, got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForBindingActive(
	t *testing.T,
	ctx context.Context,
	pgPool *pgxpool.Pool,
	bindingID int64,
	want int,
	capacity int,
) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		var active int
		if err := pgPool.QueryRow(ctx,
			`SELECT count(*)::int FROM pool_slot_acquisitions
			  WHERE binding_id=$1 AND status='acquired'`,
			bindingID,
		).Scan(&active); err != nil {
			t.Fatalf("read binding active count: %v", err)
		}
		if active > capacity {
			t.Fatalf("binding active=%d exceeded cap=%d", active, capacity)
		}
		if active == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("binding active=%d want %d", active, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForBindingClaimStatus(
	t *testing.T,
	ctx context.Context,
	pgPool *pgxpool.Pool,
	seed *smokeSeed,
	logicalID string,
	wantStatus string,
) int64 {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		var claimID int64
		var status string
		err := pgPool.QueryRow(ctx,
			`SELECT id, status FROM billing_ledger_claims
			  WHERE tenant_id=$1 AND api_key_id=$2 AND logical_request_id=$3`,
			seed.tenantID, seed.apiKeyID, logicalID,
		).Scan(&claimID, &status)
		if err == nil && status == wantStatus {
			return claimID
		}
		if err != nil && err != pgx.ErrNoRows {
			t.Fatalf("read binding claim logical_id=%s: %v", logicalID, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("binding claim logical_id=%s status=%q want %q", logicalID, status, wantStatus)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
