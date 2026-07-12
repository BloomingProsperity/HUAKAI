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
	"github.com/BloomingProsperity/HUAKAI/internal/hermesconfirm"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops/mutateguard"
	"github.com/BloomingProsperity/HUAKAI/internal/modulehttp"
)

type AuthResolver interface {
	Resolve(context.Context, *http.Request) (sessionauth.Identity, error)
}

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
	// mutator + confirmCache 支撑 WAVE H4 的 mutating 工具路径。当 mutator 为
	// nil 时,mutating 工具被拒绝(503)——只读路径不受影响。
	// confirmCache 现为 hermesconfirm 共享单例(可经 RouterDeps 注入,以便未来 Phase B 的 LLM 提议侧
	// 与 operator 确认侧共用同一实例);未注入时本构造回退新建一个(行为同旧 newConfirmCache())。
	mutator      MutateOrchestrator
	confirmCache *hermesconfirm.Cache
	// mutatingDisabled 是 KNOB A 的取反值:针对所有 mutating 工具的运行时 kill-switch。
	// 以「禁用」形式存储,这样 handler 的 0 值即表示「启用」(当前行为)——直接构造
	// handler{} 的白盒 handler 测试无需任何设置即可让 mutating 路径保持可用。
	// NewRouterWithDeps 把 RouterDeps.MutatingEnabled(接线层默认 true)映射成这里的
	// !MutatingEnabled。为 true 时,executeTool 会在 previewMutation/confirmMutation
	// 之前拒绝 mutating 分支(403 hermes_mutating_disabled)并记录一条 denied 行;
	// 只读诊断路径不受影响。
	mutatingDisabled bool
	// mutateRateLimiter 是 S2 (c) 的 per-operator-token 滑动窗口限流器。它在
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
	// Tools、ToolCalls 与 ContextSource 接入 WAVE H3 的只读 ops 主干。
	// 三者均可选:未设置时,tool/context 路由返回 503(service unavailable)
	// 而非 panic。
	Tools         ToolRegistry
	ToolCalls     hermesops.ToolCallInserter
	ContextSource ContextSource
	// Mutator 接入 WAVE H4 的 mutating 工具路径(atomic-audit + advisory-lock
	// orchestrator)。可选:未设置时,mutating 工具被拒绝(503),只读路径不受影响。
	Mutator MutateOrchestrator
	// MutatingEnabled 是 KNOB A:针对所有 mutating 工具的运行时 kill-switch。
	// gateway 接线层从 HUAKAI_HERMES_MUTATING_ENABLED 取值(默认 true)。为 false 时,
	// handler 拒绝 tool-execute 的 mutating 分支(403 hermes_mutating_disabled,记录
	// denial),而只读路径 + chat 仍保持可用。注意:为接线层表达清晰,该字段以「启用」
	// 形式命名;handler 存储其取反值,这样 0 值 handler{}(测试里直接构造)默认保持
	// mutation 启用。
	MutatingEnabled bool
	// ConfirmCache 是 dry-run→confirm 的共享 correlation-id 存储。可选:为 nil 时,
	// NewRouterWithDeps 会为每个 router 新建一个独立实例(与旧行为完全一致)。gateway
	// 接线层注入一个进程级单例,使(未来 Phase B 的)LLM 提议侧与本 operator 确认侧
	// 共用同一份缓存——一次提议发出的 correlation_id 可被 operator confirm 消费。
	// nil 缓存始终 fail-closed(confirm → 400,绝不执行)。
	ConfirmCache *hermesconfirm.Cache
	// MutateGuard 是可选的 S2「为 mutating 路径设界」组合。其 handler 侧的部分是
	// per-operator-token 限流器;并发信号量 + tx deadline 在接线时施加于 orchestrator。
	// gateway 接线层仅在 admin-only mutator 路径激活时设置它;为 nil 时,handler 的
	// 限流器是「禁用」哨兵——与旧行为逐字节一致。
	MutateGuard *MutateGuardDeps
}

// MutateGuardDeps 携带 S2 mutating 路径设界中 handler 侧的部分:per-operator-token
// 限流器。并发信号量 + tx deadline 在接线时施加于 orchestrator(NewMutateOrchestrator
// 选项),而非这里。nil 的 RateLimiter(或一个被禁用的)即旧的无界行为。
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
		// 注入共享 Cache;未注入则回退新建(行为同旧 newConfirmCache(),保证 NewRouter()/白盒测试不变)。
		confirmCache: d.ConfirmCache,
		// KNOB A:存储取反值,使 0 值 handler{}(白盒测试)默认保持 mutation 启用;
		// 接线层传入 MutatingEnabled(默认 true)。
		mutatingDisabled: !d.MutatingEnabled,
	}
	// 未注入共享 Cache 时回退新建一个(行为同旧 newConfirmCache():每路由一份),保证 NewRouter()
	// 与白盒 handler 测试无需注入即可工作。
	if h.confirmCache == nil {
		h.confirmCache = hermesconfirm.NewCache()
	}
	// S2 (c):当 guard 组合被接入时,启用 per-operator-token 限流器。
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
	r.Delete("/api-profiles/{id}", h.deleteProfile)
	r.Post("/chat", h.startChat)
	r.Get("/conversations", h.listConversations)
	r.Get("/conversations/{id}", h.getConversation)
	r.Delete("/conversations/{id}", h.deleteConversation)
	r.Get("/conversations/{id}/messages", h.listConversationMessages)
	// WAVE H3 只读 ops 主干 + WAVE H4 mutating ops 工具。tool-execute 直接派发只读
	// 工具,而把 mutating 工具引导经过 dry-run + confirm 流程(confirm=false => preview,
	// confirm=true => execute)。
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

// withAdminActor 在请求以 admin-only 模式完成认证时,把解析出的 operator token id
// + role 折入已脱敏的审计 args。actor_user_id 仍记录 tenant user(以保证 users FK
// 成立);本函数记录是哪个 operator 执行了该动作,使审计轨迹能归因到真正的 admin。
// 终端用户路径的请求原样返回。该 map 会被复制,以免调用方的 args(也用于错误路径)
// 被改动,同时也让审计的敏感键脱敏器仍能作用其上。
func withAdminActor(ctx context.Context, args map[string]any) map[string]any {
	actor, ok := adminActorFromContext(ctx)
	if !ok {
		return args
	}
	out := make(map[string]any, len(args)+2)
	for k, v := range args {
		out[k] = v
	}
	// 键名用 admin_actor_id(而非 *_token_id):hermes 审计脱敏器
	// (hermes.SanitizeArgs/sensitiveKey)会对任何含 "token" 的键脱敏,而这个值是
	// 非机密的 admin_tokens 行 PK,必须保留进入持久化轨迹——否则 operator 归因会被
	// 静默丢弃。
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
