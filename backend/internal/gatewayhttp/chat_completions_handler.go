package gatewayhttp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/cache_routing"
	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
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
	ResponseCache        l2cache.Store
	Settler              billing.Settler
	CompletionBus        *eventbus.Bus
	AuditLedger          auditledger.Ledger
	Signer               *sign.Signer
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
		requestStartedAt := time.Now()

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
		cacheVendor := pool.VendorFromProtocolFamily(resolved.ProtocolFamily)
		cacheKey := ""
		if d.ResponseCache != nil && !req.Stream {
			var canonicalErr error
			cacheKey, _, canonicalErr = l2cache.BuildKey(l2cache.KeyInput{
				TenantID: ident.TenantID,
				Vendor:   cacheVendor,
				Model:    upstreamModelID,
				Body:     body,
			})
			if canonicalErr != nil {
				_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "cache_key_error")
				writeJSONError(w, http.StatusBadRequest, "cache_key_error", canonicalErr.Error())
				return
			}
			if cached, ok := d.ResponseCache.Get(ctx, cacheKey); ok {
				cachemetrics.ObserveL2Hit(cacheVendor, upstreamModelID)
				if serveL2CacheHit(ctx, w, r, d, l2CacheHitInput{
					Entry:             cached,
					Ident:             ident,
					ClientProtocol:    clientProtocol,
					ProtocolFamily:    resolved.ProtocolFamily,
					RouteID:           routeID,
					RequestID:         requestID,
					AccountID:         acquiredAccountID,
					AcquisitionToken:  acquisitionToken,
					PoolID:            fmt.Sprintf("%d", attempt.PoolGroupID),
					UpstreamModelID:   upstreamModelID,
					RequestedModel:    req.Model,
					Provider:          cacheVendor,
					IdempotencyHeader: idempotencyHeader,
					PromptHash:        promptHash,
					RequestStartedAt:  requestStartedAt,
					ReserveResult:     reserveRes,
					SelectionResult:   selRes,
					PlanSnapshot:      plan.SnapshotVersion,
					PayloadHash:       payloadHash,
				}) {
					return
				}
				d.ResponseCache.Delete(ctx, cacheKey)
				syncL2SizeMetrics(d.ResponseCache)
			}
			cachemetrics.ObserveL2Miss(cacheVendor, upstreamModelID)
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
				canonicalReq.RequestMeta.EndpointFamily = resolved.ProtocolFamily
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
			fillAccountingModelUpstreamReported(bufferedEnv)
			bufferedEnv.Accounting.HopChain = gateway.BuildHopChain(forwardReq, "", requestStartedAt, time.Now())
			ledgerEntry, err := submitAuditLedgerEntry(ctx, d, bufferedEnv, ident.TenantID, requestID)
			if err != nil {
				_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "audit_ledger_error")
				writeJSONError(w, http.StatusInternalServerError, "audit_ledger_error", err.Error())
				return
			}

			clientBody, _, err := clientAdapter.CanonicalToClientResponse(seedCtx, bufferedEnv)
			if err != nil {
				_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "canonical_response_error")
				writeJSONError(w, http.StatusBadGateway, "canonical_response_error", err.Error())
				return
			}
			cacheEnvelope, cacheEnvelopeOK := encodeL2CacheEnvelope(bufferedEnv)

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
				Provider:          cacheVendor,
				Stream:            false,
				ActualCost:        actualCost,
				Fingerprint:       payloadHash,
				Draft:             nonStreamingUsageDraft(bufferedEnv, actualCost, selRes.RoutingReasonJSON),
				SnapshotVersion:   plan.SnapshotVersion,
			}
			if _, err := settleCompletion(ctx, d, eventbus.RequestCompletionEvent{
				ID:                        requestID,
				TenantID:                  ident.TenantID,
				ClaimID:                   reserveRes.ClaimID,
				AccountID:                 acquiredAccountID,
				RequestID:                 requestID,
				EndpointFamily:            d.effectiveEndpointFamily(),
				RequestedModel:            req.Model,
				UpstreamModel:             upstreamModelID,
				PayloadHash:               payloadHash,
				RawBodyHash:               bodyHash(body),
				RedactedBodyRef:           redactedBodyRef(body),
				AuditLedgerID:             ledgerID(ledgerEntry),
				AuditSignatureFingerprint: ledgerFingerprint(ledgerEntry),
				SettleRequest:             settleReq,
			}); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "settle_error", err.Error())
				return
			}
			if d.ResponseCache != nil && cacheKey != "" && cacheEnvelopeOK {
				d.ResponseCache.Set(ctx, l2cache.Entry{
					Key:      cacheKey,
					TenantID: ident.TenantID,
					Vendor:   cacheVendor,
					Model:    upstreamModelID,
					Status:   http.StatusOK,
					Body:     clientBody,
					Envelope: cacheEnvelope,
				})
				syncL2SizeMetrics(d.ResponseCache)
			}

			w.Header().Set("Content-Type", "application/json")
			if d.ResponseCache != nil && cacheKey != "" {
				w.Header().Set("X-HUAKAI-Cache-L2", "miss")
			}
			WriteHuakaiHeaders(w.Header(), req.Model, bufferedEnv, ledgerEntry)
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
		if d.ResponseCache != nil {
			w.Header().Set("X-HUAKAI-Cache-L2", "skip")
		}
		declareStreamBillingTrailers(w.Header())
		writeStreamBillingHeaders(w.Header(), billing.Attempt{State: billing.StreamStateInFlight})
		streamForwarder := *d.Forwarder
		if streamForwarder.AuditLedger == nil {
			streamForwarder.AuditLedger = d.AuditLedger
		}
		if streamForwarder.Signer == nil {
			streamForwarder.Signer = d.Signer
		}
		streamForwarder.LedgerCallback = func(entryID, sigFingerprint string) {
			WriteHuakaiLedgerHeaders(w.Header(), requestID, entryID, sigFingerprint)
		}
		draft, fwdErr := streamForwarder.Forward(ctx, dispatchRes.UpstreamReader, w, forwardReq)
		streamAttempt := billing.AttemptFromGatewayDraft(true, draft)
		writeStreamBillingHeaders(w.Header(), streamAttempt)
		if fwdErr != nil {
			w.Header().Set("X-Huakai-Forward-Error", fwdErr.Error())
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
			Provider:          cacheVendor,
			Stream:            true,
			ActualCost:        actualCost,
			Fingerprint:       payloadHash,
			Draft:             draft,
			StreamAttempt:     &streamAttempt,
			SnapshotVersion:   plan.SnapshotVersion,
		}
		if _, err := settleCompletion(ctx, d, eventbus.RequestCompletionEvent{
			ID:              requestID,
			TenantID:        ident.TenantID,
			ClaimID:         reserveRes.ClaimID,
			AccountID:       acquiredAccountID,
			RequestID:       requestID,
			EndpointFamily:  d.effectiveEndpointFamily(),
			RequestedModel:  req.Model,
			UpstreamModel:   upstreamModelID,
			PayloadHash:     payloadHash,
			RawBodyHash:     bodyHash(body),
			RedactedBodyRef: redactedBodyRef(body),
			SettleRequest:   settleReq,
		}); err != nil {
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

func settleCompletion(ctx context.Context, d ChatHandlerDeps, event eventbus.RequestCompletionEvent) (*billing.SettleResult, error) {
	if d.CompletionBus == nil {
		return d.Settler.Settle(ctx, event.SettleRequest)
	}
	if err := d.CompletionBus.Emit(ctx, event); err != nil {
		if shouldDirectSettleFallback(err) {
			return d.Settler.Settle(ctx, event.SettleRequest)
		}
		return nil, err
	}
	return &billing.SettleResult{}, nil
}

func shouldDirectSettleFallback(err error) bool {
	return errors.Is(err, eventbus.ErrNoHandlers) ||
		errors.Is(err, eventbus.ErrBusClosed) ||
		errors.Is(err, eventbus.ErrQueueFull)
}

func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func redactedBodyRef(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	return "sha256:" + bodyHash(body)
}

func ledgerID(entry *auditledger.LedgerEntry) string {
	if entry == nil {
		return ""
	}
	return entry.LedgerID
}

func ledgerFingerprint(entry *auditledger.LedgerEntry) string {
	if entry == nil {
		return ""
	}
	return entry.PubkeyFingerprint
}

type l2CacheHitInput struct {
	Entry             l2cache.Entry
	Ident             auth.Identity
	ClientProtocol    proto.ClientProtocol
	ProtocolFamily    string
	RouteID           string
	RequestID         string
	AccountID         int64
	AcquisitionToken  uuid.UUID
	PoolID            string
	UpstreamModelID   string
	RequestedModel    string
	Provider          string
	IdempotencyHeader string
	PromptHash        string
	RequestStartedAt  time.Time
	ReserveResult     *billing.ReserveResult
	SelectionResult   *pool.SelectionResult
	PlanSnapshot      string
	PayloadHash       string
}

func serveL2CacheHit(ctx context.Context, w http.ResponseWriter, r *http.Request, d ChatHandlerDeps, in l2CacheHitInput) bool {
	cachedEnv, err := decodeL2CacheEnvelope(in.Entry.Envelope)
	if err != nil || cachedEnv == nil || cachedEnv.BufferedResponse == nil {
		return false
	}
	seed := requestMetaSeed(r, in.Ident, in.ClientProtocol, in.ProtocolFamily, in.RouteID, in.RequestID, in.AccountID, in.AcquisitionToken)
	_ = seed.ApplyToRequestMeta(&cachedEnv.RequestMeta)
	enrichCanonicalRequestMeta(cachedEnv, in.UpstreamModelID, in.Provider, in.IdempotencyHeader, in.PromptHash)
	setAccountingModelRequested(cachedEnv, in.RequestedModel)
	setAccountingModelRouteDecided(cachedEnv, in.UpstreamModelID)
	fillAccountingModelUpstreamReported(cachedEnv)
	var routingReason []byte
	if in.SelectionResult != nil {
		routingReason = in.SelectionResult.RoutingReasonJSON
	}
	forwardReq := gateway.ForwardRequest{
		TenantID:             in.Ident.TenantID,
		AccountID:            in.AccountID,
		AcquisitionToken:     in.AcquisitionToken,
		RequestID:            in.RequestID,
		RouteID:              in.RouteID,
		PoolID:               in.PoolID,
		IngressPath:          r.URL.Path,
		ProtocolFamily:       in.ProtocolFamily,
		ClientProtocol:       string(in.ClientProtocol),
		Model:                in.UpstreamModelID,
		RequestedModel:       in.RequestedModel,
		Provider:             in.Provider,
		RoutingReasonPayload: routingReason,
		SessionHash:          in.PromptHash,
	}
	cachedEnv.Accounting.HopChain = gateway.BuildHopChain(forwardReq, "", in.RequestStartedAt, time.Now())
	appendTrustChainWarning(cachedEnv, "response_cache_l2_hit", "served from HUAKAI L2 response cache")
	ledgerEntry, err := submitAuditLedgerEntry(ctx, d, cachedEnv, in.Ident.TenantID, in.RequestID)
	if err != nil {
		_ = d.Settler.Abort(ctx, in.Ident.TenantID, in.ReserveResult.ClaimID, "audit_ledger_error")
		writeJSONError(w, http.StatusInternalServerError, "audit_ledger_error", err.Error())
		return true
	}
	actualCost := decimal.Zero
	settleReq := billing.SettleRequest{
		ClaimID:           in.ReserveResult.ClaimID,
		AccountID:         in.AccountID,
		AcquisitionToken:  in.AcquisitionToken,
		TenantID:          in.Ident.TenantID,
		APIKeyID:          in.Ident.APIKeyID,
		UserID:            in.Ident.UserID,
		ProviderAccountID: in.AccountID,
		AttemptSeq:        1,
		RequestedModel:    in.RequestedModel,
		UpstreamModel:     in.UpstreamModelID,
		Provider:          in.Provider,
		Stream:            false,
		ActualCost:        actualCost,
		Fingerprint:       in.PayloadHash,
		Draft:             nonStreamingUsageDraft(cachedEnv, actualCost, routingReasonWithCacheHit(routingReason, true, in.Entry.Key)),
		SnapshotVersion:   in.PlanSnapshot,
	}
	if _, err := settleCompletion(ctx, d, eventbus.RequestCompletionEvent{
		ID:                        in.RequestID,
		TenantID:                  in.Ident.TenantID,
		ClaimID:                   in.ReserveResult.ClaimID,
		AccountID:                 in.AccountID,
		RequestID:                 in.RequestID,
		EndpointFamily:            d.effectiveEndpointFamily(),
		RequestedModel:            in.RequestedModel,
		UpstreamModel:             in.UpstreamModelID,
		PayloadHash:               in.PayloadHash,
		AuditLedgerID:             ledgerID(ledgerEntry),
		AuditSignatureFingerprint: ledgerFingerprint(ledgerEntry),
		SettleRequest:             settleReq,
	}); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "settle_error", err.Error())
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-HUAKAI-Cache-L2", "hit")
	WriteHuakaiHeaders(w.Header(), in.RequestedModel, cachedEnv, ledgerEntry)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(in.Entry.Body)
	return true
}

func encodeL2CacheEnvelope(env *proto.HCSF) ([]byte, bool) {
	if env == nil {
		return nil, false
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, false
	}
	var clone proto.HCSF
	if err := json.Unmarshal(raw, &clone); err != nil {
		return nil, false
	}
	clone.Accounting.LedgerID = ""
	clone.Accounting.Signature = ""
	clone.Accounting.PubkeyFingerprint = ""
	raw, err = json.Marshal(&clone)
	return raw, err == nil
}

func decodeL2CacheEnvelope(raw []byte) (*proto.HCSF, error) {
	var env proto.HCSF
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

func routingReasonWithCacheHit(base []byte, hit bool, key string) []byte {
	payload := map[string]any{}
	if len(base) > 0 {
		_ = json.Unmarshal(base, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["cache_hit"] = hit
	if key != "" {
		payload["cache_key"] = key
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return []byte(`{"cache_hit":true}`)
	}
	return raw
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
	return os.Getenv("HUAKAI_DISPATCH_HCSF") != "0"
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

const (
	headerHUAKAIAuditLedgerID       = "X-HUAKAI-Ledger-ID"
	headerHUAKAIAuditVerify         = "X-HUAKAI-Verify"
	headerHUAKAIAuditSigFingerprint = "X-HUAKAI-Sig-Fingerprint"
	headerHUAKAIStreamState         = "X-HUAKAI-Stream-State"
	headerHUAKAIDeliveredTokens     = "X-HUAKAI-Delivered-Tokens"
)

func declareStreamBillingTrailers(h http.Header) {
	if h == nil {
		return
	}
	h.Add("Trailer", headerHUAKAIStreamState)
	h.Add("Trailer", headerHUAKAIDeliveredTokens)
}

func writeStreamBillingHeaders(h http.Header, attempt billing.Attempt) {
	if h == nil {
		return
	}
	attempt = attempt.Normalized()
	h.Set(headerHUAKAIStreamState, attempt.State.String())
	h.Set(headerHUAKAIDeliveredTokens, strconv.FormatInt(attempt.DeliveredTokenCount, 10))
}

func WriteHuakaiHeaders(h http.Header, requested string, env *proto.HCSF, entry *auditledger.LedgerEntry) {
	setHUAKAIModelHeaders(h, requested, env)
	if h == nil || entry == nil {
		return
	}
	WriteHuakaiLedgerHeaders(h, entry.RequestID, entry.LedgerID, entry.PubkeyFingerprint)
}

func WriteHuakaiLedgerHeaders(h http.Header, requestID, ledgerID, sigFingerprint string) {
	if h == nil {
		return
	}
	if ledgerID != "" {
		h.Set(headerHUAKAIAuditLedgerID, ledgerID)
	}
	if sigFingerprint != "" {
		h.Set(headerHUAKAIAuditSigFingerprint, sigFingerprint)
	}
	if requestID != "" {
		query := url.Values{}
		query.Set("request_id", requestID)
		if ledgerID != "" {
			query.Set("ledger-id", ledgerID)
		}
		h.Set(headerHUAKAIAuditVerify, "/v1/audit/verify?"+query.Encode())
	}
}

func submitAuditLedgerEntry(ctx context.Context, d ChatHandlerDeps, env *proto.HCSF, tenantID int64, requestID string) (*auditledger.LedgerEntry, error) {
	if env == nil {
		return nil, nil
	}
	if d.AuditLedger == nil {
		appendTrustChainWarning(env, "audit_ledger_not_configured", "audit ledger dependency unset; trust-chain ledger entry skipped")
		return nil, nil
	}
	if d.Signer == nil {
		appendTrustChainWarning(env, "audit_signer_not_configured", "audit signer dependency unset; trust-chain ledger entry skipped")
		return nil, nil
	}
	if requestID == "" {
		requestID = env.RequestMeta.RequestID
	}
	entry := auditledger.LedgerEntry{
		LedgerID:          uuid.NewString(),
		Timestamp:         time.Now().UTC().Format(time.RFC3339Nano),
		RequestID:         requestID,
		TenantID:          tenantID,
		HopChain:          cloneHopChain(env.Accounting.HopChain),
		ModelChain:        cloneModelChain(env.Accounting.ModelChain),
		PubkeyFingerprint: d.Signer.Fingerprint(),
	}
	hash, err := auditledger.EntryHash(&entry)
	if err != nil {
		return nil, fmt.Errorf("audit ledger entry hash: %w", err)
	}
	entry.Signature = base64.StdEncoding.EncodeToString(d.Signer.Sign(hash[:]))
	appended, err := d.AuditLedger.Append(ctx, entry)
	if err != nil {
		return nil, fmt.Errorf("audit ledger append: %w", err)
	}
	env.Accounting.LedgerID = appended.LedgerID
	env.Accounting.Signature = appended.Signature
	env.Accounting.PubkeyFingerprint = appended.PubkeyFingerprint
	return &appended, nil
}

func appendTrustChainWarning(env *proto.HCSF, code, reason string) {
	if env == nil {
		return
	}
	env.CapabilityGraph.ProtocolLoss = append(env.CapabilityGraph.ProtocolLoss, proto.ProtocolLossEntry{
		Severity: proto.ProtocolLossWarning,
		Code:     code,
		Reason:   reason,
	})
}

func fillAccountingModelUpstreamReported(env *proto.HCSF) {
	if env == nil || env.BufferedResponse == nil || env.BufferedResponse.Model == "" {
		return
	}
	mc := ensureAccountingModelChain(env)
	if mc.UpstreamReported == "" {
		mc.UpstreamReported = env.BufferedResponse.Model
	}
}

func cloneHopChain(in []proto.HopAttestation) []proto.HopAttestation {
	if in == nil {
		return nil
	}
	out := make([]proto.HopAttestation, len(in))
	copy(out, in)
	return out
}

func cloneModelChain(in *proto.ModelChain) *proto.ModelChain {
	if in == nil {
		return nil
	}
	out := *in
	return &out
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
		DeliveredTokenCount:   int64(usage.OutputTokens),
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

// NewResponsesHandler 是 /v1/responses 端点 handler。它复用同一条
// auth/routing/billing/forwarding pipeline，仅把 billing endpoint family 标为
// openai_responses；真实上下游协议仍由 registry 的 ProtocolFamily 决定。
func NewResponsesHandler(d ChatHandlerDeps) http.HandlerFunc {
	if d.EndpointFamily == "" {
		d.EndpointFamily = "openai_responses"
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
