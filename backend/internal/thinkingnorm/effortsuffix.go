package thinkingnorm

// 聊天入口处的推理/思考 effort 后缀归一化。
//
// 调用方可能请求一个别名带有推理/思考 effort 后缀的模型,例如
// "gpt-5-thinking-high"。该后缀是控制模型该思考多努力的每请求级旋钮;
// 它不属于可路由/可定价的模型身份。当检测到一个真正的
// "<reasoning-model>-<effort>" 名称时,本包返回 BASE 模型名(让路由和定价
// 看到的是 base),并改写请求体,使规范化(canonical)的 thinking/reasoning
// 参数把所请求的 effort 携带到上游。
//
// 感知注册表(REGISTRY-AWARE)的剥除(关键的承载性保证):只有当完整请求名
// 在模型注册表中无法解析,且 base 名能解析为一个具备推理能力的模型时,
// effort 后缀才会被剥除。一个真实上线的模型,即便其名称恰好以一个像 effort
// 的 token 结尾(例如 "yi-medium"),也能自行解析,因此永远不会被改动。
// 解析由调用方通过 ModelResolver 提供,所以本包自身从不触达注册表;无后缀的
// 常见路径会执行零次 resolver 调用。
//
// 按入口协议决定 EFFORT 参数:HUAKAI 是按入口路径(openai-chat vs anthropic)
// 而非模型名族系来选择请求解析器。因此所发出的参数取决于入口客户端协议,
// 而非模型名:openai-chat 入口发出顶层的 reasoning_effort 字符串(由
// openai-chat 规范解析器读取);anthropic 入口发出顶层的 thinking 对象(由
// anthropic 规范解析器读取)。正是这一点让发往 openai-chat 端点的
// "claude-...-high" 能够在规范化中存活,而不是被悄悄丢弃。
//
// 它与现有的 thinking 表示(capability_thinking / NormalizeThinkingValidity)
// 组合工作;只负责填充下游规范解析器本就会消费的请求级参数。

import (
	"encoding/json"
	"strings"
)

// IngressProtocol 标识入口请求的客户端协议,它决定了下游哪个请求解析器会
// 读取 body,因而决定 effort 必须被写入哪个 body 参数。它是网关
// client-protocol 枚举的一个本地薄镜像,从而让本包不依赖 proto/gateway 层。
type IngressProtocol int

const (
	// IngressOpenAIChat 覆盖 OpenAI chat-completions 入口路径,其规范解析器
	// 读取顶层的 reasoning_effort 字符串并丢弃顶层的 thinking 对象。
	//(OpenAI Responses 入口不属于此 —— 其解析器使用一个嵌套的 reasoning
	// 对象 —— 因此被映射为 IngressOther。)
	IngressOpenAIChat IngressProtocol = iota
	// IngressAnthropic 覆盖 Anthropic messages 入口路径,其规范解析器读取
	// 顶层的 thinking 对象并丢弃顶层的 reasoning_effort 字符串。
	IngressAnthropic
	// IngressOther 表示任何此处未建模其 effort 参数形状的入口;这类请求保持
	// 不变(不剥除后缀),以便它能像以前一样精确地 404/解析。
	IngressOther
)

// effortLevel 是规范化的、小写的 thinking effort 等级。
type effortLevel string

const (
	effortMinimal effortLevel = "minimal"
	effortLow     effortLevel = "low"
	effortMedium  effortLevel = "medium"
	effortHigh    effortLevel = "high"
	effortMax     effortLevel = "max"
	effortNone    effortLevel = "none"
)

// suffixEntry 把一个带连字符的传输后缀绑定到它的规范 effort 等级。
type suffixEntry struct {
	suffix string
	level  effortLevel
}

// effortSuffixes 是已识别的 effort token,按最具体优先排序,以便更长的
// token("-minimal")先于更短的 token 被匹配,避免歧义。该集合刻意做得小而
// 显式:不在此集合中的 token(例如 "...-turbo" / "...-latest" / "...-preview"
// 别名)根本不会进入感知解析(resolve-aware)的路径,因此永远不会被改动。
// 由于剥除以注册表解析为门控(full-unresolved + base-resolves-reasoning),
// 该集合可以安全地包含 "-max" / "-none":真实的 "...-max" 模型能自行解析,
// 无论 token 如何都会被原样保留。
var effortSuffixes = []suffixEntry{
	{"-minimal", effortMinimal},
	{"-medium", effortMedium},
	{"-high", effortHigh},
	{"-low", effortLow},
	{"-none", effortNone},
	{"-max", effortMax},
}

// levelToBudget 把规范 effort 等级映射为以 token 计的 thinking 预算,供
// anthropic 入口的 thinking 对象使用(它需要一个数值预算)。"none" => 0
// 表示「禁用 thinking」。这些是保守的中间值,而非各 provider 的上限;最终
// 对请求自身输出预算的下钳在 applyAnthropicThinkingBudget 中进行。
var levelToBudget = map[effortLevel]int{
	effortMinimal: 512,
	effortLow:     1024,
	effortMedium:  8192,
	effortHigh:    24576,
	effortMax:     32768,
	effortNone:    0,
}

// openAIEffortLevels 是 OpenAI-chat 规范解析器能理解的离散 reasoning_effort
// 字符串值。"max" 会折叠为 "high",以确保永远不会发出 OpenAI 无效的等级;
// "none" 则移除该字段。
var openAIEffortLevels = map[effortLevel]bool{
	effortMinimal: true,
	effortLow:     true,
	effortMedium:  true,
	effortHigh:    true,
}

// ModelResolver 针对一个候选模型名,回答它是否在模型注册表中可解析,以及
//(若可解析)它是否具备推理/思考能力。调用方将其接到网关的注册表;本包从不
// 自行解析模型。实现应当调用代价低廉,但即便如此也只会在带后缀且完整名
// 无法解析的路径上被调用。
type ModelResolver interface {
	// Resolve 报告 name 是否为已知模型(可解析),以及当其可解析时,是否
	// 具备推理/思考能力。
	Resolve(name string) (resolves bool, reasoningCapable bool)
}

// EffortSuffixOutcome 报告一次归一化处理的结果。
type EffortSuffixOutcome struct {
	// Normalized 仅当 effort 后缀被剥除且 body 被改写时为 true。为 false 时,
	// BaseModel == 输入模型,且 Body 与输入逐字节相同。
	Normalized bool
	// BaseModel 是用于路由/定价的模型名:当 Normalized 时为剥除后的 base,
	// 否则为未改动的输入。
	BaseModel string
	// Level 是从后缀解析出的规范 effort 等级(未归一化时为空)。便于
	// 日志/计费记录。
	Level string
}

// HasEffortSuffix 报告 model 是否以一个已识别的 effort token 结尾。它是
// 廉价的预检查:一个纯字符串形状测试,除一次小写折叠外不做任何分配,且
// 零次 resolver/registry 调用。99% 的常见路径(无 effort 后缀)在此返回
// false,调用方随即短路,使请求逐字节不变。这必须是调用方查询的第一道门。
func HasEffortSuffix(model string) bool {
	_, _, ok := parseEffortSuffix(model)
	return ok
}

// NormalizeEffortSuffix 应用感知注册表的 effort 后缀归一化。
//
// 契约 / 检查顺序(每道门都比下一道更廉价):
//
//  1. 廉价预检查:若 model 没有已识别的 effort token,返回
//     Normalized=false、BaseModel==model、body 逐字节相同。零次 resolver
//     调用。(期望调用方先用 HasEffortSuffix 做门控,但本函数会重新检查,
//     因此可以安全地直接调用。)
//  2. 若 model 以一个 effort token 结尾,调用方此时已经得知完整名无法
//     解析(fullResolves=false 是调用剥除路径的前置条件)。剥除得到 base,
//     并就 base 询问 resolver。
//  3. 只有当 base 可解析且具备推理能力时,我们才剥除并改写。否则原样返回,
//     使请求像以前一样精确地 404 / 路由。
//
// 一个真实模型,如果其名称以像 effort 的 token 结尾(例如 "yi-medium"),
// 会作为完整名被解析,因此调用方永远不会为它进入本函数的剥除分支;即便
// 进入了,base("yi")无法解析也会使其保持不变。无论哪种情况,"yi-medium"
// 都绝不会被改动。
//
// body 是按入口协议而非模型族系改写的:openai-chat 入口 -> 顶层
// reasoning_effort 字符串;anthropic 入口 -> 顶层 thinking 对象。不是 JSON
// 对象的 body 保持原样不动(base 模型仍会被返回),以确保入口路径永远不会
// 在畸形 body 上崩溃。已识别的后缀具有权威性,会覆盖客户端同时设置的任何
// 显式 body 级 reasoning/thinking 参数(后缀是调用方亲手输入的、每次调用
// 显式选择的模型)。
func NormalizeEffortSuffix(model string, body []byte, ingress IngressProtocol, resolver ModelResolver) (EffortSuffixOutcome, []byte) {
	base, level, ok := parseEffortSuffix(model)
	if !ok {
		return EffortSuffixOutcome{BaseModel: model}, body
	}
	if ingress == IngressOther {
		return EffortSuffixOutcome{BaseModel: model}, body
	}
	if resolver == nil {
		return EffortSuffixOutcome{BaseModel: model}, body
	}
	resolves, reasoning := resolver.Resolve(base)
	if !resolves || !reasoning {
		// base 不是已知的、具备推理能力的模型:不剥除。让请求像没有此特性时
		// 一样精确地路由/404。
		return EffortSuffixOutcome{BaseModel: model}, body
	}

	outcome := EffortSuffixOutcome{Normalized: true, BaseModel: base, Level: string(level)}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		// body 不是我们能编辑的 JSON 对象;仍然剥除后缀以便路由/定价看到
		// base 模型,但保持 body 不动。
		return outcome, body
	}

	switch ingress {
	case IngressAnthropic:
		applyAnthropicThinkingBudget(obj, level)
	default: // IngressOpenAIChat
		applyOpenAIReasoningEffort(obj, level)
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return outcome, body
	}
	return outcome, out
}

// parseEffortSuffix 在 model 以一个已识别的 effort token 结尾时返回
// (baseModel, level, true)。对 token 的比较不区分大小写;返回的 base 保留
// 前缀的原始大小写。按最具体优先排序。
func parseEffortSuffix(model string) (string, effortLevel, bool) {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return model, "", false
	}
	lower := strings.ToLower(trimmed)
	for _, ent := range effortSuffixes {
		if strings.HasSuffix(lower, ent.suffix) && len(trimmed) > len(ent.suffix) {
			return trimmed[:len(trimmed)-len(ent.suffix)], ent.level, true
		}
	}
	return model, "", false
}

// applyOpenAIReasoningEffort 设置由 openai-chat 规范解析器消费的顶层
// reasoning_effort 字符串。"none" 移除该字段(不推理);"max" 折叠为 "high",
// 以确保永远不会发出 OpenAI 无效的等级;其余等级原样发出。
func applyOpenAIReasoningEffort(obj map[string]json.RawMessage, level effortLevel) {
	switch {
	case level == effortNone:
		delete(obj, "reasoning_effort")
		return
	case openAIEffortLevels[level]:
		// 原样发出
	case level == effortMax:
		level = effortHigh
	default:
		return
	}
	if raw, err := json.Marshal(string(level)); err == nil {
		obj["reasoning_effort"] = raw
	}
}

// applyAnthropicThinkingBudget 写入由 anthropic 规范解析器消费的顶层
// thinking 对象。"none" 禁用 thinking;其它等级则用 level<->budget 表中的
// 预算启用它,当请求自身的 max-output 预算更小时会被下钳到其之下
//(thinking 预算永远不能超过回答预算)。
func applyAnthropicThinkingBudget(obj map[string]json.RawMessage, level effortLevel) {
	if level == effortNone {
		thinking, _ := json.Marshal(map[string]any{"type": "disabled", "budget_tokens": 0})
		obj["thinking"] = thinking
		return
	}
	budget := budgetForLevel(level)
	if maxOut := requestMaxOutputBudget(obj); maxOut > 0 && budget > maxOut {
		budget = maxOut
	}
	thinking, _ := json.Marshal(map[string]any{"type": "enabled", "budget_tokens": budget})
	obj["thinking"] = thinking
}

// budgetForLevel 把一个 level 解析为 token 预算,对任何意外的 level 默认
// 取 medium 预算,这样未来新增的 level 永远不会意外地得到零(关闭 thinking)
// 预算。
func budgetForLevel(level effortLevel) int {
	if b, ok := levelToBudget[level]; ok {
		return b
	}
	return levelToBudget[effortMedium]
}

// requestMaxOutputBudget 从 max_completion_tokens / max_tokens /
// max_output_tokens 中第一个出现的字段读取请求的 max-output token 预算。
// 当没有一个是可用的正整数时返回 0。
func requestMaxOutputBudget(obj map[string]json.RawMessage) int {
	for _, key := range []string{"max_completion_tokens", "max_tokens", "max_output_tokens"} {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		var n int
		if err := json.Unmarshal(raw, &n); err == nil && n > 0 {
			return n
		}
	}
	return 0
}
