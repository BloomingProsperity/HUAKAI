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
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
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
// AccountInfo,供直接调用 identityRewrite。上游协议族默认 anthropic_messages
// (R7 改写仅对 Anthropic 形 body 合法;协议族门控见 newIdentityRewriteExecFamily)。
func newIdentityRewriteExec(externalAccountID string) *chatExecution {
	return newIdentityRewriteExecFamily(externalAccountID, "anthropic_messages")
}

// newIdentityRewriteExecFamily 同上但可指定上游协议族,供协议族门控测试用。
func newIdentityRewriteExecFamily(externalAccountID, protocolFamily string) *chatExecution {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(""))
	req.Header.Set("User-Agent", "claude-cli/2.1.78 (external, cli)")
	return &chatExecution{
		r:        req,
		resolved: registry.Resolved{ProtocolFamily: protocolFamily},
		accInfo: provider.AccountInfo{
			AccountID:         42,
			Platform:          "anthropic",
			AccountType:       "claude_ai_oauth", // 反转/订阅号:身份改写仅对反转号生效
			ExternalAccountID: externalAccountID,
		},
	}
}

// TestIdentityRewrite_协议族门控_非Anthropic不改写 修 R7 S2:R7 身份改写写的是
// metadata.user_id(Claude Code/Anthropic 专属语义),只有上游协议族是
// anthropic_messages 时 dispatchBody 才是 Anthropic 形请求体、注入才合法。此前
// identityRewrite 仅以 ExternalAccountID!="" 为闸、无协议族判断 —— 运维开 R7 后,
// 池中带 external_account_id 的 OpenAI/Gemini 账号请求会被强注入 Anthropic 形顶层
// metadata.user_id,被上游拒(Gemini 未知顶层字段 400、OpenAI metadata 语义错配)。
//
// 变异证伪:删 identityRewrite 里 `ex.resolved.ProtocolFamily != "anthropic_messages"`
// 守卫 → 非 Anthropic body 被注入 metadata → 下面"字节等价"断言变红。
func TestIdentityRewrite_协议族门控_非Anthropic不改写(t *testing.T) {
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_REWRITE", "true")
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_SECRET", "fixed-secret-for-test")

	// 一个无 metadata 的 OpenAI 形 body(若守卫失效会被强注入顶层 metadata.user_id)。
	openAIBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)

	// 关键:协议族为 openai_chat(非 anthropic_messages),即便开关开 + secret 设 +
	// external id 非空,也必须 fail-open 不改写。
	ex := newIdentityRewriteExecFamily("acc-xyz", "openai_chat")
	out := ex.identityRewrite(openAIBody)
	if string(out) != string(openAIBody) {
		t.Fatalf("非 Anthropic 协议族(openai_chat)必须 fail-open 字节等价,绝不注入 Anthropic 形 metadata\n原: %s\n出: %s", openAIBody, out)
	}

	// 对照:同样配置但协议族为 anthropic_messages 时,改写确实发生(证明门控不是把
	// 所有路都堵死,而是精确按族放行)。
	exAnthropic := newIdentityRewriteExecFamily("acc-xyz", "anthropic_messages")
	outA := exAnthropic.identityRewrite(anthropicMarshalledBodyNoMetadata())
	if string(outA) == string(anthropicMarshalledBodyNoMetadata()) {
		t.Fatalf("anthropic_messages 族本应改写,却字节等价(门控误伤 Anthropic 路)")
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

// TestIdentityRewrite_显式关_零变更 验证:运维开关显式设为 false 时,
// identityRewrite 返回与入参逐字节等价的 body。
//
// 变异证伪:把 false 也当成开 → 改写发生 → account_uuid 变化 → 字节不再等价 → 红。
func TestIdentityRewrite_显式关_零变更(t *testing.T) {
	// 显式关:开关设为 false。
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_REWRITE", "false")
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_SECRET", "fixed-secret-for-test")

	body := identityRewriteFixtureBody(t)
	ex := newIdentityRewriteExec("acc-xyz")

	out := ex.identityRewrite(body)
	if string(out) != string(body) {
		t.Fatalf("显式关时必须字节等价\n原: %s\n出: %s", body, out)
	}
}

// anthropicMarshalledBodyNoMetadata 模拟 HCSF canonical 路 MarshalToProviderRequest
// 对 anthropic 产出的【无 metadata 字段】上游 body(marshalAnthropicMessages 只产
// model/messages/stream/system,绝不带 metadata)。这是 R7 第三路改写钩子的真实
// 作用对象 —— 与流式/legacy raw 路的"客户端原 body 自带 metadata"形态不同。
func anthropicMarshalledBodyNoMetadata() []byte {
	return []byte(`{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"stream":false}`)
}

// TestIdentityRewrite_HCSF三路一致_marshal后body被注入身份 验证【C:三路一致】的
// 关键一环 —— 同一个 ex.identityRewrite 闭环(HCSF 接线点 dispatch.go 传给
// HCSFDispatchInput.IdentityRewrite 的就是它),施加在 HCSF canonical marshal 产物
// (无 metadata 的 anthropic body)上时,能把池账号身份【注入】成 metadata.user_id,
// 与流式/raw 路对自带 metadata 的客户端 body 改写后落到同一含上游 id 的身份。
//
// 这坐实:HCSF 路虽然在入口改 ex.body 流不过去(canonical 往返丢 metadata),但
// 把【同一闭环】挪到 marshal 后施加即闭环 —— 三路最终都让上游看到含池账号 id 的
// metadata.user_id(account 组件 == ExternalAccountID)。
//
// 变异证伪:把 RewriteInboundBody 的 MetadataInjectRewrite 在无 user_id 时的
// 回退注入路径删掉(无 metadata 就不注入)→ HCSF 路 marshal 后 body 仍无
// metadata → 下面"已注入 + account 组件==上游 id"断言变红。
func TestIdentityRewrite_HCSF三路一致_marshal后body被注入身份(t *testing.T) {
	const externalID = "acc-xyz"
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_REWRITE", "true")
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_SECRET", "fixed-secret-for-test")

	ex := newIdentityRewriteExec(externalID)

	// 1) HCSF 路:无 metadata 的 marshal 产物 → 闭环注入 metadata.user_id。
	hcsfOut := ex.identityRewrite(anthropicMarshalledBodyNoMetadata())
	hcsfUUID := extractMetadataAccountUUID(t, hcsfOut)
	if hcsfUUID != externalID {
		t.Fatalf("HCSF 路 marshal 后 body 应被注入含上游 id 的 metadata.user_id,account 组件实际 %q", hcsfUUID)
	}

	// 2) 流式/raw 路:自带 metadata 的客户端 body → 改写同一 account 组件。
	rawOut := ex.identityRewrite(identityRewriteFixtureBody(t))
	rawUUID := extractMetadataAccountUUID(t, rawOut)

	// 三路一致:两种入参形态最终 account 组件都落到同一上游 id。
	if rawUUID != externalID || hcsfUUID != rawUUID {
		t.Fatalf("三路应一致改写到同一上游 id %q:HCSF=%q raw=%q", externalID, hcsfUUID, rawUUID)
	}
}
