//go:build integration_pg

package billing

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestUsageOutcomeErrorFilter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin usage outcome tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	fixture := seedUsageOutcomeFixture(t, ctx, tx)
	q := New(tx)

	success := "success"
	errorOnly := "error"
	all := "all"

	// 变异:忽略 outcome 会丢掉 WHERE,让 error 查询返回成功行。
	errorRows, err := q.ListUsageRecords(ctx, ListUsageRecordsParams{TenantID: &fixture.tenantID, Outcome: &errorOnly, PageLimit: 20})
	if err != nil {
		t.Fatalf("list error outcome: %v", err)
	}
	assertUsageOutcomeRows(t, errorRows, []string{"upstream_error_5xx"})
	assertUsageOutcomeCount(t, ctx, q, fixture.tenantID, &errorOnly, 1)

	successRows, err := q.ListUsageRecords(ctx, ListUsageRecordsParams{TenantID: &fixture.tenantID, Outcome: &success, PageLimit: 20})
	if err != nil {
		t.Fatalf("list success outcome: %v", err)
	}
	assertUsageOutcomeRows(t, successRows, []string{"stream_end_graceful", "non_streaming"})
	assertUsageOutcomeCount(t, ctx, q, fixture.tenantID, &success, 2)

	allRows, err := q.ListUsageRecords(ctx, ListUsageRecordsParams{TenantID: &fixture.tenantID, Outcome: &all, PageLimit: 20})
	if err != nil {
		t.Fatalf("list all outcome: %v", err)
	}
	assertUsageOutcomeRows(t, allRows, []string{"upstream_error_5xx", "stream_end_graceful", "non_streaming"})
	assertUsageOutcomeCount(t, ctx, q, fixture.tenantID, &all, 3)

	defaultRows, err := q.ListUsageRecords(ctx, ListUsageRecordsParams{TenantID: &fixture.tenantID, PageLimit: 20})
	if err != nil {
		t.Fatalf("list default outcome: %v", err)
	}
	assertUsageOutcomeRows(t, defaultRows, []string{"upstream_error_5xx", "stream_end_graceful", "non_streaming"})
}

type usageOutcomeFixture struct {
	tenantID          int64
	userID            int64
	apiKeyID          int64
	providerAccountID int64
	poolID            int64
}

func openUsageOutcomePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping usage outcome integration test")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return pool
}

func seedUsageOutcomeFixture(t *testing.T, ctx context.Context, tx pgx.Tx) usageOutcomeFixture {
	t.Helper()
	suffix := fmt.Sprintf("usage-outcome-%d", time.Now().UnixNano())
	var f usageOutcomeFixture
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, suffix).Scan(&f.tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO users (tenant_id, email, display_name) VALUES ($1, $2, $3) RETURNING id`, f.tenantID, suffix+"@example.test", "Usage Outcome").Scan(&f.userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix) VALUES ($1, $2, $3, $4, $5) RETURNING id`, f.tenantID, f.userID, suffix, "hash-"+suffix, "prefix-"+suffix[:8]).Scan(&f.apiKeyID); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	var providerID int64
	if err := tx.QueryRow(ctx, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, $3, $4) RETURNING id`, f.tenantID, "provider-"+suffix, "Provider "+suffix, "anthropic_messages").Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, f.tenantID, "pool-"+suffix).Scan(&f.poolID); err != nil {
		t.Fatalf("insert pool: %v", err)
	}
	var channelID int64
	if err := tx.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, f.tenantID, f.poolID, "channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, health_state) VALUES ($1, $2, $3, $4, 'api_key', 'healthy') RETURNING id`, f.tenantID, providerID, channelID, "account-"+suffix).Scan(&f.providerAccountID); err != nil {
		t.Fatalf("insert provider account: %v", err)
	}

	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	seedUsageOutcomeRecord(t, ctx, tx, f, suffix+"-success-non-stream", "non_streaming", base.Add(time.Second))
	seedUsageOutcomeRecord(t, ctx, tx, f, suffix+"-success-stream", "stream_end_graceful", base.Add(2*time.Second))
	seedUsageOutcomeRecord(t, ctx, tx, f, suffix+"-error-5xx", "upstream_error_5xx", base.Add(3*time.Second))
	return f
}

func seedUsageOutcomeRecord(t *testing.T, ctx context.Context, tx pgx.Tx, f usageOutcomeFixture, logicalRequestID, endClass string, settledAt time.Time) {
	t.Helper()
	acquisitionToken := uuid.New()
	var claimID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			provider_account_id, acquisition_token, attempt_seq, billing_policy_version,
			request_class, predicted_cost, actual_cost, currency_code, status, settled_at,
			lease_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'messages', 'claude-usage-outcome', $7,
			$8, $9, 1, 'bp-test', 'standard', 0.01000000, 0.01000000, 'USD',
			'committed', $10, $11)
		RETURNING id
	`, f.tenantID, "idem-"+logicalRequestID, "fingerprint-"+logicalRequestID, f.apiKeyID, f.userID, logicalRequestID, f.poolID, f.providerAccountID, acquisitionToken, settledAt, settledAt.Add(time.Hour)).Scan(&claimID); err != nil {
		t.Fatalf("insert claim %s: %v", logicalRequestID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq, tokens_input, tokens_output, actual_cost,
			input_cost, output_cost, end_class, usage_source, pending_reconciliation,
			requested_at, settled_at, requested_model, upstream_model, stream,
			settlement_source
		)
		VALUES ($1, $2, $3, $4, $5, $6, 1, 10, 20, 0.01000000, 0.00400000,
			0.00600000, $7, 'reported', false, $8, $9, 'claude-usage-outcome',
			'claude-usage-outcome-upstream', false, 'provider_upstream')
	`, f.tenantID, claimID, f.apiKeyID, f.userID, f.providerAccountID, acquisitionToken, endClass, settledAt.Add(-time.Second), settledAt); err != nil {
		t.Fatalf("insert usage %s: %v", logicalRequestID, err)
	}
}

func assertUsageOutcomeRows(t *testing.T, rows []ListUsageRecordsRow, want []string) {
	t.Helper()
	if len(rows) != len(want) {
		t.Fatalf("row count=%d want=%d rows=%v", len(rows), len(want), usageOutcomeEndClasses(rows))
	}
	for i, row := range rows {
		if row.EndClass != want[i] {
			t.Fatalf("row %d end_class=%q want=%q all=%v", i, row.EndClass, want[i], usageOutcomeEndClasses(rows))
		}
	}
}

func assertUsageOutcomeCount(t *testing.T, ctx context.Context, q *Queries, tenantID int64, outcome *string, want int64) {
	t.Helper()
	got, err := q.CountUsageRecords(ctx, CountUsageRecordsParams{TenantID: &tenantID, Outcome: outcome})
	if err != nil {
		t.Fatalf("count outcome %v: %v", outcome, err)
	}
	if got != want {
		t.Fatalf("count=%d want=%d outcome=%v", got, want, outcome)
	}
}

func usageOutcomeEndClasses(rows []ListUsageRecordsRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.EndClass)
	}
	return out
}
