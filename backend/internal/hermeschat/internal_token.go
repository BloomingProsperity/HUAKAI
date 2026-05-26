package hermeschat

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	InternalTokenSecretEnv = "HUAKAI_HERMES_INTERNAL_TOKEN_SECRET"
	InternalBaseURLEnv     = "HUAKAI_HERMES_INTERNAL_BASE_URL"
	DefaultInternalBaseURL = "http://127.0.0.1:8080/internal/v1/openai"
	InternalTokenTTL       = 5 * time.Minute
)

var ErrInvalidInternalToken = errors.New("hermeschat: invalid internal token")

type InternalTokenClaims struct {
	TenantID  int64
	UserID    int64
	RequestID string
	ExpiresAt time.Time
}

func SignInternalToken(secret []byte, claims InternalTokenClaims) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("%w: secret is required", ErrInvalidInternalToken)
	}
	requestID := strings.TrimSpace(claims.RequestID)
	if claims.TenantID <= 0 || claims.UserID <= 0 || requestID == "" || strings.Contains(requestID, "|") || claims.ExpiresAt.IsZero() {
		return "", fmt.Errorf("%w: required claims missing", ErrInvalidInternalToken)
	}
	canonical := strings.Join([]string{
		strconv.FormatInt(claims.TenantID, 10),
		strconv.FormatInt(claims.UserID, 10),
		requestID,
		strconv.FormatInt(claims.ExpiresAt.UTC().Unix(), 10),
	}, "|")
	return canonical + "|" + signInternalCanonical(secret, canonical), nil
}

func signInternalCanonical(secret []byte, canonical string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyInternalToken(token string, secret []byte, now time.Time) (InternalTokenClaims, error) {
	if len(secret) == 0 {
		return InternalTokenClaims{}, fmt.Errorf("%w: secret is required", ErrInvalidInternalToken)
	}
	parts := strings.Split(token, "|")
	if len(parts) != 5 {
		return InternalTokenClaims{}, ErrInvalidInternalToken
	}
	tenantID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return InternalTokenClaims{}, ErrInvalidInternalToken
	}
	userID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return InternalTokenClaims{}, ErrInvalidInternalToken
	}
	requestID := parts[2]
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return InternalTokenClaims{}, ErrInvalidInternalToken
	}
	signature := parts[4]
	canonical := strings.Join(parts[:4], "|")
	if !hmac.Equal([]byte(signature), []byte(signInternalCanonical(secret, canonical))) {
		return InternalTokenClaims{}, ErrInvalidInternalToken
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	current := now.UTC().Unix()
	expiresAt := time.Unix(exp, 0).UTC()
	if tenantID <= 0 || userID <= 0 || strings.TrimSpace(requestID) == "" || strings.TrimSpace(signature) == "" ||
		exp < current || exp > current+int64(InternalTokenTTL/time.Second) {
		return InternalTokenClaims{}, ErrInvalidInternalToken
	}
	return InternalTokenClaims{
		TenantID: tenantID, UserID: userID,
		RequestID: requestID, ExpiresAt: expiresAt,
	}, nil
}
