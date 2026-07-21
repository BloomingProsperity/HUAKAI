package accountident

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifyOIDCES256IdentityRejectsClaimAndSignatureDrift(t *testing.T) {
	now := time.Date(2026, 7, 21, 18, 0, 0, 0, time.UTC)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kid": "key-1", "kty": "EC", "crv": "P-256", "alg": "ES256", "use": "sig",
			"x": base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))),
			"y": base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32))),
		}}})
	}))
	defer server.Close()

	sign := func(signingKey *ecdsa.PrivateKey, issuer, audience, nonce string, expiresAt time.Time) string {
		token := jwt.NewWithClaims(jwt.SigningMethodES256, oidcClaims{
			Email: "owner@example.test", Nonce: nonce, TeamID: "team-1",
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer: issuer, Subject: "stable-subject", Audience: jwt.ClaimStrings{audience},
				ExpiresAt: jwt.NewNumericDate(expiresAt), IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)),
			},
		})
		token.Header["kid"] = "key-1"
		raw, signErr := token.SignedString(signingKey)
		if signErr != nil {
			t.Fatal(signErr)
		}
		return raw
	}
	valid := sign(key, "https://issuer.example", "client-1", "nonce-1", now.Add(time.Hour))
	identity, err := VerifyOIDCES256Identity(context.Background(), OIDCVerificationInput{
		RawIDToken: valid, Issuer: "https://issuer.example", Audience: "client-1", Nonce: "nonce-1",
		JWKSURL: server.URL, Source: SourceXAIOIDCSubject, RequireAccountScope: true,
		HTTPClient: server.Client(), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.SubjectID != "stable-subject" || identity.AccountID != "team-1" ||
		identity.Email != "owner@example.test" || identity.Source != SourceXAIOIDCSubject {
		t.Fatalf("验证身份=%+v", identity)
	}

	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		raw      string
		nonce    string
		audience string
	}{
		{name: "nonce", raw: valid, nonce: "other", audience: "client-1"},
		{name: "audience", raw: valid, nonce: "nonce-1", audience: "client-2"},
		{name: "issuer", raw: sign(key, "https://attacker.example", "client-1", "nonce-1", now.Add(time.Hour)), nonce: "nonce-1", audience: "client-1"},
		{name: "expired", raw: sign(key, "https://issuer.example", "client-1", "nonce-1", now.Add(-time.Minute)), nonce: "nonce-1", audience: "client-1"},
		{name: "signature", raw: sign(otherKey, "https://issuer.example", "client-1", "nonce-1", now.Add(time.Hour)), nonce: "nonce-1", audience: "client-1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, verifyErr := VerifyOIDCES256Identity(context.Background(), OIDCVerificationInput{
				RawIDToken: test.raw, Issuer: "https://issuer.example", Audience: test.audience, Nonce: test.nonce,
				JWKSURL: server.URL, Source: SourceXAIOIDCSubject, HTTPClient: server.Client(), Now: now,
			})
			if verifyErr == nil {
				t.Fatal("被篡改的 OIDC 身份不应通过")
			}
		})
	}

	withoutScope := jwt.NewWithClaims(jwt.SigningMethodES256, oidcClaims{
		Email: "owner@example.test", Nonce: "nonce-1",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "https://issuer.example", Subject: "stable-subject", Audience: jwt.ClaimStrings{"client-1"},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)), IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)),
		},
	})
	withoutScope.Header["kid"] = "key-1"
	rawWithoutScope, err := withoutScope.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyOIDCES256Identity(context.Background(), OIDCVerificationInput{
		RawIDToken: rawWithoutScope, Issuer: "https://issuer.example", Audience: "client-1", Nonce: "nonce-1",
		JWKSURL: server.URL, Source: SourceXAIOIDCSubject, RequireAccountScope: true,
		HTTPClient: server.Client(), Now: now,
	}); err == nil {
		t.Fatal("缺少账号范围的 OIDC 身份不应通过共享账号导入")
	}
}
