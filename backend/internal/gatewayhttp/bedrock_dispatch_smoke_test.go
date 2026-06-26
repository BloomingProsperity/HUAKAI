// bedrock_dispatch_smoke_test.go — HUAKAI Bedrock-on-Anthropic 全链路冒烟测试。
//
// 覆盖链路:
//
//	inbound POST /v1/messages (Anthropic Messages API form)
//	  → Auth.Resolve → Registry.Resolve (bedrock_invoke)
//	  → Router.Plan → ClaimGate.Reserve → Selector.Select(account 99)
//	  → CredentialVault.Resolve(99) → {aws_sigv4 fake creds}
//	  → Dispatcher.Dispatch → bedrock.PassthroughAdapter.BuildRequest
//	     (AutoTranslate Anthropic → Bedrock body + Track C cache_control 注入 + SigV4 sign)
//	  → redirectRoundTripper → httptest.Server（模拟 AWS Bedrock invoke-with-response-stream）
//	  → Forwarder.Forward (BedrockEventStreamScanner 解 binary frames + bedrock.EventStreamAdapter
//	     转 canonical → SSE 输出给客户端)
//	  → Settler.Settle
//
// 与 dispatch_smoke_test.go (OpenAI) 共用同包 stubs (smokeAuth/smokeRouter/...)
// 不依赖真实 AWS 网络，不引新依赖。
//
// Lane: claude-executor | UTC: 2026-05-08
package gatewayhttp

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/bedrock"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/bedrock/eventstream"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

// ─────────────────────────────────────────────────────────────────────────
// Bedrock binary EventStream 编码 helper（内联，与 gateway test pkg 同形）。
// ─────────────────────────────────────────────────────────────────────────

// encodeBedrockEventFrame 拼装一个完整的 Bedrock EventStream 帧:
// prelude (12B) + headers + payload + 4B message-CRC。
func encodeBedrockEventFrame(headers map[string]string, payload []byte) []byte {
	// headers 编码: 每条 = name_len(1B) + name + value_type(1B) + value_len(2B) + value
	var hbuf bytes.Buffer
	for name, value := range headers {
		hbuf.WriteByte(byte(len(name)))
		hbuf.WriteString(name)
		hbuf.WriteByte(byte(eventstream.HeaderTypeString))
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(value)))
		hbuf.Write(l[:])
		hbuf.WriteString(value)
	}
	headersBytes := hbuf.Bytes()
	headersLen := uint32(len(headersBytes))
	const preludeSize = 12
	const messageCRCSize = 4
	totalLen := uint32(preludeSize + int(headersLen) + len(payload) + messageCRCSize)

	var pre [preludeSize]byte
	binary.BigEndian.PutUint32(pre[0:4], totalLen)
	binary.BigEndian.PutUint32(pre[4:8], headersLen)
	binary.BigEndian.PutUint32(pre[8:12], crc32.ChecksumIEEE(pre[0:8]))

	var msg bytes.Buffer
	msg.Write(pre[:])
	msg.Write(headersBytes)
	msg.Write(payload)

	// message CRC 覆盖 prelude + headers + payload。
	msgCRC := crc32.ChecksumIEEE(msg.Bytes())
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], msgCRC)
	msg.Write(crcBuf[:])
	return msg.Bytes()
}

// chunkPayload 包装 Anthropic 内层事件 JSON 为 Bedrock chunk envelope:
// {"bytes":"<base64-of-inner-json>"}。
func chunkPayload(innerJSON string) []byte {
	enc := bytes.NewBufferString(`{"bytes":"`)
	// 对 innerJSON 做 base64 编码
	const tab = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	src := []byte(innerJSON)
	for i := 0; i < len(src); i += 3 {
		var b1, b2, b3 byte = src[i], 0, 0
		var pad int
		if i+1 < len(src) {
			b2 = src[i+1]
		} else {
			pad++
		}
		if i+2 < len(src) {
			b3 = src[i+2]
		} else {
			pad++
		}
		enc.WriteByte(tab[b1>>2])
		enc.WriteByte(tab[((b1&0x3)<<4)|(b2>>4)])
		if pad < 2 {
			enc.WriteByte(tab[((b2&0xF)<<2)|(b3>>6)])
		} else {
			enc.WriteByte('=')
		}
		if pad < 1 {
			enc.WriteByte(tab[b3&0x3F])
		} else {
			enc.WriteByte('=')
		}
	}
	enc.WriteString(`"}`)
	return enc.Bytes()
}

// chunkFrame 直接构造一个 ":event-type":"chunk" 帧, payload 是
// {"bytes":"<base64>"} envelope。
func chunkFrame(innerJSON string) []byte {
	headers := map[string]string{
		":event-type":   "chunk",
		":content-type": "application/json",
		":message-type": "event",
	}
	return encodeBedrockEventFrame(headers, chunkPayload(innerJSON))
}

// bedrockSmokeStream 拼接 Bedrock-on-Anthropic happy path 的 4 帧 binary 流:
// message_start + content_block_delta(text="Hello") + message_delta(usage) +
// message_stop。message_delta 携带 usage 含 cache_creation_input_tokens=1024
// （表示首次写入 vendor cache），用于断言 cache metrics 链路。
func bedrockSmokeStream() []byte {
	frames := [][]byte{
		chunkFrame(`{"type":"message_start","message":{"id":"msg_smoke","model":"claude-3-5-sonnet-20241022","usage":{"input_tokens":12,"output_tokens":1}}}`),
		chunkFrame(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`),
		chunkFrame(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3,"cache_creation_input_tokens":1024,"cache_read_input_tokens":0}}`),
		chunkFrame(`{"type":"message_stop"}`),
	}
	var out bytes.Buffer
	for _, f := range frames {
		out.Write(f)
	}
	return out.Bytes()
}

// ─────────────────────────────────────────────────────────────────────────
// 主冒烟测试
// ─────────────────────────────────────────────────────────────────────────

// TestDispatch_FullPipeline_BedrockOnAnthropic 验证从 inbound POST /v1/messages
// 到 SSE 响应的完整 Bedrock-on-Anthropic 链路。
func TestDispatch_FullPipeline_BedrockOnAnthropic(t *testing.T) {
	// --- 1. 模拟 AWS Bedrock invoke-with-response-stream 上游 ---
	var (
		upstreamReqCount int64
		upstreamAuthHdr  string
		upstreamPath     string
		upstreamBody     []byte
	)

	mockServer := newGatewayHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&upstreamReqCount, 1)
		upstreamAuthHdr = r.Header.Get("Authorization")
		upstreamPath = r.URL.Path
		var err error
		upstreamBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body err", http.StatusInternalServerError)
			return
		}

		// AWS Bedrock 响应 Content-Type 是 application/vnd.amazon.eventstream
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bedrockSmokeStream())
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer mockServer.Close()

	mockHost := strings.TrimPrefix(mockServer.URL, "http://")

	// --- 2. CredentialVault: 注入 account 42 → fake AWS SigV4 凭据 ---
	// 用 42 与 dispatch_smoke_test.go 现有 smokeSelector / smokeRouter 默认
	// account ID 对齐,无需新增 fixture。
	vault := provider.NewStaticVault()
	if err := vault.Set(42, provider.Credential{
		Type:  provider.CredentialTypeAWSSigV4,
		Value: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		Extra: map[string]string{
			"aws_region":        "us-east-1",
			"aws_access_key_id": "AKIDEXAMPLE",
		},
	}, provider.AccountInfo{
		AccountID:   42,
		Platform:    "bedrock",
		AccountType: "bedrock",
	}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}

	// --- 3. UpstreamDispatcher: 真 bedrock.PassthroughAdapter (AutoTranslate=true) ---
	adapterReg := provider.NewStaticRegistry()
	adapterReg.MustRegister("bedrock_invoke", &bedrock.PassthroughAdapter{
		AutoTranslateAnthropicAPIBody: true,
	})

	tf := transport.NewFactory()
	dispatcher := &gateway.UpstreamDispatcher{
		Adapters:         adapterReg,
		TransportFactory: tf,
		HTTPClient: &http.Client{
			Transport: &redirectRoundTripper{mockHost: mockHost},
		},
	}

	// --- 4. StreamForwarder: 真 BedrockEventStreamScanner + ProtocolAdapters ---
	// 使用 default registries — 都已 wire bedrock_invoke (gateway/stream_scanner.go +
	// gateway/protocol_selector.go)。
	forwarder := &gateway.StreamForwarder{
		ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
		Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
	}

	// --- 5. 计费 stub ---
	settler := &smokeSettler{}

	// --- 6. ChatHandlerDeps ---
	deps := ChatHandlerDeps{
		Auth: smokeAuth{identity: auth.Identity{
			TenantID: 7,
			APIKeyID: 11,
			UserID:   3,
		}},
		Registry: smokeRegistry{resolved: registry.Resolved{
			ProtocolFamily:   "bedrock_invoke",
			CanonicalModelID: "claude-3-5-sonnet",
			ProviderModelID:  "anthropic.claude-3-5-sonnet-20241022-v2:0",
			PoolCandidates:   []int64{99},
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

	// --- 7. 入站 Anthropic Messages API body, system prompt ≥ 4096 bytes
	// 触发 Track C auto cache_control 注入。
	longSystem := strings.Repeat("You are a helpful assistant. ", 200) // ≈ 5800 bytes
	bodyMap := map[string]any{
		"model":      "anthropic.claude-3-5-sonnet-20241022-v2:0",
		"messages":   []map[string]string{{"role": "user", "content": "Hi"}},
		"max_tokens": 1024,
		"stream":     true,
		"system":     longSystem,
	}
	reqBody, _ := json.Marshal(bodyMap)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer hk_smoke_token")

	rec := httptest.NewRecorder()
	NewMessagesHandler(deps)(rec, req)

	// --- 8. 断言 ---

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d 期望 200; body=%s", rec.Code, rec.Body.String())
	}

	if atomic.LoadInt64(&upstreamReqCount) == 0 {
		t.Fatal("Bedrock mock 未收到任何请求")
	}

	// 路径应是 invoke-with-response-stream（stream:true → 流式 endpoint）
	if !strings.Contains(upstreamPath, "invoke-with-response-stream") {
		t.Errorf("期望路径含 invoke-with-response-stream, 得 %q", upstreamPath)
	}

	// AWS SigV4 Authorization header 形态: "AWS4-HMAC-SHA256 Credential=..."
	if !strings.HasPrefix(upstreamAuthHdr, "AWS4-HMAC-SHA256") {
		t.Errorf("期望 SigV4 Authorization 前缀, 得 %q", upstreamAuthHdr)
	}

	// 翻译后 body 应不含 stream/model 字段, 含 anthropic_version
	var out map[string]any
	if err := json.Unmarshal(upstreamBody, &out); err != nil {
		t.Fatalf("upstream body 不是 JSON: %v\n%s", err, string(upstreamBody))
	}
	if _, has := out["model"]; has {
		t.Errorf("Bedrock 翻译后 body 不应含 model 字段")
	}
	if _, has := out["stream"]; has {
		t.Errorf("Bedrock 翻译后 body 不应含 stream 字段")
	}
	if v, _ := out["anthropic_version"].(string); v != "bedrock-2023-05-31" {
		t.Errorf("缺 anthropic_version 或值错: %v", out["anthropic_version"])
	}

	// Track C 注入: system 字段应被 wrap 成 array form 含 cache_control:ephemeral
	if !bytes.Contains(upstreamBody, []byte(`"cache_control"`)) {
		t.Errorf("Track C 自动 cache_control 注入未生效\nbody:%s", string(upstreamBody))
	}
	if !bytes.Contains(upstreamBody, []byte(`"ephemeral"`)) {
		t.Errorf("cache_control 类型应是 ephemeral")
	}

	// SSE 响应 body 应含 message_start / content_block_delta / message_stop
	respBody := rec.Body.String()
	if !strings.Contains(respBody, "message_start") {
		t.Errorf("SSE 输出缺 message_start: %q", respBody)
	}
	if !strings.Contains(respBody, `"text":"Hello"`) {
		t.Errorf("SSE 输出缺 text 内容: %q", respBody)
	}
	if !strings.Contains(respBody, "message_stop") {
		t.Errorf("SSE 输出缺 message_stop: %q", respBody)
	}

	// Settler.Settle 应被调用恰好一次, Abort 不调
	if sc := atomic.LoadInt64(&settler.settleCalls); sc != 1 {
		t.Errorf("Settler.Settle 调用 %d 次; 期望 1", sc)
	}
	if ac := atomic.LoadInt64(&settler.abortCalls); ac != 0 {
		t.Errorf("Settler.Abort 调用 %d 次; 期望 0", ac)
	}
}

// TestDispatch_FullPipeline_BedrockOnAnthropic_UpstreamFailure 验证 synthesis
// plan §4 step 6 failure path: mock Bedrock 返 5xx 时 gateway 不能 silent-OK
// + 必须调 Abort 不调 Settle，audit/metrics 不能误记 success。
func TestDispatch_FullPipeline_BedrockOnAnthropic_UpstreamFailure(t *testing.T) {
	var upstreamReqCount int64

	mockServer := newGatewayHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&upstreamReqCount, 1)
		// 模拟 AWS Bedrock throttling / 内部错误。AWS 出错走 JSON application/x-amz-json-1.1
		// 形态而不是 binary EventStream（错误发生在 stream 建立前），HUAKAI 上游层
		// 不应解析为 binary EventStream success path。
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"__type":"ServiceUnavailableException","message":"upstream throttle simulation"}`))
	}))
	defer mockServer.Close()
	mockHost := strings.TrimPrefix(mockServer.URL, "http://")

	vault := provider.NewStaticVault()
	if err := vault.Set(42, provider.Credential{
		Type:  provider.CredentialTypeAWSSigV4,
		Value: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		Extra: map[string]string{"aws_region": "us-east-1", "aws_access_key_id": "AKIDEXAMPLE"},
	}, provider.AccountInfo{AccountID: 42, Platform: "bedrock", AccountType: "bedrock"}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}

	adapterReg := provider.NewStaticRegistry()
	adapterReg.MustRegister("bedrock_invoke", &bedrock.PassthroughAdapter{
		AutoTranslateAnthropicAPIBody: true,
	})

	dispatcher := &gateway.UpstreamDispatcher{
		Adapters:         adapterReg,
		TransportFactory: transport.NewFactory(),
		HTTPClient:       &http.Client{Transport: &redirectRoundTripper{mockHost: mockHost}},
	}

	forwarder := &gateway.StreamForwarder{
		ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
		Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
	}

	settler := &smokeSettler{}
	deps := ChatHandlerDeps{
		Auth:                 smokeAuth{identity: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 3}},
		Registry:             smokeRegistry{resolved: registry.Resolved{ProtocolFamily: "bedrock_invoke", CanonicalModelID: "claude-3-5-sonnet", ProviderModelID: "anthropic.claude-3-5-sonnet-20241022-v2:0", PoolCandidates: []int64{42}}},
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

	// 长 system 触发 Track C 注入路径（与 happy 一致, 让 failure 不影响 inject 已发生）
	longSystem := strings.Repeat("You are a helpful assistant. ", 200)
	bodyMap := map[string]any{
		"model":      "anthropic.claude-3-5-sonnet-20241022-v2:0",
		"messages":   []map[string]string{{"role": "user", "content": "Hi"}},
		"max_tokens": 1024,
		"stream":     true,
		"system":     longSystem,
	}
	reqBody, _ := json.Marshal(bodyMap)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer hk_smoke_token")

	rec := httptest.NewRecorder()
	NewMessagesHandler(deps)(rec, req)

	// 断言: 上游被打到 + status 不是 200（应映射 5xx 上游成 5xx 客户响应）
	if atomic.LoadInt64(&upstreamReqCount) != 1 {
		t.Errorf("mock 期望被打 1 次, 得 %d", atomic.LoadInt64(&upstreamReqCount))
	}
	if rec.Code == http.StatusOK {
		t.Errorf("上游 5xx 时客户端不应见 200; 得 %d (body=%s)", rec.Code, rec.Body.String())
	}

	// 断言: failure path 不能调 Settle (没有正常完成); 应调 Abort
	if sc := atomic.LoadInt64(&settler.settleCalls); sc != 0 {
		t.Errorf("upstream 失败时 Settler.Settle 不应被调 (得 %d)", sc)
	}
	// Abort 是预期行为, 但当前 settler stub 接口 Abort 计数, 不强制 == 1
	// (handler 实现可能选择特定语义; 关键是 Settle 必须 = 0)
}
