package proto

import (
	"encoding/json"
	"testing"
)

func TestIsValidHop_ClosedSet(t *testing.T) {
	valid := []HopHop{HopIngress, HopRouter, HopPool, HopAccount, HopProvider, HopResponse}
	for _, h := range valid {
		if !IsValidHop(h) {
			t.Errorf("expected %q valid", h)
		}
	}
	if IsValidHop("") {
		t.Error("empty hop must be invalid")
	}
	if IsValidHop("unknown_future_hop") {
		t.Error("future hop must be invalid until allowlisted")
	}
}

func TestModelChain_IsConsistent(t *testing.T) {
	cases := []struct {
		name string
		m    *ModelChain
		want bool
	}{
		{"nil", nil, true}, // 未启用 chain，不算 inconsistent
		{"happy 3way same", &ModelChain{Requested: "claude-3-opus", RouteDecided: "claude-3-opus", UpstreamReported: "claude-3-opus"}, true},
		{"streaming inflight", &ModelChain{Requested: "claude-3-opus", RouteDecided: "claude-3-opus"}, true},
		{"requested empty", &ModelChain{RouteDecided: "x"}, false},
		{"route empty", &ModelChain{Requested: "x"}, false},
		{"req != route (router 偷换)", &ModelChain{Requested: "claude-3-opus", RouteDecided: "claude-3-haiku"}, false},
		{"upstream != req (vendor 报错或 substitute)", &ModelChain{Requested: "claude-3-opus", RouteDecided: "claude-3-opus", UpstreamReported: "claude-3-haiku"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.m.IsConsistent(); got != tc.want {
				t.Errorf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestHopAttestation_RoundTripJSON(t *testing.T) {
	original := HopAttestation{
		Hop:           HopAccount,
		Timestamp:     "2026-05-13T09:30:00Z",
		AccountIDHash: "sha1prefix12345678",
		PoolID:        "pool_anthropic_oauth",
		RouteID:       "rt_anthropic",
	}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got HopAttestation
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Hop != original.Hop || got.AccountIDHash != original.AccountIDHash {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestAccounting_TrustChainFieldsOmitemptyByDefault(t *testing.T) {
	// 验证：现有零值 Accounting 序列化不会泄露 trust-chain 字段（向后兼容 P-1 fixture）。
	a := Accounting{}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	keys := []string{"hop_chain", "model_chain", "ledger_id", "signature", "pubkey_fp"}
	for _, k := range keys {
		if containsKey(b, k) {
			t.Errorf("零值 Accounting must omit %q (omitempty), got body=%s", k, b)
		}
	}
}

func TestAccounting_TrustChainFieldsRoundTrip(t *testing.T) {
	a := Accounting{
		HopChain: []HopAttestation{
			{Hop: HopIngress, Timestamp: "2026-05-13T09:30:00Z", RequestID: "req_x"},
		},
		ModelChain: &ModelChain{
			Requested:    "claude-3-opus",
			RouteDecided: "claude-3-opus",
		},
		LedgerID:          "lid_001",
		Signature:         "base64_ed25519_sig",
		PubkeyFingerprint: "sha256pfx16chars",
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Accounting
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.HopChain) != 1 || got.HopChain[0].Hop != HopIngress {
		t.Errorf("HopChain roundtrip wrong: %+v", got.HopChain)
	}
	if got.ModelChain == nil || got.ModelChain.Requested != "claude-3-opus" {
		t.Errorf("ModelChain roundtrip wrong: %+v", got.ModelChain)
	}
	if got.Signature != "base64_ed25519_sig" || got.PubkeyFingerprint != "sha256pfx16chars" || got.LedgerID != "lid_001" {
		t.Errorf("trust-chain scalars roundtrip wrong: sig=%q fp=%q lid=%q", got.Signature, got.PubkeyFingerprint, got.LedgerID)
	}
}

// containsKey 检查 JSON byte 含某个顶层 key（粗匹配，足够单测用）。
func containsKey(b []byte, key string) bool {
	target := []byte("\"" + key + "\":")
	for i := 0; i+len(target) <= len(b); i++ {
		match := true
		for j := range target {
			if b[i+j] != target[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
