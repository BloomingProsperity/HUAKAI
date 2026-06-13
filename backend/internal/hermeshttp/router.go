// Package hermeshttp 暴露 Hermes user-facing HTTP endpoints。
package hermeshttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/headerfirewall"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeschat"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
	"github.com/BloomingProsperity/HUAKAI/internal/modulehttp"
)

type AuthResolver interface {
	Resolve(context.Context, *http.Request) (sessionauth.Identity, error)
}

type authContextKey struct{}

// ToolRegistry is the registry of ops tools the tool-execute handler dispatches
// to. *hermesops.Registry satisfies it. It covers BOTH the read-only diagnostic
// dispatch (Run) and the mutating-tool authorization (AuthorizeMutating) that
// the confirm-gated mutate path uses.
type ToolRegistry interface {
	List() []hermesops.ToolSpec
	Get(name string) (hermesops.ToolSpec, bool)
	Authorize(name, actorRole string) (hermesops.ToolSpec, error)
	AuthorizeMutating(name, actorRole string) (hermesops.ToolSpec, error)
	Run(ctx context.Context, name string, req hermesops.ToolRequest) (hermesops.ToolResult, error)
}

// MutateOrchestrator runs a confirmed mutating tool under the atomic-audit +
// advisory-lock transaction. *hermesops.MutateOrchestrator satisfies it. Kept
// as an interface so the mutate handler is unit-testable with a fake.
type MutateOrchestrator interface {
	Execute(ctx context.Context, lockKey string, rec hermesops.MutationAuditRecord, mutate func(ctx context.Context, tx pgx.Tx) (hermesops.ToolResult, error)) (hermesops.ToolResult, error)
}

// ContextSource provides the merged module-knowledge view for GET
// /v1/hermes/context. *cmdgateway moduleSource (a modulehttp.Source) is wrapped
// by the wiring; the handler only needs the merged accessor.
type ContextSource interface {
	modulehttp.Source
}

type handler struct {
	svc            *hermes.Service
	runner         *hermes.RunnerClient
	chatBridge     *hermeschat.Bridge
	headerSettings headerfirewall.PlatformSettings
	tools          ToolRegistry
	toolCalls      hermesops.ToolCallInserter
	contextSource  ContextSource
	// mutator + confirmCache back the WAVE H4 mutating-tool path. When mutator is
	// nil, mutating tools are rejected (503) — the read-only path is unaffected.
	mutator      MutateOrchestrator
	confirmCache *confirmCache
}

type RouterDeps struct {
	Service        *hermes.Service
	Runner         *hermes.RunnerClient
	Bridge         *hermeschat.Bridge
	HeaderSettings headerfirewall.PlatformSettings
	// Tools, ToolCalls, and ContextSource wire the WAVE H3 read-only ops spine.
	// All are optional: when unset, the tool/context routes return a 503
	// (service unavailable) rather than panicking.
	Tools         ToolRegistry
	ToolCalls     hermesops.ToolCallInserter
	ContextSource ContextSource
	// Mutator wires the WAVE H4 mutating-tool path (atomic-audit + advisory-lock
	// orchestrator). Optional: when unset, mutating tools are rejected (503) and
	// the read-only path is unaffected.
	Mutator MutateOrchestrator
}

func NewRouter(svc *hermes.Service, runnerClient *hermes.RunnerClient, bridges ...*hermeschat.Bridge) http.Handler {
	var bridge *hermeschat.Bridge
	if len(bridges) > 0 {
		bridge = bridges[0]
	}
	return NewRouterWithDeps(RouterDeps{Service: svc, Runner: runnerClient, Bridge: bridge})
}

func NewRouterWithDeps(d RouterDeps) http.Handler {
	h := handler{
		svc:            d.Service,
		runner:         d.Runner,
		chatBridge:     d.Bridge,
		headerSettings: d.HeaderSettings,
		tools:          d.Tools,
		toolCalls:      d.ToolCalls,
		contextSource:  d.ContextSource,
		mutator:        d.Mutator,
		confirmCache:   newConfirmCache(),
	}
	r := chi.NewRouter()
	r.Get("/settings", h.getSettings)
	r.Post("/settings/enable", h.enableSettings)
	r.Post("/settings/disable", h.disableSettings)
	r.Post("/api-profiles", h.createProfile)
	r.Get("/api-profiles", h.listProfiles)
	r.Get("/api-profiles/{id}", h.getProfile)
	r.Delete("/api-profiles/{id}", h.deleteProfile)
	r.Post("/chat", h.startChat)
	r.Get("/conversations", h.listConversations)
	r.Get("/conversations/{id}", h.getConversation)
	r.Delete("/conversations/{id}", h.deleteConversation)
	r.Get("/conversations/{id}/messages", h.listConversationMessages)
	// WAVE H3 read-only ops spine + WAVE H4 mutating ops tools. tool-execute
	// dispatches read-only tools directly and routes mutating tools through the
	// dry-run + confirm flow (confirm=false => preview, confirm=true => execute).
	r.Get("/tools", h.listTools)
	r.Post("/tool-execute", h.executeTool)
	r.Get("/context", h.getModuleContext)
	return r
}

func APIKeyMiddleware(resolver AuthResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if resolver == nil {
				writeError(w, http.StatusServiceUnavailable, "hermes_auth_unavailable", "hermes auth resolver unset")
				return
			}
			ident, err := resolver.Resolve(r.Context(), r)
			if err != nil {
				if errors.Is(err, sessionauth.ErrAuthBackend) || errors.Is(err, sessionauth.ErrAuthMisconfigured) {
					writeError(w, http.StatusServiceUnavailable, "hermes_auth_backend_error", "hermes auth backend transient failure")
					return
				}
				if errors.Is(err, sessionauth.ErrForbidden) {
					writeError(w, http.StatusForbidden, "forbidden", "api key policy forbids this request")
					return
				}
				writeError(w, http.StatusUnauthorized, "hermes_unauthorized", "missing or invalid bearer token")
				return
			}
			ctx := context.WithValue(r.Context(), authContextKey{}, ident)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (h handler) requireIdentity(w http.ResponseWriter, r *http.Request) (sessionauth.Identity, bool) {
	if h.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_service_unavailable", "hermes service unset")
		return sessionauth.Identity{}, false
	}
	ident, ok := r.Context().Value(authContextKey{}).(sessionauth.Identity)
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "hermes_unauthorized", "hermes api key bearer token is required")
		return sessionauth.Identity{}, false
	}
	return ident, true
}

func (h handler) requireRunner(w http.ResponseWriter) bool {
	if h.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_runner_unavailable", "hermes runner client unset")
		return false
	}
	return true
}

func (h handler) requireChatBridge(w http.ResponseWriter) bool {
	if h.chatBridge == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_chat_bridge_unavailable", "hermes chat bridge unset")
		return false
	}
	return true
}

func (h handler) audit(w http.ResponseWriter, r *http.Request, ident sessionauth.Identity, action string, args map[string]any, result string) bool {
	err := h.svc.RecordAudit(r.Context(), ident.TenantID, ident.UserID, action, withAdminActor(r.Context(), args), result, correlationID(r), requestID(r))
	if err != nil {
		writeHermesError(w, err)
		return false
	}
	return true
}

// withAdminActor folds the resolved operator's token id + role into the
// sanitized audit args when the request was authenticated in admin-only mode.
// actor_user_id continues to record the tenant user (so the users FK holds);
// this records WHICH operator performed the action so the audit trail
// attributes the real admin. End-user-path requests are returned unchanged.
// The map is copied so the caller's args (also used in error paths) are not
// mutated, and so the audit's sensitive-key sanitizer still runs over it.
func withAdminActor(ctx context.Context, args map[string]any) map[string]any {
	actor, ok := adminActorFromContext(ctx)
	if !ok {
		return args
	}
	out := make(map[string]any, len(args)+2)
	for k, v := range args {
		out[k] = v
	}
	// Key is admin_actor_id (NOT *_token_id): the hermes audit sanitizer
	// (hermes.SanitizeArgs/sensitiveKey) redacts any key containing "token", and
	// this value is a non-secret admin_tokens row PK that MUST survive into the
	// persisted trail — otherwise operator attribution is silently dropped.
	out["admin_actor_id"] = actor.TokenID
	out["admin_role"] = actor.Role
	return out
}

func (h handler) auditFailureThenError(w http.ResponseWriter, r *http.Request, ident sessionauth.Identity, action string, args map[string]any, err error) {
	if !h.audit(w, r, ident, action, args, hermes.AuditResultFailure) {
		return
	}
	writeHermesError(w, err)
}

func auditFields(r *http.Request, ident sessionauth.Identity, action string, args map[string]any, result string) hermes.AuditFields {
	return hermes.AuditFields{
		TenantID: ident.TenantID, ActorUserID: ident.UserID, Action: action,
		SanitizedArgs: withAdminActor(r.Context(), args), Result: result,
		CorrelationID: correlationID(r), RequestID: requestID(r),
	}
}

func requestID(r *http.Request) string {
	if id := middleware.GetReqID(r.Context()); id != "" {
		return id
	}
	if id := r.Header.Get("X-Request-Id"); id != "" {
		return id
	}
	return r.Header.Get("X-Request-ID")
}

func correlationID(r *http.Request) string {
	if id := strings.TrimSpace(r.Header.Get("X-Correlation-ID")); id != "" {
		return id
	}
	return requestID(r)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":%q}}`, code, message)
}

func writeHermesError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hermes.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "hermes_invalid_input", "invalid hermes request")
	case errors.Is(err, hermes.ErrProfileNotOwned):
		writeError(w, http.StatusForbidden, "hermes_profile_not_owned", "hermes profile is not owned by current user")
	case errors.Is(err, hermes.ErrProfileInUse):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprint(w, `{"error":"profile_in_use","detail":"profile is currently used by settings"}`)
	case errors.Is(err, hermes.ErrForbidden):
		writeError(w, http.StatusForbidden, "hermes_forbidden", "hermes resource is not allowed")
	case errors.Is(err, hermes.ErrNotFound):
		writeError(w, http.StatusNotFound, "hermes_not_found", "hermes resource not found")
	case errors.Is(err, hermes.ErrGone):
		writeError(w, http.StatusGone, "hermes_gone", "hermes resource is no longer available")
	case errors.Is(err, hermes.ErrMisconfigured):
		writeError(w, http.StatusServiceUnavailable, "hermes_service_unavailable", "hermes service unavailable")
	case errors.Is(err, hermes.ErrAuditRecordFailed):
		log.Printf("hermes audit insert failed: %v", err)
		writeError(w, http.StatusServiceUnavailable, "hermes_backend_error", "hermes backend transient failure")
	default:
		writeError(w, http.StatusServiceUnavailable, "hermes_backend_error", "hermes backend transient failure")
	}
}
