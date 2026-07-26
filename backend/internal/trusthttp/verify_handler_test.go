package trusthttp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/trustreceipt"
)

func TestVerifyHandlerValidBase64PayloadReturnsSignedOnly(t *testing.T) {
	signer := mustTestSigner(t)
	canonical := mustCanonicalReceipt(t, sampleTrustReceipt())
	req := verifyRequestBody(t, base64.StdEncoding.EncodeToString(canonical), base64.StdEncoding.EncodeToString(signer.Sign(canonical)), signer.Fingerprint())

	rec := doTrustVerify(t, NewVerifyHandler(VerifyDeps{Registry: mustRegistryForSigner(t, signer)}), req)
	got := decodeVerifyResponse(t, rec)
	if rec.Code != http.StatusOK || !got.Valid || got.Status != "signed-only" || !got.SignatureValid || got.KeyStatus != "active" {
		t.Fatalf("verify response mismatch status=%d body=%s decoded=%+v", rec.Code, rec.Body.String(), got)
	}
	if got.SchemaVersion != "trust.receipt.v1" || got.CanonicalHash == "" {
		t.Fatalf("schema/hash missing: %+v", got)
	}
}

func TestVerifyHandlerValidJSONPayloadReturnsSignedOnly(t *testing.T) {
	signer := mustTestSigner(t)
	receipt := sampleTrustReceipt()
	canonical := mustCanonicalReceipt(t, receipt)
	payload := map[string]any{
		"schema_version":              "trust.receipt.v1",
		"receipt_id":                  trustreceipt.ReceiptID(receipt.RequestID, receipt.ReceiptSequence),
		"request_id":                  receipt.RequestID,
		"receipt_sequence":            receipt.ReceiptSequence,
		"tenant_scope_ref":            receipt.TenantScopeRef,
		"occurred_at":                 receipt.OccurredAt.UTC().Format(time.RFC3339Nano),
		"provider":                    receipt.Provider,
		"requested_model":             receipt.RequestedModel,
		"routed_model":                receipt.RoutedModel,
		"upstream_model":              receipt.UpstreamModel,
		"delivered_model":             receipt.DeliveredModel,
		"cost_cents":                  receipt.CostCents,
		"token_counts":                map[string]any{"input": receipt.TokenCounts.Input, "output": receipt.TokenCounts.Output, "cached": receipt.TokenCounts.Cached},
		"price_snapshot":              map[string]any{"rate_table_snapshot_id": receipt.PriceSnapshot.RateTableSnapshotID, "snapshot_version": receipt.PriceSnapshot.SnapshotVersion, "currency_code": receipt.PriceSnapshot.CurrencyCode},
		"validation_state":            receipt.ValidationState,
		"redacted_metadata_allowlist": map[string]any{"safe_label": "green"},
	}
	raw := bytes.NewBuffer(nil)
	if err := json.NewEncoder(raw).Encode(map[string]any{
		"payload":            payload,
		"signature":          base64.StdEncoding.EncodeToString(signer.Sign(canonical)),
		"pubkey_fingerprint": signer.Fingerprint(),
	}); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	rec := doTrustVerify(t, NewVerifyHandler(VerifyDeps{Registry: mustRegistryForSigner(t, signer)}), raw.String())
	got := decodeVerifyResponse(t, rec)
	if rec.Code != http.StatusOK || got.Status != "signed-only" || !got.SignatureValid {
		t.Fatalf("JSON payload did not verify status=%d body=%s decoded=%+v", rec.Code, rec.Body.String(), got)
	}
}

func TestVerifyHandlerInvalidSignatureReturnsMismatch(t *testing.T) {
	signer := mustTestSigner(t)
	canonical := mustCanonicalReceipt(t, sampleTrustReceipt())
	tampered := append([]byte(nil), canonical...)
	tampered[len(tampered)-2] ^= 0x01
	req := verifyRequestBody(t, base64.StdEncoding.EncodeToString(tampered), base64.StdEncoding.EncodeToString(signer.Sign(canonical)), signer.Fingerprint())

	rec := doTrustVerify(t, NewVerifyHandler(VerifyDeps{Registry: mustRegistryForSigner(t, signer)}), req)
	got := decodeVerifyResponse(t, rec)
	if rec.Code != http.StatusOK || got.Valid || got.Status != "mismatch" || got.SignatureValid || got.KeyStatus != "active" {
		t.Fatalf("invalid signature mismatch not enforced status=%d body=%s decoded=%+v", rec.Code, rec.Body.String(), got)
	}
}

func TestVerifyHandlerUnknownFingerprintReturnsMismatch(t *testing.T) {
	signer := mustTestSigner(t)
	canonical := mustCanonicalReceipt(t, sampleTrustReceipt())
	req := verifyRequestBody(t, base64.StdEncoding.EncodeToString(canonical), base64.StdEncoding.EncodeToString(signer.Sign(canonical)), "abcdef1234567890")

	rec := doTrustVerify(t, NewVerifyHandler(VerifyDeps{Registry: mustRegistryForSigner(t, signer)}), req)
	got := decodeVerifyResponse(t, rec)
	if rec.Code != http.StatusOK || got.Valid || got.Status != "mismatch" || got.KeyStatus != "unknown" || got.Reason != "unknown_signer" {
		t.Fatalf("unknown fingerprint response mismatch status=%d body=%s decoded=%+v", rec.Code, rec.Body.String(), got)
	}
}

func TestVerifyHandlerRevokedKeyReturnsUnverified(t *testing.T) {
	signer := mustTestSigner(t)
	canonical := mustCanonicalReceipt(t, sampleTrustReceipt())
	req := verifyRequestBody(t, base64.StdEncoding.EncodeToString(canonical), base64.StdEncoding.EncodeToString(signer.Sign(canonical)), signer.Fingerprint())

	rec := doTrustVerify(t, NewVerifyHandler(VerifyDeps{
		Registry: mustRegistryForSigner(t, signer),
		Revocations: Revocations{
			signer.Fingerprint(): {Fingerprint: signer.Fingerprint(), RevokedAt: fixedTrustHTTPNow(), ReasonClass: "key_compromise"},
		},
	}), req)
	got := decodeVerifyResponse(t, rec)
	if rec.Code != http.StatusOK || got.Valid || got.Status != "unverified" || !got.SignatureValid || got.KeyStatus != "revoked" || got.Reason != "key_revoked" {
		t.Fatalf("revoked key response mismatch status=%d body=%s decoded=%+v", rec.Code, rec.Body.String(), got)
	}
}

func TestVerifyHandlerMissingRequiredFieldsReturnsMissing(t *testing.T) {
	rec := doTrustVerify(t, NewVerifyHandler(VerifyDeps{}), `{"payload":""}`)
	got := decodeVerifyResponse(t, rec)
	if rec.Code != http.StatusOK || got.Valid || got.Status != "missing" || got.Reason == "" {
		t.Fatalf("missing field response mismatch status=%d body=%s decoded=%+v", rec.Code, rec.Body.String(), got)
	}
}

func TestVerifyHandlerBodyMaxIs10KB(t *testing.T) {
	rec := doTrustVerify(t, NewVerifyHandler(VerifyDeps{}), `{"payload":"`+strings.Repeat("a", 11*1024)+`"}`)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413 body=%s", rec.Code, rec.Body.String())
	}
}

func TestVerifyHandlerAnonymousIPLimitIsSixtyPerMinute(t *testing.T) {
	signer := mustTestSigner(t)
	canonical := mustCanonicalReceipt(t, sampleTrustReceipt())
	body := verifyRequestBody(t, base64.StdEncoding.EncodeToString(canonical), base64.StdEncoding.EncodeToString(signer.Sign(canonical)), signer.Fingerprint())
	handler := NewVerifyHandler(VerifyDeps{Registry: mustRegistryForSigner(t, signer)})

	for i := 0; i < 60; i++ {
		rec := doTrustVerifyFromAddr(t, handler, body, "203.0.113.10:1234")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := doTrustVerifyFromAddr(t, handler, body, "203.0.113.10:1234")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("61st request status=%d want 429 body=%s", rec.Code, rec.Body.String())
	}
}

func TestVerifyHandlerTrustedProxySeparatesRealClients(t *testing.T) {
	signer := mustTestSigner(t)
	canonical := mustCanonicalReceipt(t, sampleTrustReceipt())
	body := verifyRequestBody(t, base64.StdEncoding.EncodeToString(canonical), base64.StdEncoding.EncodeToString(signer.Sign(canonical)), signer.Fingerprint())
	resolver, err := clientip.NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("build resolver: %v", err)
	}
	handler := NewVerifyHandler(VerifyDeps{
		Registry: mustRegistryForSigner(t, signer),
		ClientIP: resolver,
	})

	for i := 0; i < 60; i++ {
		rec := doTrustVerifyFromProxy(t, handler, body, "10.1.2.3:1234", "198.51.100.10")
		if rec.Code != http.StatusOK {
			t.Fatalf("client A request %d status=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}
	if rec := doTrustVerifyFromProxy(t, handler, body, "10.1.2.3:1234", "198.51.100.11"); rec.Code != http.StatusOK {
		t.Fatalf("client B behind same trusted proxy must have an independent bucket, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doTrustVerifyFromProxy(t, handler, body, "10.1.2.3:1234", "198.51.100.10"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("client A request 61 status=%d want 429 body=%s", rec.Code, rec.Body.String())
	}
}

func TestVerifyHandlerUntrustedPeerCannotForgeFreshBuckets(t *testing.T) {
	signer := mustTestSigner(t)
	canonical := mustCanonicalReceipt(t, sampleTrustReceipt())
	body := verifyRequestBody(t, base64.StdEncoding.EncodeToString(canonical), base64.StdEncoding.EncodeToString(signer.Sign(canonical)), signer.Fingerprint())
	resolver, err := clientip.NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("build resolver: %v", err)
	}
	handler := NewVerifyHandler(VerifyDeps{
		Registry: mustRegistryForSigner(t, signer),
		ClientIP: resolver,
	})

	for i := 0; i < 60; i++ {
		xff := "198.51.100." + strconv.Itoa(i+1)
		rec := doTrustVerifyFromProxy(t, handler, body, "203.0.113.10:1234", xff)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := doTrustVerifyFromProxy(t, handler, body, "203.0.113.10:1234", "192.0.2.250")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("forged forwarding headers minted fresh buckets, status=%d want 429 body=%s", rec.Code, rec.Body.String())
	}
}

func sampleTrustReceipt() trustreceipt.TrustReceiptV1 {
	return trustreceipt.TrustReceiptV1{
		RequestID:       "req-trust-http",
		ReceiptSequence: 0,
		TenantScopeRef:  "tenant:7",
		OccurredAt:      time.Date(2026, 5, 27, 12, 30, 0, 0, time.UTC),
		Provider:        "openai",
		RequestedModel:  "gpt-4o",
		RoutedModel:     "gpt-4o-mini",
		UpstreamModel:   "gpt-4o-mini",
		DeliveredModel:  "gpt-4o-mini",
		CostCents:       12,
		TokenCounts:     trustreceipt.TokenCounts{Input: 40, Output: 12, Cached: 3},
		PriceSnapshot:   trustreceipt.PriceSnapshot{RateTableSnapshotID: 44, SnapshotVersion: "registry:7:44", CurrencyCode: "USD"},
		ValidationState: "valid",
		RedactedMetadataAllowlist: map[string]any{
			"safe_label": "green",
		},
	}
}

func mustCanonicalReceipt(t *testing.T, receipt trustreceipt.TrustReceiptV1) []byte {
	t.Helper()
	canonical, err := trustreceipt.Canonical(receipt)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	return canonical
}

func verifyRequestBody(t *testing.T, payload string, signature string, fingerprint string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"payload":            payload,
		"signature":          signature,
		"pubkey_fingerprint": fingerprint,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return string(raw)
}

func doTrustVerify(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doTrustVerifyFromAddr(t, handler, body, "198.51.100.7:9999")
}

func doTrustVerifyFromAddr(t *testing.T, handler http.Handler, body string, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/trust/verify", strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func doTrustVerifyFromProxy(t *testing.T, handler http.Handler, body, remoteAddr, xff string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/trust/verify", strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", xff)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeVerifyResponse(t *testing.T, rec *httptest.ResponseRecorder) VerifyResponse {
	t.Helper()
	var got VerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	return got
}
