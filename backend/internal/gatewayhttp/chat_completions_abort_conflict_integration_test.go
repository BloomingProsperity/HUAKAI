//go:build integration_pg

package gatewayhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

// TestAbortConflictHTTP_ATCD3003_RealPGPreservesContractAndExpeditesLease
// 用真实 Abort Tx2 连续制造九次 40001，覆盖数据库状态与 HTTP 出口的同一条链。
func TestAbortConflictHTTP_ATCD3003_RealPGPreservesContractAndExpeditesLease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openAbortConflictHTTPPool(t, ctx)
	seed := seedAbortConflictHTTPClaim(t, ctx, pool)
	sequenceRef, cleanupFault := installAbortConflictHTTPFault(t, ctx, pool, seed.claimID, 9)
	defer cleanupFault()

	deps := clientAdapterDeps(t)
	deps.Auth = stubAuth{identity: auth.Identity{TenantID: seed.tenantID, APIKeyID: seed.apiKeyID, UserID: seed.userID}}
	deps.ClaimGate = &pr5ClaimGate{claimID: seed.claimID}
	deps.Selector = claimRaceSelector{}
	deps.Settler = billing.NewSettler(pool)
	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s，want 409", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After=%q，want 1", got)
	}
	if got := rec.Header().Get("X-Huakai-Abort-Failed"); got != "abort_failed" {
		t.Fatalf("X-Huakai-Abort-Failed=%q，want abort_failed", got)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error body：%v body=%s", err, rec.Body.String())
	}
	if envelope.Error.Code != "claim_race" {
		t.Fatalf("stable error code=%q，want claim_race", envelope.Error.Code)
	}
	var attempts int64
	if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT last_value FROM %s`, sequenceRef)).Scan(&attempts); err != nil {
		t.Fatalf("read Tx2 attempts：%v", err)
	}
	if attempts != 9 {
		t.Fatalf("Tx2 attempts=%d，want 9", attempts)
	}

	var claimStatus, holdState string
	var lease, dbNow time.Time
	var held decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT c.status, c.lease_expires_at, h.state, b.held, clock_timestamp()
		 FROM billing_ledger_claims c
		 JOIN balance_holds h ON h.claim_id=c.id
		 JOIN user_balances b ON b.tenant_id=c.tenant_id AND b.user_id=c.user_id
		 WHERE c.tenant_id=$1 AND c.id=$2`,
		seed.tenantID, seed.claimID,
	).Scan(&claimStatus, &lease, &holdState, &held, &dbNow); err != nil {
		t.Fatalf("read failed abort state：%v", err)
	}
	if claimStatus != "reserving" || holdState != "held" || !held.Equal(decimal.RequireFromString("0.01000000")) || lease.After(dbNow) {
		t.Fatalf("status/hold/held/lease=%s/%s/%s/%s，want reserving/held/0.01000000/<=%s", claimStatus, holdState, held, lease, dbNow)
	}
	var events, usage int
	if err := pool.QueryRow(ctx,
		`SELECT
		 (SELECT count(*) FROM billing_events WHERE claim_id=$1),
		 (SELECT count(*) FROM usage_records WHERE claim_id=$1)`,
		seed.claimID,
	).Scan(&events, &usage); err != nil {
		t.Fatalf("count rolled-back evidence：%v", err)
	}
	if events != 0 || usage != 0 {
		t.Fatalf("events/usage=%d/%d，want 0/0", events, usage)
	}
}

type abortConflictHTTPSeed struct {
	tenantID int64
	apiKeyID int64
	userID   int64
	claimID  int64
}

func openAbortConflictHTTPPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Fatal("integration_pg 必须显式设置 HUAKAI_DATABASE_URL")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL：%v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedAbortConflictHTTPClaim(t *testing.T, ctx context.Context, pool *pgxpool.Pool) abortConflictHTTPSeed {
	t.Helper()
	unique := uuid.NewString()
	var seed abortConflictHTTPSeed
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "cd3-http-"+unique).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant：%v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "cd3-http-user-"+unique,
	).Scan(&seed.userID); err != nil {
		t.Fatalf("seed user：%v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, '$2a$10$placeholder-not-resolved-by-test', 'hk_cd3_http', 'active') RETURNING id`,
		seed.tenantID, seed.userID, "cd3-http-key-"+unique,
	).Scan(&seed.apiKeyID); err != nil {
		t.Fatalf("seed api key：%v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0)`,
		seed.tenantID, seed.userID,
	); err != nil {
		t.Fatalf("seed user balance：%v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, billing_policy_version,
			request_class, predicted_cost, currency_code, lease_expires_at
		 ) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4o', 'test-policy',
			'default', 0.01, 'USD', NOW()+interval '90 seconds'
		 ) RETURNING id`,
		seed.tenantID, "cd3-http-idem-"+unique, "cd3-http-fp-"+unique, seed.apiKeyID, seed.userID,
		"cd3-http-logical-"+unique,
	).Scan(&seed.claimID); err != nil {
		t.Fatalf("seed claim：%v", err)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin hold Tx：%v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := billing.Reserve(ctx, tx, billing.ReserveParams{
		TenantID: seed.tenantID,
		UserID:   seed.userID,
		ClaimID:  seed.claimID,
		Cost:     decimal.RequireFromString("0.01000000"),
	}); err != nil {
		t.Fatalf("reserve balance hold：%v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit balance hold：%v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM usage_record_dlq WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM usage_records WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM balance_holds WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM user_balances WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_keys WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, seed.tenantID)
	})
	return seed
}

func installAbortConflictHTTPFault(t *testing.T, ctx context.Context, pool *pgxpool.Pool, claimID int64, failures int) (string, func()) {
	t.Helper()
	suffix := fmt.Sprintf("%d_%d", claimID, time.Now().UTC().UnixNano())
	sequenceName := "huakai_cd3_http_seq_" + suffix
	functionName := "huakai_cd3_http_fail_" + suffix
	triggerName := "huakai_cd3_http_fail_" + suffix
	sequenceID := pgx.Identifier{"public", sequenceName}.Sanitize()
	functionID := pgx.Identifier{"public", functionName}.Sanitize()
	triggerID := pgx.Identifier{triggerName}.Sanitize()
	sequenceRef := "public." + sequenceName
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE SEQUENCE %s`, sequenceID)); err != nil {
		t.Fatalf("create conflict sequence：%v", err)
	}
	createFunction := fmt.Sprintf(`
CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.claim_id = %d
		AND NEW.event_type = 'claim_aborted'
		AND nextval('%s'::regclass) <= %d THEN
		RAISE EXCEPTION 'forced HTTP abort conflict' USING ERRCODE = '40001';
	END IF;
	RETURN NEW;
END;
$$`, functionID, claimID, sequenceRef, failures)
	if _, err := pool.Exec(ctx, createFunction); err != nil {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, sequenceID))
		t.Fatalf("create conflict function：%v", err)
	}
	createTrigger := fmt.Sprintf(`
CREATE TRIGGER %s
BEFORE INSERT ON billing_events
FOR EACH ROW EXECUTE FUNCTION %s()`, triggerID, functionID)
	if _, err := pool.Exec(ctx, createTrigger); err != nil {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionID))
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, sequenceID))
		t.Fatalf("create conflict trigger：%v", err)
	}
	return sequenceRef, func() {
		if _, err := pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON billing_events`, triggerID)); err != nil {
			t.Errorf("drop HTTP conflict trigger：%v", err)
		}
		if _, err := pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionID)); err != nil {
			t.Errorf("drop HTTP conflict function：%v", err)
		}
		if _, err := pool.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, sequenceID)); err != nil {
			t.Errorf("drop HTTP conflict sequence：%v", err)
		}
	}
}
