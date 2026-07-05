package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

type mockCanonicalBufferedDispatcher struct {
	calls    int
	err      error
	observed *proto.HCSF
}

func (m *mockCanonicalBufferedDispatcher) DispatchHCSF(_ context.Context, requestEnvelope *proto.HCSF) (*proto.HCSF, error) {
	m.calls++
	m.observed = requestEnvelope
	if m.err != nil {
		return nil, m.err
	}
	env := proto.NewEmptyEnvelope()
	env.RequestMeta = requestEnvelope.RequestMeta
	env.BufferedResponse = &proto.CanonicalResponse{
		ID:         "chatcmpl-clientadapter-test",
		Model:      requestEnvelope.RequestMeta.UpstreamModel,
		Content:    []proto.CanonicalContentBlock{{Type: "text", Text: "hello from canonical"}},
		Usage:      proto.CanonicalUsage{InputTokens: 2, OutputTokens: 3},
		StopReason: proto.CanonicalStopEndTurn,
	}
	env.Accounting.Usage = env.BufferedResponse.Usage
	env.Accounting.EvidenceLabel = proto.EvidenceMock
	return env, nil
}

// protocolLossTestDispatcher 是 item 2 用的可配置 buffered dispatcher。
// stopReason 默认 end_turn;设为 CanonicalStopUnknown 触发 client 响应转换损失
// (stop_reason_unknown)。preserveRequestLoss 模拟真实 cloneHCSF
// (upstream_dispatcher_hcsf.go:144/153)把请求侧 CapabilityGraph.ProtocolLoss 带入响应 env。
type protocolLossTestDispatcher struct {
	calls               int
	stopReason          proto.CanonicalStopReason
	preserveRequestLoss bool
}

func (m *protocolLossTestDispatcher) DispatchHCSF(_ context.Context, requestEnvelope *proto.HCSF) (*proto.HCSF, error) {
	m.calls++
	stop := m.stopReason
	if stop == "" {
		stop = proto.CanonicalStopEndTurn
	}
	env := proto.NewEmptyEnvelope()
	env.RequestMeta = requestEnvelope.RequestMeta
	env.BufferedResponse = &proto.CanonicalResponse{
		ID:         "chatcmpl-protocol-loss-test",
		Model:      requestEnvelope.RequestMeta.UpstreamModel,
		Content:    []proto.CanonicalContentBlock{{Type: "text", Text: "hi"}},
		Usage:      proto.CanonicalUsage{InputTokens: 2, OutputTokens: 3},
		StopReason: stop,
	}
	env.Accounting.Usage = env.BufferedResponse.Usage
	env.Accounting.EvidenceLabel = proto.EvidenceMock
	if m.preserveRequestLoss {
		env.CapabilityGraph.ProtocolLoss = append(env.CapabilityGraph.ProtocolLoss, requestEnvelope.CapabilityGraph.ProtocolLoss...)
	}
	return env, nil
}

// settledLossHasCode 解码 SettleRequest.ProtocolLoss(json.RawMessage)并判 code 成员。
func settledLossHasCode(t *testing.T, raw json.RawMessage, code string) bool {
	t.Helper()
	if len(raw) == 0 {
		return false
	}
	var entries []proto.ProtocolLossEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("unmarshal settle protocol_loss: %v (raw=%s)", err, raw)
	}
	return hasProtocolLossCode(entries, code)
}

func enableHCSFDispatchForTest(t *testing.T) {
	t.Helper()
	t.Setenv("HUAKAI_DISPATCH_HCSF", "1")
}

func clientAdapterDeps(t *testing.T) ChatHandlerDeps {
	t.Helper()
	d := minimalDeps()
	d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "gpt-4o",
		CanonicalModelID: "openai/gpt-4o",
		ProviderModelID:  "gpt-4o",
		ProtocolFamily:   "openai_chat",
		PoolCandidates:   []int64{42},
	}}
	vault := provider.NewStaticVault()
	if err := vault.Set(1, provider.Credential{
		Type:  provider.CredentialTypeAPIKey,
		Value: "sk-test",
	}, provider.AccountInfo{
		AccountID:           1,
		Platform:            "openai",
		AccountType:         "apikey",
		AccountCredentialID: 9001,
		CredentialVersion:   1,
	}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	d.CredentialVault = vault
	return d
}

type recordingChannelHealth struct {
	signals        []channelhealth.Signal
	forceCooldowns []recordedForceCooldown
}

type recordedForceCooldown struct {
	key    channelhealth.ChannelKey
	until  time.Time
	reason string
}

func (r *recordingChannelHealth) ApplySignal(_ context.Context, sig channelhealth.Signal) (channelhealth.Record, error) {
	r.signals = append(r.signals, sig)
	return channelhealth.Record{Key: sig.Key, State: channelhealth.StateActive}, nil
}

func (r *recordingChannelHealth) ForceCooldown(_ context.Context, key channelhealth.ChannelKey, until time.Time, reason string) (channelhealth.Record, error) {
	r.forceCooldowns = append(r.forceCooldowns, recordedForceCooldown{key: key, until: until, reason: reason})
	return channelhealth.Record{Key: key, State: channelhealth.StateCoolingDown, CooldownUntil: &until}, nil
}

func invokeHandlerPath(t *testing.T, deps ChatHandlerDeps, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewChatCompletionsHandler(deps)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestChatCompletionsClientAdapter_NonStreamingHappyPath(t *testing.T) {
	enableHCSFDispatchForTest(t)
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("Content-Type = %q; want application/json", rec.Header().Get("Content-Type"))
	}
	if dispatcher.calls != 1 {
		t.Fatalf("canonical dispatcher calls = %d; want 1", dispatcher.calls)
	}
	if dispatcher.observed == nil {
		t.Fatal("canonical dispatcher did not receive request envelope")
	}
	meta := dispatcher.observed.RequestMeta
	if meta.RequestID == "" {
		t.Fatal("RequestMeta.RequestID must be populated")
	}
	if meta.ClientProtocol != proto.ClientProtocolOpenAIChat {
		t.Fatalf("ClientProtocol = %q; want openai_chat", meta.ClientProtocol)
	}
	if meta.ProtocolFamily != "openai_chat" || meta.IngressPath != "/v1/chat/completions" {
		t.Fatalf("meta protocol/path = %q/%q", meta.ProtocolFamily, meta.IngressPath)
	}
	if meta.TenantID != 7 || meta.AccountID != 1 {
		t.Fatalf("tenant/account = %d/%d; want 7/1", meta.TenantID, meta.AccountID)
	}
	if meta.UpstreamModel != "gpt-4o" || meta.Provider != "openai" {
		t.Fatalf("upstream/provider = %q/%q; want gpt-4o/openai", meta.UpstreamModel, meta.Provider)
	}
	if meta.EvidenceLabel != proto.EvidenceMock {
		t.Fatalf("EvidenceLabel = %q; want mock", meta.EvidenceLabel)
	}

	var out struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content *string `json:"content"`
			} `json:"message"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response json: %v\n%s", err, rec.Body.String())
	}
	if out.Object != "chat.completion" {
		t.Fatalf("object = %q; want chat.completion", out.Object)
	}
	if len(out.Choices) != 1 || out.Choices[0].Message.Content == nil || *out.Choices[0].Message.Content != "hello from canonical" {
		t.Fatalf("choices = %+v; want assistant text", out.Choices)
	}
	if out.Choices[0].FinishReason == nil || *out.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %v; want stop", out.Choices[0].FinishReason)
	}
	if out.Usage.PromptTokens != 2 || out.Usage.CompletionTokens != 3 || out.Usage.TotalTokens != 5 {
		t.Fatalf("usage = %+v; want 2/3/5", out.Usage)
	}
}

func TestChatCompletions_ChannelHealthSignalOnHCSFSuccess(t *testing.T) {
	enableHCSFDispatchForTest(t)
	health := &recordingChannelHealth{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.ChannelHealth = health

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(health.signals) != 1 {
		t.Fatalf("signals=%+v want one success signal", health.signals)
	}
	got := health.signals[0]
	if got.Class != channelhealth.SignalSuccess || got.RawUpstreamText != "" {
		t.Fatalf("signal=%+v want normalized success without raw upstream text", got)
	}
	if got.Key.TenantID != 7 || got.Key.ProviderAccountID != 1 ||
		got.Key.AccountCredentialID != 9001 || got.Key.CredentialVersion != 1 {
		t.Fatalf("signal key=%+v", got.Key)
	}
}

func TestChatCompletions_ChannelHealthSignalOnHCSFTimeout(t *testing.T) {
	enableHCSFDispatchForTest(t)
	health := &recordingChannelHealth{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{err: errors.New("upstream connection timeout")}
	d.ChannelHealth = health

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503; body = %s", rec.Code, rec.Body.String())
	}
	if len(health.signals) != 1 || health.signals[0].Class != channelhealth.SignalTimeout {
		t.Fatalf("signals=%+v want one timeout signal", health.signals)
	}
}

func TestChatCompletionsClientAdapter_NonStreamingUnknownPath(t *testing.T) {
	enableHCSFDispatchForTest(t)
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/not-real", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unknown_route") {
		t.Fatalf("body = %q; want unknown_route", rec.Body.String())
	}
	if dispatcher.calls != 0 {
		t.Fatalf("canonical dispatcher calls = %d; want 0", dispatcher.calls)
	}
}

func TestChatCompletionsClientAdapter_NonStreamingInvalidRequestBody(t *testing.T) {
	enableHCSFDispatchForTest(t)
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":123}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_request_body") {
		t.Fatalf("body = %q; want invalid_request_body", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "messages[0].content") {
		t.Fatalf("body = %q; must not expose concrete adapter diagnostic", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "request body is invalid") {
		t.Fatalf("body = %q; want fixed invalid_request_body message", rec.Body.String())
	}
	if dispatcher.calls != 0 {
		t.Fatalf("canonical dispatcher calls = %d; want 0", dispatcher.calls)
	}
}

// marshalLossErrorDispatcher 模拟 DispatchHCSF 内 MarshalToProviderRequest 原地往
// requestEnvelope.CapabilityGraph.ProtocolLoss 追加 canonical→upstream marshal 损失
// (addMarshalLossRaw, hcsf_graph_marshal.go),随后上游 dispatch 失败(client.Do/非 2xx)。
// 守 finding 1: dispatch-error abort 必须携带 marshal 阶段证据。
type marshalLossErrorDispatcher struct {
	calls int
	code  string
}

func (m *marshalLossErrorDispatcher) DispatchHCSF(_ context.Context, requestEnvelope *proto.HCSF) (*proto.HCSF, error) {
	m.calls++
	requestEnvelope.CapabilityGraph.ProtocolLoss = append(requestEnvelope.CapabilityGraph.ProtocolLoss,
		proto.ProtocolLossEntry{Severity: proto.ProtocolLossWarning, Code: m.code, Reason: "marshal downgrade"})
	return nil, errors.New("upstream connection reset")
}

func TestNonStreamingDispatchError_RefreshesMarshalLossBeforeAbort(t *testing.T) {
	enableHCSFDispatchForTest(t)
	dispatcher := &marshalLossErrorDispatcher{code: "marshal_loss_on_dispatch_error"}
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.Settler = settler

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected dispatch-error status, got 200: %s", rec.Body.String())
	}
	if dispatcher.calls != 1 {
		t.Fatalf("dispatcher calls=%d want 1", dispatcher.calls)
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("aborts=%+v want exactly one dispatch-error abort", settler.aborts)
	}
	// 变异: 删除 dispatch.go 中 DispatchHCSF 之后的 ex.protocolLoss = protocolLossJSONFromEnv(canonicalReq)
	// 刷新 → abort 仍用 dispatch 前快照(仅请求翻译损失,本例为空)→ 缺 marshal sentinel → RED。
	if !settledLossHasCode(t, settler.aborts[0].protocolLoss, "marshal_loss_on_dispatch_error") {
		t.Fatalf("dispatch-error abort missing marshal-stage loss: %s", settler.aborts[0].protocolLoss)
	}
}

// identityHookCapturingDispatcher 在 DispatchHCSF 内从 ctx 取回本次 dispatch 的
// HCSFDispatchInput,把 R7 IdentityRewrite 钩子记下并实地对一个【无 metadata 的
// anthropic marshal 产物】施加一次,捕获改写后 body —— 用以断言 gatewayhttp 的
// dispatchCanonicalBuffered 真把 ex.identityRewrite 接进了 HCSF dispatch 输入。
type identityHookCapturingDispatcher struct {
	calls        int
	hookNonNil   bool
	rewrittenOut []byte
}

func (m *identityHookCapturingDispatcher) DispatchHCSF(ctx context.Context, requestEnvelope *proto.HCSF) (*proto.HCSF, error) {
	m.calls++
	in := gateway.HCSFDispatchInputFromContext(ctx)
	if in.IdentityRewrite != nil {
		m.hookNonNil = true
		// 用 HCSF canonical 路真实形态(无 metadata 的 anthropic body)施加钩子。
		m.rewrittenOut = in.IdentityRewrite([]byte(`{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"stream":false}`))
	}
	env := proto.NewEmptyEnvelope()
	env.RequestMeta = requestEnvelope.RequestMeta
	env.BufferedResponse = &proto.CanonicalResponse{
		ID:         "chatcmpl-identity-wire-test",
		Model:      requestEnvelope.RequestMeta.UpstreamModel,
		Content:    []proto.CanonicalContentBlock{{Type: "text", Text: "ok"}},
		Usage:      proto.CanonicalUsage{InputTokens: 1, OutputTokens: 1},
		StopReason: proto.CanonicalStopEndTurn,
	}
	env.Accounting.Usage = env.BufferedResponse.Usage
	env.Accounting.EvidenceLabel = proto.EvidenceMock
	return env, nil
}

// identityWiredDeps 在 clientAdapterDeps 基础上把池账号补上 ExternalAccountID,
// 让 R7 开关开后改写非空。协议族用 anthropic_messages —— R7 身份改写仅对 Anthropic
// 形 body 合法(协议族门控,见 TestIdentityRewrite_协议族门控_非Anthropic不改写),
// 故验证 HCSF 接线点真触发改写必须走 Anthropic 族。
func identityWiredDeps(t *testing.T, externalAccountID string) ChatHandlerDeps {
	t.Helper()
	d := clientAdapterDeps(t)
	// 覆盖 stub registry 为 Anthropic 族(clientAdapterDeps 默认 openai_chat)。
	d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "claude-3-5-sonnet",
		CanonicalModelID: "anthropic/claude-3-5-sonnet",
		ProviderModelID:  "claude-3-5-sonnet",
		ProtocolFamily:   "anthropic_messages",
		PoolCandidates:   []int64{42},
	}}
	vault := provider.NewStaticVault()
	if err := vault.Set(1, provider.Credential{
		Type:  provider.CredentialTypeAPIKey,
		Value: "sk-test",
	}, provider.AccountInfo{
		AccountID:           1,
		Platform:            "anthropic",
		AccountType:         "claude_ai_oauth", // 反转/订阅号:身份改写仅对反转号生效
		AccountCredentialID: 9001,
		CredentialVersion:   1,
		ExternalAccountID:   externalAccountID,
	}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	d.CredentialVault = vault
	return d
}

// TestChatCompletions_HCSF路真接R7改写钩子 验证【A 接线证据 + 变异锚点】:HCSF
// 默认非流式路(dispatchCanonicalBuffered)真把 ex.identityRewrite 注入了
// HCSFDispatchInput.IdentityRewrite,且 R7 开关开 + 账号带 ExternalAccountID 时,
// 该钩子真把无 metadata 的 anthropic body 改写出含上游 id 的 metadata.user_id。
//
// 变异证伪(破坏 HCSF 接线):把 chat_completions_dispatch.go 里
// `IdentityRewrite: ex.identityRewrite` 这一行删掉 → 钩子变 nil → hookNonNil=false
// → 下面断言变红(精确锚住 S2 漏接线)。
func TestChatCompletions_HCSF路真接R7改写钩子(t *testing.T) {
	const externalID = "acc-wired-9"
	enableHCSFDispatchForTest(t)
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_REWRITE", "true")
	t.Setenv("HUAKAI_MIMICRY_IDENTITY_SECRET", "fixed-secret-for-test")

	dispatcher := &identityHookCapturingDispatcher{}
	d := identityWiredDeps(t, externalID)
	d.CanonicalDispatcher = dispatcher

	// 反转号只接受官方客户端:请求带 Claude Code UA 过鉴真门,再验证 HCSF 钩子改写。
	rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions",
		`{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{"User-Agent": "claude-cli/2.1.78 (external, cli)"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if dispatcher.calls != 1 {
		t.Fatalf("canonical dispatcher calls = %d; want 1", dispatcher.calls)
	}
	if !dispatcher.hookNonNil {
		t.Fatalf("HCSF 路未接改写钩子:HCSFDispatchInput.IdentityRewrite 为 nil —— dispatch.go 接线疑似缺失")
	}
	// 无 metadata 的 anthropic body 经钩子后必须冒出 metadata + 含上游 id 的 user_id。
	// (用包含式断言,对 legacy / JSON 两种 user_id 形态都成立。)
	gotAccountUUID := extractMetadataUserIDAccountComponent(t, dispatcher.rewrittenOut)
	if gotAccountUUID != externalID {
		t.Fatalf("HCSF 钩子改写后 account 组件应为上游 id %q,实际 %q\nbody=%s", externalID, gotAccountUUID, dispatcher.rewrittenOut)
	}
}

// extractMetadataUserIDAccountComponent 从 body.metadata.user_id 解出 account 组件,
// 兼容 legacy("user_<hex>_account_<id>_session_<uuid>")与 JSON 两种形态。
func extractMetadataUserIDAccountComponent(t *testing.T, body []byte) string {
	t.Helper()
	var outer struct {
		Metadata *struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &outer); err != nil {
		t.Fatalf("解析 metadata 失败: %v\nbody=%s", err, body)
	}
	if outer.Metadata == nil || outer.Metadata.UserID == "" {
		t.Fatalf("HCSF 改写后 body 缺 metadata.user_id(钩子未注入?):%s", body)
	}
	uid := outer.Metadata.UserID
	if strings.HasPrefix(strings.TrimSpace(uid), "{") {
		var inner struct {
			AccountUUID string `json:"account_uuid"`
		}
		if err := json.Unmarshal([]byte(uid), &inner); err != nil {
			t.Fatalf("解析 JSON user_id 失败: %v (user_id=%q)", err, uid)
		}
		return inner.AccountUUID
	}
	// legacy 形:取 "_account_" 与下一个 "_session_" 之间的段。
	const accMark, sessMark = "_account_", "_session_"
	i := strings.Index(uid, accMark)
	j := strings.Index(uid, sessMark)
	if i < 0 || j < 0 || j < i {
		t.Fatalf("legacy user_id 形态异常,无法定位 account 组件:%q", uid)
	}
	return uid[i+len(accMark) : j]
}

func TestChatCompletionsClientAdapter_StreamTrueBypassesCanonicalDispatcher(t *testing.T) {
	enableHCSFDispatchForTest(t)
	dispatcher := &mockCanonicalBufferedDispatcher{err: errors.New("canonical path must not run for stream=true")}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if dispatcher.calls != 0 {
		t.Fatalf("canonical dispatcher calls = %d; want 0", dispatcher.calls)
	}
	if rec.Code == http.StatusNotFound || strings.Contains(rec.Body.String(), "unknown_route") {
		t.Fatalf("stream=true must not use non-streaming route lookup; status=%d body=%s", rec.Code, rec.Body.String())
	}
}
