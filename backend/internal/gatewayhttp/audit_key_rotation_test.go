package gatewayhttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestAT_AUDIT_001_050_LedgerVerifyHistoricalKeyAfterRotation(t *testing.T) {
	ctx := context.Background()
	oldSigner := mustReceiptSigner(t)
	newSigner := mustReceiptSigner(t)
	registry := auditledger.NewMemoryPubkeyRegistry()
	effectiveFrom := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	rotatedAt := effectiveFrom.Add(time.Hour)
	if err := auditledger.EnsureSignerPubkey(ctx, registry, oldSigner, effectiveFrom); err != nil {
		t.Fatalf("register old signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(oldSigner)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	entry, err := ledger.Append(ctx, auditledger.LedgerEntry{
		Timestamp: effectiveFrom.Add(30 * time.Minute).Format(time.RFC3339Nano),
		RequestID: "req-historical-key",
		TenantID:  7,
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: effectiveFrom.Format(time.RFC3339Nano)}},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := auditledger.RotateSignerPubkey(ctx, registry, []byte(oldSigner.Fingerprint()), newSigner, rotatedAt); err != nil {
		t.Fatalf("rotate signer: %v", err)
	}

	rec := invokeAuditVerifyWithDeps(AuditVerifyStaticDeps{Ledger: &auditVerifyLedgerStub{entry: entry}, Registry: registry}, "/v1/audit/verify?request_id=req-historical-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got AuditVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ChainProof.SignatureValid == nil || !*got.ChainProof.SignatureValid || got.ChainProof.KeyStatus != "rotated" {
		t.Fatalf("historical key verify response mismatch: %+v", got.ChainProof)
	}
}

func TestAT_AUDIT_001_051_LedgerVerifyUnknownFingerprint(t *testing.T) {
	entry := auditledger.LedgerEntry{
		LedgerID:          "ldg_t7_00000000000000000001",
		Timestamp:         time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		RequestID:         "req-unknown-signer",
		TenantID:          7,
		PubkeyFingerprint: "0011223344556677",
		Signature:         base64.StdEncoding.EncodeToString(make([]byte, 64)),
	}
	rec := invokeAuditVerifyWithDeps(AuditVerifyStaticDeps{
		Ledger:   &auditVerifyLedgerStub{entry: entry},
		Registry: auditledger.NewMemoryPubkeyRegistry(),
	}, "/v1/audit/verify?request_id=req-unknown-signer")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got AuditVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ChainProof.SignatureValid == nil || *got.ChainProof.SignatureValid || got.ChainProof.Reason != "unknown_signer" {
		t.Fatalf("unknown signer response mismatch: %+v", got.ChainProof)
	}
}

func TestAT_AUDIT_001_052_AuditPubkeysListIncludesActiveAndHistorical(t *testing.T) {
	ctx := context.Background()
	oldSigner := mustReceiptSigner(t)
	newSigner := mustReceiptSigner(t)
	registry := auditledger.NewMemoryPubkeyRegistry()
	effectiveFrom := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	rotatedAt := effectiveFrom.Add(time.Hour)
	if err := auditledger.EnsureSignerPubkey(ctx, registry, oldSigner, effectiveFrom); err != nil {
		t.Fatalf("register old signer: %v", err)
	}
	if err := auditledger.RotateSignerPubkey(ctx, registry, []byte(oldSigner.Fingerprint()), newSigner, rotatedAt); err != nil {
		t.Fatalf("rotate signer: %v", err)
	}
	r := chi.NewRouter()
	MountAuditPubkeyRoutes(r, AuditPubkeyDeps{Signer: newSigner, Registry: registry})

	rec := doReceiptRequest(t, r, http.MethodGet, "/v1/audit/pubkeys", nil, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got AuditPubkeysResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := map[string]string{}
	for _, key := range got.Keys {
		seen[key.Fingerprint] = key.KeyStatus
	}
	if seen[oldSigner.Fingerprint()] != "rotated" || seen[newSigner.Fingerprint()] != "active" {
		t.Fatalf("pubkey list missing active/history: %+v", got.Keys)
	}
}

func TestAT_AUDIT_001_052b_AuditPubkeyByFingerprintReturnsHistoricalValidity(t *testing.T) {
	ctx := context.Background()
	oldSigner := mustReceiptSigner(t)
	newSigner := mustReceiptSigner(t)
	registry := auditledger.NewMemoryPubkeyRegistry()
	effectiveFrom := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	rotatedAt := effectiveFrom.Add(time.Hour)
	if err := auditledger.EnsureSignerPubkey(ctx, registry, oldSigner, effectiveFrom); err != nil {
		t.Fatalf("register old signer: %v", err)
	}
	if err := auditledger.RotateSignerPubkey(ctx, registry, []byte(oldSigner.Fingerprint()), newSigner, rotatedAt); err != nil {
		t.Fatalf("rotate signer: %v", err)
	}
	r := chi.NewRouter()
	MountAuditPubkeyRoutes(r, AuditPubkeyDeps{Signer: newSigner, Registry: registry})

	rec := doReceiptRequest(t, r, http.MethodGet, "/v1/audit/pubkey/"+oldSigner.Fingerprint(), nil, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got AuditPubkeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Fingerprint != oldSigner.Fingerprint() || got.KeyStatus != "rotated" || got.EffectiveTo == "" {
		t.Fatalf("historical pubkey response mismatch: %+v", got)
	}
}

func TestAT_AUDIT_001_053_ReceiptVerifyHistoricalKeyAfterRotation(t *testing.T) {
	ctx := context.Background()
	oldSigner := mustReceiptSigner(t)
	newSigner := mustReceiptSigner(t)
	registry := auditledger.NewMemoryPubkeyRegistry()
	effectiveFrom := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	rotatedAt := effectiveFrom.Add(time.Hour)
	if err := auditledger.EnsureSignerPubkey(ctx, registry, oldSigner, effectiveFrom); err != nil {
		t.Fatalf("register old signer: %v", err)
	}
	if err := auditledger.RotateSignerPubkey(ctx, registry, []byte(oldSigner.Fingerprint()), newSigner, rotatedAt); err != nil {
		t.Fatalf("rotate signer: %v", err)
	}
	payload := mustUserReceipt(t, signedGatewayReceipt(t, oldSigner, 7, "req-receipt-historical-key"))

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{
		Signer:         newSigner,
		PubkeyRegistry: registry,
		Now:            fixedReceiptNow,
	}), http.MethodPost, "/v1/receipts/req-receipt-historical-key/verify", payload, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Valid || got.KeyStatus != "rotated" || got.Reason != "" {
		t.Fatalf("historical receipt verify response mismatch: %+v", got)
	}
}

func TestAT_AUDIT_001_054_LedgerVerifyRejectsSignatureOutsideKeyWindow(t *testing.T) {
	ctx := context.Background()
	oldSigner := mustReceiptSigner(t)
	newSigner := mustReceiptSigner(t)
	registry := auditledger.NewMemoryPubkeyRegistry()
	effectiveFrom := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	rotatedAt := effectiveFrom.Add(time.Hour)
	if err := auditledger.EnsureSignerPubkey(ctx, registry, oldSigner, effectiveFrom); err != nil {
		t.Fatalf("register old signer: %v", err)
	}
	if err := auditledger.RotateSignerPubkey(ctx, registry, []byte(oldSigner.Fingerprint()), newSigner, rotatedAt); err != nil {
		t.Fatalf("rotate signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(oldSigner)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	entry, err := ledger.Append(ctx, auditledger.LedgerEntry{
		Timestamp: rotatedAt.Add(time.Minute).Format(time.RFC3339Nano),
		RequestID: "req-outside-key-window",
		TenantID:  7,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	rec := invokeAuditVerifyWithDeps(AuditVerifyStaticDeps{Ledger: &auditVerifyLedgerStub{entry: entry}, Registry: registry}, "/v1/audit/verify?request_id=req-outside-key-window")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got AuditVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ChainProof.SignatureValid == nil || *got.ChainProof.SignatureValid || got.ChainProof.Reason != "signature_outside_key_window" {
		t.Fatalf("outside-window verify response mismatch: %+v", got.ChainProof)
	}
}

func TestAT_AUDIT_001_055_AuditPubkeysListUsesRFC3339Timestamps(t *testing.T) {
	ctx := context.Background()
	oldSigner := mustReceiptSigner(t)
	newSigner := mustReceiptSigner(t)
	registry := auditledger.NewMemoryPubkeyRegistry()
	effectiveFrom := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	rotatedAt := effectiveFrom.Add(time.Hour)
	if err := auditledger.EnsureSignerPubkey(ctx, registry, oldSigner, effectiveFrom); err != nil {
		t.Fatalf("register old signer: %v", err)
	}
	if err := auditledger.RotateSignerPubkey(ctx, registry, []byte(oldSigner.Fingerprint()), newSigner, rotatedAt); err != nil {
		t.Fatalf("rotate signer: %v", err)
	}
	r := chi.NewRouter()
	MountAuditPubkeyRoutes(r, AuditPubkeyDeps{Signer: newSigner, Registry: registry})

	rec := doReceiptRequest(t, r, http.MethodGet, "/v1/audit/pubkeys", nil, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got AuditPubkeysResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range got.Keys {
		if _, err := time.Parse(time.RFC3339, key.EffectiveFrom); err != nil {
			t.Fatalf("effective_from is not RFC3339 for %s: %q err=%v", key.Fingerprint, key.EffectiveFrom, err)
		}
		if key.Fingerprint == oldSigner.Fingerprint() {
			if key.EffectiveFrom != effectiveFrom.Format(time.RFC3339) || key.EffectiveTo != rotatedAt.Format(time.RFC3339) {
				t.Fatalf("historical key timestamps mismatch: %+v", key)
			}
			return
		}
	}
	t.Fatalf("historical pubkey missing from list: %+v", got.Keys)
}

func invokeAuditVerifyWithDeps(d AuditVerifyDeps, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	NewAuditVerifyHandler(d).ServeHTTP(rec, req)
	return rec
}

type auditVerifyLedgerStub struct {
	entry auditledger.LedgerEntry
}

func (s *auditVerifyLedgerStub) GetByRequestID(_ context.Context, requestID string) (auditledger.LedgerEntry, error) {
	if s.entry.RequestID != requestID {
		return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
	}
	return s.entry, nil
}

func (s *auditVerifyLedgerStub) LatestMerkleRoot(context.Context) ([32]byte, error) {
	return s.entry.MerkleRoot, nil
}

func (s *auditVerifyLedgerStub) Size(context.Context) int {
	if s.entry.RequestID == "" {
		return 0
	}
	return 1
}
