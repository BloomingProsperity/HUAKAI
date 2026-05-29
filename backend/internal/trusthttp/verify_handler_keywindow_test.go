package trusthttp

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
)

// TestVerifyHandlerRejectsReceiptSignedOutsideKeyWindow guards S1-032:
// receipt verification must reject a receipt whose occurred_at falls outside the
// signing key's [EffectiveFrom, EffectiveTo] window — even though the ed25519
// signature is cryptographically valid. This is the leaked-rotated-key attack: an
// old key valid only Jan–Mar 2026 signs a NEW receipt dated May 2026.
//
// Mutation check: delete the SignatureOutsideKeyWindow branch in verify() and the
// "outside" sub-assertion flips to valid=true status="signed-only" → red. The
// "inside" control proves a receipt dated within the window still verifies, so the
// check is discriminating (not a blanket reject).
func TestVerifyHandlerRejectsReceiptSignedOutsideKeyWindow(t *testing.T) {
	signer := mustTestSigner(t)
	key := mustPubkeyFromSigner(t, signer, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	effectiveTo := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	key.EffectiveTo = &effectiveTo // rotated: valid only 2026-01-01 .. 2026-03-01
	handler := NewVerifyHandler(VerifyDeps{Registry: auditledger.NewMemoryPubkeyRegistry(key)})

	// Attack: same (rotated) key signs a receipt dated AFTER its EffectiveTo.
	outside := sampleTrustReceipt()
	outside.OccurredAt = time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	canonOut := mustCanonicalReceipt(t, outside)
	reqOut := verifyRequestBody(t,
		base64.StdEncoding.EncodeToString(canonOut),
		base64.StdEncoding.EncodeToString(signer.Sign(canonOut)),
		signer.Fingerprint(),
	)
	gotOut := decodeVerifyResponse(t, doTrustVerify(t, handler, reqOut))
	if !gotOut.SignatureValid {
		t.Fatalf("precondition: ed25519 signature must verify over canonical bytes, got %+v", gotOut)
	}
	if gotOut.Valid || gotOut.Reason != "signature_outside_key_window" {
		t.Fatalf("receipt signed outside key window must be rejected, got %+v", gotOut)
	}

	// Control: a receipt dated INSIDE the window still verifies.
	inside := sampleTrustReceipt()
	inside.OccurredAt = time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	canonIn := mustCanonicalReceipt(t, inside)
	reqIn := verifyRequestBody(t,
		base64.StdEncoding.EncodeToString(canonIn),
		base64.StdEncoding.EncodeToString(signer.Sign(canonIn)),
		signer.Fingerprint(),
	)
	gotIn := decodeVerifyResponse(t, doTrustVerify(t, handler, reqIn))
	if !gotIn.Valid || gotIn.Status != "signed-only" {
		t.Fatalf("in-window receipt must still verify, got %+v", gotIn)
	}
}

// TestVerifyHandlerSignerOnlyDoesNotWindowRejectHistoricalReceipt guards the S1-032a
// codex P2 regression fix: in signer-only mode (no registry), lookupKey fabricates the
// key with EffectiveFrom = verification time, so a naive window check would reject every
// historical receipt (occurred_at < now). The fix only enforces the window when a real
// registry is present. Here a receipt dated 2026-05-27 is verified at "now"=2026-06-01
// via Signer-only deps and MUST still verify.
//
// Mutation check: drop the `h.deps.Registry != nil` guard on the window check and this
// receipt flips to valid=false reason="signature_outside_key_window" → red.
func TestVerifyHandlerSignerOnlyDoesNotWindowRejectHistoricalReceipt(t *testing.T) {
	signer := mustTestSigner(t)
	handler := NewVerifyHandler(VerifyDeps{
		Signer: signer,
		Now:    func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) },
	})
	receipt := sampleTrustReceipt() // OccurredAt 2026-05-27, before the fabricated EffectiveFrom
	canonical := mustCanonicalReceipt(t, receipt)
	req := verifyRequestBody(t,
		base64.StdEncoding.EncodeToString(canonical),
		base64.StdEncoding.EncodeToString(signer.Sign(canonical)),
		signer.Fingerprint(),
	)
	got := decodeVerifyResponse(t, doTrustVerify(t, handler, req))
	if !got.Valid || got.Status != "signed-only" {
		t.Fatalf("signer-only historical receipt must still verify (no fabricated-window reject): %+v", got)
	}
}

// TestVerifyHandlerRevocationTakesPrecedenceOverKeyWindow guards the S1-032 Round-2 codex
// P2 fix: when a key is BOTH CRL-revoked AND signed a receipt outside its effective window,
// the response must report the REVOCATION (key_revoked / key_status=revoked), not mask it as
// signature_outside_key_window — operators must still see that a compromised key was revoked.
//
// Mutation check: move the window check before the revoked branch (the pre-fix order) and
// this asserts key_revoked but gets signature_outside_key_window → red.
func TestVerifyHandlerRevocationTakesPrecedenceOverKeyWindow(t *testing.T) {
	signer := mustTestSigner(t)
	key := mustPubkeyFromSigner(t, signer, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	effectiveTo := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	key.EffectiveTo = &effectiveTo // sample receipt (2026-05-27) is outside this window
	handler := NewVerifyHandler(VerifyDeps{
		Registry: auditledger.NewMemoryPubkeyRegistry(key),
		Revocations: Revocations{
			signer.Fingerprint(): {Fingerprint: signer.Fingerprint(), RevokedAt: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), ReasonClass: "key_compromise"},
		},
	})
	canonical := mustCanonicalReceipt(t, sampleTrustReceipt())
	req := verifyRequestBody(t,
		base64.StdEncoding.EncodeToString(canonical),
		base64.StdEncoding.EncodeToString(signer.Sign(canonical)),
		signer.Fingerprint(),
	)
	got := decodeVerifyResponse(t, doTrustVerify(t, handler, req))
	if got.Valid {
		t.Fatalf("revoked+out-of-window must not be valid: %+v", got)
	}
	if got.Reason != "key_revoked" || got.KeyStatus != "revoked" {
		t.Fatalf("revocation must take precedence over key-window (compromise must stay visible): %+v", got)
	}
}
