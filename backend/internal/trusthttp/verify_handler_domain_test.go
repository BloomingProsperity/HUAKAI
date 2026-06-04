package trusthttp

import (
	"encoding/base64"
	"net/http"
	"testing"
)

// /v1/trust/verify must NOT certify "valid trust receipt" for arbitrary bytes that
// merely happen to be signed by the shared audit signing key. The audit ledger signs
// entry_hash bytes (and other trust.ledger.v1 payloads) with the same key family, so
// without domain separation an attacker could base64 such a signed payload and get
// valid=true / status="signed-only".
//
// Both cases below are signed by the SAME trusted signer the handler accepts, so the
// ed25519 check passes — the ONLY thing that must stop them is the domain check.
//
// Mutation check: remove the requireCanonicalTrustReceipt call in verify() (or make it
// always return nil) and both sub-cases flip to valid=true status="signed-only" → red.
func TestVerifyHandlerRejectsNonReceiptSignedPayload(t *testing.T) {
	signer := mustTestSigner(t)
	reg := mustRegistryForSigner(t, signer)

	cases := map[string][]byte{
		// audit-ledger domain JSON (different schema_version, same key)
		"ledger_domain_json": []byte(`{"schema_version":"trust.ledger.v1","entry_hash":"deadbeef","sequence":7}`),
		// raw entry_hash-shaped bytes that are not JSON at all
		"raw_entry_hash_bytes": {0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
	}
	for name, payload := range cases {
		payload := payload
		t.Run(name, func(t *testing.T) {
			req := verifyRequestBody(t,
				base64.StdEncoding.EncodeToString(payload),
				base64.StdEncoding.EncodeToString(signer.Sign(payload)),
				signer.Fingerprint(),
			)
			rec := doTrustVerify(t, NewVerifyHandler(VerifyDeps{Registry: reg}), req)
			got := decodeVerifyResponse(t, rec)

			if rec.Code != http.StatusOK {
				t.Fatalf("unexpected http status %d body=%s", rec.Code, rec.Body.String())
			}
			// The signature really is valid over these bytes — that is the trap.
			if !got.SignatureValid {
				t.Fatalf("precondition: signature should verify over the signed bytes, got %+v", got)
			}
			// But it must NOT be certified as a trust receipt.
			if got.Valid || got.Status == "signed-only" {
				t.Fatalf("non-receipt signed payload certified as receipt: status=%q valid=%v (%+v)", got.Status, got.Valid, got)
			}
			// reason 复用契约内的 payload_invalid(见 OpenAPI TrustVerifyResponse.reason 枚举);
			// status=unverified + signature_valid=true 区分「签名有效但非 receipt 域」与篡改(mismatch)。
			if got.Reason != "payload_invalid" {
				t.Fatalf("want reason=payload_invalid for non-receipt domain, got %+v", got)
			}
			if got.Status != "unverified" {
				t.Fatalf("want status=unverified for signed-but-not-receipt, got %+v", got)
			}
		})
	}
}

// TestVerifyHandlerStillAcceptsRealReceiptBase64 proves the domain check did not break
// the legitimate base64 path: a genuine canonical trust.receipt.v1 signed by the key
// must still return valid=true status="signed-only".
func TestVerifyHandlerStillAcceptsRealReceiptBase64(t *testing.T) {
	signer := mustTestSigner(t)
	canonical := mustCanonicalReceipt(t, sampleTrustReceipt())
	req := verifyRequestBody(t,
		base64.StdEncoding.EncodeToString(canonical),
		base64.StdEncoding.EncodeToString(signer.Sign(canonical)),
		signer.Fingerprint(),
	)
	rec := doTrustVerify(t, NewVerifyHandler(VerifyDeps{Registry: mustRegistryForSigner(t, signer)}), req)
	got := decodeVerifyResponse(t, rec)
	if !got.Valid || got.Status != "signed-only" {
		t.Fatalf("genuine receipt base64 must still verify, got %+v", got)
	}
}
