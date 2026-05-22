package auditledger

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestPreparedEntryJSONRoundTripPreservesAppendIntent(t *testing.T) {
	// Risk killed: DLQ must persist enough append intent to reconstruct a raw
	// LedgerEntry and then re-run PrepareEntry before Append. Losing any JSON
	// shadow field means replay signs a different ledger entry.
	// Mutation self-check: delete request_id, tenant_id, created_at,
	// tenant_scope_ref, hop_chain, or model_chain from the shadow mapping and
	// this test fails because the re-prepared entry no longer matches the
	// fixture.
	prepared := mustPrepareForAppend(t, context.Background(), LedgerEntry{
		Timestamp:      "2026-05-22T12:34:56.789Z",
		RequestID:      "req_prepared_json_roundtrip",
		TenantID:       42,
		TenantScopeRef: TenantScopeRef(42),
		HopChain: []proto.HopAttestation{
			{
				Hop:         proto.HopIngress,
				HopKind:     "ingress",
				HopIndex:    1,
				Actor:       "gateway",
				StartedAt:   "2026-05-22T12:34:56.000Z",
				EndedAt:     "2026-05-22T12:34:56.050Z",
				DecisionRef: "decision-ingress",
				FeatureRefs: []string{"F-TRUST-001", "F-DLQ-001"},
				Timestamp:   "2026-05-22T12:34:56.050Z",
				Detail:      json.RawMessage(`{"latency_ms":50,"phase":"ingress"}`),
			},
			{
				Hop:         proto.HopProvider,
				HopKind:     "provider",
				HopIndex:    2,
				Actor:       "provider",
				StartedAt:   "2026-05-22T12:34:56.100Z",
				EndedAt:     "2026-05-22T12:34:56.220Z",
				DecisionRef: "decision-provider",
				FeatureRefs: []string{"F-PASR-001"},
				Provider:    "openai",
				Endpoint:    "https://api.openai.example/v1/chat/completions",
				Timestamp:   "2026-05-22T12:34:56.220Z",
				Detail:      json.RawMessage(`{"status":200,"cache_hit":false}`),
			},
		},
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "match",
		},
	})

	raw, err := json.Marshal(prepared)
	if err != nil {
		t.Fatalf("Marshal PreparedEntry: %v", err)
	}
	for _, key := range []string{
		"request_id", "tenant_id", "created_at",
		"tenant_scope_ref", "hop_chain", "model_chain",
	} {
		if !jsonKeyPresent(t, raw, key) {
			t.Fatalf("marshaled PreparedEntry missing key %q: %s", key, raw)
		}
	}

	decoded, err := decodeLedgerEntryFromDLQPayload(raw)
	if err != nil {
		t.Fatalf("decode DLQ LedgerEntry JSON: %v", err)
	}
	reprepared, err := PrepareEntry(context.Background(), decoded)
	if err != nil {
		t.Fatalf("PrepareEntry decoded DLQ payload: %v", err)
	}

	if got, want := reprepared.AsLedgerEntry(), prepared.AsLedgerEntry(); !reflect.DeepEqual(got, want) {
		t.Fatalf("PreparedEntry JSON roundtrip mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestPreparedEntryExternalJSONUnmarshalCannotBypassSeal(t *testing.T) {
	// Risk killed: callers outside auditledger must not be able to construct a
	// sealed PreparedEntry by feeding arbitrary JSON into json.Unmarshal. If a
	// public UnmarshalJSON method exists, this test fails because the projection
	// is populated instead of staying zero-valued.
	prepared := mustPrepareForAppend(t, context.Background(), LedgerEntry{
		Timestamp:      "2026-05-22T13:00:00.000Z",
		RequestID:      "req_prepared_json_seal",
		TenantID:       77,
		TenantScopeRef: TenantScopeRef(77),
		HopChain: []proto.HopAttestation{
			{
				Hop:         proto.HopIngress,
				HopKind:     "ingress",
				HopIndex:    1,
				Actor:       "gateway",
				StartedAt:   "2026-05-22T13:00:00.000Z",
				EndedAt:     "2026-05-22T13:00:00.010Z",
				DecisionRef: "decision-seal",
				FeatureRefs: []string{"F-TRUST-SEAL"},
				Timestamp:   "2026-05-22T13:00:00.010Z",
				Detail:      json.RawMessage(`{"contains":"hop_chain"}`),
			},
		},
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o-mini",
			RouteDecided:     "gpt-4o-mini",
			UpstreamReported: "gpt-4o-mini",
			Verdict:          "match",
		},
	})
	raw, err := json.Marshal(prepared)
	if err != nil {
		t.Fatalf("Marshal PreparedEntry fixture: %v", err)
	}
	if !jsonKeyPresent(t, raw, "hop_chain") {
		t.Fatalf("seal fixture must contain hop_chain: %s", raw)
	}

	var externalStyle PreparedEntry
	if err := json.Unmarshal(raw, &externalStyle); err != nil {
		t.Fatalf("external-style Unmarshal PreparedEntry: %v", err)
	}

	if got, want := externalStyle.AsLedgerEntry(), (LedgerEntry{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("external-style json.Unmarshal bypassed PreparedEntry seal:\n got: %#v\nwant: %#v", got, want)
	}
}

func jsonKeyPresent(t testing.TB, raw []byte, key string) bool {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal object keys: %v", err)
	}
	_, ok := fields[key]
	return ok
}
