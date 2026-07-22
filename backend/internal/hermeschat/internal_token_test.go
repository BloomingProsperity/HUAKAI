package hermeschat

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func completeInternalClaims(now time.Time) InternalTokenClaims {
	return InternalTokenClaims{
		Purpose:  InternalTokenPurposeMCP,
		TenantID: 7, UserID: 42,
		ActorSource: "session", ActorID: 99, ActorRole: "tenant_operator",
		RequestID: "req-round-trip", IssuedAt: now, ExpiresAt: now.Add(InternalTokenTTL),
	}
}

func flipLast(value string) string {
	if value == "" {
		return "x"
	}
	replacement := byte('A')
	if value[len(value)-1] == replacement {
		replacement = 'B'
	}
	return value[:len(value)-1] + string(replacement)
}

func TestInternalToken完整声明可独立验真(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	want := completeInternalClaims(now)
	token, err := SignInternalToken([]byte(testInternalSecret), want)
	if err != nil {
		t.Fatalf("签发内部令牌失败：%v", err)
	}
	if !strings.HasPrefix(token, "v1.") || len(strings.Split(token, ".")) != 3 {
		t.Fatalf("令牌格式=%q，不符合版本化三段合同", token)
	}
	got, err := VerifyInternalToken(token, []byte(testInternalSecret), now)
	if err != nil {
		t.Fatalf("验证内部令牌失败：%v", err)
	}
	if got != want {
		t.Fatalf("验证声明=%+v，期望=%+v", got, want)
	}
}

func TestInternalToken拒绝错误密钥和签名篡改(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	token, err := SignInternalToken([]byte(testInternalSecret), completeInternalClaims(now))
	if err != nil {
		t.Fatalf("签发内部令牌失败：%v", err)
	}
	if _, err := VerifyInternalToken(token, []byte("错误但长度足够的内部密钥值"), now); !errors.Is(err, ErrInvalidInternalToken) {
		t.Fatalf("错误密钥验证结果=%v，期望无效令牌", err)
	}
	parts := strings.Split(token, ".")
	forged := parts[0] + "." + parts[1] + "." + flipLast(parts[2])
	if _, err := VerifyInternalToken(forged, []byte(testInternalSecret), now); !errors.Is(err, ErrInvalidInternalToken) {
		t.Fatalf("篡改签名验证结果=%v，期望无效令牌", err)
	}
}

func TestInternalToken拒绝未重新签名的身份篡改(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	token, err := SignInternalToken([]byte(testInternalSecret), completeInternalClaims(now))
	if err != nil {
		t.Fatalf("签发内部令牌失败：%v", err)
	}
	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("解码令牌负载失败：%v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("解析令牌负载失败：%v", err)
	}
	payload["tenant_id"] = float64(8)
	payload["actor_id"] = float64(100)
	raw, _ = json.Marshal(payload)
	forged := parts[0] + "." + base64.RawURLEncoding.EncodeToString(raw) + "." + parts[2]
	if _, err := VerifyInternalToken(forged, []byte(testInternalSecret), now); !errors.Is(err, ErrInvalidInternalToken) {
		t.Fatalf("篡改身份验证结果=%v，期望无效令牌", err)
	}
}

func TestInternalToken严格限制时效来源和角色(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	tests := []struct {
		name   string
		mutate func(*InternalTokenClaims)
	}{
		{"缺少操作者", func(c *InternalTokenClaims) { c.ActorID = 0 }},
		{"非法来源", func(c *InternalTokenClaims) { c.ActorSource = "user" }},
		{"非法角色", func(c *InternalTokenClaims) { c.ActorRole = "user" }},
		{"超过五分钟", func(c *InternalTokenClaims) { c.ExpiresAt = c.IssuedAt.Add(InternalTokenTTL + time.Second) }},
		{"已经过期", func(c *InternalTokenClaims) { c.IssuedAt = now.Add(-time.Minute); c.ExpiresAt = now }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims := completeInternalClaims(now)
			tc.mutate(&claims)
			token, err := SignInternalToken([]byte(testInternalSecret), claims)
			if tc.name == "已经过期" {
				if err != nil {
					t.Fatalf("过期令牌应能签发后由验签阶段拒绝：%v", err)
				}
				if _, err := VerifyInternalToken(token, []byte(testInternalSecret), now); !errors.Is(err, ErrInvalidInternalToken) {
					t.Fatalf("过期令牌验证结果=%v，期望无效令牌", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidInternalToken) {
				t.Fatalf("签发错误=%v，期望无效令牌", err)
			}
		})
	}
}

func TestInternalToken拒绝旧格式(t *testing.T) {
	for _, token := range []string{
		"7|42|req|1700000300|deadbeef",
		"hki_v1.eyJ0ZW5hbnRfaWQiOjd9.deadbeef",
	} {
		if _, err := VerifyInternalToken(token, []byte(testInternalSecret), time.Unix(1700000000, 0).UTC()); !errors.Is(err, ErrInvalidInternalToken) {
			t.Fatalf("旧格式 %q 的验证结果=%v，期望无效令牌", token, err)
		}
	}
}
