// Package hermesops 是受 admin 门控的 Hermes 运维助手的、由网关中介的工具执行主干
// (WAVE H3 只读 + WAVE H4 mutating)。
//
// 它暴露一个工具 registry,这些工具包裹 EXISTING(既有)的网关函数,好让运营者
// (以及之后的助手 LLM)能通过单一的受审计端点运行根因诊断 AND(并)施加修复:
//   - READ-ONLY 诊断工具(H3):MUST NOT(绝不能)改动状态;经 Run dispatch。
//   - MUTATING 运维工具(H4):replay / pause / resume / renew。每个都把一项既有的 mutation
//     包在五层安全契约后面(RBAC 下限、dry-run + confirm、atomic audit、advisory lock、
//     幂等),并且只经 confirm 门控的 mutate 路径 dispatch——NEVER(绝不)经 Run。
//
// 设计:
//   - ToolSpec 声明一个工具的身份、类别、最低要求角色,以及一个 Run(只读)或一对
//     Resolve + Mutate(mutating)。
//   - Registry 持有这些 spec 并执行 RBAC + dispatch。它 fail-closed:未知工具被拒,依赖
//     未接线的工具返回 error(永不 panic),角色不足的调用方被拒,且 mutating 工具永远无法
//     走只读 Run 路径运行(反之亦然)。
//   - MutateOrchestrator(mutate_tx.go)拥有那个单一事务,它在每目标 advisory lock(L4)下
//     把 mutation 与其 hermes_tool_calls + admin_audit_events 行(L3)绑在一起。
//   - 隐私:一份工具结果 ONLY(只)携带枚举 / 计数 / id / 指纹 / 状态名——绝不携带 prompt、
//     completion、原始 body、密钥、PII 或轮换后的凭证材料。持久化层还会作为纵深防御,把 args
//     与 summary 再过一遍 hermes sanitizer。
package hermesops

import (
	"context"
	"errors"
)

// 工具名。这些是权威标识符;它们 MUST(必须)与 hermes_tool_calls.tool_name 的 CHECK 列表
// 以及 hermes.tool.<name> 审计动作匹配。H4 mutating 工具通过 DROP+ADD 迁移加入新名字。
const (
	ToolCredentialDiagnose    = "credential_diagnose"
	ToolAccountHealthDiagnose = "account_health_diagnose"
	ToolRequestDiagnose       = "request_diagnose"
	ToolDLQInspect            = "dlq_inspect"
	ToolAuditLookup           = "audit_lookup"
	ToolLogAnalyze            = "log_analyze"
	// ToolChannelHealthList(0152 迁移准入)是逐通道健康列表的只读诊断工具,租户级、可按 state 过滤,
	// 补 account_health_diagnose(单账号)缺的"跨账号看哪些通道不健康"。只读 → 仅写 hermes_tool_calls。
	ToolChannelHealthList = "channel_health_list"

	// ToolModelResolveDiagnose(0153 迁移准入)诊断一个公开模型别名在调用方租户内的路由解析:
	// 落到哪些上游池(pool group)、各 binding 的优先级/权重/选号模式/限流/fallback,以及该模型的
	// 能力/上下文窗口/协议族。只读 → 仅写 hermes_tool_calls。投影 safe-by-construction:绝不露 binding
	// 上的 SystemPrompt/SensitiveWords/ParamOverride 等自由文本运营配置(只降级为存在标记/计数)。
	ToolModelResolveDiagnose = "model_resolve_diagnose"

	// ToolPoolList(0154 迁移准入)列出本租户的 pool group(路由池)及其配置:名称、路由策略版本、
	// top-k/能力默认、各类等待/超时/限流参数、启用状态。补全 model_resolve_diagnose 开的"模型→池→账号"
	// 路由拓扑里"池本身有哪些、怎么配的"一环。只读 → 仅写 hermes_tool_calls。PoolGroup 全是结构化配置、
	// 无自由文本/PII(Name 是运营自取的池标签)。
	ToolPoolList = "pool_list"

	// ToolProviderAccountList(0155 迁移准入)列出本租户的上游 provider account 清册(整租户俯瞰,可按
	// state 过滤),补 account_health_diagnose(单账号)缺的"我有哪些账号、各自启用/健康/凭证/限流状态、
	// 路由权重/容量"。只读 → 仅写 hermes_tool_calls。投影 safe-by-construction:绝不露 Extra(原始 JSON
	// blob)、RateLimitReason(可能夹带上游错误文本)、Tags 值(运营自由标签→只露 count)、ProxyGroupID 文本;
	// 且**绝无凭证/token 明文**(原始凭证存 credentialstore,本行根本不含)。
	ToolProviderAccountList = "provider_account_list"

	// ToolQuotaPolicyList(0156 迁移准入)列出本租户的配额策略(quota policy)及其配置:scope/metric/
	// 窗口/限额/burst/模式/优先级/启用/生效区间。让 Hermes 能回答"我的配额怎么配的、对谁、限多少"。只读 →
	// 仅写 hermes_tool_calls。QuotaPolicy 全是结构化配置;投影排 CreatedByActor/LastModifiedByActor
	// (actor 标识)与 TenantID。
	ToolQuotaPolicyList = "quota_policy_list"

	// ToolAlertRuleList(0157 迁移准入)列出本租户的告警规则(alert rule)及其配置:名称、metric、
	// 比较器/阈值、严重度、窗口/持续/冷却秒、是否邮件通知、filters(运营自定义的过滤标签)、是否启用、
	// 上次触发时间。让 Hermes 能回答"我配了哪些告警、对什么 metric、阈值多少、上次什么时候触发"。只读 →
	// 仅写 hermes_tool_calls。AlertRule 全是结构化配置;Filters 是运营自填的规则过滤标签(规则定义的一部分,
	// 非用户内容);投影排 TenantID。
	ToolAlertRuleList = "alert_rule_list"

	// ToolAlertEventList(0158 迁移准入)列出本租户的告警事件(alert event,可按 state 过滤):规则 id、
	// 状态(firing/resolved/manual_resolved)、观测值/阈值/指标值、dimensions(触发时的规则过滤标签)、
	// 触发/解决时间、是否已发邮件。补 alert_rule_list(规则配置)缺的"实际触发了什么"。让 Hermes 能回答
	// "现在有什么告警在响、最近触发过什么"。只读 → 仅写 hermes_tool_calls。Dimensions 同 AlertRule.Filters
	// 来源(规则过滤标签,运营自填);投影排 TenantID。
	ToolAlertEventList = "alert_event_list"

	// ToolProviderCatalogList(0159 迁移准入)列出本租户的上游供应商目录(provider catalog):code、
	// 显示名、上游协议、启用、创建时间。让 Hermes 能回答"我接了哪些供应商类型"。只读 → 仅写
	// hermes_tool_calls。Row 全是结构化目录数据(无 PII/密钥,连 TenantID 都不含)。
	ToolProviderCatalogList = "provider_catalog_list"

	// ToolChannelCatalogList(0159 迁移准入)列出本租户的渠道目录(channel catalog):所属 pool group、
	// 名称、failover 状态码、启用、创建时间。让 Hermes 能回答"我定义了哪些渠道、挂在哪个池"。只读 → 仅写
	// hermes_tool_calls。Row 全是结构化目录数据(无 PII/密钥)。
	ToolChannelCatalogList = "channel_catalog_list"

	// WAVE H4 MUTATING(可变更)工具名。每个都把一项 EXISTING(既有)的 admin mutation 包在
	// 五层安全契约后面(RBAC、dry-run+confirm、atomic audit、advisory lock、幂等)。它们以
	// Mutating=true 注册,所以只读 dispatch 路径永远无法运行它们。
	ToolDLQReplay     = "dlq_replay"
	ToolAccountPause  = "account_pause"
	ToolAccountResume = "account_resume"
	ToolRenewTrigger  = "renew_trigger"

	// ToolAlertRuleEnable / ToolAlertRuleDisable(0160 迁移准入)是 Phase B"扩可提议覆盖面"
	// 的首批新增 mutating 工具:启用/禁用本租户的一条告警规则(alert rule)。它们是**可逆的 B 级
	// 运营操作**(翻 enabled 列,随时可翻回),因此 Proposable=true —— LLM 可在对话里提议,但
	// RequiresConfirmation=true 意味着仍需 operator 一键确认才真正执行,LLM 绝不能直接执行。
	// 与 0146 的四个 mutating 工具一样,经 confirm 门控的 mutate 路径 + orchestrator 原子运行
	// (规则翻转与 hermes_tool_calls + admin_audit_events 行在同一事务内提交)。
	ToolAlertRuleEnable  = "alert_rule_enable"
	ToolAlertRuleDisable = "alert_rule_disable"

	// ToolModerationKeywordEnable / ToolModerationKeywordDisable(0161 迁移准入)继续 Phase B
	// "扩可提议覆盖面":启用/禁用本租户的一条内容审核关键词规则(moderation keyword)。它们是
	// **安全敏感但可逆的 B 级运营操作** —— disable 等于临时关掉一个内容过滤器,enable 再开回来,
	// 翻 enabled 列、随时可翻回,因此 Proposable=true(LLM 可在对话里提议);但 RequiresConfirmation=true
	// 意味着仍需 operator 一键确认才真正执行,LLM 绝不能直接执行。与 alert_rule_enable/disable 同构:
	// 经 confirm 门控的 mutate 路径 + orchestrator 原子运行(关键词翻转与 hermes_tool_calls +
	// admin_audit_events 行在同一事务内提交),且只对未软删(deleted_at IS NULL)的关键词 toggle。
	ToolModerationKeywordEnable  = "moderation_keyword_enable"
	ToolModerationKeywordDisable = "moderation_keyword_disable"
)

// Roles 镜像 internal/admin 的角色标识符。保留为本地常量,这样本包就不必为两个字符串去 import
// admin 包(RBAC 校验本身由调用方经 admin.AdminIdentity.CanActOnTenant 执行,本包永不绕过它)。
const (
	RolePlatformAdmin  = "platform_admin"
	RoleTenantOperator = "tenant_operator"
)

// Category 给工具分组用于列表/UX。H3 工具是诊断读;H4 增加了 mutating 运维工具("修复"能力)。
type Category string

const (
	CategoryDiagnostic Category = "diagnostic"
	CategoryMutating   Category = "mutating"
)

// ResultStatus 是持久化的 hermes_tool_calls.result_status 枚举。
type ResultStatus string

const (
	ResultOK     ResultStatus = "ok"
	ResultError  ResultStatus = "error"
	ResultDenied ResultStatus = "denied"
)

// 哨兵 error。工具与 registry 返回这些,好让 HTTP 层无需字符串匹配即可把它们映射到状态码。
var (
	// ErrToolUnknown 在工具名未注册时返回。
	ErrToolUnknown = errors.New("hermesops: unknown tool")
	// ErrToolForbidden 在调用方角色低于工具最低要求角色时返回。租户作用域的拒绝由调用方经
	// CanActOnTenant 单独强制执行,也会同样呈现为一条 denied 行。
	ErrToolForbidden = errors.New("hermesops: tool forbidden for role")
	// ErrDependencyUnwired 由其底层读依赖为 nil 的工具返回。工具 MUST(必须)以此 fail-closed
	// 而非 panic。
	ErrDependencyUnwired = errors.New("hermesops: tool dependency unwired")
	// ErrInvalidArgs 在工具参数格式错误 / 缺少必填项时返回。
	ErrInvalidArgs = errors.New("hermesops: invalid tool args")
	// ErrNotMutating 在 mutate/preview 路径被要求运行一个只读工具(或反之)时返回,这样 mutation
	// 永远无法从只读 dispatch 偷溜进来,只读工具也永远无法到达 confirm 路径。
	ErrNotMutating = errors.New("hermesops: tool is not mutating")
	// ErrNotProposable 在 LLM-propose 路径被要求 resolve 一个未标记 Proposable 的 mutating 工具
	// (不可逆 / A 级,例如凭证轮换)时返回。这类工具仍可由 OPERATOR(运营者)经 H1 confirm 路径
	// 驱动;但 LLM 永不提议它。区别于 ErrNotMutating(只读工具)与 ErrToolForbidden(角色不足)。
	ErrNotProposable = errors.New("hermesops: tool is not LLM-proposable")
	// ErrTargetResolution 在 mutating 工具无法 resolve 其目标(租户缺失/异租户、账号未找到)时返回。
	// 它区别于 ErrInvalidArgs,好让 HTTP 层把它映射到 404/403 而非 400。
	ErrTargetResolution = errors.New("hermesops: target resolution failed")
)

// ToolRequest 是交给工具 Run 的、已 resolve 且已授权的调用上下文。TenantID 是由中间件推导、
// 经作用域校验的租户;HTTP 层保证在调用 Run 之前 CanActOnTenant 已通过。
type ToolRequest struct {
	// TenantID 是工具必须把其读操作限定到的、已 resolve 的租户。永远 > 0(HTTP 层会在 dispatch
	// 之前拒绝非正的租户)。
	TenantID int64
	// ActorUserID 是运营者所在的、其运维上下文所属的租户用户。
	ActorUserID int64
	// Role 是运营者的 admin 角色(platform_admin / tenant_operator)。
	Role string
	// Args 是来自请求 body 的、原始的、已解码的工具参数 map。工具只读它认识的键,其余忽略。
	// 绝不以原样持久化——store 会脱敏它。
	Args map[string]any
}

// ToolResult 是一个工具的结构化、已脱敏输出。Summary ONLY(只)持有系统诊断的枚举 / 计数 / id;
// 它是返回给调用方的 body,并(在第二遍脱敏后)持久化到 hermes_tool_calls.result_summary。
type ToolResult struct {
	// Summary 是诊断载荷(只含枚举/计数/id)。
	Summary map[string]any
	// ErrorClass 是诊断暴露出问题时的可选、非 PII 分类(例如 "invalid_grant"、
	// "rate_limit_exceeded")。它是一个短枚举,绝不是含用户数据的自由文本消息。
	ErrorClass string
}

// ArgInt 提取一个正 int64 参数,缺失或非正时返回 ErrInvalidArgs。JSON 把数字解码为 float64,
// 所以 float64 与 int64 都接受。
func ArgInt(args map[string]any, key string) (int64, error) {
	v, ok := args[key]
	if !ok {
		return 0, ErrInvalidArgs
	}
	switch n := v.(type) {
	case float64:
		if n <= 0 || n != float64(int64(n)) {
			return 0, ErrInvalidArgs
		}
		return int64(n), nil
	case int64:
		if n <= 0 {
			return 0, ErrInvalidArgs
		}
		return n, nil
	case int:
		if n <= 0 {
			return 0, ErrInvalidArgs
		}
		return int64(n), nil
	default:
		return 0, ErrInvalidArgs
	}
}

// ArgString 提取一个去除首尾空白后非空的字符串参数(可选)。缺失时返回 ("", false);非字符串值
// 时返回 ("", false)(由调用方决定缺失是否算错误)。它绝不返回用户 prompt 内容——调用方只拉取
// 标识符形态的参数(request_id、claim_id、status 过滤条件)。
func ArgString(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	if s == "" {
		return "", false
	}
	return s, true
}

// ToolSpec 声明一个工具(只读诊断 OR mutating 运维工具)。
type ToolSpec struct {
	Name         string
	Category     Category
	Description  string
	ReadOnly     bool
	RequiredRole string
	// Mutating 对 H4 的"修复"工具为 true。mutating 工具 MUST(必须)设置 Resolve + Mutate
	// (而非 Run),并且只经 confirm 门控的 mutate 路径 dispatch;只读 Run dispatch 会拒绝它。
	// 本波次每个 mutating 工具的 RequiresConfirmation 都为 true(dry-run + confirm 是强制的)。
	Mutating             bool
	RequiresConfirmation bool
	// Proposable 标记一个 LLM 可在对话中 PROPOSE(提议)的 MUTATING 工具(随后它会走 dry-run
	// preview → 运营者 confirm)。它门控 ResolveProposal 与 ProposableCatalog。ONLY(只)对
	// 可逆的 B 级 mutation 置 true(例如启用/禁用一个账号)。对不可逆 / A 级 mutation(例如凭证
	// 轮换)为 false(默认)——这类工具经 H1 confirm 路径保持运营者专属,且 NEVER(绝不)展示给
	// LLM、也不可由 LLM 提议。只读工具忽略此标志。
	Proposable bool
	// InputSchema 是一个描述所接受参数的小 map(name -> 给人看的提示),由 GET /v1/hermes/tools
	// 呈现。它仅供文档说明,不做校验。
	InputSchema map[string]string
	// Run 为一个 READ-ONLY 工具包裹其底层读函数。它 MUST NOT(绝不能)改动状态,并且在依赖缺失时
	// MUST(必须)返回 error(永不 panic)。对 mutating 工具为 nil。
	Run func(ctx context.Context, req ToolRequest) (ToolResult, error)
	// Resolve 为一个 MUTATING 工具执行 READ-ONLY 的目标 resolve + dry-run preview。它校验目标
	// 属于 req.TenantID、读取其当前状态,并返回一个描述目标 + 意图改动的 MutationPlan。它在 BOTH
	// (两处)被调用——dry-run preview 时 AND 紧接真正 mutation 之前——所以 preview 永远不会与
	// 实际动作分叉。它 MUST NOT(绝不能)改动状态。对只读工具为 nil。
	Resolve func(ctx context.Context, req ToolRequest) (MutationPlan, error)
	// Mutate 在给定 Resolve 产出的 plan 后,为一个 MUTATING 工具执行真正的状态改动。它恰好被调用
	// 一次,且只在已确认的请求上、在持有每目标 advisory lock 期间调用。它返回最终的 mutation 后
	// summary(只含枚举/计数/id/状态名——NEVER(绝不)含密钥或轮换后的凭证材料)。对只读工具为 nil。
	Mutate func(ctx context.Context, req ToolRequest, plan MutationPlan) (ToolResult, error)
}

// MutationPlan 是对一项待执行 mutation 的、已 resolve 的只读描述:目标身份 + 当前状态 +
// 意图改动。它是 dry-run preview 与真正执行所共享的单一事实来源(这样 preview 无法对动作将做
// 什么撒谎)。TargetType/TargetID 喂给 admin_audit_events 行;Preview 是 dry-run 时返回给
// 调用方的、已脱敏的"将会改什么"载荷。
type MutationPlan struct {
	// TargetType 是 admin_audit_events 的 target_type(例如 "provider_account"、
	// "account_credential"、"dlq_event")。它 MUST(必须)在迁移白名单中。
	TargetType string
	// TargetID 是目标行的数字 id(advisory-lock 键 + 审计 target_id)。
	TargetID int64
	// LockKey 是 advisory-lock 判别符,用于串行化对 SAME(同一)目标的并发 mutation。以
	// tenant + tool + target 为键,这样两个运营者无法对一个账号竞争 pause/replay。为空 =>
	// orchestrator 从 (TenantID, ToolName, TargetID) 推导一个默认值。
	LockKey string
	// Preview 是 confirm=false 时返回给调用方的、已脱敏的当前状态 + 意图改动载荷。只含
	// 枚举/计数/id/状态名。
	Preview map[string]any
}
