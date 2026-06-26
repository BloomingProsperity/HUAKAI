package credentialacq

import (
	"bytes"
	"testing"

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

func TestCLIImportRejectsEmptyInput(t *testing.T) {
	if _, err := ParseImportContent(" \n\t ", credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth); err != ErrInvalidImportBody {
		t.Fatalf("err=%v want %v", err, ErrInvalidImportBody)
	}
}

// TestCLIImportRejectsMalformedJSONLine 守护这样一类行：明显意图写结构化 JSON
//（以 { 或 [ 开头）但解析失败，必须被拒绝，而不是被静默存成 raw session token ——
// 后者此前会让导入"看似成功"，实则得到不可用的凭据文本。
//
// 变异检查：删掉 jsonLikeLine 分支后，每条畸形行都会被当作 token 接受
//（err==nil）→ "expect ErrInvalidImportBody" 的断言变红。末尾的 raw-token 用例
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
