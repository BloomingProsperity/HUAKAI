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

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

type mockCanonicalBufferedDispatcher struct {
	calls    int
	err      error
	observed *CanonicalBufferedDispatchInput
}

func (m *mockCanonicalBufferedDispatcher) DispatchCanonicalBuffered(_ context.Context, in CanonicalBufferedDispatchInput) (*proto.HCSF, error) {
	m.calls++
	observed := in
	m.observed = &observed
	if m.err != nil {
		return nil, m.err
	}
	env := proto.NewEmptyEnvelope()
	env.RequestMeta = in.RequestEnvelope.RequestMeta
	env.BufferedResponse = &proto.CanonicalResponse{
		ID:         "chatcmpl-clientadapter-test",
		Model:      in.UpstreamModelID,
		Content:    []proto.CanonicalContentBlock{{Type: "text", Text: "hello from canonical"}},
		Usage:      proto.CanonicalUsage{InputTokens: 2, OutputTokens: 3},
		StopReason: proto.CanonicalStopEndTurn,
	}
	env.Accounting.Usage = env.BufferedResponse.Usage
	env.Accounting.EvidenceLabel = proto.EvidenceMock
	return env, nil
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
		AccountID:   1,
		Platform:    "openai",
		AccountType: "apikey",
	}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	d.CredentialVault = vault
	return d
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
	if dispatcher.observed == nil || dispatcher.observed.RequestEnvelope == nil {
		t.Fatal("canonical dispatcher did not receive request envelope")
	}
	meta := dispatcher.observed.RequestEnvelope.RequestMeta
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

func TestChatCompletionsClientAdapter_NonStreamingUnknownPath(t *testing.T) {
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
	if !strings.Contains(rec.Body.String(), "messages[0].content") {
		t.Fatalf("body = %q; want concrete adapter diagnostic", rec.Body.String())
	}
	if dispatcher.calls != 0 {
		t.Fatalf("canonical dispatcher calls = %d; want 0", dispatcher.calls)
	}
}

func TestChatCompletionsClientAdapter_StreamTrueBypassesCanonicalDispatcher(t *testing.T) {
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
