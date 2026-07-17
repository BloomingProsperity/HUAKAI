package geminihttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeymodelallow"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	protogemini "github.com/BloomingProsperity/HUAKAI/internal/proto/gemini"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

const (
	ActionGenerateContent       = "generateContent"
	ActionStreamGenerateContent = "streamGenerateContent"
	ActionCountTokens           = "countTokens"

	endpointFamilyGenerateContent = "gemini_generate_content"
	maxRequestBodyBytes           = 2 << 20
	maxUpstreamBodyBytes          = 16 << 20
)

type NativeGateway interface {
	ServeNativeClient(http.ResponseWriter, *http.Request, gatewayhttp.NativeClientRequest)
}

type CountTokensRelay interface {
	ServeGeminiCountTokens(http.ResponseWriter, *http.Request, string)
}

type retryBudgetGate interface {
	Allow(tenantID int64) bool
}

type Deps struct {
	Gateway     NativeGateway
	CountTokens CountTokensRelay
	Models      http.Handler
	// Embeddings 是 OpenAI 形 /v1/embeddings 管线(embeddingshttp,完整
	// auth/配额/计费);embedContent/batchEmbedContents 经翻译包装复用它。
	Embeddings http.Handler
}

func NewDeps(
	chat gatewayhttp.ChatHandlerDeps,
	models http.Handler,
	embeddings http.Handler,
	feedback *upstreamfeedback.Observer,
	retryBudget retryBudgetGate,
) Deps {
	return Deps{
		Gateway:     gatewayhttp.NewNativeClientGateway(chat),
		CountTokens: NewCountTokensRelay(chat, feedback, retryBudget),
		Models:      models,
		Embeddings:  embeddings,
	}
}

func NewGenerateContentHandler(d Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1beta/models" {
			if d.Models == nil {
				writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "Gemini model list handler dependency unset")
				return
			}
			d.Models.ServeHTTP(w, r)
			return
		}

		model, action, ok := parseModelAction(r.URL.Path)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown_route", "Gemini v1beta route must be /v1beta/models/{model}:{action}")
			return
		}
		switch action {
		case ActionGenerateContent, ActionStreamGenerateContent:
			if d.Gateway == nil {
				writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "Gemini native gateway dependency unset")
				return
			}
			d.Gateway.ServeNativeClient(w, r, gatewayhttp.NativeClientRequest{
				Model:          model,
				Action:         action,
				Stream:         action == ActionStreamGenerateContent,
				ClientProtocol: proto.ClientProtocolGemini,
				ClientAdapter:  &protogemini.GeminiClient{},
				EndpointFamily: endpointFamilyGenerateContent,
			})
		case ActionEmbedContent:
			serveGeminiEmbed(w, r, model, d.Embeddings, false)
		case ActionBatchEmbedContents:
			serveGeminiEmbed(w, r, model, d.Embeddings, true)
		case ActionCountTokens:
			if d.CountTokens == nil {
				writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "Gemini countTokens dependency unset")
				return
			}
			d.CountTokens.ServeGeminiCountTokens(w, r, model)
		default:
			writeJSONError(w, http.StatusNotFound, "unknown_gemini_action", "unsupported Gemini v1beta action")
		}
	})
}

func parseModelAction(path string) (string, string, bool) {
	const prefix = "/v1beta/models/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	idx := strings.LastIndex(rest, ":")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", false
	}
	model, err := url.PathUnescape(rest[:idx])
	if err != nil || strings.TrimSpace(model) == "" {
		return "", "", false
	}
	return model, rest[idx+1:], true
}

type countTokensRelay struct {
	d           gatewayhttp.ChatHandlerDeps
	feedback    *upstreamfeedback.Observer
	retryBudget retryBudgetGate
}

func NewCountTokensRelay(
	d gatewayhttp.ChatHandlerDeps,
	feedback *upstreamfeedback.Observer,
	retryBudget retryBudgetGate,
) CountTokensRelay {
	return &countTokensRelay{d: d, feedback: feedback, retryBudget: retryBudget}
}

func (relay *countTokensRelay) ServeGeminiCountTokens(w http.ResponseWriter, r *http.Request, model string) {
	if relay == nil || !relay.configured() {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "Gemini countTokens handler dependency unset")
		return
	}
	ident, ok := relay.resolveAuth(w, r)
	if !ok {
		return
	}
	body, ok := readRequestBody(w, r)
	if !ok {
		return
	}
	if !validGeminiCountTokensBody(body) {
		writeJSONError(w, http.StatusBadRequest, "invalid_contents", "contents field must be a non-empty array")
		return
	}
	if !apikeymodelallow.AllowsCSV(ident.AllowedModels, model) {
		writeJSONError(w, http.StatusForbidden, "model_not_allowed", "api key is not allowed to use this model")
		return
	}

	requestID := uuid.NewString()
	// 请求 ID 通过显式 ctx 向下游传播(resolveModel/planRoute 均直接收 ctx),
	// 无需回写 r.Context();原 r = r.WithContext(ctx) 的回写值从未被读取,已移除(SA4006/SA4017)。
	ctx := context.WithValue(r.Context(), middleware.RequestIDKey, requestID)
	w.Header().Set(middleware.RequestIDHeader, requestID)

	resolved, ok := relay.resolveModel(w, ctx, model, ident)
	if !ok {
		return
	}
	plan, ok := relay.planRoute(w, ctx, requestID, model, ident, resolved)
	if !ok {
		return
	}
	relay.runCountTokens(w, ctx, requestID, model, body, ident, resolved, plan)
}

func (relay *countTokensRelay) configured() bool {
	d := relay.d
	return d.Auth != nil && d.Registry != nil && d.Router != nil &&
		d.Selector != nil && d.CredentialVault != nil && d.Dispatcher != nil
}

func (relay *countTokensRelay) resolveAuth(w http.ResponseWriter, r *http.Request) (auth.Identity, bool) {
	ident, err := relay.d.Auth.Resolve(r.Context(), r)
	if errors.Is(err, auth.ErrAuthMisconfigured) {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth tables unavailable")
		return auth.Identity{}, false
	}
	if errors.Is(err, auth.ErrAuthBackend) {
		writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
		return auth.Identity{}, false
	}
	if errors.Is(err, auth.ErrForbidden) {
		writeJSONError(w, http.StatusForbidden, "forbidden", "api key policy forbids this request")
		return auth.Identity{}, false
	}
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
		return auth.Identity{}, false
	}
	return ident, true
}

func (relay *countTokensRelay) resolveModel(w http.ResponseWriter, ctx context.Context, model string, ident auth.Identity) (registry.Resolved, bool) {
	resolved, err := relay.d.Registry.ResolveModel(ctx, model, ident.TenantID)
	if errors.Is(err, registry.ErrRegistryBackend) {
		writeJSONError(w, http.StatusServiceUnavailable, "registry_backend_error", "registry backend transient failure")
		return registry.Resolved{}, false
	}
	if errors.Is(err, registry.ErrUnknownModel) || errors.Is(err, registry.ErrModelDisabled) || errors.Is(err, registry.ErrTenantNoAccess) {
		writeJSONError(w, http.StatusNotFound, "model_not_available", "model not available")
		return registry.Resolved{}, false
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, clienterr.CodeRegistryUnknownError, clienterr.MessageFor(clienterr.CodeRegistryUnknownError))
		return registry.Resolved{}, false
	}
	return resolved, true
}

func (relay *countTokensRelay) planRoute(w http.ResponseWriter, ctx context.Context, requestID, model string, ident auth.Identity, resolved registry.Resolved) (router.RoutePlan, bool) {
	plan, err := relay.d.Router.Plan(ctx, router.PlanInput{
		Context: router.RequestContext{
			TenantID:  ident.TenantID,
			UserID:    ident.UserID,
			APIKeyID:  ident.APIKeyID,
			RequestID: requestID,
		},
		Model: routerResolvedModel(resolved),
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, clienterr.CodeRouterPlanError, clienterr.MessageFor(clienterr.CodeRouterPlanError))
		return router.RoutePlan{}, false
	}
	if len(plan.Attempts) == 0 {
		writeJSONError(w, http.StatusInternalServerError, clienterr.CodeRouterPlanError, "router returned no attempts")
		return router.RoutePlan{}, false
	}
	_ = model
	return plan, true
}

func (relay *countTokensRelay) selectAccount(
	w http.ResponseWriter,
	ctx context.Context,
	ident auth.Identity,
	model string,
	resolved registry.Resolved,
	attempt router.AttemptPlan,
	attemptSeq int,
	excludedAccounts map[int64]struct{},
) (*pool.SelectionResult, bool) {
	upstreamModelID := firstNonEmpty(attempt.UpstreamModelID, resolved.ProviderModelID, model)
	selRes, err := relay.d.Selector.Select(ctx, pool.SelectionRequest{
		TenantID:         ident.TenantID,
		UserID:           ident.UserID,
		APIKeyID:         ident.APIKeyID,
		PoolGroupID:      attempt.PoolGroupID,
		RequestedModel:   model,
		ModelCooldownKey: upstreamModelID,
		ProtocolFamily:   resolved.ProtocolFamily,
		EndpointFamily:   "gemini_count_tokens",
		AttemptSeq:       attemptSeq,
		ExcludedAccounts: excludedAccounts,
		CapabilityFlags:  attempt.RequiredCapabilities,
		Vendor:           pool.VendorFromProtocolFamily(resolved.ProtocolFamily),
		UserGroup:        ident.UserGroup,
	})
	if errors.Is(err, pool.ErrNoEligibleAccount) || errors.Is(err, pool.ErrNoSlotAvailable) || errors.Is(err, pool.ErrAllChannelsDegraded) {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodeNoCapacity, clienterr.MessageFor(clienterr.CodeNoCapacity))
		return nil, false
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, clienterr.CodePoolSelectError, clienterr.MessageFor(clienterr.CodePoolSelectError))
		return nil, false
	}
	if selRes == nil || selRes.AccountID == 0 || selRes.WaitPlan != nil {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodeNoCapacity, clienterr.MessageFor(clienterr.CodeNoCapacity))
		return nil, false
	}
	return selRes, true
}

func (relay *countTokensRelay) resolveCredential(w http.ResponseWriter, ctx context.Context, ident auth.Identity, accountID int64) (provider.Credential, provider.AccountInfo, bool) {
	cred, accInfo, err := relay.d.CredentialVault.Resolve(ctx, ident.TenantID, accountID)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodeCredentialResolveError, clienterr.MessageFor(clienterr.CodeCredentialResolveError))
		return provider.Credential{}, provider.AccountInfo{}, false
	}
	if accInfo.AccountID == 0 {
		accInfo.AccountID = accountID
	}
	return cred, accInfo, true
}

func readRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeBodyReadError, clienterr.MessageFor(clienterr.CodeBodyReadError))
		return nil, false
	}
	return body, true
}

func validGeminiCountTokensBody(body []byte) bool {
	var req struct {
		Contents []json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}
	return len(req.Contents) > 0
}

func readUpstreamBody(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxUpstreamBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxUpstreamBodyBytes {
		return nil, errors.New("gemini upstream response exceeds size limit")
	}
	return raw, nil
}

func routerResolvedModel(resolved registry.Resolved) router.ResolvedModel {
	return router.ResolvedModel{
		PublicAlias:     resolved.PublicAlias,
		InternalModelID: resolved.CanonicalModelID,
		ProviderModelID: resolved.ProviderModelID,
		ContextWindow:   resolved.ContextWindow,
		Capabilities:    resolved.Capabilities,
		PricingClass:    resolved.PricingClass,
		ProtocolFamily:  resolved.ProtocolFamily,
		PoolCandidates:  resolved.PoolCandidates,
		PoolMetadata:    routerPoolMetadataFromRegistry(resolved),
		SnapshotVersion: resolved.SnapshotVersion,
	}
}

func routerPoolMetadataFromRegistry(resolved registry.Resolved) []router.PoolCandidateMeta {
	if len(resolved.BindingMetadata) == 0 {
		return nil
	}
	defaultProviderModelID := resolved.DefaultProviderModelID
	if defaultProviderModelID == "" {
		defaultProviderModelID = resolved.ProviderModelID
	}
	out := make([]router.PoolCandidateMeta, 0, len(resolved.BindingMetadata))
	for _, binding := range resolved.BindingMetadata {
		if binding.PoolGroupID == 0 {
			continue
		}
		providerModelID := defaultProviderModelID
		if binding.ProviderModelIDOverride != nil && *binding.ProviderModelIDOverride != "" {
			providerModelID = *binding.ProviderModelIDOverride
		}
		out = append(out, router.PoolCandidateMeta{
			PoolGroupID:     binding.PoolGroupID,
			ProviderModelID: providerModelID,
		})
	}
	return out
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
