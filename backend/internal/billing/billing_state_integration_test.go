//go:build integration_pg

package billing

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

func TestBillingState_PartialRSTCommitsDeliveredTokens(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "billing-state-partial-rst")
	settler := NewSettler(pool)

	req := settleRequest(seed, decimal.RequireFromString("0.02000000"))
	req.Stream = true
	req.Provider = "openai"
	req.Draft.EndClass = gateway.UpstreamError5xx
	req.Draft.UsageSource = gateway.UsageSourcePartial
	req.Draft.TokensOutput = 0
	req.Draft.DeliveredTokenCount = 3
	req.Draft.PendingReconciliation = true

	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("Settle partial RST: %v", err)
	}

	assertStreamStateRow(t, ctx, pool, seed.claimID, int16(StreamStatePartial), 3, "upstream_5xx", "0.02000000")
}

func TestBillingState_FailedBeforeOutputNoCharge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pool, "billing-state-failed-timeout")
	settler := NewSettler(pool)

	req := settleRequest(seed, decimal.RequireFromString("0.02000000"))
	req.Stream = true
	req.Provider = "anthropic"
	req.Draft.EndClass = gateway.TotalStreamTimeout
	req.Draft.UsageSource = gateway.UsageSourceAmbiguous
	req.Draft.TokensInput = 0
	req.Draft.TokensOutput = 0
	req.Draft.DeliveredTokenCount = 0

	if _, err := settler.Settle(ctx, req); err != nil {
		t.Fatalf("Settle failed timeout: %v", err)
	}

	assertStreamStateRow(t, ctx, pool, seed.claimID, int16(StreamStateFailed), 0, "upstream_timeout", "0.00000000")
}

func assertStreamStateRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, claimID int64, wantState int16, wantDelivered int64, wantReason, wantCost string) {
	t.Helper()
	var usageState int16
	var usageDelivered int64
	var usageReason *string
	var usageCost decimal.Decimal
	var tokensOutput int32
	if err := pool.QueryRow(ctx,
		`SELECT stream_state, delivered_token_count, stream_terminated_reason, actual_cost, tokens_output
		 FROM usage_records WHERE claim_id=$1`,
		claimID,
	).Scan(&usageState, &usageDelivered, &usageReason, &usageCost, &tokensOutput); err != nil {
		t.Fatalf("read usage stream state: %v", err)
	}
	if usageState != wantState || usageDelivered != wantDelivered {
		t.Fatalf("usage state/delivered=%d/%d want %d/%d", usageState, usageDelivered, wantState, wantDelivered)
	}
	if gotReason := stringValue(usageReason); gotReason != wantReason {
		t.Fatalf("usage reason=%q want %q", gotReason, wantReason)
	}
	if usageCost.StringFixed(8) != wantCost {
		t.Fatalf("usage cost=%s want %s", usageCost.StringFixed(8), wantCost)
	}
	if int64(tokensOutput) != wantDelivered {
		t.Fatalf("tokens_output=%d want delivered %d", tokensOutput, wantDelivered)
	}

	var eventState int16
	var eventDelivered int64
	var eventReason *string
	var eventCost decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT stream_state, delivered_token_count, stream_terminated_reason, actual_cost
		 FROM billing_events WHERE claim_id=$1`,
		claimID,
	).Scan(&eventState, &eventDelivered, &eventReason, &eventCost); err != nil {
		t.Fatalf("read event stream state: %v", err)
	}
	if eventState != wantState || eventDelivered != wantDelivered {
		t.Fatalf("event state/delivered=%d/%d want %d/%d", eventState, eventDelivered, wantState, wantDelivered)
	}
	if gotReason := stringValue(eventReason); gotReason != wantReason {
		t.Fatalf("event reason=%q want %q", gotReason, wantReason)
	}
	if eventCost.StringFixed(8) != wantCost {
		t.Fatalf("event cost=%s want %s", eventCost.StringFixed(8), wantCost)
	}
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
