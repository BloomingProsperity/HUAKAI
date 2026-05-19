package gatewayhttp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

func TestHandleStreamingResponse_CrossProtocolTranslatesRequestAndResponse(t *testing.T) {
	upstream := &recordingStreamingDoer{responseBody: anthropicStreamingFixture()}
	adapterReg := provider.NewStaticRegistry()
	adapterReg.MustRegister("anthropic_messages", &anthropic.PassthroughAdapter{})
	reqBody := []byte(`{"model":"gpt-4o","stream":true,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(reqBody)))
	ex := &chatExecution{
		d: ChatHandlerDeps{
			Dispatcher: &gateway.UpstreamDispatcher{
				Adapters:         adapterReg,
				TransportFactory: transport.NewFactory(),
				HTTPClient:       upstream,
			},
			Forwarder: &gateway.StreamForwarder{
				ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
				Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
				ScannerBufferCap: 1 << 20,
			},
			Settler: &stubSettler{},
		},
		r:                 req,
		ctx:               req.Context(),
		startedAt:         time.Now(),
		ident:             auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 3},
		body:              reqBody,
		req:               chatRequest{Model: "gpt-4o", Stream: true},
		clientProtocol:    proto.ClientProtocolOpenAIChat,
		requestID:         "req_stream_cross_protocol",
		resolved:          registry.Resolved{ProtocolFamily: "anthropic_messages", ProviderModelID: "claude-3-5-haiku"},
		plan:              router.RoutePlan{SnapshotVersion: "test-snapshot"},
		attempt:           router.AttemptPlan{PoolGroupID: 42},
		routeID:           "route-1",
		reserveRes:        &billing.ReserveResult{ClaimID: 999},
		acquiredAccountID: 1,
		upstreamModelID:   "claude-3-5-haiku",
		cacheVendor:       "anthropic",
		cred:              provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant-test"},
		accInfo:           provider.AccountInfo{AccountID: 1, Platform: "anthropic", AccountType: "apikey"},
		forwardReq: gateway.ForwardRequest{
			TenantID:       7,
			AccountID:      1,
			RequestID:      "req_stream_cross_protocol",
			RouteID:        "route-1",
			PoolID:         "42",
			IngressPath:    "/v1/chat/completions",
			ProtocolFamily: "anthropic_messages",
			ClientProtocol: string(proto.ClientProtocolOpenAIChat),
			Model:          "claude-3-5-haiku",
			RequestedModel: "gpt-4o",
			Provider:       "anthropic",
		},
	}

	rec := httptest.NewRecorder()
	ex.handleStreamingResponse(rec)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var sent map[string]any
	if err := json.Unmarshal(upstream.body, &sent); err != nil {
		t.Fatalf("upstream body json: %v\n%s", err, string(upstream.body))
	}
	if sent["model"] != "claude-3-5-haiku" {
		t.Fatalf("upstream model=%v want claude-3-5-haiku; body=%s", sent["model"], string(upstream.body))
	}
	if sent["stream"] != true {
		t.Fatalf("upstream stream=%v want true; body=%s", sent["stream"], string(upstream.body))
	}
	if sent["max_tokens"] != float64(16) {
		t.Fatalf("upstream max_tokens=%v want 16; body=%s", sent["max_tokens"], string(upstream.body))
	}
	messages, ok := sent["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("upstream messages=%T/%v; body=%s", sent["messages"], sent["messages"], string(upstream.body))
	}
	first, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("first message=%T", messages[0])
	}
	content, ok := first["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("translated content=%T/%v; body=%s", first["content"], first["content"], string(upstream.body))
	}
	block, ok := content[0].(map[string]any)
	if !ok || block["type"] != "text" || block["text"] != "hi" {
		t.Fatalf("translated block=%T/%v; body=%s", content[0], content[0], string(upstream.body))
	}
	if strings.Contains(string(upstream.body), `"content":"hi"`) {
		t.Fatalf("raw OpenAI body leaked to Anthropic upstream: %s", string(upstream.body))
	}
	clientBody := rec.Body.String()
	if strings.Contains(clientBody, "event: message_start") {
		t.Fatalf("raw Anthropic SSE leaked to OpenAI client: %s", clientBody)
	}
	if !strings.Contains(clientBody, `"object":"chat.completion.chunk"`) || !strings.Contains(clientBody, "data: [DONE]") {
		t.Fatalf("client body did not use OpenAI Chat SSE shape: %s", clientBody)
	}
}

type recordingStreamingDoer struct {
	body         []byte
	responseBody string
}

func (d *recordingStreamingDoer) Do(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	d.body = body
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(d.responseBody)),
	}, nil
}

func anthropicStreamingFixture() string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-3-5-haiku","usage":{"input_tokens":1}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")
}

var _ gateway.HTTPDoer = (*recordingStreamingDoer)(nil)
