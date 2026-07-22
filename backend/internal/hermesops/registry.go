package hermesops

import (
	"context"
	"fmt"
	"sort"
	"strings"
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
	tools           map[string]ToolSpec
	registrationErr error
}

// NewRegistry 构造一个空 registry。
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]ToolSpec)}
}

// Register 加入一个 spec。重名会覆盖(后写者胜),与模块 registry 约定一致;接线时每个工具
// 恰好注册一次。
func (r *Registry) Register(spec ToolSpec) error {
	if r == nil {
		return fmt.Errorf("%w: 注册表为空", ErrInvalidToolSpec)
	}
	spec.Name = strings.TrimSpace(spec.Name)
	if err := ValidateToolSpec(spec); err != nil {
		r.rememberRegistrationError(err)
		return err
	}
	if _, exists := r.tools[spec.Name]; exists {
		err := fmt.Errorf("%w: %s", ErrDuplicateTool, spec.Name)
		r.rememberRegistrationError(err)
		return err
	}
	r.tools[spec.Name] = spec
	return nil
}

func (r *Registry) rememberRegistrationError(err error) {
	if r.registrationErr == nil {
		r.registrationErr = err
	}
}

// Validate 把注册阶段捕获的首个错误交给组合根，使网关在监听端口前失败。
func (r *Registry) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: 注册表为空", ErrInvalidToolSpec)
	}
	return r.registrationErr
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

// Run 先授权，再派发一个只读工具。它是唯一的只读派发入口，不绕过角色下限，
// 并拒绝改动型工具，防止状态变更进入只读路径。调用方必须先用 CanIssueForTenant
// 校验租户作用域，Run 信任 req.TenantID 已完成该校验。
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

// AuthorizeMutating 只校验改动型工具的角色下限，不执行工具，并拒绝只读工具，
// 防止确认路径误指向诊断工具。调用方先校验身份租户作用域，各工具的 Resolve 再复核目标行租户。
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

// ResolveProposal 为模型提议路径授权，并以空跑方式解析改动型工具，只返回只读的 MutationPlan。
// 它不暴露 ToolSpec 或 Mutate 句柄，因此调用方在结构上无法改动状态。真正的变更只能由运营者
// 通过独立认证的确认入口触发。
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
	// 仅做只读空跑。本路径不引用 spec.Mutate，状态改动只在运营者确认时、
	// 经另一个入口发生。
	return spec.Resolve(ctx, req)
}
