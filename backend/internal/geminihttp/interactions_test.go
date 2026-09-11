package geminihttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestGeminiInteractionsCreateAndGetRouting(t *testing.T) {
	t.Parallel()
	gateway := &recordingNativeGateway{}
	handler := NewGenerateContentHandler(Deps{Gateway: gateway})

	create := httptest.NewRequest(http.MethodPost, "/v1beta/interactions", strings.NewReader(`{"model":"gemini-3.6-flash","input":"hi","stream":true}`))
	create.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, create)
	if createRec.Code != http.StatusNoContent {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	if len(gateway.calls) != 1 {
		t.Fatalf("create calls=%d", len(gateway.calls))
	}
	got := gateway.calls[0]
	if got.Model != "gemini-3.6-flash" || got.Action != ActionCreateInteraction || !got.Stream ||
		got.EndpointPath != officialInteractionsPath || got.HTTPMethod != http.MethodPost ||
		got.ClientProtocol != proto.ClientProtocolGemini || got.EndpointFamily != endpointFamilyInteractions {
		t.Fatalf("create native=%+v", got)
	}

	missing := httptest.NewRequest(http.MethodPost, "/v1beta/interactions", strings.NewReader(`{"input":"hi"}`))
	missing.Header.Set("Content-Type", "application/json")
	missingRec := httptest.NewRecorder()
	handler.ServeHTTP(missingRec, missing)
	if missingRec.Code != http.StatusBadRequest {
		t.Fatalf("missing model/agent status=%d", missingRec.Code)
	}
	if !strings.Contains(missingRec.Body.String(), "model or agent") {
		t.Fatalf("missing model 文案=%s", missingRec.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/v1beta/interactions/v1_abc?model=gemini-3.6-flash", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, get)
	if getRec.Code != http.StatusNoContent {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	if len(gateway.calls) != 2 {
		t.Fatalf("after get calls=%d", len(gateway.calls))
	}
	gotGet := gateway.calls[1]
	if gotGet.Action != ActionGetInteraction || gotGet.HTTPMethod != http.MethodGet ||
		gotGet.EndpointPath != "/v1beta/interactions/v1_abc" || gotGet.Model != "gemini-3.6-flash" {
		t.Fatalf("get native=%+v", gotGet)
	}

	getNoModel := httptest.NewRequest(http.MethodGet, "/v1beta/interactions/v1_abc", nil)
	getNoModelRec := httptest.NewRecorder()
	handler.ServeHTTP(getNoModelRec, getNoModel)
	if getNoModelRec.Code != http.StatusBadRequest {
		t.Fatalf("get without model status=%d", getNoModelRec.Code)
	}
}

func TestGeminiInteractionsDoesNotStealGenerateContent(t *testing.T) {
	t.Parallel()
	gateway := &recordingNativeGateway{}
	handler := NewGenerateContentHandler(Deps{Gateway: gateway})
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:generateContent", strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || len(gateway.calls) != 1 || gateway.calls[0].Action != ActionGenerateContent {
		t.Fatalf("generateContent 被会话入口误伤: status=%d calls=%+v", rec.Code, gateway.calls)
	}
}

func TestParseInteractionsCreateDiscriminatesWinnerAndLoser(t *testing.T) {
	t.Parallel()
	model, agent, stream, err := parseInteractionsCreate([]byte(`{"agent":"deep-research-preview-04-2026","stream":false}`))
	if err != nil || model != "" || agent != "deep-research-preview-04-2026" || stream {
		t.Fatalf("agent 请求解析失败: model=%q agent=%q stream=%v err=%v", model, agent, stream, err)
	}
	if _, _, _, err := parseInteractionsCreate([]byte(`{"input":"x"}`)); err == nil {
		t.Fatal("缺 model/agent 的基线必须失败")
	}
}
