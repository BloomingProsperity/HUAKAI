package gatewayhttp

import (
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

func TestChatCompletionsClientAdapter_NonStreamingModelChainAndHeaders(t *testing.T) {
	enableHCSFDispatchForTest(t)
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "gpt-4o",
		CanonicalModelID: "openai/gpt-4o",
		ProviderModelID:  "gpt-4o-2024-08-06",
		ProtocolFamily:   "openai_chat",
		PoolCandidates:   []int64{42},
	}}
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(headerHUAKAIModelRequested); got != "gpt-4o" {
		t.Fatalf("%s=%q want gpt-4o", headerHUAKAIModelRequested, got)
	}
	if got := rec.Header().Get(headerHUAKAIModelDelivered); got != "gpt-4o-2024-08-06" {
		t.Fatalf("%s=%q want gpt-4o-2024-08-06", headerHUAKAIModelDelivered, got)
	}
	if dispatcher.observed == nil {
		t.Fatal("dispatcher did not observe request envelope")
	}
	accounting := dispatcher.observed.Accounting
	if accounting.ModelChain == nil {
		t.Fatal("request envelope ModelChain is nil")
	}
	if accounting.ModelChain.Requested != "gpt-4o" {
		t.Fatalf("ModelChain.Requested=%q want gpt-4o", accounting.ModelChain.Requested)
	}
	if accounting.ModelChain.RouteDecided != "gpt-4o-2024-08-06" {
		t.Fatalf("ModelChain.RouteDecided=%q want provider model", accounting.ModelChain.RouteDecided)
	}
	if len(accounting.HopChain) != 4 {
		t.Fatalf("HopChain len=%d want 4", len(accounting.HopChain))
	}
	for i, hop := range accounting.HopChain {
		if len(hop.Detail) != 0 {
			t.Fatalf("hop[%d] detail must stay empty, got %s", i, hop.Detail)
		}
	}
}

func TestSetHUAKAIModelHeadersOmitsEmptyDelivered(t *testing.T) {
	h := http.Header{}
	setHUAKAIModelHeaders(h, "requested-model", proto.NewEmptyEnvelope())

	if got := h.Get(headerHUAKAIModelRequested); got != "requested-model" {
		t.Fatalf("%s=%q want requested-model", headerHUAKAIModelRequested, got)
	}
	if got := h.Get(headerHUAKAIModelDelivered); got != "" {
		t.Fatalf("%s=%q want empty", headerHUAKAIModelDelivered, got)
	}
}

func TestWriteStreamBillingHeaders(t *testing.T) {
	h := http.Header{}
	declareStreamBillingTrailers(h)
	writeStreamBillingHeaders(h, billing.Attempt{
		State:               billing.StreamStatePartial,
		DeliveredTokenCount: 7,
	})

	if got := h.Get(headerHUAKAIStreamState); got != "partial" {
		t.Fatalf("%s=%q want partial", headerHUAKAIStreamState, got)
	}
	if got := h.Get(headerHUAKAIDeliveredTokens); got != "7" {
		t.Fatalf("%s=%q want 7", headerHUAKAIDeliveredTokens, got)
	}
	gotTrailers := h.Values("Trailer")
	wantTrailers := map[string]bool{
		headerHUAKAIStreamState:         false,
		headerHUAKAIDeliveredTokens:     false,
		headerHUAKAIAuditLedgerID:       false,
		"X-HUAKAI-Ledger-DLQ-Ref":       false,
		headerHUAKAIAuditVerify:         false,
		headerHUAKAIAuditSigFingerprint: false,
	}
	for _, got := range gotTrailers {
		if _, ok := wantTrailers[got]; ok {
			wantTrailers[got] = true
		}
	}
	for trailer, seen := range wantTrailers {
		if !seen {
			t.Fatalf("Trailer values=%v missing %s", gotTrailers, trailer)
		}
	}
	if len(gotTrailers) != len(wantTrailers) {
		t.Fatalf("Trailer values=%v want exactly %d stream billing/ledger trailers", gotTrailers, len(wantTrailers))
	}
}

// TestWriteHuakaiHeaders_NonStreamingDeferredWritesDLQRef
//
// W5 Owner Bug #1 / synthesis §6 C3 闭合用例:非流式 LedgerResultStateDeferred +
// 非空 DLQRef → response header 必须含 X-HUAKAI-Ledger-DLQ-Ref。流式 trailer 早
// 已守这个语义(chat_completions_stream.go:608),非流式响应 header 之前直接 return,
// 客户端拿不到 ref 复理 — 本用例守此 gap。
//
// 判别 fixture A (Deferred + DLQRef):应写 X-HUAKAI-Ledger-DLQ-Ref,**不**写
// LedgerID / verify URL / signature。
// 判别 fixture B (Deferred + 空 DLQRef):什么也不写 (防 DLQRef 字段被误写空串)。
//
// Mutation 自检:保留旧 `if result.State != Persisted return` 行 → A 路径
// X-HUAKAI-Ledger-DLQ-Ref 是空 → 本用例 red。
func TestWriteHuakaiHeaders_NonStreamingDeferredWritesDLQRef(t *testing.T) {
	// fixture A:Deferred + DLQRef="audit_ledger_dlq:42"
	hA := http.Header{}
	resultA := auditledger.AuditLedgerResult{
		State:  auditledger.LedgerResultStateDeferred,
		DLQRef: "audit_ledger_dlq:42",
	}
	WriteHuakaiHeaders(hA, "claude-3-5-sonnet", proto.NewEmptyEnvelope(), resultA, "req-abc", 7001)
	if got := hA.Get(headerHUAKAIAuditLedgerDLQRef); got != "audit_ledger_dlq:42" {
		t.Fatalf("Deferred+DLQRef:%s=%q want 'audit_ledger_dlq:42'", headerHUAKAIAuditLedgerDLQRef, got)
	}
	if got := hA.Get(headerHUAKAIAuditLedgerID); got != "" {
		t.Fatalf("Deferred path MUST NOT write ledger id; got %s=%q", headerHUAKAIAuditLedgerID, got)
	}
	if got := hA.Get(headerHUAKAIAuditVerify); got != "" {
		t.Fatalf("Deferred path MUST NOT write verify URL; got %s=%q", headerHUAKAIAuditVerify, got)
	}
	if got := hA.Get(headerHUAKAIAuditSigFingerprint); got != "" {
		t.Fatalf("Deferred path MUST NOT write signature fingerprint; got %s=%q", headerHUAKAIAuditSigFingerprint, got)
	}

	// fixture B:Deferred + 空 DLQRef → 什么也不写 (paired-fixture 防误写空)
	hB := http.Header{}
	resultB := auditledger.AuditLedgerResult{
		State:  auditledger.LedgerResultStateDeferred,
		DLQRef: "",
	}
	WriteHuakaiHeaders(hB, "claude-3-5-sonnet", proto.NewEmptyEnvelope(), resultB, "req-abc", 7001)
	if got := hB.Get(headerHUAKAIAuditLedgerDLQRef); got != "" {
		t.Fatalf("Deferred+empty-DLQRef MUST NOT write DLQ header; got %s=%q", headerHUAKAIAuditLedgerDLQRef, got)
	}
}

// TestWriteHuakaiHeaders_NonStreamingPersistedHeadersUnchanged
//
// 正向保持守:Persisted 路径(包括 LedgerID/Fingerprint/verify URL)行为不动,
// **不**写 DLQ ref header。配对 T_NS1 防 Persisted/Deferred 分支错乱。
//
// Mutation 自检:把 Persisted case 也加 DLQRef 写 → 本用例 red(DLQ header 不应出现)。
func TestWriteHuakaiHeaders_NonStreamingPersistedHeadersUnchanged(t *testing.T) {
	h := http.Header{}
	result := auditledger.AuditLedgerResult{
		State:       auditledger.LedgerResultStatePersisted,
		LedgerID:    "ledger-1",
		Fingerprint: "fp-xyz",
		DLQRef:      "should-not-appear", // 故意带,确认 Persisted 不漏写
	}
	WriteHuakaiHeaders(h, "claude-3-5-sonnet", proto.NewEmptyEnvelope(), result, "req-abc", 7001)
	if got := h.Get(headerHUAKAIAuditLedgerID); got != "ledger-1" {
		t.Fatalf("Persisted path must write ledger id; got %s=%q", headerHUAKAIAuditLedgerID, got)
	}
	if got := h.Get(headerHUAKAIAuditSigFingerprint); got != "fp-xyz" {
		t.Fatalf("Persisted path must write fingerprint; got %s=%q", headerHUAKAIAuditSigFingerprint, got)
	}
	if got := h.Get(headerHUAKAIAuditVerify); got == "" {
		t.Fatalf("Persisted path must write verify URL; got empty")
	}
	if got := h.Get(headerHUAKAIAuditLedgerDLQRef); got != "" {
		t.Fatalf("Persisted path MUST NOT write DLQ ref (it's Deferred-only); got %s=%q",
			headerHUAKAIAuditLedgerDLQRef, got)
	}
}
