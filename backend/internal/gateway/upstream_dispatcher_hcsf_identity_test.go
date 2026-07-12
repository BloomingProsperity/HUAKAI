package gateway

// 本测试守 R7 身份改写【第三路:HCSF canonical 非流式路】的覆盖闭环。
//
// 背景(对抗审查抓出的 S2):R7 身份改写此前只接进流式路与 legacy raw 路,
// 但默认走的 HCSF canonical 非流式路(dispatchCanonicalBuffered → DispatchHCSF)
// 没接。该路上游真实 body 由 MarshalToProviderRequest 从 canonical 结构重新
// marshal 出来 —— marshalAnthropicMessages 根本不带 metadata 字段(且
// RequestToCanonical 把客户端 metadata 整段丢弃,仅记 d1_metadata_not_yet_implemented
// loss),故在 dispatch 入口对 ex.body 做改写【流不过去】。本切片把改写钩子
// (HCSFDispatchInput.IdentityRewrite)施加在【已 marshal 出的最终上游 body】上,
// 让 R7 也覆盖 HCSF 非流式路。
//
// 这里直接驱动 buildHCSFProviderRequest(marshal + 钩子的真实接线点),用
// stubAdapter 捕获 lastInput.InboundBody = 发往上游的真实 body,断言其
// metadata.user_id 被改写钩子注入/改写。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// anthropicHCSFEnvelopeForIdentity 构造一个最小 anthropic_messages HCSF envelope,
// marshal 产物里【不含 metadata】(marshalAnthropicMessages 只产 model/messages/
// stream),正是要被 R7 钩子在事后注入 metadata.user_id 的目标形态。
func anthropicHCSFEnvelopeForIdentity() *proto.HCSF {
	env := testHCSFEnvelope()
	env.RequestMeta.ClientProtocol = proto.ClientProtocolAnthropicMessages
	env.RequestMeta.ProtocolFamily = "anthropic_messages"
	env.RequestMeta.EndpointFamily = "anthropic_messages"
	env.RequestMeta.Model = "claude-3-5-sonnet"
	env.RequestMeta.UpstreamModel = "claude-3-5-sonnet"
	env.RequestMeta.Provider = "anthropic"
	env.Accounting.ModelChain = &proto.ModelChain{Requested: "claude-3-5-sonnet", RouteDecided: "claude-3-5-sonnet"}
	return env
}

// identityRewriteHookForTest 返回一个与生产 R7 钩子【同引擎】的改写函数:用
// gateway.RewriteMetadataUserID(R7 改写所依赖的同一引擎)把 metadata.user_id
// 投影成含给定 account UUID 的身份。account 为空时返回原 body 拷贝(模拟
// fail-open)。生产侧 ex.identityRewrite → mimicryidentity.RewriteForDispatch
// 最终也落到这条引擎,故本钩子忠实代表 R7 在 HCSF 路上的真实改写效果。
func identityRewriteHookForTest(accountUUID string) func([]byte) []byte {
	return func(body []byte) []byte {
		if strings.TrimSpace(accountUUID) == "" {
			return append([]byte(nil), body...)
		}
		res, err := RewriteMetadataUserID(body, MetadataUserIDPlan{
			Mode:           MetadataInjectRewrite,
			DeviceID:       "device0000000000000000000000000000000000000000000000000000000000",
			AccountUUID:    accountUUID,
			SessionID:      "11111111-2222-3333-4444-555555555555",
			UseNewFormat:   true,
			FallbackUserID: FormatMetadataUserID("device0000000000000000000000000000000000000000000000000000000000", accountUUID, "11111111-2222-3333-4444-555555555555", true),
		})
		if err != nil {
			return append([]byte(nil), body...)
		}
		return res.Body
	}
}

// extractUpstreamMetadataAccountUUID 从上游 body 取 metadata.user_id,再解出其
// account_uuid 组件(JSON 新格式)。
func extractUpstreamMetadataAccountUUID(t *testing.T, body []byte) (string, bool) {
	t.Helper()
	var outer struct {
		Metadata *struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &outer); err != nil {
		t.Fatalf("解析上游 body 失败: %v\nbody=%s", err, body)
	}
	if outer.Metadata == nil || outer.Metadata.UserID == "" {
		return "", false
	}
	var inner struct {
		AccountUUID string `json:"account_uuid"`
	}
	if err := json.Unmarshal([]byte(outer.Metadata.UserID), &inner); err != nil {
		t.Fatalf("解析 user_id JSON 失败: %v (user_id=%q)", err, outer.Metadata.UserID)
	}
	return inner.AccountUUID, true
}

// TestHCSFIdentityRewrite_canonical路覆盖_上游body被改写 验证【A:HCSF 覆盖点亮】:
// anthropic canonical 非流式路上,IdentityRewrite 钩子被施加在 marshal 出的最终
// 上游 body 上 → 发往上游的 body 出现含池账号 UUID 的 metadata.user_id。
//
// 变异证伪(重现 S2 漏覆盖):把 buildHCSFProviderRequest canonical-marshal 子路
// 里的 `body = applyIdentityRewrite(body, identityRewrite)` 删掉 → 上游 body 退回
// 无 metadata 的 marshal 原貌 → 下面"metadata.user_id 已注入且 account_uuid==上游 id"
// 断言变红。
func TestHCSFIdentityRewrite_canonical路覆盖_上游body被改写(t *testing.T) {
	const upstreamUUID = "acc-upstream-7777"
	adapter := &stubAdapter{platform: "anthropic"}

	_, err := buildHCSFProviderRequest(context.Background(), adapter, provider.BuildInput{
		UpstreamModelID: "claude-3-5-sonnet",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant-test"},
		Account:         provider.AccountInfo{AccountID: 9, Platform: "anthropic", AccountType: "apikey"},
	}, anthropicHCSFEnvelopeForIdentity(), "anthropic_messages", "anthropic_messages", nil, identityRewriteHookForTest(upstreamUUID))
	if err != nil {
		t.Fatalf("buildHCSFProviderRequest: %v", err)
	}

	got, ok := extractUpstreamMetadataAccountUUID(t, adapter.lastInput.InboundBody)
	if !ok {
		t.Fatalf("HCSF 漏覆盖:上游 body 仍无 metadata.user_id —— canonical 路改写未施加\nbody=%s", adapter.lastInput.InboundBody)
	}
	if got != upstreamUUID {
		t.Fatalf("metadata.user_id 的 account_uuid 应为上游 id %q,实际 %q\nbody=%s", upstreamUUID, got, adapter.lastInput.InboundBody)
	}
}

// TestHCSFIdentityRewrite_默认关_上游body字节等价 验证【B:HCSF 默认关零变更】:
// IdentityRewrite 钩子为 nil(模拟 R7 默认关时 ex.identityRewrite 的空操作效果 ——
// 钩子返回入参拷贝)时,canonical 路上游 body 与"完全不接钩子"字节等价,且
// 绝不出现 metadata 字段(anthropic marshal 原貌)。
//
// 变异证伪:把 applyIdentityRewrite 改成默认关也强行注入 metadata → 上游 body 多出
// metadata 字段、不再字节等价 → 红。
func TestHCSFIdentityRewrite_默认关_上游body字节等价(t *testing.T) {
	build := func(hook func([]byte) []byte) []byte {
		adapter := &stubAdapter{platform: "anthropic"}
		_, err := buildHCSFProviderRequest(context.Background(), adapter, provider.BuildInput{
			UpstreamModelID: "claude-3-5-sonnet",
			Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant-test"},
			Account:         provider.AccountInfo{AccountID: 9, Platform: "anthropic", AccountType: "apikey"},
		}, anthropicHCSFEnvelopeForIdentity(), "anthropic_messages", "anthropic_messages", nil, hook)
		if err != nil {
			t.Fatalf("buildHCSFProviderRequest: %v", err)
		}
		return append([]byte(nil), adapter.lastInput.InboundBody...)
	}

	// nil 钩子 = R7 默认关时的空操作(applyIdentityRewrite 对 nil 钩子原样返回)。
	withNilHook := build(nil)
	// 不接钩子(钩子返回入参拷贝)= 默认关时 ex.identityRewrite 的真实语义。
	withNoopHook := build(func(b []byte) []byte { return append([]byte(nil), b...) })

	if string(withNilHook) != string(withNoopHook) {
		t.Fatalf("默认关时 HCSF 上游 body 必须字节等价\nnil钩子: %s\nnoop钩子: %s", withNilHook, withNoopHook)
	}
	// 默认关时绝不应出现 metadata(anthropic marshal 原貌)。
	if _, ok := extractUpstreamMetadataAccountUUID(t, withNilHook); ok {
		t.Fatalf("默认关时 HCSF 上游 body 不应有 metadata.user_id:%s", withNilHook)
	}
}

// TestHCSFIdentityRewrite_failopen_空上游id_不注入 验证【C 之一致性:fail-open】:
// 钩子拿到空 account uuid(账号无 external id)时退回原 body 拷贝 → 上游 body 不被
// 注入 metadata、与默认关字节等价。这与流式/raw 两路 fail-open 语义一致。
//
// 变异证伪:把 identityRewriteHookForTest 的空串短路删掉(空也强行 marshal 注入)→
// 上游 body 冒出 metadata → 红。
func TestHCSFIdentityRewrite_failopen_空上游id_不注入(t *testing.T) {
	adapter := &stubAdapter{platform: "anthropic"}
	_, err := buildHCSFProviderRequest(context.Background(), adapter, provider.BuildInput{
		UpstreamModelID: "claude-3-5-sonnet",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant-test"},
		Account:         provider.AccountInfo{AccountID: 9, Platform: "anthropic", AccountType: "apikey"},
	}, anthropicHCSFEnvelopeForIdentity(), "anthropic_messages", "anthropic_messages", nil, identityRewriteHookForTest(""))
	if err != nil {
		t.Fatalf("buildHCSFProviderRequest: %v", err)
	}
	if _, ok := extractUpstreamMetadataAccountUUID(t, adapter.lastInput.InboundBody); ok {
		t.Fatalf("空上游 id 应 fail-open 不注入 metadata:%s", adapter.lastInput.InboundBody)
	}
}
