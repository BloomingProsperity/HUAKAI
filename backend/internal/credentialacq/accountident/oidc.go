package accountident

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const maxJWKSResponseBytes = 1 << 20

// OIDCVerificationInput 描述固定 OIDC 发行方的身份校验合同。调用方必须提供经过
// SSRF 防护的 HTTP client，并把发现文档确认过的 issuer/JWKS 固定在服务端配置中。
type OIDCVerificationInput struct {
	RawIDToken          string
	Issuer              string
	Audience            string
	Nonce               string
	RequireNonce        bool
	JWKSURL             string
	Source              string
	RequireAccountScope bool
	HTTPClient          *http.Client
	Now                 time.Time
}

type AccessTokenScopeVerificationInput struct {
	RawAccessToken  string
	Issuer          string
	Audience        string
	ExpectedSubject string
	JWKSURL         string
	Source          string
	HTTPClient      *http.Client
	Now             time.Time
}

type oidcClaims struct {
	Email  string `json:"email"`
	Nonce  string `json:"nonce"`
	TeamID string `json:"team_id"`
	jwt.RegisteredClaims
}

type oidcJWKSet struct {
	Keys []oidcJWK `json:"keys"`
}

type oidcJWK struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// VerifyOIDCES256Identity 验证 ES256 OIDC ID Token，并只返回可持久化的非机密身份。
// 任一签名或声明条件不满足都会失败，不能降级为未经验证的自动账号匹配。
func VerifyOIDCES256Identity(ctx context.Context, in OIDCVerificationInput) (Identity, error) {
	if strings.TrimSpace(in.RawIDToken) == "" || strings.TrimSpace(in.Issuer) == "" ||
		strings.TrimSpace(in.Audience) == "" ||
		strings.TrimSpace(in.JWKSURL) == "" || in.HTTPClient == nil {
		return Identity{}, errors.New("OIDC 身份校验参数不完整")
	}
	if in.RequireNonce && strings.TrimSpace(in.Nonce) == "" {
		return Identity{}, errors.New("OIDC 身份校验缺少必须的 nonce")
	}
	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	claims := &oidcClaims{}
	token, err := jwt.ParseWithClaims(
		in.RawIDToken,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodES256 {
				return nil, fmt.Errorf("OIDC 签名算法不是 ES256")
			}
			kid, _ := token.Header["kid"].(string)
			if strings.TrimSpace(kid) == "" {
				return nil, errors.New("OIDC token 缺少 kid")
			}
			return fetchOIDCES256Key(ctx, in.HTTPClient, in.JWKSURL, kid)
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuer(strings.TrimSpace(in.Issuer)),
		jwt.WithAudience(strings.TrimSpace(in.Audience)),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil || token == nil || !token.Valid {
		return Identity{}, fmt.Errorf("OIDC ID Token 校验失败: %w", err)
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return Identity{}, errors.New("OIDC ID Token 缺少主体")
	}
	if in.RequireNonce && !hmac.Equal([]byte(strings.TrimSpace(claims.Nonce)), []byte(strings.TrimSpace(in.Nonce))) {
		return Identity{}, errors.New("OIDC nonce 不匹配")
	}
	if in.RequireAccountScope && strings.TrimSpace(claims.TeamID) == "" {
		return Identity{}, errors.New("OIDC ID Token 缺少可验证账号范围")
	}
	return FromVerifiedOIDCClaims(claims.TeamID, claims.Subject, claims.Email, in.Source), nil
}

// VerifyES256AccessTokenScope 验证同一发行方签发的访问令牌，并把账号范围绑定到
// 已经由 ID Token 验证过的个人主体。访问令牌来自固定 token 端点，但仍必须验签、
// 校验发行方、时效和主体一致性，不能直接信任未验证的 team_id。
func VerifyES256AccessTokenScope(ctx context.Context, in AccessTokenScopeVerificationInput) (Identity, error) {
	if strings.TrimSpace(in.RawAccessToken) == "" || strings.TrimSpace(in.Issuer) == "" ||
		strings.TrimSpace(in.Audience) == "" || strings.TrimSpace(in.ExpectedSubject) == "" ||
		strings.TrimSpace(in.JWKSURL) == "" || in.HTTPClient == nil {
		return Identity{}, errors.New("访问令牌账号范围校验参数不完整")
	}
	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	claims := &oidcClaims{}
	token, err := jwt.ParseWithClaims(
		in.RawAccessToken,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodES256 {
				return nil, errors.New("访问令牌签名算法不是 ES256")
			}
			kid, _ := token.Header["kid"].(string)
			if strings.TrimSpace(kid) == "" {
				return nil, errors.New("访问令牌缺少 kid")
			}
			return fetchOIDCES256Key(ctx, in.HTTPClient, in.JWKSURL, kid)
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuer(strings.TrimSpace(in.Issuer)),
		jwt.WithAudience(strings.TrimSpace(in.Audience)),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil || token == nil || !token.Valid {
		return Identity{}, fmt.Errorf("访问令牌校验失败: %w", err)
	}
	if !hmac.Equal([]byte(strings.TrimSpace(claims.Subject)), []byte(strings.TrimSpace(in.ExpectedSubject))) {
		return Identity{}, errors.New("访问令牌主体与 ID Token 不一致")
	}
	if strings.TrimSpace(claims.TeamID) == "" {
		return Identity{}, errors.New("访问令牌缺少可验证账号范围")
	}
	return FromVerifiedOIDCClaims(claims.TeamID, claims.Subject, claims.Email, in.Source), nil
}

func fetchOIDCES256Key(ctx context.Context, client *http.Client, rawURL, kid string) (*ecdsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("OIDC JWKS 返回状态 %d", res.StatusCode)
	}
	var set oidcJWKSet
	decoder := json.NewDecoder(io.LimitReader(res.Body, maxJWKSResponseBytes))
	if err := decoder.Decode(&set); err != nil {
		return nil, fmt.Errorf("解析 OIDC JWKS: %w", err)
	}
	for _, key := range set.Keys {
		if strings.TrimSpace(key.Kid) != strings.TrimSpace(kid) {
			continue
		}
		return parseOIDCES256Key(key)
	}
	return nil, errors.New("OIDC JWKS 找不到匹配 kid")
}

func parseOIDCES256Key(key oidcJWK) (*ecdsa.PublicKey, error) {
	if key.Kty != "EC" || key.Crv != "P-256" || (key.Alg != "" && key.Alg != "ES256") ||
		(key.Use != "" && key.Use != "sig") {
		return nil, errors.New("OIDC JWK 类型或用途不符合 ES256 签名合同")
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(key.X)
	if err != nil {
		return nil, fmt.Errorf("解析 OIDC JWK x: %w", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(key.Y)
	if err != nil {
		return nil, fmt.Errorf("解析 OIDC JWK y: %w", err)
	}
	curve := elliptic.P256()
	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)
	if len(xBytes) != 32 || len(yBytes) != 32 || !curve.IsOnCurve(x, y) {
		return nil, errors.New("OIDC JWK 坐标无效")
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}
