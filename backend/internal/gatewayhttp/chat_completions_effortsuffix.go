package gatewayhttp

import (
	"context"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/thinkingnorm"
)

// resolveModelWithEffortSuffix 解析请求的模型,并融入 registry 感知的 effort 后缀
// 归一化。它先解析「完整」的请求名(沿用已有的路由/计价解析)。「仅当」该名称
// 未知时,才考虑 reasoning/thinking 的 effort 后缀(例如 "gpt-5-thinking-high"):
// 剥离到基名,若基名解析为具备 reasoning 能力的模型,则就地归一化 ex.req.Model 与
// ex.body 并重新解析基名。一个真实上架的模型若只是恰好以看似 effort 的 token 结尾
// (例如 "yi-medium"),在首次调用时即作为完整名解析成功,因此从不被改动,也不会发起
// 额外的 registry 查询。
func (ex *chatExecution) resolveModelWithEffortSuffix() (registry.Resolved, error) {
	resolved, err := ex.d.Registry.ResolveModel(ex.ctx, ex.req.Model, ex.ident.TenantID)
	if errors.Is(err, registry.ErrUnknownModel) && ex.applyEffortSuffixNormalization() {
		return ex.d.Registry.ResolveModel(ex.ctx, ex.req.Model, ex.ident.TenantID)
	}
	return resolved, err
}

// applyEffortSuffixNormalization 是 registry 感知的 effort 后缀钩子,「仅」在完整
// 请求模型名已经解析失败(registry.ErrUnknownModel)之后由 prepareRoute 调用。它
// 从 ex.req.Model 上剥离一个识别出的 reasoning/thinking effort 后缀,并「仅当」
// 「基名」解析为具备 reasoning 能力的模型时,把 ex.req.Model 改写为基名,并改写
// ex.body,使规范的 thinking/reasoning 参数(由「ingress」协议而非模型族选定)把该
// effort 携带到上游。当且仅当它改动了请求时返回 true,以便调用方用基名重新解析。
//
// 成本形态:廉价的前置检查(HasEffortSuffix)在名称不含 effort token 时以「零」次
// registry 调用短路返回。仅在带后缀且完整名未解析的路径上才会对基名发起一次
// registry 查询;已解析的常规路径与无后缀路径都「不」发起额外的 registry 调用。
func (ex *chatExecution) applyEffortSuffixNormalization() bool {
	if !thinkingnorm.HasEffortSuffix(ex.req.Model) {
		return false
	}
	resolver := effortSuffixResolver{
		ctx:      ex.ctx,
		registry: ex.d.Registry,
		tenantID: ex.ident.TenantID,
	}
	outcome, normalizedBody := thinkingnorm.NormalizeEffortSuffix(
		ex.req.Model, ex.body, ingressProtocolForEffort(ex.clientProtocol), resolver)
	if !outcome.Normalized {
		return false
	}
	ex.req.Model = outcome.BaseModel
	ex.body = normalizedBody
	return true
}

// ingressProtocolForEffort 把网关的 ingress 客户端协议映射到该 ingress 下游规范解析
// 器所读取的 effort 参数形态。所发出的参数以「ingress 路径」的协议(它选定请求解析
// 器)为准,而「非」模型名族:openai chat-completions ingress 解析器读取顶层的
// reasoning_effort 字符串;anthropic messages ingress 解析器读取顶层的 thinking 对象。
//
// OpenAI Responses ingress 被刻意不建模(IngressOther):它的规范解析器消费的是
// 「嵌套」的 reasoning 对象,而非顶层 reasoning_effort,所以在那里发出 chat 形态会在
// 规范化过程中被静默丢弃。不建模意味着带 effort 后缀的 Responses 请求不会被改写,行为
// 与改动前完全一致(不会静默丢失 effort)。原生 Responses effort 接线是后续工作。任何
// 其它 ingress 也都不建模,因此其请求从不被改写。
func ingressProtocolForEffort(p proto.ClientProtocol) thinkingnorm.IngressProtocol {
	switch p {
	case proto.ClientProtocolOpenAIChat:
		return thinkingnorm.IngressOpenAIChat
	case proto.ClientProtocolAnthropicMessages:
		return thinkingnorm.IngressAnthropic
	default:
		return thinkingnorm.IngressOther
	}
}

// effortSuffixResolver 把模型 registry 适配为 thinkingnorm.ModelResolver。它通过运行
// 路由准备已经使用的同一个 ResolveModel,回答「这个名称能否解析,以及是否具备
// reasoning/thinking 能力?」,因此基名是按完全相同的路由/计价 registry 视角来判定的。
// 解析错误(unknown / disabled / no-access / transient)都意味着「不是可用的基名」,
// 因此 effort 后缀原样保留,请求仍按改动前一样返回 404。
type effortSuffixResolver struct {
	ctx      context.Context
	registry registry.Registry
	tenantID int64
}

func (r effortSuffixResolver) Resolve(name string) (resolves bool, reasoningCapable bool) {
	if r.registry == nil {
		return false, false
	}
	resolved, err := r.registry.ResolveModel(r.ctx, name, r.tenantID)
	if err != nil {
		return false, false
	}
	return true, hasReasoningCapability(resolved.Capabilities)
}

// hasReasoningCapability 报告已解析模型的能力列表是否标记其具备 reasoning/thinking
// 能力。两套 registry 能力词汇都被认可:"reasoning"(公开发现描述符)与 "thinking"
// (HCSF 能力族)。
func hasReasoningCapability(capabilities []string) bool {
	for _, c := range capabilities {
		if c == "reasoning" || c == "thinking" {
			return true
		}
	}
	return false
}
