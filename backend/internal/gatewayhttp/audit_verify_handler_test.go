package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	rec := invokeAuditVerify(t, ledger, "/v1/audit/verify?request_id=req_1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got AuditVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LedgerEntry.RequestID != "req_1" || got.LedgerEntry.TenantID != 7 {
		t.Fatalf("ledger entry mismatch: %+v", got.LedgerEntry)
	}
	if got.ChainProof.MerkleRoot != rootHex(entry.MerkleRoot) {
		t.Fatalf("merkle root=%q want %q", got.ChainProof.MerkleRoot, rootHex(entry.MerkleRoot))
	}
	if got.ChainProof.Signature == "" || got.ChainProof.PubkeyFingerprint == "" {
		t.Fatalf("proof missing signature or fingerprint: %+v", got.ChainProof)
	}
}

func TestAuditVerifyHandler_NotFound(t *testing.T) {
	rec := invokeAuditVerify(t, newAuditVerifyTestLedger(t), "/v1/audit/verify?request_id=missing")
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
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	NewAuditVerifyHandler(AuditVerifyStaticDeps{Ledger: ledger})(rec, req)
	return rec
}

func invokeAuditMerkle(t *testing.T, ledger *auditledger.MemoryLedger) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/audit/merkle-tree.json", nil)
	NewAuditMerkleTreeHandler(AuditVerifyStaticDeps{Ledger: ledger})(rec, req)
	return rec
}
