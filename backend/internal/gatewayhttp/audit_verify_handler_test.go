package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func TestAuditVerifyHandler_HappyPath(t *testing.T) {
	ledger := newAuditVerifyTestLedger(t)
	entry, err := ledger.Append(context.Background(), auditledger.LedgerEntry{
		LedgerID:  "lid_1",
		RequestID: "req_1",
		TenantID:  7,
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-05-13T10:00:00Z"}},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	rec := invokeAuditVerify(t, ledger, "/v1/audit/verify?request_id=req_1&tenant_scope_ref="+auditledger.TenantScopeRef(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got AuditVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LedgerEntry.RequestID != "req_1" || got.LedgerEntry.TenantScopeRef != auditledger.TenantScopeRef(7) {
		t.Fatalf("ledger entry mismatch: %+v", got.LedgerEntry)
	}
	if got.ChainProof.MerkleRoot != rootHex(entry.MerkleRoot) {
		t.Fatalf("merkle root=%q want %q", got.ChainProof.MerkleRoot, rootHex(entry.MerkleRoot))
	}
	if got.ChainProof.Signature == "" || got.ChainProof.PubkeyFingerprint == "" {
		t.Fatalf("proof missing signature or fingerprint: %+v", got.ChainProof)
	}
}

func TestATPRIV001009AuditVerifyTenantScopeRefMismatchReturns404(t *testing.T) {
	ledger := newAuditVerifyTestLedger(t)
	_, err := ledger.Append(context.Background(), auditledger.LedgerEntry{
		LedgerID:  "lid_scope",
		RequestID: "req_scope",
		TenantID:  7,
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-05-13T10:00:00Z"}},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	rec := invokeAuditVerify(t, ledger, "/v1/audit/verify?request_id=req_scope&tenant_scope_ref="+auditledger.TenantScopeRef(8))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestAT_SECURITY_W1_B14_AuditVerifyRequiresTenantScopeRef(t *testing.T) {
	// Risk killed: a public verify request with only request_id must not read a
	// different tenant's signed ledger entry.
	ledger := newAuditVerifyTestLedger(t)
	_, err := ledger.Append(context.Background(), auditledger.LedgerEntry{
		LedgerID:  "lid_scope_required",
		RequestID: "req_scope_required",
		TenantID:  7,
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-05-13T10:00:00Z"}},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	rec := invokeAuditVerify(t, ledger, "/v1/audit/verify?request_id=req_scope_required")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "lid_scope_required") {
		t.Fatalf("missing tenant_scope_ref response leaked ledger entry: %s", rec.Body.String())
	}
}

func TestAuditVerifyHandler_NotFound(t *testing.T) {
	rec := invokeAuditVerify(t, newAuditVerifyTestLedger(t), "/v1/audit/verify?request_id=missing&tenant_scope_ref="+auditledger.TenantScopeRef(7))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuditVerifyHandler_MissingRequestID(t *testing.T) {
	rec := invokeAuditVerify(t, newAuditVerifyTestLedger(t), "/v1/audit/verify")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuditVerifyHandler_PostBody(t *testing.T) {
	ledger := newAuditVerifyTestLedger(t)
	entry, err := ledger.Append(context.Background(), auditledger.LedgerEntry{
		LedgerID:  "lid_post",
		RequestID: "req_post",
		TenantID:  7,
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-05-13T10:00:00Z"}},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audit/verify", strings.NewReader(`{"request_id":"req_post","tenant_scope_ref":"`+auditledger.TenantScopeRef(7)+`"}`))
	NewAuditVerifyHandler(AuditVerifyStaticDeps{Ledger: ledger})(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got AuditVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LedgerEntry.RequestID != "req_post" || got.ChainProof.MerkleRoot != rootHex(entry.MerkleRoot) {
		t.Fatalf("post verify mismatch: %+v", got)
	}
}

func TestAuditVerifyHandler_PostBodyRejectsOversizedTrailingData(t *testing.T) {
	body := `{"request_id":"req_post"}` + strings.Repeat(" ", auditVerifyBodyMaxBytes)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audit/verify", strings.NewReader(body))
	req.ContentLength = -1
	NewAuditVerifyHandler(AuditVerifyStaticDeps{Ledger: newAuditVerifyTestLedger(t)})(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413 body=%s", rec.Code, rec.Body.String())
	}
}

func TestAT_AUDIT_001_064_AuditVerifyInternalErrorDoesNotLeak(t *testing.T) {
	ledger := &failingAuditVerifyLedger{err: errors.New("pq: relation internal_schema.audit_ledger_entries missing at /srv/huakai/backend/internal/audit/store.go:41")}
	rec := invokeAuditVerifyWithDeps(AuditVerifyStaticDeps{Ledger: ledger}, "/v1/audit/verify?request_id=req_leak&tenant_scope_ref="+auditledger.TenantScopeRef(7))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "internal_schema") || strings.Contains(body, "/srv/huakai") || strings.Contains(body, "audit_ledger_entries") {
		t.Fatalf("audit verify leaked internal error details: %s", body)
	}
	if !strings.Contains(body, "audit ledger temporarily unavailable") {
		t.Fatalf("audit verify safe message missing: %s", body)
	}
}

func TestAuditMerkleTreeHandler_Empty(t *testing.T) {
	ledger := newAuditVerifyTestLedger(t)
	rec := invokeAuditMerkle(t, ledger)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got AuditMerkleTreeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Size != 0 || got.LatestMerkleRoot != rootHex(auditledger.ZeroRoot) {
		t.Fatalf("empty tree mismatch: %+v", got)
	}
}

func TestAuditMerkleTreeHandler_WithEntries(t *testing.T) {
	ledger := newAuditVerifyTestLedger(t)
	_, _ = ledger.Append(context.Background(), auditledger.LedgerEntry{LedgerID: "1", RequestID: "r1"})
	latest, _ := ledger.Append(context.Background(), auditledger.LedgerEntry{LedgerID: "2", RequestID: "r2"})

	rec := invokeAuditMerkle(t, ledger)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got AuditMerkleTreeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Size != 2 || got.LatestMerkleRoot != rootHex(latest.MerkleRoot) {
		t.Fatalf("tree mismatch: %+v latest=%s", got, rootHex(latest.MerkleRoot))
	}
}

func newAuditVerifyTestLedger(t *testing.T) *auditledger.MemoryLedger {
	t.Helper()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	return ledger
}

func invokeAuditVerify(t *testing.T, ledger *auditledger.MemoryLedger, target string) *httptest.ResponseRecorder {
	t.Helper()
	return invokeAuditVerifyWithDeps(AuditVerifyStaticDeps{Ledger: ledger}, target)
}

func invokeAuditMerkle(t *testing.T, ledger *auditledger.MemoryLedger) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/audit/merkle-tree.json", nil)
	NewAuditMerkleTreeHandler(AuditVerifyStaticDeps{Ledger: ledger})(rec, req)
	return rec
}

type failingAuditVerifyLedger struct {
	err error
}

func (l *failingAuditVerifyLedger) GetByRequestID(context.Context, string) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, l.err
}

func (l *failingAuditVerifyLedger) GetByRequestIDAndTenantScope(context.Context, string, string) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, l.err
}

func (l *failingAuditVerifyLedger) LatestMerkleRoot(context.Context) ([32]byte, error) {
	return [32]byte{}, l.err
}

func (l *failingAuditVerifyLedger) Size(context.Context) int {
	return 0
}
