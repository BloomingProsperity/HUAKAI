package gatewayhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/trusthttp"
)

func TestAT_AUDIT_001_009_GetReceiptHit(t *testing.T) {
	signer := mustReceiptSigner(t)
	receipt := signedGatewayReceipt(t, signer, 7, "req-hit")
	store := newReceiptStoreStub(receipt)

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Receipts: store, Signer: signer}), http.MethodGet, "/v1/receipts/req-hit", nil, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got UserCostReceipt
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.RequestID != "req-hit" || got.TenantScopeRef != auditledger.TenantScopeRef(7) || got.Signature == "" || got.CanonicalHash == "" {
		t.Fatalf("receipt response mismatch: %+v", got)
	}
	if strings.Contains(rec.Body.String(), `"tenant_id"`) {
		t.Fatalf("receipt response exposed raw tenant_id: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"user_id"`) {
		t.Fatalf("receipt response exposed raw user_id: %s", rec.Body.String())
	}
}

func TestCostReceiptGetSameTenantCrossUserReturns404(t *testing.T) {
	signer := mustReceiptSigner(t)
	receipt := signedGatewayReceipt(t, signer, 7, "req-cross-user-get")
	receipt.UserID = 7002
	store := newReceiptStoreStub(receipt)

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Receipts: store, Signer: signer}), http.MethodGet, "/v1/receipts/req-cross-user-get", nil, sessionauth.SessionIdentity{TenantID: 7, UserID: 7001})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
	if store.seenUserID != 7001 {
		t.Fatalf("GetReceiptForUser user_id=%d want 7001", store.seenUserID)
	}
}

func TestCostReceiptVerifySameTenantCrossUserReturns404(t *testing.T) {
	signer := mustReceiptSigner(t)
	receipt := signedGatewayReceipt(t, signer, 7, "req-cross-user-verify")
	receipt.UserID = 7002
	payload := mustUserReceipt(t, receipt)
	store := newReceiptStoreStub(receipt)

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Receipts: store, Signer: signer, Now: fixedReceiptNow}), http.MethodPost, "/v1/receipts/req-cross-user-verify/verify", payload, sessionauth.SessionIdentity{TenantID: 7, UserID: 7001})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
	if store.seenUserID != 7001 {
		t.Fatalf("verify GetReceiptForUser user_id=%d want 7001", store.seenUserID)
	}
}

func TestCostReceiptGetLegacyReceiptWithoutOwnerReturns404ForUser(t *testing.T) {
	signer := mustReceiptSigner(t)
	receipt := signedGatewayReceipt(t, signer, 7, "req-legacy-user")
	receipt.UserID = 0
	receipt.ClaimID = 0
	receipt.OwnerSource = ""
	store := newReceiptStoreStub(receipt)

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Receipts: store, Signer: signer}), http.MethodGet, "/v1/receipts/req-legacy-user", nil, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestCostReceiptVerifyDerivedCrossUserReturns404(t *testing.T) {
	signer := mustReceiptSigner(t)
	submitted := signedGatewayReceipt(t, signer, 7, "req-verify-derived-cross-user")
	submitted.UserID = 7002
	payload := mustUserReceipt(t, submitted)
	derived := *submitted
	queue := &mismatchRefundQueueStub{}

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{
		Signer:          signer,
		Now:             fixedReceiptNow,
		DerivedReceipts: &derivedReceiptStub{receipt: &derived},
		MismatchRefunds: queue,
	}), http.MethodPost, "/v1/receipts/req-verify-derived-cross-user/verify", payload, sessionauth.SessionIdentity{TenantID: 7, UserID: 7001})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
	if queue.calls != 0 {
		t.Fatalf("cross-user verify must not enqueue mismatch refund; calls=%d", queue.calls)
	}
}

func TestCostReceiptVerifyCrossUserMismatchDoesNotEnqueueRefund(t *testing.T) {
	signer := mustReceiptSigner(t)
	submitted := signedGatewayReceipt(t, signer, 7, "req-mismatch-cross-user")
	submitted.UserID = 7002
	payload := mustUserReceipt(t, submitted)
	derived := *submitted
	derived.ClaimID = 991
	derived.CostUSDMicros = submitted.CostUSDMicros - 50
	queue := &mismatchRefundQueueStub{}

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{
		Signer:          signer,
		Now:             fixedReceiptNow,
		DerivedReceipts: &derivedReceiptStub{receipt: &derived},
		MismatchRefunds: queue,
	}), http.MethodPost, "/v1/receipts/req-mismatch-cross-user/verify", payload, sessionauth.SessionIdentity{TenantID: 7, UserID: 7001})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
	if queue.calls != 0 {
		t.Fatalf("cross-user mismatch must not enqueue refund; calls=%d", queue.calls)
	}
}

func TestAT_AUDIT_001_010_CrossTenantReceiptReturns404(t *testing.T) {
	signer := mustReceiptSigner(t)
	store := newReceiptStoreStub(signedGatewayReceipt(t, signer, 8, "req-cross"))
	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Receipts: store, Signer: signer}), http.MethodGet, "/v1/receipts/req-cross", nil, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestAT_AUDIT_001_011_ReceiptNotFound(t *testing.T) {
	signer := mustReceiptSigner(t)
	store := &receiptStoreStub{err: audit.ErrReceiptNotFound}
	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Receipts: store, Signer: signer}), http.MethodGet, "/v1/receipts/missing", nil, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestAT_AUDIT_001_012_ReceiptUnavailableReturns202(t *testing.T) {
	signer := mustReceiptSigner(t)
	store := &receiptStoreStub{err: audit.ErrReceiptUnavailable}
	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Receipts: store, Signer: signer}), http.MethodGet, "/v1/receipts/pending", nil, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202 body=%s", rec.Code, rec.Body.String())
	}
}

func TestAT_AUDIT_001_013_DetachedVerifyPass(t *testing.T) {
	signer := mustReceiptSigner(t)
	receipt := signedGatewayReceipt(t, signer, 7, "req-verify")
	store := newReceiptStoreStub(receipt)
	registry := auditledger.NewMemoryPubkeyRegistry(mustGatewayReceiptPubkey(t, signer))

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Receipts: store, PubkeyRegistry: registry, Now: fixedReceiptNow}), http.MethodPost, "/v1/receipts/req-verify/verify", nil, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Valid || got.Status != "signed-only" || !got.SignatureValid || got.KeyStatus != "active" || got.AgeSeconds <= 0 || got.SchemaVersion != "trust.receipt.v1" || got.CanonicalHash == "" {
		t.Fatalf("verify response mismatch: %+v", got)
	}
}

func TestCostReceiptVerifyUnsignedStoredReceiptReturnsUnverified(t *testing.T) {
	signer := mustReceiptSigner(t)
	receipt := signedGatewayReceipt(t, signer, 7, "req-unsigned")
	receipt.SignedHash = nil
	receipt.SignerFingerprint = nil
	store := newReceiptStoreStub(receipt)

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Receipts: store, Now: fixedReceiptNow}), http.MethodPost, "/v1/receipts/req-unsigned/verify", nil, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Valid || got.Status != "unverified" || got.SignatureValid || got.Reason != "receipt_unsigned" {
		t.Fatalf("unsigned receipt verify mismatch: %+v", got)
	}
}

func TestReceiptVerifyMarksRevokedKeyAsUnverified(t *testing.T) {
	signer := mustReceiptSigner(t)
	receipt := signedGatewayReceipt(t, signer, 7, "req-revoked-key")
	store := newReceiptStoreStub(receipt)
	registry := auditledger.NewMemoryPubkeyRegistry(mustGatewayReceiptPubkey(t, signer))

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{
		Receipts:       store,
		PubkeyRegistry: registry,
		Revocations: trusthttp.Revocations{
			signer.Fingerprint(): {Fingerprint: signer.Fingerprint(), RevokedAt: fixedReceiptNow(), ReasonClass: "key_compromise"},
		},
		Now: fixedReceiptNow,
	}), http.MethodPost, "/v1/receipts/req-revoked-key/verify", nil, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Valid || got.Status != "unverified" || !got.SignatureValid || got.KeyStatus != "revoked" || got.Reason != "key_revoked" {
		t.Fatalf("revoked signed receipt verify mismatch: %+v", got)
	}
}

// TestReceiptVerifyRejectsSignatureOutsideKeyWindow guards on the cost-receipt
// path: /v1/receipts/{id}/verify must reject a stored receipt whose occurred_at is
// outside the signing key's [EffectiveFrom, EffectiveTo] window — even with a valid
// ed25519 signature. This is the leaked rotated-key attack mirrored on the user receipt
// endpoint.
//
// Mutation check: remove the SignatureOutsideKeyWindow branch in verifyReceiptTrustSignature
// and the "outside" case flips to valid=true status="signed-only" → red. The in-window
// sub-case proves the check is discriminating, not a blanket reject.
func TestReceiptVerifyRejectsSignatureOutsideKeyWindow(t *testing.T) {
	signer := mustReceiptSigner(t)
	receipt := signedGatewayReceipt(t, signer, 7, "req-window") // occurred_at == CreatedAt 2026-05-18
	store := newReceiptStoreStub(receipt)

	// rotated key valid only 2026-05-01 .. 2026-05-10; receipt dated 2026-05-18 is AFTER EffectiveTo.
	outKey := mustGatewayReceiptPubkey(t, signer)
	outTo := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	outKey.EffectiveTo = &outTo
	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{
		Receipts:       store,
		PubkeyRegistry: auditledger.NewMemoryPubkeyRegistry(outKey),
		Now:            fixedReceiptNow,
	}), http.MethodPost, "/v1/receipts/req-window/verify", nil, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.SignatureValid {
		t.Fatalf("precondition: ed25519 signature must be valid, got %+v", got)
	}
	if got.Valid || got.Status != "unverified" || got.Reason != "signature_outside_key_window" {
		t.Fatalf("receipt signed outside key window must be rejected: %+v", got)
	}

	// Control: same receipt, key window extended past occurred_at → still verifies.
	inKey := mustGatewayReceiptPubkey(t, signer)
	inTo := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	inKey.EffectiveTo = &inTo
	recOK := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{
		Receipts:       newReceiptStoreStub(signedGatewayReceipt(t, signer, 7, "req-window-ok")),
		PubkeyRegistry: auditledger.NewMemoryPubkeyRegistry(inKey),
		Now:            fixedReceiptNow,
	}), http.MethodPost, "/v1/receipts/req-window-ok/verify", nil, receiptSession(7))
	var gotOK receiptVerifyResponse
	if err := json.Unmarshal(recOK.Body.Bytes(), &gotOK); err != nil {
		t.Fatalf("decode ok: %v", err)
	}
	if !gotOK.Valid || gotOK.Status != "signed-only" {
		t.Fatalf("in-window receipt must still verify: %+v", gotOK)
	}
}

func TestCostReceiptVerifyDisplayReceiptIDUsesStoredReceipt(t *testing.T) {
	signer := mustReceiptSigner(t)
	receipt := signedGatewayReceipt(t, signer, 7, "req-display-id")
	displayID, err := audit.FinalTrustReceiptDisplayID(receipt)
	if err != nil {
		t.Fatalf("display id: %v", err)
	}
	store := newReceiptStoreStub(receipt)
	registry := auditledger.NewMemoryPubkeyRegistry(mustGatewayReceiptPubkey(t, signer))

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Receipts: store, PubkeyRegistry: registry, Now: fixedReceiptNow}), http.MethodPost, "/v1/receipts/"+displayID+"/verify", nil, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "signed-only" || !got.SignatureValid || !got.Valid {
		t.Fatalf("display receipt id did not verify stored receipt: %+v", got)
	}
}

func TestAT_AUDIT_001_032_RefundedReceiptVerifyPass(t *testing.T) {
	signer := mustReceiptSigner(t)
	receipt := signedGatewayReceipt(t, signer, 7, "req-refunded-verify")
	receipt.ReceiptSequence = 1
	receipt.ValidationState = audit.ReceiptValidationStateMismatchRefunded
	receipt.Verdict = audit.ReceiptVerdictMismatchRefundPending
	receipt.AdjustmentRefs = []string{"billing_event:123", "audit_ledger:ldg_t7_2"}
	signGatewayReceipt(t, signer, receipt)
	router := receiptRouter(CostReceiptHandlerDeps{
		Receipts: newReceiptStoreStub(receipt),
		Signer:   signer,
		Now:      fixedReceiptNow,
	})

	getRec := doReceiptRequest(t, router, http.MethodGet, "/v1/receipts/req-refunded-verify", nil, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var payload UserCostReceipt
	if err := json.Unmarshal(getRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if payload.ReceiptSequence != 1 {
		t.Fatalf("receipt_sequence=%d want 1 payload=%+v", payload.ReceiptSequence, payload)
	}

	verifyRec := doReceiptRequest(t, router, http.MethodPost, "/v1/receipts/req-refunded-verify/verify", payload, receiptSession(7))
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", verifyRec.Code, verifyRec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(verifyRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode verify: %v", err)
	}
	if !got.Valid || got.KeyStatus != "active" || got.ReceiptSequence != 1 {
		t.Fatalf("refunded receipt verify mismatch: %+v", got)
	}
}

func TestAT_AUDIT_001_014_DetachedVerifyTamperFails(t *testing.T) {
	signer := mustReceiptSigner(t)
	payload := mustUserReceipt(t, signedGatewayReceipt(t, signer, 7, "req-tamper"))
	payload.Cost.CostTotalMicroUSD++

	assertDetachedVerifyInvalid(t, signer, "req-tamper", payload)
}

func TestAT_AUDIT_001_014b_DetachedVerifyValidationStateTamperFails(t *testing.T) {
	signer := mustReceiptSigner(t)
	payload := mustUserReceipt(t, signedGatewayReceipt(t, signer, 7, "req-tamper-validation"))
	payload.ValidationState = "mismatch_refunded"

	assertDetachedVerifyInvalid(t, signer, "req-tamper-validation", payload)
}

func TestAT_AUDIT_001_014c_DetachedVerifyVerdictTamperFails(t *testing.T) {
	signer := mustReceiptSigner(t)
	payload := mustUserReceipt(t, signedGatewayReceipt(t, signer, 7, "req-tamper-verdict"))
	payload.Verdict = "refund_pending"

	assertDetachedVerifyInvalid(t, signer, "req-tamper-verdict", payload)
}

func TestAT_AUDIT_001_014d_DetachedVerifyAdjustmentRefsTamperFails(t *testing.T) {
	signer := mustReceiptSigner(t)
	payload := mustUserReceipt(t, signedGatewayReceipt(t, signer, 7, "req-tamper-adjustments"))
	payload.AdjustmentRefs = []string{"adj-ref-1"}

	assertDetachedVerifyInvalid(t, signer, "req-tamper-adjustments", payload)
}

func assertDetachedVerifyInvalid(t *testing.T, signer *sign.Signer, requestID string, payload UserCostReceipt) {
	t.Helper()
	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Signer: signer, Now: fixedReceiptNow}), http.MethodPost, "/v1/receipts/"+requestID+"/verify", payload, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Valid {
		t.Fatalf("tampered receipt verified: %+v", got)
	}
}

func TestAT_AUDIT_001_015_GetPricingRateTableByVersion(t *testing.T) {
	source := &rateTableSourceStub{table: billing.RateTable{
		ID: 3, Version: "rate-v3", PricingData: json.RawMessage(`{"models":{"gpt-test":{"input_micro_usd":1}}}`),
		EffectiveFrom: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt:     time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}}
	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{RateTables: source}), http.MethodGet, "/v1/pricing/rate-table?version=rate-v3", nil, sessionauth.SessionIdentity{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if source.seenVersion != "rate-v3" || !strings.Contains(rec.Body.String(), `"rate-v3"`) {
		t.Fatalf("pricing response mismatch version=%q body=%s", source.seenVersion, rec.Body.String())
	}
}

func TestAT_AUDIT_001_016_GetPricingSnapshots(t *testing.T) {
	source := &rateTableSourceStub{snapshots: []billing.RateTableSnapshot{
		{ID: 1, Version: "rate-v1", EffectiveFrom: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), CreatedAt: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)},
		{ID: 2, Version: "rate-v2", EffectiveFrom: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
	}}
	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{RateTables: source}), http.MethodGet, "/v1/pricing/snapshots", nil, sessionauth.SessionIdentity{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"rate-v1"`) || !strings.Contains(rec.Body.String(), `"rate-v2"`) {
		t.Fatalf("snapshots missing versions: %s", rec.Body.String())
	}
}

func TestAT_AUDIT_001_023_GetPricingSnapshotByID(t *testing.T) {
	source := &rateTableSourceStub{table: billing.RateTable{
		ID: 44, Version: "rate-v44", PricingData: json.RawMessage(`{"models":{"gpt-test":{"input_micro_usd":2}}}`),
		EffectiveFrom: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt:     time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}}
	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{RateTables: source}), http.MethodGet, "/v1/pricing/snapshots/44", nil, sessionauth.SessionIdentity{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if source.seenSnapshotID != 44 || !strings.Contains(rec.Body.String(), `"rate-v44"`) {
		t.Fatalf("snapshot response mismatch id=%d body=%s", source.seenSnapshotID, rec.Body.String())
	}
}

func TestAT_AUDIT_001_017_GetAuditPubkeyReturnsFingerprint(t *testing.T) {
	signer := mustReceiptSigner(t)
	r := chi.NewRouter()
	r.Get("/v1/audit/pubkey", NewAuditPubkeyHandler(AuditPubkeyDeps{Signer: signer}))

	rec := doReceiptRequest(t, r, http.MethodGet, "/v1/audit/pubkey", nil, sessionauth.SessionIdentity{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got AuditPubkeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Algorithm != "ed25519" || got.Fingerprint != signer.Fingerprint() || got.PublicKeyBase64 == "" {
		t.Fatalf("pubkey response mismatch: %+v", got)
	}
}

func TestAT_AUDIT_001_018_RequestIDHeaderTooLongReturns400(t *testing.T) {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(RequestIDLengthLimiter(MaxRequestIDLength))
	r.Get("/ok", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("X-Request-Id", strings.Repeat("x", MaxRequestIDLength+1))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != `{"error":"request_id_too_long"}` {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestAT_AUDIT_001_019_VerifyV2CoversTrustFields(t *testing.T) {
	signer := mustReceiptSigner(t)
	payload := mustUserReceipt(t, signedGatewayReceipt(t, signer, 7, "req-trust"))
	payload.ValidationState = "mismatch_refunded"
	payload.Verdict = "mismatch_refund_pending"
	payload.AdjustmentRefs = []string{"adj-ref-1"}

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Signer: signer, Now: fixedReceiptNow}), http.MethodPost, "/v1/receipts/req-trust/verify", payload, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Valid {
		t.Fatalf("trust-field tamper verified: %+v", got)
	}
}

func TestAT_AUDIT_001_020_VerifyV1Legacy(t *testing.T) {
	signer := mustReceiptSigner(t)
	payload := legacyV1UserReceipt(t, signer, 7, "req-v1-legacy")

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Signer: signer, Now: fixedReceiptNow}), http.MethodPost, "/v1/receipts/req-v1-legacy/verify", payload, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Valid || got.KeyStatus != "active" {
		t.Fatalf("legacy v1 receipt did not verify: %+v", got)
	}
}

func TestAT_AUDIT_001_063_UnsupportedSchemaVersionReturnsGracefulVerdict(t *testing.T) {
	signer := mustReceiptSigner(t)
	payload := mustUserReceipt(t, signedGatewayReceipt(t, signer, 7, "req-schema-unsupported"))
	payload.SchemaVersion = "audit.receipt.v999"

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Signer: signer, Now: fixedReceiptNow}), http.MethodPost, "/v1/receipts/req-schema-unsupported/verify", payload, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Valid || got.KeyStatus != "unknown" || got.Verdict != "schema_unsupported" || got.Reason != "schema_unsupported" {
		t.Fatalf("unsupported schema verify response=%+v", got)
	}
	if len(got.SupportedVersions) != 2 || got.SupportedVersions[0] != audit.ReceiptSchemaVersionV1 || got.SupportedVersions[1] != audit.ReceiptSchemaVersion {
		t.Fatalf("supported versions=%+v", got.SupportedVersions)
	}
	if strings.Contains(rec.Body.String(), "invalid_receipt") {
		t.Fatalf("unsupported schema must not be reported as invalid_receipt: %s", rec.Body.String())
	}
}

func TestAT_AUDIT_001_024_VerifyMismatchEnqueuesRefund(t *testing.T) {
	signer := mustReceiptSigner(t)
	submitted := signedGatewayReceipt(t, signer, 7, "req-mismatch-refund")
	payload := mustUserReceipt(t, submitted)
	derived := *submitted
	derived.ClaimID = 909
	derived.CostUSDMicros = submitted.CostUSDMicros - 50
	queue := &mismatchRefundQueueStub{}

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{
		Signer:          signer,
		Now:             fixedReceiptNow,
		DerivedReceipts: &derivedReceiptStub{receipt: &derived},
		MismatchRefunds: queue,
	}), http.MethodPost, "/v1/receipts/req-mismatch-refund/verify", payload, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Valid || got.Verdict != audit.ReceiptValidationStateMismatchPending || got.DeltaMicroUSD != 50 {
		t.Fatalf("verify mismatch response=%+v", got)
	}
	if queue.calls != 1 || queue.receipt == nil || queue.receipt.ClaimID != 909 {
		t.Fatalf("refund enqueue calls=%d receipt=%+v", queue.calls, queue.receipt)
	}
}

func TestAT_AUDIT_001_049_UnderChargeNoEnqueue(t *testing.T) {
	signer := mustReceiptSigner(t)
	submitted := signedGatewayReceipt(t, signer, 7, "req-undercharge-no-enqueue")
	payload := mustUserReceipt(t, submitted)
	derived := *submitted
	derived.ClaimID = 910
	derived.CostUSDMicros = submitted.CostUSDMicros + 50
	queue := &mismatchRefundQueueStub{}

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{
		Signer:          signer,
		Now:             fixedReceiptNow,
		DerivedReceipts: &derivedReceiptStub{receipt: &derived},
		MismatchRefunds: queue,
	}), http.MethodPost, "/v1/receipts/req-undercharge-no-enqueue/verify", payload, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Valid || got.Verdict != audit.ReceiptValidationStateMismatchPending || got.DeltaMicroUSD != 50 || got.RefundEventID != 0 {
		t.Fatalf("verify under-charge response=%+v", got)
	}
	if queue.calls != 0 {
		t.Fatalf("under-charge verify must not enqueue mismatch refund; calls=%d", queue.calls)
	}
}

func TestAT_AUDIT_001_041_VerifyRequiresSession(t *testing.T) {
	signer := mustReceiptSigner(t)
	payload := mustUserReceipt(t, signedGatewayReceipt(t, signer, 7, "req-verify-session"))

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Signer: signer, Now: fixedReceiptNow}), http.MethodPost, "/v1/receipts/req-verify-session/verify", payload, sessionauth.SessionIdentity{})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "session_token_required") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestAT_AUDIT_001_042_VerifyCrossTenant404(t *testing.T) {
	signer := mustReceiptSigner(t)
	requestID := "req-verify-cross-tenant"
	submitted := signedGatewayReceipt(t, signer, 8, requestID)
	payload := mustUserReceipt(t, submitted)
	derived := *signedGatewayReceipt(t, signer, 7, requestID)
	queue := &mismatchRefundQueueStub{}

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{
		Signer:          signer,
		Now:             fixedReceiptNow,
		DerivedReceipts: &derivedReceiptStub{receipt: &derived},
		MismatchRefunds: queue,
	}), http.MethodPost, "/v1/receipts/"+requestID+"/verify", payload, receiptSession(8))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
	if queue.calls != 0 {
		t.Fatalf("cross-tenant verify must not enqueue mismatch refund; calls=%d", queue.calls)
	}
}

func TestAT_AUDIT_001_043_VerifyEnqueueOnlySameTenant(t *testing.T) {
	t.Run("same tenant mismatch enqueues", func(t *testing.T) {
		signer := mustReceiptSigner(t)
		submitted := signedGatewayReceipt(t, signer, 7, "req-enqueue-same-tenant")
		payload := mustUserReceipt(t, submitted)
		derived := *submitted
		derived.ClaimID = 1001
		derived.CostUSDMicros = submitted.CostUSDMicros - 75
		queue := &mismatchRefundQueueStub{}

		rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{
			Signer:          signer,
			Now:             fixedReceiptNow,
			DerivedReceipts: &derivedReceiptStub{receipt: &derived},
			MismatchRefunds: queue,
		}), http.MethodPost, "/v1/receipts/req-enqueue-same-tenant/verify", payload, receiptSession(7))
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var got receiptVerifyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Valid || got.Verdict != audit.ReceiptValidationStateMismatchPending || got.RefundEventID != 808 {
			t.Fatalf("verify mismatch response=%+v", got)
		}
		if queue.calls != 1 || queue.receipt == nil || queue.receipt.TenantID != 7 {
			t.Fatalf("refund enqueue calls=%d receipt=%+v", queue.calls, queue.receipt)
		}
	})

	t.Run("submitted tenant mismatch blocks enqueue", func(t *testing.T) {
		signer := mustReceiptSigner(t)
		requestID := "req-enqueue-cross-submitted"
		submitted := signedGatewayReceipt(t, signer, 8, requestID)
		payload := mustUserReceipt(t, submitted)
		derived := *signedGatewayReceipt(t, signer, 7, requestID)
		derived.ClaimID = 1002
		derived.CostUSDMicros = submitted.CostUSDMicros + 75
		queue := &mismatchRefundQueueStub{}

		rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{
			Signer:          signer,
			Now:             fixedReceiptNow,
			DerivedReceipts: &derivedReceiptStub{receipt: &derived},
			MismatchRefunds: queue,
		}), http.MethodPost, "/v1/receipts/"+requestID+"/verify", payload, receiptSession(7))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
		}
		if queue.calls != 0 {
			t.Fatalf("cross-tenant verify must not enqueue mismatch refund; calls=%d", queue.calls)
		}
	})
}

func TestAT_AUDIT_001_029_VerifyDerivationErrorReturnsUnknown(t *testing.T) {
	signer := mustReceiptSigner(t)
	payload := mustUserReceipt(t, signedGatewayReceipt(t, signer, 7, "req-derive-unavailable"))
	queue := &mismatchRefundQueueStub{}

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{
		Signer:          signer,
		Now:             fixedReceiptNow,
		DerivedReceipts: &derivedReceiptStub{err: errors.New("temporary derivation outage")},
		MismatchRefunds: queue,
	}), http.MethodPost, "/v1/receipts/req-derive-unavailable/verify", payload, receiptSession(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Valid || got.KeyStatus != "active" || got.Verdict != audit.ReceiptVerdictUnknown {
		t.Fatalf("verify derivation degrade response=%+v", got)
	}
	if queue.calls != 0 {
		t.Fatalf("derivation failure must not enqueue mismatch refund; calls=%d", queue.calls)
	}
}

func TestAT_AUDIT_001_021_RequestIDWithSlash(t *testing.T) {
	signer := mustReceiptSigner(t)
	receipt := signedGatewayReceipt(t, signer, 7, "host/random-000001")
	store := newReceiptStoreStub(receipt)
	r := receiptRouter(CostReceiptHandlerDeps{Receipts: store, Signer: signer, Now: fixedReceiptNow})

	getRec := doReceiptRequest(t, r, http.MethodGet, "/v1/receipts/host/random-000001", nil, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	payload := mustUserReceipt(t, receipt)
	verifyRec := doReceiptRequest(t, r, http.MethodPost, "/v1/receipts/host/random-000001/verify", payload, receiptSession(7))
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", verifyRec.Code, verifyRec.Body.String())
	}
	var got receiptVerifyResponse
	if err := json.Unmarshal(verifyRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Valid {
		t.Fatalf("slash request_id did not verify: %+v", got)
	}

	named := chi.NewRouter()
	named.Get("/v1/receipts/{request_id_host}/{request_id_tail}", NewCostReceiptGetHandler(CostReceiptHandlerDeps{Receipts: store, Signer: signer, Now: fixedReceiptNow}))
	named.Post("/v1/receipts/{request_id_host}/{request_id_tail}/verify", NewCostReceiptVerifyHandler(CostReceiptHandlerDeps{Receipts: store, Signer: signer, Now: fixedReceiptNow}))
	namedGetRec := doReceiptRequest(t, named, http.MethodGet, "/v1/receipts/host/random-000001", nil, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})
	if namedGetRec.Code != http.StatusOK {
		t.Fatalf("named get status=%d body=%s", namedGetRec.Code, namedGetRec.Body.String())
	}
	namedVerifyRec := doReceiptRequest(t, named, http.MethodPost, "/v1/receipts/host/random-000001/verify", payload, receiptSession(7))
	if namedVerifyRec.Code != http.StatusOK {
		t.Fatalf("named verify status=%d body=%s", namedVerifyRec.Code, namedVerifyRec.Body.String())
	}
}

func TestAT_AUDIT_001_022_ChatCompletionWritesReceiptThenGet200(t *testing.T) {
	enableHCSFDispatchForTest(t)
	signer := mustReceiptSigner(t)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	source := &flowReceiptSource{inputs: audit.ReceiptInputs{
		TenantID:            7,
		UserID:              7001,
		ClaimID:             9001,
		Model:               "gpt-4o",
		InputTokens:         2,
		OutputTokens:        3,
		CachedTokens:        0,
		CostUSDMicros:       10000,
		RateTableSnapshotID: 77,
		CreatedAt:           time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC),
	}}
	formatter, err := audit.NewReceiptFormatter(ledger, nil, source, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}
	store := newReceiptStoreStub()
	hook := audit.NewReceiptHookHandler(formatter, store, audit.WithReceiptHookTrustSigner(signer))

	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.AuditLedger = ledger
	d.Signer = signer
	d.Settler = audit.NewReceiptHookSettler(&stubSettler{}, hook)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Post("/v1/chat/completions", NewChatCompletionsHandler(d))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "req-receipt-flow")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status=%d body=%s", rec.Code, rec.Body.String())
	}
	canonicalRequestID := rec.Header().Get(middleware.RequestIDHeader)
	if canonicalRequestID == "" || canonicalRequestID == "req-receipt-flow" {
		t.Fatalf("%s=%q want server-generated canonical request id", middleware.RequestIDHeader, canonicalRequestID)
	}
	if source.seenRequestID != canonicalRequestID || source.seenTenantID != 7 {
		t.Fatalf("receipt source args request=%q tenant=%d", source.seenRequestID, source.seenTenantID)
	}

	appended := store.receipts[canonicalRequestID]
	if appended == nil || appended.UserID != 7001 || appended.ClaimID != 9001 {
		t.Fatalf("appended receipt owner mismatch: %+v", appended)
	}

	getRec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{Receipts: store, Signer: signer}), http.MethodGet, "/v1/receipts/"+canonicalRequestID, nil, sessionauth.SessionIdentity{TenantID: 7, UserID: 7001})
	if getRec.Code != http.StatusOK {
		t.Fatalf("receipt status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var got UserCostReceipt
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if got.RequestID != canonicalRequestID || got.Signature == "" || got.CanonicalHash == "" || got.ValidationState != "valid" || got.Verdict != "match" {
		t.Fatalf("receipt response mismatch: %+v", got)
	}
}

func receiptRouter(d CostReceiptHandlerDeps) chi.Router {
	r := chi.NewRouter()
	r.Get("/v1/receipts/*", NewCostReceiptGetHandler(d))
	r.Post("/v1/receipts/*", NewCostReceiptVerifyHandler(d))
	r.Get("/v1/pricing/rate-table", NewPricingRateTableHandler(d))
	r.Get("/v1/pricing/snapshots", NewPricingSnapshotsHandler(d))
	r.Get("/v1/pricing/snapshots/{snapshot_id}", NewPricingSnapshotHandler(d))
	return r
}

func doReceiptRequest(t *testing.T, h http.Handler, method, path string, body any, ident sessionauth.SessionIdentity) *httptest.ResponseRecorder {
	t.Helper()
	var reader ioReader = bytes.NewReader(nil)
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if ident.TenantID != 0 || ident.UserID != 0 {
		req = req.WithContext(sessionauth.ContextWithSession(req.Context(), ident))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type ioReader interface {
	Read([]byte) (int, error)
}

func mustReceiptSigner(t *testing.T) *sign.Signer {
	t.Helper()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return signer
}

func mustGatewayReceiptPubkey(t *testing.T, signer *sign.Signer) *auditledger.Pubkey {
	t.Helper()
	key, err := auditledger.PubkeyFromSigner(signer, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("pubkey from signer: %v", err)
	}
	return key
}

func signedGatewayReceipt(t *testing.T, signer *sign.Signer, tenantID int64, requestID string) *audit.CostReceipt {
	t.Helper()
	createdAt := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	receipt := &audit.CostReceipt{
		RequestID:           requestID,
		TenantID:            tenantID,
		UserID:              42,
		ClaimID:             9001,
		OwnerSource:         audit.ReceiptOwnerSourceSettle,
		Model:               "gpt-test",
		InputTokens:         100,
		OutputTokens:        25,
		CachedTokens:        5,
		CostUSDMicros:       1234,
		RateTableSnapshotID: 3,
		SignerFingerprint:   []byte(signer.Fingerprint()),
		CreatedAt:           createdAt,
	}
	signGatewayReceipt(t, signer, receipt)
	return receipt
}

func signGatewayReceipt(t *testing.T, signer *sign.Signer, receipt *audit.CostReceipt) {
	t.Helper()
	canonical, err := canonicalBytesFromCostReceipt(receipt)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	receipt.SignerFingerprint = []byte(signer.Fingerprint())
	receipt.SignedHash = []byte(base64.StdEncoding.EncodeToString(signer.Sign(canonical)))
}

func mustUserReceipt(t *testing.T, receipt *audit.CostReceipt) UserCostReceipt {
	t.Helper()
	out, err := userCostReceiptFromAudit(context.Background(), receipt)
	if err != nil {
		t.Fatalf("format receipt: %v", err)
	}
	return out
}

func legacyV1UserReceipt(t *testing.T, signer *sign.Signer, tenantID int64, requestID string) UserCostReceipt {
	t.Helper()
	occurredAt := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)
	out := UserCostReceipt{
		SchemaVersion: audit.ReceiptSchemaVersionV1,
		RequestID:     requestID,
		TenantID:      tenantID,
		OccurredAt:    occurredAt.Format(time.RFC3339Nano),
		Cost: UserReceiptCost{
			Model:               "gpt-test",
			InputTokens:         100,
			OutputTokens:        25,
			CachedTokens:        5,
			CostTotalMicroUSD:   1234,
			RateTableSnapshotID: 3,
		},
		PubkeyFingerprint: signer.Fingerprint(),
	}
	canonical, err := canonicalBytesFromUserReceipt(out)
	if err != nil {
		t.Fatalf("v1 canonical: %v", err)
	}
	canonicalSum := sha256.Sum256(canonical)
	out.CanonicalHash = hex.EncodeToString(canonicalSum[:])
	out.Signature = base64.StdEncoding.EncodeToString(signer.Sign(canonical))
	return out
}

func fixedReceiptNow() time.Time {
	return time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC)
}

func receiptSession(tenantID int64) sessionauth.SessionIdentity {
	return sessionauth.SessionIdentity{TenantID: tenantID, UserID: 42}
}

type receiptStoreStub struct {
	receipts   map[string]*audit.CostReceipt
	err        error
	seenUserID int64
}

func newReceiptStoreStub(receipts ...*audit.CostReceipt) *receiptStoreStub {
	store := &receiptStoreStub{receipts: map[string]*audit.CostReceipt{}}
	for _, receipt := range receipts {
		store.receipts[receipt.RequestID] = receipt
	}
	return store
}

func (s *receiptStoreStub) GetReceiptForUser(_ context.Context, requestID string, tenantID, userID int64) (*audit.CostReceipt, error) {
	s.seenUserID = userID
	if s.err != nil {
		return nil, s.err
	}
	receipt := s.receipts[requestID]
	if receipt == nil || receipt.TenantID != tenantID || receipt.UserID <= 0 || receipt.UserID != userID {
		return nil, audit.ErrReceiptNotFound
	}
	return receipt, nil
}

func (s *receiptStoreStub) GetReceiptForAdmin(_ context.Context, requestID string, tenantID int64) (*audit.CostReceipt, error) {
	if s.err != nil {
		return nil, s.err
	}
	receipt := s.receipts[requestID]
	if receipt == nil || receipt.TenantID != tenantID {
		return nil, audit.ErrReceiptNotFound
	}
	return receipt, nil
}

func (s *receiptStoreStub) GetReceiptBySequence(_ context.Context, requestID string, tenantID int64, sequence int32) (*audit.CostReceipt, error) {
	if s.err != nil {
		return nil, s.err
	}
	receipt := s.receipts[requestID]
	if receipt == nil || receipt.TenantID != tenantID || receipt.ReceiptSequence != sequence {
		return nil, audit.ErrReceiptNotFound
	}
	return receipt, nil
}

func (s *receiptStoreStub) GetReceiptByDisplayID(_ context.Context, displayID string, tenantID, userID int64) (*audit.CostReceipt, error) {
	if s.err != nil {
		return nil, s.err
	}
	for _, receipt := range s.receipts {
		if receipt == nil || receipt.TenantID != tenantID || receipt.UserID != userID || receipt.UserID <= 0 {
			continue
		}
		got, err := audit.FinalTrustReceiptDisplayID(receipt)
		if err != nil {
			return nil, err
		}
		if got == displayID {
			return receipt, nil
		}
	}
	return nil, audit.ErrReceiptNotFound
}

func (s *receiptStoreStub) AppendReceipt(_ context.Context, receipt *audit.CostReceipt) error {
	if s.err != nil {
		return s.err
	}
	if s.receipts == nil {
		s.receipts = map[string]*audit.CostReceipt{}
	}
	if _, exists := s.receipts[receipt.RequestID]; exists {
		return audit.ErrReceiptDuplicate
	}
	cloned := *receipt
	cloned.SignerFingerprint = append([]byte(nil), receipt.SignerFingerprint...)
	cloned.SignedHash = append([]byte(nil), receipt.SignedHash...)
	s.receipts[receipt.RequestID] = &cloned
	return nil
}

type rateTableSourceStub struct {
	mu             sync.Mutex
	table          billing.RateTable
	snapshots      []billing.RateTableSnapshot
	seenVersion    string
	seenSnapshotID int64
	err            error
}

func (s *rateTableSourceStub) GetRateTable(_ context.Context, version string) (billing.RateTable, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seenVersion = version
	if s.err != nil {
		return billing.RateTable{}, s.err
	}
	if s.table.Version != version {
		return billing.RateTable{}, billing.ErrRateTableNotFound
	}
	return s.table, nil
}

func (s *rateTableSourceStub) GetRateTableSnapshot(_ context.Context, snapshotID int64) (billing.RateTable, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seenSnapshotID = snapshotID
	if s.err != nil {
		return billing.RateTable{}, s.err
	}
	if s.table.ID != snapshotID {
		return billing.RateTable{}, billing.ErrRateTableNotFound
	}
	return s.table, nil
}

func (s *rateTableSourceStub) ListRateTableSnapshots(context.Context) ([]billing.RateTableSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil && !errors.Is(s.err, billing.ErrRateTableNotFound) {
		return nil, s.err
	}
	return s.snapshots, nil
}

type derivedReceiptStub struct {
	receipt *audit.CostReceipt
	err     error
}

func (s *derivedReceiptStub) DeriveReceipt(context.Context, string) (*audit.CostReceipt, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.receipt, nil
}

type mismatchRefundQueueStub struct {
	calls   int
	receipt *audit.CostReceipt
	verdict audit.MismatchVerdict
}

func (s *mismatchRefundQueueStub) EnqueueMismatchRefund(_ context.Context, receipt *audit.CostReceipt, verdict audit.MismatchVerdict) (int64, error) {
	s.calls++
	s.receipt = receipt
	s.verdict = verdict
	return 808, nil
}

type flowReceiptSource struct {
	inputs        audit.ReceiptInputs
	seenRequestID string
	seenTenantID  int64
}

func (s *flowReceiptSource) LookupReceiptInputs(_ context.Context, requestID string, tenantID int64) (audit.ReceiptInputs, error) {
	s.seenRequestID = requestID
	s.seenTenantID = tenantID
	return s.inputs, nil
}
