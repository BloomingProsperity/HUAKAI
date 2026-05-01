// Phase C real chat-completions handler.
//
// Pipeline (per docs/plans/2026-04-30-n5b-handler-rewrite.md):
//
//	auth resolve -> raw-JSON reject body pool_group_id -> parse JSON
//	-> Registry.ResolveModel -> Router.Plan
//	-> ClaimGate.Reserve -> Pool.Select (writes acquisition_token)
//	-> Forwarder.Forward (with resolved.ProviderModelID)
//	-> Settler.Settle (with plan.SnapshotVersion)
//
// Failure paths:
//
//	missing Registry/Router    -> 503 gateway_not_configured
//	auth fail                  -> 401 (or 503 for misconfigured/backend)
//	body has pool_group_id     -> 400 body_field_disallowed
//	missing_model              -> 400
//	stream=false               -> 400
//	registry unknown/disabled/no_access -> 404 model_not_available (uniform anti-enum)
//	registry backend           -> 503 registry_backend_error
//	router plan error          -> 500 router_plan_error
//	idempotency conflict       -> 409
//	pool no capacity           -> 503 + Retry-After (claim aborted via Settler.Abort)
//	upstream error             -> 502 (only if response headers not yet committed)
//	settler error              -> 500 (logged loudly; headers may already be in flight)
//
// Slice 2 (N+5b 2026-05-01) changes from prior version:
//   - chatRequest.PoolGroupID DELETED. Body must not include the field.
//   - Registry resolves the public model alias into pool candidates +
//     provider model id + snapshot version.
//   - Router stamps the combined registry+router snapshot onto RoutePlan.
//   - Forwarder receives resolved.ProviderModelID (binding override
//     applied per N+5a) instead of the client's public alias string.
//   - Settler receives plan.SnapshotVersion which lands on
//     usage_records.snapshot_version.
//
// Known Phase C limitations (deferred to Phase E):
//
//   - Wire format: Forwarder is configured with proto.AnthropicAdapter
//     upstream and NIL ClientAdapter. When the chat path succeeds the
//     client receives passthrough Anthropic SSE, NOT OpenAI
//     chat.completion.chunk wire format.
//   - Cost: hardcoded 0.01 USD per successful request; F-BILL-001 pricing
//     lookup against actual token counts is Phase E.
//   - Audit log: error reasons (registry_unknown / registry_disabled /
//     registry_no_access / registry_backend) are NOT exposed to clients
//     in headers or body — leaking them defeats the uniform 404
//     anti-enumeration design (codex N+5b P1 pass2 finding 2026-05-01).
//     They land in the structured logger that Phase E plumbs through;
//     L0 simply has no logger handle in this scope.

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
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

// authResolver is the local interface the handler depends on so unit
// tests can stub auth without booting a real DB. *auth.APIKeyResolver
// already satisfies this signature.
type authResolver interface {
	Resolve(ctx context.Context, req *http.Request) (auth.Identity, error)
}

// ChatHandlerDeps is the subset of run()'s dependency tree the chat
// handler actually consumes.
type ChatHandlerDeps struct {
	Auth                 authResolver
	Registry             registry.Registry
	Router               router.Router
	ClaimGate            billing.ClaimGate
	Selector             pool.Selector
	Forwarder            *gateway.StreamForwarder
	Settler              billing.Settler
	BillingPolicyVersion string
	RequestClass         string
}

// chatRequest is the minimal /v1/chat/completions request body the
// handler accepts. Stream is required. PoolGroupID is INTENTIONALLY
// absent — the gateway resolves the pool from the model alias via
// Registry as of N+5b.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// NewChatCompletionsHandler returns the http.HandlerFunc to wire under
// POST /v1/chat/completions.
func NewChatCompletionsHandler(d ChatHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Boot-time misconfig fail-closed. Codex N+5b pass1 flagged
		// Registry/Router; pass2 flagged that the new interface-typed
		// Auth + Selector + ClaimGate + Settler + Forwarder fields can
		// also panic if left nil. Guard them all here so a deps wiring
		// gap in main.go surfaces as 503 rather than a runtime panic.
		if d.Registry == nil || d.Router == nil || d.Auth == nil ||
			d.Selector == nil || d.ClaimGate == nil || d.Settler == nil ||
			d.Forwarder == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"chat handler dependency unset")
			return
		}

		// Auth — table-backed APIKeyResolver per N+4a synthesized plan.
		// Three error classes mapped to distinct HTTP status:
		//   ErrAuthMisconfigured -> 503 (boot-time config gap)
		//   ErrAuthBackend       -> 503 (transient infra outage)
		//   ErrUnauthorized      -> 401 (credential failure; uniform per D10)
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

		// Read body once with a 1 MiB cap.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "body_read_error", err.Error())
			return
		}

		// Pre-parse to detect the removed pool_group_id field. Done
		// before canonical struct unmarshal because Go's json.Unmarshal
		// silently drops unknown fields. We can't use
		// Decoder.DisallowUnknownFields() because the public OpenAI
		// schema includes many fields the Phase C handler ignores (e.g.
		// temperature, top_p, max_tokens, response_format, …).
		// Synthesized N+5b plan §D2 — Codex-recommended approach.
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(body, &keys); err == nil {
			if _, found := keys["pool_group_id"]; found {
				writeJSONError(w, http.StatusBadRequest, "body_field_disallowed",
					"pool_group_id field removed in N+5b; the gateway resolves the pool from the model alias")
				return
			}
		}
		// If unmarshal-to-map failed, the canonical unmarshal below will
		// surface the same JSON error in a familiar shape.

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

		// Registry resolve. Slice 2 (N+5b) — replaces body pool_group_id.
		// Per synthesized plan §D1: ALL credential-grade failure modes
		// collapse to a uniform 404 model_not_available so attackers
		// cannot distinguish unknown / disabled / no-access by status,
		// body, OR response header (codex N+5b P1 pass2 finding
		// 2026-05-01 — never leak the internal reason via X-Huakai-*
		// headers; audit reason lives server-side ONLY, and lands in
		// the structured logger that Phase E threads through.
		// ErrRegistryBackend stays 503 because a transient backend
		// outage is observably different from "model not available".
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

		// Router.Plan. Builds RoutePlan with Attempts[0].PoolGroupID
		// from resolved.PoolCandidates[0] (L0 single-attempt). Stamps
		// concatenated registry+router snapshot per synthesized §D3.
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

		// Idempotency fingerprint (9 fields per F-OBS-001 §Tx1; client
		// header recorded but NOT in hash).
		idempotencyHeader := r.Header.Get("Idempotency-Key")
		logicalRequestID := idempotencyHeader
		if logicalRequestID == "" {
			logicalRequestID = uuid.NewString()
		}
		payloadHash := normalizedPayloadHash(req.Model, req.Messages)

		// Tx1: reserve. PoolingGroupID is now from the Router-emitted
		// attempt, not the request body.
		reserveRes, err := d.ClaimGate.Reserve(ctx, billing.ReserveRequest{
			TenantID:                   ident.TenantID,
			APIKeyID:                   ident.APIKeyID,
			UserID:                     ident.UserID,
			LogicalRequestID:           logicalRequestID,
			EndpointFamily:             "chat",
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

		// Pool.Select. On no capacity, abort the claim before returning.
		// CapabilityFlags carry the Router's required capability set so
		// the pool's intra-pool gate can filter accounts that lack
		// e.g. streaming or tool-use support (codex N+5b P2 pass2).
		selRes, err := d.Selector.Select(ctx, pool.SelectionRequest{
			TenantID:        ident.TenantID,
			UserID:          ident.UserID,
			APIKeyID:        ident.APIKeyID,
			PoolGroupID:     attempt.PoolGroupID,
			RequestedModel:  req.Model,
			EndpointFamily:  "chat",
			ClaimID:         reserveRes.ClaimID,
			AttemptSeq:      1,
			CapabilityFlags: attempt.RequiredCapabilities,
		})
		if errors.Is(err, pool.ErrNoEligibleAccount) || errors.Is(err, pool.ErrNoSlotAvailable) {
			abortReason := "pool_no_capacity"
			if abortErr := d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, abortReason); abortErr != nil {
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

		// Mock upstream emits Anthropic SSE; AnthropicAdapter extracts
		// usage tokens into UsageRecordDraft. Slice 5 swaps in real
		// upstream call; until then the mock model id is what the
		// AnthropicAdapter parses out.
		upstreamModelID := resolved.ProviderModelID
		if upstreamModelID == "" {
			upstreamModelID = req.Model
		}
		upstreamBytes := MockAnthropicUpstreamBytes("msg_phaseC_smoke", upstreamModelID, 7, 13)

		// Stream + forward. ProviderModelID carries the binding-level
		// upstream override resolved by Registry (N+5a) — so a tenant
		// alias like "claude-latest" can call upstream with a pinned id
		// like "claude-opus-4-7-20251101" without baking the rename
		// into the canonical model row.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		draft, fwdErr := d.Forwarder.Forward(ctx, bytesReader(upstreamBytes), w, gateway.ForwardRequest{
			TenantID:         ident.TenantID,
			AccountID:        acquiredAccountID,
			AcquisitionToken: acquisitionToken,
			Model:            upstreamModelID,
		})
		if fwdErr != nil {
			_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "forwarder_error")
			w.Header().Set("X-Huakai-Forward-Error", fwdErr.Error())
			return
		}

		// Tx2: settle synchronously (NEVER queue per F-OBS-001 §45).
		// Phase C v0.1 cost: hardcoded 0.01 USD; F-BILL-001 pricing-table
		// lookup is Phase E. Slice 2 (N+5b): SnapshotVersion stamps
		// usage_records.snapshot_version for audit replay.
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

// normalizedPayloadHash hashes (model + messages-in-original-order) into
// a deterministic SHA-256 hex string. Message ORDER is semantically
// meaningful in chat conversations — two messages [user, assistant]
// are not the same as [assistant, user]. We never reorder.
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

func bytesReader(b []byte) io.Reader {
	return &bytesIO{b: b}
}

type bytesIO struct {
	b []byte
	i int
}

func (r *bytesIO) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
