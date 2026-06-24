package gatewayhttp

// 本测试守 R7 身份改写在 dispatch 调用点的【闭环点亮】:chatExecution.identityRewrite
// 现在把 ex.accInfo.ExternalAccountID 喂进 mimicryidentity(而非 PR#115 的硬编 "")。
// 这是直击"双重 inert"原状的闭环守卫 —— 把实参改回喂空(重现原 inert)即让
// 闭环测试变红。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// identityRewriteFixtureBody 构造含 metadata.user_id 的请求 body,account_uuid
// 故意填一个与下游池账号不同的占位值,改写后该组件必须变成池账号的 external id。
func identityRewriteFixtureBody(t *testing.T) []byte {
	t.Helper()
	const clientUserID = `{"device_id":"clientdevice000000000000000000000000000000000000000000000000000000","account_uuid":"00000000-0000-0000-0000-000000000000","session_id":"99999999-8888-7777-6666-555555555555"}`
	uidJSON, err := json.Marshal(clientUserID)
	if err != nil {
		t.Fatalf("marshal user_id 失败: %v", err)
	}
	root := map[string]json.RawMessage{
		"model":    json.RawMessage(`"claude-3-5-sonnet"`),
		"messages": json.RawMessage(`[{"role":"user","content":"hi"}]`),
		"metadata": json.RawMessage(`{"user_id":` + string(uidJSON) + `}`),
	}
	b, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal body 失败: %v", err)
	}
	return b
}

// extractMetadataUserID 取 body.metadata.user_id;再从中解出 account_uuid 组件。
func extractMetadataAccountUUID(t *testing.T, body []byte) string {
	t.Helper()
	var outer struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &outer); err != nil {
		t.Fatalf("解析 metadata.user_id 失败: %v", err)
	}
	var inner struct {
		AccountUUID string `json:"account_uuid"`
	}
	if err := json.Unmarshal([]byte(outer.Metadata.UserID), &inner); err != nil {
		t.Fatalf("解析 user_id JSON 失败: %v (user_id=%q)", err, outer.Metadata.UserID)
	}
	return inner.AccountUUID
}

// newIdentityRewriteExec 构造一个最小 chatExecution,带 Claude Code UA + 指定
// AccountInfo,供直接调用 identityRewrite。
func newIdentityRewriteExec(externalAccountID string) *chatExecution {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(""))
	req.Header.Set("User-Agent", "claude-cli/2.1.78 (external, cli)")
	return &chatExecution{
		r: req,
		accInfo: provider.AccountInfo{
			AccountID:         42,
			Platform:          "anthropic",
			AccountType:       "apikey",
			ExternalAccountID: externalAccountID,
		},
	}
}

// TestIdentityRewrite_闭环点亮_用上游id改写 验证:operator 开关开 + secret 设 +
// 账号带 ExternalAccountID 时,经 identityRewrite(dispatch 调用点)后
// metadata.user_id 的 account_uuid 组件被改写成该上游 id(≠ 客户端原值)。
//
// 变异证伪(直击双重 inert):把 chat_completions_stream.go 的 identityRewrite
// 实参从 ex.accInfo.ExternalAccountID 改回硬编 "" → external id 喂空 → 改写退回
// fail-open(account_uuid 不变,仍是客户端原占位值)→ 下面"被改写"断言变红。
func TestIdentityRewrite_闭环点亮_用上游id改写(t *testing.T) {
	const externalID = "acc-xyz"
	const clientOriginal = "00000000-0000-0000-0000-000000000000"

	// operator opt-in:显式开 switch + 设 secret。
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_REWRITE", "true")
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_SECRET", "fixed-secret-for-test")

	body := identityRewriteFixtureBody(t)
	ex := newIdentityRewriteExec(externalID)

	out := ex.identityRewrite(body)
	got := extractMetadataAccountUUID(t, out)

	if got == clientOriginal {
		t.Fatalf("闭环未点亮:account_uuid 仍是客户端原占位值 %q —— dispatch 调用点疑似仍喂空 external id", got)
	}
	if got != externalID {
		t.Fatalf("account_uuid 应被改写成上游 external id %q,实际 %q", externalID, got)
	}
}

// TestIdentityRewrite_failopen_空上游id_不改写 验证:开关开 + secret 设,但账号
// 无 ExternalAccountID(空)时,fail-open 不改写 —— 请求体逐字节等价(account /
// device / session 组件都不动)。
//
// 变异证伪:在 mimicryidentity 删去 ExternalAccountID=="" 短路(空也强行改写)→
// device_id / session_id 会被派生值改写(account_uuid 因夹具原值恰为零 UUID 不变,
// 故必须比整体字节而非仅 account 组件,才能 discriminating 地捕获该变异)→
// 下面字节等价断言变红。
func TestIdentityRewrite_failopen_空上游id_不改写(t *testing.T) {
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_REWRITE", "true")
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_SECRET", "fixed-secret-for-test")

	body := identityRewriteFixtureBody(t)
	ex := newIdentityRewriteExec("") // 账号无上游 id

	out := ex.identityRewrite(body)
	if string(out) != string(body) {
		t.Fatalf("空 external id 应 fail-open 字节等价(device/session 也不得改写)\n原: %s\n出: %s", body, out)
	}
}

// TestIdentityRewrite_默认关_零变更 验证:运维开关默认关(不设 env)时,
// identityRewrite 返回与入参逐字节等价的 body(PR#115 已有此性质,确认穿线后仍绿)。
//
// 变异证伪:把开关默认当成开 → 改写发生 → account_uuid 变化 → 字节不再等价 → 红。
func TestIdentityRewrite_默认关_零变更(t *testing.T) {
	// 显式清空,防环境污染;默认关。
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_REWRITE", "")
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_SECRET", "fixed-secret-for-test")

	body := identityRewriteFixtureBody(t)
	ex := newIdentityRewriteExec("acc-xyz")

	out := ex.identityRewrite(body)
	if string(out) != string(body) {
		t.Fatalf("默认关时必须字节等价\n原: %s\n出: %s", body, out)
	}
}
