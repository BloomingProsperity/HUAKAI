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
	"os"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/cache_routing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
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
	CanonicalDispatcher  HCSFDispatcher
	Forwarder            *gateway.StreamForwarder
	Settler              billing.Settler
	BillingPolicyVersion string
	RequestClass         string

	// EndpointFamily 标记 billing 字段; 空字符串退化为 "chat"。
	// /v1/chat/completions: "chat"
	// /v1/messages:         "messages"
	EndpointFamily string
}

// HCSFDispatcher 是 non-streaming HCSF 主链路；默认关闭，由 env 开关启用。
type HCSFDispatcher interface {
	DispatchHCSF(ctx context.Context, envelope *proto.HCSF) (*proto.HCSF, error)
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
		var clientProtocol proto.ClientProtocol
		var clientAdapter proto.ClientAdapter
		if inferred, ok := proto.ClientProtocolByIngressPath(r.URL.Path); ok {
			clientProtocol = inferred
		} else if !req.Stream {
			writeJSONError(w, http.StatusNotFound, "unknown_route",
				fmt.Sprintf("no client protocol registered for ingress path %q", r.URL.Path))
			return
		}
		if !req.Stream {
			var adapterOK bool
			clientAdapter, adapterOK = proto.DefaultClientAdapterRegistry().Lookup(clientProtocol)
			if !adapterOK {
				writeJSONError(w, http.StatusServiceUnavailable, "adapter_unregistered",
					fmt.Sprintf("client adapter not registered for protocol %q", clientProtocol))
				return
			}
		}
		if req.Model == "" {
			writeJSONError(w, http.StatusBadRequest, "missing_model", "model field required")
			return
		}
		requestID := middleware.GetReqID(ctx)
		if requestID == "" {
			requestID = uuid.NewString()
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
				RequestID: requestID,
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
		routeID := plan.SnapshotVersion
		if attempt.Reason != "" {
			routeID = fmt.Sprintf("%s:%s", routeID, attempt.Reason)
		}

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
			// D2: vendor 从 ResolvedModel.ProtocolFamily 派生, 给 dispatcher
			// 写 per-vendor 切片 metric (4 vendor: anthropic/openai/gemini/codex)
			Vendor: pool.VendorFromProtocolFamily(resolved.ProtocolFamily),
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
		forwardReq := gateway.ForwardRequest{
			TenantID:             ident.TenantID,
			AccountID:            acquiredAccountID,
			AcquisitionToken:     acquisitionToken,
			RequestID:            requestID,
			RouteID:              routeID,
			PoolID:               fmt.Sprintf("%d", attempt.PoolGroupID),
			IngressPath:          r.URL.Path,
			ProtocolFamily:       resolved.ProtocolFamily,
			ClientProtocol:       string(clientProtocol),
			Model:                upstreamModelID,
			RequestedModel:       req.Model,
			Provider:             accInfo.Platform,
			RoutingReasonPayload: selRes.RoutingReasonJSON,
			SessionHash:          promptHash,
		}

		if !req.Stream {
			seed := requestMetaSeed(r, ident, clientProtocol, resolved.ProtocolFamily, routeID, requestID, acquiredAccountID, acquisitionToken)
			seedCtx := proto.ContextWithRequestMetaSeed(ctx, seed)
			var bufferedEnv *proto.HCSF

			if hcsfDispatchEnabled() {
				canonicalReq, _, err := clientAdapter.RequestToCanonical(seedCtx, body)
				if err != nil {
					_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "invalid_request_body")
					writeJSONError(w, http.StatusBadRequest, "invalid_request_body", err.Error())
					return
				}
				if canonicalReq == nil {
					_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "invalid_request_body")
					writeJSONError(w, http.StatusBadRequest, "invalid_request_body", "client adapter returned nil canonical envelope")
					return
				}
				enrichCanonicalRequestMeta(canonicalReq, upstreamModelID, accInfo.Platform, idempotencyHeader, promptHash)
				setAccountingModelRequested(canonicalReq, req.Model)
				setAccountingModelRouteDecided(canonicalReq, forwardReq.Model)
				gateway.ApplyForwardRequestHopChain(canonicalReq, forwardReq)

				dispatcher := hcsfDispatcher(d)
				if dispatcher == nil {
					_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "non_streaming_not_yet_wired")
					writeJSONError(w, http.StatusServiceUnavailable, "non_streaming_not_yet_wired",
						fmt.Sprintf("dispatcher lacks HCSF dispatch support for client_protocol=%q protocol_family=%q", clientProtocol, resolved.ProtocolFamily))
					return
				}
				dispatchCtx := gateway.ContextWithHCSFDispatchInput(seedCtx, gateway.HCSFDispatchInput{
					ProtocolFamily:  resolved.ProtocolFamily,
					UpstreamModelID: upstreamModelID,
					Account:         accInfo,
					Credential:      cred,
					RawBody:         body,
				})
				bufferedEnv, err = dispatcher.DispatchHCSF(dispatchCtx, canonicalReq)
				if err != nil {
					_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "upstream_dispatch_error")
					writeJSONError(w, http.StatusBadGateway, "upstream_dispatch_error", err.Error())
					return
				}
			} else {
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
				raw, readErr := io.ReadAll(io.LimitReader(dispatchRes.UpstreamReader, 1<<20))
				if readErr != nil {
					_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "upstream_read_error")
					writeJSONError(w, http.StatusBadGateway, "upstream_read_error", readErr.Error())
					return
				}
				if dispatchRes.StatusCode < 200 || dispatchRes.StatusCode >= 300 {
					classification, classifyErr := gateway.Classify(dispatchRes.StatusCode, dispatchRes.Headers, raw, accInfo.Platform)
					abortReason := "upstream_error"
					if classifyErr == nil && classification.Class != "" {
						abortReason = "upstream_" + string(classification.Class)
					}
					_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, abortReason)
					writeNormalizedUpstreamError(w, clientStatusForUpstreamError(dispatchRes.StatusCode, classification.Class), "upstream_error", classification)
					return
				}
				upstreamAdapter, err := protocolAdapterForBuffered(d.Forwarder, resolved.ProtocolFamily)
				if err != nil {
					_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "upstream_adapter_error")
					writeJSONError(w, http.StatusBadGateway, "upstream_adapter_error", err.Error())
					return
				}
				bufferedEnv, _, err = upstreamAdapter.ProviderResponseToCanonical(seedCtx, raw)
				if err != nil {
					_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "canonical_response_error")
					writeJSONError(w, http.StatusBadGateway, "canonical_response_error", err.Error())
					return
				}
				if bufferedEnv != nil {
					_ = seed.ApplyToRequestMeta(&bufferedEnv.RequestMeta)
					enrichCanonicalRequestMeta(bufferedEnv, upstreamModelID, accInfo.Platform, idempotencyHeader, promptHash)
				}
			}
			if bufferedEnv == nil || bufferedEnv.BufferedResponse == nil {
				_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "upstream_empty_response")
				writeJSONError(w, http.StatusBadGateway, "upstream_empty_response", "dispatcher returned no buffered HCSF response")
				return
			}
			setAccountingModelRequested(bufferedEnv, req.Model)
			setAccountingModelRouteDecided(bufferedEnv, forwardReq.Model)
			gateway.ApplyForwardRequestHopChain(bufferedEnv, forwardReq)

			clientBody, _, err := clientAdapter.CanonicalToClientResponse(seedCtx, bufferedEnv)
			if err != nil {
				_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "canonical_response_error")
				writeJSONError(w, http.StatusBadGateway, "canonical_response_error", err.Error())
				return
			}

			actualCost := decimal.NewFromFloat(0.01)
			if _, err := d.Settler.Settle(ctx, billing.SettleRequest{
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
				Stream:            false,
				ActualCost:        actualCost,
				Fingerprint:       payloadHash,
				Draft:             nonStreamingUsageDraft(bufferedEnv, actualCost, selRes.RoutingReasonJSON),
				SnapshotVersion:   plan.SnapshotVersion,
			}); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "settle_error", err.Error())
				return
			}

			w.Header().Set("Content-Type", "application/json")
			setHUAKAIModelHeaders(w.Header(), req.Model, bufferedEnv)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(clientBody)
			return
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
		draft, fwdErr := d.Forwarder.Forward(ctx, dispatchRes.UpstreamReader, w, forwardReq)
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

func requestMetaSeed(r *http.Request, ident auth.Identity, clientProtocol proto.ClientProtocol, protocolFamily, routeID, requestID string, accountID int64, acquisitionToken uuid.UUID) proto.RequestMetaSeed {
	token := ""
	if acquisitionToken != uuid.Nil {
		token = acquisitionToken.String()
	}
	return proto.RequestMetaSeed{
		RequestID:        requestID,
		ClientProtocol:   clientProtocol,
		ProtocolFamily:   protocolFamily,
		IngressPath:      r.URL.Path,
		TenantID:         ident.TenantID,
		RouteID:          routeID,
		AccountID:        accountID,
		AcquisitionToken: token,
		EvidenceLabel:    proto.EvidenceMock,
	}
}

func enrichCanonicalRequestMeta(env *proto.HCSF, upstreamModelID, providerName, idempotencyKey, sessionHash string) {
	if env == nil {
		return
	}
	env.RequestMeta.UpstreamModel = upstreamModelID
	env.RequestMeta.Provider = providerName
	env.RequestMeta.IdempotencyKey = idempotencyKey
	env.RequestMeta.SessionHash = sessionHash
	if env.RequestMeta.EvidenceLabel == "" {
		env.RequestMeta.EvidenceLabel = proto.EvidenceMock
	}
	if env.Accounting.EvidenceLabel == "" {
		env.Accounting.EvidenceLabel = proto.EvidenceMock
	}
}

func hcsfDispatchEnabled() bool {
	return os.Getenv("HUAKAI_DISPATCH_HCSF") == "1"
}

func hcsfDispatcher(d ChatHandlerDeps) HCSFDispatcher {
	if d.CanonicalDispatcher != nil {
		return d.CanonicalDispatcher
	}
	if d.Dispatcher == nil {
		return nil
	}
	return d.Dispatcher
}

func protocolAdapterForBuffered(f *gateway.StreamForwarder, protocolFamily string) (proto.UpstreamAdapter, error) {
	var adapters gateway.ProtocolAdapterRegistry
	if f != nil {
		adapters = f.ProtocolAdapters
	}
	if adapters == nil {
		adapters = gateway.BuildDefaultProtocolAdapterRegistry()
	}
	return adapters.For(protocolFamily)
}

func nonStreamingUsageDraft(env *proto.HCSF, actualCost decimal.Decimal, routingReason []byte) gateway.UsageRecordDraft {
	usage := proto.CanonicalUsage{}
	if env != nil {
		usage = env.Accounting.Usage
		if env.BufferedResponse != nil {
			usage = env.BufferedResponse.Usage
		}
	}
	confidence := 1.0
	return gateway.UsageRecordDraft{
		TokensInput:           usage.InputTokens,
		TokensOutput:          usage.OutputTokens,
		CacheCreationTokens:   usage.CacheCreationInputTokens,
		CacheReadTokens:       usage.CacheReadInputTokens,
		ActualCost:            actualCost,
		RoutingReason:         routingReason,
		EndClass:              gateway.StreamEndGraceful,
		UsageSource:           gateway.UsageSourceReported,
		ConfidenceScore:       &confidence,
		DrainOutcome:          gateway.DrainNotDrained,
		PendingReconciliation: false,
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
