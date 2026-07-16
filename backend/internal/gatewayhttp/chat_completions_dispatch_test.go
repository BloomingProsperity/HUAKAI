package gatewayhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/affinityrules"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/cache_routing"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	protoanthropic "github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	protoopenai "github.com/BloomingProsperity/HUAKAI/internal/proto/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	provideranthropic "github.com/BloomingProsperity/HUAKAI/internal/provider/anthropic"
	provideropenai "github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

type recordingClaimGate struct {
	endpointFamily string
	req            billing.ReserveRequest
	claimID        int64
}

func (g *recordingClaimGate) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.endpointFamily = req.EndpointFamily
	g.req = req
	claimID := g.claimID
	if claimID == 0 {
		claimID = 999
	}
	return &billing.ReserveResult{ClaimID: claimID, AttemptSeq: 1}, nil
}

type reserveClaimRaceClaimGate struct{}

func (reserveClaimRaceClaimGate) Reserve(context.Context, billing.ReserveRequest) (*billing.ReserveResult, error) {
	return nil, billing.ErrClaimRace
}

func TestHandler_NoStream(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	d := clientAdapterDeps(t)
	rec := invokeHandler(t, d, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_unknown_upstream") {
		t.Fatalf("body = %q; want normalized upstream error", rec.Body.String())
	}
}

func TestHandler_DefaultHCSFOn(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if dispatcher.calls != 1 {
		t.Fatalf("canonical dispatcher calls = %d; want 1", dispatcher.calls)
	}
}

func TestHandler_HCSFUpstreamHTTPErrorDoesNotLeakBody(t *testing.T) {
	enableHCSFDispatchForTest(t)
	const marker = "SENSITIVE_UPSTREAM_MARKER"
	dispatcher := &mockCanonicalBufferedDispatcher{
		err: &gateway.UpstreamHTTPError{
			StatusCode: http.StatusUnauthorized,
			Body:       []byte(`{"error":"invalid_grant","detail":"` + marker + `"}`),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		},
	}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), marker) {
		t.Fatalf("response leaked upstream marker: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream_oauth_invalid_grant") {
		t.Fatalf("body=%s want oauth invalid grant classification code", rec.Body.String())
	}
}

// TestHandler_HCSFOversizedSuccessMapsToTooLarge 守 S2-7 调用方映射(默认 HCSF-on 路径)：
// DispatchHCSF 返回 ErrUpstreamResponseTooLarge 时，client 必须拿 502 + upstream_response_too_large，
// 而非塌成 opaque upstream_dispatch_error；且终止不重试(dispatcher 只被调一次)。
// 变异: 删 chat_completions_dispatch.go 的 errors.Is(ErrUpstreamResponseTooLarge) 分支 →
// 落到 generic upstream_dispatch_error → body 不含 upstream_response_too_large → RED。
func TestHandler_HCSFOversizedSuccessMapsToTooLarge(t *testing.T) {
	enableHCSFDispatchForTest(t)
	dispatcher := &mockCanonicalBufferedDispatcher{
		err: gateway.ErrUpstreamResponseTooLarge,
	}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream_response_too_large") {
		t.Fatalf("body=%s want upstream_response_too_large (not opaque upstream_dispatch_error)", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "upstream_dispatch_error") {
		t.Fatalf("body=%s leaked generic dispatch error; too-large must not collapse to it", rec.Body.String())
	}
	// 终止不重试：too-large 重试只会重新拉同样的超大响应，dispatcher 必须只被调一次。
	if dispatcher.calls != 1 {
		t.Fatalf("DispatchHCSF calls=%d want 1 (too-large is terminal, no retry/fallback)", dispatcher.calls)
	}
}

func TestHandler_EnvOffHCSFOff(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadGateway || dispatcher.calls != 0 {
		t.Fatalf("status/calls = %d/%d; body = %s", rec.Code, dispatcher.calls, rec.Body.String())
	}
}

func TestHandler_AnthropicEndpointFamilySet(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := anthropicClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	body := `{"model":"claude-3-5-sonnet","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	rec := invokeHandlerPath(t, d, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if dispatcher.observed == nil || dispatcher.observed.RequestMeta.EndpointFamily != "anthropic_messages" {
		t.Fatalf("EndpointFamily = %+v", dispatcher.observed)
	}
}

func TestHandler_AnthropicMessagesRawBufferedNoLonger501(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	doer := &anthropicBufferedDoer{body: `{
			"id":"msg_raw_handler",
			"type":"message",
			"role":"assistant",
			"model":"claude-3-5-sonnet",
			"content":[{"type":"text","text":"hello from raw anthropic"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":4,"output_tokens":5}
		}`}

	d := anthropicClientAdapterDeps(t)
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("anthropic_messages", &provideranthropic.PassthroughAdapter{})
	d.Dispatcher = &gateway.UpstreamDispatcher{
		Adapters:         adapters,
		TransportFactory: transport.NewFactory(),
		HTTPClient:       doer,
	}
	d.Forwarder = &gateway.StreamForwarder{ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry()}

	body := `{"model":"claude-3-5-sonnet","stream":false,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	rec := invokeHandlerPath(t, d, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "buffered_anthropic_not_supported") {
		t.Fatalf("handler still returned old 501 marker: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello from raw anthropic") {
		t.Fatalf("body=%s want translated Anthropic response", rec.Body.String())
	}
	if doer.requestPath != "/v1/messages" {
		t.Fatalf("upstream path=%q want /v1/messages", doer.requestPath)
	}
}

func TestHandler_RawBufferedBodyOverLimitIsTypedError(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	doer := &anthropicBufferedDoer{
		body: `{"id":"msg_big","type":"message","role":"assistant","model":"claude-3-5-sonnet","content":[{"type":"text","text":"` +
			strings.Repeat("x", 1<<20) +
			`"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
	}

	d := anthropicClientAdapterDeps(t)
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("anthropic_messages", &provideranthropic.PassthroughAdapter{})
	d.Dispatcher = &gateway.UpstreamDispatcher{
		Adapters:         adapters,
		TransportFactory: transport.NewFactory(),
		HTTPClient:       doer,
	}
	d.Forwarder = &gateway.StreamForwarder{ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry()}

	rec := invokeHandlerPath(t, d, "/v1/messages", `{"model":"claude-3-5-sonnet","stream":false,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream_response_too_large") {
		t.Fatalf("body=%s want typed upstream_response_too_large error", rec.Body.String())
	}
}

func TestReadRawBufferedUpstreamBodyTooLargeReturnsTruncatedBody(t *testing.T) {
	raw, err := readRawBufferedUpstreamBody(strings.NewReader(strings.Repeat("x", maxRawBufferedUpstreamBodyBytes+1)))
	if !errors.Is(err, errRawBufferedUpstreamBodyTooLarge) {
		t.Fatalf("err=%v want errRawBufferedUpstreamBodyTooLarge", err)
	}
	if len(raw) != maxRawBufferedUpstreamBodyBytes {
		t.Fatalf("len(raw)=%d want %d", len(raw), maxRawBufferedUpstreamBodyBytes)
	}
}

func TestHandler_RawBufferedNon2xxBodyOverLimitUsesClassification(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	const marker = "SENSITIVE_TRUNCATED_UPSTREAM_MARKER"
	doer := &anthropicBufferedDoer{
		status: http.StatusTooManyRequests,
		body:   marker + strings.Repeat("x", maxRawBufferedUpstreamBodyBytes+1),
	}

	d := anthropicClientAdapterDeps(t)
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("anthropic_messages", &provideranthropic.PassthroughAdapter{})
	d.Dispatcher = &gateway.UpstreamDispatcher{
		Adapters:         adapters,
		TransportFactory: transport.NewFactory(),
		HTTPClient:       doer,
	}
	d.Forwarder = &gateway.StreamForwarder{ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry()}

	rec := invokeHandlerPath(t, d, "/v1/messages", `{"model":"claude-3-5-sonnet","stream":false,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 classified rate-limit response; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "upstream_response_too_large") {
		t.Fatalf("body=%s must not use terminal too-large error for non-2xx upstream response", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream_rate_limited") {
		t.Fatalf("body=%s want rate-limit classification", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), marker) {
		t.Fatalf("body=%s must not leak truncated upstream marker", rec.Body.String())
	}
}

type anthropicBufferedDoer struct {
	body        string
	status      int
	requestPath string
}

func (d *anthropicBufferedDoer) Do(req *http.Request) (*http.Response, error) {
	d.requestPath = req.URL.Path
	status := d.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(d.body)),
	}, nil
}

type codexSSEDoer struct {
	body        string
	status      int
	requestPath string
	requestBody string
}

const (
	codexForcedStreamingAccountID int64 = 1
	codexLargeReasoningTokens     int   = 6
	codexSmallReasoningTokens     int   = 3
)

var codexForcedStreamingAcquisitionToken = uuid.MustParse("22222222-3333-4444-5555-666666666666")

func (d *codexSSEDoer) Do(req *http.Request) (*http.Response, error) {
	d.requestPath = req.URL.Path
	raw, _ := io.ReadAll(req.Body)
	d.requestBody = string(raw)
	status := d.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(d.body)),
	}, nil
}

type codexForcedStreamingSelector struct{}

func (codexForcedStreamingSelector) Select(_ context.Context, _ pool.SelectionRequest) (*pool.SelectionResult, error) {
	return &pool.SelectionResult{
		AccountID:        codexForcedStreamingAccountID,
		AcquisitionToken: codexForcedStreamingAcquisitionToken,
	}, nil
}

func codexForcedStreamingDeps(t *testing.T, doer *codexSSEDoer, claimGate *recordingClaimGate, settler *recordingSettler) ChatHandlerDeps {
	t.Helper()
	t.Setenv("HUAKAI_TRANSPORT_MIMICRY", "false")
	d := responsesClientAdapterDeps(t)
	d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "gpt-5.5",
		CanonicalModelID: "openai/gpt-5.5",
		ProviderModelID:  "gpt-5.5",
		ProtocolFamily:   "openai_codex",
		PoolCandidates:   []int64{42},
	}}
	vault := provider.NewStaticVault()
	if err := vault.Set(codexForcedStreamingAccountID, provider.Credential{
		Type:  provider.CredentialTypeSessionToken,
		Value: "codex-session-test",
	}, provider.AccountInfo{
		AccountID:           codexForcedStreamingAccountID,
		Platform:            "openai_codex",
		AccountType:         "session",
		AccountCredentialID: 9201,
		CredentialVersion:   1,
	}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	providerAdapters := provider.NewStaticRegistry()
	providerAdapters.MustRegister("openai_codex", &provideropenai.CodexSessionAdapter{})
	protoAdapters := gateway.NewStaticProtocolAdapterRegistry()
	protoAdapters.MustRegister("openai_codex", &protoopenai.ResponsesAdapter{})
	dispatcher := &gateway.UpstreamDispatcher{
		Adapters:         providerAdapters,
		TransportFactory: transport.NewFactory(),
		ProtocolAdapters: protoAdapters,
		HTTPClient:       doer,
	}
	d.CredentialVault = vault
	d.Selector = codexForcedStreamingSelector{}
	d.Dispatcher = dispatcher
	d.CanonicalDispatcher = dispatcher
	d.Forwarder = &gateway.StreamForwarder{
		ProtocolAdapters: protoAdapters,
		Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
		ScannerBufferCap: 1 << 20,
	}
	d.ClaimGate = claimGate
	d.Settler = settler
	return d
}

func largeCodexResponsesSSE(t *testing.T) (string, string) {
	t.Helper()
	const frames = 1500
	chunk := strings.Repeat("x", 768)
	var text strings.Builder
	var body strings.Builder
	appendCodexSSEEvent(t, &body, "response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":     "resp_forced_stream_large",
			"model":  "gpt-5.5",
			"status": "in_progress",
		},
	})
	appendCodexSSEEvent(t, &body, "response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": 0,
		"item": map[string]any{
			"id":   "msg_large",
			"type": "message",
			"role": "assistant",
		},
	})
	appendCodexSSEEvent(t, &body, "response.content_part.added", map[string]any{
		"type":         "response.content_part.added",
		"output_index": 0,
		"item_id":      "msg_large",
		"part":         map[string]any{"type": "output_text"},
	})
	for i := 0; i < frames; i++ {
		text.WriteString(chunk)
		appendCodexSSEEvent(t, &body, "response.output_text.delta", map[string]any{
			"type":         "response.output_text.delta",
			"output_index": 0,
			"item_id":      "msg_large",
			"delta":        chunk,
		})
	}
	wantText := text.String()
	appendCodexSSEEvent(t, &body, "response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_forced_stream_large",
			"model":  "gpt-5.5",
			"status": "completed",
			"output": []map[string]any{{
				"id":   "msg_large",
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{{
					"type": "output_text",
					"text": wantText,
				}},
			}},
			"usage": map[string]any{
				"input_tokens":  11,
				"output_tokens": 22,
				"total_tokens":  33,
				"output_tokens_details": map[string]any{
					"reasoning_tokens": codexLargeReasoningTokens,
				},
			},
		},
	})
	if body.Len() <= maxRawBufferedUpstreamBodyBytes {
		t.Fatalf("fixture raw SSE len=%d must exceed %d", body.Len(), maxRawBufferedUpstreamBodyBytes)
	}
	return body.String(), wantText
}

func smallCodexResponsesSSE(t *testing.T, text string) string {
	t.Helper()
	var body strings.Builder
	appendCodexSSEEvent(t, &body, "response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":     "resp_small",
			"model":  "gpt-5.5",
			"status": "in_progress",
		},
	})
	appendCodexSSEEvent(t, &body, "response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": 0,
		"item": map[string]any{
			"id":   "msg_small",
			"type": "message",
			"role": "assistant",
		},
	})
	appendCodexSSEEvent(t, &body, "response.content_part.added", map[string]any{
		"type":         "response.content_part.added",
		"output_index": 0,
		"item_id":      "msg_small",
		"part":         map[string]any{"type": "output_text"},
	})
	appendCodexSSEEvent(t, &body, "response.output_text.delta", map[string]any{
		"type":         "response.output_text.delta",
		"output_index": 0,
		"item_id":      "msg_small",
		"delta":        text,
	})
	appendCodexSSEEvent(t, &body, "response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_small",
			"model":  "gpt-5.5",
			"status": "completed",
			"output": []map[string]any{{
				"id":   "msg_small",
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{{
					"type": "output_text",
					"text": text,
				}},
			}},
			"usage": map[string]any{
				"input_tokens":  5,
				"output_tokens": 7,
				"total_tokens":  12,
				"output_tokens_details": map[string]any{
					"reasoning_tokens": codexSmallReasoningTokens,
				},
			},
		},
	})
	return body.String()
}

func truncatedCodexResponsesSSE() string {
	return strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"id":"resp_truncated"`,
		``,
	}, "\n")
}

func appendCodexSSEEvent(t *testing.T, b *strings.Builder, event string, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal SSE payload: %v", err)
	}
	b.WriteString("event: ")
	b.WriteString(event)
	b.WriteString("\n")
	b.WriteString("data: ")
	b.Write(raw)
	b.WriteString("\n\n")
}

func assertCodexAggregatedResponse(t *testing.T, rec *httptest.ResponseRecorder, wantText string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body prefix=%s", rec.Code, safeBodyPrefix(rec.Body.String()))
	}
	body := rec.Body.String()
	if strings.Contains(body, "event:") || strings.Contains(body, "data:") {
		t.Fatalf("响应仍是原始 SSE，而不是非流式 JSON: prefix=%s", safeBodyPrefix(body))
	}
	if !strings.Contains(body, `"object":"response"`) || !strings.Contains(body, `"output_text"`) || !strings.Contains(body, `"usage"`) {
		t.Fatalf("响应缺少 Responses JSON 基本字段: prefix=%s", safeBodyPrefix(body))
	}
	var decoded struct {
		Object string `json:"object"`
		Usage  struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
		Output []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode aggregated response: %v; prefix=%s", err, safeBodyPrefix(body))
	}
	if decoded.Object != "response" {
		t.Fatalf("object=%q want response", decoded.Object)
	}
	var gotText strings.Builder
	for _, item := range decoded.Output {
		for _, part := range item.Content {
			if part.Type == "output_text" {
				gotText.WriteString(part.Text)
			}
		}
	}
	if gotText.String() != wantText {
		t.Fatalf("聚合文本长度=%d want %d", gotText.Len(), len(wantText))
	}
	if decoded.Usage.InputTokens != 11 || decoded.Usage.OutputTokens != 22 || decoded.Usage.TotalTokens != 33 {
		t.Fatalf("usage=%+v want 11/22/33", decoded.Usage)
	}
}

func assertCodexAggregatedBilling(t *testing.T, claimGate *recordingClaimGate, settler *recordingSettler, wantClaimID int64) {
	t.Helper()
	if claimGate.endpointFamily != "openai_responses" {
		t.Fatalf("reserve EndpointFamily=%q want openai_responses", claimGate.endpointFamily)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("abort calls=%d want 0", len(settler.aborts))
	}
	call := settler.calls[0]
	if call.ClaimID != wantClaimID {
		t.Fatalf("settle ClaimID=%d want %d", call.ClaimID, wantClaimID)
	}
	assertCodexSettleIdentity(t, call)
	if call.Stream {
		t.Fatal("settle Stream=true; 聚合响应必须落非流式 usage record")
	}
	if call.Draft.TokensInput != 11 || call.Draft.TokensOutput != 22 {
		t.Fatalf("draft tokens=%d/%d want 11/22", call.Draft.TokensInput, call.Draft.TokensOutput)
	}
	if call.Draft.ReasoningTokens != codexLargeReasoningTokens {
		t.Fatalf("draft ReasoningTokens=%d want %d", call.Draft.ReasoningTokens, codexLargeReasoningTokens)
	}
	if call.Draft.UsageSource != gateway.UsageSourceReported {
		t.Fatalf("UsageSource=%q want reported", call.Draft.UsageSource)
	}
}

func assertCodexRequestBodyResponsesShape(t *testing.T, raw string, wantInstructions string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode codex request body: %v body=%s", err, safeBodyPrefix(raw))
	}
	if _, ok := body["input"].([]any); !ok {
		t.Fatalf("codex request missing Responses input[]: %+v", body)
	}
	gotInstructions, _ := body["instructions"].(string)
	if wantInstructions != "" && !strings.Contains(gotInstructions, wantInstructions) {
		t.Fatalf("instructions=%v want containing %q; body=%+v", body["instructions"], wantInstructions, body)
	}
	if body["stream"] != true || body["store"] != false {
		t.Fatalf("codex request stream/store=%v/%v want true/false; body=%+v", body["stream"], body["store"], body)
	}
	if _, ok := body["messages"]; ok {
		t.Fatalf("codex request must not use Chat messages shape: %+v", body)
	}
	for _, field := range []string{"max_output_tokens", "temperature", "top_p"} {
		if _, ok := body[field]; ok {
			t.Fatalf("codex adapter should strip unsupported field %q before wire body: %+v", field, body)
		}
	}
}

func assertCodexChatCompletionResponse(t *testing.T, rec *httptest.ResponseRecorder, wantText string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body prefix=%s", rec.Code, safeBodyPrefix(rec.Body.String()))
	}
	var decoded struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode chat response: %v; prefix=%s", err, safeBodyPrefix(rec.Body.String()))
	}
	if decoded.Object != "chat.completion" || len(decoded.Choices) != 1 {
		t.Fatalf("chat response object/choices=%q/%d body=%s", decoded.Object, len(decoded.Choices), safeBodyPrefix(rec.Body.String()))
	}
	if decoded.Choices[0].Message.Content != wantText {
		t.Fatalf("chat content len=%d want %d", len(decoded.Choices[0].Message.Content), len(wantText))
	}
	if decoded.Usage.PromptTokens != 11 || decoded.Usage.CompletionTokens != 22 || decoded.Usage.TotalTokens != 33 {
		t.Fatalf("chat usage=%+v want 11/22/33", decoded.Usage)
	}
}

func assertCodexAnthropicMessageResponse(t *testing.T, rec *httptest.ResponseRecorder, wantText string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var decoded struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode anthropic response: %v; body=%s", err, rec.Body.String())
	}
	if decoded.Type != "message" || len(decoded.Content) != 1 || decoded.Content[0].Text != wantText {
		t.Fatalf("anthropic response=%+v want text %q", decoded, wantText)
	}
	if decoded.Usage.InputTokens != 5 || decoded.Usage.OutputTokens != 7 {
		t.Fatalf("anthropic usage=%+v want 5/7", decoded.Usage)
	}
}

func assertCodexSettledUsage(t *testing.T, settler *recordingSettler, wantClaimID int64, wantInput, wantOutput, wantReasoning int) {
	t.Helper()
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("abort calls=%d want 0", len(settler.aborts))
	}
	call := settler.calls[0]
	if call.ClaimID != wantClaimID {
		t.Fatalf("settle ClaimID=%d want %d", call.ClaimID, wantClaimID)
	}
	assertCodexSettleIdentity(t, call)
	if call.Stream {
		t.Fatal("settle Stream=true; 非流式聚合响应必须落 buffered usage record")
	}
	if call.Draft.TokensInput != wantInput || call.Draft.TokensOutput != wantOutput {
		t.Fatalf("draft tokens=%d/%d want %d/%d", call.Draft.TokensInput, call.Draft.TokensOutput, wantInput, wantOutput)
	}
	if call.Draft.ReasoningTokens != wantReasoning {
		t.Fatalf("draft ReasoningTokens=%d want %d", call.Draft.ReasoningTokens, wantReasoning)
	}
	if call.Draft.UsageSource != gateway.UsageSourceReported {
		t.Fatalf("UsageSource=%q want reported", call.Draft.UsageSource)
	}
}

func assertCodexSettleIdentity(t *testing.T, call billing.SettleRequest) {
	t.Helper()
	if call.AccountID != codexForcedStreamingAccountID {
		t.Fatalf("settle AccountID=%d want %d", call.AccountID, codexForcedStreamingAccountID)
	}
	if call.AcquisitionToken != codexForcedStreamingAcquisitionToken {
		t.Fatalf("settle AcquisitionToken=%s want %s", call.AcquisitionToken, codexForcedStreamingAcquisitionToken)
	}
}

func safeBodyPrefix(body string) string {
	if len(body) > 512 {
		return body[:512] + "...<truncated>"
	}
	return body
}

func TestHandler_OpenAIEndpointFamilySet(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if dispatcher.observed == nil || dispatcher.observed.RequestMeta.EndpointFamily != "openai_chat" {
		t.Fatalf("EndpointFamily = %+v", dispatcher.observed)
	}
}

func TestResponsesRoute200RoundTrip(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	rec := invokeResponsesHandlerPath(t, d, "/v1/responses", `{"model":"gpt-4o","stream":false,"input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"object":"response"`) {
		t.Fatalf("body = %s; want OpenAI Responses response object", rec.Body.String())
	}
	if dispatcher.calls != 1 {
		t.Fatalf("canonical dispatcher calls = %d; want 1", dispatcher.calls)
	}
}

func TestCodexResponsesIngressRouted(t *testing.T) {
	// 变异：把 /backend-api/codex/responses 从 ClientProtocolByIngressPath 里
	// 漏掉；validateClientProtocol 会返回 404 unknown_route，Responses dispatcher
	// 永不被调用。
	enableHCSFDispatchForTest(t)
	const codexPath = "/backend-api/codex/responses"
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher

	rec := invokeResponsesHandlerPath(t, d, codexPath, `{
		"model":"gpt-4o",
		"instructions":"reply tersely",
		"input":[{"type":"message","role":"user","content":"hi"}],
		"store":false,
		"stream":false
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"object":"response"`) {
		t.Fatalf("body = %s; want OpenAI Responses response object", rec.Body.String())
	}
	if dispatcher.calls != 1 {
		t.Fatalf("canonical dispatcher calls = %d; want 1", dispatcher.calls)
	}
	if dispatcher.observed == nil {
		t.Fatal("canonical dispatcher did not observe Codex Responses request")
	}
	meta := dispatcher.observed.RequestMeta
	if string(meta.ClientProtocol) != "openai_responses" || meta.EndpointFamily != "openai_responses" {
		t.Fatalf("Codex Responses meta client/family=%q/%q; want openai_responses/openai_responses", meta.ClientProtocol, meta.EndpointFamily)
	}
	if meta.IngressPath != codexPath {
		t.Fatalf("IngressPath=%q want %q", meta.IngressPath, codexPath)
	}
}

func TestResponsesFamilySetEndpointFamily(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	claimGate := &recordingClaimGate{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.ClaimGate = claimGate
	rec := invokeResponsesHandlerPath(t, d, "/v1/responses", `{"model":"gpt-4o","stream":false,"input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if claimGate.endpointFamily != "openai_responses" {
		t.Fatalf("billing EndpointFamily=%q want openai_responses", claimGate.endpointFamily)
	}
	if dispatcher.observed == nil {
		t.Fatal("canonical dispatcher did not observe request")
	}
	if string(dispatcher.observed.RequestMeta.ClientProtocol) != "openai_responses" ||
		dispatcher.observed.RequestMeta.EndpointFamily != "openai_responses" {
		t.Fatalf("responses meta client/family=%q/%q", dispatcher.observed.RequestMeta.ClientProtocol, dispatcher.observed.RequestMeta.EndpointFamily)
	}
}

func TestCodexResponsesBilled(t *testing.T) {
	// 变异：让 Codex ingress 绕过正常的 Responses settle 路径；HTTP 响应仍可能
	// 是 200，但不会记录任何 SettleRequest。
	enableHCSFDispatchForTest(t)
	const codexPath = "/backend-api/codex/responses"
	dispatcher := &mockCanonicalBufferedDispatcher{}
	claimGate := &recordingClaimGate{claimID: 8123}
	settler := &recordingSettler{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.ClaimGate = claimGate
	d.Settler = settler

	rec := invokeResponsesHandlerPath(t, d, codexPath, `{"model":"gpt-4o","stream":false,"input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if claimGate.endpointFamily != "openai_responses" {
		t.Fatalf("reserve EndpointFamily=%q want openai_responses", claimGate.endpointFamily)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	if settler.calls[0].ClaimID != 8123 {
		t.Fatalf("settle ClaimID=%d want 8123 from reserve path", settler.calls[0].ClaimID)
	}
	if settler.calls[0].RequestedModel != "gpt-4o" {
		t.Fatalf("settle RequestedModel=%q want gpt-4o", settler.calls[0].RequestedModel)
	}
}

func TestCodexResponsesRawForcedStreamingAggregatesOverRawLimit(t *testing.T) {
	// 变异红点：删 dispatchRawBuffered 的强制流式聚合分支，回退到
	// readRawBufferedUpstreamBody 读取整条原始 SSE，本用例会返回 502
	// upstream_response_too_large。
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	body, wantText := largeCodexResponsesSSE(t)
	doer := &codexSSEDoer{body: body}
	claimGate := &recordingClaimGate{claimID: 8221}
	settler := &recordingSettler{}
	d := codexForcedStreamingDeps(t, doer, claimGate, settler)

	rec := invokeResponsesHandlerPath(t, d, "/v1/responses", `{"model":"gpt-5.5","stream":false,"input":"hi"}`)
	assertCodexAggregatedResponse(t, rec, wantText)
	assertCodexAggregatedBilling(t, claimGate, settler, 8221)
	if doer.requestPath != "/backend-api/codex/responses" {
		t.Fatalf("upstream path=%q want /backend-api/codex/responses", doer.requestPath)
	}
	if !strings.Contains(doer.requestBody, `"stream":true`) {
		t.Fatalf("上游请求未被 session adapter 强制 stream=true: %s", doer.requestBody)
	}
}

func TestCodexResponsesHCSFForcedStreamingAggregatesOverRawLimit(t *testing.T) {
	// 变异红点：只修 legacy raw、不修 DispatchHCSF 的 2xx reader 聚合时，
	// 默认 HCSF-on 路径仍会在 1MiB 原始 SSE 处返回 upstream_response_too_large。
	enableHCSFDispatchForTest(t)
	body, wantText := largeCodexResponsesSSE(t)
	doer := &codexSSEDoer{body: body}
	claimGate := &recordingClaimGate{claimID: 8222}
	settler := &recordingSettler{}
	d := codexForcedStreamingDeps(t, doer, claimGate, settler)

	rec := invokeResponsesHandlerPath(t, d, "/v1/responses", `{"model":"gpt-5.5","stream":false,"input":"hi"}`)
	assertCodexAggregatedResponse(t, rec, wantText)
	assertCodexAggregatedBilling(t, claimGate, settler, 8222)
	if doer.requestPath != "/backend-api/codex/responses" {
		t.Fatalf("upstream path=%q want /backend-api/codex/responses", doer.requestPath)
	}
}

func TestCodexResponsesForcedStreamingAggregationFailureAbortsClaim(t *testing.T) {
	// 变异红点：把 dispatchForcedStreamingBuffered 的聚合失败 abort 分支错换成
	// settle，settler.calls 会变成 1，且同一 claim 不再只通过 abort 释放。
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	doer := &codexSSEDoer{body: truncatedCodexResponsesSSE()}
	claimGate := &recordingClaimGate{claimID: 8321}
	settler := &recordingSettler{}
	d := codexForcedStreamingDeps(t, doer, claimGate, settler)

	rec := invokeResponsesHandlerPath(t, d, "/v1/responses", `{"model":"gpt-5.5","stream":false,"input":"hi"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502; body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 on unreconstructable forced-streaming SSE", len(settler.calls))
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1 on unreconstructable forced-streaming SSE", len(settler.aborts))
	}
	abort := settler.aborts[0]
	if abort.claimID != 8321 {
		t.Fatalf("abort claimID=%d want 8321", abort.claimID)
	}
	if abort.reason != "canonical_response_error" {
		t.Fatalf("abort reason=%q want canonical_response_error", abort.reason)
	}
}

func TestChatToCodexHCSFForcedStreamingAggregatesAndRendersChat(t *testing.T) {
	// 变异红点：把 hcsfShouldAggregateForcedStreamingBuffered 改回只允许
	// openai_responses 客户端时，本用例会在大 SSE raw reader 处返回 502，
	// 不会得到 chat.completion，也不会 settle reported usage。
	enableHCSFDispatchForTest(t)
	body, wantText := largeCodexResponsesSSE(t)
	doer := &codexSSEDoer{body: body}
	claimGate := &recordingClaimGate{claimID: 8331}
	settler := &recordingSettler{}
	d := codexForcedStreamingDeps(t, doer, claimGate, settler)

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{
		"model":"gpt-5.5",
		"stream":false,
		"max_tokens":16,
		"temperature":0.2,
		"top_p":0.9,
		"messages":[
			{"role":"system","content":"system policy"},
			{"role":"user","content":"hi"}
		]
	}`)
	assertCodexChatCompletionResponse(t, rec, wantText)
	assertCodexRequestBodyResponsesShape(t, doer.requestBody, "system policy")
	assertCodexSettledUsage(t, settler, 8331, 11, 22, codexLargeReasoningTokens)
	if !settledLossHasCode(t, settler.calls[0].ProtocolLoss, "codex_max_output_tokens_stripped") {
		t.Fatalf("settle ProtocolLoss=%s want codex_max_output_tokens_stripped", settler.calls[0].ProtocolLoss)
	}
	if !settledLossHasCode(t, settler.calls[0].ProtocolLoss, "codex_temperature_stripped") {
		t.Fatalf("settle ProtocolLoss=%s want codex_temperature_stripped", settler.calls[0].ProtocolLoss)
	}
	if !settledLossHasCode(t, settler.calls[0].ProtocolLoss, "codex_top_p_stripped") {
		t.Fatalf("settle ProtocolLoss=%s want codex_top_p_stripped", settler.calls[0].ProtocolLoss)
	}
}

func TestChatToCodexStreamingSettleCarriesMarshalLossAndReasoning(t *testing.T) {
	// 变异红点：删 translatedStreamingInboundBody 中 streamingProviderRequestBody
	// 之后的 protocolLoss 重快照，settle ProtocolLoss 会漏
	// codex_max_output_tokens_stripped；删流式 draft 的 ReasoningTokens 拷贝，
	// 下方 ReasoningTokens 断言会红。
	enableHCSFDispatchForTest(t)
	const wantText = "hello from codex"
	doer := &codexSSEDoer{body: smallCodexResponsesSSE(t, wantText)}
	claimGate := &recordingClaimGate{claimID: 8333}
	settler := &recordingSettler{}
	d := codexForcedStreamingDeps(t, doer, claimGate, settler)

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{
		"model":"gpt-5.5",
		"stream":true,
		"max_tokens":16,
		"messages":[{"role":"user","content":"hi"}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("abort calls=%d want 0", len(settler.aborts))
	}
	call := settler.calls[0]
	assertCodexSettleIdentity(t, call)
	if call.ClaimID != 8333 {
		t.Fatalf("settle ClaimID=%d want 8333", call.ClaimID)
	}
	if !call.Stream {
		t.Fatal("settle Stream=false; 流式请求必须落 stream usage record")
	}
	if call.Draft.TokensInput != 5 || call.Draft.TokensOutput != 7 {
		t.Fatalf("draft tokens=%d/%d want 5/7", call.Draft.TokensInput, call.Draft.TokensOutput)
	}
	if call.Draft.ReasoningTokens != codexSmallReasoningTokens {
		t.Fatalf("draft ReasoningTokens=%d want %d", call.Draft.ReasoningTokens, codexSmallReasoningTokens)
	}
	if !settledLossHasCode(t, call.ProtocolLoss, "codex_max_output_tokens_stripped") {
		t.Fatalf("settle ProtocolLoss=%s want codex_max_output_tokens_stripped", call.ProtocolLoss)
	}
}

func TestAnthropicToCodexHCSFForcedStreamingAggregatesAndRendersMessage(t *testing.T) {
	enableHCSFDispatchForTest(t)
	const wantText = "hello from codex"
	doer := &codexSSEDoer{body: smallCodexResponsesSSE(t, wantText)}
	claimGate := &recordingClaimGate{claimID: 8332}
	settler := &recordingSettler{}
	d := codexForcedStreamingDeps(t, doer, claimGate, settler)
	h := NewMessagesHandler(d)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
		"model":"gpt-5.5",
		"stream":false,
		"max_tokens":16,
		"system":"system policy",
		"messages":[{"role":"user","content":"hi"}]
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)

	assertCodexAnthropicMessageResponse(t, rec, wantText)
	assertCodexRequestBodyResponsesShape(t, doer.requestBody, "system policy")
	assertCodexSettledUsage(t, settler, 8332, 5, 7, codexSmallReasoningTokens)
	if !settledLossHasCode(t, settler.calls[0].ProtocolLoss, "codex_max_output_tokens_stripped") {
		t.Fatalf("settle ProtocolLoss=%s want codex_max_output_tokens_stripped", settler.calls[0].ProtocolLoss)
	}
}

func TestCodexResponsesAuthRequired(t *testing.T) {
	// 变异：让 Codex ingress 绕过 NewResponsesHandler 的鉴权；mock dispatcher
	// 会被调用，响应会变成 200 而非 401。
	enableHCSFDispatchForTest(t)
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := responsesClientAdapterDeps(t)
	d.Auth = stubAuth{err: auth.ErrUnauthorized}
	d.CanonicalDispatcher = dispatcher

	rec := invokeResponsesHandlerPath(t, d, "/backend-api/codex/responses", `{"model":"gpt-4o","stream":false,"input":"hi"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401; body = %s", rec.Code, rec.Body.String())
	}
	if dispatcher.calls != 0 {
		t.Fatalf("dispatcher calls=%d want 0 when auth fails", dispatcher.calls)
	}
}

func TestResponsesPreviousResponseIDPreservesSessionHashThroughDispatch(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	selector := &recordingSelectionRequestSelector{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.Selector = selector

	rec := invokeResponsesHandlerPath(t, d, "/v1/responses", `{"model":"gpt-4o","stream":false,"previous_response_id":"resp_abc","input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector requests = %d; want 1", len(selector.requests))
	}
	if got := selector.requests[0].SessionHash; got != "resp_abc" {
		t.Fatalf("selector SessionHash=%q want previous_response_id resp_abc", got)
	}
	if dispatcher.observed == nil {
		t.Fatal("canonical dispatcher did not observe request")
	}
	if got := dispatcher.observed.RequestMeta.SessionHash; got != "resp_abc" {
		t.Fatalf("SessionHash=%q want previous_response_id resp_abc; empty prompt hash must not overwrite sticky affinity", got)
	}
}

func TestDispatch_EstimatedInputTokensPopulatedWhenModelFallbackOff(t *testing.T) {
	// 抓 S2(对抗 bug-hunt):EstimatedInputTokens 是 per-key / per-binding / per-account(ROUTE-121)的
	// TPM 限流器按 token 累积窗口的增量源。此前它只在 model-fallback 开启时才估算 → 默认(回退关闭)
	// 恒 0 → 三个 TPM 限流器的窗口永不累积、配置的 TPM 上限被静默绕过。修后无条件估算。
	// 本测试在回退关闭(默认:未设 ModelFallbackSettings → FromSettings 返回 Enabled=false)下发一个
	// 有内容的请求,断言传给 selector 的 EstimatedInputTokens > 0。
	// 变异(已验证转红):把 estInput 估算移回 `if ex.modelFallbackEnabled` 分支 → 回退关闭时恒 0 → 此处红。
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	selector := &recordingSelectionRequestSelector{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.Selector = selector

	rec := invokeHandlerPath(t, d, "/v1/chat/completions",
		`{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"a reasonably long prompt with several words to estimate tokens"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector requests=%d want 1", len(selector.requests))
	}
	if got := selector.requests[0].EstimatedInputTokens; got <= 0 {
		t.Fatalf("EstimatedInputTokens=%d want >0(model-fallback 关时也须估算,否则 per-key/binding/account TPM 限流静默失效)", got)
	}
}

func TestResponsesPreviousResponseIDDoesNotReplacePromptHashAffinity(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	selector := &recordingSelectionRequestSelector{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.Selector = selector

	body := `{"model":"gpt-4o","stream":false,"previous_response_id":"resp_abc","input":"hi","tools":[{"type":"function","name":"f1","description":"...","parameters":{}}]}`
	want := cache_routing.ComputePromptHash([]byte(body))
	if want == "" {
		t.Fatal("test fixture must produce a non-empty prompt hash")
	}
	rec := invokeResponsesHandlerPath(t, d, "/v1/responses", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector requests = %d; want 1", len(selector.requests))
	}
	if got := selector.requests[0].SessionHash; got != want {
		t.Fatalf("selector SessionHash=%q want prompt hash %q", got, want)
	}
	if dispatcher.observed == nil {
		t.Fatal("canonical dispatcher did not observe request")
	}
	if got := dispatcher.observed.RequestMeta.SessionHash; got != want {
		t.Fatalf("RequestMeta.SessionHash=%q want prompt hash %q", got, want)
	}
}

func TestSessionHashHonorsExplicitClientSessionID(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	selector := &recordingSelectionRequestSelector{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Selector = selector

	bodyA := `{"model":"gpt-4o","stream":false,"tools":[{"type":"function","function":{"name":"lookup_a","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"first prompt"}]}`
	bodyB := `{"model":"gpt-4o","stream":false,"tools":[{"type":"function","function":{"name":"lookup_b","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"second prompt"}]}`
	promptHashA := cache_routing.ComputePromptHash([]byte(bodyA))
	promptHashB := cache_routing.ComputePromptHash([]byte(bodyB))
	if promptHashA == "" || promptHashB == "" || promptHashA == promptHashB {
		t.Fatalf("test fixture must produce distinct non-empty prompt hashes: %q %q", promptHashA, promptHashB)
	}

	headers := map[string]string{"X-Session-ID": "thread-stable-1"}
	for _, body := range []string{bodyA, bodyB} {
		rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, headers)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
	}
	if len(selector.requests) != 2 {
		t.Fatalf("selector requests = %d; want 2", len(selector.requests))
	}
	gotA := selector.requests[0].SessionHash
	gotB := selector.requests[1].SessionHash
	if gotA == "" || gotB == "" {
		t.Fatalf("explicit session hash must be non-empty: %q %q", gotA, gotB)
	}
	// 变异：忽略显式 id、总是用 prefix hash；这些不同的 prompt 前缀会产生
	// 不同的 sticky session hash。
	if gotA != gotB {
		t.Fatalf("same X-Session-ID produced different SessionHash values: %q vs %q", gotA, gotB)
	}
	if gotA == promptHashA || gotA == promptHashB {
		t.Fatalf("SessionHash=%q still used prompt hash despite explicit client session id", gotA)
	}
}

func TestSessionHashFallbackUnchanged(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	selector := &recordingSelectionRequestSelector{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Selector = selector

	body := `{"model":"gpt-4o","stream":false,"tools":[{"type":"function","function":{"name":"fallback_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"no explicit session"}]}`
	want := cache_routing.ComputePromptHash([]byte(body))
	if want == "" {
		t.Fatal("test fixture must produce a non-empty prompt hash")
	}

	rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector requests = %d; want 1", len(selector.requests))
	}
	// 变异：即便 id 缺失也总走显式 id 的 hash 路径；空 id 的 hash 会与改动前的
	// prompt hash 不同。
	if got := selector.requests[0].SessionHash; got != want {
		t.Fatalf("selector SessionHash=%q want unchanged prompt hash %q", got, want)
	}
}

func TestAffinityRulesOverrideDefaultSessionHashWhenConfigured(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	selector := &recordingSelectionRequestSelector{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Selector = selector
	d.AffinityRules = affinityrules.AffinityRuleSet{{
		Name:             "cache",
		ModelRegex:       []string{"^gpt-"},
		PathRegex:        []string{"^/v1/chat/completions$"},
		UserAgentInclude: []string{"affinity-client"},
		KeySources: []affinityrules.KeySource{{
			Type: affinityrules.KeySourceRequestHeader,
			Key:  "X-Affinity-Key",
		}},
		IncludeRuleName: true,
	}}
	body := `{"model":"gpt-4o","stream":false,"tools":[{"type":"function","function":{"name":"affinity_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"configured rule"}]}`
	promptHash := cache_routing.ComputePromptHash([]byte(body))
	if promptHash == "" {
		t.Fatal("test fixture must produce a non-empty prompt hash")
	}

	rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, map[string]string{
		"User-Agent":     "huakai affinity-client",
		"X-Session-ID":   "legacy-thread",
		"X-Affinity-Key": "rule-key",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector requests = %d; want 1", len(selector.requests))
	}
	// 变异：在 affinity 规则之前仍沿用旧的 requestSessionHash 级联；这会改而
	// 返回 client-session hash。
	if got := selector.requests[0].SessionHash; got != "cache:rule-key" {
		t.Fatalf("selector SessionHash=%q want cache:rule-key", got)
	}
}

func TestAffinityRulesNoMatchFallsBackToExistingSessionHash(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	selector := &recordingSelectionRequestSelector{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Selector = selector
	d.AffinityRules = affinityrules.AffinityRuleSet{{
		Name:       "claude-only",
		ModelRegex: []string{"^claude-"},
		KeySources: []affinityrules.KeySource{{
			Type: affinityrules.KeySourceRequestHeader,
			Key:  "X-Affinity-Key",
		}},
	}}
	body := `{"model":"gpt-4o","stream":false,"tools":[{"type":"function","function":{"name":"fallback_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"rule no match"}]}`
	want := cache_routing.ComputePromptHash([]byte(body))
	if want == "" {
		t.Fatal("test fixture must produce a non-empty prompt hash")
	}

	rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, map[string]string{
		"X-Affinity-Key": "rule-key",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector requests = %d; want 1", len(selector.requests))
	}
	// 变异：把一个已配置但未命中的规则集当作权威；这会丢弃或替换掉旧的
	// prompt-hash 回退。
	if got := selector.requests[0].SessionHash; got != want {
		t.Fatalf("selector SessionHash=%q want existing fallback %q", got, want)
	}
}

func TestSessionHashHeaderPriority(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")

	t.Run("x_session_id_beats_body_conversation_id", func(t *testing.T) {
		selector := &recordingSelectionRequestSelector{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		d.Selector = selector
		body := `{"model":"gpt-4o","stream":false,"conversation_id":"body-thread","tools":[{"type":"function","function":{"name":"priority_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"priority"}]}`

		rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, map[string]string{"X-Session-ID": "header-thread"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if len(selector.requests) != 1 {
			t.Fatalf("selector requests = %d; want 1", len(selector.requests))
		}
		want := expectedClientSessionHashForTest("header-thread")
		// 变异：把 body 里的 conversation_id 置于 X-Session-ID 之上；观测到的
		// sticky hash 会变成 body-thread 的 hash 而非 header-thread 的。
		if got := selector.requests[0].SessionHash; got != want {
			t.Fatalf("selector SessionHash=%q want X-Session-ID hash %q", got, want)
		}
	})

	t.Run("invalid_control_header_falls_through_to_body_id", func(t *testing.T) {
		selector := &recordingSelectionRequestSelector{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		d.Selector = selector
		body := `{"model":"gpt-4o","stream":false,"conversation_id":"body-thread","tools":[{"type":"function","function":{"name":"control_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"control"}]}`

		rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, map[string]string{"X-Session-ID": "bad\x01thread"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if len(selector.requests) != 1 {
			t.Fatalf("selector requests = %d; want 1", len(selector.requests))
		}
		want := expectedClientSessionHashForTest("body-thread")
		// 变异：接受 header id 里的控制字符；观测到的 sticky hash 会从这个
		// 非法 header 值派生。
		if got := selector.requests[0].SessionHash; got != want {
			t.Fatalf("selector SessionHash=%q want body conversation_id hash %q", got, want)
		}
	})

	t.Run("metadata_user_id_claude_code_session_suffix_fills_session", func(t *testing.T) {
		selector := &recordingSelectionRequestSelector{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		d.Selector = selector
		sessionID := "11111111-2222-3333-4444-555555555555"
		body := `{"model":"gpt-4o","stream":false,"metadata":{"user_id":"user_x__session_` + sessionID + `"},"tools":[{"type":"function","function":{"name":"metadata_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"metadata"}]}`

		rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if len(selector.requests) != 1 {
			t.Fatalf("selector requests = %d; want 1", len(selector.requests))
		}
		want := expectedClientSessionHashForTest(sessionID)
		// 变异：跳过 metadata.user_id，或对完整 user id 而非 Claude-Code 的
		// session 后缀做 hash；session hash 会回退到 prompt hash 或用上一个
		// 不同的 client-session hash。
		if got := selector.requests[0].SessionHash; got != want {
			t.Fatalf("selector SessionHash=%q want metadata.user_id session hash %q", got, want)
		}
	})

	t.Run("x_client_request_id_is_lowest_header_priority", func(t *testing.T) {
		selector := &recordingSelectionRequestSelector{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		d.Selector = selector
		body := `{"model":"gpt-4o","stream":false,"conversation_id":"body-thread","tools":[{"type":"function","function":{"name":"client_request_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"request id"}]}`

		rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, map[string]string{
			"X-Client-Request-Id": "client-request-thread",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if len(selector.requests) != 1 {
			t.Fatalf("selector requests = %d; want 1", len(selector.requests))
		}
		want := expectedClientSessionHashForTest("client-request-thread")
		if got := selector.requests[0].SessionHash; got != want {
			t.Fatalf("selector SessionHash=%q want X-Client-Request-Id hash %q", got, want)
		}

		selector.requests = nil
		rec = invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, map[string]string{
			"X-Session-ID":        "primary-thread",
			"X-Client-Request-Id": "client-request-thread",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if len(selector.requests) != 1 {
			t.Fatalf("selector requests = %d; want 1", len(selector.requests))
		}
		want = expectedClientSessionHashForTest("primary-thread")
		// 变异：把 X-Client-Request-Id 排在既有 session header 之前；它会在
		// 这里抢走 X-Session-ID 的优先级。
		if got := selector.requests[0].SessionHash; got != want {
			t.Fatalf("selector SessionHash=%q want existing header priority hash %q", got, want)
		}
	})

	t.Run("too_long_body_id_falls_back_to_prompt_hash", func(t *testing.T) {
		selector := &recordingSelectionRequestSelector{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		d.Selector = selector
		longSessionID := strings.Repeat("x", 201)
		body := `{"model":"gpt-4o","stream":false,"session_id":"` + longSessionID + `","tools":[{"type":"function","function":{"name":"long_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"long"}]}`
		want := cache_routing.ComputePromptHash([]byte(body))
		if want == "" {
			t.Fatal("test fixture must produce a non-empty prompt hash")
		}

		rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if len(selector.requests) != 1 {
			t.Fatalf("selector requests = %d; want 1", len(selector.requests))
		}
		// 变异：接受一个超长的显式 id；观测到的 sticky hash 会从 session_id
		// 派生，而非沿用既有的 prompt hash。
		if got := selector.requests[0].SessionHash; got != want {
			t.Fatalf("selector SessionHash=%q want prompt hash fallback %q", got, want)
		}
	})
}

func TestHandler_WaitPlanReturnsQueueWait(t *testing.T) {
	settler := &stubSettler{}
	d := minimalDeps()
	d.Selector = waitPlanSelector{}
	d.Settler = settler

	rec := invokeHandler(t, d, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s; want 429", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "3" {
		t.Fatalf("Retry-After=%q want 3", got)
	}
	if !strings.Contains(rec.Body.String(), "queue_wait") {
		t.Fatalf("body=%s; want queue_wait", rec.Body.String())
	}
	if settler.abortCalls != 1 || settler.lastAbortClaimID != 999 || settler.lastAbortReason != "queue_wait" {
		t.Fatalf("abort calls/id/reason=%d/%d/%q; want 1/999/queue_wait",
			settler.abortCalls, settler.lastAbortClaimID, settler.lastAbortReason)
	}
}

func TestHandler_ReserveClaimRaceReturns409RetryAfterWithoutAbort(t *testing.T) {
	// 变异：删掉 reserve 阶段的 ErrClaimRace 分支会落到通用 reserve_error 路径，
	// 产出不带 Retry-After 的 500；下面的 409 断言必须抓住这个回归。
	settler := &stubSettler{}
	d := minimalDeps()
	d.ClaimGate = reserveClaimRaceClaimGate{}
	d.Settler = settler

	rec := invokeHandler(t, d, validBody())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s; want 409 claim_race, not reserve_error 500", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After=%q want 1", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"claim_race"`) {
		t.Fatalf("body=%s; want claim_race code", body)
	}
	for _, bad := range []string{"reserve_error", "request reservation failed"} {
		if strings.Contains(body, bad) {
			t.Fatalf("body=%s leaked generic reserve error marker %q", body, bad)
		}
	}
	if settler.abortCalls != 0 {
		t.Fatalf("reserve-phase claim race must not abort a rolled-back Tx1 claim; abort calls=%d", settler.abortCalls)
	}
}

func TestHandler_QuotaDenyAbortsBillingClaimAndReturns429(t *testing.T) {
	// 变异：删掉 quota deny 分支会让请求继续走 buffered 的顺利路径，产出 200
	// 且没有 quota_denied 计费 abort；下面两条断言都必须变红。
	enableHCSFDispatchForTest(t)
	claimGate := &recordingClaimGate{claimID: 99001}
	quotaReserver := &recordingQuotaReserver{
		err: &quota.DenyError{Decision: quota.Decision{
			Kind:   quota.DecisionDeny,
			Code:   "quota_limit_exceeded",
			Reason: "unit test deny",
		}},
	}
	settler := &stubSettler{}
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.ClaimGate = claimGate
	d.QuotaReserver = quotaReserver
	d.Settler = settler
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s; want 429 quota denial", rec.Code, rec.Body.String())
	}
	if dispatcher.calls != 0 {
		t.Fatalf("dispatcher calls=%d want 0 because genuine quota deny must not proceed", dispatcher.calls)
	}
	body := rec.Body.String()
	for _, want := range []string{`"type":"insufficient_quota"`, `"code":"insufficient_balance"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body=%s missing %s", body, want)
		}
	}
	if settler.abortCalls != 1 || settler.lastAbortClaimID != 99001 || settler.lastAbortReason != "quota_denied" {
		t.Fatalf("abort calls/id/reason=%d/%d/%q; want 1/99001/quota_denied",
			settler.abortCalls, settler.lastAbortClaimID, settler.lastAbortReason)
	}
	if quotaReserver.calls != 1 {
		t.Fatalf("quota reserve calls=%d want 1", quotaReserver.calls)
	}
	if quotaReserver.req.TenantID != validIdentity().TenantID || quotaReserver.req.ClaimID != 99001 {
		t.Fatalf("quota reserve identity=%+v want tenant=%d claim=99001", quotaReserver.req, validIdentity().TenantID)
	}
	if !quotaReserver.req.PredictedCost.Equal(claimGate.req.PredictedCost) {
		t.Fatalf("quota predicted cost=%s want same billing predicted cost %s",
			quotaReserver.req.PredictedCost, claimGate.req.PredictedCost)
	}
	if !hasQuotaScope(quotaReserver.req.Scopes, quota.ScopeGlobal, "*") {
		t.Fatalf("quota scopes=%+v missing tenant-level global scope", quotaReserver.req.Scopes)
	}
	if !hasQuotaScope(quotaReserver.req.Scopes, quota.ScopeUser, "3") {
		t.Fatalf("quota scopes=%+v missing user scope", quotaReserver.req.Scopes)
	}
	if !hasQuotaScope(quotaReserver.req.Scopes, quota.ScopeAPIKey, "11") {
		t.Fatalf("quota scopes=%+v missing api-key scope", quotaReserver.req.Scopes)
	}
}

// TestHandler_QuotaReserveFeedsInputTokenEstimate W5:输入 token 估算必须喂进
// 配额预检的 ReservedTokens(否则 token-per-window 配额永远拿不到量、无法拦截)。
// 变异: 去掉 reserveQuota 的 ReservedTokens 接线 → req.ReservedTokens=0 →
// 本断言红。
func TestHandler_QuotaReserveFeedsInputTokenEstimate(t *testing.T) {
	enableHCSFDispatchForTest(t)
	claimGate := &recordingClaimGate{claimID: 99010}
	quotaReserver := &recordingQuotaReserver{} // 放行
	d := clientAdapterDeps(t)
	d.ClaimGate = claimGate
	d.QuotaReserver = quotaReserver
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}

	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	invokeHandlerPath(t, d, "/v1/chat/completions", body)

	// gpt-4o 走真实 tokenizer；这里必须用 handler 从 body 读到的同一个 model，
	// 估值才能与预留的 token 数对上。
	want := int64(estimateInputTokens("gpt-4o", []byte(body)))
	if want <= 0 {
		t.Fatalf("fixture non-discriminating: estimateInputTokens=%d must be >0", want)
	}
	if quotaReserver.req.ReservedTokens != want {
		t.Fatalf("quota ReservedTokens=%d want %d(输入 token 估算须喂进配额预检)", quotaReserver.req.ReservedTokens, want)
	}
}

// TestHandler_QuotaDenyEmitsRetryAfterAndWindowResetsAt "更强"delta:窗口配额
// 拒绝时,引擎算出的 RetryAfter 必须吐成 Retry-After 头 + body 的
// window_resets_at,让客户端按窗口边界智能退避(逐窗口区分,优于单一累计配额)。
// 变异: 拒绝写回改回 writeInsufficientQuotaError(w)(不传 RetryAfter)→
// Retry-After 头缺失 + body 无 window_resets_at → 两断言红。
func TestHandler_QuotaDenyEmitsRetryAfterAndWindowResetsAt(t *testing.T) {
	enableHCSFDispatchForTest(t)
	claimGate := &recordingClaimGate{claimID: 99011}
	quotaReserver := &recordingQuotaReserver{
		err: &quota.DenyError{Decision: quota.Decision{
			Kind:       quota.DecisionDeny,
			Code:       "quota_limit_exceeded",
			Reason:     "token window exhausted",
			RetryAfter: 2 * time.Hour,
		}},
	}
	d := clientAdapterDeps(t)
	d.ClaimGate = claimGate
	d.QuotaReserver = quotaReserver
	d.Settler = &stubSettler{}
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "7200" {
		t.Fatalf("Retry-After=%q want 7200(2h=7200s)", got)
	}
	if !strings.Contains(rec.Body.String(), `"window_resets_at"`) {
		t.Fatalf("body=%s missing window_resets_at", rec.Body.String())
	}
}

// TestHandler_QuotaDenyEmitsWindowKind 验证窗口配额拒绝时 429 body 透出 quota_window,让客户端区分
// 是日额还是月额超了(逐窗口区分超限)。子用例二验 manual 窗口:quota_window 仍透出但与
// window_resets_at 解耦(manual 无固定重置时刻)。
// 变异: 删 exceededDecision/DenyWindowKind 的窗口透传、或删 errFields 写 quota_window 那行 →
// body 缺 quota_window → calendar_month 断言红。
func TestHandler_QuotaDenyEmitsWindowKind(t *testing.T) {
	run := func(t *testing.T, kind quota.WindowKind, retryAfter time.Duration) *httptest.ResponseRecorder {
		enableHCSFDispatchForTest(t)
		quotaReserver := &recordingQuotaReserver{
			err: &quota.DenyError{Decision: quota.Decision{
				Kind:       quota.DecisionDeny,
				Code:       "quota_limit_exceeded",
				Reason:     "window exhausted",
				RetryAfter: retryAfter,
				WindowKind: kind,
			}},
		}
		d := clientAdapterDeps(t)
		d.ClaimGate = &recordingClaimGate{claimID: 99012}
		d.QuotaReserver = quotaReserver
		d.Settler = &stubSettler{}
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		return invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	}

	t.Run("calendar_month", func(t *testing.T) {
		rec := run(t, quota.WindowCalendarMonth, 3*time.Hour)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status=%d want 429", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"quota_window":"calendar_month"`) {
			t.Fatalf("body=%s 缺 quota_window=calendar_month(客户端无法区分是哪个窗口超限)", rec.Body.String())
		}
	})

	t.Run("manual_decoupled_from_resets_at", func(t *testing.T) {
		// manual 窗口无固定重置:retryAfter=0 → 无 window_resets_at,但 quota_window 仍应透出。
		rec := run(t, quota.WindowManual, 0)
		if !strings.Contains(rec.Body.String(), `"quota_window":"manual"`) {
			t.Fatalf("body=%s 缺 quota_window=manual", rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), `"window_resets_at"`) {
			t.Fatalf("manual 窗口无固定重置,不应出现 window_resets_at: %s", rec.Body.String())
		}
	})
}

func TestHandler_QuotaReserveInfraErrorFailsOpenAndKeepsBillingClaim(t *testing.T) {
	// 变异：恢复旧的 quota_reserve_error abort+500 分支会让这里返回 500 并使
	// abortCalls 自增，于是状态与 abort 断言都必须变红。
	enableHCSFDispatchForTest(t)
	before := quotaReserveFailedOpenCount(t)
	claimGate := &recordingClaimGate{claimID: 99004}
	quotaReserver := &recordingQuotaReserver{err: errors.New("quota store unavailable")}
	settler := &stubSettler{}
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.ClaimGate = claimGate
	d.QuotaReserver = quotaReserver
	d.Settler = settler
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200 fail-open on quota reserve infra error", rec.Code, rec.Body.String())
	}
	if settler.abortCalls != 0 {
		t.Fatalf("billing abort calls=%d reason=%q; want 0 so claim remains for money settlement",
			settler.abortCalls, settler.lastAbortReason)
	}
	if quotaReserver.calls != 1 {
		t.Fatalf("quota reserve calls=%d want 1", quotaReserver.calls)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("dispatcher calls=%d want request to proceed after quota reserve infra error", dispatcher.calls)
	}
	after := quotaReserveFailedOpenCount(t)
	if after != before+1 {
		t.Fatalf("quota_reserve_failed_open_total before/after=%d/%d want +1", before, after)
	}
}

func TestHandler_QuotaAllowProceedsToPoolSelection(t *testing.T) {
	claimGate := &recordingClaimGate{claimID: 99002}
	quotaReserver := &recordingQuotaReserver{result: quota.ReserveResult{Allowed: true}}
	selector := &recordingSelectionRequestSelector{}
	d := minimalDeps()
	d.ClaimGate = claimGate
	d.QuotaReserver = quotaReserver
	d.Selector = selector

	rec := invokeHandler(t, d, validBody())

	if rec.Code == http.StatusTooManyRequests && strings.Contains(rec.Body.String(), "insufficient_quota") {
		t.Fatalf("status=%d body=%s; allowed quota must not render quota denial", rec.Code, rec.Body.String())
	}
	if quotaReserver.calls != 1 {
		t.Fatalf("quota reserve calls=%d want 1", quotaReserver.calls)
	}
	if selector.calls != 1 {
		t.Fatalf("selector calls=%d want request to proceed after quota allow", selector.calls)
	}
	if selector.requests[0].ClaimID != 99002 {
		t.Fatalf("selector ClaimID=%d want billing claim 99002", selector.requests[0].ClaimID)
	}
}

func TestHandler_IdempotencyReplaySkipsQuotaReserve(t *testing.T) {
	quotaReserver := &recordingQuotaReserver{result: quota.ReserveResult{Allowed: true}}
	d := minimalDeps()
	d.ClaimGate = replayClaimGate{claimID: 99003, hit: true}
	d.QuotaReserver = quotaReserver

	rec := invokeHandler(t, d, validBody())

	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "replay_without_cache") {
		t.Fatalf("status/body=%d/%s want replay_without_cache from idempotency path", rec.Code, rec.Body.String())
	}
	if quotaReserver.calls != 0 {
		t.Fatalf("quota reserve calls=%d want 0 for idempotency replay", quotaReserver.calls)
	}
}

func TestHandler_AttemptLoopPassesAttemptSeqAndEmptyExclusionsOnFirstSuccess(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	selector := &recordingSelectionRequestSelector{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.Selector = selector
	d.Router = stubRouter{plan: router.RoutePlan{
		Attempts: []router.AttemptPlan{
			{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
			{Index: 1, PoolGroupID: 43, UpstreamModelID: "gpt-4o-backup", Reason: "cross_pool_fallback"},
		},
		AttemptBudget:   2,
		SnapshotVersion: "registry:7:1;router:multi",
	}}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200", rec.Code, rec.Body.String())
	}
	if selector.calls != 1 {
		t.Fatalf("selector calls=%d want 1 because first attempt succeeds", selector.calls)
	}
	req := selector.requests[0]
	if req.PoolGroupID != 42 {
		t.Fatalf("PoolGroupID=%d want first planned pool 42", req.PoolGroupID)
	}
	if req.AttemptSeq != 1 {
		t.Fatalf("AttemptSeq=%d want 1", req.AttemptSeq)
	}
	if req.ExcludedAccounts == nil {
		t.Fatal("ExcludedAccounts must be a non-nil empty map in PR3")
	}
	if len(req.ExcludedAccounts) != 0 {
		t.Fatalf("ExcludedAccounts=%v want empty map in PR3", req.ExcludedAccounts)
	}
}

func TestRouterResolvedModelFromRegistryMapsPerPoolModelOverrides(t *testing.T) {
	poolAOverride := "pool-a-upstream"
	poolBOverride := "pool-b-upstream"
	poolAMaxParallel := int32(7)
	poolBMaxParallel := int32(11)
	resolved := registry.Resolved{
		PublicAlias:            "gpt-4o",
		CanonicalModelID:       "openai/gpt-4o",
		DefaultProviderModelID: "default-upstream",
		ProviderModelID:        "pool-a-upstream",
		ContextWindow:          128000,
		Capabilities:           []string{"stream"},
		PricingClass:           "standard",
		ProtocolFamily:         "openai_chat",
		PoolCandidates:         []int64{701, 702, 703},
		BindingMetadata: []registry.BindingMetadata{
			{BindingID: 1, PoolGroupID: 701, Priority: 10, Weight: 5, SelectionMode: "strict_priority", FallbackClass: "normal", ProviderModelIDOverride: &poolAOverride, MaxParallelRequests: &poolAMaxParallel},
			{BindingID: 2, PoolGroupID: 702, Priority: 20, Weight: 3, SelectionMode: "strict_priority", FallbackClass: "quota", ProviderModelIDOverride: &poolBOverride, MaxParallelRequests: &poolBMaxParallel},
			{BindingID: 3, PoolGroupID: 703, Priority: 30, Weight: 1, SelectionMode: "strict_priority", FallbackClass: "manual"},
		},
		SnapshotVersion: "registry:7:3",
	}

	got := routerResolvedModelFromRegistry(resolved)

	if got.ProviderModelID != "pool-a-upstream" {
		t.Fatalf("ProviderModelID=%q want primary override", got.ProviderModelID)
	}
	if len(got.PoolMetadata) != 3 {
		t.Fatalf("PoolMetadata len=%d want 3", len(got.PoolMetadata))
	}
	// BW-AT-01：Priority/Weight/SelectionMode 必须完整穿过 registry→router
	// 翻译边界。变异删除任一字段映射时，下面的结构体精确比较直接变红。
	want := []router.PoolCandidateMeta{
		{BindingID: 1, PoolGroupID: 701, ProviderModelID: "pool-a-upstream", Priority: 10, Weight: 5, SelectionMode: "strict_priority", MaxParallelRequests: 7, FallbackClass: "normal"},
		{BindingID: 2, PoolGroupID: 702, ProviderModelID: "pool-b-upstream", Priority: 20, Weight: 3, SelectionMode: "strict_priority", MaxParallelRequests: 11, FallbackClass: "quota"},
		{BindingID: 3, PoolGroupID: 703, ProviderModelID: "default-upstream", Priority: 30, Weight: 1, SelectionMode: "strict_priority", FallbackClass: "manual"},
	}
	for i := range want {
		if got.PoolMetadata[i] != want[i] {
			t.Fatalf("PoolMetadata[%d]=%+v want %+v", i, got.PoolMetadata[i], want[i])
		}
	}
}

func TestClassifyPoolSelectFailureBindingConcurrencyUsesDedicated429AndAbortReason(t *testing.T) {
	settler := &recordingSettler{}
	ex := &chatExecution{
		ctx:        context.Background(),
		ident:      auth.Identity{TenantID: 7},
		d:          ChatHandlerDeps{Settler: settler},
		requestID:  "binding-concurrency-429",
		reserveRes: &billing.ReserveResult{ClaimID: 91},
	}
	failure := ex.classifyPoolSelectFailure(
		httptest.NewRecorder(),
		fmt.Errorf("wrapped: %w", pool.ErrBindingConcurrencyLimited),
	)
	if failure == nil {
		t.Fatal("binding concurrency failure must be classified")
	}
	if failure.ClientStatus != http.StatusTooManyRequests ||
		failure.ClientCode != clienterr.CodeBindingConcurrencyLimited ||
		failure.AbortReason != "binding_concurrency_limited" ||
		failure.RetryAfterSeconds != 1 {
		t.Fatalf("failure=%+v want dedicated binding 429 contract", failure)
	}
	if failure.Decision.RetryableBeforeDelivery ||
		failure.Decision.SwitchAccount ||
		failure.Decision.SwitchPool {
		t.Fatalf("binding cap must be terminal, got decision=%+v", failure.Decision)
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1", len(settler.aborts))
	}
	abort := settler.aborts[0]
	if abort.tenantID != 7 || abort.claimID != 91 || abort.reason != "binding_concurrency_limited" {
		t.Fatalf("abort=%+v want tenant=7 claim=91 dedicated reason", abort)
	}
}

func TestBuildPoolSelectionRequestKeepsBindingConcurrencyPairFromAttempt(t *testing.T) {
	ex := &chatExecution{
		ctx:        context.Background(),
		ident:      auth.Identity{TenantID: 7, UserID: 8, APIKeyID: 9},
		body:       []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
		req:        chatRequest{Model: "gpt-4o"},
		reserveRes: &billing.ReserveResult{ClaimID: 91},
		attempt: router.AttemptPlan{
			PoolGroupID: 702, BindingID: 22, MaxParallelRequests: 5,
		},
		resolved: registry.Resolved{
			ProtocolFamily: "openai_chat",
			BindingMetadata: []registry.BindingMetadata{
				{BindingID: 11, PoolGroupID: 701},
				{BindingID: 22, PoolGroupID: 702},
			},
		},
	}
	got := ex.buildPoolSelectionRequest(attemptInput{AttemptSeq: 1})
	if got.BindingID != 22 || got.MaxParallelRequests != 5 {
		t.Fatalf("BindingID/MaxParallelRequests=%d/%d want 22/5", got.BindingID, got.MaxParallelRequests)
	}
}

func quotaReserveFailedOpenCount(t *testing.T) int64 {
	t.Helper()
	v := expvar.Get("quota_reserve_failed_open_total")
	if v == nil {
		return 0
	}
	iv, ok := v.(*expvar.Int)
	if !ok {
		t.Fatalf("quota_reserve_failed_open_total is %T want *expvar.Int", v)
	}
	return iv.Value()
}

type recordingQuotaReserver struct {
	calls  int
	req    quota.ReserveRequest
	result quota.ReserveResult
	err    error
}

func (r *recordingQuotaReserver) Reserve(_ context.Context, req quota.ReserveRequest) (quota.ReserveResult, error) {
	r.calls++
	r.req = req
	if r.err != nil {
		return r.result, r.err
	}
	if !r.result.Allowed {
		r.result.Allowed = true
	}
	return r.result, nil
}

func hasQuotaScope(scopes []quota.Scope, kind quota.ScopeKind, id string) bool {
	for _, scope := range scopes {
		if scope.Kind == kind && scope.ID == id {
			return true
		}
	}
	return false
}

type waitPlanSelector struct{}

func (waitPlanSelector) Select(context.Context, pool.SelectionRequest) (*pool.SelectionResult, error) {
	return &pool.SelectionResult{WaitPlan: &pool.WaitPlan{
		AccountID:      1,
		MaxConcurrency: 2,
		TimeoutMS:      2500,
		MaxWaiting:     0,
	}}, nil
}

type recordingSelectionRequestSelector struct {
	calls    int
	requests []pool.SelectionRequest
}

func (s *recordingSelectionRequestSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.calls++
	s.requests = append(s.requests, req)
	return &pool.SelectionResult{AccountID: 1}, nil
}

func invokeHandlerPathWithHeaders(t *testing.T, deps ChatHandlerDeps, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewChatCompletionsHandler(deps)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func expectedClientSessionHashForTest(id string) string {
	sum := sha256.Sum256([]byte("huakai:client-session:v1:" + id))
	return "client-session:" + hex.EncodeToString(sum[:])
}

// TestSelectPoolAccount_ThreadsUserGroupFromIdentity 守 R-SUB-WIRE-1 接线: 选号时
// SelectionRequest.UserGroup 必须从 auth.Identity.UserGroup 透传, 否则订阅分组路由 gate
// 永远收到空档 → 恒放行 → 限档失效。
// 变异: 删 selectPoolAccount 里 `UserGroup: ex.ident.UserGroup` → 透传为空串 → 红。
func TestSelectPoolAccount_ThreadsUserGroupFromIdentity(t *testing.T) {
	selector := &recordingSelectionRequestSelector{}
	ex := &chatExecution{
		ctx:        context.Background(),
		ident:      auth.Identity{TenantID: 7, UserID: 3, APIKeyID: 9, UserGroup: "premium"},
		d:          ChatHandlerDeps{Selector: selector},
		body:       []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
		req:        chatRequest{Model: "gpt-4o"},
		attempt:    router.AttemptPlan{PoolGroupID: 42},
		reserveRes: &billing.ReserveResult{},
		resolved:   registry.Resolved{ProtocolFamily: "openai_chat"},
	}

	if f := ex.selectPoolAccount(httptest.NewRecorder(), attemptInput{AttemptSeq: 1}); f != nil {
		t.Fatalf("selectPoolAccount returned failure: %+v", f)
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector calls=%d want 1", len(selector.requests))
	}
	if got := selector.requests[0].UserGroup; got != "premium" {
		t.Fatalf("SelectionRequest.UserGroup=%q want premium (must thread from auth.Identity.UserGroup)", got)
	}
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	_ = os.Unsetenv(key)
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func anthropicClientAdapterDeps(t *testing.T) ChatHandlerDeps {
	t.Helper()
	d := minimalDeps()
	d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "claude-3-5-sonnet",
		CanonicalModelID: "anthropic/claude-3-5-sonnet",
		ProviderModelID:  "claude-3-5-sonnet",
		ProtocolFamily:   "anthropic_messages",
		PoolCandidates:   []int64{42},
	}}
	vault := provider.NewStaticVault()
	if err := vault.Set(1, provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant-test"}, provider.AccountInfo{AccountID: 1, Platform: "anthropic", AccountType: "apikey", AccountCredentialID: 9002, CredentialVersion: 1}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	d.CredentialVault = vault
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	protoReg := gateway.NewStaticProtocolAdapterRegistry()
	protoReg.MustRegister("anthropic_messages", &protoanthropic.Adapter{})
	d.Forwarder = &gateway.StreamForwarder{
		ProtocolAdapters: protoReg,
	}
	return d
}

func responsesClientAdapterDeps(t *testing.T) ChatHandlerDeps {
	t.Helper()
	d := clientAdapterDeps(t)
	d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "gpt-4o",
		CanonicalModelID: "openai/gpt-4o",
		ProviderModelID:  "gpt-4o",
		ProtocolFamily:   "openai_responses",
		PoolCandidates:   []int64{42},
	}}
	return d
}

// recordingPlanInputRouter 记录最后一次 Plan 收到的 PlanInput，便于断言 :313
// 把 body-derived caps 接进了 RequestFeatures，而不是只接了 Stream。
type recordingPlanInputRouter struct {
	delegate router.Router
	last     router.PlanInput
}

func (r *recordingPlanInputRouter) Plan(ctx context.Context, in router.PlanInput) (router.RoutePlan, error) {
	r.last = in
	return r.delegate.Plan(ctx, in)
}

func capSet(caps []string) map[string]bool {
	m := make(map[string]bool, len(caps))
	for _, c := range caps {
		m[c] = true
	}
	return m
}

// TestPrepareRoute_ThreadsBodyDerivedCapabilities 是自证接线测试 (ROUTE-024 核心契约):
// 一个含 image part + tools + json_schema 的 streamed body 经 prepareRoute 后,
// 真 Router 产出的 AttemptPlan.RequiredCapabilities 必须 == {stream,vision,tools,json};
// 基线 arm = 纯 text 非流 body,同代码路径只应得到 {} (没有 :313 接线两臂不会有差别)。
// 还断言 PlanInput.Features 三个 Wants* 位被 body 真正驱动。
// 变异: 还原 :313 为 `Features: router.RequestFeatures{Stream: ex.req.Stream}` ->
// rich arm 的 vision/tools/json 全丢 -> 转红。
func TestPrepareRoute_ThreadsBodyDerivedCapabilities(t *testing.T) {
	richBody := `{"model":"claude-3-5-sonnet","stream":true,` +
		`"messages":[{"role":"user","content":[` +
		`{"type":"text","text":"look"},` +
		`{"type":"image_url","image_url":{"url":"https://example.com/cat.png"}}]}],` +
		`"tools":[{"type":"function","function":{"name":"f"}}],` +
		`"response_format":{"type":"json_schema","json_schema":{"name":"x","schema":{}}}}`
	baselineBody := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"plain text"}]}`

	run := func(t *testing.T, body string, stream bool) (router.PlanInput, []string) {
		t.Helper()
		rec := &recordingPlanInputRouter{delegate: router.NewDefaultRouter()}
		ex := &chatExecution{
			ctx:       context.Background(),
			ident:     auth.Identity{TenantID: 7, UserID: 3, APIKeyID: 9},
			d:         ChatHandlerDeps{Router: rec},
			body:      []byte(body),
			req:       chatRequest{Model: "claude-3-5-sonnet", Stream: stream},
			requestID: "r-route024",
		}
		ex.d.Registry = stubRegistry{resolved: registry.Resolved{
			PublicAlias:      "claude-3-5-sonnet",
			CanonicalModelID: "anthropic/claude-3-5-sonnet",
			ProviderModelID:  "claude-3-5-sonnet",
			ProtocolFamily:   "anthropic_messages",
			PoolCandidates:   []int64{42},
		}}
		if ok := ex.prepareRoute(httptest.NewRecorder()); !ok {
			t.Fatalf("prepareRoute returned false")
		}
		return rec.last, ex.attempt.RequiredCapabilities
	}

	richPlan, richCaps := run(t, richBody, true)
	if !richPlan.Features.WantsVision || !richPlan.Features.WantsToolUse || !richPlan.Features.WantsJSON {
		t.Fatalf("PlanInput.Features not driven by body: %+v", richPlan.Features)
	}
	if !richPlan.Features.Stream {
		t.Fatalf("PlanInput.Features.Stream regressed; want true for streamed request")
	}
	gotRich := capSet(richCaps)
	for _, want := range []string{"stream", "vision", "tools", "json"} {
		if !gotRich[want] {
			t.Fatalf("rich arm RequiredCapabilities missing %q; got %v", want, richCaps)
		}
	}
	if len(richCaps) != 4 {
		t.Fatalf("rich arm should carry exactly {stream,vision,tools,json}; got %v", richCaps)
	}

	basePlan, baseCaps := run(t, baselineBody, false)
	if basePlan.Features.WantsVision || basePlan.Features.WantsToolUse || basePlan.Features.WantsJSON || basePlan.Features.Stream {
		t.Fatalf("baseline arm should derive no capabilities; got %+v", basePlan.Features)
	}
	if len(baseCaps) != 0 {
		t.Fatalf("baseline arm RequiredCapabilities should be empty; got %v", baseCaps)
	}

	// 自证：两个分支必须不同。没有 :313 处的接线，两者都会塌缩成 {stream}/{}，
	// 这个守卫就会变红。
	if len(richCaps) == len(baseCaps) {
		t.Fatalf("rich and baseline arms must differ; rich=%v baseline=%v", richCaps, baseCaps)
	}
}

// TestPrepareRoute_StreamOnlyWhenBodyHasNoCaps 守 stream 不被回归:
// 一个 streamed 但无 vision/tools/json 的 body 只应得到 {stream}。
// 变异: :313 误删 Stream 字段 -> 转红。
func TestPrepareRoute_StreamOnlyWhenBodyHasNoCaps(t *testing.T) {
	rec := &recordingPlanInputRouter{delegate: router.NewDefaultRouter()}
	ex := &chatExecution{
		ctx:       context.Background(),
		ident:     auth.Identity{TenantID: 7, UserID: 3, APIKeyID: 9},
		d:         ChatHandlerDeps{Router: rec},
		body:      []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
		req:       chatRequest{Model: "gpt-4o", Stream: true},
		requestID: "r-route024-stream",
	}
	ex.d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "gpt-4o",
		CanonicalModelID: "openai/gpt-4o",
		ProviderModelID:  "gpt-4o",
		ProtocolFamily:   "openai_chat",
		PoolCandidates:   []int64{42},
	}}
	if ok := ex.prepareRoute(httptest.NewRecorder()); !ok {
		t.Fatalf("prepareRoute returned false")
	}
	caps := ex.attempt.RequiredCapabilities
	if len(caps) != 1 || caps[0] != "stream" {
		t.Fatalf("stream-only body should yield exactly [stream]; got %v", caps)
	}
}

// noAccountSelector 返回 AccountID==0 且 err==nil 的退化结果,复现 dispatch 的 no-account 兜底路径
// (选择器没报错却也没给出可用账号)。
type noAccountSelector struct{}

func (noAccountSelector) Select(context.Context, pool.SelectionRequest) (*pool.SelectionResult, error) {
	return &pool.SelectionResult{AccountID: 0}, nil
}

// TestSelectPoolAccount_NoAccountFallbackSetsRetryAfter 守 no-account 兜底分支(selRes.AccountID==0 且
// err==nil):返回的失败必须带默认 Retry-After,修"503 无退避头致客户端盲目重试"缺陷。
// 变异:删 dispatch 里 failure.RetryAfterSeconds = noCapacityFallbackRetryAfter → RetryAfterSeconds 归 0 → 红。
func TestSelectPoolAccount_NoAccountFallbackSetsRetryAfter(t *testing.T) {
	ex := &chatExecution{
		ctx:        context.Background(),
		ident:      auth.Identity{TenantID: 7, UserID: 3, APIKeyID: 9},
		d:          ChatHandlerDeps{Selector: noAccountSelector{}, Settler: &stubSettler{}},
		body:       []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
		req:        chatRequest{Model: "gpt-4o"},
		attempt:    router.AttemptPlan{PoolGroupID: 42},
		reserveRes: &billing.ReserveResult{},
		resolved:   registry.Resolved{ProtocolFamily: "openai_chat"},
	}

	f := ex.selectPoolAccount(httptest.NewRecorder(), attemptInput{AttemptSeq: 1})
	if f == nil {
		t.Fatalf("no-account 路径应返回失败,得 nil")
	}
	if f.RetryAfterSeconds != noCapacityFallbackRetryAfter {
		t.Fatalf("RetryAfterSeconds=%d want %d(no-account 兜底应带默认退避头)", f.RetryAfterSeconds, noCapacityFallbackRetryAfter)
	}
}
