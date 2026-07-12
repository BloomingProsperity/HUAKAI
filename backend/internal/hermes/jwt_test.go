package hermes

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestJWTSignVerifyRoundTripAndRejectsMutations(t *testing.T) {
	// 回归守护：runner JWT 必须只接受非对称 EdDSA，alg/header 或签名篡改都不能通过。
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	now := time.Now().UTC()
	claims := Claims{
		Iss: "huakai-gateway",
		Aud: "hermes-runner",
		Sub: "runner-7",
		Iat: now.Unix(),
		Nbf: now.Add(-time.Second).Unix(),
		Exp: now.Add(15 * time.Minute).Unix(),
	}

	token, err := Sign(privateKey, "kid-a", claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := Verify(publicKey, token)
	if err != nil {
		t.Fatalf("Verify valid token: %v", err)
	}
	if got.Iss != claims.Iss || got.Aud != claims.Aud || got.Sub != claims.Sub || got.Kid != "kid-a" {
		t.Fatalf("claims=%+v want iss/aud/sub/kid from signed token", got)
	}

	algNone := replaceJWTPart(t, token, 0, `{"alg":"none","typ":"JWT","kid":"kid-a"}`)
	if _, err := Verify(publicKey, algNone); err == nil {
		t.Fatalf("alg=none token verified; want EdDSA whitelist rejection")
	}

	tamperedSignature := flipJWTSignatureByte(t, token)
	if _, err := Verify(publicKey, tamperedSignature); err == nil {
		t.Fatalf("tampered signature verified; want signature rejection")
	}

	expired := claims
	expired.Iat = now.Add(-20 * time.Minute).Unix()
	expired.Nbf = now.Add(-20 * time.Minute).Unix()
	expired.Exp = now.Add(-10 * time.Minute).Unix()
	expiredToken, err := Sign(privateKey, "kid-a", expired)
	if err != nil {
		t.Fatalf("Sign expired token: %v", err)
	}
	if _, err := Verify(publicKey, expiredToken); err == nil {
		t.Fatalf("expired token verified; want exp rejection")
	}
}

func TestJWTVerifyRejectsWrongAudienceAndFutureNBF(t *testing.T) {
	// 回归守护：即使用正确 key 签名，错误 audience 或未来 nbf 也必须拒绝。
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	now := time.Now().UTC()
	claims := Claims{
		Iss: "huakai-gateway",
		Aud: "wrong-runner",
		Sub: "runner-7",
		Iat: now.Unix(),
		Nbf: now.Add(-time.Second).Unix(),
		Exp: now.Add(15 * time.Minute).Unix(),
	}
	token, err := Sign(privateKey, "kid-a", claims)
	if err != nil {
		t.Fatalf("Sign wrong audience: %v", err)
	}
	if _, err := Verify(publicKey, token); err == nil {
		t.Fatalf("wrong audience token verified; want audience rejection")
	}

	claims.Aud = DefaultJWTAudience
	claims.Nbf = now.Add(10 * time.Minute).Unix()
	token, err = Sign(privateKey, "kid-a", claims)
	if err != nil {
		t.Fatalf("Sign future nbf: %v", err)
	}
	if _, err := Verify(publicKey, token); err == nil {
		t.Fatalf("future nbf token verified; want not-before rejection")
	}
}

func TestJWTVerifyRejectsFutureIATEvenWhenNBFAllowsNow(t *testing.T) {
	// 回归守护:nbf<=now 但 iat 在未来的 token 不能被接受。
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	now := time.Now().UTC()
	claims := Claims{
		Iss: DefaultJWTIssuer,
		Aud: DefaultJWTAudience,
		Sub: "runner-7",
		Iat: now.Add(10 * time.Minute).Unix(),
		Nbf: now.Add(-time.Second).Unix(),
		Exp: now.Add(15 * time.Minute).Unix(),
	}
	token, err := Sign(privateKey, "kid-a", claims)
	if err != nil {
		t.Fatalf("Sign future iat: %v", err)
	}
	if _, err := VerifyAt(publicKey, token, now); err == nil {
		t.Fatalf("future iat token verified; want issued-at rejection")
	}
}

func replaceJWTPart(t *testing.T, token string, index int, jsonPart string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token parts=%d want 3", len(parts))
	}
	parts[index] = base64.RawURLEncoding.EncodeToString([]byte(jsonPart))
	return strings.Join(parts, ".")
}

func flipJWTSignatureByte(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token parts=%d want 3", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	sig[0] ^= 0x80
	parts[2] = base64.RawURLEncoding.EncodeToString(sig)
	return strings.Join(parts, ".")
}
