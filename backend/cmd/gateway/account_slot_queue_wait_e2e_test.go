//go:build e2e_concurrency

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

const (
	accountSlotE2EMaxWaiting      = int32(1)
	accountSlotE2EWaitTimeoutMS   = int32(3000)
	accountSlotE2EShortTimeoutMS  = int32(200)
	accountSlotE2EOverflowMaxWait = int32(1)
)

func runAccountSlotQueueWaitE2E(t *testing.T) {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping e2e concurrency test")
	}
	dsn = useDisposableSpecializedLiveDatabase(t, dsn)
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer setupCancel()

	pgPool, err := db.Open(setupCtx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open dev pool: %v", err)
	}
	t.Cleanup(pgPool.Close)

	pricingVersion := "e2e-account-slot-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = pgPool.Exec(context.Background(), `DELETE FROM billing_pricing_versions WHERE tenant_id=0 AND version=$1`, pricingVersion)
	})

	binPath := buildGateway(t)
	defer os.Remove(binPath)

	addr := reserveLocalPort(t)
	cmd := startAccountSlotE2EGateway(t, binPath, dsn, addr, pricingVersion)
	t.Cleanup(func() { stopGateway(cmd) })
	waitForGateway(t, addr)
	setupCancel()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 15 * time.Second}

	t.Run("waiter_succeeds_after_slot_release", func(t *testing.T) {
		seed := seedAccountSlotQueueWaitScenario(t, ctx, pgPool, pricingVersion, accountSlotE2EMaxWaiting, accountSlotE2EWaitTimeoutMS)
		holders, waitHolders, peak := startAccountSlotHoldersAndSampler(t, ctx, pgPool, client, addr, seed)
		waiter := postAccountSlotChat(ctx, client, addr, seed.bearer, "account-slot-wait-success-"+uuid.NewString())
		waitHolders()
		if waiter.err != nil {
			t.Fatalf("waiter err=%v", waiter.err)
		}
		if waiter.statusCode != http.StatusOK {
			t.Fatalf("waiter status=%d body=%s; want 200", waiter.statusCode, safeAccountSlotBody(waiter.body))
		}
		assertAccountSlotPeak(t, peak)
		successes := append(holders, waiter)
		for _, result := range successes {
			assertAccountSlotSuccessPG(t, ctx, pgPool, seed, result.logicalID)
		}
		waitForAccountSlotInFlight(t, ctx, pgPool, seed.providerAccountID, 0, accountSlotE2ECapacity)
		assertAccountSlotNoLeaks(t, ctx, pgPool, seed, len(successes), 0, 0)
	})

	t.Run("max_waiting_plus_one_overflows_fast", func(t *testing.T) {
		seed := seedAccountSlotQueueWaitScenario(t, ctx, pgPool, pricingVersion, accountSlotE2EOverflowMaxWait, accountSlotE2EWaitTimeoutMS)
		holders, waitHolders, peak := startAccountSlotHoldersAndSampler(t, ctx, pgPool, client, addr, seed)
		firstDone := make(chan accountSlotHTTPResult, 1)
		go func() {
			firstDone <- postAccountSlotChat(ctx, client, addr, seed.bearer, "account-slot-overflow-waiter-"+uuid.NewString())
		}()
		time.Sleep(100 * time.Millisecond)
		overflowStart := time.Now()
		overflow := postAccountSlotChat(ctx, client, addr, seed.bearer, "account-slot-overflow-fast-"+uuid.NewString())
		overflowElapsed := time.Since(overflowStart)
		first := <-firstDone
		waitHolders()
		if overflow.err != nil {
			t.Fatalf("overflow err=%v", overflow.err)
		}
		if overflow.statusCode != http.StatusTooManyRequests || !bytes.Contains(overflow.body, []byte("queue_wait")) {
			t.Fatalf("overflow status=%d body=%s; want 429/queue_wait", overflow.statusCode, safeAccountSlotBody(overflow.body))
		}
		if overflowElapsed > 800*time.Millisecond {
			t.Fatalf("overflow elapsed=%s want 快速小于 holder delay", overflowElapsed)
		}
		if first.err != nil || first.statusCode != http.StatusOK {
			t.Fatalf("first waiter result status=%d err=%v body=%s; want 200", first.statusCode, first.err, safeAccountSlotBody(first.body))
		}
		assertAccountSlotPeak(t, peak)
		successes := append(holders, first)
		for _, result := range successes {
			assertAccountSlotSuccessPG(t, ctx, pgPool, seed, result.logicalID)
		}
		assertAccountSlotRejectPG(t, ctx, pgPool, seed, overflow.logicalID)
		waitForAccountSlotInFlight(t, ctx, pgPool, seed.providerAccountID, 0, accountSlotE2ECapacity)
		assertAccountSlotNoLeaks(t, ctx, pgPool, seed, len(successes), 1, 0)
	})

	t.Run("timeout_aborts_claim_and_releases_hold", func(t *testing.T) {
		seed := seedAccountSlotQueueWaitScenario(t, ctx, pgPool, pricingVersion, accountSlotE2EMaxWaiting, accountSlotE2EShortTimeoutMS)
		holders, waitHolders, peak := startAccountSlotHoldersAndSampler(t, ctx, pgPool, client, addr, seed)
		waiter := postAccountSlotChat(ctx, client, addr, seed.bearer, "account-slot-timeout-"+uuid.NewString())
		waitHolders()
		if waiter.err != nil {
			t.Fatalf("timeout waiter err=%v", waiter.err)
		}
		if waiter.statusCode != http.StatusTooManyRequests || !bytes.Contains(waiter.body, []byte("queue_wait")) {
			t.Fatalf("timeout status=%d body=%s; want 429/queue_wait", waiter.statusCode, safeAccountSlotBody(waiter.body))
		}
		if got := retryAfterFromAccountSlotResult(waiter); got != "1" {
			t.Fatalf("timeout Retry-After=%q want 1", got)
		}
		assertAccountSlotPeak(t, peak)
		for _, result := range holders {
			assertAccountSlotSuccessPG(t, ctx, pgPool, seed, result.logicalID)
		}
		assertAccountSlotRejectPG(t, ctx, pgPool, seed, waiter.logicalID)
		waitForAccountSlotInFlight(t, ctx, pgPool, seed.providerAccountID, 0, accountSlotE2ECapacity)
		assertAccountSlotNoLeaks(t, ctx, pgPool, seed, len(holders), 1, 0)
	})

	t.Run("disconnect_releases_waiting_slot", func(t *testing.T) {
		seed := seedAccountSlotQueueWaitScenario(t, ctx, pgPool, pricingVersion, accountSlotE2EMaxWaiting, accountSlotE2EWaitTimeoutMS)
		holders, waitHolders, peak := startAccountSlotHoldersAndSampler(t, ctx, pgPool, client, addr, seed)
		cancelLogicalID := "account-slot-disconnect-" + uuid.NewString()
		waiterCtx, waiterCancel := context.WithCancel(ctx)
		cancelDone := make(chan accountSlotHTTPResult, 1)
		go func() {
			timer := time.AfterFunc(100*time.Millisecond, waiterCancel)
			defer timer.Stop()
			cancelDone <- postAccountSlotChat(waiterCtx, client, addr, seed.bearer, cancelLogicalID)
		}()
		cancelled := <-cancelDone
		if cancelled.err == nil {
			t.Fatalf("cancelled waiter status=%d body=%s; want client-side cancel error", cancelled.statusCode, safeAccountSlotBody(cancelled.body))
		}
		replacement := postAccountSlotChat(ctx, client, addr, seed.bearer, "account-slot-after-disconnect-"+uuid.NewString())
		waitHolders()
		waitAccountSlotClaimStatus(t, ctx, pgPool, seed, cancelLogicalID, "aborted")
		if replacement.err != nil || replacement.statusCode != http.StatusOK {
			t.Fatalf("replacement status=%d err=%v body=%s; want 200 after cancelled waiter released slot",
				replacement.statusCode, replacement.err, safeAccountSlotBody(replacement.body))
		}
		assertAccountSlotPeak(t, peak)
		successes := append(holders, replacement)
		for _, result := range successes {
			assertAccountSlotSuccessPG(t, ctx, pgPool, seed, result.logicalID)
		}
		assertAccountSlotRejectReasonPG(t, ctx, pgPool, seed, cancelLogicalID, "queue_wait_cancelled")
		waitForAccountSlotInFlight(t, ctx, pgPool, seed.providerAccountID, 0, accountSlotE2ECapacity)
		assertAccountSlotNoLeaks(t, ctx, pgPool, seed, len(successes), 1, 0)
	})
}

func seedAccountSlotQueueWaitScenario(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, pricingVersion string, maxWaiting, timeoutMS int32) *smokeSeed {
	t.Helper()
	seed := seedSmokeGraph(t, ctx, pgPool)
	seedAccountSlotE2EConfig(t, ctx, pgPool, seed, pricingVersion)
	resetAccountSlotQueueWaitScenario(t, ctx, pgPool, seed, maxWaiting, timeoutMS)
	return seed
}

func resetAccountSlotQueueWaitScenario(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *smokeSeed, maxWaiting, timeoutMS int32) {
	t.Helper()
	if _, err := pgPool.Exec(ctx,
		`UPDATE provider_accounts
		    SET cap_concurrency=$1,
		        cap_queue_fallback=$2,
		        in_flight_count=0,
		        priority=100
		  WHERE id=$3 AND tenant_id=$4`,
		accountSlotE2ECapacity, maxWaiting, seed.providerAccountID, seed.tenantID,
	); err != nil {
		t.Fatalf("reset provider account: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`UPDATE pool_groups
		    SET fallback_wait_max_waiting=$1,
		        fallback_wait_timeout_ms=$2
		  WHERE id=$3 AND tenant_id=$4`,
		maxWaiting, timeoutMS, seed.poolGroupID, seed.tenantID,
	); err != nil {
		t.Fatalf("reset pool group wait policy: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`UPDATE user_balances
		    SET balance=1000000.00, held=0, version=version+1, updated_at=now()
		  WHERE tenant_id=$1 AND user_id=$2`,
		seed.tenantID, seed.userID,
	); err != nil {
		t.Fatalf("reset balance: %v", err)
	}
}

func startAccountSlotHoldersAndSampler(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, client *http.Client, addr string, seed *smokeSeed) ([]accountSlotHTTPResult, func(), func() int32) {
	t.Helper()
	samplerCtx, stopSampler := context.WithCancel(ctx)
	sampler := &accountSlotInFlightSampler{}
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		sampler.run(samplerCtx, pgPool, seed.providerAccountID)
	}()

	capacity := int(accountSlotE2ECapacity)
	holderResults := make([]accountSlotHTTPResult, capacity)
	holderStart := make(chan struct{})
	var holderWG sync.WaitGroup
	for i := range holderResults {
		i := i
		holderWG.Add(1)
		go func() {
			defer holderWG.Done()
			<-holderStart
			logicalID := fmt.Sprintf("account-slot-holder-%02d-%s", i, uuid.NewString())
			holderResults[i] = postAccountSlotChat(ctx, client, addr, seed.bearer, logicalID)
		}()
	}
	close(holderStart)
	fullSeen := waitForAccountSlotInFlight(t, ctx, pgPool, seed.providerAccountID, accountSlotE2ECapacity, accountSlotE2ECapacity)
	sampler.observe(fullSeen)

	waitHolders := func() {
		holderWG.Wait()
		for _, result := range holderResults {
			if result.err != nil {
				t.Fatalf("holder logical_id=%s err=%v", result.logicalID, result.err)
			}
			if result.statusCode != http.StatusOK {
				t.Fatalf("holder logical_id=%s status=%d body=%s; want 200",
					result.logicalID, result.statusCode, safeAccountSlotBody(result.body))
			}
		}
		stopSampler()
		<-samplerDone
	}
	peak := func() int32 {
		got, err := sampler.result()
		if err != nil {
			t.Fatalf("采样 provider_accounts.in_flight_count: %v", err)
		}
		return got
	}
	return holderResults, waitHolders, peak
}

func assertAccountSlotPeak(t *testing.T, peak func() int32) {
	t.Helper()
	if got := peak(); got != accountSlotE2ECapacity {
		t.Fatalf("账号在途峰值=%d want %d;峰值必须打满但不能越过 cap", got, accountSlotE2ECapacity)
	}
}

func waitAccountSlotClaimStatus(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *smokeSeed, logicalID, wantStatus string) int64 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var (
			claimID int64
			status  string
		)
		err := pgPool.QueryRow(ctx,
			`SELECT id, status
			   FROM billing_ledger_claims
			  WHERE tenant_id=$1 AND api_key_id=$2 AND logical_request_id=$3`,
			seed.tenantID, seed.apiKeyID, logicalID,
		).Scan(&claimID, &status)
		if err == nil && status == wantStatus {
			return claimID
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("read claim logical_id=%s: %v", logicalID, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("claim logical_id=%s status=%q err=%v want %q", logicalID, status, err, wantStatus)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func retryAfterFromAccountSlotResult(result accountSlotHTTPResult) string {
	return result.retryAfter
}
