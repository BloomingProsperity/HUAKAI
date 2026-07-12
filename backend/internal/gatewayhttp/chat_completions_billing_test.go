package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func TestChatCompletionsNonStreamingSettleFailureKeepsDeliveredResponseAndEnqueuesRecovery(t *testing.T) {
	// 该测试守住结算与交付的顺序：完整业务体必须先到客户端，之后的结算故障只能进入恢复。
	// 变异：把结算移回写体之前，会返回 500 且响应缺少 canonical 内容，本测试必红。
	enableHCSFDispatchForTest(t)
	settler := &failingSettleSettler{err: errors.New("settle unavailable")}
	recovery := &postDeliverySpyEnqueuer{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.SettleRecoveryDLQ = recovery

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !json.Valid(rec.Body.Bytes()) || !strings.Contains(rec.Body.String(), "hello from canonical") {
		t.Fatalf("响应体不是完整业务 JSON: %q", rec.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("aborts=%+v want none after full delivery", settler.aborts)
	}
	if recovery.calls != 1 || recovery.lastEvt.EventKind != dlq.EventKindPostDeliverySettlement {
		t.Fatalf("recovery calls/kind=%d/%q want 1/%q", recovery.calls, recovery.lastEvt.EventKind, dlq.EventKindPostDeliverySettlement)
	}
	decoded, err := settlementrecovery.Decode(recovery.lastEvt.Payload)
	if err != nil {
		t.Fatalf("decode recovery payload: %v", err)
	}
	if decoded.Source != settlementrecovery.SourceDirectSettle {
		t.Fatalf("recovery source=%q want %q", decoded.Source, settlementrecovery.SourceDirectSettle)
	}
}

func TestChatCompletionsNonStreamingPartialWriteAbortsWithoutSettlement(t *testing.T) {
	// 该测试守住未完整交付不得计费：写出部分业务字节后报错时，只能释放预留，不能结算或入恢复。
	// 变异：忽略 Write 的 n/err 会产生一次 Settle 且没有 Abort，本测试必红。
	enableHCSFDispatchForTest(t)
	settler := &recordingSettler{}
	recovery := &postDeliverySpyEnqueuer{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.SettleRecoveryDLQ = recovery
	w := &partialWriteResponseWriter{header: make(http.Header), limit: 9, err: io.ErrClosedPipe}
	h := NewChatCompletionsHandler(d)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")

	h.ServeHTTP(w, req)

	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 on partial write", len(settler.calls))
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != "client_response_write_error" {
		t.Fatalf("aborts=%+v want one client_response_write_error", settler.aborts)
	}
	if recovery.calls != 0 {
		t.Fatalf("recovery calls=%d want 0 for undelivered body", recovery.calls)
	}
	if w.writeHeaderCalls != 0 {
		t.Fatalf("WriteHeader calls=%d want 0 before fallible body write", w.writeHeaderCalls)
	}
	if w.body.Len() == 0 {
		t.Fatal("fixture did not exercise a partial write")
	}
	if w.flushes != 0 {
		t.Fatalf("flushes=%d want 0 after incomplete body", w.flushes)
	}
}

func TestChatCompletionsNonStreamingFullLengthWriteErrorIsConservativelyDelivered(t *testing.T) {
	// 写入器报告完整长度但同时返回错误时，客户端是否收到不可判定；必须按已交付结算，不能 Abort。
	enableHCSFDispatchForTest(t)
	w := &partialWriteResponseWriter{header: make(http.Header), limit: -1, err: io.ErrUnexpectedEOF}
	settler := &flushCheckingSettler{flushed: func() bool { return w.flushes > 0 }}
	recovery := &postDeliverySpyEnqueuer{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.SettleRecoveryDLQ = recovery
	h := NewChatCompletionsHandler(d)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")

	h.ServeHTTP(w, req)

	if len(settler.calls) != 1 || len(settler.aborts) != 0 {
		t.Fatalf("settles=%d aborts=%d want 1/0 for uncertain full write", len(settler.calls), len(settler.aborts))
	}
	if recovery.calls != 0 {
		t.Fatalf("recovery calls=%d want 0 after successful direct settlement", recovery.calls)
	}
	if !json.Valid(w.body.Bytes()) {
		t.Fatalf("fixture did not report a full JSON write: %q", w.body.String())
	}
	if w.flushes != 1 || settler.settleBeforeFlush {
		t.Fatalf("flushes/settleBeforeFlush=%d/%v want 1/false", w.flushes, settler.settleBeforeFlush)
	}
}

type flushCheckingSettler struct {
	recordingSettler
	flushed           func() bool
	settleBeforeFlush bool
}

func (s *flushCheckingSettler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	if s.flushed == nil || !s.flushed() {
		s.settleBeforeFlush = true
	}
	return s.recordingSettler.Settle(ctx, req)
}

type partialWriteResponseWriter struct {
	header           http.Header
	body             bytes.Buffer
	limit            int
	err              error
	writeHeaderCalls int
	flushes          int
}

func (w *partialWriteResponseWriter) Header() http.Header { return w.header }

func (w *partialWriteResponseWriter) WriteHeader(int) { w.writeHeaderCalls++ }

func (w *partialWriteResponseWriter) Write(p []byte) (int, error) {
	n := w.limit
	if n < 0 || n > len(p) {
		n = len(p)
	}
	_, _ = w.body.Write(p[:n])
	return n, w.err
}

func (w *partialWriteResponseWriter) Flush() { w.flushes++ }

func TestChatCompletions_AuditLedgerNilGracefulNoPanic(t *testing.T) {
	enableHCSFDispatchForTest(t)
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Signer = signer

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(headerHUAKAIAuditLedgerID); got != "" {
		t.Fatalf("%s=%q want empty when ledger is nil", headerHUAKAIAuditLedgerID, got)
	}
}

func TestNonStreamingSettleRequestCarriesSchedulerOutboxIntent(t *testing.T) {
	ex := &chatExecution{
		ident:             auth.Identity{TenantID: 7, APIKeyID: 8, UserID: 9},
		req:               chatRequest{Model: "gpt-4o"},
		reserveRes:        &billing.ReserveResult{ClaimID: 9001},
		acquiredAccountID: 7001,
		acquisitionToken:  uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		upstreamModelID:   "gpt-4o-2024-08-06",
		cacheVendor:       "openai",
		payloadHash:       "payload-fp",
		plan:              router.RoutePlan{SnapshotVersion: "registry:7:v1;router:r1"},
	}

	req := ex.nonStreamingSettleRequest(proto.NewEmptyEnvelope(), completionCostBreakdown{}, nil)
	if !req.EmitSchedulerOutbox {
		t.Fatal("provider-backed non-streaming settlement must carry scheduler outbox intent")
	}
}

func TestNonStreamingUsageDraftCarriesCostSnapshot(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	env.BufferedResponse = &proto.CanonicalResponse{
		Usage: proto.CanonicalUsage{InputTokens: 2, OutputTokens: 3},
	}
	actualCost := completionCostBreakdown{
		Total:        decimal.RequireFromString("0.0025"),
		CostSnapshot: "tiered:vtest-policy",
	}

	draft := nonStreamingUsageDraft(env, actualCost, nil)

	// 变异：去掉非流式 draft 的 CostSnapshot 赋值会让 cost 仍正确，但丢失对该行
	// 按哪个 model 计费的可审计性。
	if draft.CostSnapshot != "tiered:vtest-policy" {
		t.Fatalf("CostSnapshot=%q want tiered:vtest-policy", draft.CostSnapshot)
	}
}

func TestStreamingCompletionEventCarriesSchedulerOutboxIntent(t *testing.T) {
	ex := &chatExecution{
		ident:             auth.Identity{TenantID: 7, APIKeyID: 8, UserID: 9},
		req:               chatRequest{Model: "gpt-4o"},
		requestID:         "req-stream-outbox",
		reserveRes:        &billing.ReserveResult{ClaimID: 9002},
		acquiredAccountID: 7002,
		acquisitionToken:  uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
		upstreamModelID:   "gpt-4o-2024-08-06",
		cacheVendor:       "openai",
		payloadHash:       "payload-fp-stream",
		plan:              router.RoutePlan{SnapshotVersion: "registry:7:v1;router:r1"},
	}

	event := ex.streamingCompletionEvent(gateway.UsageRecordDraft{
		TokensInput:         11,
		TokensOutput:        13,
		UsageSource:         gateway.UsageSourceReported,
		EndClass:            gateway.StreamEndGraceful,
		DeliveredTokenCount: 13,
	}, billing.Attempt{State: billing.StreamStatePartial, DeliveredTokenCount: 13}, auditledger.AuditLedgerResult{})
	if !event.SettleRequest.EmitSchedulerOutbox {
		t.Fatal("provider-backed streaming settlement must carry scheduler outbox intent")
	}
}

func TestChatCompletions_AuditLedgerAppendWritesHeaders(t *testing.T) {
	enableHCSFDispatchForTest(t)
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.AuditLedger = ledger
	d.Signer = signer

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if ledger.Size(context.Background()) != 1 {
		t.Fatalf("ledger size=%d want 1", ledger.Size(context.Background()))
	}
	if got := rec.Header().Get(headerHUAKAIAuditLedgerID); got == "" {
		t.Fatalf("%s header is empty", headerHUAKAIAuditLedgerID)
	}
	if got := rec.Header().Get(headerHUAKAIAuditSigFingerprint); got != signer.Fingerprint() {
		t.Fatalf("%s=%q want %q", headerHUAKAIAuditSigFingerprint, got, signer.Fingerprint())
	}
	verifyHeader := rec.Header().Get(headerHUAKAIAuditVerify)
	verifyURL, err := url.Parse(verifyHeader)
	if err != nil {
		t.Fatalf("%s=%q parse error: %v", headerHUAKAIAuditVerify, verifyHeader, err)
	}
	query := verifyURL.Query()
	if query.Get("ledger-id") == "" || query.Get("request_id") == "" {
		t.Fatalf("%s=%q want ledger-id and request_id", headerHUAKAIAuditVerify, verifyHeader)
	}
	if got, want := query.Get("tenant_scope_ref"), auditledger.TenantScopeRef(7); got != want {
		t.Fatalf("%s tenant_scope_ref=%q want %q in %q", headerHUAKAIAuditVerify, got, want, verifyHeader)
	}
	verifyRec := invokeAuditVerify(t, ledger, verifyHeader)
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("advertised verify link status=%d body=%s link=%q", verifyRec.Code, verifyRec.Body.String(), verifyHeader)
	}
}

func TestChatCompletions_AuditLedgerAppendFailureEnqueuesDLQAndDelivers(t *testing.T) {
	// 已消除风险：GW-07 的 Append 失败必须持久化一条 DLQ intent，同时仍交付
	// buffered 响应。变异自检：删除 DLQ 入队路径会让 events 为空，即使 handler
	// 仍返回 200，本测试也会失败。
	enableHCSFDispatchForTest(t)
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	dlqSink := &recordingGatewayAuditLedgerDLQ{id: 313}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.AuditLedger = &failingAppendLedger{appendErr: errors.New("ledger unavailable")}
	d.AuditLedgerDLQ = dlqSink
	d.Signer = signer

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if len(dlqSink.events) != 1 {
		t.Fatalf("DLQ events=%d want 1", len(dlqSink.events))
	}
	event := dlqSink.events[0]
	if event.EventKind != dlq.EventKindAuditLedgerEntry {
		t.Fatalf("DLQ EventKind=%q want %q", event.EventKind, dlq.EventKindAuditLedgerEntry)
	}
	if event.IdempotencyKey == "" || event.SourceTable != "audit_ledger" || event.ReplicaStatus != dlq.ReplicaStatusNone {
		t.Fatalf("DLQ envelope mismatch: %+v", event)
	}
	if got := rec.Header().Get(headerHUAKAIAuditLedgerID); got != "" {
		t.Fatalf("%s=%q want empty for Deferred result", headerHUAKAIAuditLedgerID, got)
	}
}

func TestChatCompletions_AuditLedgerAppendAndDLQFailureProductionDoesNotSettle(t *testing.T) {
	// 已消除风险：当生产环境里 Append 与 DLQ 入队都失败时，请求必须在做出正向
	// 结算之前 fail closed。变异自检：在入队失败时仍返回 Deferred 会让状态变 200
	// 并记录一次 settle 调用，于是本测试失败。
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	enableHCSFDispatchForTest(t)
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	settler := &recordingSettler{}
	dlqSink := &recordingGatewayAuditLedgerDLQ{id: 0, err: errors.New("dlq unavailable")}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.AuditLedger = &failingAppendLedger{appendErr: errors.New("ledger unavailable")}
	d.AuditLedgerDLQ = dlqSink
	d.Signer = signer
	d.Settler = settler

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0", len(settler.calls))
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != "audit_ledger_error" {
		t.Fatalf("aborts=%+v want one audit_ledger_error abort", settler.aborts)
	}
	if len(dlqSink.events) != 1 {
		t.Fatalf("DLQ events=%d want 1 attempted enqueue", len(dlqSink.events))
	}
}

func TestChatCompletions_AuditLedgerDuplicateRequestIDStillSettlesDeliveredCharge(t *testing.T) {
	// 变异：保留旧的 ErrDuplicateRequestID Abort+500 分支会让第二次已交付的
	// 请求返回 500，且只有一次非零 settle。
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	enableHCSFDispatchForTest(t)
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	inner, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	ledger := &secondAppendDuplicateLookupLedger{inner: inner}
	settler := &recordingSettler{}
	policy := productionAuditRefPolicyForGatewayTest(false)
	var events []eventbus.RequestCompletionEvent
	bus := eventbus.New(eventbus.Config{HighWorkers: 1, HighBuffer: 4, AuditRefPolicy: policy})
	mustRegisterEventHandler(t, bus, eventbus.HandlerFunc{
		HandlerID:    eventbus.HandlerBillingPersister,
		HandlerTier:  eventbus.TierHigh,
		HandlerOrder: 10,
		IsCritical:   true,
		Fn: func(ctx context.Context, event eventbus.RequestCompletionEvent) error {
			events = append(events, event)
			_, err := settler.Settle(ctx, event.SettleRequest)
			return err
		},
	})
	t.Cleanup(func() {
		_ = bus.Stop(context.Background())
	})

	dlqSink := &recordingGatewayAuditLedgerDLQ{id: 314}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.AuditLedger = ledger
	d.AuditLedgerDLQ = dlqSink
	d.Signer = signer
	d.Settler = settler
	d.CompletionBus = bus
	d.AuditRefPolicy = policy

	first := invokeHandlerPathWithRequestID(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"first"}]}`, "dup-1")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d want 200 body=%s", first.Code, first.Body.String())
	}
	second := invokeHandlerPathWithRequestID(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"second"}]}`, "dup-1")
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d want 200 body=%s", second.Code, second.Body.String())
	}
	if len(settler.calls) != 2 {
		t.Fatalf("settle calls=%d want 2", len(settler.calls))
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("aborts=%+v want none", settler.aborts)
	}
	if len(dlqSink.events) != 0 {
		t.Fatalf("handler DLQ events=%d want 0 for duplicate request_id", len(dlqSink.events))
	}
	firstCost := settler.calls[0].ActualCost
	secondCost := settler.calls[1].ActualCost
	if firstCost.IsZero() {
		t.Fatalf("first settle ActualCost=%s want non-zero", firstCost)
	}
	if !secondCost.Equal(firstCost) {
		t.Fatalf("second settle ActualCost=%s want same non-zero cost as first %s", secondCost, firstCost)
	}
	if len(events) != 2 {
		t.Fatalf("completion events=%d want 2", len(events))
	}
	for i, event := range events {
		if got := event.Metadata["client_request_id"]; got != "dup-1" {
			t.Fatalf("event[%d] client_request_id metadata=%q want dup-1; metadata=%v", i, got, event.Metadata)
		}
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{"first": first, "second": second} {
		if got := rec.Header().Get("X-Huakai-Request-Id"); got == "" || got == "dup-1" {
			t.Fatalf("%s X-Huakai-Request-Id=%q want non-empty server id, not client header", name, got)
		}
		if got := rec.Header().Get(middleware.RequestIDHeader); got == "" || got == "dup-1" {
			t.Fatalf("%s %s=%q want non-empty server id, not client header", name, middleware.RequestIDHeader, got)
		}
	}
}

func TestChatCompletions_DirectSettleNilBusRejectsMissingAuditRef(t *testing.T) {
	// 变异: 删除 CompletionBus==nil 分支 Settle 前的 validator 时，本用例会返回 200 且 settle calls 变成 1。
	enableHCSFDispatchForTest(t)
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.CompletionBus = nil
	d.AuditRefPolicy = productionAuditRefPolicyForGatewayTest(false)

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), clienterr.CodeAuditRefMissing) {
		t.Fatalf("body=%s want %s", rec.Body.String(), clienterr.CodeAuditRefMissing)
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0", len(settler.calls))
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != clienterr.CodeAuditRefMissing {
		t.Fatalf("aborts=%+v want one %s abort", settler.aborts, clienterr.CodeAuditRefMissing)
	}
}

func TestChatCompletions_DirectSettleNilBusAllowsDLQRef(t *testing.T) {
	// 变异: 删除 Deferred ledger result 到 AuditLedgerDLQRef 的映射时，本用例会被 production policy 拒绝且 settle calls 保持 0。
	enableHCSFDispatchForTest(t)
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.CompletionBus = nil
	d.AuditRefPolicy = productionAuditRefPolicyForGatewayTest(false)
	enableDeferredAuditLedgerForGatewayTest(t, &d, 401)

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("aborts=%+v want none", settler.aborts)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
}

func TestChatCompletions_DirectSettleFallbackRejectsMissingAuditRef(t *testing.T) {
	// 变异: 只保护 CompletionBus==nil、漏掉 shouldDirectSettleFallback 分支时，本用例会 回退 settle 并让 settle calls 变成 1。
	enableHCSFDispatchForTest(t)
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.CompletionBus = queueFullCompletionBusForGatewayTest(t)
	d.AuditRefPolicy = productionAuditRefPolicyForGatewayTest(false)

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), clienterr.CodeAuditRefMissing) {
		t.Fatalf("body=%s want %s", rec.Body.String(), clienterr.CodeAuditRefMissing)
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0", len(settler.calls))
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != clienterr.CodeAuditRefMissing {
		t.Fatalf("aborts=%+v want one %s abort", settler.aborts, clienterr.CodeAuditRefMissing)
	}
}

func TestChatCompletions_DirectSettleFallbackAllowsDLQRef(t *testing.T) {
	// 变异: 回退分支 Settle 前误把 DLQRef 分支也要求 fingerprint 时，本用例会返回 500 且 settle calls 保持 0。
	enableHCSFDispatchForTest(t)
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.CompletionBus = queueFullCompletionBusForGatewayTest(t)
	d.AuditRefPolicy = productionAuditRefPolicyForGatewayTest(false)
	enableDeferredAuditLedgerForGatewayTest(t, &d, 402)

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("aborts=%+v want none", settler.aborts)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
}

func TestChatCompletions_DirectSettleProductionEscapeFlagStillRejectsMissingAuditRef(t *testing.T) {
	// 变异: 恢复 production AllowMissingMoneyRef 旁路时，本用例会返回 200 且 settle calls 变成 1。
	enableHCSFDispatchForTest(t)
	logs := captureSlogForTest(t)
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.CompletionBus = nil
	d.AuditRefPolicy = productionAuditRefPolicyForGatewayTest(true)

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), clienterr.CodeAuditRefMissing) {
		t.Fatalf("body=%s want %s", rec.Body.String(), clienterr.CodeAuditRefMissing)
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0", len(settler.calls))
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != clienterr.CodeAuditRefMissing {
		t.Fatalf("aborts=%+v want one %s abort", settler.aborts, clienterr.CodeAuditRefMissing)
	}
	assertLogContains(t, logs, clienterr.CodeAuditRefMissing, "direct_settle", "money_path_audit_ref_missing")
	for _, forbidden := range []string{"missing_ref_details", "validation_err"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("money-path log leaked removed detail field %q: %s", forbidden, logs.String())
		}
	}
}

type failingAppendLedger struct {
	appendErr error
}

func (l *failingAppendLedger) Append(context.Context, auditledger.PreparedEntry) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, l.appendErr
}

func (l *failingAppendLedger) GetByRequestID(context.Context, string) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
}

func (l *failingAppendLedger) GetByRequestIDAndTenantScope(context.Context, string, string) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
}

func (l *failingAppendLedger) LatestMerkleRoot(context.Context) ([32]byte, error) {
	return auditledger.ZeroRoot, nil
}

func (l *failingAppendLedger) Size(context.Context) int { return 0 }

type secondAppendDuplicateLookupLedger struct {
	inner       *auditledger.MemoryLedger
	appendCalls int
}

func (l *secondAppendDuplicateLookupLedger) Append(ctx context.Context, entry auditledger.PreparedEntry) (auditledger.LedgerEntry, error) {
	l.appendCalls++
	if l.appendCalls == 2 {
		if _, err := l.inner.Append(ctx, entry); err != nil && !errors.Is(err, auditledger.ErrDuplicateRequestID) {
			return auditledger.LedgerEntry{}, err
		}
		return auditledger.LedgerEntry{}, auditledger.ErrDuplicateRequestID
	}
	return l.inner.Append(ctx, entry)
}

func (l *secondAppendDuplicateLookupLedger) GetByRequestID(ctx context.Context, requestID string) (auditledger.LedgerEntry, error) {
	return l.inner.GetByRequestID(ctx, requestID)
}

func (l *secondAppendDuplicateLookupLedger) GetByRequestIDAndTenantScope(ctx context.Context, requestID, tenantScopeRef string) (auditledger.LedgerEntry, error) {
	return l.inner.GetByRequestIDAndTenantScope(ctx, requestID, tenantScopeRef)
}

func (l *secondAppendDuplicateLookupLedger) LatestMerkleRoot(ctx context.Context) ([32]byte, error) {
	return l.inner.LatestMerkleRoot(ctx)
}

func (l *secondAppendDuplicateLookupLedger) Size(ctx context.Context) int {
	return l.inner.Size(ctx)
}

type recordingGatewayAuditLedgerDLQ struct {
	id     int64
	events []dlq.Event
	err    error
}

func (q *recordingGatewayAuditLedgerDLQ) Enqueue(_ context.Context, event dlq.Event) (int64, error) {
	q.events = append(q.events, event)
	if q.err != nil {
		return 0, q.err
	}
	return q.id, nil
}

func productionAuditRefPolicyForGatewayTest(allowMissing bool) *eventbus.AuditRefPolicy {
	return &eventbus.AuditRefPolicy{
		ReleaseMode:          eventbus.ReleaseModeProduction,
		AllowMissingMoneyRef: allowMissing,
	}
}

func enableDeferredAuditLedgerForGatewayTest(t *testing.T, d *ChatHandlerDeps, dlqID int64) {
	t.Helper()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	d.AuditLedger = &failingAppendLedger{appendErr: errors.New("ledger unavailable")}
	d.AuditLedgerDLQ = &recordingGatewayAuditLedgerDLQ{id: dlqID}
	d.Signer = signer
}

func queueFullCompletionBusForGatewayTest(t *testing.T) *eventbus.Bus {
	t.Helper()
	bus := eventbus.New(eventbus.Config{})
	mustRegisterEventHandler(t, bus, eventbus.HandlerFunc{
		HandlerID:    eventbus.HandlerBillingPersister,
		HandlerTier:  eventbus.TierHigh,
		HandlerOrder: 10,
		IsCritical:   true,
		Fn: func(context.Context, eventbus.RequestCompletionEvent) error {
			return eventbus.ErrQueueFull
		},
	})
	t.Cleanup(func() {
		_ = bus.Stop(context.Background())
	})
	return bus
}

func invokeHandlerPathWithRequestID(t *testing.T, deps ChatHandlerDeps, path, body, requestID string) *httptest.ResponseRecorder {
	t.Helper()
	h := middleware.RequestID(NewChatCompletionsHandler(deps))
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.RequestIDHeader, requestID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
