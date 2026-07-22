// Package hermeshttp 暴露部署者与获授权租户管理员使用的 Hermes 运维端点。
package hermeshttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/BloomingProsperity/HUAKAI/internal/hermesconfirm"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops/mutateguard"
	"github.com/BloomingProsperity/HUAKAI/internal/modulehttp"
)

type authContextKey struct{}

// ToolRegistry 是 tool-execute handler 派发到的 ops 工具注册表。
// *hermesops.Registry 实现了它。它同时覆盖只读诊断派发(Run)与 confirm 门控
// 变更路径所用的 mutating 工具授权(AuthorizeMutating)。
type ToolRegistry interface {
	List() []hermesops.ToolSpec
	Get(name string) (hermesops.ToolSpec, bool)
	Authorize(name, actorRole string) (hermesops.ToolSpec, error)
	AuthorizeMutating(name, actorRole string) (hermesops.ToolSpec, error)
	Run(ctx context.Context, name string, req hermesops.ToolRequest) (hermesops.ToolResult, error)
}

// MutateOrchestrator 在 atomic-audit + advisory-lock 事务下运行一个已确认的
// mutating 工具。*hermesops.MutateOrchestrator 实现了它。保留为接口,以便 mutate
// handler 可用 fake 做单元测试。
type MutateOrchestrator interface {
	Execute(ctx context.Context, lockKey string, rec hermesops.MutationAuditRecord, mutate func(ctx context.Context, tx pgx.Tx) (hermesops.ToolResult, error)) (hermesops.ToolResult, error)
}

// ContextSource 为 GET /v1/hermes/context 提供合并后的 module-knowledge 视图。
// *cmdgateway 的 moduleSource(一个 modulehttp.Source)由接线层包装;handler 只需
// 这个合并访问器。
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
	// mutator + confirmStore 支撑改动型工具路径。当 mutator 为
	// nil 时,mutating 工具被拒绝(503)——只读路径不受影响。
	// 生产注入跨副本共享存储；未注入时只为纯单元测试回退到进程内实现。
	mutator      MutateOrchestrator
	confirmStore hermesconfirm.Store
	// mutatingDisabled 是所有改动型工具运行时总开关的取反值。
	// 以「禁用」形式存储,这样 handler 的 0 值即表示「启用」(当前行为)——直接构造
	// handler{} 的白盒 handler 测试无需任何设置即可让 mutating 路径保持可用。
	// NewRouterWithDeps 把 RouterDeps.MutatingEnabled(接线层默认 true)映射成这里的
	// !MutatingEnabled。为 true 时,executeTool 会在 previewMutation/confirmMutation
	// 之前拒绝 mutating 分支(403 hermes_mutating_disabled)并记录一条 denied 行;
	// 只读诊断路径不受影响。
	mutatingDisabled bool
	// mutateRateLimiter 是按管理员身份执行的滑动窗口限流器。它在
	// confirmMutation 里、correlation-id 消费之后、Execute 之前被检查,因此只有真正
	// 已确认的执行才计数(preview/denial 不计)。nil 限流器(handler 的 0 值,也是测试
	// 默认构造的形态)是「禁用」哨兵——每次 confirm 都放行,与旧行为逐字节一致。
	mutateRateLimiter *mutateguard.RateLimiter
}

type RouterDeps struct {
	Service        *hermes.Service
	Runner         *hermes.RunnerClient
	Bridge         *hermeschat.Bridge
	HeaderSettings headerfirewall.PlatformSettings
	// Tools、ToolCalls 与 ContextSource 接入只读运维工具主干。
	// 三者均可选:未设置时,tool/context 路由返回 503(service unavailable)
	// 而非 panic。
	Tools         ToolRegistry
	ToolCalls     hermesops.ToolCallInserter
	ContextSource ContextSource
	// Mutator 接入带原子日志和 advisory lock 的改动型工具路径。未设置时，
	// 改动型工具返回 503，只读路径不受影响。
	Mutator MutateOrchestrator
	// MutatingEnabled 是所有改动型工具的运行时总开关。
	// gateway 接线层从 HUAKAI_HERMES_MUTATING_ENABLED 取值(默认 true)。为 false 时,
	// handler 拒绝 tool-execute 的 mutating 分支(403 hermes_mutating_disabled,记录
	// denial),而只读路径 + chat 仍保持可用。注意:为接线层表达清晰,该字段以「启用」
	// 形式命名;handler 存储其取反值,这样 0 值 handler{}(测试里直接构造)默认保持
	// mutation 启用。
	MutatingEnabled bool
	// ConfirmStore 是提议与人工确认共用的跨副本单次消费存储。
	ConfirmStore hermesconfirm.Store
	// MutateGuard 是改动路径的可选资源保护组合。处理器侧按管理员身份限流，
	// 并发信号量和事务期限由接线层施加于编排器。为 nil 时不启用限流。
	MutateGuard *MutateGuardDeps
}

// MutateGuardDeps 携带改动路径在处理器侧使用的按管理员身份限流器。
type MutateGuardDeps struct {
	RateLimiter *mutateguard.RateLimiter
}

func NewRouter(svc *hermes.Service, runnerClient *hermes.RunnerClient, bridges ...*hermeschat.Bridge) http.Handler {
	var bridge *hermeschat.Bridge
	if len(bridges) > 0 {
		bridge = bridges[0]
	}
	return NewRouterWithDeps(RouterDeps{Service: svc, Runner: runnerClient, Bridge: bridge, MutatingEnabled: true})
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
		confirmStore:   d.ConfirmStore,
		// 存储取反值，使 0 值 handler{} 在白盒测试中默认保持改动路径启用；
		// 接线层传入 MutatingEnabled(默认 true)。
		mutatingDisabled: !d.MutatingEnabled,
	}
	// 仅纯单元测试未注入数据库时回退到每路由一份内存存储。
	if h.confirmStore == nil {
		h.confirmStore = hermesconfirm.NewCache()
	}
	// 接入保护组合时启用按管理员身份限流器。
	// nil 组合(或 nil 限流器)会让 h.mutateRateLimiter 保持 nil = 禁用。
	if d.MutateGuard != nil {
		h.mutateRateLimiter = d.MutateGuard.RateLimiter
	}
	r := chi.NewRouter()
	r.Get("/settings", h.getSettings)
	r.Post("/settings/enable", h.enableSettings)
	r.Post("/settings/disable", h.disableSettings)
	r.Post("/api-profiles", h.createProfile)
	r.Get("/api-profiles", h.listProfiles)
	r.Get("/api-profiles/{id}", h.getProfile)
	r.Put("/api-profiles/{id}", h.rotateProfile)
	r.Delete("/api-profiles/{id}", h.deleteProfile)
	r.Post("/chat", h.startChat)
	r.Get("/conversations", h.listConversations)
	r.Get("/conversations/{id}", h.getConversation)
	r.Delete("/conversations/{id}", h.deleteConversation)
	r.Get("/conversations/{id}/messages", h.listConversationMessages)
	// tool-execute 直接派发只读工具，并把改动型工具引导经过预览和人工确认流程。
	r.Get("/tools", h.listTools)
	r.Post("/tool-execute", h.executeTool)
	r.Get("/context", h.getModuleContext)
	return r
}

func (h handler) requireIdentity(w http.ResponseWriter, r *http.Request) (sessionauth.Identity, bool) {
	if h.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_service_unavailable", "hermes service unset")
		return sessionauth.Identity{}, false
	}
	ident, ok := r.Context().Value(authContextKey{}).(sessionauth.Identity)
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "hermes_unauthorized", "hermes administrator identity is required")
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
	err := h.svc.RecordAudit(r.Context(), auditFields(r, ident, action, args, result))
	if err != nil {
		writeHermesError(w, err)
		return false
	}
	return true
}

func (h handler) auditFailureThenError(w http.ResponseWriter, r *http.Request, ident sessionauth.Identity, action string, args map[string]any, err error) {
	if !h.audit(w, r, ident, action, args, hermes.AuditResultFailure) {
		return
	}
	writeHermesError(w, err)
}

func auditFields(r *http.Request, ident sessionauth.Identity, action string, args map[string]any, result string) hermes.AuditFields {
	actor, _ := adminActorFromContext(r.Context())
	return hermes.AuditFields{
		TenantID: ident.TenantID, ActorSource: actor.Source, ActorID: actor.ID, ActorRole: actor.Role, Action: action,
		SanitizedArgs: args, Result: result,
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
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON value")
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
	case errors.Is(err, hermes.ErrConflict):
		writeError(w, http.StatusConflict, "hermes_conflict", "hermes resource changed concurrently")
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
