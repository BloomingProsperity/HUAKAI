package hermes

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	AlgEdDSA           = "EdDSA"
	DefaultJWTIssuer   = "huakai-gateway"
	DefaultJWTAudience = "hermes-runner"
	DefaultJWTTTL      = 15 * time.Minute
	JWTRefreshLead     = 2 * time.Minute
)

// Claims 是 Hermes runner JWT 的最小 claim 集合。Kid 来自受签名保护的 header。
type Claims struct {
	Iss string `json:"iss"`
	Aud string `json:"aud"`
	Sub string `json:"sub"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
	Nbf int64  `json:"nbf"`
	Kid string `json:"-"`
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

// Sign 用 Ed25519 生成 compact JWT。header/payload 使用 struct JSON 保持字段序稳定。
func Sign(privateKey ed25519.PrivateKey, kid string, claims Claims) (string, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("%w: ed25519 private key required", ErrInvalidInput)
	}
	if strings.TrimSpace(kid) == "" {
		return "", fmt.Errorf("%w: kid is required", ErrInvalidInput)
	}
	header := jwtHeader{Alg: AlgEdDSA, Typ: "JWT", Kid: strings.TrimSpace(kid)}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := b64(headerJSON) + "." + b64(payloadJSON)
	signature := ed25519.Sign(privateKey, []byte(signingInput))
	return signingInput + "." + b64(signature), nil
}

// Verify 校验 Hermes runner JWT 签名、算法白名单和默认 iss/aud/time claim。
func Verify(publicKey ed25519.PublicKey, token string) (Claims, error) {
	return VerifyAt(publicKey, token, time.Now().UTC())
}

func VerifyAt(publicKey ed25519.PublicKey, token string, now time.Time) (Claims, error) {
	return verifyAt(publicKey, token, now, DefaultJWTIssuer, DefaultJWTAudience)
}

func verifyAt(publicKey ed25519.PublicKey, token string, now time.Time, expectedIssuer, expectedAudience string) (Claims, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return Claims{}, fmt.Errorf("%w: ed25519 public key required", ErrInvalidInput)
	}
	header, payload, signingInput, signature, err := parseJWT(token)
	if err != nil {
		return Claims{}, err
	}
	if header.Alg != AlgEdDSA {
		return Claims{}, fmt.Errorf("%w: unsupported jwt alg", ErrForbidden)
	}
	if header.Typ != "" && header.Typ != "JWT" {
		return Claims{}, fmt.Errorf("%w: unsupported jwt typ", ErrForbidden)
	}
	if strings.TrimSpace(header.Kid) == "" {
		return Claims{}, fmt.Errorf("%w: jwt kid is required", ErrForbidden)
	}
	if !ed25519.Verify(publicKey, []byte(signingInput), signature) {
		return Claims{}, fmt.Errorf("%w: jwt signature invalid", ErrForbidden)
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, fmt.Errorf("%w: invalid jwt claims", ErrInvalidInput)
	}
	claims.Kid = header.Kid
	if err := validateClaimsAt(claims, now.UTC(), expectedIssuer, expectedAudience); err != nil {
		return Claims{}, err
	}
	return claims, nil
}

func validateClaimsAt(claims Claims, now time.Time, expectedIssuer, expectedAudience string) error {
	expectedIssuer = strings.TrimSpace(expectedIssuer)
	if expectedIssuer == "" {
		expectedIssuer = DefaultJWTIssuer
	}
	expectedAudience = strings.TrimSpace(expectedAudience)
	if expectedAudience == "" {
		expectedAudience = DefaultJWTAudience
	}
	if claims.Iss != expectedIssuer {
		return fmt.Errorf("%w: invalid jwt issuer", ErrForbidden)
	}
	if claims.Aud != expectedAudience {
		return fmt.Errorf("%w: invalid jwt audience", ErrForbidden)
	}
	if strings.TrimSpace(claims.Sub) == "" {
		return fmt.Errorf("%w: jwt subject required", ErrForbidden)
	}
	if claims.Iat == 0 || claims.Nbf == 0 || claims.Exp == 0 {
		return fmt.Errorf("%w: jwt time claims required", ErrForbidden)
	}
	nowUnix := now.Unix()
	if claims.Iat > nowUnix {
		return fmt.Errorf("%w: jwt issued in the future", ErrForbidden)
	}
	if claims.Nbf > nowUnix {
		return fmt.Errorf("%w: jwt not yet valid", ErrForbidden)
	}
	if claims.Exp < nowUnix {
		return fmt.Errorf("%w: jwt expired", ErrForbidden)
	}
	if claims.Exp <= claims.Nbf {
		return fmt.Errorf("%w: jwt exp must be after nbf", ErrForbidden)
	}
	if claims.Exp-claims.Iat > int64(DefaultJWTTTL.Seconds()) {
		return fmt.Errorf("%w: jwt ttl exceeds policy", ErrForbidden)
	}
	return nil
}

func parseJWT(token string) (jwtHeader, []byte, string, []byte, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return jwtHeader{}, nil, "", nil, fmt.Errorf("%w: jwt must have three parts", ErrInvalidInput)
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return jwtHeader{}, nil, "", nil, fmt.Errorf("%w: invalid jwt header encoding", ErrInvalidInput)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtHeader{}, nil, "", nil, fmt.Errorf("%w: invalid jwt payload encoding", ErrInvalidInput)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return jwtHeader{}, nil, "", nil, fmt.Errorf("%w: invalid jwt signature encoding", ErrInvalidInput)
	}
	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return jwtHeader{}, nil, "", nil, fmt.Errorf("%w: invalid jwt header", ErrInvalidInput)
	}
	return header, payloadBytes, parts[0] + "." + parts[1], signature, nil
}

func b64(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}
