package privacy_test

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
)

func TestATPRIV001IntegrationSentinelInjectionAcrossSinks(t *testing.T) {
	ctx := context.Background()
	sentinel := "PROMPT_SENTINEL_secret"
	var scanned []string

	_, priv, err := ed25519.GenerateKey(strings.NewReader(strings.Repeat("a", 128)))
	if err != nil {
		t.Fatalf("ed25519 key: %v", err)
	}
	signer, err := auditledger.NewLocalEd25519Signer(priv, nil)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	prepared, err := auditledger.PrepareEntry(ctx, auditledger.LedgerEntry{
		RequestID: "req_priv_integration",
		TenantID:  17,
		HopChain: []proto.HopAttestation{{
			SchemaVersion: "trust.hop.v1",
			HopIndex:      0,
			HopKind:       "ingress_auth",
			Actor:         "gateway",
			DecisionRef:   "reason_class=safe_metadata " + sentinel,
			RequestID:     "req_priv_integration",
		}},
		ModelChain: &proto.ModelChain{Requested: "claude", RouteDecided: "claude", UpstreamReported: "claude", Verdict: "match"},
	})
	if err != nil {
		t.Fatalf("ledger prepare: %v", err)
	}
	entry, err := ledger.Append(ctx, prepared)
	if err != nil {
		t.Fatalf("ledger append: %v", err)
	}
	ledgerRaw, _ := json.Marshal(entry)
	scanned = append(scanned, string(ledgerRaw))

	outbox := obsdlq.NewMemoryOutbox()
	ev, err := outbox.Enqueue(ctx, obsdlq.OutboxEvent{
		TenantID:  17,
		EventType: obsdlq.EventTypeChannelAlert,
		Payload:   json.RawMessage(`{"request_id":"req_priv_integration","prompt":"PROMPT_SENTINEL_secret","credential_version":3}`),
	})
	if err != nil {
		t.Fatalf("outbox enqueue: %v", err)
	}
	if err := outbox.MarkFailedDead(ctx, ev.ID, "upstream body "+sentinel); err != nil {
		t.Fatalf("outbox dead: %v", err)
	}
	for _, item := range outbox.Snapshot() {
		raw, _ := json.Marshal(item)
		scanned = append(scanned, string(raw))
	}
	for _, item := range outbox.DeadEvents() {
		raw, _ := json.Marshal(item)
		scanned = append(scanned, string(raw))
	}

	chStore := channelhealth.NewMemoryStore()
	err = chStore.AppendAudit(ctx, channelhealth.AuditEvent{
		Type: channelhealth.EventDisabled,
		Key: channelhealth.ChannelKey{
			TenantID: 17, Vendor: "anthropic", AccountCredentialID: 9, CredentialVersion: 1,
		},
		NewState:      channelhealth.StateDisabled,
		ReasonClass:   channelhealth.SignalUpstream5xx,
		PolicyVersion: "channel-health-v1",
		Payload:       map[string]any{"evidence": sentinel, "total_attempts": 3},
		OccurredAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("channel audit: %v", err)
	}
	chRaw, _ := json.Marshal(chStore.Audits())
	scanned = append(scanned, string(chRaw))

	voucherSink := voucher.NewMemoryAuditSink()
	err = voucherSink.EmitVoucherAudit(ctx, voucher.AuditEvent{
		EventType:       voucher.AuditVoucherRedeemed,
		TenantID:        17,
		UserID:          3,
		RequestID:       "req_priv_integration",
		CodeFingerprint: "abc123",
		Payload:         map[string]any{"voucher_code": "CODE-" + sentinel, "amount_cents": 100},
		OccurredAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("voucher audit: %v", err)
	}
	voucherRaw, _ := json.Marshal(voucherSink.Events())
	scanned = append(scanned, string(voucherRaw))

	billingLike := privacy.SafePayloadOrBlocked(ctx, map[string]any{
		"request_id":      "req_priv_integration",
		"ledger_id":       "ldg_1",
		"input_tokens":    10,
		"output_tokens":   20,
		"cost_microcents": 30,
		"raw_body":        sentinel,
	})
	scanned = append(scanned, string(billingLike))

	all := strings.Join(scanned, "\n")
	if strings.Contains(all, sentinel) || strings.Contains(all, "voucher_code") || strings.Contains(all, "raw_body") {
		t.Fatalf("sentinel leaked across sinks:\n%s", all)
	}
}
