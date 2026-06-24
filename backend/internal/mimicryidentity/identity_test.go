package mimicryidentity

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

// 测试夹具:带新格式 metadata.user_id 的请求 body,以及一段非 metadata 内容
// (system/messages),用于 CCH 字节不变自证。
const testServerSecret = "fixed-server-secret-for-derivation"
const testExternalAccountUUID = "11111111-2222-3333-4444-555555555555"

// originalUserID 是夹具里的客户端原始 user_id(新 JSON 格式,带不同的
// account_uuid / device / session),改写后必须变成与池账号一致的派生值。
const originalUserID = `{"device_id":"clientdevice0000000000000000000000000000000000000000000000000000aa","account_uuid":"00000000-0000-0000-0000-000000000000","session_id":"99999999-8888-7777-6666-555555555555"}`

// fixtureBody 构造一个含 system / messages / metadata.user_id 的请求 body。
func fixtureBody(t *testing.T) []byte {
	t.Helper()
	root := map[string]json.RawMessage{
		"model":    json.RawMessage(`"claude-3-5-sonnet"`),
		"system":   json.RawMessage(`[{"type":"text","text":"你是一个助手","cache_control":{"type":"ephemeral"}}]`),
		"messages": json.RawMessage(`[{"role":"user","content":"早上好"}]`),
		"metadata": json.RawMessage(`{"user_id":` + mustJSONString(t, originalUserID) + `}`),
	}
	b, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("构造夹具 body 失败: %v", err)
	}
	return b
}

func mustJSONString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal string 失败: %v", err)
	}
	return string(b)
}

// extractUserID 从 body 取出 metadata.user_id 字符串。
func extractUserID(t *testing.T, body []byte) string {
	t.Helper()
	var root struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("解析 body.metadata.user_id 失败: %v", err)
	}
	return root.Metadata.UserID
}

// stripMetadata 返回去掉 metadata 字段后 body 的稳定序列化(键排序),用于
// 逐字节比对"非 metadata 部分"是否被改写顺手动过。
func stripMetadata(t *testing.T, body []byte) []byte {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("解析 body 失败: %v", err)
	}
	delete(root, "metadata")
	// map 序列化键有序,保证两侧可逐字节比对。
	out, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("重序列化失败: %v", err)
	}
	return out
}

// TestA_默认关零行为变更 验证:运维开关未配置时,经接线入口后请求体与原 body
// 字节等价。
//
// 变异证伪:把默认翻成开(即让 RewriteEnabled 默认返回 true,或本测试用
// t.Setenv 设 "true")→ 改写发生 → 字节不再等价 → 本测试变红。
func TestA_默认关零行为变更(t *testing.T) {
	// 不设置 HUAKAI_MIMICRY_IDENTITY_REWRITE → 默认关。显式清空以防环境污染。
	t.Setenv(envIdentityRewrite, "")
	body := fixtureBody(t)
	id := AccountIdentity{AccountID: 42, ExternalAccountID: testExternalAccountUUID, ClientCLIVersion: "2.1.78"}

	out, err := RewriteInboundBody(body, id, testServerSecret)
	if err != nil {
		t.Fatalf("默认关路径不应返回 error: %v", err)
	}
	if !bytes.Equal(out, body) {
		t.Fatalf("默认关时请求体必须字节等价\n原: %s\n出: %s", body, out)
	}
	// 自证:同样入参显式开启后,结果应当不同(否则说明开关根本没生效)。
	t.Setenv(envIdentityRewrite, "true")
	enabledOut, err := RewriteInboundBody(body, id, testServerSecret)
	if err != nil {
		t.Fatalf("开启路径返回 error: %v", err)
	}
	if bytes.Equal(enabledOut, body) {
		t.Fatalf("开启后请求体本应被改写,却与原 body 字节等价 —— 开关或改写失效")
	}
}

// TestB_failopen_空外部账号id 验证:开关开启但 external account id 为空时,
// 不改写、字节等价(镜像 sub2 account_uuid==” 跳过)。
//
// 变异证伪:把 fail-open 改成"空也强行改写"(删去 ExternalAccountID=="" 短路)
// → 会拿空 account 组件去改写 → 字节不再等价 → 本测试变红。
func TestB_failopen_空外部账号id(t *testing.T) {
	t.Setenv(envIdentityRewrite, "true")
	body := fixtureBody(t)
	idEmpty := AccountIdentity{AccountID: 42, ExternalAccountID: "", ClientCLIVersion: "2.1.78"}

	out, err := RewriteInboundBody(body, idEmpty, testServerSecret)
	if err != nil {
		t.Fatalf("fail-open 路径不应返回 error: %v", err)
	}
	if !bytes.Equal(out, body) {
		t.Fatalf("external account id 空时必须 fail-open 字节等价\n原: %s\n出: %s", body, out)
	}
	// 自证:补上非空 id 后同样开关下结果应被改写,证明"空才跳过"而非"恒不改"。
	idFilled := AccountIdentity{AccountID: 42, ExternalAccountID: testExternalAccountUUID, ClientCLIVersion: "2.1.78"}
	filledOut, _ := RewriteInboundBody(body, idFilled, testServerSecret)
	if bytes.Equal(filledOut, body) {
		t.Fatalf("非空 external account id 本应触发改写,却字节等价 —— fail-open 判定恒真")
	}
}

// TestB2_failopen_空serverSecret 验证:serverSecret 为空时也 fail-open。
//
// 变异证伪:删去 serverSecret=="" 短路 → 会用空 secret 派生指纹去改写 →
// 字节不再等价 → 本测试变红。
func TestB2_failopen_空serverSecret(t *testing.T) {
	t.Setenv(envIdentityRewrite, "true")
	body := fixtureBody(t)
	id := AccountIdentity{AccountID: 42, ExternalAccountID: testExternalAccountUUID, ClientCLIVersion: "2.1.78"}

	out, err := RewriteInboundBody(body, id, "")
	if err != nil {
		t.Fatalf("空 serverSecret 路径不应返回 error: %v", err)
	}
	if !bytes.Equal(out, body) {
		t.Fatalf("serverSecret 空时必须 fail-open 字节等价\n原: %s\n出: %s", body, out)
	}
}

// TestC_开启且有身份_user_id被改写成派生值 验证:开启且 external id 非空时,
// metadata.user_id 真被改写成与账号匹配的派生值(account 组件 = 外部账号 id,
// device/session = 确定性派生),且 ≠ 原值。
//
// 变异证伪:把改写步骤短路(BuildPlan 不启 step5 / Enabled=false)→ user_id
// 不变 → 期望断言失败 → 本测试变红。
func TestC_开启且有身份_user_id被改写成派生值(t *testing.T) {
	t.Setenv(envIdentityRewrite, "true")
	body := fixtureBody(t)
	const accountID int64 = 42
	id := AccountIdentity{AccountID: accountID, ExternalAccountID: testExternalAccountUUID, ClientCLIVersion: "2.1.78"}

	out, err := RewriteInboundBody(body, id, testServerSecret)
	if err != nil {
		t.Fatalf("改写返回 error: %v", err)
	}

	gotUserID := extractUserID(t, out)
	if gotUserID == originalUserID {
		t.Fatalf("metadata.user_id 未被改写,仍为原值: %s", gotUserID)
	}

	// 期望派生值:account 组件必须等于外部账号 id;device/session 等于确定性
	// 派生函数的产出。新格式 → JSON。
	wantDevice := deriveDeviceID(testServerSecret, accountID)
	wantSession := deriveSessionUUID(testServerSecret, accountID, "") // id.ClientSessionID 为空
	wantUserID := gateway.FormatMetadataUserID(wantDevice, testExternalAccountUUID, wantSession, true)
	if gotUserID != wantUserID {
		t.Fatalf("改写后 user_id 与期望派生值不符\n得到: %s\n期望: %s", gotUserID, wantUserID)
	}

	// 判别性强化:account 组件必须确实是池账号 id,而非客户端原值。
	var parsed struct {
		DeviceID    string `json:"device_id"`
		AccountUUID string `json:"account_uuid"`
		SessionID   string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(gotUserID), &parsed); err != nil {
		t.Fatalf("解析改写后 user_id JSON 失败: %v", err)
	}
	if parsed.AccountUUID != testExternalAccountUUID {
		t.Fatalf("account_uuid 应被投影成池账号 id %q,实际 %q", testExternalAccountUUID, parsed.AccountUUID)
	}
	if parsed.AccountUUID == "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("account_uuid 仍是客户端原值,改写未生效")
	}
}

// TestSession按客户端会话派生 修 R7 S2:此前 session 仅以 accountID 为 seed,落到同一
// 池账号的所有请求(不同终端、不同对话)写给上游的 session_id 完全相同,上游观测成
// "同 session 塞进互相矛盾的跨用户上下文"反触发会话级风控。修复后 clientSessionID
// 纳入 seed:不同客户端会话派生不同 session_id,同(账号,会话)稳定。
//
// 变异证伪:把 deriveSessionUUID 的 seed 退回不含 clientSessionID(忽略入参)→
// 两个不同客户端会话派生相同 → 第一段断言变红。
func TestSession按客户端会话派生(t *testing.T) {
	const acct int64 = 42
	sA := deriveSessionUUID(testServerSecret, acct, "client-session-aaa")
	sB := deriveSessionUUID(testServerSecret, acct, "client-session-bbb")
	if sA == sB {
		t.Fatalf("同账号不同客户端会话本应派生不同 session_id,却相同: %s", sA)
	}
	// 稳定性:同(账号, 客户端会话)重复派生同值(保上游会话亲和)。
	if again := deriveSessionUUID(testServerSecret, acct, "client-session-aaa"); again != sA {
		t.Fatalf("同(账号, 客户端会话)派生应稳定: %s != %s", again, sA)
	}
	// 空客户端会话退回账号级派生,仍是合法 UUID 形态(8-4-4-4-12 = 36 字符)。
	if sEmpty := deriveSessionUUID(testServerSecret, acct, ""); len(sEmpty) != 36 {
		t.Fatalf("空客户端会话应仍派生 UUID 形态(36 字符),实际 %d: %s", len(sEmpty), sEmpty)
	}
}

// TestC2_派生确定性 验证:同 (serverSecret, accountID) 多次派生结果稳定;
// 不同 accountID 派生不同(免存储、确定性、可复现)。
//
// 变异证伪:把派生 seed 里的 accountID 去掉 → 不同账号派生相同 → 第二段断言
// 变红。
func TestC2_派生确定性(t *testing.T) {
	d1 := deriveDeviceID(testServerSecret, 42)
	d2 := deriveDeviceID(testServerSecret, 42)
	if d1 != d2 {
		t.Fatalf("同账号 device 派生不稳定: %s != %s", d1, d2)
	}
	dOther := deriveDeviceID(testServerSecret, 43)
	if d1 == dOther {
		t.Fatalf("不同账号 device 派生本应不同,却相同: %s", d1)
	}
	s1 := deriveSessionUUID(testServerSecret, 42, "")
	sOther := deriveSessionUUID(testServerSecret, 43, "")
	if s1 == sOther {
		t.Fatalf("不同账号 session 派生本应不同,却相同: %s", s1)
	}
	// device 必须是 64 位 hex,session 必须是 UUID 形态。
	if len(d1) != 64 {
		t.Fatalf("device 应为 64 位 hex,实际长度 %d", len(d1))
	}
	if len(s1) != 36 || s1[8] != '-' || s1[13] != '-' || s1[18] != '-' || s1[23] != '-' {
		t.Fatalf("session 应为 UUID 形态 8-4-4-4-12,实际 %q", s1)
	}
}

// TestD_CCH字节不变_仅动metadata 验证:开启改写后,除 metadata 外的所有字节
// 逐字节不变(self-proving:对比改写前后去掉 metadata 的稳定序列化)。
//
// 变异证伪:让改写顺手改了别的字段(如把 step5 换成会动 system/tools 的步骤,
// 或 plan 顺带启了其它 step)→ 非 metadata 序列化变化 → 本测试变红。
func TestD_CCH字节不变_仅动metadata(t *testing.T) {
	t.Setenv(envIdentityRewrite, "true")
	body := fixtureBody(t)
	id := AccountIdentity{AccountID: 42, ExternalAccountID: testExternalAccountUUID, ClientCLIVersion: "2.1.78"}

	out, err := RewriteInboundBody(body, id, testServerSecret)
	if err != nil {
		t.Fatalf("改写返回 error: %v", err)
	}
	if bytes.Equal(out, body) {
		t.Fatalf("前置条件:改写本应发生,但 out 与原 body 等价")
	}

	beforeNonMeta := stripMetadata(t, body)
	afterNonMeta := stripMetadata(t, out)
	if !bytes.Equal(beforeNonMeta, afterNonMeta) {
		t.Fatalf("非 metadata 字节被改写顺手动过(CCH 风险)\n前: %s\n后: %s", beforeNonMeta, afterNonMeta)
	}

	// 额外坐实:metadata 子树确实变了(否则上面"非 metadata 不变"会因为整体
	// 没动而假绿)。
	if extractUserID(t, out) == originalUserID {
		t.Fatalf("metadata.user_id 未变,测试 D 的差异性前提不成立")
	}
}

// TestE_缺metadata_failopen_不阻断 验证:body 没有 metadata 字段时,
// ApplyMimicryPlan 内部 fail-open,入口不阻断、不报错;此时是否注入由
// rewrite 模式 + fallback 决定 —— 这里断言至少不返回 error 且 body 仍是合法
// JSON(永不阻断请求)。
func TestE_缺metadata_failopen_不阻断(t *testing.T) {
	t.Setenv(envIdentityRewrite, "true")
	body := []byte(`{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}]}`)
	id := AccountIdentity{AccountID: 7, ExternalAccountID: testExternalAccountUUID, ClientCLIVersion: "2.1.78"}

	out, err := RewriteInboundBody(body, id, testServerSecret)
	if err != nil {
		t.Fatalf("缺 metadata 时不应返回阻断性 error: %v", err)
	}
	var sink map[string]json.RawMessage
	if jerr := json.Unmarshal(out, &sink); jerr != nil {
		t.Fatalf("改写产物应为合法 JSON: %v", jerr)
	}
}
