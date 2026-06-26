package hermesops

import (
	"context"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

// ModelResolveDiagnoseDeps 是 model_resolve_diagnose 工具的只读依赖:Resolve 包装 registry 的
// ResolveModel(REPEATABLE READ + read-only TX 的只读解析,不写任何状态)。Resolve 为 nil 时工具
// 按依赖检查 fail-closed。
type ModelResolveDiagnoseDeps struct {
	Resolve func(ctx context.Context, publicAlias string, tenantID int64) (registry.Resolved, error)
}

// ModelResolveDiagnoseSpec 构建只读 model_resolve_diagnose 工具:回答"模型 X 在本租户怎么路由的"——
// 别名解析成哪个 canonical/上游模型、落到哪些 pool group、各 binding 的优先级/权重/选号模式/限流/
// fallback,以及能力/上下文窗口/协议族。
//
// 隐私 safe-by-construction(见 modelResolveShape):registry.Resolved 的 BindingMetadata 夹带
// **自由文本运营配置**——SystemPrompt(整段系统提示词)、SensitiveWords(审核关键字 denylist)、
// ParamOverride(参数覆盖值)、StatusCodeMapping 等。这些**绝不投影**:SystemPrompt/SensitiveWords/
// ParamOverride/BodyParamStrips 只降级为存在标记或计数,其余只露受控枚举/数值/ids。这样即便未来给
// BindingMetadata 新增字段也不会自动泄露(新字段除非是枚举/数值才显式列入)。
//
// 租户 scope 取自已鉴权的 req.TenantID;ResolveModel 内部所有读都按该 tenantID 过滤(别名/模型/
// binding 三层 tenant-scoped),故不会跨租户泄露路由拓扑。
//
// Args: { "model": <string, required 公开模型别名> }
func ModelResolveDiagnoseSpec(deps ModelResolveDiagnoseDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolModelResolveDiagnose,
		Category:     CategoryDiagnostic,
		Description:  "Diagnose how a public model alias resolves & routes within the caller's tenant: target canonical/provider model, which upstream pool groups it binds to, each binding's priority/weight/selection_mode/rpm-tpm limits/fallback, plus capabilities/context_window/protocol_family. READ ONLY. Never returns system prompts, sensitive-word lists, or param-override values.",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema: map[string]string{
			"model": "public model alias to diagnose (e.g. claude-3-5-sonnet, required)",
		},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.Resolve == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			alias, ok := ArgString(req.Args, "model")
			if !ok {
				return ToolResult{}, ErrInvalidArgs
			}
			resolved, err := deps.Resolve(ctx, alias, req.TenantID)
			if err != nil {
				// registry 的 anti-enum 错误族(unknown/disabled/no-access)统一归一成工具层的
				// "未解析",不区分原因、不泄露枚举信号——与 ResolveModel 自身把三态收敛成同一对外
				// 表象的设计一致。后端 datastore 故障(ErrRegistryBackend)等非预期错误才上抛。
				if class, isResolveMiss := modelResolveMissClass(err); isResolveMiss {
					return ToolResult{
						Summary: map[string]any{
							"model":    alias,
							"resolved": false,
						},
						ErrorClass: class,
					}, nil
				}
				return ToolResult{}, err
			}
			return ToolResult{Summary: modelResolveShape(alias, resolved)}, nil
		},
	}
}

// modelResolveMissClass 判断 err 是否属于 registry 的"解析不到/不可用"错误族——unknown(别名不存在)/
// disabled(别名或模型被禁)/no-eligible-binding(无可用 pool binding)。registry 把这三态**有意收敛成
// 同一对外表象**以防枚举(internal/registry/errors.go:"ErrModelDisabled -> 404 model_not_available
// (uniform per D4 anti-enum)")。本工具**遵循该 anti-enum 不变量**:三态统一归一成单一非 PII 枚举
// "model_not_available",绝不区分原因、不泄露"模型存在但被禁 vs 完全不存在"之类的枚举信号。返回
// (枚举, true) 表示预期解析缺失(工具返回 resolved=false 而非报错);其它错误(如 ErrRegistryBackend
// 后端故障)返回 ("", false) 交由调用方上抛。
func modelResolveMissClass(err error) (string, bool) {
	switch {
	case errors.Is(err, registry.ErrUnknownModel),
		errors.Is(err, registry.ErrModelDisabled),
		errors.Is(err, registry.ErrTenantNoAccess):
		return "model_not_available", true
	default:
		return "", false
	}
}

// modelResolveShape 把 registry.Resolved 投影成纯路由诊断字段。**显式列举 + safe-by-construction**:
// 只露 enum/数值/ids/计数,绝不 echo 整个 struct,**绝不露 binding 上的任何自由文本运营配置**
// (SystemPrompt/SensitiveWords/ParamOverride/BodyParamStrips/StatusCodeMapping)——这些降级为
// 存在标记或计数,诊断信号由路由结构(pool/优先级/选号/限流/fallback)与受控枚举已充分传达。
func modelResolveShape(alias string, r registry.Resolved) map[string]any {
	bindings := make([]map[string]any, 0, len(r.BindingMetadata))
	for _, b := range r.BindingMetadata {
		bindings = append(bindings, modelResolveBindingShape(b))
	}
	return map[string]any{
		"model":              alias,
		"resolved":           true,
		"public_alias":       r.PublicAlias,
		"canonical_model":    r.CanonicalModelID,
		"provider_model":     r.ProviderModelID,
		"context_window":     r.ContextWindow,
		"pricing_class":      r.PricingClass,
		"protocol_family":    r.ProtocolFamily,
		"request_timeout_ms": r.RequestTimeoutMS,
		"capabilities":       capabilitiesShape(r.Capabilities),
		"pool_candidates":    r.PoolCandidates,
		"binding_count":      len(r.BindingMetadata),
		"bindings":           bindings,
		"snapshot_version":   r.SnapshotVersion,
	}
}

// modelResolveBindingShape 投影单条 binding 的路由结构。自由文本/业务逻辑字段
// (SystemPrompt/SensitiveWords/ParamOverride/BodyParamStrips/StatusCodeMapping/SystemPromptOverride)
// 一律**不露明文值**:SystemPrompt 只降级为 has_system_prompt(是否配置了系统提示词,路由-transform
// 核心事实、非文本),其余只露计数。SystemPromptOverride 等纯 transform 语义标记不投影(边际诊断价值低,
// 保守起见不露)。上游模型重命名(ProviderModelIDOverride)是模型标识符(非密钥)且为路由诊断核心信息,
// 显式露出。
func modelResolveBindingShape(b registry.BindingMetadata) map[string]any {
	out := map[string]any{
		"binding_id":            b.BindingID,
		"pool_group_id":         b.PoolGroupID,
		"priority":              b.Priority,
		"weight":                b.Weight,
		"selection_mode":        b.SelectionMode,
		"fallback_class":        b.FallbackClass,
		"rpm_limit":             int32PtrAny(b.RPMLimit),
		"tpm_limit":             int32PtrAny(b.TPMLimit),
		"max_parallel_requests": int32PtrAny(b.MaxParallelRequests),
		"force_format":          b.ForceFormat,
		// 自由文本/业务逻辑配置:只露"是否存在"或计数,绝不露明文。
		"has_system_prompt":      b.SystemPrompt != "",
		"sensitive_word_count":   len(b.SensitiveWords),
		"param_override_count":   len(b.ParamOverride),
		"body_param_strip_count": len(b.BodyParamStrips),
	}
	if b.ProviderModelIDOverride != nil && *b.ProviderModelIDOverride != "" {
		out["provider_model_rename"] = *b.ProviderModelIDOverride
	}
	return out
}

// capabilitiesShape 把能力列表原样返回(已是受控枚举字符串,如 "vision"/"tools");nil 归一成空切片
// 保证 JSON 形态稳定。
func capabilitiesShape(caps []string) []string {
	if caps == nil {
		return []string{}
	}
	return caps
}
