//go:build integration_pg

package billing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// MUTATION: GROUP BY tenant_id instead of provider_account_id -> A/B rows merge -> RED.
func TestUsageCountsByChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin usage counts tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	fixture := seedObsMiscFixture(t, ctx, tx)
	base := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	seedObsMiscUsage(t, ctx, tx, fixture, fixture.providerAccountAID, "count-a-1", base.Add(time.Minute), 10, 20, "1.00000000")
	seedObsMiscUsage(t, ctx, tx, fixture, fixture.providerAccountAID, "count-a-2", base.Add(2*time.Minute), 30, 40, "2.00000000")
	seedObsMiscUsage(t, ctx, tx, fixture, fixture.providerAccountBID, "count-b-1", base.Add(3*time.Minute), 5, 6, "0.50000000")

	rows, err := New(tx).AggregateUsageCountsByProviderAccount(ctx, AggregateUsageCountsByProviderAccountParams{
		FromTs: pgtype.Timestamptz{Time: base, Valid: true},
		ToTs:   pgtype.Timestamptz{Time: base.Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("AggregateUsageCountsByProviderAccount: %v", err)
	}

	byAccount := map[int64]AggregateUsageCountsByProviderAccountRow{}
	for _, row := range rows {
		byAccount[row.ProviderAccountID] = row
	}
	a, ok := byAccount[fixture.providerAccountAID]
	if !ok {
		t.Fatalf("missing provider account A row; rows=%v", rows)
	}
	if a.RequestCount != 2 || a.TotalCost != "3.00000000" || a.TotalInputTokens != 40 || a.TotalOutputTokens != 60 {
		t.Fatalf("provider account A aggregate = %+v, want count=2 cost=3 tokens=40/60", a)
	}
	b, ok := byAccount[fixture.providerAccountBID]
	if !ok {
		t.Fatalf("missing provider account B row; rows=%v", rows)
	}
	if b.RequestCount != 1 || b.TotalCost != "0.50000000" || b.TotalInputTokens != 5 || b.TotalOutputTokens != 6 {
		t.Fatalf("provider account B aggregate = %+v, want count=1 cost=0.5 tokens=5/6", b)
	}
}

// MUTATION: remove ak.tenant_id=ur.tenant_id or u.tenant_id=ur.tenant_id -> SQL text guard catches it,
// while this integration test verifies the happy path returns only same-tenant display names.
func TestListUsageRecordsWithNamesTenantScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin usage names tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	fixture := seedObsMiscFixture(t, ctx, tx)
	other := seedObsMiscFixture(t, ctx, tx)
	base := time.Date(2026, 6, 8, 11, 0, 0, 0, time.UTC)
	seedObsMiscUsage(t, ctx, tx, fixture, fixture.providerAccountAID, "names-main", base, 7, 8, "0.25000000")
	seedObsMiscUsage(t, ctx, tx, other, other.providerAccountAID, "names-other", base, 9, 10, "0.75000000")

	rows, err := New(tx).ListUsageRecordsWithNames(ctx, ListUsageRecordsWithNamesParams{
		TenantID:  &fixture.tenantID,
		FromTs:    pgtype.Timestamptz{Time: base.Add(-time.Minute), Valid: true},
		ToTs:      pgtype.Timestamptz{Time: base.Add(time.Minute), Valid: true},
		PageLimit: 20,
	})
	if err != nil {
		t.Fatalf("ListUsageRecordsWithNames: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%d want 1; rows=%v", len(rows), rows)
	}
	if rows[0].TokenName == nil || *rows[0].TokenName != fixture.apiKeyName {
		t.Fatalf("token_name=%v want %q", rows[0].TokenName, fixture.apiKeyName)
	}
	if rows[0].Username == nil || *rows[0].Username != fixture.userName {
		t.Fatalf("username=%v want %q", rows[0].Username, fixture.userName)
	}
	if rows[0].TokenName != nil && *rows[0].TokenName == other.apiKeyName {
		t.Fatalf("cross-tenant token name leaked: %q", other.apiKeyName)
	}
}

type obsMiscFixture struct {
	tenantID           int64
	userID             int64
	userName           string
	apiKeyID           int64
	apiKeyName         string
	poolID             int64
	providerAccountAID int64
	providerAccountBID int64
}

func seedObsMiscFixture(t *testing.T, ctx context.Context, tx pgx.Tx) obsMiscFixture {
	t.Helper()
	suffix := fmt.Sprintf("obs-misc-%d", time.Now().UnixNano())
	var f obsMiscFixture
	f.userName = "user-" + suffix
	f.apiKeyName = "prod-key-" + suffix
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "tenant-"+suffix).Scan(&f.tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO users (tenant_id, email, display_name) VALUES ($1, $2, $3) RETURNING id`, f.tenantID, suffix+"@example.test", f.userName).Scan(&f.userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix) VALUES ($1, $2, $3, $4, $5) RETURNING id`, f.tenantID, f.userID, f.apiKeyName, "hash-"+suffix, "prefix-"+suffix[:8]).Scan(&f.apiKeyID); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	var providerID int64
	if err := tx.QueryRow(ctx, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, $3, $4) RETURNING id`, f.tenantID, "provider-"+suffix, "Provider "+suffix, "anthropic_messages").Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, f.tenantID, "pool-"+suffix).Scan(&f.poolID); err != nil {
		t.Fatalf("insert pool: %v", err)
	}
	var channelAID, channelBID int64
	if err := tx.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, f.tenantID, f.poolID, "channel-a-"+suffix).Scan(&channelAID); err != nil {
		t.Fatalf("insert channel A: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, f.tenantID, f.poolID, "channel-b-"+suffix).Scan(&channelBID); err != nil {
		t.Fatalf("insert channel B: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, health_state) VALUES ($1, $2, $3, $4, 'api_key', 'healthy') RETURNING id`, f.tenantID, providerID, channelAID, "account-a-"+suffix).Scan(&f.providerAccountAID); err != nil {
		t.Fatalf("insert provider account A: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, health_state) VALUES ($1, $2, $3, $4, 'api_key', 'healthy') RETURNING id`, f.tenantID, providerID, channelBID, "account-b-"+suffix).Scan(&f.providerAccountBID); err != nil {
		t.Fatalf("insert provider account B: %v", err)
	}
	return f
}

func seedObsMiscUsage(t *testing.T, ctx context.Context, tx pgx.Tx, f obsMiscFixture, providerAccountID int64, logicalRequestID string, settledAt time.Time, inputTokens, outputTokens int, cost string) {
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
		VALUES ($1, $2, $3, $4, $5, $6, 'messages', 'claude-obs-misc', $7,
			$8, $9, 1, 'bp-test', 'standard', $10::numeric, $10::numeric, 'USD',
			'committed', $11, $12)
		RETURNING id
	`, f.tenantID, "idem-"+logicalRequestID, "fingerprint-"+logicalRequestID, f.apiKeyID, f.userID, logicalRequestID, f.poolID, providerAccountID, acquisitionToken, cost, settledAt, settledAt.Add(time.Hour)).Scan(&claimID); err != nil {
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
		VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $8, $9::numeric, 0,
			$9::numeric, 'non_streaming', 'reported', false, $10, $11,
			'claude-obs-misc', 'claude-obs-misc-upstream', false, 'provider_upstream')
	`, f.tenantID, claimID, f.apiKeyID, f.userID, providerAccountID, acquisitionToken, inputTokens, outputTokens, cost, settledAt.Add(-time.Second), settledAt); err != nil {
		t.Fatalf("insert usage %s: %v", logicalRequestID, err)
	}
}
