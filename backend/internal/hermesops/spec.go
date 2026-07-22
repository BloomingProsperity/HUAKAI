// Package hermesops 提供由网关控制的 Hermes 运维工具注册、授权、派发和日志合同。
// 只读工具只能运行查询；改动型工具只能经过目标解析、预览、人工确认、目标锁和原子日志
// 路径执行。工具结果只允许封闭的系统状态，不携带请求正文、凭据或个人信息。
package hermesops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/hermesops/toolschema"
)

// 工具名是日志、数据库约束和管理接口共同使用的权威标识符。
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

	// 改动型工具复用既有管理写路径，只能从人工确认路径执行。
	ToolDLQReplay     = "dlq_replay"
	ToolAccountPause  = "account_pause"
	ToolAccountResume = "account_resume"
	ToolRenewTrigger  = "renew_trigger"

	// 告警规则启停是可逆操作，模型可以提议，但管理员确认后才会执行。
	ToolAlertRuleEnable  = "alert_rule_enable"
	ToolAlertRuleDisable = "alert_rule_disable"

	// 内容审核关键词启停安全敏感但可逆；模型只能提议，管理员确认后才会修改未删除规则。
	ToolModerationKeywordEnable  = "moderation_keyword_enable"
	ToolModerationKeywordDisable = "moderation_keyword_disable"
)

// Roles 镜像 internal/admin 的角色标识符。保留为本地常量,这样本包就不必为两个字符串去 import
// admin 包(RBAC 校验本身由调用方经 admin.AdminIdentity.CanIssueForTenant 执行,本包永不绕过它)。
const (
	RolePlatformAdmin  = "platform_admin"
	RoleTenantOperator = "tenant_operator"
)

// Category 用于区分诊断工具和改动型运维工具。
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
	// CanIssueForTenant 单独强制执行,也会同样呈现为一条 denied 行。
	ErrToolForbidden = errors.New("hermesops: tool forbidden for role")
	// ErrDependencyUnwired 表示工具依赖未接线，调用必须失败关闭而不能 panic。
	ErrDependencyUnwired = errors.New("hermesops: tool dependency unwired")
	// ErrInvalidArgs 在工具参数格式错误 / 缺少必填项时返回。
	ErrInvalidArgs = errors.New("hermesops: invalid tool args")
	// ErrNotMutating 在 mutate/preview 路径被要求运行一个只读工具(或反之)时返回,这样 mutation
	// 永远无法从只读 dispatch 偷溜进来,只读工具也永远无法到达 confirm 路径。
	ErrNotMutating = errors.New("hermesops: tool is not mutating")
	// ErrNotProposable 表示模型尝试提议一个仅允许管理员主动发起的改动型工具。
	ErrNotProposable = errors.New("hermesops: tool is not LLM-proposable")
	// ErrTargetResolution 在 mutating 工具无法 resolve 其目标(租户缺失/异租户、账号未找到)时返回。
	// 它区别于 ErrInvalidArgs,好让 HTTP 层把它映射到 404/403 而非 400。
	ErrTargetResolution = errors.New("hermesops: target resolution failed")
	// ErrInvalidToolSpec 表示注册表中的工具定义不完整。
	ErrInvalidToolSpec = errors.New("hermesops: invalid tool spec")
	// ErrInvalidToolSchema 表示工具参数合同不是受支持的 JSON Schema。
	ErrInvalidToolSchema = errors.New("hermesops: invalid tool schema")
	// ErrDuplicateTool 表示同名工具被重复注册。
	ErrDuplicateTool = errors.New("hermesops: duplicate tool")
)

// ToolRequest 是交给工具 Run 的、已 resolve 且已授权的调用上下文。TenantID 是由中间件推导、
// 经作用域校验的租户;HTTP 层保证在调用 Run 之前 CanIssueForTenant 已通过。
type ToolRequest struct {
	// TenantID 是工具必须把其读操作限定到的、已 resolve 的租户。永远 > 0(HTTP 层会在 dispatch
	// 之前拒绝非正的租户)。
	TenantID int64
	// ActorSource 与 ActorID 唯一标识真实管理员，不能使用内部服务主体代替。
	ActorSource string
	ActorID     int64
	// Role 是运营者的 admin 角色(platform_admin / tenant_operator)。
	Role string
	// Args 是来自请求 body 的、原始的、已解码的工具参数 map。工具只读它认识的键,其余忽略。
	// 绝不以原样持久化——store 会脱敏它。
	Args map[string]any
}

// ToolResult 是工具的结构化、已脱敏输出。Summary 只持有系统诊断的枚举、计数和编号；
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

// ToolSpec 声明一个只读诊断或改动型运维工具。
type ToolSpec struct {
	Name         string
	Category     Category
	Description  string
	ReadOnly     bool
	RequiredRole string
	// 改动型工具必须设置 Resolve 和 Mutate，并强制人工确认；只读派发会拒绝它。
	Mutating             bool
	RequiresConfirmation bool
	// Proposable 只对可逆操作开启。模型生成的提议仍需管理员确认；凭证轮换等操作保持
	// 管理员主动发起，不向模型目录暴露。
	Proposable bool
	// InputSchema 是同时供管理 API 与 MCP 使用的 JSON Schema。注册时会校验其结构，
	// 调用时也会按同一份合同校验参数，避免目录说明与真实执行规则漂移。
	InputSchema map[string]any
	// Run 包装只读查询；依赖缺失时返回错误。改动型工具必须为 nil。
	Run func(ctx context.Context, req ToolRequest) (ToolResult, error)
	// Resolve 只读解析目标、校验租户归属并生成预览。预览和确认执行共用该结果，避免
	// 展示内容与实际动作分叉。只读工具必须为 nil。
	Resolve func(ctx context.Context, req ToolRequest) (MutationPlan, error)
	// Mutate 只在确认成功且持有目标锁时执行一次，结果不得包含密钥或凭证材料。
	Mutate func(ctx context.Context, req ToolRequest, plan MutationPlan) (ToolResult, error)
}

// ObjectSchema 构造 HUAKAI 工具统一使用的对象参数合同。默认拒绝未声明字段，避免模型把
// 租户、角色或其它越权参数夹带进工具调用。
func ObjectSchema(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	requiredCopy := append([]string(nil), required...)
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             requiredCopy,
		"additionalProperties": false,
	}
}

// StringSchema 构造字符串参数合同。
func StringSchema(description string, enum ...string) map[string]any {
	schema := map[string]any{"type": "string", "description": description}
	if len(enum) > 0 {
		schema["enum"] = append([]string(nil), enum...)
	}
	return schema
}

// NonEmptyStringSchema 构造必需非空的字符串参数合同。
func NonEmptyStringSchema(description string) map[string]any {
	schema := StringSchema(description)
	schema["minLength"] = 1
	return schema
}

// PositiveIntegerSchema 构造正整数参数合同。
func PositiveIntegerSchema(description string) map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "description": description}
}

// BoundedIntegerSchema 构造带上下界的整数参数合同。
func BoundedIntegerSchema(description string, minimum, maximum int64) map[string]any {
	return map[string]any{
		"type": "integer", "minimum": minimum, "maximum": maximum, "description": description,
	}
}

const (
	defaultToolPageLimit = 50
	maxToolPageLimit     = 200
	maxToolPageOffset    = 1_000_000
	maxToolCursorID      = int64(9_007_199_254_740_991)
)

// paginationProperties 在已有工具参数上加入统一的有界分页合同。
func paginationProperties(properties map[string]any) map[string]any {
	out := make(map[string]any, len(properties)+2)
	for key, value := range properties {
		out[key] = value
	}
	out["limit"] = BoundedIntegerSchema("本页最多返回的记录数", 1, maxToolPageLimit)
	out["offset"] = BoundedIntegerSchema("从结果集起点跳过的记录数", 0, maxToolPageOffset)
	return out
}

func pageArgs(args map[string]any) (limit, offset int, err error) {
	limit = defaultToolPageLimit
	if raw, ok := args["limit"]; ok {
		value, valid := integerValue(raw)
		if !valid || value < 1 || value > maxToolPageLimit {
			return 0, 0, ErrInvalidArgs
		}
		limit = int(value)
	}
	if raw, ok := args["offset"]; ok {
		value, valid := integerValue(raw)
		if !valid || value < 0 || value > maxToolPageOffset {
			return 0, 0, ErrInvalidArgs
		}
		offset = int(value)
	}
	return limit, offset, nil
}

func trimPage[T any](rows []T, limit, offset int) ([]T, map[string]any) {
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	var nextOffset any
	if hasMore {
		nextOffset = offset + len(rows)
	}
	return rows, map[string]any{
		"limit": limit, "offset": offset, "returned": len(rows),
		"has_more": hasMore, "next_offset": nextOffset,
	}
}

// ObjectValueSchema 构造普通 JSON 对象参数合同。
func ObjectValueSchema(description string) map[string]any {
	return map[string]any{"type": "object", "description": description}
}

// ValidateToolSpec 在注册阶段验证工具分类、执行入口与 JSON Schema。任何不完整定义都必须
// 在进程启动时暴露，不能等模型真正调用时才发现。
func ValidateToolSpec(spec ToolSpec) error {
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("%w: 工具名为空", ErrInvalidToolSpec)
	}
	if strings.TrimSpace(spec.Description) == "" {
		return fmt.Errorf("%w: %s 缺少说明", ErrInvalidToolSpec, spec.Name)
	}
	if roleRank(spec.RequiredRole) == 0 {
		return fmt.Errorf("%w: %s 的角色无效", ErrInvalidToolSpec, spec.Name)
	}
	if spec.Mutating {
		if spec.ReadOnly || spec.Resolve == nil || spec.Mutate == nil || !spec.RequiresConfirmation {
			return fmt.Errorf("%w: %s 的改动合同不完整", ErrInvalidToolSpec, spec.Name)
		}
	} else if !spec.ReadOnly || spec.Run == nil || spec.Resolve != nil || spec.Mutate != nil || spec.Proposable {
		return fmt.Errorf("%w: %s 的只读合同不完整", ErrInvalidToolSpec, spec.Name)
	}
	return ValidateInputSchema(spec.InputSchema)
}

// ValidateInputSchema 校验本项目工具支持的 JSON Schema 子集。
func ValidateInputSchema(schema map[string]any) error {
	if err := toolschema.ValidateSchema(schema); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidToolSchema, err)
	}
	return nil
}

// ValidateToolArguments 按工具注册时的合同校验一次调用。
func ValidateToolArguments(schema map[string]any, args map[string]any) error {
	err := toolschema.ValidateArguments(schema, args)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, toolschema.ErrSchema):
		return fmt.Errorf("%w: %v", ErrInvalidToolSchema, err)
	default:
		return fmt.Errorf("%w: %v", ErrInvalidArgs, err)
	}
}

func integerValue(value any) (int64, bool) {
	return toolschema.IntegerValue(value)
}

// MutationPlan 是对一项待执行 mutation 的、已 resolve 的只读描述:目标身份 + 当前状态 +
// 意图改动。它是 dry-run preview 与真正执行所共享的单一事实来源(这样 preview 无法对动作将做
// 什么撒谎)。TargetType/TargetID 喂给 admin_audit_events 行;Preview 是 dry-run 时返回给
// 调用方的、已脱敏的"将会改什么"载荷。
type MutationPlan struct {
	// TargetType 是 admin_audit_events 的目标类型，必须在数据库允许清单中。
	TargetType string
	// TargetID 是目标行的数字 id(advisory-lock 键 + 审计 target_id)。
	TargetID int64
	// LockKey 是 advisory lock 判别符，用于串行化对同一目标的并发改动。以
	// tenant + tool + target 为键,这样两个运营者无法对一个账号竞争 pause/replay。为空 =>
	// orchestrator 从 (TenantID, ToolName, TargetID) 推导一个默认值。
	LockKey string
	// Preview 是 confirm=false 时返回给调用方的、已脱敏的当前状态 + 意图改动载荷。只含
	// 枚举/计数/id/状态名。
	Preview map[string]any
}
