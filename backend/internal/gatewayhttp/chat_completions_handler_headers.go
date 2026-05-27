package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/trust"
)

const (
	headerHUAKAIModelRequested = "X-HUAKAI-Model-Requested"
	headerHUAKAIModelDelivered = "X-HUAKAI-Model-Delivered"
)

func setAccountingModelRequested(env *proto.HCSF, requested string) {
	if env == nil || requested == "" {
		return
	}
	ensureAccountingModelChain(env).Requested = requested
}

func setAccountingModelRouteDecided(env *proto.HCSF, routeDecided string) {
	if env == nil || routeDecided == "" {
		return
	}
	ensureAccountingModelChain(env).RouteDecided = routeDecided
}

func setHUAKAIModelHeaders(h http.Header, requested string, env *proto.HCSF) {
	if h == nil {
		return
	}
	if requested != "" {
		h.Set(headerHUAKAIModelRequested, requested)
	}
	if delivered := deliveredModel(env); delivered != "" {
		h.Set(headerHUAKAIModelDelivered, delivered)
	}
}

func WriteHuakaiHeaders(h http.Header, requested string, env *proto.HCSF, result auditledger.AuditLedgerResult, requestID string, tenantID int64) {
	setHUAKAIModelHeaders(h, requested, env)
	if h == nil {
		return
	}
	meta := trust.MetadataFromHCSF(env)
	if meta.RequestID == "" {
		meta.RequestID = requestID
	}
	trust.WriteResponseHeaders(h, meta, result)
	switch result.State {
	case auditledger.LedgerResultStatePersisted:
		WriteHuakaiLedgerHeaders(h, requestID, result.LedgerID, result.Fingerprint, tenantID)
	case auditledger.LedgerResultStateDeferred:
		// W5 Owner Bug #1 / synthesis §6 C3:Deferred ledger result 写 DLQ ref header
		// 与流式 trailer 语义对齐;客户端可拿这个 ref 后续问 admin DLQ replay 复理。
		// Persisted 路径的 ledger id / verify url / signature 都没意义,故不写。
		if result.DLQRef != "" {
			h.Set(headerHUAKAIAuditLedgerDLQRef, result.DLQRef)
		}
	}
}

func WriteHuakaiLedgerHeaders(h http.Header, requestID, ledgerID, sigFingerprint string, tenantID int64) {
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
		if scopeRef := auditledger.TenantScopeRef(tenantID); scopeRef != "" {
			query.Set("tenant_scope_ref", scopeRef)
		}
		h.Set(headerHUAKAIAuditVerify, "/v1/audit/verify?"+query.Encode())
	}
}

func ensureAccountingModelChain(env *proto.HCSF) *proto.ModelChain {
	if env.Accounting.ModelChain == nil {
		env.Accounting.ModelChain = &proto.ModelChain{}
	}
	return env.Accounting.ModelChain
}

func deliveredModel(env *proto.HCSF) string {
	if env == nil {
		return ""
	}
	if env.BufferedResponse != nil && env.BufferedResponse.Model != "" {
		return env.BufferedResponse.Model
	}
	if env.Accounting.ModelChain == nil {
		return ""
	}
	if env.Accounting.ModelChain.UpstreamReported != "" {
		return env.Accounting.ModelChain.UpstreamReported
	}
	return env.Accounting.ModelChain.RouteDecided
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
	AttemptSeq        int
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
	ledgerResult, err := submitAuditLedgerEntry(ctx, d, cachedEnv, in.Ident.TenantID, in.RequestID)
	// in.AccountID == 0 表示 cache 检查在 acquire 之前,
	// reserve 完成但还没拿到 pool slot / acquisition_token, 不能走 settleCompletion
	// (它 lookup claim by acquisition_token 找不到行返 500)。
	if in.ReserveResult == nil || in.AccountID == 0 {
		if err != nil {
			if in.ReserveResult != nil {
				if abortErr := d.Settler.Abort(ctx, in.Ident.TenantID, in.ReserveResult.ClaimID, "audit_ledger_error", in.RequestID, 0); abortErr != nil {
					setAbortFailedHeader(w, ctx, in.RequestID, abortErr)
				}
			}
			writeLoggedJSONError(ctx, in.RequestID, w, http.StatusInternalServerError, clienterr.CodeAuditLedgerError, err)
			return true
		}
		// cache 命中是成功请求 (返 200 缓存体), claim 必须以 committed (零成本)
		// 终结而非 aborted — 否则审计把成功请求记成中止。 CommitCacheHit 同时写
		// 一条 provider-less usage_records 行, 使 receipt / 用量视图可见。 终结
		// 必须在写 200 body 之前完成, 否则进程退出会让 claim 永卡 reserving。
		if in.ReserveResult != nil {
			cacheHitReq := billing.SettleRequest{
				ClaimID:         in.ReserveResult.ClaimID,
				TenantID:        in.Ident.TenantID,
				AuditRequestID:  in.RequestID,
				RequestedModel:  in.RequestedModel,
				UpstreamModel:   in.UpstreamModelID,
				Provider:        in.Provider,
				Stream:          false,
				RequestedAt:     in.RequestStartedAt,
				Fingerprint:     in.PayloadHash,
				Draft:           nonStreamingUsageDraft(cachedEnv, decimal.Zero, routingReasonWithCacheHit(routingReason, true, in.Entry.Key)),
				SnapshotVersion: in.PlanSnapshot,
			}
			auditEvent := eventbus.RequestCompletionEvent{
				ID:                        in.RequestID,
				TenantID:                  in.Ident.TenantID,
				ClaimID:                   in.ReserveResult.ClaimID,
				AccountID:                 in.AccountID,
				RequestID:                 in.RequestID,
				EndpointFamily:            d.effectiveEndpointFamily(),
				RequestedModel:            in.RequestedModel,
				UpstreamModel:             in.UpstreamModelID,
				PayloadHash:               in.PayloadHash,
				AuditLedgerID:             ledgerID(ledgerResult),
				AuditLedgerDLQRef:         ledgerDLQRef(ledgerResult),
				AuditSignatureFingerprint: ledgerFingerprint(ledgerResult),
				SettleRequest:             cacheHitReq,
				Metadata:                  routeMetadata(in.RouteID),
			}
			if err := validateMoneyPathAuditRefForSource(ctx, d, auditEvent, "cache_hit_commit"); err != nil {
				rejectErr, abortErr := rejectMoneyPathCacheHitCommit(ctx, d, auditEvent, err)
				if abortErr != nil {
					setAbortFailedHeader(w, ctx, in.RequestID, abortErr)
				}
				writeLoggedJSONError(ctx, in.RequestID, w, http.StatusInternalServerError, clienterr.CodeAuditRefMissing, rejectErr)
				return true
			}
			if commitErr := d.Settler.CommitCacheHit(ctx, cacheHitReq); commitErr != nil {
				writeLoggedJSONError(ctx, in.RequestID, w, http.StatusInternalServerError, clienterr.CodeCacheSettleError, commitErr)
				return true
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-HUAKAI-Cache-L2", "hit")
		WriteHuakaiHeaders(w.Header(), in.RequestedModel, cachedEnv, ledgerResult, in.RequestID, in.Ident.TenantID)
		recordCacheHitReplay(ctx, d, in)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(in.Entry.Body)
		return true
	}
	if err != nil {
		if abortErr := d.Settler.Abort(ctx, in.Ident.TenantID, in.ReserveResult.ClaimID, "audit_ledger_error", in.RequestID, 0); abortErr != nil {
			setAbortFailedHeader(w, ctx, in.RequestID, abortErr)
		}
		writeLoggedJSONError(ctx, in.RequestID, w, http.StatusInternalServerError, clienterr.CodeAuditLedgerError, err)
		return true
	}
	actualCost := decimal.Zero
	attemptSeq := in.AttemptSeq
	if attemptSeq <= 0 {
		attemptSeq = 1
	}
	settleReq := billing.SettleRequest{
		ClaimID:           in.ReserveResult.ClaimID,
		AccountID:         in.AccountID,
		AcquisitionToken:  in.AcquisitionToken,
		TenantID:          in.Ident.TenantID,
		APIKeyID:          in.Ident.APIKeyID,
		UserID:            in.Ident.UserID,
		ProviderAccountID: in.AccountID,
		AttemptSeq:        int32(attemptSeq),
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
		AuditLedgerID:             ledgerID(ledgerResult),
		AuditLedgerDLQRef:         ledgerDLQRef(ledgerResult),
		AuditSignatureFingerprint: ledgerFingerprint(ledgerResult),
		SettleRequest:             settleReq,
		Metadata:                  routeMetadata(in.RouteID),
	}); err != nil {
		writeLoggedJSONError(ctx, in.RequestID, w, http.StatusInternalServerError, settleErrorCode(err), err)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-HUAKAI-Cache-L2", "hit")
	WriteHuakaiHeaders(w.Header(), in.RequestedModel, cachedEnv, ledgerResult, in.RequestID, in.Ident.TenantID)
	recordCacheHitReplay(ctx, d, in)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(in.Entry.Body)
	return true
}

func cacheEntry(ex *chatExecution, cacheKey string, clientBody, cacheEnvelope []byte) l2cache.Entry {
	return l2cache.Entry{
		Key:      cacheKey,
		TenantID: ex.ident.TenantID,
		Vendor:   ex.cacheVendor,
		Model:    ex.upstreamModelID,
		Status:   http.StatusOK,
		Body:     clientBody,
		Envelope: cacheEnvelope,
	}
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
