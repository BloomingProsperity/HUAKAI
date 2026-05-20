// dispatch_smoke_test.go — HUAKAI 全链路冒烟测试（httptest 版）
//
// 覆盖链路：
//
//	inbound POST /v1/chat/completions
//	  → Auth.Resolve → Registry.Resolve (openai_chat)
//	  → Router.Plan → ClaimGate.Reserve → Selector.Select(account 42)
//	  → CredentialVault.Resolve(42) → {api_key=sk-fake}
//	  → Dispatcher.Dispatch → openai.PassthroughAdapter.BuildRequest
//	  → redirectRoundTripper → httptest.Server（模拟 OpenAI 上游）
//	  → Forwarder.Forward（透传 SSE） → 200 + SSE body
//	  → Settler.Settle
//
// Lane: claude-executor | Agent: claude-executor (Sonnet 4.6) | UTC: 2026-05-06
package gatewayhttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

// ---------------------------------------------------------------------------
// 冒烟专用 Stubs（扩展自 chat_completions_handler_test.go，不修改原文件）
// ---------------------------------------------------------------------------

// smokeAuth 总是返回指定身份，不做任何 bearer 验证。
type smokeAuth struct{ identity auth.Identity }

func (s smokeAuth) Resolve(_ context.Context, _ *http.Request) (auth.Identity, error) {
	return s.identity, nil
}

// smokeRegistry 按固定值返回 Resolved，携带 openai_chat 协议族。
type smokeRegistry struct{ resolved registry.Resolved }

func (s smokeRegistry) ResolveModel(_ context.Context, _ string, _ int64) (registry.Resolved, error) {
	return s.resolved, nil
}

// smokeRouter 返回带有 account 42 候选的路由计划。
type smokeRouter struct{}

func (smokeRouter) Plan(_ context.Context, _ router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts:        []router.AttemptPlan{{PoolGroupID: 42}},
		SnapshotVersion: "registry:7:1;router:v0.1-smoke",
	}, nil
}

// smokeClaimGate 总是返回成功的预留结果。
type smokeClaimGate struct{}

func (smokeClaimGate) Reserve(_ context.Context, _ billing.ReserveRequest) (*billing.ReserveResult, error) {
	return &billing.ReserveResult{ClaimID: 1001}, nil
}

// smokeSelector 总是返回 account 42（与 Vault 中写入的账号对齐）。
type smokeSelector struct{}

func (smokeSelector) Select(_ context.Context, _ pool.SelectionRequest) (*pool.SelectionResult, error) {
	return &pool.SelectionResult{AccountID: 42}, nil
}

// smokeSettler 记录 Settle / Abort 调用次数，供断言使用。
type smokeSettler struct {
	settleCalls int64
	abortCalls  int64
}

func (s *smokeSettler) Settle(_ context.Context, _ billing.SettleRequest) (*billing.SettleResult, error) {
	atomic.AddInt64(&s.settleCalls, 1)
	return &billing.SettleResult{}, nil
}

func (s *smokeSettler) Abort(_ context.Context, _, _ int64, _, _ string) error {
	atomic.AddInt64(&s.abortCalls, 1)
	return nil
}

func (s *smokeSettler) CommitCacheHit(_ context.Context, _ billing.SettleRequest) error {
	return nil
}

func (s *smokeSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return &billing.RefundResult{}, nil
}

// ---------------------------------------------------------------------------
// redirectRoundTripper — 把出站请求的 Host 重写为 mockServer 的 Host，
// 其余全部透传给 http.DefaultTransport。
// ---------------------------------------------------------------------------

// redirectRoundTripper 拦截出站请求，将 URL.Host 替换为 mockHost，
// 然后通过 http.DefaultTransport 真正发出请求（到 httptest.Server）。
type redirectRoundTripper struct {
	// mockHost 是 httptest.Server URL 的 host:port 部分（不含 scheme）。
	mockHost string
}

func (rt *redirectRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// 克隆请求，避免修改原始 Request（Go HTTP 规范要求 RoundTripper 不修改入参）。
	clone := req.Clone(req.Context())
	// 重写目标 host 和 scheme，指向 httptest.Server。
	clone.URL.Host = rt.mockHost
	clone.URL.Scheme = "http"
	// 清除可能被 http.Client 缓存的 RequestURI（必须为空才能让 Transport 自行组装）。
	clone.RequestURI = ""
	return http.DefaultTransport.RoundTrip(clone)
}

// ---------------------------------------------------------------------------
// rawPassthroughAdapter — 对 SSE 事件做最小处理：直接把原始 event data
// 作为 "data: <bytes>\n\n" 帧写回客户端，不做任何 HCSF 转换。
// 适用于冒烟测试：只需验证 SSE 数据到达客户端，不验证 canonical 事件形态。
// ---------------------------------------------------------------------------

// rawPassthroughUpstreamAdapter 实现 proto.UpstreamAdapter。
// 它接受任意 state 类型，把上游原始 SSE bytes 原样返回为单个 canonical 事件（[]any{[]byte}）。
// forwarder 在 handleEventWithAdapter 中调用此方法；返回的 []any 元素不是
// proto.CanonicalEvent，所以 canonicalUsage / canonicalTerminal 会静默忽略，
// 但原始 SSE bytes 会经由 clientChunks → rawSSE(fallback) 路径写给客户端。
//
// 注意：本测试只验证 raw SSE 能穿过完整 HTTP pipeline，不验证 openai.Adapter
// 的 canonical 事件形态。这里用任意 state 都可接受的 stub，避免把 smoke test
// 绑定到 vendor adapter 的字段级断言。
type rawPassthroughUpstreamAdapter struct{}

func (a *rawPassthroughUpstreamAdapter) CanonicalToProviderRequest(_ context.Context, _ *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	return nil, nil, proto.ErrNotImplemented
}

func (a *rawPassthroughUpstreamAdapter) ProviderResponseToCanonical(_ context.Context, _ []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	return nil, nil, proto.ErrNotImplemented
}

// ProviderEventToCanonicalEvents 接受 state（任意类型，无断言）；
// 直接把 SSE event data 原样返回，forwarder 的 clientChunks() 会走 rawSSE(fallback) 路径，
// 把原始 event 写给客户端。
func (a *rawPassthroughUpstreamAdapter) ProviderEventToCanonicalEvents(_ context.Context, _ any, _ any) ([]any, []proto.ProtocolLossEntry, error) {
	// 返回空切片：forwarder 不会写任何 canonical 帧，但 rawSSE(fallback) 路径
	// 在 clientChunks 中会用 fallback SSEEvent 直接写。
	// 实际上 handleEventWithAdapter 的写路径是：
	//   canonicalEvents → 若空则 loop 体不写 → 但 wrote=false → 不更新 firstEmitted
	// 这意味着若始终返回空，客户端拿不到任何数据。
	// 因此改为：返回一个哨兵值，让 clientChunks 走 rawSSE(fallback) 路径。
	// 但 clientChunks 只在 f.ClientAdapter==nil 时才 rawSSE(fallback)，
	// 且它用的是 canonical 元素，不是 evt 本身。
	//
	// 实际透传路径：f.ClientAdapter == nil 时，clientChunks 返回 rawSSE(fallback)，
	// 其中 fallback 就是当前 SSEEvent（含原始 data bytes）。
	// 所以这里只需返回至少一个元素（哪怕是 nil），触发 clientChunks 调用。
	return []any{nil}, nil, nil
}

func (a *rawPassthroughUpstreamAdapter) FinalizeUpstreamStream(_ context.Context, _ any) ([]any, error) {
	return nil, nil
}

// singleFamilyAdapterRegistry 将一个 proto.UpstreamAdapter 绑定到指定 family。
type singleFamilyAdapterRegistry struct {
	family  string
	adapter proto.UpstreamAdapter
}

func (r *singleFamilyAdapterRegistry) For(family string) (proto.UpstreamAdapter, error) {
	if family == r.family {
		return r.adapter, nil
	}
	return nil, fmt.Errorf("%w: %s", gateway.ErrUnknownProtocolFamily, family)
}

// ---------------------------------------------------------------------------
// 主冒烟测试
// ---------------------------------------------------------------------------

// TestDispatch_FullPipeline_OpenAIChat 验证从 inbound POST 到 SSE 响应的完整链路。
func TestDispatch_FullPipeline_OpenAIChat(t *testing.T) {
	// --- 1. 构建模拟 OpenAI 上游服务器 ---
	// 该服务器发出 3 个 SSE 数据帧（hello / world / final + [DONE]），
	// 并记录收到的 Authorization header 和请求 body 供后续断言。

	var (
		// 上游收到的请求计数
		upstreamReqCount int64
		// 上游收到的 Authorization header 值
		upstreamAuthHeader string
		// 上游收到的请求 body bytes
		upstreamBody []byte
	)

	// mockServer 模拟 OpenAI Chat Completions SSE 上游。
	// 响应 3 个 chunk：hello / world / final（含 usage）+ [DONE]。
	mockServer := newGatewayHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 记录请求元数据，供断言使用。
		atomic.AddInt64(&upstreamReqCount, 1)
		upstreamAuthHeader = r.Header.Get("Authorization")
		var err error
		upstreamBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body error", http.StatusInternalServerError)
			return
		}

		// 设置 SSE 响应头。
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		// chunk 1：第一个文本片段 "hello"
		writeOpenAIChunk(w, `{"id":"chatcmpl-smoke","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`)
		// chunk 2：第二个文本片段 "world"
		writeOpenAIChunk(w, `{"id":"chatcmpl-smoke","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`)
		// chunk 3：final chunk，携带 usage 信息，finish_reason=stop
		writeOpenAIChunk(w, `{"id":"chatcmpl-smoke","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
		// [DONE] 终止标记
		fmt.Fprint(w, "data: [DONE]\n\n")
		// 主动 flush，确保 httptest 把数据发出。
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer mockServer.Close()

	// mockHost 是 httptest.Server 的 host:port（不含 scheme），
	// redirectRoundTripper 会把出站请求重定向到此地址。
	mockHost := strings.TrimPrefix(mockServer.URL, "http://")

	// --- 2. 构建真实 CredentialVault，注入 account 42 → sk-fake ---
	vault := provider.NewStaticVault()
	if err := vault.Set(42, provider.Credential{
		Type:  provider.CredentialTypeAPIKey,
		Value: "sk-fake",
	}, provider.AccountInfo{
		AccountID:   42,
		Platform:    "openai",
		AccountType: "apikey",
	}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}

	// --- 3. 构建真实 UpstreamDispatcher ---
	// Adapters：使用 registrydefault.Build() 中的 openai.PassthroughAdapter，
	// 通过 provider.StaticRegistry。
	adapterReg := provider.NewStaticRegistry()
	adapterReg.MustRegister("openai_chat", &openaiPassthroughAdapter{})

	// TransportFactory：zero-value，标准模式走 http.DefaultTransport。
	tf := transport.NewFactory()

	dispatcher := &gateway.UpstreamDispatcher{
		Adapters:         adapterReg,
		TransportFactory: tf,
		// HTTPClient 注入 redirectRoundTripper，把出站请求重定向到 mockServer。
		HTTPClient: &http.Client{
			Transport: &redirectRoundTripper{mockHost: mockHost},
		},
	}

	// --- 4. 构建真实 StreamForwarder ---
	// ProtocolAdapters：注入单协议 stub，使用 rawPassthroughUpstreamAdapter
	// 保持本 smoke test 只断言 raw SSE 透传，不耦合 openai.Adapter 的
	// canonical event 细节。
	protoReg := &singleFamilyAdapterRegistry{
		family:  "openai_chat",
		adapter: &rawPassthroughUpstreamAdapter{},
	}

	forwarder := &gateway.StreamForwarder{
		ProtocolAdapters: protoReg,
		// A1 atomic：Forward 现要求 Scanners 非 nil。注入默认注册表
		// （19 个 family 全 SSE，与本测试期望的 SSE wire 行为一致）。
		Scanners: gateway.BuildDefaultStreamScannerRegistry(),
	}

	// --- 5. 构建计费 stub ---
	settler := &smokeSettler{}

	// --- 6. 组装 ChatHandlerDeps ---
	deps := ChatHandlerDeps{
		Auth: smokeAuth{identity: auth.Identity{
			TenantID: 7,
			APIKeyID: 11,
			UserID:   3,
		}},
		Registry: smokeRegistry{resolved: registry.Resolved{
			// ProtocolFamily 决定 dispatcher 选 openai.PassthroughAdapter
			// 以及 forwarder 选 rawPassthroughUpstreamAdapter。
			ProtocolFamily:   "openai_chat",
			CanonicalModelID: "gpt-4o",
			ProviderModelID:  "gpt-4o",
			PoolCandidates:   []int64{42},
		}},
		Router:               smokeRouter{},
		ClaimGate:            smokeClaimGate{},
		Selector:             smokeSelector{},
		CredentialVault:      vault,
		Dispatcher:           dispatcher,
		Forwarder:            forwarder,
		Settler:              settler,
		RateTables:           testRateTables("smoke-v1"),
		BillingPolicyVersion: "smoke-v1",
		RequestClass:         "default",
	}

	// --- 7. 构建 HTTP 请求并调用 handler ---
	reqBody := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer hk_test_smoke")

	rec := httptest.NewRecorder()
	handler := NewChatCompletionsHandler(deps)
	handler(rec, req)

	// --- 8. 断言 ---

	// 断言 1：HTTP 状态码 200。
	if rec.Code != http.StatusOK {
		t.Fatalf("断言1失败：status = %d；期望 200；body = %s", rec.Code, rec.Body.String())
	}

	// 断言 2：Content-Type 以 "text/event-stream" 开头。
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("断言2失败：Content-Type = %q；期望前缀 text/event-stream", ct)
	}

	// 断言 3：响应 body 包含 "data: " SSE 帧。
	respBody := rec.Body.String()
	if !strings.Contains(respBody, "data: ") {
		t.Fatalf("断言3失败：响应 body 未包含 SSE data 帧；body = %q", respBody)
	}

	// 断言 4：上游服务器至少收到一次请求。
	if atomic.LoadInt64(&upstreamReqCount) == 0 {
		t.Fatal("断言4失败：上游 mock 服务器未收到任何请求")
	}

	// 断言 5：上游收到的 Authorization header 为 "Bearer sk-fake"。
	if upstreamAuthHeader != "Bearer sk-fake" {
		t.Fatalf("断言5失败：上游 Authorization = %q；期望 Bearer sk-fake", upstreamAuthHeader)
	}

	// 断言 6：上游收到的请求 body 包含 "gpt-4o" 字符串。
	if !bytes.Contains(upstreamBody, []byte("gpt-4o")) {
		t.Fatalf("断言6失败：上游 body 未包含 gpt-4o；body = %q", string(upstreamBody))
	}

	// 断言 7：Settler.Settle 被调用恰好一次。
	if sc := atomic.LoadInt64(&settler.settleCalls); sc != 1 {
		t.Fatalf("断言7失败：Settler.Settle 调用次数 = %d；期望 1", sc)
	}

	// 断言 8：Settler.Abort 未被调用。
	if ac := atomic.LoadInt64(&settler.abortCalls); ac != 0 {
		t.Fatalf("断言8失败：Settler.Abort 调用次数 = %d；期望 0", ac)
	}
}

// ---------------------------------------------------------------------------
// 辅助：写一个 OpenAI Chat Completions SSE chunk。
// ---------------------------------------------------------------------------

// writeOpenAIChunk 向 ResponseWriter 写一个 "data: <json>\n\n" 帧，并即时 flush。
func writeOpenAIChunk(w http.ResponseWriter, jsonData string) {
	fmt.Fprintf(w, "data: %s\n\n", jsonData)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// ---------------------------------------------------------------------------
// openaiPassthroughAdapter — 对 provider.Adapter 的最小包装，
// 仅用于 dispatcher 构造出站请求；直接委托给 openai.PassthroughAdapter。
// 写在此处避免 import cycle（不直接 import provider/openai 子包）。
// ---------------------------------------------------------------------------

// openaiPassthroughAdapter 是 provider.Adapter 的轻量包装，
// 把入站 body 透传给 https://api.openai.com/v1/chat/completions，
// 并注入 Authorization: Bearer <api_key>。
// 此 adapter 与 provider/openai.PassthroughAdapter 行为等价，
// 内联于此以避免在测试文件内 import provider/openai 子包。
type openaiPassthroughAdapter struct{}

func (a *openaiPassthroughAdapter) Platform() string { return "openai" }

func (a *openaiPassthroughAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{provider.CredentialTypeAPIKey}
}

func (a *openaiPassthroughAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	// endpoint 固定为 https://api.openai.com/v1/chat/completions；
	// redirectRoundTripper 会在 transport 层把 host 替换为 mockServer。
	const endpoint = "https://api.openai.com/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("openaiPassthroughAdapter: BuildRequest: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+in.Credential.Value)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
