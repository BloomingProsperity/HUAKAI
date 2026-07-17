package accountident

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// buildJWT 拼装一个紧凑的 3 段式 JWT，其 payload 以 base64url 编码给定的 claims。
// header/signature 段是惰性占位符（提取器从不验证 signature）。padPayload=false 会
// 剥掉尾部 padding，使该 fixture 触发 padding 还原分支；padPayload=true 则保留标准 padding。
func buildJWT(t *testing.T, claims map[string]any, stripPadding bool) string {
	t.Helper()
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payload := base64.URLEncoding.EncodeToString(raw)
	if stripPadding {
		payload = strings.TrimRight(payload, "=")
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	return header + "." + payload + ".sig"
}

func TestParseJWTClaimsUnverified_DecodesPayload(t *testing.T) {
	// 有区分力的 fixture：剥掉 padding 后，序列化得到的 payload 长度被有意设成
	// 不是 4 的倍数，因此只有 padding 还原分支才能让解码成功。确认该性质成立。
	claims := map[string]any{
		"sub":   "u-123",
		"email": "a@b.com",
		openAIAuthClaimKey: map[string]any{
			"chatgpt_account_id": "acct-XYZ",
		},
	}
	token := buildJWT(t, claims, true /* stripPadding */)
	stripped := strings.Split(token, ".")[1]
	if len(stripped)%4 == 0 {
		t.Fatalf("fixture not discriminating: stripped payload len %d is already a multiple of 4; padding branch would not be exercised", len(stripped))
	}

	got, err := ParseJWTClaimsUnverified(token)
	if err != nil {
		// 变异：删掉 decodeBase64URLSegment 中的 padding 还原分支，会让
		// URLEncoding.DecodeString 拒绝这个段 -> 该断言变红。
		t.Fatalf("ParseJWTClaimsUnverified: unexpected error %v (padding-restore branch likely missing)", err)
	}
	if got["sub"] != "u-123" {
		t.Fatalf("sub = %v, want u-123", got["sub"])
	}
	if got["email"] != "a@b.com" {
		t.Fatalf("email = %v, want a@b.com", got["email"])
	}
	auth, ok := got[openAIAuthClaimKey].(map[string]any)
	if !ok {
		t.Fatalf("auth claim missing or wrong type: %T", got[openAIAuthClaimKey])
	}
	if auth["chatgpt_account_id"] != "acct-XYZ" {
		t.Fatalf("chatgpt_account_id = %v, want acct-XYZ", auth["chatgpt_account_id"])
	}
}

func TestExtractChatGPT_PrefersJWTClaimOverBodyAndSub(t *testing.T) {
	// 三个候选来源各携带不同的值，因此优先级错误的 bug 不可能侥幸通过：
	// 只有读取 JWT auth claim 才会得到 acct-FROM-JWT。
	claims := map[string]any{
		"sub":   "u-sub",
		"email": "jwt@example.com",
		openAIAuthClaimKey: map[string]any{
			"chatgpt_account_id": "acct-FROM-JWT",
		},
	}
	token := buildJWT(t, claims, false)

	id := ExtractChatGPT(token, "acct-FROM-BODY", "user-FROM-BODY")
	if id.AccountID != "acct-FROM-JWT" {
		// 变异：返回 body 值或 sub 而非该 claim -> 变红。
		t.Fatalf("AccountID = %q, want acct-FROM-JWT (claim must win over body and sub)", id.AccountID)
	}
	if id.Source != SourceChatGPTJWTClaim {
		t.Fatalf("Source = %q, want %q", id.Source, SourceChatGPTJWTClaim)
	}
	if id.Email != "jwt@example.com" {
		t.Fatalf("Email = %q, want jwt@example.com", id.Email)
	}
	if id.SubjectID != "u-sub" {
		t.Fatalf("SubjectID = %q, want u-sub", id.SubjectID)
	}
}

func TestExtractChatGPT_FallsBackToBodyThenSub(t *testing.T) {
	// 没有 auth claim -> body 优先于 sub。
	claims := map[string]any{"sub": "u-sub"}
	token := buildJWT(t, claims, false)
	id := ExtractChatGPT(token, "acct-FROM-BODY", "user-FROM-BODY")
	if id.AccountID != "acct-FROM-BODY" {
		t.Fatalf("AccountID = %q, want acct-FROM-BODY (body must win over sub when claim absent)", id.AccountID)
	}
	if id.Source != SourceOpenAITokenBody {
		t.Fatalf("Source = %q, want %q", id.Source, SourceOpenAITokenBody)
	}
	if id.SubjectID != "u-sub" {
		t.Fatalf("SubjectID = %q, want u-sub", id.SubjectID)
	}

	// 没有 auth claim、也没有 body -> sub 作为最后兜底。
	id = ExtractChatGPT(token, "", "")
	if id.AccountID != "u-sub" {
		t.Fatalf("AccountID = %q, want u-sub (sub is last-resort)", id.AccountID)
	}
	if id.Source != SourceChatGPTJWTClaim {
		t.Fatalf("Source = %q, want %q", id.Source, SourceChatGPTJWTClaim)
	}

	// token 没有 subject 时，受信 token response 中的个人主体必须保留。
	withoutSubject := buildJWT(t, map[string]any{}, false)
	id = ExtractChatGPT(withoutSubject, "", "user-FROM-BODY")
	if id.AccountID != "user-FROM-BODY" || id.SubjectID != "user-FROM-BODY" || id.Source != SourceOpenAITokenBody {
		t.Fatalf("body subject fallback = %+v, want account/subject user-FROM-BODY", id)
	}

	// id_token 不可解析也不能丢掉同一 token response 中的已解析身份字段。
	id = ExtractChatGPT("invalid", "acct-FROM-BODY", "user-FROM-BODY")
	if id.AccountID != "acct-FROM-BODY" || id.SubjectID != "user-FROM-BODY" || id.Source != SourceOpenAITokenBody {
		t.Fatalf("invalid token body fallback = %+v", id)
	}
}

func TestExtractAnthropic_UsesAccountUUIDAndEmail(t *testing.T) {
	id := ExtractAnthropic("acc-uuid-1", "acc@x.com", "" /* topEmail empty */)
	if id.AccountID != "acc-uuid-1" {
		// 变异：把 exchanger.go 回退为去掉新增的 uuid 字段，会在此传入 ""
		// -> AccountID 为空 -> 变红。
		t.Fatalf("AccountID = %q, want acc-uuid-1", id.AccountID)
	}
	if id.Email != "acc@x.com" {
		t.Fatalf("Email = %q, want acc@x.com", id.Email)
	}
	if id.Source != SourceAnthropicAccountID {
		t.Fatalf("Source = %q, want %q", id.Source, SourceAnthropicAccountID)
	}
}

func TestExtractAnthropic_EmailPrefersAccountThenTop(t *testing.T) {
	id := ExtractAnthropic("acc-uuid-2", "", "top@x.com")
	if id.Email != "top@x.com" {
		t.Fatalf("Email = %q, want top@x.com (top-level fallback)", id.Email)
	}
}

func TestExtract_FailClosedToManual(t *testing.T) {
	// 格式错误/为空的 id_token 绝不能中止获取流程：每个提取器都返回一个空的
	// manual Identity（无 AccountID），而 ParseJWTClaimsUnverified 自身会报告 error，
	// 使测试中的契约显式可见。
	for _, bad := range []string{"", "not.a.jwt.x", "onlyonesegment", "two.segments"} {
		if _, err := ParseJWTClaimsUnverified(bad); err == nil {
			t.Fatalf("ParseJWTClaimsUnverified(%q): expected error", bad)
		}

		// ChatGPT 且无 body 回退 -> manual。
		if id := ExtractChatGPT(bad, "", ""); id.Source != SourceManual || id.AccountID != "" {
			// 变异：若提取器在此处把解码 error 传播出去，或返回了一个
			// 非 manual 的 identity，则此处变红。
			t.Fatalf("ExtractChatGPT(%q): got %+v, want empty manual identity", bad, id)
		}

		// Gemini -> manual。
		if id := ExtractGemini(bad, ""); id.Source != SourceManual || id.AccountID != "" {
			t.Fatalf("ExtractGemini(%q): got %+v, want empty manual identity", bad, id)
		}
	}

	// Anthropic 且 uuid 为空 -> manual（不存在 error 类型；只是空结果）。
	if id := ExtractAnthropic("", "any@x.com", ""); id.Source != SourceManual || id.AccountID != "" {
		t.Fatalf("ExtractAnthropic empty uuid: got %+v, want empty manual identity", id)
	}
}

func TestExtractGemini_UsesSubAndEmail(t *testing.T) {
	claims := map[string]any{"sub": "g-sub-1", "email": "g@x.com"}
	token := buildJWT(t, claims, false)
	id := ExtractGemini(token, "userinfo@x.com")
	if id.AccountID != "g-sub-1" {
		t.Fatalf("AccountID = %q, want g-sub-1", id.AccountID)
	}
	if id.SubjectID != "g-sub-1" {
		t.Fatalf("SubjectID = %q, want g-sub-1", id.SubjectID)
	}
	if id.Email != "g@x.com" {
		t.Fatalf("Email = %q, want g@x.com (email claim wins over userinfo fallback)", id.Email)
	}
	if id.Source != SourceGoogleIDTokenSub {
		t.Fatalf("Source = %q, want %q", id.Source, SourceGoogleIDTokenSub)
	}

	// 没有 email claim -> 回退到 userinfo。
	token2 := buildJWT(t, map[string]any{"sub": "g-sub-2"}, false)
	id2 := ExtractGemini(token2, "userinfo@x.com")
	if id2.Email != "userinfo@x.com" {
		t.Fatalf("Email = %q, want userinfo@x.com (fallback)", id2.Email)
	}
}

func TestIdentityEmptyIncludesEmail(t *testing.T) {
	if !(Identity{}).Empty() {
		t.Fatal("零值 identity 应为空")
	}
	for _, identity := range []Identity{
		{AccountID: "account-1"},
		{SubjectID: "subject-1"},
		{Email: "person@example.com"},
	} {
		if identity.Empty() {
			t.Fatalf("identity=%+v 携带标识时不应为空", identity)
		}
	}
}
