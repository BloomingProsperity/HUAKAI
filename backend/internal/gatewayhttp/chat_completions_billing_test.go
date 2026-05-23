package gatewayhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

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
	// Risk killed: GW-07 Append failure must persist a DLQ intent and still
	// deliver the buffered response. Mutation self-check: deleting the DLQ
	// enqueue path leaves events empty and this test fails even if the handler
	// still returns 200.
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
	// Risk killed: when both Append and DLQ enqueue fail in production, the
	// request must fail closed before positive settlement. Mutation self-check:
	// returning Deferred despite enqueue failure makes status 200 and records a
	// settle call, so this test fails.
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

func TestChatCompletions_AuditLedgerDuplicateRequestIDAbortsWithoutDLQ(t *testing.T) {
	// Risk killed: ErrDuplicateRequestID is a deterministic replay collision,
	// not a transient append failure. Mutation self-check: treating it like a
	// normal append error enqueues DLQ, returns Deferred, and records a settle.
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	enableHCSFDispatchForTest(t)
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	dlqSink := &recordingGatewayAuditLedgerDLQ{id: 314}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.AuditLedger = &failingAppendLedger{appendErr: auditledger.ErrDuplicateRequestID}
	d.AuditLedgerDLQ = dlqSink
	d.Signer = signer

	result, err := submitAuditLedgerEntry(context.Background(), d, proto.NewEmptyEnvelope(), validIdentity().TenantID, "req-buffered-duplicate")
	if !errors.Is(err, auditledger.ErrDuplicateRequestID) {
		t.Fatalf("submitAuditLedgerEntry error=%v want ErrDuplicateRequestID", err)
	}
	if result != (auditledger.AuditLedgerResult{}) {
		t.Fatalf("submitAuditLedgerEntry result=%+v want zero value on duplicate", result)
	}
	if len(dlqSink.events) != 0 {
		t.Fatalf("direct submit DLQ events=%d want 0", len(dlqSink.events))
	}

	settler := &recordingSettler{}
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
	if len(dlqSink.events) != 0 {
		t.Fatalf("handler DLQ events=%d want 0 for duplicate request_id", len(dlqSink.events))
	}
}

func TestChatCompletions_DirectSettleNilBusRejectsMissingAuditRef(t *testing.T) {
	// Mutation: 删除 CompletionBus==nil 分支 Settle 前的 validator 时，本用例会返回 200 且 settle calls 变成 1。
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
	// Mutation: 删除 Deferred ledger result 到 AuditLedgerDLQRef 的映射时，本用例会被 production policy 拒绝且 settle calls 保持 0。
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
	// Mutation: 只保护 CompletionBus==nil、漏掉 shouldDirectSettleFallback 分支时，本用例会 fallback settle 并让 settle calls 变成 1。
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
	// Mutation: fallback 分支 Settle 前误把 DLQRef 分支也要求 fingerprint 时，本用例会返回 500 且 settle calls 保持 0。
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

func TestChatCompletions_DirectSettleEscapeFlagBypassesAndLogs(t *testing.T) {
	// Mutation: 忽略 AllowMissingMoneyRef 时，本用例会返回 500；删除 bypass ERROR 日志时，日志断言会失败。
	enableHCSFDispatchForTest(t)
	logs := captureSlogForTest(t)
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.CompletionBus = nil
	d.AuditRefPolicy = productionAuditRefPolicyForGatewayTest(true)

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	assertLogContains(t, logs, clienterr.CodeAuditRefMissing, "direct_settle", "escape_flag_active", "missing_ref_details")
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
