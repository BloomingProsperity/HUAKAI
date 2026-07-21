package accountmodeldiscovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

type parserKind string

const (
	parserOpenAI    parserKind = "openai"
	parserCodex     parserKind = "codex"
	parserAnthropic parserKind = "anthropic"
	parserGemini    parserKind = "gemini"
	parserCloudCode parserKind = "cloud_code"
)

type requestPlan struct {
	protocolFamily string
	method         string
	endpointPath   string
	parser         parserKind
	query          url.Values
	dispatchVendor string
}

func (s *Service) Discover(ctx context.Context, tenantID, accountID int64) (_ Result, retErr error) {
	if s == nil || s.vault == nil || s.dispatcher == nil {
		return Result{}, &DiscoveryError{Kind: ErrorNotConfigured}
	}
	if tenantID <= 0 || accountID <= 0 {
		return Result{}, &DiscoveryError{Kind: ErrorAccountUnavailable, Err: errors.New("tenant_id 和 account_id 必须为正数")}
	}
	credential, account, err := s.vault.Resolve(ctx, tenantID, accountID)
	if err != nil {
		return Result{}, &DiscoveryError{Kind: ErrorAccountUnavailable, Err: err}
	}
	defer func() {
		if retErr != nil {
			annotate(retErr, credentialstore.Normalize(account.Platform), credentialstore.Normalize(account.AccountType))
		}
	}()
	plan, err := planForAccount(account, credential)
	if err != nil {
		return Result{}, err
	}

	timeout := s.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	models := make([]Model, 0)
	for page := 0; page < maxPages; page++ {
		body, status, err := s.dispatchPage(requestCtx, plan, account, credential)
		if err != nil {
			return Result{}, err
		}
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			return Result{}, statusError(status)
		}
		pageModels, nextQuery, err := parsePage(plan.parser, plan.protocolFamily, body)
		if err != nil {
			return Result{}, &DiscoveryError{Kind: ErrorResponseInvalid, Err: err}
		}
		models = append(models, pageModels...)
		if len(models) > maxModels {
			return Result{}, &DiscoveryError{Kind: ErrorCatalogTooLarge, Err: fmt.Errorf("模型数量超过 %d", maxModels)}
		}
		if len(nextQuery) == 0 {
			break
		}
		if page == maxPages-1 {
			return Result{}, &DiscoveryError{Kind: ErrorCatalogTooLarge, Err: errors.New("模型目录分页超过上限")}
		}
		plan.query = nextQuery
	}
	models = normalizeModels(models)
	if len(models) == 0 {
		return Result{}, &DiscoveryError{Kind: ErrorEmptyCatalog}
	}
	return Result{
		AccountID: accountID, AccountCredentialID: account.AccountCredentialID,
		CredentialVersion: account.CredentialVersion, Vendor: credentialstore.Normalize(account.Platform),
		AuthMode: credentialstore.Normalize(account.AccountType), ProtocolFamily: plan.protocolFamily,
		Models: models, DiscoveredAt: time.Now().UTC(),
	}, nil
}

func (s *Service) dispatchPage(ctx context.Context, plan requestPlan, account provider.AccountInfo, credential provider.Credential) ([]byte, int, error) {
	if plan.dispatchVendor != "" {
		account.Platform = plan.dispatchVendor
	}
	result, err := s.dispatcher.Dispatch(ctx, gateway.DispatchInput{
		HTTPMethod: plan.method, ProtocolFamily: plan.protocolFamily,
		EndpointPath: plan.endpointPath, EndpointQuery: plan.query.Encode(),
		InboundBody: []byte(`{}`), Account: account, Credential: credential,
		NonStreamingBuffered: true,
	})
	if err != nil {
		return nil, 0, &DiscoveryError{Kind: ErrorUpstream, Err: err}
	}
	if result == nil {
		return nil, 0, &DiscoveryError{Kind: ErrorUpstream, Err: errors.New("上游返回空结果")}
	}
	if result.Close != nil {
		defer result.Close()
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(result.UpstreamReader, maxResponseBytes))
		return nil, result.StatusCode, nil
	}
	limited := io.LimitReader(result.UpstreamReader, maxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, 0, &DiscoveryError{Kind: ErrorUpstream, Err: err}
	}
	if len(body) > maxResponseBytes {
		return nil, 0, &DiscoveryError{Kind: ErrorCatalogTooLarge, Err: errors.New("上游响应超过 8 MiB")}
	}
	return body, result.StatusCode, nil
}

func statusError(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &DiscoveryError{Kind: ErrorCredentialRejected, StatusCode: status}
	case http.StatusTooManyRequests:
		return &DiscoveryError{Kind: ErrorRateLimited, StatusCode: status}
	default:
		return &DiscoveryError{Kind: ErrorUpstream, StatusCode: status}
	}
}

func planForAccount(account provider.AccountInfo, credential provider.Credential) (requestPlan, error) {
	vendor := credentialstore.Normalize(account.Platform)
	authMode := credentialstore.Normalize(account.AccountType)
	if authMode == "upstream_static" {
		if credential.Type != provider.CredentialTypeUpstreamPassthrough || strings.TrimSpace(credential.Extra["base_url"]) == "" {
			return requestPlan{}, &DiscoveryError{Kind: ErrorUnsupported, Err: errors.New("upstream_static 模型发现需要带 base_url 的透传凭据")}
		}
		// 自定义 OpenAI 兼容上游没有固定 vendor transport，统一按标准
		// OpenAI 兼容协议构造请求，真实目标仍由凭据中的 base_url 决定。
		return requestPlan{
			protocolFamily: registrydefault.ProtocolOpenAIChat,
			method:         http.MethodGet,
			endpointPath:   "/v1/models",
			parser:         parserOpenAI,
			query:          url.Values{},
			dispatchVendor: credentialstore.VendorOpenAI,
		}, nil
	}
	switch vendor {
	case credentialstore.VendorOpenAI:
		switch authMode {
		case credentialstore.AuthModeAPIKey, credentialstore.AuthModeRefreshToken:
			return requestPlan{protocolFamily: registrydefault.ProtocolOpenAIChat, method: http.MethodGet, endpointPath: "/v1/models", parser: parserOpenAI, query: url.Values{}}, nil
		case credentialstore.AuthModeAzure:
			if credential.Type != provider.CredentialTypeUpstreamPassthrough || strings.TrimSpace(credential.Extra["base_url"]) == "" {
				return requestPlan{}, &DiscoveryError{Kind: ErrorUnsupported, Err: errors.New("azure 模型发现需要 Entra Bearer 凭据与资源 base_url")}
			}
			query := url.Values{}
			if version := strings.TrimSpace(credential.Extra["azure_api_version"]); version != "" {
				query.Set("api-version", version)
			}
			return requestPlan{protocolFamily: registrydefault.ProtocolOpenAIChat, method: http.MethodGet,
				endpointPath: "/openai/v1/models", parser: parserOpenAI, query: query}, nil
		case credentialstore.AuthModeChatGPTOAuth, credentialstore.AuthModeCodexCLIOAuth,
			credentialstore.AuthModeCodexWebOAuth, credentialstore.AuthModeCodexAgent:
			version := strings.TrimSpace(credential.Extra["codex_version"])
			if version == "" {
				version = "0.0.0"
			}
			return requestPlan{protocolFamily: registrydefault.ProtocolOpenAICodex, method: http.MethodGet,
				endpointPath: "/backend-api/codex/models", parser: parserCodex,
				query: url.Values{"client_version": []string{version}}}, nil
		}
	case credentialstore.VendorAnthropic:
		switch authMode {
		case credentialstore.AuthModeAPIKey:
			return requestPlan{protocolFamily: registrydefault.ProtocolAnthropicMessages, method: http.MethodGet, endpointPath: "/v1/models", parser: parserAnthropic, query: url.Values{}}, nil
		case credentialstore.AuthModeClaudeAIOAuth, credentialstore.AuthModeClaudeCode, credentialstore.AuthModeClaudeSetupToken:
			return requestPlan{protocolFamily: registrydefault.ProtocolAnthropicClaudeSession, method: http.MethodGet, endpointPath: "/v1/models", parser: parserAnthropic, query: url.Values{}}, nil
		}
	case credentialstore.VendorGemini:
		switch authMode {
		case credentialstore.AuthModeAIStudioAPIKey:
			return requestPlan{protocolFamily: registrydefault.ProtocolGeminiMessages, method: http.MethodGet, endpointPath: "/v1beta/models", parser: parserGemini, query: url.Values{}}, nil
		case credentialstore.AuthModeCodeAssist:
			return requestPlan{protocolFamily: registrydefault.ProtocolGeminiCodeAssist, method: http.MethodPost, endpointPath: "/v1internal:fetchAvailableModels", parser: parserCloudCode, query: url.Values{}}, nil
		case credentialstore.AuthModeAntigravity:
			return requestPlan{protocolFamily: registrydefault.ProtocolAntigravitySession, method: http.MethodPost, endpointPath: "/v1internal:fetchAvailableModels", parser: parserCloudCode, query: url.Values{}}, nil
		}
	case credentialstore.VendorAntigravity:
		if authMode == credentialstore.AuthModeOAuth {
			return requestPlan{protocolFamily: registrydefault.ProtocolAntigravitySession, method: http.MethodPost, endpointPath: "/v1internal:fetchAvailableModels", parser: parserCloudCode, query: url.Values{}}, nil
		}
	case credentialstore.VendorGrok:
		if authMode == credentialstore.AuthModeAPIKey || authMode == credentialstore.AuthModeXAIOAuth {
			return requestPlan{protocolFamily: registrydefault.ProtocolGrokChat, method: http.MethodGet, endpointPath: "/v1/models", parser: parserOpenAI, query: url.Values{}}, nil
		}
	case credentialstore.VendorKimi:
		if authMode == credentialstore.AuthModeAPIKey || authMode == credentialstore.AuthModeKimiOAuth {
			return requestPlan{protocolFamily: registrydefault.ProtocolKimiChat, method: http.MethodGet, endpointPath: "/coding/v1/models", parser: parserOpenAI, query: url.Values{}}, nil
		}
	}
	return requestPlan{}, &DiscoveryError{Kind: ErrorUnsupported, Err: fmt.Errorf("%s/%s 没有账号级模型发现合同", vendor, authMode)}
}

func normalizeModels(models []Model) []Model {
	byID := make(map[string]Model, len(models))
	for _, model := range models {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" {
			continue
		}
		if model.DisplayName = strings.TrimSpace(model.DisplayName); model.DisplayName == "" {
			model.DisplayName = model.ID
		}
		model.Capabilities = cleanStrings(model.Capabilities)
		if _, exists := byID[model.ID]; !exists {
			byID[model.ID] = model
		}
	}
	out := make([]Model, 0, len(byID))
	for _, model := range byID {
		out = append(out, model)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func cleanStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
