package geminihttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

type recordingNativeGateway struct {
	calls []gatewayhttp.NativeClientRequest
}

func (g *recordingNativeGateway) ServeNativeClient(w http.ResponseWriter, r *http.Request, req gatewayhttp.NativeClientRequest) {
	g.calls = append(g.calls, req)
	w.WriteHeader(http.StatusNoContent)
}

func TestGeminiIngressRoutingSelectsStreamingFromActionSuffix(t *testing.T) {
	gateway := &recordingNativeGateway{}
	handler := NewGenerateContentHandler(Deps{Gateway: gateway})

	streamReq := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:streamGenerateContent", strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	streamReq.Header.Set("Content-Type", "application/json")
	streamRec := httptest.NewRecorder()
	handler.ServeHTTP(streamRec, streamReq)

	if streamRec.Code != http.StatusNoContent {
		t.Fatalf("stream status=%d body=%s want 204", streamRec.Code, streamRec.Body.String())
	}
	if len(gateway.calls) != 1 {
		t.Fatalf("gateway calls=%d want 1", len(gateway.calls))
	}
	if got := gateway.calls[0]; got.Model != "gemini-pro" || got.Action != ActionStreamGenerateContent || !got.Stream || got.ClientProtocol != proto.ClientProtocolGemini {
		t.Fatalf("stream native request=%+v want gemini-pro stream Gemini protocol", got)
	}

	generateReq := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:generateContent", strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	generateReq.Header.Set("Content-Type", "application/json")
	generateRec := httptest.NewRecorder()
	handler.ServeHTTP(generateRec, generateReq)

	if generateRec.Code != http.StatusNoContent {
		t.Fatalf("generate status=%d body=%s want 204", generateRec.Code, generateRec.Body.String())
	}
	if len(gateway.calls) != 2 {
		t.Fatalf("gateway calls=%d want 2", len(gateway.calls))
	}
	if got := gateway.calls[1]; got.Model != "gemini-pro" || got.Action != ActionGenerateContent || got.Stream || got.ClientProtocol != proto.ClientProtocolGemini {
		t.Fatalf("generate native request=%+v want gemini-pro non-stream Gemini protocol", got)
	}
}
