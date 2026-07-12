package hermesops

import (
	"context"
	"sort"
)

// roleRank 把 admin 角色从最低到最高权威排序。调用方的 rank >= 工具要求角色的 rank 时
// 才可运行该工具。tenant_operator 是本波次允许的最低 actor;platform_admin 在其之上。
func roleRank(role string) int {
	switch role {
	case RoleTenantOperator:
		return 1
	case RolePlatformAdmin:
		return 2
	default:
		return 0 // 未知 / 未认证角色:低于每一个工具
	}
}

// RoleAllowed 报告 actorRole 是否满足 requiredRole。租户作用域校验
// (CanIssueForTenant)是一个 SEPARATE(独立)权威,由 HTTP 层在 dispatch 前强制执行;
// 本函数只检查角色下限。
func RoleAllowed(actorRole, requiredRole string) bool {
	return roleRank(actorRole) >= roleRank(requiredRole) && roleRank(actorRole) > 0
}

// Registry 持有已注册的诊断工具,并执行 RBAC + dispatch。它在接线时构造一次,之后只读
// (无并发注册),所以请求路径上无需加锁。
type Registry struct {
	tools map[string]ToolSpec
}

// NewRegistry 构造一个空 registry。
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]ToolSpec)}
}

// Register 加入一个 spec。重名会覆盖(后写者胜),与模块 registry 约定一致;接线时每个工具
// 恰好注册一次。
func (r *Registry) Register(spec ToolSpec) {
	if r == nil || spec.Name == "" {
		return
	}
	r.tools[spec.Name] = spec
}

// Get 返回一个已注册的 spec。
func (r *Registry) Get(name string) (ToolSpec, bool) {
	if r == nil {
		return ToolSpec{}, false
	}
	s, ok := r.tools[name]
	return s, ok
}

// List 返回所有 spec,按 name 排序(给 GET /v1/hermes/tools 输出稳定结果)。
func (r *Registry) List() []ToolSpec {
	if r == nil {
		return nil
	}
	out := make([]ToolSpec, 0, len(r.tools))
	for _, s := range r.tools {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Authorize 在 NOT(不)运行工具的前提下检查其角色下限。对未注册的 name 返回 ErrToolUnknown,
// 角色低于工具下限时返回 ErrToolForbidden。HTTP 层会在 Run 之前调用本函数(以及
// CanIssueForTenant),好让一次拒绝被记录成一条 denied 的 tool-call 行。
func (r *Registry) Authorize(name, actorRole string) (ToolSpec, error) {
	spec, ok := r.Get(name)
	if !ok {
		return ToolSpec{}, ErrToolUnknown
	}
	if !RoleAllowed(actorRole, spec.RequiredRole) {
		return spec, ErrToolForbidden
	}
	return spec, nil
}

// Run 先授权,再 dispatch 一个 READ-ONLY(只读)工具。它是唯一的只读 dispatch 入口;
// 永不绕过角色下限,并且会 REFUSE(拒绝)一个 mutating 工具(ErrNotMutating),从而让状态
// 改动永远无法从只读路径偷溜进来。租户作用域授权是调用方的责任(在 Run 之前经
// CanIssueForTenant 强制执行)——Run 信任 req.TenantID 已经过作用域校验。
func (r *Registry) Run(ctx context.Context, name string, req ToolRequest) (ToolResult, error) {
	spec, err := r.Authorize(name, req.Role)
	if err != nil {
		return ToolResult{}, err
	}
	if spec.Mutating {
		// mutating 工具绝不能走只读路径执行——那会绕过 dry-run/confirm + advisory lock +
		// atomic audit。fail-closed(出错即拒)。
		return ToolResult{}, ErrNotMutating
	}
	if spec.Run == nil {
		return ToolResult{}, ErrDependencyUnwired
	}
	return spec.Run(ctx, req)
}

// AuthorizeMutating 在 NOT(不)运行的前提下授权一个 MUTATING 工具的角色下限,并拒绝只读
// 工具(ErrNotMutating),从而让 confirm 路径无法被指向某个诊断工具。租户作用域由调用方
// 单独强制执行(H1 中间件 + 每工具 Resolve 对目标行租户的再次校验)。
func (r *Registry) AuthorizeMutating(name, actorRole string) (ToolSpec, error) {
	spec, err := r.Authorize(name, actorRole)
	if err != nil {
		return ToolSpec{}, err
	}
	if !spec.Mutating {
		return spec, ErrNotMutating
	}
	if spec.Resolve == nil || spec.Mutate == nil {
		return spec, ErrDependencyUnwired
	}
	return spec, nil
}

// ResolveProposal 为 LLM-propose(LLM 提议)路径授权 + 以 DRY-RUN(空跑)方式 resolve 一个
// MUTATING 工具,并且 ONLY(只)返回只读的 MutationPlan。它故意既不返回 ToolSpec、也不返回任何
// 指向 Mutate 的句柄,所以调用方(对话式 internal tool handler)没有任何通往状态改动的路径——
// LLM-propose 路径在 STRUCTURALLY(结构上)就是只读的(门控来自缺少 Mutate 句柄,而非某个调用方
// 可跳过的运行时检查)。真正的 mutation 仅在之后、当 OPERATOR(运营者)经独立的、需运营者认证的
// H1 confirm 路径确认时才运行。
//
// 它会 fail-closed 拒绝:
//   - 只读工具(ErrNotMutating,经 AuthorizeMutating);
//   - 未标记 Proposable 的 mutating 工具(ErrNotProposable)——例如凭证轮换:运营者可直接驱动,
//     但 LLM 永不提议它;
//   - 角色不足(ErrToolForbidden,经角色下限)。
//
// 返回的 plan 携带已脱敏的 Preview + TargetType/TargetID,调用方会把它们钉进一次性的
// correlation_id。租户作用域在 Resolve 内部强制执行(它再次校验目标行属于 req.TenantID)。
func (r *Registry) ResolveProposal(ctx context.Context, name, actorRole string, req ToolRequest) (MutationPlan, error) {
	spec, err := r.AuthorizeMutating(name, actorRole)
	if err != nil {
		return MutationPlan{}, err
	}
	if !spec.Proposable {
		return MutationPlan{}, ErrNotProposable
	}
	// 仅做 READ-ONLY 的 dry-run。本路径绝不引用 spec.Mutate;状态改动只在运营者确认时、
	// 经另一个入口发生。
	return spec.Resolve(ctx, req)
}
