package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/clientgate"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func thinkingReplayBody() []byte {
	return []byte(`{
		"model":"claude-opus-4-8",
		"max_tokens":1024,
		"messages":[
			{"role":"user","content":"sum"},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"I should call add","signature":"sig_real_not_synthetic"},
				{"type":"text","text":"need tool"},
				{"type":"tool_use","id":"toolu_1","name":"add","input":{"a":1}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"2"}
			]}
		]
	}`)
}

func hasSignedThinking(raw []byte) bool {
	return bytes.Contains(raw, []byte(`"type":"thinking"`)) && bytes.Contains(raw, []byte(`sig_real_not_synthetic`))
}

// TestOfficialDirectKeepsThinkingBytes 官方直发合同不变：整包直通，含无/有签名思考。
// 变异：OfficialDirect 误走重组 → 字节不再相等。
func TestOfficialDirectKeepsThinkingBytes(t *testing.T) {
	raw := thinkingReplayBody()
	adapter := &stubAdapter{platform: "anthropic"}
	ctx := ContextWithHCSFDispatchInput(context.Background(), HCSFDispatchInput{
		OfficialDirect:  true,
		ProtocolFamily:  "anthropic_messages",
		UpstreamModelID: "claude-opus-4-8",
		Account:         provider.AccountInfo{AccountID: 1, Platform: "anthropic", AccountType: "oauth"},
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant"},
		RawBody:         raw,
	})
	in := provider.BuildInput{
		UpstreamModelID: "claude-opus-4-8",
		Account:         provider.AccountInfo{AccountID: 1, Platform: "anthropic", AccountType: "oauth"},
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant"},
	}
	if _, err := buildHCSFProviderRequest(ctx, adapter, in, proto.NewEmptyEnvelope(), "anthropic_messages", "anthropic_claude_session", raw, nil); err != nil {
		t.Fatalf("official direct: %v", err)
	}
	if !bytes.Equal(adapter.lastInput.InboundBody, raw) {
		t.Fatalf("官方直发字节漂移\ngot  %s\nwant %s", adapter.lastInput.InboundBody, raw)
	}
	if !hasSignedThinking(adapter.lastInput.InboundBody) {
		t.Fatal("官方直发必须保留思考块")
	}
}

// TestCompatRemarshalKeepsSignedThinking API Key 重组不得再剥已签名思考。
func TestCompatRemarshalKeepsSignedThinking(t *testing.T) {
	raw := thinkingReplayBody()
	env, losses, err := (&proto.AnthropicMessagesClient{}).RequestToCanonical(
		proto.ContextWithRequestMetaSeed(context.Background(), proto.RequestMetaSeed{
			RequestID:      "req_at_apikey",
			ClientProtocol: proto.ClientProtocolAnthropicMessages,
			ProtocolFamily: "anthropic_messages",
			IngressPath:    "/v1/messages",
			EvidenceLabel:  proto.EvidenceMock,
		}), raw)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	for _, loss := range losses {
		if loss.Code == "d1x_thinking_block_pending" {
			t.Fatalf("入站不得再 pending: %+v", loss)
		}
	}
	env.RequestMeta.Provider = "minimax"
	adapter := &stubAdapter{platform: "minimax"}
	ctx := ContextWithHCSFDispatchInput(context.Background(), HCSFDispatchInput{
		OfficialDirect:  false,
		ProtocolFamily:  "anthropic_messages",
		UpstreamModelID: "claude-opus-4-8",
		Account:         provider.AccountInfo{AccountID: 2, Platform: "minimax", AccountType: "apikey"},
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant"},
		RawBody:         raw,
	})
	in := provider.BuildInput{
		UpstreamModelID: "claude-opus-4-8",
		Account:         provider.AccountInfo{AccountID: 2, Platform: "minimax", AccountType: "apikey"},
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant"},
	}
	if _, err := buildHCSFProviderRequest(ctx, adapter, in, env, "anthropic_messages", "anthropic_messages", raw, nil); err != nil {
		t.Fatalf("compat remashal: %v", err)
	}
	if bytes.Equal(adapter.lastInput.InboundBody, raw) {
		t.Fatal("重组路径应重编码，但必须留下思考；本断言防止误走直发")
	}
	if !hasSignedThinking(adapter.lastInput.InboundBody) {
		t.Fatalf("兼容重组丢掉签名思考: %s", adapter.lastInput.InboundBody)
	}
}

func TestClientGateFamilyDecisionsUnchanged(t *testing.T) {
	body := thinkingReplayBody()
	official := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	official.Header.Set("Content-Type", "application/json")
	official.Header.Set("User-Agent", "claude-cli/2.1.78 (external, cli)")
	official.Header.Set("X-App", "cli")
	official.Header.Set("X-Stainless-Lang", "js")
	official.Header.Set("X-Stainless-Runtime", "node")
	official.Header.Set("X-Stainless-Package-Version", "0.74.0")
	official.Header.Set("X-Stainless-Retry-Count", "0")
	official.Header.Set("Anthropic-Version", "2023-06-01")

	got := clientgate.DecideWithBody(context.Background(), nil, credentialstore.AuthModeClaudeAIOAuth, "anthropic", false, official, body)
	if got.Decision != clientgate.DecisionOfficialDirect || !bytes.Equal(got.Body, body) {
		t.Fatalf("官方形态必须直发: %+v", got)
	}

	compat := official.Clone(official.Context())
	compat.Header = http.Header{}
	compat.Header.Set("Content-Type", "application/json")
	compat.Header.Set("User-Agent", "curl/8.0")
	got = clientgate.DecideWithBody(context.Background(), nil, credentialstore.AuthModeClaudeAIOAuth, "anthropic", false, compat, body)
	if got.Decision != clientgate.DecisionAllow || got.Reason != "anthropic_session_compatible_rewrite" {
		t.Fatalf("第三方 Messages 必须进兼容重写: %+v", got)
	}
}

func unsignedThinkingBody() []byte {
	return []byte(`{
		"model":"claude-opus-4-8",
		"max_tokens":1024,
		"messages":[
			{"role":"user","content":"sum"},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"unsigned plan"},
				{"type":"text","text":"need tool"},
				{"type":"tool_use","id":"toolu_1","name":"add","input":{"a":1}}
			]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"2"}]}
		]
	}`)
}

func decodeThinkingReplayEnv(t *testing.T, raw []byte) *proto.HCSF {
	t.Helper()
	env, losses, err := (&proto.AnthropicMessagesClient{}).RequestToCanonical(
		proto.ContextWithRequestMetaSeed(context.Background(), proto.RequestMetaSeed{
			RequestID:      "req_at_family_bind",
			ClientProtocol: proto.ClientProtocolAnthropicMessages,
			ProtocolFamily: "anthropic_messages",
			IngressPath:    "/v1/messages",
			EvidenceLabel:  proto.EvidenceMock,
		}), raw)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	for _, loss := range losses {
		if loss.Code == "d1x_thinking_block_pending" {
			t.Fatalf("入站不得再 pending: %+v", loss)
		}
	}
	if env.RequestMeta.Provider != "" {
		t.Fatalf("本 AT 要求入站后 Provider 为空，才能证明热路径用账号 vendor: %q", env.RequestMeta.Provider)
	}
	return env
}

func remashalThinking(t *testing.T, env *proto.HCSF, raw []byte, platform string) []byte {
	t.Helper()
	adapter := &stubAdapter{platform: platform}
	acc := provider.AccountInfo{AccountID: 9, Platform: platform, AccountType: "apikey"}
	ctx := ContextWithHCSFDispatchInput(context.Background(), HCSFDispatchInput{
		OfficialDirect:  false,
		ProtocolFamily:  "anthropic_messages",
		UpstreamModelID: "claude-opus-4-8",
		Account:         acc,
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant"},
		RawBody:         raw,
	})
	in := provider.BuildInput{
		UpstreamModelID: "claude-opus-4-8",
		Account:         acc,
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant"},
	}
	if _, err := buildHCSFProviderRequest(ctx, adapter, in, env, "anthropic_messages", "anthropic_messages", raw, nil); err != nil {
		t.Fatalf("remarshal %s: %v", platform, err)
	}
	return adapter.lastInput.InboundBody
}

// TestRemarshalFamilyUsesAccountPlatform 守住：族判定读出站账号 vendor，不读空 Provider / 客户端 claude-* 名。
// 变异：删 bindReplayProviderFromAccount → 官方账号也会留下无签名思考。
func TestRemarshalFamilyUsesAccountPlatform(t *testing.T) {
	raw := unsignedThinkingBody()
	official := remashalThinking(t, decodeThinkingReplayEnv(t, raw), raw, "anthropic")
	if bytes.Contains(official, []byte("unsigned plan")) {
		t.Fatalf("官方账号重组必须剥无签名思考: %s", official)
	}
	compat := remashalThinking(t, decodeThinkingReplayEnv(t, raw), raw, "minimax")
	if !bytes.Contains(compat, []byte(`"type":"thinking"`)) || !bytes.Contains(compat, []byte("unsigned plan")) {
		t.Fatalf("兼容账号重组必须留无签名思考: %s", compat)
	}
}
