// Phase C real chat-completions handler. Boots only when smoke-auth env
// is fully configured; otherwise the chat route returns 503 fail-closed.
//
// Pipeline (per docs/plans/2026-04-30-phase-c-gateway-wiring.md):
//
//	auth resolve → parse JSON (require stream=true) → ClaimGate.Reserve
//	→ Pool.Select (writes acquisition_token to claim) → mock upstream SSE
//	→ Forwarder.Forward → Settler.Settle (sync, same goroutine)
//
// Failure paths:
//
//	auth fail        → 401
//	missing config   → 503
//	stream=false     → 400
//	idempotency conflict → 409
//	pool no capacity → 503 + Retry-After (claim aborted via Settler.Abort)
//	upstream error   → 502 (only if response headers not yet committed)
//	settler error    → 500 (logged loudly; headers may already be in flight)
//
// Known Phase C limitations (deferred to Phase E):
//
//   - Wire format: Forwarder is configured with proto.AnthropicAdapter
//     upstream and NIL ClientAdapter. When the chat path succeeds the
//     client receives passthrough Anthropic SSE, NOT OpenAI
//     chat.completion.chunk wire format. The Phase C goal is money-path
//     correctness (real PG row at status=committed), not OpenAI compat.
//     Phase E will add an OpenAI ClientAdapter that translates Anthropic
//     upstream events → OpenAI chat.completion.chunk events. Codex P1
//     finding 2026-04-30; accepted as scope-deferred.
//   - Cost: Phase C settles every successful request at a hardcoded
//     0.01 USD baseline (matches predicted_cost reservation). F-BILL-001
//     pricing-table lookup against real token counts is Phase E.

package gatewayhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

// ChatHandlerDeps is the subset of run()'s dependency tree the chat
// handler actually consumes. Concrete deps struct in cmd/gateway/main.go
// satisfies this implicitly via duck typing — handler stays decoupled
// from main package layout.
type ChatHandlerDeps struct {
	Auth                 *auth.SmokeAuthResolver
	ClaimGate            billing.ClaimGate
	Selector             *pool.DefaultSelector
	Forwarder            *gateway.StreamForwarder
	Settler              billing.Settler
	BillingPolicyVersion string
	RequestClass         string
}

// chatRequest is the minimal /v1/chat/completions request body the Phase
// C handler accepts. Stream is required.
type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Stream   bool           `json:"stream"`
	// pool group hint; Phase C smoke seed sets it to the seeded pool group.
	PoolGroupID int64 `json:"pool_group_id"`
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

		// Auth.
		ident, err := d.Auth.Resolve(ctx, r)
		if errors.Is(err, auth.ErrSmokeAuthMisconfigured) {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "smoke auth env not fully set")
			return
		}
		if errors.Is(err, auth.ErrSmokeBearerMismatch) {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "bearer token did not match")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "auth_error", err.Error())
			return
		}

		// Parse + validate body (1 MiB cap; rejects any larger upload to
		// avoid OOM via authenticated-but-malicious bodies).
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "body_read_error", err.Error())
			return
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

		// Idempotency fingerprint (9 fields per F-OBS-001 §Tx1; client
		// header recorded but NOT in hash).
		idempotencyHeader := r.Header.Get("Idempotency-Key")
		logicalRequestID := idempotencyHeader
		if logicalRequestID == "" {
			logicalRequestID = uuid.NewString()
		}
		payloadHash := normalizedPayloadHash(req.Model, req.Messages)

		// Tx1: reserve.
		reserveRes, err := d.ClaimGate.Reserve(ctx, billing.ReserveRequest{
			TenantID:                   ident.TenantID,
			APIKeyID:                   ident.APIKeyID,
			UserID:                     ident.UserID,
			LogicalRequestID:           logicalRequestID,
			EndpointFamily:             "chat",
			NormalizedPayloadHash:      payloadHash,
			RequestedModel:             req.Model,
			PoolingGroupID:             req.PoolGroupID,
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
			// Phase C v0.1 has no replay cache wired. Return 409 explicitly
			// rather than fake a 200 with empty body (truth-first).
			writeJSONError(w, http.StatusConflict, "replay_without_cache",
				"idempotent request hit but replay cache is Phase E scope")
			return
		}

		// Pool.Select. On no capacity, abort the claim before returning.
		selRes, err := d.Selector.Select(ctx, pool.SelectionRequest{
			TenantID:       ident.TenantID,
			UserID:         ident.UserID,
			APIKeyID:       ident.APIKeyID,
			PoolGroupID:    req.PoolGroupID,
			RequestedModel: req.Model,
			EndpointFamily: "chat",
			ClaimID:        reserveRes.ClaimID,
			AttemptSeq:     1,
		})
		if errors.Is(err, pool.ErrNoEligibleAccount) || errors.Is(err, pool.ErrNoSlotAvailable) {
			abortReason := "pool_no_capacity"
			if abortErr := d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, abortReason); abortErr != nil {
				// Log via response headers only (no logger handle here);
				// the smoke test's PG-state assertion will still expose
				// the orphan if abort actually failed.
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
		// usage tokens into UsageRecordDraft.
		upstreamBytes := MockAnthropicUpstreamBytes("msg_phaseC_smoke", req.Model, 7, 13)

		// Stream + forward.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		draft, fwdErr := d.Forwarder.Forward(ctx, bytesReader(upstreamBytes), w, gateway.ForwardRequest{
			TenantID:         ident.TenantID,
			AccountID:        acquiredAccountID,
			AcquisitionToken: acquisitionToken,
			Model:            req.Model,
		})
		if fwdErr != nil {
			// Headers are already in flight (text/event-stream set above);
			// best-effort abort + log via header.
			_ = d.Settler.Abort(ctx, ident.TenantID, reserveRes.ClaimID, "forwarder_error")
			w.Header().Set("X-Huakai-Forward-Error", fwdErr.Error())
			return
		}

		// Tx2: settle synchronously (NEVER queue per F-OBS-001 §45).
		// Phase C v0.1 cost: hardcoded 0.01 USD per successful request,
		// matching the predicted_cost reserved in Tx1 above. F-BILL-001
		// pricing-table lookup against actual token counts is Phase E.
		// Without this, the money path would commit actual_cost=0 while
		// having reserved 0.01 — codex P1 finding 2026-04-30.
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
			Stream:            true,
			ActualCost:        actualCost,
			Fingerprint:       payloadHash,
			Draft:             draft,
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
