package trusthttp

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func TestWellKnownHandlerReturnsJWKSetWithHuakaiExtensions(t *testing.T) {
	active := mustTestSigner(t)
	revoked := mustTestSigner(t)
	activeKey := mustPubkeyFromSigner(t, active, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	revokedKey := mustPubkeyFromSigner(t, revoked, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	registry := auditledger.NewMemoryPubkeyRegistry(activeKey, revokedKey)
	revokedAt := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	handler := NewWellKnownHandler(WellKnownDeps{
		Signer:   active,
		Registry: registry,
		Revocations: Revocations{
			revoked.Fingerprint(): {
				Fingerprint: revoked.Fingerprint(),
				RevokedAt:   revokedAt,
				ReasonClass: "key_compromise",
			},
		},
		Now: fixedTrustHTTPNow,
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/huakai-pubkey.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var got wellKnownPubkeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SchemaVersion != "huakai.pubkey.v1" || got.GeneratedAt != fixedTrustHTTPNow().Format(time.RFC3339) {
		t.Fatalf("top-level metadata mismatch: %+v", got)
	}
	if got.Current != active.Fingerprint() {
		t.Fatalf("current=%q want active fingerprint %q", got.Current, active.Fingerprint())
	}
	if len(got.Keys) != 2 || len(got.Revoked) != 1 {
		t.Fatalf("keys/revoked shape mismatch: %+v", got)
	}

	activeJWK := keyByID(t, got.Keys, active.Fingerprint())
	if activeJWK.KTY != "OKP" || activeJWK.CRV != "Ed25519" || activeJWK.Algorithm != "EdDSA" || activeJWK.Use != "sig" || activeJWK.Status != "active" {
		t.Fatalf("active JWK mismatch: %+v", activeJWK)
	}
	if _, err := base64.RawURLEncoding.DecodeString(activeJWK.X); err != nil {
		t.Fatalf("active JWK x must be base64url without padding: %v", err)
	}

	revokedJWK := keyByID(t, got.Keys, revoked.Fingerprint())
	if revokedJWK.Status != "revoked" || revokedJWK.RevokedAt != revokedAt.Format(time.RFC3339) || revokedJWK.ReasonClass != "key_compromise" {
		t.Fatalf("revoked key did not carry CRL overlay: %+v", revokedJWK)
	}
	if got.Revoked[0].Fingerprint != revoked.Fingerprint() || got.Revoked[0].ReasonClass != "key_compromise" {
		t.Fatalf("revoked array missing fingerprint/reason: %+v", got.Revoked)
	}
}

func TestWellKnownReturnsCacheControl(t *testing.T) {
	signer := mustTestSigner(t)
	handler := NewWellKnownHandler(WellKnownDeps{
		Signer: signer,
		Now:    fixedTrustHTTPNow,
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/huakai-pubkey.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=300, stale-while-revalidate=86400" {
		t.Fatalf("Cache-Control=%q", got)
	}
}

func TestWellKnownHandlerRequiresRevokedOverlayToChangeKeyStatus(t *testing.T) {
	signer := mustTestSigner(t)
	registry := auditledger.NewMemoryPubkeyRegistry(mustPubkeyFromSigner(t, signer, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)))

	rec := httptest.NewRecorder()
	NewWellKnownHandler(WellKnownDeps{
		Signer:      signer,
		Registry:    registry,
		Revocations: Revocations{signer.Fingerprint(): {Fingerprint: signer.Fingerprint(), RevokedAt: fixedTrustHTTPNow(), ReasonClass: "test"}},
		Now:         fixedTrustHTTPNow,
	}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/huakai-pubkey.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var got wellKnownPubkeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	key := keyByID(t, got.Keys, signer.Fingerprint())
	if key.Status != "revoked" || len(got.Revoked) != 1 {
		t.Fatalf("revocation overlay was not applied: key=%+v revoked=%+v", key, got.Revoked)
	}
}

func keyByID(t *testing.T, keys []wellKnownJWK, kid string) wellKnownJWK {
	t.Helper()
	for _, key := range keys {
		if key.KID == kid {
			return key
		}
	}
	t.Fatalf("kid %q not found in %+v", kid, keys)
	return wellKnownJWK{}
}

func mustTestSigner(t *testing.T) *sign.Signer {
	t.Helper()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return signer
}

func mustPubkeyFromSigner(t *testing.T, signer *sign.Signer, effectiveFrom time.Time) *auditledger.Pubkey {
	t.Helper()
	key, err := auditledger.PubkeyFromSigner(signer, effectiveFrom)
	if err != nil {
		t.Fatalf("pubkey from signer: %v", err)
	}
	return key
}

func mustRegistryForSigner(t *testing.T, signer *sign.Signer) *auditledger.MemoryPubkeyRegistry {
	t.Helper()
	return auditledger.NewMemoryPubkeyRegistry(mustPubkeyFromSigner(t, signer, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)))
}

func fixedTrustHTTPNow() time.Time {
	return time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
}

var _ ed25519.PublicKey
var _ = context.Background
