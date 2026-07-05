//go:build integration_pg

package billing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
)

func TestRepriceUsageRecordsDryRunDoesNotWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pg := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pg, "manual-reprice-dry-run")
	registerRepriceCleanup(t, pg, seed.tenantID)
	version := seedRepriceRateTable(t, ctx, pg)
	seedRepriceRatio(t, ctx, pg, seed.tenantID, seed.poolGroupID, "0.5")
	svc := newIntegrationRepriceService(pg, version)
	settledAt := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	usageID := insertPendingRepriceUsageRecord(t, ctx, pg, seed, repriceUsageFixture{
		tokensInput:  100,
		tokensOutput: 50,
		actualCost:   "0.05000000",
		settledAt:    settledAt,
	})
	beforePending := countPendingRepriceUsageRecords(t, ctx, pg)
	assertRepriceBatchContains(t, ctx, svc, seed.tenantID, settledAt.Add(-time.Minute), settledAt.Add(time.Minute), usageID, true)

	result, err := svc.RepriceUsageRecords(ctx, RepriceRequest{
		UsageRecordID: usageID,
		DryRun:        true,
	})
	if err != nil {
		t.Fatalf("RepriceUsageRecords dry-run: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items=%d want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.Status != RepriceStatusWouldApply {
		t.Fatalf("status=%q want %q", item.Status, RepriceStatusWouldApply)
	}
	if got := item.AuthoritativeCost.StringFixed(8); got != "0.10000000" {
		t.Fatalf("authoritative_cost=%s want 0.10000000", got)
	}
	if got := item.CostDelta.StringFixed(8); got != "0.05000000" {
		t.Fatalf("cost_delta=%s want 0.05000000", got)
	}
	assertRepriceEventCount(t, ctx, pg, usageID, 0)
	assertUsagePending(t, ctx, pg, usageID, true)
	if afterPending := countPendingRepriceUsageRecords(t, ctx, pg); afterPending != beforePending {
		t.Fatalf("dry-run pending count=%d want unchanged %d", afterPending, beforePending)
	}
	assertRepriceBatchContains(t, ctx, svc, seed.tenantID, settledAt.Add(-time.Minute), settledAt.Add(time.Minute), usageID, true)
	assertNoAutomaticMoneyWrites(t, ctx, pg, seed.tenantID)
}

func TestRepriceUsageRecordsApplyWritesEventLogicallyClearsPendingAndIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pg := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pg, "manual-reprice-apply")
	registerRepriceCleanup(t, pg, seed.tenantID)
	version := seedRepriceRateTable(t, ctx, pg)
	seedRepriceRatio(t, ctx, pg, seed.tenantID, seed.poolGroupID, "0.5")
	svc := newIntegrationRepriceService(pg, version)
	reconciledAt := time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)
	usageID := insertPendingRepriceUsageRecord(t, ctx, pg, seed, repriceUsageFixture{
		tokensInput:  100,
		tokensOutput: 50,
		actualCost:   "0.05000000",
		settledAt:    reconciledAt.Add(-time.Minute),
	})
	from := reconciledAt.Add(-2 * time.Minute)
	to := reconciledAt
	beforePending := countPendingRepriceUsageRecords(t, ctx, pg)
	assertRepriceBatchContains(t, ctx, svc, seed.tenantID, from, to, usageID, true)

	result, err := svc.RepriceUsageRecords(ctx, RepriceRequest{
		UsageRecordID: usageID,
		DryRun:        false,
		ReconciledAt:  reconciledAt,
	})
	if err != nil {
		t.Fatalf("RepriceUsageRecords apply: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items=%d want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.Status != RepriceStatusRepriced {
		t.Fatalf("item=%+v want status %q", item, RepriceStatusRepriced)
	}
	if result.Summary.Repriced != 1 || result.Summary.AlreadyRepriced != 0 {
		t.Fatalf("summary=%+v want repriced=1/already_repriced=0", result.Summary)
	}
	if got := item.CostDelta.StringFixed(8); got != "0.05000000" {
		t.Fatalf("response cost_delta=%s want 0.05000000", got)
	}
	assertRepriceEvent(t, ctx, pg, seed.tenantID, usageID, repriceEventExpectation{
		tokensInput:         100,
		tokensOutput:        50,
		authoritativeCost:   "0.10000000",
		costDelta:           "0.05000000",
		source:              RepriceDefaultSource,
		reconciledAtRFC3339: reconciledAt.Format(time.RFC3339),
	})
	assertUsagePending(t, ctx, pg, usageID, true)
	if afterPending := countPendingRepriceUsageRecords(t, ctx, pg); afterPending != beforePending-1 {
		t.Fatalf("pending count after apply=%d want %d", afterPending, beforePending-1)
	}
	assertRepriceBatchContains(t, ctx, svc, seed.tenantID, from, to, usageID, false)
	assertNoAutomaticMoneyWrites(t, ctx, pg, seed.tenantID)

	replay, err := svc.RepriceUsageRecords(ctx, RepriceRequest{
		UsageRecordID: usageID,
		DryRun:        false,
		ReconciledAt:  reconciledAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("RepriceUsageRecords replay: %v", err)
	}
	if len(replay.Items) != 1 {
		t.Fatalf("replay items=%d want 1", len(replay.Items))
	}
	if replay.Items[0].Status != RepriceStatusAlreadyRepriced {
		t.Fatalf("replay item=%+v want already_repriced", replay.Items[0])
	}
	if replay.Summary.AlreadyRepriced != 1 || replay.Summary.Repriced != 0 {
		t.Fatalf("replay summary=%+v want already_repriced=1/repriced=0", replay.Summary)
	}
	assertRepriceEventCount(t, ctx, pg, usageID, 1)
	assertUsagePending(t, ctx, pg, usageID, true)
	assertNoAutomaticMoneyWrites(t, ctx, pg, seed.tenantID)
}

type repriceUsageFixture struct {
	tokensInput  int32
	tokensOutput int32
	actualCost   string
	settledAt    time.Time
}

type repriceEventExpectation struct {
	tokensInput         int32
	tokensOutput        int32
	authoritativeCost   string
	costDelta           string
	source              string
	reconciledAtRFC3339 string
}

func newIntegrationRepriceService(pg *pgxpool.Pool, version string) *RepriceService {
	return NewPostgresRepriceService(
		pg,
		NewPGXRateTableSource(pg),
		pricingcatalog.NewRatioResolver(pricingcatalog.NewPostgresStore(pg), 0),
		version,
	)
}

func seedRepriceRateTable(t *testing.T, ctx context.Context, pg *pgxpool.Pool) string {
	t.Helper()
	version := "manual-reprice-current-" + uuid.NewString()
	data := json.RawMessage(`{
		"models": {
			"gpt-4.1-mini": {
				"input_micro_usd": "1000",
				"output_micro_usd": "2000"
			}
		}
	}`)
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM billing_pricing_versions WHERE version=$1`, version)
	})
	if _, err := pg.Exec(ctx, `
		INSERT INTO billing_pricing_versions (
			tenant_id, version, pricing_data, effective_from, created_by_actor, is_public
		) VALUES (
			0, $1, $2::jsonb, $3, 'integration:test:manual-reprice', true
		)`,
		version,
		string(data),
		time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("seed reprice rate table: %v", err)
	}
	return version
}

func seedRepriceRatio(t *testing.T, ctx context.Context, pg *pgxpool.Pool, tenantID, poolGroupID int64, ratio string) {
	t.Helper()
	if _, err := pg.Exec(ctx, `
		INSERT INTO pool_group_pricing_ratios (
			tenant_id, pool_group_id, ratio, public_ratio, created_by, updated_by
		) VALUES (
			$1, $2, $3::text::numeric(20,8), false, 'integration:test:manual-reprice', 'integration:test:manual-reprice'
		)
		ON CONFLICT (tenant_id, pool_group_id) DO UPDATE
		SET ratio = EXCLUDED.ratio,
		    public_ratio = EXCLUDED.public_ratio,
		    updated_by = EXCLUDED.updated_by,
		    updated_at = now()`,
		tenantID,
		poolGroupID,
		ratio,
	); err != nil {
		t.Fatalf("seed reprice ratio: %v", err)
	}
}

func registerRepriceCleanup(t *testing.T, pg *pgxpool.Pool, tenantID int64) {
	t.Helper()
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pg.Exec(c, `DELETE FROM billing_ledger_adjustments WHERE tenant_id=$1`, tenantID)
		_, _ = pg.Exec(c, `DELETE FROM usage_record_reconciliation_events WHERE tenant_id=$1`, tenantID)
		_, _ = pg.Exec(c, `DELETE FROM pool_group_pricing_ratios WHERE tenant_id=$1`, tenantID)
	})
}

func insertPendingRepriceUsageRecord(t *testing.T, ctx context.Context, pg *pgxpool.Pool, seed settlerSeed, row repriceUsageFixture) int64 {
	t.Helper()
	var id int64
	if err := pg.QueryRow(ctx, `
		INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq,
			tokens_input, tokens_output,
			cache_creation_tokens, cache_read_tokens,
			cache_creation_5m_tokens, cache_creation_1h_tokens, image_output_tokens,
			actual_cost, input_cost, output_cost,
			cache_creation_cost, cache_read_cost, image_output_cost,
			end_class, usage_source, confidence_score, pending_reconciliation,
			stream_state, delivered_token_count, stream_terminated_reason,
			routing_reason, protocol_loss,
			requested_at, settled_at, requested_model, upstream_model,
			stream, snapshot_version, settlement_source
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 1,
			$7, $8,
			0, 0,
			0, 0, 0,
			$9::text::numeric(20,8), 0, 0,
			0, 0, 0,
			'non_streaming', 'reported', 1.0, true,
			$10, $11, 'manual_reprice_fixture',
			'{}'::jsonb, '[]'::jsonb,
			$12, $13, 'gpt-4.1-mini', 'gpt-4.1-mini',
			false, 'registry:99:7;router:v0.1-phase-c', 'provider_upstream'
		)
		RETURNING id`,
		seed.tenantID,
		seed.claimID,
		seed.apiKeyID,
		seed.userID,
		seed.providerAccountID,
		seed.acquisitionToken,
		row.tokensInput,
		row.tokensOutput,
		row.actualCost,
		StreamStatePartial.DBValue(),
		int64(row.tokensOutput),
		row.settledAt.Add(-time.Second),
		row.settledAt,
	).Scan(&id); err != nil {
		t.Fatalf("insert reprice usage record: %v", err)
	}
	return id
}

func assertRepriceEvent(t *testing.T, ctx context.Context, pg *pgxpool.Pool, tenantID, usageID int64, want repriceEventExpectation) {
	t.Helper()
	var (
		tokensInput       int32
		tokensOutput      int32
		authoritativeCost string
		costDelta         string
		source            string
		reconciledAt      time.Time
	)
	if err := pg.QueryRow(ctx, `
		SELECT authoritative_tokens_input,
		       authoritative_tokens_output,
		       authoritative_cost::numeric(20,8)::text,
		       cost_delta::numeric(20,8)::text,
		       reconciliation_source,
		       reconciled_at
		FROM usage_record_reconciliation_events
		WHERE tenant_id=$1 AND original_usage_record_id=$2`,
		tenantID,
		usageID,
	).Scan(&tokensInput, &tokensOutput, &authoritativeCost, &costDelta, &source, &reconciledAt); err != nil {
		t.Fatalf("query reprice event: %v", err)
	}
	if tokensInput != want.tokensInput || tokensOutput != want.tokensOutput {
		t.Fatalf("tokens=(%d,%d) want (%d,%d)", tokensInput, tokensOutput, want.tokensInput, want.tokensOutput)
	}
	if authoritativeCost != want.authoritativeCost || costDelta != want.costDelta {
		t.Fatalf("money=(%s,%s) want (%s,%s)", authoritativeCost, costDelta, want.authoritativeCost, want.costDelta)
	}
	if source != want.source {
		t.Fatalf("source=%q want %q", source, want.source)
	}
	if got := reconciledAt.UTC().Format(time.RFC3339); got != want.reconciledAtRFC3339 {
		t.Fatalf("reconciled_at=%s want %s", got, want.reconciledAtRFC3339)
	}
}

func assertRepriceEventCount(t *testing.T, ctx context.Context, pg *pgxpool.Pool, usageID int64, want int) {
	t.Helper()
	var got int
	if err := pg.QueryRow(ctx, `
		SELECT count(*)::int
		FROM usage_record_reconciliation_events
		WHERE original_usage_record_id=$1`,
		usageID,
	).Scan(&got); err != nil {
		t.Fatalf("count reprice events: %v", err)
	}
	if got != want {
		t.Fatalf("reprice event count=%d want %d", got, want)
	}
}

func countPendingRepriceUsageRecords(t *testing.T, ctx context.Context, pg *pgxpool.Pool) int64 {
	t.Helper()
	got, err := dbbilling.New(pg).CountPendingReconciliationUsageRecords(ctx)
	if err != nil {
		t.Fatalf("count pending reconciliation usage records: %v", err)
	}
	return got
}

func assertRepriceBatchContains(t *testing.T, ctx context.Context, svc *RepriceService, tenantID int64, from, to time.Time, usageID int64, want bool) {
	t.Helper()
	rows, err := svc.loadRepriceRows(ctx, RepriceRequest{
		TenantID: tenantID,
		From:     from,
		To:       to,
		Limit:    RepriceMaxBatchLimit,
	})
	if err != nil {
		t.Fatalf("load reprice batch candidates: %v", err)
	}
	got := false
	for _, row := range rows {
		if row.ID == usageID {
			got = true
			break
		}
	}
	if got != want {
		t.Fatalf("reprice batch contains usage_record %d = %v want %v; rows=%+v", usageID, got, want, rows)
	}
}

func assertUsagePending(t *testing.T, ctx context.Context, pg *pgxpool.Pool, usageID int64, want bool) {
	t.Helper()
	var got bool
	if err := pg.QueryRow(ctx, `
		SELECT pending_reconciliation
		FROM usage_records
		WHERE id=$1`,
		usageID,
	).Scan(&got); err != nil {
		t.Fatalf("query usage pending: %v", err)
	}
	if got != want {
		t.Fatalf("pending_reconciliation=%v want %v", got, want)
	}
}

func assertNoAutomaticMoneyWrites(t *testing.T, ctx context.Context, pg *pgxpool.Pool, tenantID int64) {
	t.Helper()
	for table, query := range map[string]string{
		"billing_events":             `SELECT count(*)::int FROM billing_events WHERE tenant_id=$1`,
		"billing_ledger_adjustments": `SELECT count(*)::int FROM billing_ledger_adjustments WHERE tenant_id=$1`,
	} {
		var got int
		if err := pg.QueryRow(ctx, query, tenantID).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != 0 {
			t.Fatalf("%s rows=%d want 0; 补价切片不得自动写余额/账本调整", table, got)
		}
	}
	var balance, held decimal.Decimal
	if err := pg.QueryRow(ctx, `
		SELECT balance, held
		FROM user_balances
		WHERE tenant_id=$1`,
		tenantID,
	).Scan(&balance, &held); err != nil {
		t.Fatalf("query user balance: %v", err)
	}
	if balance.StringFixed(8) != "10.00000000" || held.StringFixed(8) != "0.00000000" {
		t.Fatalf("user balance=(%s,%s) want (10.00000000,0.00000000)", balance.StringFixed(8), held.StringFixed(8))
	}
}

func TestRepriceUsageRecordsBatchLimitRejectsAboveOneHundred(t *testing.T) {
	_, err := normalizeRepriceRequest(RepriceRequest{
		TenantID: 1,
		From:     time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC),
		To:       time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC),
		Limit:    RepriceMaxBatchLimit + 1,
	})
	if err == nil {
		t.Fatal("limit above 100 must fail")
	}
	if !errors.Is(err, ErrRepriceInvalidInput) {
		t.Fatalf("err=%v want ErrRepriceInvalidInput", err)
	}
}
