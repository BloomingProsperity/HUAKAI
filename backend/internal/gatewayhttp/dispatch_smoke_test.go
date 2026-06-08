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
//	  → Forwarder.Forward（解析 OpenAI SSE usage/content，写回 SSE body）
//	  → Settler.Settle
//
// Lane: claude-executor | Agent: claude-executor (Sonnet 4.6) | UTC: 2026-05-06
package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
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
	mu          sync.Mutex
	settleCalls int64
	abortCalls  int64
	settleReq   billing.SettleRequest
}

func (s *smokeSettler) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	atomic.AddInt64(&s.settleCalls, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settleReq = req
	return &billing.SettleResult{}, nil
}

func (s *smokeSettler) Abort(_ context.Context, _, _ int64, _, _ string, _ int64, _ json.RawMessage) error {
	atomic.AddInt64(&s.abortCalls, 1)
	return nil
}

func (s *smokeSettler) CommitCacheHit(_ context.Context, _ billing.SettleRequest) error {
	return nil
}

func (s *smokeSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return &billing.RefundResult{}, nil
}

func (s *smokeSettler) lastSettleRequest() billing.SettleRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settleReq
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
	base     http.RoundTripper
}

func (rt *redirectRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// 克隆请求，避免修改原始 Request（Go HTTP 规范要求 RoundTripper 不修改入参）。
	clone := req.Clone(req.Context())
	// 重写目标 host 和 scheme，指向 httptest.Server。
	clone.URL.Host = rt.mockHost
	clone.URL.Scheme = "http"
	// 清除可能被 http.Client 缓存的 RequestURI（必须为空才能让 Transport 自行组装）。
	clone.RequestURI = ""
	base := rt.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

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
	// ProtocolAdapters：使用默认 registry，让 openai_chat 走真实 stream adapter。
	// 该 adapter 会从 mock 的 OpenAI SSE usage/content 填充 draft token 信号；
	// ClientAdapter 仍为空，因此响应侧保持 raw SSE fallback 的冒烟级断言。
	protoReg := gateway.BuildDefaultProtocolAdapterRegistry()
	forwarder := &gateway.StreamForwarder{
		ProtocolAdapters: protoReg,
		// A1 atomic：Forward 现要求 Scanners 非 nil。注入默认注册表
		// （19 个 family 全 SSE，与本测试期望的 SSE wire 行为一致）。
		Scanners: gateway.BuildDefaultStreamScannerRegistry(),
	}

	// --- 5. 构建计费 stub ---
	settler := &smokeSettler{}
	clientIPResolver, err := clientip.NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("client ip resolver: %v", err)
	}

	// --- 6. 组装 ChatHandlerDeps ---
	deps := ChatHandlerDeps{
		Auth: smokeAuth{identity: auth.Identity{
			TenantID: 7,
			APIKeyID: 11,
			UserID:   3,
		}},
		Registry: smokeRegistry{resolved: registry.Resolved{
			// ProtocolFamily 决定 dispatcher 选 openai.PassthroughAdapter
			// 以及 forwarder 选默认 openai stream adapter。
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
		ClientIPResolver:     clientIPResolver,
	}

	// --- 7. 构建 HTTP 请求并调用 handler ---
	reqBody := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer hk_test_smoke")
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	req.Header.Set("User-Agent", "huakai-origin-audit/1.0")
	req.RemoteAddr = "10.1.2.3:5000"

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

	// 断言 9：mock usage/content 已进入 draft，流式尝试处于可结算状态。
	settleReq := settler.lastSettleRequest()
	if got := settleReq.Draft.TokensInput; got != 10 {
		t.Fatalf("断言9失败：draft.TokensInput = %d；期望 10", got)
	}
	if got := settleReq.Draft.TokensOutput; got != 2 {
		t.Fatalf("断言9失败：draft.TokensOutput = %d；期望 2", got)
	}
	if got := settleReq.Draft.DeliveredTokenCount; got != 2 {
		t.Fatalf("断言9失败：draft.DeliveredTokenCount = %d；期望 2", got)
	}
	if attempt := settleReq.StreamAttempt; attempt == nil || !attempt.State.Chargeable() {
		t.Fatalf("断言9失败：StreamAttempt = %#v；期望 chargeable", attempt)
	}
	if settleReq.Draft.IPAddress == nil || *settleReq.Draft.IPAddress != "198.51.100.9" {
		t.Fatalf("断言10失败：Draft.IPAddress=%v；期望 198.51.100.9", settleReq.Draft.IPAddress)
	}
	if settleReq.Draft.UserAgent == nil || *settleReq.Draft.UserAgent != "huakai-origin-audit/1.0" {
		t.Fatalf("断言10失败：Draft.UserAgent=%v；期望 huakai-origin-audit/1.0", settleReq.Draft.UserAgent)
	}
}

func TestChatCompletionsMixedLoadP95(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mixed-load latency gate in short mode")
	}
	if os.Getenv("HUAKAI_SKIP_PERF_LATENCY_GATE") == "1" {
		t.Skip("HUAKAI_SKIP_PERF_LATENCY_GATE=1; broad race suite skips latency gate")
	}

	// Mutation self-checks for PM shell verification:
	// 1. Wrap the handler call path in a package-global sync.Mutex: wallClock
	//    should grow toward totalRequests*soloLatency, speedup should fall
	//    toward 1, and the speedup >= 4 assertion should fail.
	// 2. Start one goroutine per request without returning it: the post-load
	//    goroutine bound should fail.
	baselineGoroutines := runtime.NumGoroutine()
	harness := newFullChainChatHarness(t)
	defer harness.Close()

	const (
		workers           = 32
		requestsPerWorker = 200
		totalRequests     = workers * requestsPerWorker
		soloRuns          = 20
		soloWarmup        = 3
	)
	const reqBody = `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`

	soloLatencies := make([]time.Duration, 0, soloRuns-soloWarmup)
	for i := 0; i < soloRuns; i++ {
		reqStart := time.Now()
		rec := invokePreparedChatHandlerPath(harness.handler, "/v1/chat/completions", reqBody)
		elapsed := time.Since(reqStart)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "data: ") {
			t.Fatalf("solo baseline response status=%d body=%q", rec.Code, rec.Body.String())
		}
		if i >= soloWarmup {
			soloLatencies = append(soloLatencies, elapsed)
		}
	}
	sort.Slice(soloLatencies, func(i, j int) bool { return soloLatencies[i] < soloLatencies[j] })
	soloLatency := percentileDuration(soloLatencies, 50)
	atomic.StoreInt64(&harness.settler.settleCalls, 0)
	atomic.StoreInt64(&harness.settler.abortCalls, 0)

	latencies := make([]time.Duration, totalRequests)
	var badResponses int64
	var firstBadMu sync.Mutex
	firstBad := ""

	var ready sync.WaitGroup
	var wg sync.WaitGroup
	start := make(chan struct{})
	ready.Add(workers)
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			for i := 0; i < requestsPerWorker; i++ {
				idx := worker*requestsPerWorker + i
				reqStart := time.Now()
				rec := invokePreparedChatHandlerPath(harness.handler, "/v1/chat/completions", reqBody)
				elapsed := time.Since(reqStart)
				latencies[idx] = elapsed

				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "data: ") {
					atomic.AddInt64(&badResponses, 1)
					firstBadMu.Lock()
					if firstBad == "" {
						firstBad = fmt.Sprintf("status=%d body=%q", rec.Code, rec.Body.String())
					}
					firstBadMu.Unlock()
				}
			}
		}()
	}

	ready.Wait()
	wallStart := time.Now()
	close(start)
	wg.Wait()
	wallClock := time.Since(wallStart)

	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p50 := percentileDuration(sorted, 50)
	p95 := percentileDuration(sorted, 95)
	p99 := percentileDuration(sorted, 99)
	speedup := float64(totalRequests) * float64(soloLatency) / float64(wallClock)
	t.Logf("mixed load: requests=%d workers=%d soloLatency=%s wall=%s speedup=%.2f p50=%s p95=%s p99=%s",
		totalRequests, workers, soloLatency, wallClock, speedup, p50, p95, p99)

	if got := atomic.LoadInt64(&badResponses); got != 0 {
		t.Fatalf("badResponses=%d want 0; first=%s", got, firstBad)
	}
	if got := atomic.LoadInt64(&harness.settler.settleCalls); got != totalRequests {
		t.Fatalf("settleCalls=%d want %d", got, totalRequests)
	}
	if speedup < 1.5 {
		t.Fatalf("speedup=%.2f want >= 1.5 (soloLatency=%s wallClock=%s)", speedup, soloLatency, wallClock)
	}
	if p95 >= 200*time.Millisecond {
		t.Fatalf("p95=%s want < 200ms", p95)
	}

	harness.Close()
	assertGoroutinesWithin(t, baselineGoroutines, 20)
}

func BenchmarkChatCompletionsFullChain(b *testing.B) {
	harness := newFullChainChatHarness(b)
	defer harness.Close()

	const reqBody = `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := invokePreparedChatHandlerPath(harness.handler, "/v1/chat/completions", reqBody)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "data: ") {
			b.Fatalf("response status=%d body=%q", rec.Code, rec.Body.String())
		}
	}
	b.StopTimer()
	if got := atomic.LoadInt64(&harness.settler.settleCalls); got != int64(b.N) {
		b.Fatalf("settleCalls=%d want %d", got, b.N)
	}
}

type fullChainChatHarness struct {
	handler   http.HandlerFunc
	server    *httptest.Server
	transport *http.Transport
	settler   *smokeSettler
	closed    int32
}

func newFullChainChatHarness(tb testing.TB) *fullChainChatHarness {
	tb.Helper()

	mockServer := newGatewayHTTPTestServer(tb, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			http.Error(w, "read body error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		writeOpenAIChunk(w, `{"id":"chatcmpl-mixed","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`)
		writeOpenAIChunk(w, `{"id":"chatcmpl-mixed","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`)
		writeOpenAIChunk(w, `{"id":"chatcmpl-mixed","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))

	mockHost := strings.TrimPrefix(mockServer.URL, "http://")
	vault := provider.NewStaticVault()
	if err := vault.Set(42, provider.Credential{
		Type:  provider.CredentialTypeAPIKey,
		Value: "sk-fake",
	}, provider.AccountInfo{
		AccountID:   42,
		Platform:    "openai",
		AccountType: "apikey",
	}); err != nil {
		tb.Fatalf("vault.Set: %v", err)
	}

	adapterReg := provider.NewStaticRegistry()
	adapterReg.MustRegister("openai_chat", &openaiPassthroughAdapter{})

	baseTransport := &http.Transport{}
	dispatcher := &gateway.UpstreamDispatcher{
		Adapters:         adapterReg,
		TransportFactory: transport.NewFactory(),
		HTTPClient: &http.Client{
			Transport: &redirectRoundTripper{mockHost: mockHost, base: baseTransport},
		},
	}
	forwarder := &gateway.StreamForwarder{
		ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
		Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
	}
	settler := &smokeSettler{}
	deps := ChatHandlerDeps{
		Auth: smokeAuth{identity: auth.Identity{
			TenantID: 7,
			APIKeyID: 11,
			UserID:   3,
		}},
		Registry: smokeRegistry{resolved: registry.Resolved{
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

	return &fullChainChatHarness{
		handler:   NewChatCompletionsHandler(deps),
		server:    mockServer,
		transport: baseTransport,
		settler:   settler,
	}
}

func (h *fullChainChatHarness) Close() {
	if h == nil || !atomic.CompareAndSwapInt32(&h.closed, 0, 1) {
		return
	}
	if h.transport != nil {
		h.transport.CloseIdleConnections()
	}
	if h.server != nil {
		h.server.Close()
	}
}

func invokePreparedChatHandlerPath(handler http.HandlerFunc, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer hk_test_smoke")
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func percentileDuration(sorted []time.Duration, pct int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := (len(sorted)*pct + 99) / 100
	if idx <= 0 {
		idx = 1
	}
	if idx > len(sorted) {
		idx = len(sorted)
	}
	return sorted[idx-1]
}

func assertGoroutinesWithin(t *testing.T, baseline, slack int) {
	t.Helper()
	deadline := time.Now().Add(750 * time.Millisecond)
	for {
		runtime.GC()
		got := runtime.NumGoroutine()
		if got <= baseline+slack {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines=%d want <= baseline(%d)+%d", got, baseline, slack)
		}
		time.Sleep(25 * time.Millisecond)
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
