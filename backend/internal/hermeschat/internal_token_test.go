package hermeschat

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestInternalTokenMatchesRunnerPipeFormat(t *testing.T) {
	// 守护的回归:gateway 签发的 token 必须能被 runner 的 pipe-format HMAC 验证器接受。
	now := time.Unix(1700000000, 0).UTC()
	token, err := SignInternalToken([]byte(testInternalSecret), InternalTokenClaims{
		TenantID: 7, UserID: 42, RequestID: "req-runner-format",
		ExpiresAt: now.Add(InternalTokenTTL),
	})
	if err != nil {
		t.Fatalf("SignInternalToken: %v", err)
	}
	if strings.HasPrefix(token, "hki_v1.") {
		t.Fatalf("token=%q uses legacy hki_v1 base64 format; runner requires pipe format", token)
	}

	claims, err := verifyLikeRunner(token, []byte(testInternalSecret), 7, 42, now)
	if err != nil {
		t.Fatalf("runner verifier rejected gateway token: %v", err)
	}
	if claims.RequestID != "req-runner-format" || !claims.ExpiresAt.Equal(now.Add(InternalTokenTTL)) {
		t.Fatalf("claims=%+v want request req-runner-format exp %s", claims, now.Add(InternalTokenTTL))
	}
}

func TestInternalTokenRejectsLegacyBase64Format(t *testing.T) {
	// 变异检查:若 gateway 签名退回到 hki_v1.<base64-json>.<base64-hmac>,验证必须变红。
	legacy := "hki_v1.eyJ0ZW5hbnRfaWQiOjcsInVzZXJfaWQiOjQyLCJyZXF1ZXN0X2lkIjoicmVxIiwiZXhwIjoxNzAwMDAwMzAwfQ.deadbeef"
	if _, err := VerifyInternalToken(legacy, []byte(testInternalSecret), time.Unix(1700000000, 0).UTC()); err == nil {
		t.Fatalf("VerifyInternalToken accepted legacy hki_v1 token")
	}
}

func verifyLikeRunner(token string, secret []byte, tenantID, userID int64, now time.Time) (InternalTokenClaims, error) {
	parts := strings.Split(token, "|")
	if len(parts) != 5 {
		return InternalTokenClaims{}, fmt.Errorf("part count=%d want 5", len(parts))
	}
	tokenTenant, tokenUser, requestID, expRaw, signature := parts[0], parts[1], parts[2], parts[3], parts[4]
	if tokenTenant != strconv.FormatInt(tenantID, 10) || tokenUser != strconv.FormatInt(userID, 10) {
		return InternalTokenClaims{}, fmt.Errorf("tenant/user=%s/%s want %d/%d", tokenTenant, tokenUser, tenantID, userID)
	}
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(signature) == "" {
		return InternalTokenClaims{}, fmt.Errorf("request id and signature are required")
	}
	exp, err := strconv.ParseInt(expRaw, 10, 64)
	if err != nil {
		return InternalTokenClaims{}, fmt.Errorf("exp parse: %w", err)
	}
	current := now.UTC().Unix()
	if exp < current || exp > current+int64(InternalTokenTTL/time.Second) {
		return InternalTokenClaims{}, fmt.Errorf("exp=%d outside runner ttl window at %d", exp, current)
	}
	canonical := strings.Join(parts[:4], "|")
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(canonical))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return InternalTokenClaims{}, fmt.Errorf("signature mismatch")
	}
	return InternalTokenClaims{
		TenantID: tenantID, UserID: userID, RequestID: requestID,
		ExpiresAt: time.Unix(exp, 0).UTC(),
	}, nil
}
