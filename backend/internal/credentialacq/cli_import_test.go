package credentialacq

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestCLIImportParsesMultipleShapes(t *testing.T) {
	cases := []struct {
		name  string
		input string
		count int
		shape string
	}{
		{
			name:  "json object",
			input: `{"vendor":"openai","auth_mode":"codex_cli_oauth","session_token":"session-a"}`,
			count: 1,
			shape: "json_object",
		},
		{
			name:  "json array",
			input: `[{"vendor":"openai","auth_mode":"codex_cli_oauth","session_token":"session-a"},{"vendor":"anthropic","auth_mode":"claude_code","session_token":"session-b"}]`,
			count: 2,
			shape: "json_object",
		},
		{
			name:  "json lines",
			input: "{\"vendor\":\"openai\",\"auth_mode\":\"codex_cli_oauth\",\"session_token\":\"session-a\"}\n{\"vendor\":\"anthropic\",\"auth_mode\":\"claude_code\",\"session_token\":\"session-b\"}",
			count: 2,
			shape: "json_object",
		},
		{
			name:  "single token",
			input: "session-single-value",
			count: 1,
			shape: "single_token",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseImportContent(tc.input, credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.count {
				t.Fatalf("count=%d want %d", len(got), tc.count)
			}
			if got[0].RedactedContext["shape"] != tc.shape {
				t.Fatalf("shape=%v want %s", got[0].RedactedContext["shape"], tc.shape)
			}
			if !bytes.Contains(got[0].Payload, []byte("session")) {
				t.Fatalf("payload does not carry parsed session material: %s", got[0].Payload)
			}
		})
	}
}

// TestCLIImportFlattensAntigravityConsumerToken 守住真实 CLI 文件的嵌套 token
// 形态：access/refresh/token_type 必须进入顶层，expiry 必须改名 expires_at，
// 并能被现有 credentialstore handler 直接物化。若仍按扁平输入读取，
// access_token/refresh_token 都为空，本测试会红。
func TestCLIImportFlattensAntigravityConsumerToken(t *testing.T) {
	input := `{
		"auth_method":"consumer",
		"token":{
			"access_token":"ya29.redacted-access",
			"token_type":"Bearer",
			"refresh_token":"1//redacted-refresh",
			"expiry":"2099-07-11T12:34:56Z"
		}
	}`
	candidates, err := ParseImportContent(input, credentialstore.VendorGemini, credentialstore.AuthModeAntigravity)
	if err != nil {
		t.Fatalf("ParseImportContent 失败：%v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate 数量=%d，期望 1", len(candidates))
	}
	candidate := candidates[0]
	if candidate.Vendor != credentialstore.VendorGemini || candidate.AuthMode != credentialstore.AuthModeAntigravity {
		t.Fatalf("candidate vendor/mode=(%q,%q)", candidate.Vendor, candidate.AuthMode)
	}
	var payload map[string]any
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatalf("解析 payload 失败：%v", err)
	}
	if payload["access_token"] != "ya29.redacted-access" || payload["refresh_token"] != "1//redacted-refresh" {
		t.Fatalf("token 未扁平化：%s", candidate.Payload)
	}
	if payload["token_type"] != "Bearer" || payload["expires_at"] != "2099-07-11T12:34:56Z" {
		t.Fatalf("token_type/expiry 映射错误：%s", candidate.Payload)
	}
	if _, exists := payload["token"]; exists {
		t.Fatalf("payload 不应残留嵌套 token：%s", candidate.Payload)
	}
	if _, exists := payload["expiry"]; exists {
		t.Fatalf("payload 不应残留旧 expiry 字段：%s", candidate.Payload)
	}
	if _, err := time.Parse(time.RFC3339, payload["expires_at"].(string)); err != nil {
		t.Fatalf("expires_at 不是 RFC3339：%v", err)
	}

	handler, err := credentialstore.DefaultHandlerRegistry().MustLookup(candidate.Vendor, candidate.AuthMode)
	if err != nil {
		t.Fatalf("查找 credentialstore handler 失败：%v", err)
	}
	if err := handler.ValidatePayload(candidate.Payload); err != nil {
		t.Fatalf("扁平 payload 无法进入 credentialstore：%v", err)
	}
	runtimeMaterial, err := handler.RuntimeMaterial(candidate.Payload)
	if err != nil {
		t.Fatalf("扁平 payload 无法物化：%v", err)
	}
	if runtimeMaterial.Kind != credentialstore.RuntimeSessionToken || runtimeMaterial.Value != "ya29.redacted-access" {
		t.Fatalf("runtime material=(%q,%q)", runtimeMaterial.Kind, runtimeMaterial.Value)
	}
	if runtimeMaterial.Extra["expires_at"] != "2099-07-11T12:34:56Z" {
		t.Fatalf("runtime expires_at=%q", runtimeMaterial.Extra["expires_at"])
	}
}

func TestCLIImportRejectsEmptyInput(t *testing.T) {
	if _, err := ParseImportContent(" \n\t ", credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth); err != ErrInvalidImportBody {
		t.Fatalf("err=%v want %v", err, ErrInvalidImportBody)
	}
}

// TestCLIImportRejectsMalformedJSONLine 守护这样一类行：明显意图写结构化 JSON
// （以 { 或 [ 开头）但解析失败，必须被拒绝，而不是被静默存成 raw session token ——
// 后者此前会让导入"看似成功"，实则得到不可用的凭据文本。
//
// 变异检查：删掉 jsonLikeLine 分支后，每条畸形行都会被当作 token 接受
// （err==nil）→ "expect ErrInvalidImportBody" 的断言变红。末尾的 raw-token 用例
// 证明我们没有让合法的非 JSON token 导入发生回归。
func TestCLIImportRejectsMalformedJSONLine(t *testing.T) {
	malformed := []string{
		`{"session_token":"abc"`,    // 缺少右花括号
		`[{"session_token":"abc"}`,  // 缺少右方括号
		`{"vendor":"openai", oops}`, // 未加引号的垃圾内容
	}
	for _, in := range malformed {
		if _, err := ParseImportContent(in, credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth); err != ErrInvalidImportBody {
			t.Fatalf("malformed JSON-like line %q must be rejected; got err=%v", in, err)
		}
	}
	got, err := ParseImportContent("session-raw-value-xyz", credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
	if err != nil || len(got) != 1 || got[0].RedactedContext["shape"] != "single_token" {
		t.Fatalf("non-JSON raw token must still import as single_token; got=%d err=%v", len(got), err)
	}
}

func TestCLIImportAttachesIdentityWithoutPuttingSubjectInAuditContext(t *testing.T) {
	candidates, err := ParseImportContent(`{
		"vendor":"openai",
		"auth_mode":"codex_cli_oauth",
		"account_id":"workspace-import",
		"chatgpt_user_id":"subject-import",
		"email":"import@example.test",
		"access_token":"access-import",
		"refresh_token":"refresh-import"
	}`, credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("ParseImportContent candidates=%d err=%v", len(candidates), err)
	}
	got := candidates[0]
	if got.ExternalAccountID != "workspace-import" || got.ExternalSubjectID != "subject-import" ||
		got.ExternalAccountEmail != "import@example.test" || got.AccountIDSource != accountident.SourceImportPayload {
		t.Fatalf("candidate identity=%+v", got)
	}
	redacted, err := json.Marshal(got.RedactedContext)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if _, exists := got.RedactedContext["upstream_subject_id"]; exists || strings.Contains(string(redacted), "subject-import") {
		t.Fatalf("RedactedContext 泄漏个人 subject: %v", got.RedactedContext)
	}
}
