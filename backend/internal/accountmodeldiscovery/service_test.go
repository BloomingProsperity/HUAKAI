package accountmodeldiscovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

type stubVault struct {
	credential provider.Credential
	account    provider.AccountInfo
	err        error
}

func (s stubVault) Resolve(context.Context, int64, int64) (provider.Credential, provider.AccountInfo, error) {
	return s.credential, s.account, s.err
}

type queuedDispatcher struct {
	inputs    []gateway.DispatchInput
	responses []*gateway.DispatchResult
	err       error
}

func (s *queuedDispatcher) Dispatch(_ context.Context, in gateway.DispatchInput) (*gateway.DispatchResult, error) {
	s.inputs = append(s.inputs, in)
	if s.err != nil {
		return nil, s.err
	}
	if len(s.responses) == 0 {
		return nil, errors.New("缺少桩响应")
	}
	result := s.responses[0]
	s.responses = s.responses[1:]
	return result, nil
}

func response(status int, body string) *gateway.DispatchResult {
	return &gateway.DispatchResult{
		StatusCode: status, Headers: make(http.Header),
		UpstreamReader: strings.NewReader(body), Close: func() error { return nil },
	}
}

func TestPlanForAccountCoversSupportedCredentialFamilies(t *testing.T) {
	tests := []struct {
		name, vendor, mode, family, method, path string
	}{
		{"OpenAI 官方 Key", credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey, registrydefault.ProtocolOpenAIChat, http.MethodGet, "/v1/models"},
		{"Codex 会话", credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth, registrydefault.ProtocolOpenAICodex, http.MethodGet, "/backend-api/codex/models"},
		{"Claude 官方 Key", credentialstore.VendorAnthropic, credentialstore.AuthModeAPIKey, registrydefault.ProtocolAnthropicMessages, http.MethodGet, "/v1/models"},
		{"Claude Setup Token", credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeSetupToken, registrydefault.ProtocolAnthropicClaudeSession, http.MethodGet, "/v1/models"},
		{"Gemini 官方 Key", credentialstore.VendorGemini, credentialstore.AuthModeAIStudioAPIKey, registrydefault.ProtocolGeminiMessages, http.MethodGet, "/v1beta/models"},
		{"Gemini Code Assist", credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist, registrydefault.ProtocolGeminiCodeAssist, http.MethodPost, "/v1internal:fetchAvailableModels"},
		{"Antigravity", credentialstore.VendorGemini, credentialstore.AuthModeAntigravity, registrydefault.ProtocolAntigravitySession, http.MethodPost, "/v1internal:fetchAvailableModels"},
		{"Grok 官方 Key", credentialstore.VendorGrok, credentialstore.AuthModeAPIKey, registrydefault.ProtocolGrokChat, http.MethodGet, "/v1/models"},
		{"Kimi OAuth", credentialstore.VendorKimi, credentialstore.AuthModeKimiOAuth, registrydefault.ProtocolKimiChat, http.MethodGet, "/coding/v1/models"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := planForAccount(provider.AccountInfo{Platform: test.vendor, AccountType: test.mode}, provider.Credential{Extra: map[string]string{"codex_version": "1.2.3"}})
			if err != nil {
				t.Fatal(err)
			}
			if plan.protocolFamily != test.family || plan.method != test.method || plan.endpointPath != test.path {
				t.Fatalf("计划=%+v，期望 family=%s method=%s path=%s", plan, test.family, test.method, test.path)
			}
		})
	}
}

func TestPlanForAccountPreservesStaticUpstreamAndUsesStandardDispatcher(t *testing.T) {
	plan, err := planForAccount(provider.AccountInfo{
		Platform: "custom_vendor", AccountType: "upstream_static",
	}, provider.Credential{
		Type:  provider.CredentialTypeUpstreamPassthrough,
		Extra: map[string]string{"base_url": "https://upstream.example/api/v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.protocolFamily != registrydefault.ProtocolOpenAIChat || plan.endpointPath != "/v1/models" || plan.dispatchVendor != credentialstore.VendorOpenAI {
		t.Fatalf("静态上游计划=%+v", plan)
	}

	dispatcher := &queuedDispatcher{responses: []*gateway.DispatchResult{
		response(http.StatusOK, `{"data":[{"id":"custom-model"}]}`),
	}}
	service := NewService(stubVault{
		credential: provider.Credential{
			Type: provider.CredentialTypeUpstreamPassthrough, Value: "Bearer secret",
			Extra: map[string]string{"base_url": "https://upstream.example/api/v1"},
		},
		account: provider.AccountInfo{AccountID: 9, TenantID: 7, Platform: "custom_vendor", AccountType: "upstream_static"},
	}, dispatcher, nil, nil)
	if _, err := service.Discover(context.Background(), 7, 9); err != nil {
		t.Fatal(err)
	}
	if got := dispatcher.inputs[0].Account.Platform; got != credentialstore.VendorOpenAI {
		t.Fatalf("Dispatcher transport vendor=%q，期望通用标准出口 %q", got, credentialstore.VendorOpenAI)
	}
	if got := dispatcher.inputs[0].TransportMode; got != "" {
		t.Fatalf("服务不得绕过 Dispatcher 强制 transport，得到 %q", got)
	}
}

func TestPlanForAzureRequiresResourceEndpointAndUsesAzureModelsPath(t *testing.T) {
	if _, err := planForAccount(provider.AccountInfo{
		Platform: credentialstore.VendorOpenAI, AccountType: credentialstore.AuthModeAzure,
	}, provider.Credential{Type: provider.CredentialTypeAPIKey}); KindOf(err) != ErrorUnsupported {
		t.Fatalf("缺少资源 endpoint 的 Azure 凭据应拒绝，err=%v", err)
	}
	plan, err := planForAccount(provider.AccountInfo{
		Platform: credentialstore.VendorOpenAI, AccountType: credentialstore.AuthModeAzure,
	}, provider.Credential{
		Type: provider.CredentialTypeUpstreamPassthrough,
		Extra: map[string]string{
			"base_url":          "https://resource.openai.azure.com/openai/v1",
			"azure_api_version": "preview",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.endpointPath != "/openai/v1/models" || plan.query.Get("api-version") != "preview" {
		t.Fatalf("Azure 模型计划=%+v", plan)
	}
}

func TestDiscoverPaginatesAnthropicAndNormalizesModels(t *testing.T) {
	dispatcher := &queuedDispatcher{responses: []*gateway.DispatchResult{
		response(http.StatusOK, `{"data":[{"id":"claude-b","display_name":"B"}],"has_more":true,"last_id":"cursor-1"}`),
		response(http.StatusOK, `{"data":[{"id":"claude-a","display_name":"A"},{"id":"claude-b","display_name":"重复"}],"has_more":false}`),
	}}
	service := NewService(stubVault{
		credential: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		account: provider.AccountInfo{AccountID: 9, TenantID: 7, Platform: credentialstore.VendorAnthropic,
			AccountType: credentialstore.AuthModeAPIKey, AccountCredentialID: 11, CredentialVersion: 3},
	}, dispatcher, nil, nil)
	result, err := service.Discover(context.Background(), 7, 9)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(result.ModelIDs(), ","); got != "claude-a,claude-b" {
		t.Fatalf("模型=%q，期望去重并排序", got)
	}
	if len(dispatcher.inputs) != 2 || dispatcher.inputs[0].EndpointQuery != "" || dispatcher.inputs[1].EndpointQuery != "after_id=cursor-1" {
		t.Fatalf("分页查询错误: %+v", dispatcher.inputs)
	}
	for _, input := range dispatcher.inputs {
		if input.TransportMode != "" {
			t.Fatalf("服务不应绕过统一 Dispatcher 强制 transport: %q", input.TransportMode)
		}
		if input.ProtocolFamily != registrydefault.ProtocolAnthropicMessages || input.HTTPMethod != http.MethodGet {
			t.Fatalf("协议或方法错误: %+v", input)
		}
	}
}

func TestDiscoverClassifiesUpstreamStatusWithoutLeakingBody(t *testing.T) {
	for _, test := range []struct {
		status int
		kind   ErrorKind
	}{
		{http.StatusUnauthorized, ErrorCredentialRejected},
		{http.StatusForbidden, ErrorCredentialRejected},
		{http.StatusTooManyRequests, ErrorRateLimited},
		{http.StatusBadGateway, ErrorUpstream},
	} {
		dispatcher := &queuedDispatcher{responses: []*gateway.DispatchResult{response(test.status, "secret upstream body")}}
		service := NewService(stubVault{
			credential: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
			account:    provider.AccountInfo{Platform: credentialstore.VendorOpenAI, AccountType: credentialstore.AuthModeAPIKey},
		}, dispatcher, nil, nil)
		_, err := service.Discover(context.Background(), 1, 2)
		if KindOf(err) != test.kind {
			t.Fatalf("status=%d kind=%q，期望 %q，err=%v", test.status, KindOf(err), test.kind, err)
		}
		if strings.Contains(err.Error(), "secret upstream body") {
			t.Fatal("错误不得泄露上游正文")
		}
		var discoveryErr *DiscoveryError
		if !errors.As(err, &discoveryErr) || discoveryErr.Vendor != credentialstore.VendorOpenAI || discoveryErr.AuthMode != credentialstore.AuthModeAPIKey {
			t.Fatalf("失败必须回填账号族供失败日志辨识: %+v", discoveryErr)
		}
	}
}

func TestDispatchPageClosesResponse(t *testing.T) {
	closed := false
	dispatcher := &queuedDispatcher{responses: []*gateway.DispatchResult{{
		StatusCode: http.StatusOK, UpstreamReader: io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-4o"}]}`)),
		Close: func() error { closed = true; return nil },
	}}}
	service := NewService(stubVault{
		credential: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		account:    provider.AccountInfo{Platform: credentialstore.VendorOpenAI, AccountType: credentialstore.AuthModeAPIKey},
	}, dispatcher, nil, nil)
	if _, err := service.Discover(context.Background(), 1, 2); err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("模型发现必须关闭上游响应")
	}
}
