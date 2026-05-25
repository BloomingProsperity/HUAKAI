package hermes

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func TestBootstrapIssueRefreshAndRevoke(t *testing.T) {
	// 回归守护：bootstrap issue/refresh 必须先落公钥，撤销后不能继续 refresh。
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := newMemoryJWTKeyQueries()
	keyStore := NewKeyStore(store)
	now := time.Now().UTC()
	issuedAt := now
	issuer := &BootstrapIssuer{
		PrivateKey: privateKey,
		PublicKey:  publicKey,
		KeyStore:   keyStore,
		Issuer:     DefaultJWTIssuer,
		Audience:   DefaultJWTAudience,
		TTL:        15 * time.Minute,
		Now:        func() time.Time { return now },
	}

	token, err := issuer.IssueBootstrapJWT(context.Background(), "runner-7")
	if err != nil {
		t.Fatalf("IssueBootstrapJWT: %v", err)
	}
	claims, err := Verify(publicKey, token)
	if err != nil {
		t.Fatalf("Verify bootstrap token: %v", err)
	}
	if claims.Sub != "runner-7" || claims.Kid == "" {
		t.Fatalf("claims=%+v want runner sub and kid", claims)
	}
	if _, err := keyStore.GetKeyByKid(context.Background(), claims.Kid); err != nil {
		t.Fatalf("bootstrap public key not inserted: %v", err)
	}

	now = now.Add(5 * time.Minute)
	if _, err := issuer.RefreshJWT(context.Background(), token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("RefreshJWT with 10m remaining err=%v want ErrForbidden", err)
	}

	now = issuedAt.Add(14 * time.Minute)
	refreshed, err := issuer.RefreshJWT(context.Background(), token)
	if err != nil {
		t.Fatalf("RefreshJWT: %v", err)
	}
	refreshedClaims, err := VerifyAt(publicKey, refreshed, now)
	if err != nil {
		t.Fatalf("Verify refreshed token: %v", err)
	}
	if refreshedClaims.Sub != "runner-7" || refreshedClaims.Exp <= claims.Exp {
		t.Fatalf("refreshed claims=%+v original=%+v want same sub and later exp", refreshedClaims, claims)
	}

	if err := keyStore.RevokeKey(context.Background(), refreshedClaims.Kid); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	if _, err := issuer.RefreshJWT(context.Background(), refreshed); !errors.Is(err, ErrForbidden) {
		t.Fatalf("RefreshJWT after revoke err=%v want ErrForbidden", err)
	}
}

func TestRefreshJWTUsesConfiguredIssuerAudience(t *testing.T) {
	// 回归守护：refresh 校验必须使用配置的 issuer/audience，不能硬编码默认值。
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := newMemoryJWTKeyQueries()
	keyStore := NewKeyStore(store)
	issuedAt := time.Unix(1700000000, 0).UTC()
	now := issuedAt
	issuer := &BootstrapIssuer{
		PrivateKey: privateKey,
		PublicKey:  publicKey,
		KeyStore:   keyStore,
		Issuer:     "custom-hermes-gateway",
		Audience:   "custom-hermes-runner",
		TTL:        DefaultJWTTTL,
		Now:        func() time.Time { return now },
	}

	customToken, err := issuer.IssueBootstrapJWT(context.Background(), "runner-custom")
	if err != nil {
		t.Fatalf("IssueBootstrapJWT custom issuer/audience: %v", err)
	}
	kid, err := KIDFromToken(customToken)
	if err != nil {
		t.Fatalf("KIDFromToken custom token: %v", err)
	}
	now = issuedAt.Add(14 * time.Minute)
	if _, err := issuer.RefreshJWT(context.Background(), customToken); err != nil {
		t.Fatalf("RefreshJWT custom issuer/audience err=%v want pass", err)
	}

	defaultIssuerToken, err := Sign(privateKey, kid, Claims{
		Iss: DefaultJWTIssuer,
		Aud: DefaultJWTAudience,
		Sub: "runner-custom",
		Iat: issuedAt.Unix(),
		Nbf: issuedAt.Unix(),
		Exp: issuedAt.Add(DefaultJWTTTL).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign default issuer token: %v", err)
	}
	if _, err := issuer.RefreshJWT(context.Background(), defaultIssuerToken); !errors.Is(err, ErrForbidden) {
		t.Fatalf("RefreshJWT default issuer under custom config err=%v want ErrForbidden", err)
	}
}
