package gatewayhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
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
	RateTables           billing.RateTableSource
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
	ChannelHealth        channelHealthRecorder
	BillingPolicyVersion string
	RequestClass         string

	// EndpointFamily 标记 billing 字段；空字符串退化为 "chat"。
	// /v1/chat/completions: "chat"
	// /v1/messages:         "messages"
	EndpointFamily string
}

type channelHealthRecorder interface {
	ApplySignal(context.Context, channelhealth.Signal) (channelhealth.Record, error)
}

// HCSFDispatcher 是 non-streaming HCSF 主链路；默认开启，可由 env 开关关闭。
type HCSFDispatcher interface {
	DispatchHCSF(ctx context.Context, envelope *proto.HCSF) (*proto.HCSF, error)
}

// effectiveEndpointFamily 返回 d.EndpointFamily 若非空，否则 "chat"。
func (d ChatHandlerDeps) effectiveEndpointFamily() string {
	if d.EndpointFamily == "" {
		return "chat"
	}
	return d.EndpointFamily
}

type chatExecution struct {
	d         ChatHandlerDeps
	r         *http.Request
	ctx       context.Context
	startedAt time.Time

	ident          auth.Identity
	body           []byte
	req            chatRequest
	clientProtocol proto.ClientProtocol
	clientAdapter  proto.ClientAdapter
	requestID      string

	resolved registry.Resolved
	plan     router.RoutePlan
	attempt  router.AttemptPlan
	routeID  string

	idempotencyHeader string
	logicalRequestID  string
	payloadHash       string
	promptHash        string
	reserveRes        *billing.ReserveResult

	selRes            *pool.SelectionResult
	acquiredAccountID int64
	acquisitionToken  uuid.UUID
	upstreamModelID   string
	cacheVendor       string
	cacheKey          string

	cred        provider.Credential
	accInfo     provider.AccountInfo
	forwardReq  gateway.ForwardRequest
	healthKey   channelhealth.ChannelKey
	healthKeyOK bool
}

func NewChatCompletionsHandler(d ChatHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		requestStartedAt := time.Now()

		if !chatHandlerConfigured(d) {
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

		validated, ok := validateChatCompletionsRequest(w, r, ctx)
		if !ok {
			return
		}
		exec := newChatExecution(d, r, ident, validated, requestStartedAt)
		if !exec.prepareRoute(w) {
			return
		}
		// codex chunk9 P1: 先 reserve (ClaimGate.Reserve 内部走
		// uq_claims_idempotency 唯一约束做 idempotency-key + payload fingerprint
		// 校验), 再查 cache。 否则 client reuse 同 idempotency-key 但 payload 变
		// 时, cache 命中绕过 fingerprint conflict 检查。 reserve 不占 pool slot
		// (selectPoolAccount 才占), 所以 cache 命中分支仍能避开 pool acquire。
		if !exec.reserveClaim(w) {
			return
		}
		if !exec.req.Stream {
			// cache 命中时 serveL2CacheHit 内部已在写 200 body 之前 Settler.Abort
			// 收尾 reserve 行 (codex chunk12 P2)。 这里只需 return。
			handled, proceed := exec.serveL2CacheIfAvailable(w)
			if handled || !proceed {
				return
			}
		}
		if !exec.prepareClaimAndAccount(w) {
			return
		}
		if !exec.resolveCredential(w) {
			return
		}
		if !exec.req.Stream {
			// codex review P1 2026-05-19: anthropic_messages adapter
			// ProviderResponseToCanonical 仍是 ErrNotImplemented stub, 走非流式
			// 会先扣上游账号, 解析时 502 — 客户端拿 502 但额度已花。
			// buffered 翻译器实现前 fail-fast 拒 (501 Not Implemented), 让客户端
			// 改 stream:true 或选 OpenAI 协议路径。
			//
			// 注意: 这步走到时已经 prepareClaimAndAccount + resolveCredential 完,
			// 即 ClaimGate.Reserve 已记 claim, pool slot 已 acquire。reject 不 abort
			// 会让 claim 永远停在 reserving + slot 留 acquired, 反复打反复占, 把 pool
			// 容量打空 (codex review P1 2026-05-19 catch)。所以必须先 Settler.Abort
			// 再 writeJSONError 退出。
			if exec.resolved.ProtocolFamily == "anthropic_messages" {
				if exec.reserveRes != nil {
					_ = exec.d.Settler.Abort(exec.ctx, exec.ident.TenantID, exec.reserveRes.ClaimID,
						"buffered_anthropic_not_supported", exec.requestID)
				}
				writeJSONError(w, http.StatusNotImplemented, "buffered_anthropic_not_supported",
					"Anthropic /v1/messages 非流式 (stream:false) 暂未实现; 请设 stream:true 走流式路径")
				return
			}
			exec.handleNonStreamingResponse(w)
			return
		}
		exec.handleStreamingResponse(w)
	}
}

func chatHandlerConfigured(d ChatHandlerDeps) bool {
	return d.Registry != nil && d.Router != nil && d.Auth != nil &&
		d.Selector != nil && d.ClaimGate != nil && d.Settler != nil &&
		d.CredentialVault != nil && d.Dispatcher != nil && d.Forwarder != nil
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

func (ex *chatExecution) dispatchRawBuffered(w http.ResponseWriter, seed proto.RequestMetaSeed, seedCtx context.Context, startedAt time.Time) (*proto.HCSF, bool) {
	dispatchRes, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     ex.body,
		Account:         ex.accInfo,
		Credential:      ex.cred,
	})
	if err != nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_dispatch_error", ex.requestID)
		classification, _ := gateway.Classify(0, nil, []byte(err.Error()), ex.accInfo.Platform)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromDispatchError(err, classification), 0, time.Since(startedAt), ex.requestID, nil)
		}
		writeNormalizedUpstreamError(w, http.StatusBadGateway, "upstream_dispatch_error", classification)
		return nil, false
	}
	if dispatchRes == nil || dispatchRes.UpstreamReader == nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_empty_response", ex.requestID)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, 0, time.Since(startedAt), ex.requestID, nil)
		}
		writeJSONError(w, http.StatusBadGateway, "upstream_empty_response", "upstream returned no response body")
		return nil, false
	}
	defer closeDispatchResult(dispatchRes)
	raw, readErr := io.ReadAll(io.LimitReader(dispatchRes.UpstreamReader, 1<<20))
	if readErr != nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_read_error", ex.requestID)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil)
		}
		writeJSONError(w, http.StatusBadGateway, "upstream_read_error", readErr.Error())
		return nil, false
	}
	if dispatchRes.StatusCode < 200 || dispatchRes.StatusCode >= 300 {
		classification, classifyErr := gateway.Classify(dispatchRes.StatusCode, dispatchRes.Headers, raw, ex.accInfo.Platform)
		abortReason := "upstream_error"
		if classifyErr == nil && classification.Class != "" {
			abortReason = "upstream_" + string(classification.Class)
		}
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, abortReason, ex.requestID)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromClassification(dispatchRes.StatusCode, classification), dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, rateLimitResetFromClassification(classification, time.Now()))
		}
		writeNormalizedUpstreamError(w, clientStatusForUpstreamError(dispatchRes.StatusCode, classification.Class), "upstream_error", classification)
		return nil, false
	}
	upstreamAdapter, err := protocolAdapterForBuffered(ex.d.Forwarder, ex.resolved.ProtocolFamily)
	if err != nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_adapter_error", ex.requestID)
		writeJSONError(w, http.StatusBadGateway, "upstream_adapter_error", err.Error())
		return nil, false
	}
	bufferedEnv, _, err := upstreamAdapter.ProviderResponseToCanonical(seedCtx, raw)
	if err != nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "canonical_response_error", ex.requestID)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil)
		}
		writeJSONError(w, http.StatusBadGateway, "canonical_response_error", err.Error())
		return nil, false
	}
	if bufferedEnv != nil {
		_ = seed.ApplyToRequestMeta(&bufferedEnv.RequestMeta)
		enrichCanonicalRequestMeta(bufferedEnv, ex.upstreamModelID, ex.accInfo.Platform, ex.idempotencyHeader, ex.promptHash)
	}
	return ex.finalizeBufferedEnvelope(w, bufferedEnv, dispatchRes.StatusCode, startedAt)
}

// NewMessagesHandler 是 /v1/messages 端点 handler。它复用 chat completions
// 管线，只把 billing endpoint family 标为 messages。
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
