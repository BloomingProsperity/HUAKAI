package oauthpendinghttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// 守护「社交登录补邮箱」流程的安全不变量(纯函数,§14 可变异证红):
//  ① pending/challenge token 被篡改/过期/换密钥/串类型 → 一律拒;
//  ② 码指纹绑定:同码同指纹、异码异指纹(防爆破的判别核心);
//  ③ 派生密钥域分隔 + 空会话密钥停用。

func testKey() []byte { return DeriveKey([]byte("test-session-signing-key-0123456789abcdef")) }

func fixedNow() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

func sampleIdentity() userauth.VerifiedIdentity {
	return userauth.VerifiedIdentity{Provider: "github", Subject: "sub-123", DisplayName: "Alice"}
}

func TestPendingTokenRoundTrip(t *testing.T) {
	key := testKey()
	tok, err := MintPendingToken(key, sampleIdentity(), 7, fixedNow())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	claims, err := verifyPendingToken(key, tok, fixedNow())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.TenantID != 7 || claims.Provider != "github" || claims.Subject != "sub-123" || claims.DisplayName != "Alice" {
		t.Fatalf("claims mismatch: %+v", claims)
	}
}

func TestPendingTokenTamperedRejected(t *testing.T) {
	key := testKey()
	tok, _ := MintPendingToken(key, sampleIdentity(), 7, fixedNow())
	b := []byte(tok)
	if b[len(b)-1] == 'A' {
		b[len(b)-1] = 'B'
	} else {
		b[len(b)-1] = 'A'
	}
	if _, err := verifyPendingToken(key, string(b), fixedNow()); err == nil {
		t.Fatal("被篡改的 pending token 必须被拒")
	}
}

func TestPendingTokenExpiredRejected(t *testing.T) {
	key := testKey()
	tok, _ := MintPendingToken(key, sampleIdentity(), 7, fixedNow())
	later := fixedNow().Add(pendingTokenTTL + time.Second)
	if _, err := verifyPendingToken(key, tok, later); err == nil {
		t.Fatal("过期 pending token 必须被拒")
	}
}

func TestPendingTokenWrongKeyRejected(t *testing.T) {
	tok, _ := MintPendingToken(testKey(), sampleIdentity(), 7, fixedNow())
	otherKey := DeriveKey([]byte("a-totally-different-session-key-xxxxxxxxxx"))
	if _, err := verifyPendingToken(otherKey, tok, fixedNow()); err == nil {
		t.Fatal("换密钥后 pending token 必须被拒")
	}
}

func TestTokenKindNotConfused(t *testing.T) {
	key := testKey()
	claims, _ := verifyPendingToken(key, mustPending(t, key), fixedNow())
	chal, _ := mintChallengeToken(key, claims, "a@b.com", "ABCDEFGH", fixedNow())
	if _, err := verifyPendingToken(key, chal, fixedNow()); err == nil {
		t.Fatal("challenge token 不得作为 pending token 通过(kind 校验)")
	}
	if _, err := verifyChallengeToken(key, mustPending(t, key), fixedNow()); err == nil {
		t.Fatal("pending token 不得作为 challenge token 通过(kind 校验)")
	}
}

func mustPending(t *testing.T, key []byte) string {
	t.Helper()
	tok, err := MintPendingToken(key, sampleIdentity(), 7, fixedNow())
	if err != nil {
		t.Fatalf("mint pending: %v", err)
	}
	return tok
}

// TestCodeBindingDiscriminates 是防爆破的判别核心:同码 → 指纹相等;异码 → 指纹不等。
// 变异:若 codeBinding 不把 code 纳入 HMAC → 异码也相等 → 本断言 RED。
func TestCodeBindingDiscriminates(t *testing.T) {
	key := testKey()
	right := codeBinding(key, 7, "github", "sub-123", "a@b.com", "ABCDEFGH")
	same := codeBinding(key, 7, "github", "sub-123", "a@b.com", "abcdefgh") // 规整后同码
	if right != same {
		t.Fatalf("同码(规整后)指纹应相等:%q vs %q", right, same)
	}
	wrong := codeBinding(key, 7, "github", "sub-123", "a@b.com", "WRONGXYZ")
	if right == wrong {
		t.Fatal("异码指纹必须不同(否则任意码都能过 = 爆破/绕过)")
	}
	otherEmail := codeBinding(key, 7, "github", "sub-123", "c@d.com", "ABCDEFGH")
	if right == otherEmail {
		t.Fatal("换邮箱后码指纹必须不同(码绑定到具体邮箱)")
	}
}

func TestChallengeRoundTripAndBinding(t *testing.T) {
	key := testKey()
	claims, _ := verifyPendingToken(key, mustPending(t, key), fixedNow())
	chal, err := mintChallengeToken(key, claims, "User@Example.com ", "ABCD-EFGH", fixedNow())
	if err != nil {
		t.Fatalf("mint challenge: %v", err)
	}
	cc, err := verifyChallengeToken(key, chal, fixedNow())
	if err != nil {
		t.Fatalf("verify challenge: %v", err)
	}
	if cc.Email != "user@example.com" {
		t.Fatalf("邮箱应被规整存储,得 %q", cc.Email)
	}
	if codeBinding(key, cc.TenantID, cc.Provider, cc.Subject, cc.Email, "abcd efgh") != cc.CodeBinding {
		t.Fatal("正确码(规整后)应命中 challenge 绑定")
	}
	if codeBinding(key, cc.TenantID, cc.Provider, cc.Subject, cc.Email, "ZZZZZZZZ") == cc.CodeBinding {
		t.Fatal("错误码不得命中 challenge 绑定")
	}
}

// TestSendCodeFailsClosedWithoutEmailSender 守护:邮件通道未配(EmailSender 为 nil)时,发码端点
// 必须 fail-closed 返 503,绝不签发用户永远收不到码的 challenge_token(把用户引入死等)。
// 变异:若入口守卫去掉 `d.EmailSender == nil` 判断 → 持合法 pending_token 的请求走到发码分支、
// 静默跳过投递并返 200 + challenge_token → 本断言(期望 503)RED。
func TestSendCodeFailsClosedWithoutEmailSender(t *testing.T) {
	key := testKey()
	// Now 固定到 fixedNow,使 mustPending 签发的 token 在处理时仍有效:
	// 这样「去掉守卫」的变异体会真的走到发码分支(否则 token 过期先被拒,掩盖缺陷)。
	auth := &userauth.Service{Now: func() time.Time { return fixedNow() }}
	d := Deps{Auth: auth, Key: key} // EmailSender 故意留 nil
	body, _ := json.Marshal(sendCodeRequest{PendingToken: mustPending(t, key), Email: "a@b.com"})
	req := httptest.NewRequest(http.MethodPost, "/oauth-pending/send-code", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	newSendCodeHandler(d)(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("EmailSender 缺失时应 fail-closed 503,得 %d(body=%s)", rec.Code, rec.Body.String())
	}
}

// TestCodeBindingNoDelimiterCollision 守护长度前缀编码消除分隔符碰撞:两组仅在 subject/email 边界
// 不同、但旧「|」拼接会折叠成同一字符串的输入,其码指纹必须不同。
// 变异:若 codeBinding 退回 strings.Join(...,"|") 拼接 → 下面两指纹相等 → RED。
func TestCodeBindingNoDelimiterCollision(t *testing.T) {
	key := testKey()
	// 旧拼接: 两者都 = "7|github|a|b|c@d.com|CODE1234"(subject 尾的 "|b" 与 email 头的 "b|" 挪移碰撞)。
	a := codeBinding(key, 7, "github", "a", "b|c@d.com", "CODE1234")
	b := codeBinding(key, 7, "github", "a|b", "c@d.com", "CODE1234")
	if a == b {
		t.Fatal("subject/email 边界不同的两输入,码指纹必须不同(否则分隔符碰撞可跨字段挪移绕过绑定)")
	}
}

func TestGenerateCode(t *testing.T) {
	a, err := generateCode()
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if len(a) != 8 {
		t.Fatalf("码应为 8 位 base32,得 %d 位:%q", len(a), a)
	}
	if b, _ := generateCode(); a == b {
		t.Fatalf("两次生成的码不应相同(随机性):%q", a)
	}
}

func TestDeriveKey(t *testing.T) {
	if DeriveKey(nil) != nil || DeriveKey([]byte{}) != nil {
		t.Fatal("空会话密钥应派生出 nil(补邮箱流程停用)")
	}
	k1 := DeriveKey([]byte("session-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	k2 := DeriveKey([]byte("session-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	if len(k1) != 32 || string(k1) != string(k2) {
		t.Fatal("同会话密钥应派生出稳定的 32 字节密钥")
	}
	if string(k1) == "session-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatal("派生密钥不应等于原会话密钥(须域分隔)")
	}
}

func TestLooksLikeEmail(t *testing.T) {
	for _, ok := range []string{"a@b.com", "user.name@sub.example.co"} {
		if !looksLikeEmail(ok) {
			t.Fatalf("%q 应判定为合法邮箱形态", ok)
		}
	}
	for _, bad := range []string{"", "nope", "@b.com", "a@", "a@b", "a b@c.com", "a@@b.com"} {
		if looksLikeEmail(bad) {
			t.Fatalf("%q 应判定为非法邮箱形态", bad)
		}
	}
}
