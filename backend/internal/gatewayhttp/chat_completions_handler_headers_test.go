package gatewayhttp

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/trust"
	"github.com/BloomingProsperity/HUAKAI/internal/trustreceipt"
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

// TestChatCompletionResponseHeaderIncludesUpstreamProvider
//
// 守 TRUST-A-2 wire contract：成功响应必须把实际 dispatch path 的 provider /
// model / request_id 暴露为 X-Huakai-* header。Mutation 自检：删掉 trust
// header 注入时，本测试的 provider/model/request_id 三个断言会一起 red。
func TestChatCompletionResponseHeaderIncludesUpstreamProvider(t *testing.T) {
	enableHCSFDispatchForTest(t)
	d := clientAdapterDeps(t)
	d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "claude-opus-4",
		CanonicalModelID: "anthropic/claude-opus-4",
		ProviderModelID:  "claude-opus-4",
		ProtocolFamily:   "anthropic_messages",
		PoolCandidates:   []int64{42},
	}}
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"claude-opus-4","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(trust.HeaderUpstreamProvider); got != "openai" {
		t.Fatalf("%s=%q want openai from selected provider account", trust.HeaderUpstreamProvider, got)
	}
	if got := rec.Header().Get(trust.HeaderUpstreamModel); got != "claude-opus-4" {
		t.Fatalf("%s=%q want claude-opus-4", trust.HeaderUpstreamModel, got)
	}
	if got := rec.Header().Get(trust.HeaderRequestID); got == "" {
		t.Fatalf("%s is empty; request_id must be available for detached verify panel", trust.HeaderRequestID)
	}
}

// TestChatCompletionResponseHeaderTrustStatusIsUnverifiedDefault
//
// 守 TRUST-A-1/A-2 默认状态：TRUST-B signer payload 尚未接通前，普通成功响应
// 只能标 `unverified`，不能因为旧 audit ledger 头存在就假称 verified。
// Mutation 自检：把默认状态改成 verified/signed-only，本测试会 red。
func TestChatCompletionResponseHeaderTrustStatusIsUnverifiedDefault(t *testing.T) {
	enableHCSFDispatchForTest(t)
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(trust.HeaderStatus); got != string(trust.StatusUnverified) {
		t.Fatalf("%s=%q want %q before TRUST-B signer wiring", trust.HeaderStatus, got, trust.StatusUnverified)
	}
}

// TestChatCompletionResponseHeaderTrustSignedOnlyWhenSignerAvailable
//
// 守 TRUST-B-2 inline provisional signature：非流式 Persisted ledger result +
// signer 可用时，response header 必须带 receipt 签名并把 TRUST-A 默认
// unverified 升到 signed-only。Mutation 自检：删掉 SignReceipt 调用或不覆盖
// status，本测试会看到空签名或 unverified。
func TestChatCompletionResponseHeaderTrustSignedOnlyWhenSignerAvailable(t *testing.T) {
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	h := http.Header{}
	result := signedHeaderPersistedResult("req-signed")
	env := signedHeaderEnvelope("req-signed")

	WriteHuakaiHeaders(h, "gpt-4o", env, result, "req-signed", 7001, signer)

	if got := h.Get(trust.HeaderStatus); got != string(trust.StatusSignedOnly) {
		t.Fatalf("%s=%q want signed-only", trust.HeaderStatus, got)
	}
	sigB64 := h.Get(trust.HeaderTrustSignature)
	if sigB64 == "" {
		t.Fatalf("%s is empty", trust.HeaderTrustSignature)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		t.Fatalf("%s is not base64: %v", trust.HeaderTrustSignature, err)
	}
	expected := trustreceipt.BuildProvisionalFromEnv(env, result, "req-signed", 0)
	canonical, err := trustreceipt.Canonical(expected)
	if err != nil {
		t.Fatalf("canonical receipt: %v", err)
	}
	if !ed25519.Verify(signer.PublicKey(), canonical, sigBytes) {
		t.Fatalf("%s does not verify against expected canonical receipt", trust.HeaderTrustSignature)
	}
	if got := h.Get(trust.HeaderTrustPubkeyFingerprint); got != signer.Fingerprint() {
		t.Fatalf("%s=%q want %q", trust.HeaderTrustPubkeyFingerprint, got, signer.Fingerprint())
	}
	if got := h.Get(trust.HeaderTrustSchema); got != "trust.receipt.v1" {
		t.Fatalf("%s=%q want trust.receipt.v1", trust.HeaderTrustSchema, got)
	}
}

// TestChatCompletionResponseHeaderTrustStaysUnverifiedWhenSignerNil
//
// 守 D-8 本切片语义：signer 不可用时不伪造签名、不升级状态，保留 TRUST-A
// unverified。Mutation 自检：nil signer 仍尝试签名会 panic；无条件 signed-only
// 会让 status 断言 red。
func TestChatCompletionResponseHeaderTrustStaysUnverifiedWhenSignerNil(t *testing.T) {
	h := http.Header{}
	result := signedHeaderPersistedResult("req-unsigned")

	WriteHuakaiHeaders(h, "gpt-4o", signedHeaderEnvelope("req-unsigned"), result, "req-unsigned", 7001, nil)

	if got := h.Get(trust.HeaderStatus); got != string(trust.StatusUnverified) {
		t.Fatalf("%s=%q want unverified", trust.HeaderStatus, got)
	}
	if got := h.Get(trust.HeaderTrustSignature); got != "" {
		t.Fatalf("%s=%q want empty without signer", trust.HeaderTrustSignature, got)
	}
	if got := h.Get(trust.HeaderTrustPubkeyFingerprint); got != "" {
		t.Fatalf("%s=%q want empty without signer", trust.HeaderTrustPubkeyFingerprint, got)
	}
	if got := h.Get(trust.HeaderTrustSchema); got != "" {
		t.Fatalf("%s=%q want empty without signer", trust.HeaderTrustSchema, got)
	}
}

// TestChatCompletionResponseFailOpenWhenSignerNilInProduction
//
// 守 D-8=A signer 不可用时仍走 W4 deferred ledger 路径：返 200 OK +
// status=unverified + DLQRef 非空。Mutation 自检：删 DLQ enqueue 会被
// production audit-ref policy 拒成 500；删 audit_signer_deferred warning 会
// 让 warning 断言 red。
func TestChatCompletionResponseFailOpenWhenSignerNilInProduction(t *testing.T) {
	enableHCSFDispatchForTest(t)
	ledgerSigner, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("ledger signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(ledgerSigner)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	d := clientAdapterDeps(t)
	d.Signer = nil
	d.AuditLedger = ledger
	d.AuditLedgerDLQ = &recordingGatewayAuditLedgerDLQ{id: 918}
	d.AuditRefPolicy = productionAuditRefPolicyForGatewayTest(false)
	dispatcher := &signerNilFailOpenDispatcher{}
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (D-8 fail-open) body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(trust.HeaderStatus); got != string(trust.StatusUnverified) {
		t.Fatalf("%s=%q want unverified (D-8 fail-open)", trust.HeaderStatus, got)
	}
	if got := rec.Header().Get(trust.HeaderTrustSignature); got != "" {
		t.Fatalf("%s=%q want empty (no sig when signer nil)", trust.HeaderTrustSignature, got)
	}
	if got := rec.Header().Get(headerHUAKAIAuditLedgerDLQRef); got != "audit_ledger_dlq:918" {
		t.Fatalf("%s=%q want audit_ledger_dlq:918", headerHUAKAIAuditLedgerDLQRef, got)
	}
	dlqSink := d.AuditLedgerDLQ.(*recordingGatewayAuditLedgerDLQ)
	if len(dlqSink.events) != 1 {
		t.Fatalf("audit ledger DLQ events=%d want 1", len(dlqSink.events))
	}
	if dispatcher.returned == nil {
		t.Fatal("dispatcher returned envelope was not captured")
	}
	if !hasProtocolLossCode(dispatcher.returned.CapabilityGraph.ProtocolLoss, "audit_signer_deferred") {
		t.Fatalf("ProtocolLoss codes=%v want audit_signer_deferred", protocolLossCodes(dispatcher.returned.CapabilityGraph.ProtocolLoss))
	}
}

// TestNonStreamingSettle_CapturesLedgerProtocolLoss 守 S1-025-fu item 2(b):
// submitAuditLedgerEntry 会向 env.CapabilityGraph.ProtocolLoss 追加 ledger/trust 警告
// (signer nil fail-open → audit_signer_deferred);快照必须取在 ledger 之后,settle 才带得到。
func TestNonStreamingSettle_CapturesLedgerProtocolLoss(t *testing.T) {
	enableHCSFDispatchForTest(t)
	ledgerSigner, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("ledger signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(ledgerSigner)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.Signer = nil
	d.AuditLedger = ledger
	d.AuditLedgerDLQ = &recordingGatewayAuditLedgerDLQ{id: 919}
	d.AuditRefPolicy = productionAuditRefPolicyForGatewayTest(false)
	d.Settler = settler
	d.CanonicalDispatcher = &signerNilFailOpenDispatcher{}
	d.RateTables = &rateTableSourceStub{table: billing.RateTable{
		Version:     "test-policy",
		PricingData: json.RawMessage(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (D-8 fail-open) body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	// MUTATION: 把 ex.protocolLoss 快照移回 submitAuditLedgerEntry 之前 → settle 缺 audit_signer_deferred → RED。
	if !settledLossHasCode(t, settler.calls[0].ProtocolLoss, "audit_signer_deferred") {
		t.Fatalf("settle ProtocolLoss=%s want code audit_signer_deferred", settler.calls[0].ProtocolLoss)
	}
}

type signerNilFailOpenDispatcher struct {
	returned *proto.HCSF
}

func (d *signerNilFailOpenDispatcher) DispatchHCSF(_ context.Context, requestEnvelope *proto.HCSF) (*proto.HCSF, error) {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta = requestEnvelope.RequestMeta
	env.BufferedResponse = &proto.CanonicalResponse{
		ID:         "chatcmpl-signer-nil-fail-open",
		Model:      requestEnvelope.RequestMeta.UpstreamModel,
		Content:    []proto.CanonicalContentBlock{{Type: "text", Text: "hello from canonical"}},
		Usage:      proto.CanonicalUsage{InputTokens: 2, OutputTokens: 3},
		StopReason: proto.CanonicalStopEndTurn,
	}
	env.Accounting.Usage = env.BufferedResponse.Usage
	env.Accounting.EvidenceLabel = proto.EvidenceMock
	d.returned = env
	return env, nil
}

func hasProtocolLossCode(losses []proto.ProtocolLossEntry, code string) bool {
	for _, loss := range losses {
		if loss.Code == code {
			return true
		}
	}
	return false
}

func protocolLossCodes(losses []proto.ProtocolLossEntry) []string {
	codes := make([]string, 0, len(losses))
	for _, loss := range losses {
		codes = append(codes, loss.Code)
	}
	return codes
}

func signedHeaderEnvelope(requestID string) *proto.HCSF {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta.RequestID = requestID
	env.RequestMeta.TenantID = 7001
	env.RequestMeta.Provider = "openai"
	env.RequestMeta.Model = "gpt-4o"
	env.RequestMeta.UpstreamModel = "gpt-4o"
	env.Accounting.ModelChain = &proto.ModelChain{
		Requested:        "gpt-4o",
		RouteDecided:     "gpt-4o",
		UpstreamReported: "gpt-4o",
	}
	env.BufferedResponse = &proto.CanonicalResponse{Model: "gpt-4o"}
	env.Accounting.Usage = proto.CanonicalUsage{InputTokens: 13, OutputTokens: 21}
	env.Accounting.HopChain = []proto.HopAttestation{{
		Hop:       proto.HopProvider,
		Provider:  "openai",
		StartedAt: "2026-05-27T00:00:00Z",
		Timestamp: "2026-05-27T00:00:00Z",
	}}
	return env
}

func signedHeaderPersistedResult(requestID string) auditledger.AuditLedgerResult {
	return auditledger.AuditLedgerResult{
		State:            auditledger.LedgerResultStatePersisted,
		LedgerID:         "ledger-" + requestID,
		Fingerprint:      "ledger-fp",
		UpstreamProvider: "openai",
		UpstreamModel:    "gpt-4o",
		RequestID:        requestID,
	}
}

// TestChatCompletionResponseMismatchDetectedWhenAuditMismatchesHeader
//
// 守 TRUST-A-2 mismatch 分支：response header 仍展示 dispatch path，
// 但 audit ledger append 返回的 provider 与 header 不一致时，trust status 必须
// 强制降为 mismatch。Mutation 自检：删除 header-vs-ledger 比对时，本测试会看到
// unverified 而不是 mismatch。
func TestChatCompletionResponseMismatchDetectedWhenAuditMismatchesHeader(t *testing.T) {
	enableHCSFDispatchForTest(t)
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	inner, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.AuditLedger = &providerMismatchLedger{inner: inner, provider: "anthropic"}
	d.Signer = signer

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(trust.HeaderUpstreamProvider); got != "openai" {
		t.Fatalf("%s=%q want dispatch provider openai even when ledger mismatches", trust.HeaderUpstreamProvider, got)
	}
	if got := rec.Header().Get(trust.HeaderStatus); got != string(trust.StatusMismatch) {
		t.Fatalf("%s=%q want mismatch when ledger provider differs from response header", trust.HeaderStatus, got)
	}
}

type providerMismatchLedger struct {
	inner    auditledger.Ledger
	provider string
}

func (l *providerMismatchLedger) Append(ctx context.Context, prepared auditledger.PreparedEntry) (auditledger.LedgerEntry, error) {
	entry := prepared.AsLedgerEntry()
	for i := range entry.HopChain {
		if entry.HopChain[i].Hop == proto.HopProvider {
			entry.HopChain[i].Provider = l.provider
		}
	}
	rewritten, err := auditledger.PrepareEntry(ctx, entry)
	if err != nil {
		return auditledger.LedgerEntry{}, err
	}
	return l.inner.Append(ctx, rewritten)
}

func (l *providerMismatchLedger) GetByRequestID(ctx context.Context, requestID string) (auditledger.LedgerEntry, error) {
	return l.inner.GetByRequestID(ctx, requestID)
}

func (l *providerMismatchLedger) GetByRequestIDAndTenantScope(ctx context.Context, requestID, tenantScopeRef string) (auditledger.LedgerEntry, error) {
	return l.inner.GetByRequestIDAndTenantScope(ctx, requestID, tenantScopeRef)
}

func (l *providerMismatchLedger) LatestMerkleRoot(ctx context.Context) ([32]byte, error) {
	return l.inner.LatestMerkleRoot(ctx)
}

func (l *providerMismatchLedger) Size(ctx context.Context) int {
	return l.inner.Size(ctx)
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
	WriteHuakaiHeaders(hA, "claude-3-5-sonnet", proto.NewEmptyEnvelope(), resultA, "req-abc", 7001, nil)
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
	WriteHuakaiHeaders(hB, "claude-3-5-sonnet", proto.NewEmptyEnvelope(), resultB, "req-abc", 7001, nil)
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
	WriteHuakaiHeaders(h, "claude-3-5-sonnet", proto.NewEmptyEnvelope(), result, "req-abc", 7001, nil)
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
