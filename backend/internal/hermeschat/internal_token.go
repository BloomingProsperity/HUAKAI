package hermeschat

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	InternalTokenSecretEnv  = "HUAKAI_HERMES_INTERNAL_TOKEN_SECRET"
	InternalTokenTTL        = 5 * time.Minute
	internalTokenVersion    = "v1"
	internalTokenAudience   = "huakai-hermes-internal"
	InternalTokenPurposeMCP = "mcp"
)

var ErrInvalidInternalToken = errors.New("hermeschat: invalid internal token")

// InternalTokenClaims 同时绑定内部服务主体和真实管理员，任意网关副本都能独立验真。
type InternalTokenClaims struct {
	Purpose     string
	TenantID    int64
	UserID      int64
	ActorSource string
	ActorID     int64
	ActorRole   string
	RequestID   string
	IssuedAt    time.Time
	ExpiresAt   time.Time
}

type internalTokenPayload struct {
	Audience    string `json:"aud"`
	Purpose     string `json:"purpose"`
	TenantID    int64  `json:"tenant_id"`
	UserID      int64  `json:"user_id"`
	ActorSource string `json:"actor_source"`
	ActorID     int64  `json:"actor_id"`
	ActorRole   string `json:"actor_role"`
	RequestID   string `json:"request_id"`
	IssuedAt    int64  `json:"iat"`
	ExpiresAt   int64  `json:"exp"`
}

func SignInternalToken(secret []byte, claims InternalTokenClaims) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("%w: secret is required", ErrInvalidInternalToken)
	}
	requestID := strings.TrimSpace(claims.RequestID)
	purpose := strings.TrimSpace(claims.Purpose)
	if purpose == "" {
		purpose = InternalTokenPurposeMCP
	}
	issuedAt := claims.IssuedAt.UTC()
	if claims.IssuedAt.IsZero() {
		issuedAt = claims.ExpiresAt.UTC().Add(-InternalTokenTTL)
	}
	if err := validateInternalTokenValues(purpose, claims.TenantID, claims.UserID, claims.ActorSource, claims.ActorID, claims.ActorRole, requestID, issuedAt, claims.ExpiresAt.UTC()); err != nil {
		return "", err
	}
	payload := internalTokenPayload{
		Audience: internalTokenAudience, Purpose: purpose, TenantID: claims.TenantID, UserID: claims.UserID,
		ActorSource: claims.ActorSource, ActorID: claims.ActorID, ActorRole: claims.ActorRole,
		RequestID: requestID,
		IssuedAt:  issuedAt.Unix(), ExpiresAt: claims.ExpiresAt.UTC().Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("%w: encode claims", ErrInvalidInternalToken)
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	canonical := internalTokenVersion + "." + encoded
	signature := base64.RawURLEncoding.EncodeToString(signInternalCanonical(secret, canonical))
	return canonical + "." + signature, nil
}

func signInternalCanonical(secret []byte, canonical string) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(canonical))
	return mac.Sum(nil)
}

func VerifyInternalToken(token string, secret []byte, now time.Time) (InternalTokenClaims, error) {
	if len(secret) == 0 || len(token) > 4096 {
		return InternalTokenClaims{}, fmt.Errorf("%w: secret or token invalid", ErrInvalidInternalToken)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != internalTokenVersion {
		return InternalTokenClaims{}, ErrInvalidInternalToken
	}
	canonical := parts[0] + "." + parts[1]
	gotSignature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(gotSignature, signInternalCanonical(secret, canonical)) {
		return InternalTokenClaims{}, ErrInvalidInternalToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return InternalTokenClaims{}, ErrInvalidInternalToken
	}
	var payload internalTokenPayload
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Audience != internalTokenAudience {
		return InternalTokenClaims{}, ErrInvalidInternalToken
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	issuedAt := time.Unix(payload.IssuedAt, 0).UTC()
	expiresAt := time.Unix(payload.ExpiresAt, 0).UTC()
	if err := validateInternalTokenValues(payload.Purpose, payload.TenantID, payload.UserID, payload.ActorSource, payload.ActorID, payload.ActorRole, payload.RequestID, issuedAt, expiresAt); err != nil {
		return InternalTokenClaims{}, ErrInvalidInternalToken
	}
	now = now.UTC()
	if issuedAt.After(now.Add(30*time.Second)) || !expiresAt.After(now) {
		return InternalTokenClaims{}, ErrInvalidInternalToken
	}
	return InternalTokenClaims{
		Purpose:  payload.Purpose,
		TenantID: payload.TenantID, UserID: payload.UserID,
		ActorSource: payload.ActorSource, ActorID: payload.ActorID, ActorRole: payload.ActorRole,
		RequestID: payload.RequestID,
		IssuedAt:  issuedAt, ExpiresAt: expiresAt,
	}, nil
}

func validateInternalTokenValues(purpose string, tenantID, userID int64, actorSource string, actorID int64, actorRole, requestID string, issuedAt, expiresAt time.Time) error {
	if purpose != InternalTokenPurposeMCP {
		return fmt.Errorf("%w: token purpose invalid", ErrInvalidInternalToken)
	}
	if tenantID <= 0 || userID <= 0 || actorID <= 0 || strings.TrimSpace(requestID) == "" || issuedAt.IsZero() || expiresAt.IsZero() {
		return fmt.Errorf("%w: required claims missing", ErrInvalidInternalToken)
	}
	if actorSource != "token" && actorSource != "session" {
		return fmt.Errorf("%w: actor source invalid", ErrInvalidInternalToken)
	}
	if actorRole != "platform_admin" && actorRole != "tenant_operator" {
		return fmt.Errorf("%w: actor role invalid", ErrInvalidInternalToken)
	}
	if !expiresAt.After(issuedAt) || expiresAt.Sub(issuedAt) > InternalTokenTTL {
		return fmt.Errorf("%w: token lifetime invalid", ErrInvalidInternalToken)
	}
	return nil
}
