package gatewayhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/protosse"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

type authResolver interface {
	Resolve(ctx context.Context, req *http.Request) (auth.Identity, error)
}

type ChatHandlerDeps struct {
	Auth                  authResolver
	Registry              registry.Registry
	Router                router.Router
	ClaimGate             billing.ClaimGate
	RateTables            billing.RateTableSource
	Selector              pool.Selector
	CredentialVault       provider.CredentialVault
	Dispatcher            *gateway.UpstreamDispatcher
	CanonicalDispatcher   HCSFDispatcher
	Forwarder             *gateway.StreamForwarder
	ResponseCache         l2cache.Store
	Settler               billing.Settler
	ReplayStore           billing.ReplayStore
	BillingPolicyResolver *billing.PolicyResolver
	CompletionBus         *eventbus.Bus
	AuditRefPolicy        *eventbus.AuditRefPolicy
	AuditLedger           auditledger.Ledger
	AuditLedgerDLQ        auditledger.DLQEnqueuer
	// SettleRecoveryDLQ 是 post-delivery settle 失败(流式响应已发给客户端
	// 但 Tx2 settlement 未确认提交)的 durable 兜底 enqueue;nil 时 stream
	// path 失败只 log,money path 灰区无可补救。生产部署必须 wire 上
	// dlq.Service(见 cmd/gateway/routes.go SettleRecoveryDLQ: d.dlqService)。
	SettleRecoveryDLQ    settlementrecovery.Enqueuer
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

	ident           auth.Identity
	body            []byte
	req             chatRequest
	clientProtocol  proto.ClientProtocol
	clientAdapter   proto.ClientAdapter
	requestID       string
	clientRequestID string

	resolved          registry.Resolved
	plan              router.RoutePlan
	attempt           router.AttemptPlan
	routeID           string
	currentAttemptSeq int

	idempotencyHeader                string
	logicalRequestID                 string
	payloadHash                      string
	promptHash                       string
	reserveRes                       *billing.ReserveResult
	streamInputOnlyInterruptedPolicy billing.StreamInputOnlyInterruptedPolicy

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
		ctx = context.WithValue(ctx, middleware.RequestIDKey, validated.RequestID)
		r = r.WithContext(ctx)
		w.Header().Set(middleware.RequestIDHeader, validated.RequestID)
		exec := newChatExecution(d, r, ident, validated, requestStartedAt)
		if !exec.prepareRoute(w) {
			return
		}
		// 先 reserve (ClaimGate.Reserve 内部走
		// uq_claims_idempotency 唯一约束做 idempotency-key + payload fingerprint
		// 校验), 再查 cache。 否则 client reuse 同 idempotency-key 但 payload 变
		// 时, cache 命中绕过 fingerprint conflict 检查。 reserve 不占 pool slot
		// (selectPoolAccount 才占), 所以 cache 命中分支仍能避开 pool acquire。
		if !exec.reserveClaim(w) {
			return
		}
		if !exec.req.Stream {
			// cache 命中时 serveL2CacheHit 内部已在写 200 body 之前 Settler.Abort
			// 收尾 reserve 行。这里只需 return。
			handled, proceed := exec.serveL2CacheIfAvailable(w)
			if handled || !proceed {
				return
			}
		}
		failedAccounts := make(map[int64]struct{})
		authFailoverUsed := false
		budget := effectiveAttemptBudget(exec.plan)
		for i := 0; i < budget; i++ {
			outcome := exec.runAttempt(w, attemptInput{
				Plan:             exec.plan.Attempts[i],
				AttemptSeq:       i + 1,
				ExcludedAccounts: failedAccounts,
				ReplayableBody:   true,
				FinalAttempt:     i+1 >= budget,
			})
			if outcome.Success != nil {
				writeAttemptSuccess(w, outcome)
				return
			}
			if outcome.DeliveryStarted || (outcome.Failure != nil && outcome.Failure.DeliveredToClient) {
				return
			}
			if outcome.AccountID != 0 && outcome.Failure != nil && outcome.Failure.Decision.SwitchAccount {
				failedAccounts[outcome.AccountID] = struct{}{}
			}
			if outcome.Failure != nil {
				retry, consumeAuthBudget := shouldRetryAttemptFailure(outcome.Failure, exec.plan, true, i+1 >= budget, authFailoverUsed)
				if retry {
					if consumeAuthBudget {
						authFailoverUsed = true
					}
					clearRetryableAttemptFailureHeaders(w)
					exec.prepareNextAttemptAfterAbort()
					continue
				}
				writeAttemptFailure(w, outcome.Failure)
				return
			}
		}
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

const maxRawBufferedUpstreamBodyBytes = 1 << 20

var errRawBufferedUpstreamBodyTooLarge = errors.New("gatewayhttp: upstream buffered response exceeds 1MiB limit")

func readRawBufferedUpstreamBody(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxRawBufferedUpstreamBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxRawBufferedUpstreamBodyBytes {
		// 超限时保留截断 body，供 caller 对非 2xx 上游响应继续做错误分类。
		return raw[:maxRawBufferedUpstreamBodyBytes], errRawBufferedUpstreamBodyTooLarge
	}
	return raw, nil
}

func (ex *chatExecution) dispatchRawBuffered(w http.ResponseWriter, seed proto.RequestMetaSeed, seedCtx context.Context, startedAt time.Time) (*proto.HCSF, *classifiedAttemptFailure, bool) {
	dispatchRes, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     ex.body,
		Account:         ex.accInfo,
		Credential:      ex.cred,
	})
	if err != nil {
		classification, _ := gateway.Classify(0, nil, []byte(err.Error()), ex.accInfo.Platform)
		decision := gateway.ClassifyAttemptDispatchError(err)
		if !decision.RetryableBeforeDelivery && decision.TransportClass == gateway.TransportErrorLocalDispatch {
			decision.ClientStatus = http.StatusBadGateway
		}
		if decision.AbortReason == "" {
			decision.AbortReason = "upstream_dispatch_error"
		}
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, decision.AbortReason, ex.requestID, 0)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromDispatchError(err, classification), 0, time.Since(startedAt), ex.requestID, nil)
		}
		failure := classifiedFailureFromDecision("", clienterr.MessageFor(clienterr.CodeUpstreamDispatchError), classification, decision, err)
		return nil, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr), false
	}
	if dispatchRes == nil || dispatchRes.UpstreamReader == nil {
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_empty_response", ex.requestID, 0)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, 0, time.Since(startedAt), ex.requestID, nil)
		}
		failure := retryableLocalAttemptFailure(http.StatusBadGateway, clienterr.CodeUpstreamEmptyResponse, clienterr.MessageFor(clienterr.CodeUpstreamEmptyResponse), "upstream_empty_response", gateway.UpstreamError5xx, nil)
		return nil, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr), false
	}
	defer closeDispatchResult(dispatchRes)
	raw, readErr := readRawBufferedUpstreamBody(dispatchRes.UpstreamReader)
	oversizedNon2xx := errors.Is(readErr, errRawBufferedUpstreamBodyTooLarge) && (dispatchRes.StatusCode < 200 || dispatchRes.StatusCode >= 300)
	if readErr != nil && !oversizedNon2xx {
		code := clienterr.CodeUpstreamReadError
		if errors.Is(readErr, errRawBufferedUpstreamBodyTooLarge) {
			code = clienterr.CodeUpstreamResponseTooLarge
		}
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, code, ex.requestID, 0); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadGateway, code, readErr)
		return nil, nil, false
	}
	if dispatchRes.StatusCode < 200 || dispatchRes.StatusCode >= 300 {
		decision, classification, classifyErr := gateway.ClassifyAttemptHTTPError(dispatchRes.StatusCode, dispatchRes.Headers, raw, ex.accInfo.Platform)
		if classifyErr != nil {
			classification, _ = gateway.Classify(dispatchRes.StatusCode, dispatchRes.Headers, raw, ex.accInfo.Platform)
			decision = gateway.AttemptRetryDecision{ClientStatus: clientStatusForUpstreamError(dispatchRes.StatusCode, classification.Class), AbortReason: "upstream_error"}
		}
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, decision.AbortReason, ex.requestID, 0)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromClassification(dispatchRes.StatusCode, classification), dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, rateLimitResetFromClassification(classification, time.Now()))
		}
		failure := classifiedFailureFromDecision("", clienterr.MessageFor(clienterr.CodeUpstreamDispatchError), classification, decision, nil)
		return nil, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr), false
	}
	upstreamAdapter, err := protocolAdapterForBuffered(ex.d.Forwarder, ex.resolved.ProtocolFamily)
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_adapter_error", ex.requestID, 0); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadGateway, clienterr.CodeUpstreamAdapterError, err)
		return nil, nil, false
	}
	bufferedEnv, _, err := upstreamAdapter.ProviderResponseToCanonical(seedCtx, raw)
	if err != nil {
		if reconstructedEnv, _, ok := protosse.ReconstructBufferedFromSSE(upstreamAdapter, raw); ok && reconstructedEnv != nil {
			bufferedEnv = reconstructedEnv
		} else {
			if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "canonical_response_error", ex.requestID, 0); abortErr != nil {
				setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
			}
			if ex.healthKeyOK {
				recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil)
			}
			writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadGateway, clienterr.CodeCanonicalResponseError, err)
			return nil, nil, false
		}
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
