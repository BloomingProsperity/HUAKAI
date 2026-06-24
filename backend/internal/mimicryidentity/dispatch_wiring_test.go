package mimicryidentity

import (
	"bytes"
	"testing"
)

// TestRewriteForDispatch_默认关字节等价 验证:dispatch 便捷入口在运维开关默认
// 关时返回与入参逐字节相等的 body。
//
// 变异证伪:把 RewriteEnabled 默认当成开 → 改写发生 → 字节不再等价 → 变红。
func TestRewriteForDispatch_默认关字节等价(t *testing.T) {
	t.Setenv(envIdentityRewrite, "")
	t.Setenv(envServerSecret, "fixed-secret")
	body := fixtureBody(t)
	out := RewriteForDispatch(body, 42, testExternalAccountUUID, "", "2.1.78")
	if !bytes.Equal(out, body) {
		t.Fatalf("默认关时 dispatch body 必须字节等价\n原: %s\n出: %s", body, out)
	}
}

// TestRewriteForDispatch_开启有身份改写_自证 验证:开关开启 + 派生密钥就绪 +
// external id 非空时,dispatch 入口真改写 metadata.user_id;对照"密钥缺失"
// fail-open 路径,坐实改写确实由这条链触发。
//
// 变异证伪:让 serverSecret() 永远返回非空(忽略 env)→ 第二段 fail-open 断言
// 变红;或短路改写 → 第一段断言变红。
func TestRewriteForDispatch_开启有身份改写_自证(t *testing.T) {
	body := fixtureBody(t)

	// 路径一:开关开 + 密钥就绪 → 改写发生。
	t.Setenv(envIdentityRewrite, "true")
	t.Setenv(envServerSecret, "fixed-secret")
	rewritten := RewriteForDispatch(body, 42, testExternalAccountUUID, "", "2.1.78")
	if bytes.Equal(rewritten, body) {
		t.Fatalf("开关开 + 密钥就绪 + 非空 external id,本应改写却字节等价")
	}

	// 路径二:同样开关开但派生密钥缺失 → fail-open 字节等价(证明改写依赖密钥)。
	t.Setenv(envServerSecret, "")
	failOpen := RewriteForDispatch(body, 42, testExternalAccountUUID, "", "2.1.78")
	if !bytes.Equal(failOpen, body) {
		t.Fatalf("密钥缺失时必须 fail-open 字节等价\n原: %s\n出: %s", body, failOpen)
	}
}

// TestExtractClaudeCodeVersion 验证从 UA 抽取 CLI 版本;非 Claude Code UA 返回空。
//
// 变异证伪:把正则捕获组删掉 → 抽取失败返回空 → 第一段断言变红。
func TestExtractClaudeCodeVersion(t *testing.T) {
	if got := ExtractClaudeCodeVersion("claude-cli/2.1.78 (external, cli)"); got != "2.1.78" {
		t.Fatalf("应抽出 2.1.78,实际 %q", got)
	}
	if got := ExtractClaudeCodeVersion("claude-cli/1.0"); got != "1.0" {
		t.Fatalf("应抽出 1.0,实际 %q", got)
	}
	if got := ExtractClaudeCodeVersion("curl/8.0"); got != "" {
		t.Fatalf("非 Claude Code UA 应返回空,实际 %q", got)
	}
}
