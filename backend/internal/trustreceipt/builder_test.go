package trustreceipt

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestBuildProvisionalFillsProviderAndModelFromEnv(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta.Provider = "anthropic"
	env.RequestMeta.TenantID = 7001
	env.RequestMeta.Model = "public-claude"
	env.RequestMeta.UpstreamModel = "registry-claude"
	env.BufferedResponse = &proto.CanonicalResponse{Model: "delivered-claude"}
	env.Accounting.ModelChain = &proto.ModelChain{
		Requested:        "client-claude",
		RouteDecided:     "routed-claude",
		UpstreamReported: "reported-claude",
	}
	env.Accounting.Usage = proto.CanonicalUsage{
		InputTokens:              11,
		OutputTokens:             22,
		CacheCreationInputTokens: 3,
		CacheReadInputTokens:     5,
	}

	receipt := BuildProvisionalFromEnv(env, auditledger.AuditLedgerResult{}, "req-builder", 4)

	if receipt.RequestID != "req-builder" {
		t.Fatalf("RequestID=%q want req-builder", receipt.RequestID)
	}
	if receipt.ReceiptSequence != 4 {
		t.Fatalf("ReceiptSequence=%d want 4", receipt.ReceiptSequence)
	}
	if got, want := ReceiptID(receipt.RequestID, receipt.ReceiptSequence), "req-builder:4"; got != want {
		t.Fatalf("ReceiptID=%q want %q", got, want)
	}
	if receipt.TenantScopeRef != auditledger.TenantScopeRef(7001) {
		t.Fatalf("TenantScopeRef=%q want %q", receipt.TenantScopeRef, auditledger.TenantScopeRef(7001))
	}
	if receipt.Provider != "anthropic" {
		t.Fatalf("Provider=%q want anthropic", receipt.Provider)
	}
	if receipt.RequestedModel != "client-claude" {
		t.Fatalf("RequestedModel=%q want client-claude", receipt.RequestedModel)
	}
	if receipt.RoutedModel != "routed-claude" {
		t.Fatalf("RoutedModel=%q want routed-claude", receipt.RoutedModel)
	}
	if receipt.UpstreamModel != "registry-claude" {
		t.Fatalf("UpstreamModel=%q want registry-claude", receipt.UpstreamModel)
	}
	if receipt.DeliveredModel != "delivered-claude" {
		t.Fatalf("DeliveredModel=%q want delivered-claude", receipt.DeliveredModel)
	}
	if receipt.TokenCounts.Input != 11 || receipt.TokenCounts.Output != 22 || receipt.TokenCounts.Cached != 8 {
		t.Fatalf("TokenCounts=%+v want input=11 output=22 cached=8", receipt.TokenCounts)
	}
	if receipt.CostCents != 0 {
		t.Fatalf("CostCents=%d want 0 until Accounting carries settled cost", receipt.CostCents)
	}
	if receipt.PriceSnapshot != (PriceSnapshot{}) {
		t.Fatalf("PriceSnapshot=%+v want zero value until env carries price source", receipt.PriceSnapshot)
	}
}

func TestBuildProvisionalValidationStateAlwaysProvisional(t *testing.T) {
	receipt := BuildProvisionalFromEnv(nil, auditledger.AuditLedgerResult{
		State:            auditledger.LedgerResultStatePersisted,
		UpstreamProvider: "openai",
		UpstreamModel:    "gpt-4o",
		RequestID:        "ledger-req",
	}, "req-provisional", 0)

	if receipt.ValidationState != "provisional" {
		t.Fatalf("ValidationState=%q want provisional in TRUST-B-2", receipt.ValidationState)
	}
}

