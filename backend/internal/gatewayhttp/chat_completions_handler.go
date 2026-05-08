package gatewayhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/cache_routing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

type authResolver interface {
	Resolve(ctx context.Context, req *http.Request) (auth.Identity, error)
}

type ChatHandlerDeps struct {
	Auth                 authResolver
	Registry             registry.Registry
	Router               router.Router
	ClaimGate            billing.ClaimGate
	Selector             pool.Selector
	CredentialVault      provider.CredentialVault
	Dispatcher           *gateway.UpstreamDispatcher
	Forwarder            *gateway.StreamForwarder
	Settler              billing.Settler
	BillingPolicyVersion string
	RequestClass         string

	// EndpointFamily 标记 billing 字段; 空字符串退化为 "chat"。
	// /v1/chat/completions: "chat"
	// /v1/messages:         "messages"
	EndpointFamily string
}

// effectiveEndpointFamily 返回 d.EndpointFamily 若非空，否则 "chat"
// （向后兼容: 既有 caller 不必填）。
func (d ChatHandlerDeps) effectiveEndpointFamily() string {
	if d.EndpointFamily == "" {
		return "chat"
	}
	return d.EndpointFamily
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatMessage struct {
	Role string `json:"role"`
	// Content 是 raw JSON: OpenAI Chat 用 string, Anthropic Messages API
	// 允许 string 或 [{"type":"text","text":"..."}] content block 数组。
	// 用 RawMessage 防止 string 类型时 array 被静默丢失（sonnet F1 BLOCKING 修复）。
	Content json.RawMessage `json:"content"`
}

func NewChatCompletionsHandler(d ChatHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// 启动期依赖缺失必须 fail closed，避免请求路径 panic。
		if d.Registry == nil || d.Router == nil || d.Auth == nil ||
			d.Selector == nil || d.ClaimGate == nil || d.Settler == nil ||
			d.CredentialVault == nil || d.Dispatcher == nil || d.Forwarder == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"chat handler dependency unset")
			return
		}

		ident, err := d.Auth.Resolve(ctx, r)
		if errors.Is(err, auth.ErrAuthMisconfigured) {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth tables unavailable")
			return
		}
		if errors.Is(err, auth.ErrAuthBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
			return
		}

		// 保留客户端原始 body，后续 dispatcher 直接交给 provider adapter。
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "body_read_error", err.Error())
			return
		}

		var keys map[string]json.RawMessage
		if err := json.Unmarshal(body, &keys); err == nil {
			if _, found := keys["pool_group_id"]; found {
				writeJSONError(w, http.StatusBadRequest, "body_field_disallowed",
					"pool_group_id field removed in N+5b; the gateway resolves the pool from the model alias")
				return
			}
		}

		var req chatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if !req.Stream {
			writeJSONError(w, http.StatusBadRequest, "non_streaming_unsupported",
				"non-streaming responses are Phase E scope; set stream=true")
			return
		}
		if req.Model == "" {
			writeJSONError(w, http.StatusBadRequest, "missing_model", "model field required")
			return
		}

		resolved, err := d.Registry.ResolveModel(ctx, req.Model, ident.TenantID)
		if errors.Is(err, registry.ErrRegistryBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "registry_backend_error",
				"registry backend transient failure")
			return
		}
		if errors.Is(err, registry.ErrUnknownModel) ||
			errors.Is(err, registry.ErrModelDisabled) ||
			errors.Is(err, registry.ErrTenantNoAccess) {
			writeJSONError(w, http.StatusNotFound, "model_not_available", "model not available")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "registry_unknown_error", err.Error())
			return
		}

		plan, err := d.Router.Plan(ctx, router.PlanInput{
			Context: router.RequestContext{
				TenantID:  ident.TenantID,
				UserID:    ident.UserID,
				APIKeyID:  ident.APIKeyID,
				RequestID: middleware.GetReqID(ctx),
			},
			Model: router.ResolvedModel{
				PublicAlias:     resolved.PublicAlias,
				InternalModelID: resolved.CanonicalModelID,
				ProviderModelID: resolved.ProviderModelID,
				ContextWindow:   resolved.ContextWindow,
				Capabilities:    resolved.Capabilities,
				PricingClass:    resolved.PricingClass,
				ProtocolFamily:  resolved.ProtocolFamily,
				PoolCandidates:  resolved.PoolCandidates,
				SnapshotVersion: resolved.SnapshotVersion,
			},
			Features: router.RequestFeatures{Stream: req.Stream},
		})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "router_plan_error", err.Error())
			return
		}
		if len(plan.Attempts) == 0 {
			writeJSONError(w, http.StatusInternalServerError, "router_plan_error",
				"router returned no attempts")
			return
		}
		attempt := plan.Attempts[0]

		idempotencyHeader := r.Header.Get("Idempotency-Key")
		logicalRequestID := idempotencyHeader
		if logicalRequestID == "" {
			logicalRequestID = uuid.NewString()
		}
		payloadHash := normalizedPayloadHash(req.Model, req.Messages)

		reserveRes, err := d.ClaimGate.Reserve(ctx, billing.ReserveRequest{
			TenantID:                   ident.TenantID,
			APIKeyID:                   ident.APIKeyID,
			UserID:                     ident.UserID,
			LogicalRequestID:           logicalRequestID,
			EndpointFamily:             d.effectiveEndpointFamily(),
			NormalizedPayloadHash:      payloadHash,
			RequestedModel:             req.Model,
			PoolingGroupID:             attempt.PoolGroupID,
			BillingPolicyVersion:       d.BillingPolicyVersion,
			RequestClass:               d.RequestClass,
			PredictedCost:              decimal.NewFromFloat(0.01),
			IdempotencyKeyClientHeader: idempotencyHeader,
		})
		if errors.Is(err, billing.ErrFingerprintConflict) || (reserveRes != nil && reserveRes.FingerprintConflict) {
			writeJSONError(w, http.StatusConflict, "idempotency_conflict",
				"same logical_request_id with different normalized payload")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "reserve_error", err.Error())
			return
		}
		if reserveRes.IdempotencyHit {
			writeJSONError(w, http.StatusConflict, "replay_without_cache",
				"idempotent request hit but replay cache is Phase E scope")
			return
		}

		// Track B: 计算 prompt-prefix hash 作 sticky 路由 key——同 (system,
		// tools) prefix 的请求路由到同一 provider account, 最大化 vendor 端
		// prompt cache 命中率。messages 不参与 hash, 让 multi-turn 对话稳定。
		// 空 hash (短 prompt 无 cacheable prefix) → 不参与 sticky, 走 round-robin。
		promptHash := cache_routing.ComputePromptHash(body)

		selRes, err := d.Selector.Select(ctx, pool.SelectionRequest{
			TenantID:        ident.TenantID,
			UserID:          ident.UserID,
			APIKeyID:        ident.APIKeyID,
			PoolGroupID:     attempt.PoolGroupID,
			RequestedModel:  req.Model,
			EndpointFamily:  d.effectiveEndpointFamily(),
			ClaimID:         reserveRes.ClaimID,
			AttemptSeq:      1,
			CapabilityFlags: attempt.RequiredCapabilities,
			SessionHash:     promptHash,
		})
		if errors.Is(err, pool.ErrNoEligibleAccount) || errors.Is(err, pool.ErrNoSlotAvailable) {
			if abortErr := d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "pool_no_capacity"); abortErr != nil {
				w.Header().Set("X-Huakai-Abort-Failed", abortErr.Error())
			}
			w.Header().Set("Retry-After", "5")
			writeJSONError(w, http.StatusServiceUnavailable, "no_capacity", err.Error())
			return
		}
		if err != nil {
			_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "pool_select_error")
			writeJSONError(w, http.StatusInternalServerError, "pool_select_error", err.Error())
			return
		}
		if selRes == nil || selRes.AccountID == 0 {
			_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "pool_select_no_account")
			writeJSONError(w, http.StatusServiceUnavailable, "no_capacity", "pool returned no account")
			return
		}
		acquiredAccountID := selRes.AccountID
		acquisitionToken := selRes.AcquisitionToken

		upstreamModelID := resolved.ProviderModelID
		if upstreamModelID == "" {
			upstreamModelID = req.Model
		}

		cred, accInfo, err := d.CredentialVault.Resolve(ctx, acquiredAccountID)
		if err != nil {
			_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "credential_resolve_error")
			status := http.StatusInternalServerError
			if errors.Is(err, provider.ErrAccountNotFound) {
				status = http.StatusServiceUnavailable
			}
			writeJSONError(w, status, "credential_resolve_error", "upstream credential unavailable")
			return
		}
		if accInfo.AccountID == 0 {
			accInfo.AccountID = acquiredAccountID
		}

		dispatchRes, err := d.Dispatcher.Dispatch(ctx, gateway.DispatchInput{
			ProtocolFamily:  resolved.ProtocolFamily,
			UpstreamModelID: upstreamModelID,
			InboundBody:     body,
			Account:         accInfo,
			Credential:      cred,
		})
		if err != nil {
			_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "upstream_dispatch_error")
			classification, _ := gateway.Classify(0, nil, []byte(err.Error()), accInfo.Platform)
			writeNormalizedUpstreamError(w, http.StatusBadGateway, "upstream_dispatch_error", classification)
			return
		}
		if dispatchRes == nil || dispatchRes.UpstreamReader == nil {
			_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "upstream_empty_response")
			writeJSONError(w, http.StatusBadGateway, "upstream_empty_response", "upstream returned no response body")
			return
		}
		defer closeDispatchResult(dispatchRes)

		if dispatchRes.StatusCode < 200 || dispatchRes.StatusCode >= 300 {
			errBody, readErr := io.ReadAll(io.LimitReader(dispatchRes.UpstreamReader, 1<<20))
			if readErr != nil {
				errBody = []byte(readErr.Error())
			}
			classification, classifyErr := gateway.Classify(dispatchRes.StatusCode, dispatchRes.Headers, errBody, accInfo.Platform)
			abortReason := "upstream_error"
			if classifyErr == nil && classification.Class != "" {
				abortReason = "upstream_" + string(classification.Class)
			}
			_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, abortReason)
			writeNormalizedUpstreamError(w, clientStatusForUpstreamError(dispatchRes.StatusCode, classification.Class), "upstream_error", classification)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		draft, fwdErr := d.Forwarder.Forward(ctx, dispatchRes.UpstreamReader, w, gateway.ForwardRequest{
			TenantID:         ident.TenantID,
			AccountID:        acquiredAccountID,
			AcquisitionToken: acquisitionToken,
			Model:            upstreamModelID,
			ProtocolFamily:   resolved.ProtocolFamily,
		})
		if fwdErr != nil {
			_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "forwarder_error")
			w.Header().Set("X-Huakai-Forward-Error", fwdErr.Error())
			return
		}

		actualCost := decimal.NewFromFloat(0.01)
		settleReq := billing.SettleRequest{
			ClaimID:           reserveRes.ClaimID,
			AccountID:         acquiredAccountID,
			AcquisitionToken:  acquisitionToken,
			TenantID:          ident.TenantID,
			APIKeyID:          ident.APIKeyID,
			UserID:            ident.UserID,
			ProviderAccountID: acquiredAccountID,
			AttemptSeq:        1,
			RequestedModel:    req.Model,
			UpstreamModel:     upstreamModelID,
			Stream:            true,
			ActualCost:        actualCost,
			Fingerprint:       payloadHash,
			Draft:             draft,
			SnapshotVersion:   plan.SnapshotVersion,
		}
		if _, err := d.Settler.Settle(ctx, settleReq); err != nil {
			w.Header().Set("X-Huakai-Settle-Error", err.Error())
			return
		}
	}
}

func normalizedPayloadHash(model string, messages []chatMessage) string {
	type canonical struct {
		Model    string        `json:"model"`
		Messages []chatMessage `json:"messages"`
	}
	raw, _ := json.Marshal(canonical{Model: model, Messages: messages})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":%q}}`, code, message)
}

func closeDispatchResult(res *gateway.DispatchResult) {
	if res != nil && res.Close != nil {
		_ = res.Close()
	}
}

func writeNormalizedUpstreamError(w http.ResponseWriter, status int, fallbackCode string, c gateway.Classification) {
	code := fallbackCode
	if c.Class != "" {
		code = "upstream_" + string(c.Class)
	}
	if c.RetryAfterMs > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", (c.RetryAfterMs+999)/1000))
	}
	writeJSONError(w, status, code, "upstream request failed")
}

// NewMessagesHandler 是 /v1/messages 端点 handler——形态与 chat completions
// 等价（同 deps，相同 routing/billing pipeline），只是 EndpointFamily 标
// "messages" 让 billing 数据按 endpoint 区分。
//
// 当前 chat / messages 共用同一 generic handler——下游 forwarder + provider
// adapter 看 ProtocolFamily（来自 model alias 配置）决定真实 wire format。
//
// Anthropic CLI / Claude Code 可直接对此 handler 发 Anthropic Messages API
// body, registry 把 model 别名解析到 bedrock_invoke + AutoTranslateAnthropicAPIBody=true
// 的 PassthroughAdapter 即可路由到 AWS Bedrock。
func NewMessagesHandler(d ChatHandlerDeps) http.HandlerFunc {
	if d.EndpointFamily == "" {
		d.EndpointFamily = "messages"
	}
	return NewChatCompletionsHandler(d)
}

func clientStatusForUpstreamError(upstreamStatus int, class gateway.ErrorClass) int {
	switch class {
	case gateway.ErrorClassRequestTooLarge:
		return http.StatusBadRequest
	case gateway.ErrorClassRateLimited, gateway.ErrorClassOverloaded, gateway.ErrorClassUpstreamTimeout:
		return http.StatusServiceUnavailable
	}
	if upstreamStatus == http.StatusTooManyRequests {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}
