//go:build integration_pg

package pool

import (
	"context"
	"testing"
	"time"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

func TestDBAccountSourceProjectsFreshQuotaAndRoutingSignals(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pgPool := openIntegrationPool(t, ctx)
	seed := seedAdapterGraph(t, ctx, pgPool, "quota-routing-projection")
	now := time.Now().UTC()
	if _, err := pgPool.Exec(ctx, `
INSERT INTO provider_account_routing_signals (
    tenant_id, provider_account_id, success_ewma, error_ewma,
    response_latency_ms_ewma, sample_count, last_outcome, observed_at
) VALUES ($1,$2,0.75,0.25,321.5,8,'success',$3)`, seed.tenantID, seed.providerAccountID, now); err != nil {
		t.Fatalf("写路由信号: %v", err)
	}
	if _, err := pgPool.Exec(ctx, `
INSERT INTO provider_account_quota_facts (
    tenant_id, provider_account_id, vendor, metric_key, model_key, state,
    remaining_percent, observed_at, valid_until, source
) VALUES
    ($1,$2,'openai','model_quota','model-target','exhausted',0,$3,$4,'upstream_model_catalog'),
    ($1,$2,'openai','weekly','',          'available',55,$3,$4,'upstream_usage')`,
		seed.tenantID, seed.providerAccountID, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("写额度事实: %v", err)
	}

	source := NewDBAccountSource(dbbilling.New(pgPool))
	accounts, err := source.ListAccounts(ctx, SelectionRequest{TenantID: seed.tenantID, PoolGroupID: seed.poolGroupID, RequestedModel: "model-target"})
	if err != nil || len(accounts) != 1 {
		t.Fatalf("ListAccounts len=%d err=%v", len(accounts), err)
	}
	account := accounts[0]
	if account.UpstreamQuotaState != "exhausted" || !account.UpstreamQuotaRemainingKnown || account.UpstreamQuotaRemaining != 0 {
		t.Fatalf("额度投影=%+v", account)
	}
	if account.RoutingSignalSampleCount != 8 || account.SuccessEWMA != 0.75 || account.ErrorEWMA != 0.25 || account.ResponseLatencyMSEWMA != 321.5 || account.RoutingSignalObservedAt.IsZero() {
		t.Fatalf("路由信号投影=%+v", account)
	}

	if _, err := pgPool.Exec(ctx, `UPDATE provider_account_quota_facts SET observed_at=$3, valid_until=$3 WHERE tenant_id=$1 AND provider_account_id=$2`, seed.tenantID, seed.providerAccountID, now.Add(-3*time.Hour)); err != nil {
		t.Fatalf("模拟额度过期: %v", err)
	}
	accounts, err = source.ListAccounts(ctx, SelectionRequest{TenantID: seed.tenantID, PoolGroupID: seed.poolGroupID, RequestedModel: "model-target"})
	if err != nil || len(accounts) != 1 {
		t.Fatalf("过期后 ListAccounts len=%d err=%v", len(accounts), err)
	}
	if accounts[0].UpstreamQuotaState != "unknown" || accounts[0].UpstreamQuotaRemainingKnown || !accounts[0].UpstreamQuotaObservedAt.IsZero() {
		t.Fatalf("过期额度不得进入调度：%+v", accounts[0])
	}
}
