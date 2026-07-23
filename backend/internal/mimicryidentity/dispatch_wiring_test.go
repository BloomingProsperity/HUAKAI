package mimicryidentity

import (
	"bytes"
	"testing"
)

// TestRewriteForDispatch_默认开即改写 验证:dispatch 便捷入口在运维开关【默认开】
// (未配置)+ 反转号 + 密钥就绪 + external id 非空时真改写;仅显式 "false" 才关。
//
// 变异证伪:把 RewriteEnabled 默认退回关 → 未配置时不改写 → 第一段断言变红。
func TestRewriteForDispatch_默认开即改写(t *testing.T) {
	t.Setenv(envServerSecret, "fixed-secret")
	body := fixtureBody(t)
	// 未配置 → 默认开;反转号 oauth。
	t.Setenv(envIdentityRewrite, "")
	out := RewriteForDispatch(body, 42, testExternalAccountUUID, "oauth", "", "2.1.78")
	if bytes.Equal(out, body) {
		t.Fatalf("默认开 + 反转号 + 密钥就绪,本应改写却字节等价")
	}
	// 显式 "false" → 关 → 字节等价。
	t.Setenv(envIdentityRewrite, "false")
	off := RewriteForDispatch(body, 42, testExternalAccountUUID, "oauth", "", "2.1.78")
	if !bytes.Equal(off, body) {
		t.Fatalf("显式 false 时 dispatch body 必须字节等价\n原: %s\n出: %s", body, off)
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
	rewritten := RewriteForDispatch(body, 42, testExternalAccountUUID, "oauth", "", "2.1.78")
	if bytes.Equal(rewritten, body) {
		t.Fatalf("开关开 + 密钥就绪 + 非空 external id,本应改写却字节等价")
	}

	// 路径二:同样开关开但派生密钥缺失 → fail-open 字节等价(证明改写依赖密钥)。
	t.Setenv(envServerSecret, "")
	failOpen := RewriteForDispatch(body, 42, testExternalAccountUUID, "oauth", "", "2.1.78")
	if !bytes.Equal(failOpen, body) {
		t.Fatalf("密钥缺失时必须 fail-open 字节等价\n原: %s\n出: %s", body, failOpen)
	}
}

// TestRewriteForDispatch_scope_apikey不伪装 验证经 dispatch 入口时 scope 硬守卫也生效:
// apikey 号即便开关开 + 密钥就绪也不伪装(I1),oauth 反转号则改写。
//
// 变异证伪:删去 isReverseAccountType 守卫 → apikey 经 dispatch 也被改写 → 变红。
func TestRewriteForDispatch_scope_apikey不伪装(t *testing.T) {
	t.Setenv(envIdentityRewrite, "true")
	t.Setenv(envServerSecret, "fixed-secret")
	body := fixtureBody(t)
	apikeyOut := RewriteForDispatch(body, 42, testExternalAccountUUID, "apikey", "", "2.1.78")
	if !bytes.Equal(apikeyOut, body) {
		t.Fatalf("apikey 号经 dispatch 必须不伪装字节等价\n原: %s\n出: %s", body, apikeyOut)
	}
	oauthOut := RewriteForDispatch(body, 42, testExternalAccountUUID, "oauth", "", "2.1.78")
	if bytes.Equal(oauthOut, body) {
		t.Fatalf("oauth 反转号经 dispatch 本应改写,却字节等价")
	}
}
