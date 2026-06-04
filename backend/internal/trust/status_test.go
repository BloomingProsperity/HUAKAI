package trust

import (
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// TestStatusVocabularyAcceptsOnlyTrustAWireValues
//
// 守 TRUST-A-1：wire status 只能是 5 个。Mutation 自检：
// 新增/拼错 wire 值或把 unknown 当合法状态，本测试会 red。
func TestStatusVocabularyAcceptsOnlyTrustAWireValues(t *testing.T) {
	for _, status := range []Status{StatusVerified, StatusSignedOnly, StatusUnverified, StatusMissing, StatusMismatch} {
		if !IsValidStatus(string(status)) {
			t.Fatalf("status %q should be valid", status)
		}
	}
	for _, raw := range []string{"", "verified-but-expired", "signed_only", "unknown"} {
		if IsValidStatus(raw) {
			t.Fatalf("status %q must be rejected", raw)
		}
	}
}

// TestResponseHeadersUseDispatchMetadataAndDefaultUnverified
//
// 守 TRUST-A-2：header provider/model/request_id 来自响应 dispatch metadata，
// 状态默认 unverified。Mutation 自检：删任何一个 header Set 调用都会 red。
func TestResponseHeadersUseDispatchMetadataAndDefaultUnverified(t *testing.T) {
	h := http.Header{}
	meta := ResponseMetadata{Provider: "anthropic", Model: "claude-opus-4", RequestID: "req-trust-a"}

	WriteResponseHeaders(h, meta, auditledger.DisabledLedgerResult())

	if got := h.Get(HeaderUpstreamProvider); got != "anthropic" {
		t.Fatalf("%s=%q want anthropic", HeaderUpstreamProvider, got)
	}
	if got := h.Get(HeaderUpstreamModel); got != "claude-opus-4" {
		t.Fatalf("%s=%q want claude-opus-4", HeaderUpstreamModel, got)
	}
	if got := h.Get(HeaderRequestID); got != "req-trust-a" {
		t.Fatalf("%s=%q want req-trust-a", HeaderRequestID, got)
	}
	if got := h.Get(HeaderStatus); got != string(StatusUnverified) {
		t.Fatalf("%s=%q want unverified", HeaderStatus, got)
	}
}

// TestResponseStatusMismatchWhenLedgerMetadataDiffers
//
// 守 TRUST-A-2 mismatch：ledger snapshot 与 response header metadata 任一
// 不同都必须标 mismatch。Mutation 自检：只校非空、不校相等会 red。
func TestResponseStatusMismatchWhenLedgerMetadataDiffers(t *testing.T) {
	meta := ResponseMetadata{Provider: "anthropic", Model: "claude-opus-4", RequestID: "req-1"}
	result := auditledger.AuditLedgerResult{
		State:            auditledger.LedgerResultStatePersisted,
		LedgerID:         "ldg-1",
		Fingerprint:      "fp-1",
		UpstreamProvider: "openai",
		UpstreamModel:    "claude-opus-4",
		RequestID:        "req-1",
	}

	if got := ResponseStatus(meta, result); got != StatusMismatch {
		t.Fatalf("ResponseStatus=%q want mismatch", got)
	}
}

// TestMetadataFromLedgerEntryUsesProviderHopAndRouteModel
//
// 守 provider/model 派生规则：provider 取 provider hop，model 取路由决策模型。
// Mutation 自检：改成 requested/upstream_reported 或取错 hop 会 red。
func TestMetadataFromLedgerEntryUsesProviderHopAndRouteModel(t *testing.T) {
	entry := auditledger.LedgerEntry{
		RequestID: "req-2",
		HopChain: []proto.HopAttestation{
			{Hop: proto.HopIngress},
			{Hop: proto.HopProvider, Provider: "anthropic"},
		},
		ModelChain: &proto.ModelChain{
			Requested:        "claude-opus-4",
			RouteDecided:     "claude-opus-4-20260514",
			UpstreamReported: "claude-opus-4",
		},
	}

	got := MetadataFromLedgerEntry(entry)
	if got.Provider != "anthropic" || got.Model != "claude-opus-4-20260514" || got.RequestID != "req-2" {
		t.Fatalf("MetadataFromLedgerEntry=%+v want provider/model/request_id from ledger", got)
	}
}

// TestUpgradeStatusOnSignatureOnlyPromotesUnverified
//
// 守 TRUST-B-2 provisional inline 语义：签名只把已持久化且默认 unverified 的
// 响应升到 signed-only；mismatch/missing 等风险状态不能被签名覆盖。
// Mutation 自检：无条件返回 signed-only 会让 mismatch case red；忽略签名存在性
// 会让 no-signature case red。
func TestUpgradeStatusOnSignatureOnlyPromotesUnverified(t *testing.T) {
	tests := []struct {
		name       string
		prev       Status
		sigPresent bool
		want       Status
	}{
		{name: "unverified with signature", prev: StatusUnverified, sigPresent: true, want: StatusSignedOnly},
		{name: "unverified without signature", prev: StatusUnverified, sigPresent: false, want: StatusUnverified},
		{name: "mismatch stays mismatch", prev: StatusMismatch, sigPresent: true, want: StatusMismatch},
		{name: "missing stays missing", prev: StatusMissing, sigPresent: true, want: StatusMissing},
		{name: "verified stays verified", prev: StatusVerified, sigPresent: true, want: StatusVerified},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UpgradeStatusOnSignature(tt.prev, tt.sigPresent); got != tt.want {
				t.Fatalf("UpgradeStatusOnSignature(%q,%v)=%q want %q", tt.prev, tt.sigPresent, got, tt.want)
			}
		})
	}
}
