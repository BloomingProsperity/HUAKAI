package gatewayhttp

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
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
