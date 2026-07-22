package auditledger

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestPreparedEntryJSONRoundTripPreservesAppendIntent(t *testing.T) {
	// 消除的风险：DLQ 必须持久化足够多的 append 意图，以便重建一条原始
	// LedgerEntry，并在 Append 之前重新运行 PrepareEntry。丢失任何一个 JSON
	// 影子字段都意味着重放会签出一条不同的 ledger entry。
	// 变异自检：从影子映射中删掉 request_id、tenant_id、created_at、
	// tenant_scope_ref、hop_chain 或 model_chain，本测试就会失败，因为重新
	// prepare 出的 entry 不再与 fixture 匹配。
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
	// 消除的风险：auditledger 之外的调用方不能通过把任意 JSON 喂给
	// json.Unmarshal 来构造一个已封箱的 PreparedEntry。若存在公开的
	// UnmarshalJSON 方法，本测试就会失败，因为投影被填充了而不是保持
	// 零值。
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
	//lint:ignore SA9005 此处刻意证明没有导出字段的封装对象不能被 JSON 反序列化绕过。
	if err := json.Unmarshal(raw, &externalStyle); err != nil {
		t.Fatalf("external-style Unmarshal PreparedEntry: %v", err)
	}

	if got, want := externalStyle.AsLedgerEntry(), (LedgerEntry{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("external-style json.Unmarshal bypassed PreparedEntry seal:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestDecodeLedgerEntryFromDLQPayloadAllowsMissingOptionalAppendFields(t *testing.T) {
	// 消除的风险：来自现有调用方的合法 JSON DLQ payload 可能会省略
	// created_at 或 hop_chain。Decode 不能在 worker 重新运行 PrepareEntry
	// 以及 Append 的常规 timestamp 处理之前就卡住这些行。
	// 变异自检：恢复对 request_id、created_at 或 hop_chain 的旧的必填字段
	// 守卫，对应的子测试就会以 decode 错误失败，而不是返回空的
	// LedgerEntry 字段。
	tests := []struct {
		name string
		raw  string
		want LedgerEntry
	}{
		{
			name: "missing_request_id",
			raw:  `{"tenant_id":77,"created_at":"2026-05-22T13:40:00Z","tenant_scope_ref":"tenant:fixture","hop_chain":[{"hop":"provider","hop_kind":"provider","hop_index":1,"decision_ref":"decision-required-fields","ts":"2026-05-22T13:40:00Z","provider":"openai","detail":{"status":200}}]}`,
			want: LedgerEntry{
				TenantID:       77,
				Timestamp:      "2026-05-22T13:40:00Z",
				TenantScopeRef: "tenant:fixture",
				HopChain: []proto.HopAttestation{{
					Hop:         proto.HopProvider,
					HopKind:     "provider",
					HopIndex:    1,
					DecisionRef: "decision-required-fields",
					Timestamp:   "2026-05-22T13:40:00Z",
					Provider:    "openai",
					Detail:      json.RawMessage(`{"status":200}`),
				}},
			},
		},
		{
			name: "missing_created_at",
			raw:  `{"request_id":"req_decode_missing_created_at","tenant_id":77,"tenant_scope_ref":"tenant:fixture","hop_chain":[{"hop":"provider","hop_kind":"provider","hop_index":1,"decision_ref":"decision-required-fields","ts":"2026-05-22T13:40:00Z","provider":"openai","detail":{"status":200}}]}`,
			want: LedgerEntry{
				RequestID:      "req_decode_missing_created_at",
				TenantID:       77,
				TenantScopeRef: "tenant:fixture",
				HopChain: []proto.HopAttestation{{
					Hop:         proto.HopProvider,
					HopKind:     "provider",
					HopIndex:    1,
					DecisionRef: "decision-required-fields",
					Timestamp:   "2026-05-22T13:40:00Z",
					Provider:    "openai",
					Detail:      json.RawMessage(`{"status":200}`),
				}},
			},
		},
		{
			name: "missing_hop_chain",
			raw:  `{"request_id":"req_decode_missing_hop_chain","tenant_id":77,"created_at":"2026-05-22T13:40:00Z","tenant_scope_ref":"tenant:fixture","model_chain":{"requested":"gpt-4o","route_decided":"gpt-4o","upstream_reported":"gpt-4o","verdict":"match"}}`,
			want: LedgerEntry{
				RequestID:      "req_decode_missing_hop_chain",
				TenantID:       77,
				Timestamp:      "2026-05-22T13:40:00Z",
				TenantScopeRef: "tenant:fixture",
				ModelChain: &proto.ModelChain{
					Requested:        "gpt-4o",
					RouteDecided:     "gpt-4o",
					UpstreamReported: "gpt-4o",
					Verdict:          "match",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, err := decodeLedgerEntryFromDLQPayload([]byte(tt.raw))
			if err != nil {
				t.Fatalf("decode valid DLQ payload with omitted fields: %v", err)
			}
			if !reflect.DeepEqual(decoded, tt.want) {
				t.Fatalf("decoded LedgerEntry mismatch:\n got: %#v\nwant: %#v", decoded, tt.want)
			}
		})
	}
}

func TestDecodeLedgerEntryFromDLQPayloadAllowsMissingModelChain(t *testing.T) {
	// 消除的风险：流式 ledger 发射方可能产出只含 HopChain 的意图；必填
	// 字段校验不能意外地把 model_chain 变成强制项，从而卡住合法的流式
	// DLQ 行。
	// 变异自检：在 decode 时要求 model_chain，本测试就会以 decode 错误
	// 失败。
	raw, err := json.Marshal(preparedEntryJSON{
		RequestID:      "req_decode_streaming_without_model_chain",
		TenantID:       77,
		CreatedAt:      "2026-05-22T13:45:00Z",
		TenantScopeRef: TenantScopeRef(77),
		HopChain: []proto.HopAttestation{{
			Hop:         proto.HopProvider,
			HopKind:     "provider",
			HopIndex:    1,
			DecisionRef: "decision-streaming",
			Timestamp:   "2026-05-22T13:45:00Z",
			Provider:    "openai",
			Detail:      json.RawMessage(`{"stream":true}`),
		}},
	})
	if err != nil {
		t.Fatalf("Marshal streaming payload: %v", err)
	}

	decoded, err := decodeLedgerEntryFromDLQPayload(raw)
	if err != nil {
		t.Fatalf("decode streaming payload without model_chain: %v", err)
	}
	if decoded.ModelChain != nil {
		t.Fatalf("model_chain should remain nil for streaming payload, got %+v", decoded.ModelChain)
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
