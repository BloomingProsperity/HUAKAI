package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/protosse"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/thinkingnorm"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

type HCSFDispatchInput struct {
	ProtocolFamily    string
	UpstreamModelID   string
	Account           provider.AccountInfo
	Credential        provider.Credential
	TransportMode     transport.TransportMode
	RawBody           []byte
	BodyControls      DispatchBodyControls
	InboundBetaTokens []string
	OfficialDirect    bool
	// IdentityRewrite 作用于 HCSF marshal 后的最终 body；nil 表示不改写。
	// 开关、fail-open 与身份投影语义由接线方统一提供。
	IdentityRewrite func([]byte) []byte
}

// applyIdentityRewrite 在钩子存在且 body 非空时处理最终出站字节。
func applyIdentityRewrite(body []byte, rewrite func([]byte) []byte) []byte {
	if rewrite == nil || len(body) == 0 {
		return body
	}
	return rewrite(body)
}

type hcsfDispatchInputKey struct{}

func ContextWithHCSFDispatchInput(ctx context.Context, in HCSFDispatchInput) context.Context {
	return context.WithValue(ctx, hcsfDispatchInputKey{}, in)
}

func hcsfDispatchInputFromContext(ctx context.Context) HCSFDispatchInput {
	if in, ok := ctx.Value(hcsfDispatchInputKey{}).(HCSFDispatchInput); ok {
		return in
	}
	return HCSFDispatchInput{}
}

// HCSFDispatchInputFromContext 供接线方读取本次 dispatch 输入并做接线断言。
func HCSFDispatchInputFromContext(ctx context.Context) HCSFDispatchInput {
	return hcsfDispatchInputFromContext(ctx)
}

type envelopeRequestBuilder interface {
	BuildRequestFromEnvelope(context.Context, provider.BuildInput, *proto.HCSF) (*http.Request, error)
}

// maxBufferedUpstreamResponseBytes 是 HCSF non-streaming buffered 上游响应的读取上限(1MiB)。
// 与 gatewayhttp legacy raw 路径(maxRawBufferedUpstreamBodyBytes)同值，保证两条 buffered
// 路径对超大响应行为一致。
const maxBufferedUpstreamResponseBytes = 1 << 20

// ErrUpstreamResponseTooLarge 表示上游 2xx 成功响应超过 buffered 读取上限。调用方应映射成
// clienterr.CodeUpstreamResponseTooLarge(终止、不重试：重试不会变小)，而非把截断字节喂给
// ProviderResponseToCanonical 后塌成 opaque dispatch error，或被 ReconstructBufferedFromSSE
// 当截断 SSE 计部分账。
var ErrUpstreamResponseTooLarge = errors.New("gateway: upstream buffered response exceeds size limit")

// ForcedStreamingBufferedFamily 标记上游成功响应固定以 SSE 返回的协议族。
func ForcedStreamingBufferedFamily(family string) bool {
	switch family {
	case "openai_codex":
		return true
	default:
		return false
	}
}

func hcsfShouldAggregateForcedStreamingBuffered(family string, env *proto.HCSF) bool {
	if env == nil {
		return false
	}
	return ForcedStreamingBufferedFamily(family) &&
		env.StreamPlan.Mode == proto.StreamModeBuffered
}

// readBufferedUpstreamResponse 读上游 buffered 响应，带溢出哨兵(读 limit+1 探测)。
// oversized 时 raw 截断到 maxBufferedUpstreamResponseBytes —— 仅供非 2xx 响应的错误分类用；
// 2xx 成功响应一旦 oversized，调用方必须拒绝(截断的成功体不可解析/会错计费)。
func readBufferedUpstreamResponse(r io.Reader) (raw []byte, oversized bool, err error) {
	raw, err = io.ReadAll(io.LimitReader(r, maxBufferedUpstreamResponseBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(raw) > maxBufferedUpstreamResponseBytes {
		return raw[:maxBufferedUpstreamResponseBytes], true, nil
	}
	return raw, false, nil
}

// DispatchHCSF 执行 non-streaming HCSF 主链路：envelope -> vendor HTTP -> buffered envelope。
func (d *UpstreamDispatcher) DispatchHCSF(ctx context.Context, env *proto.HCSF) (*proto.HCSF, error) {
	if d == nil {
		return nil, errors.New("dispatcher: nil receiver")
	}
	if env == nil {
		return nil, errors.New("dispatcher: HCSF envelope 为空")
	}
	if d.Adapters == nil {
		return nil, errors.New("dispatcher: AdapterRegistry 未配置")
	}
	if d.TransportFactory == nil {
		return nil, errors.New("dispatcher: TransportFactory 未配置")
	}

	in := hcsfDispatchInputFromContext(ctx)
	family := firstNonEmpty(in.ProtocolFamily, env.RequestMeta.ProtocolFamily)
	if family == "" {
		return nil, errors.New("dispatcher: HCSF ProtocolFamily 未指定")
	}
	endpointFamily := firstNonEmpty(env.RequestMeta.EndpointFamily, family)
	ingressFamily := string(env.RequestMeta.ClientProtocol)
	if ingressFamily == "" {
		ingressFamily = firstNonEmpty(env.RequestMeta.ProtocolFamily, family)
	}
	upstreamModel := firstNonEmpty(in.UpstreamModelID, env.RequestMeta.UpstreamModel, env.RequestMeta.Model)
	account := in.Account
	if account.AccountID == 0 {
		account.AccountID = env.RequestMeta.AccountID
	}
	if account.Platform == "" {
		account.Platform = env.RequestMeta.Provider
	}

	providerAdapter, err := d.Adapters.For(family)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: 取 provider adapter 失败 (protocol=%q): %w", family, err)
	}
	req, err := buildHCSFProviderRequest(ctx, providerAdapter, provider.BuildInput{
		UpstreamModelID:   upstreamModel,
		Credential:        in.Credential,
		Account:           account,
		InboundBetaTokens: in.InboundBetaTokens,
	}, env, ingressFamily, endpointFamily, in.RawBody, in.IdentityRewrite, in.BodyControls)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: BuildRequestFromEnvelope/BuildRequest 失败: %w", err)
	}
	if err := validatePassthroughEndpointTarget(ctx, in.Credential, req); err != nil {
		return nil, err
	}

	mode := in.TransportMode
	if mode == "" {
		mode = transport.TransportModeStandard
	}
	rt, err := d.TransportFactory.For(transport.ProviderCode(account.Platform), mode)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: 取 RoundTripper 失败: %w", err)
	}
	client := d.HTTPClient
	if client == nil {
		rt = d.applyTLSProfile(ctx, rt, mode, account.AccountID)
		rt, err = d.applyProxy(ctx, rt, account.AccountID)
		if err != nil {
			return nil, err
		}
		if provider.UsesCustomPassthroughEndpoint(in.Credential) {
			rt, err = provider.WrapPassthroughEndpointTransport(rt)
			if err != nil {
				return nil, fmt.Errorf("dispatcher: passthrough endpoint rejected: %w", err)
			}
		}
		client = d.httpClientForRoundTripper(rt, true)
	}

	resp, err := d.doWithDynamicCredentialRecovery(ctx, account, in.Credential, client, req, func(credential provider.Credential) (*http.Request, error) {
		rebuilt, buildErr := buildHCSFProviderRequest(ctx, providerAdapter, provider.BuildInput{
			UpstreamModelID: upstreamModel, Credential: credential,
			Account: account, InboundBetaTokens: in.InboundBetaTokens,
		}, env, ingressFamily, endpointFamily, in.RawBody, in.IdentityRewrite, in.BodyControls)
		if buildErr != nil {
			return nil, buildErr
		}
		if buildErr = validatePassthroughEndpointTarget(ctx, credential, rebuilt); buildErr != nil {
			return nil, buildErr
		}
		return rebuilt, nil
	})
	if err != nil {
		return nil, fmt.Errorf("dispatcher: HTTP Do 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _, err := readBufferedUpstreamResponse(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("dispatcher: 读取上游响应失败: %w", err)
		}
		// 用类型化错误把 status + body 透传到调用方, 由调用方决定 client 返回
		// 状态码 / 走 health classification / 触发 cooldown. 不能塌成 string-only,
		// 否则 chat handler 总是 502 + status=0 health signal 跟流式路径行为分叉。
		// 非 2xx 时即便 oversized, 截断 body 仍够做错误分类(镜像 legacy oversizedNon2xx)。
		return nil, &UpstreamHTTPError{
			StatusCode: resp.StatusCode,
			Body:       append([]byte(nil), raw...),
			Header:     resp.Header.Clone(),
		}
	}

	upstreamAdapters := d.ProtocolAdapters
	if upstreamAdapters == nil {
		upstreamAdapters = BuildDefaultProtocolAdapterRegistry()
	}
	upstreamAdapter, err := upstreamAdapters.For(family)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: 取 upstream adapter 失败 (protocol=%q): %w", family, err)
	}

	var responseEnv *proto.HCSF
	var respLosses []proto.ProtocolLossEntry
	if hcsfShouldAggregateForcedStreamingBuffered(family, env) {
		responseEnv, respLosses, err = reconstructForcedStreamingBuffered(ctx, upstreamAdapter, resp.Body)
		if err != nil {
			return nil, err
		}
	} else {
		raw, oversized, err := readBufferedUpstreamResponse(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("dispatcher: 读取上游响应失败: %w", err)
		}
		if oversized {
			// 2xx 但超 1MiB: 截断字节喂给 ProviderResponseToCanonical 会静默截断(→opaque 502)，
			// SSE 形则被 ReconstructBufferedFromSSE 当部分响应计费。在 canonicalize 前直接拒绝。
			return nil, ErrUpstreamResponseTooLarge
		}
		responseEnv, respLosses, err = upstreamAdapter.ProviderResponseToCanonical(ctx, raw)
		if err != nil {
			if reconstructedEnv, reconstructedLosses, ok := protosse.ReconstructBufferedFromSSE(upstreamAdapter, raw); ok && reconstructedEnv != nil {
				responseEnv = reconstructedEnv
				respLosses = reconstructedLosses
			} else {
				return nil, fmt.Errorf("dispatcher: ProviderResponseToCanonical 失败: %w", err)
			}
		}
	}
	if responseEnv == nil || responseEnv.BufferedResponse == nil {
		if hcsfShouldAggregateForcedStreamingBuffered(family, env) {
			return nil, errors.New("dispatcher: 上游强制流式响应未能聚合为 buffered_response")
		} else {
			return nil, errors.New("dispatcher: upstream adapter 未返回 buffered_response")
		}
	}

	out, err := cloneHCSF(env)
	if err != nil {
		return nil, err
	}
	out.BufferedResponse = responseEnv.BufferedResponse
	out.Accounting.Usage = responseEnv.BufferedResponse.Usage
	if len(respLosses) > 0 {
		// 响应侧 adapter 可能记录 usage 缺失、未知 block、tool id 兜底等协议损耗；
		// 生产 envelope 必须累加这些 loss，不能覆盖请求侧已存在的 loss。
		out.CapabilityGraph.ProtocolLoss = append(out.CapabilityGraph.ProtocolLoss, respLosses...)
	}
	fillUpstreamReported(out)
	return out, nil
}

func reconstructForcedStreamingBuffered(ctx context.Context, upstreamAdapter proto.UpstreamAdapter, body io.Reader) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	responseEnv, respLosses, ok, err := protosse.ReconstructBufferedFromSSEReader(ctx, upstreamAdapter, body, protosse.DefaultBufferedSSEReconstructLimits())
	if err != nil {
		if errors.Is(err, protosse.ErrBufferedSSECanonicalTooLarge) {
			return nil, respLosses, ErrUpstreamResponseTooLarge
		}
		return nil, respLosses, fmt.Errorf("dispatcher: 上游流式响应聚合失败: %w", err)
	}
	if !ok || responseEnv == nil || responseEnv.BufferedResponse == nil {
		return nil, respLosses, errors.New("dispatcher: 上游流式响应未返回可识别内容")
	}
	return responseEnv, respLosses, nil
}

func buildHCSFProviderRequest(ctx context.Context, a provider.Adapter, in provider.BuildInput, env *proto.HCSF, ingressFamily string, endpointFamily string, nativeRawBody []byte, identityRewrite func([]byte) []byte, controlsOpt ...DispatchBodyControls) (*http.Request, error) {
	var controls DispatchBodyControls
	if len(controlsOpt) > 0 {
		controls = controlsOpt[0]
	}
	if hcsfDispatchInputFromContext(ctx).OfficialDirect {
		if endpointFamily != "anthropic_claude_session" || ingressFamily != "anthropic_messages" {
			return nil, fmt.Errorf("dispatcher: official direct raw body rejects endpoint=%q ingress=%q", endpointFamily, ingressFamily)
		}
		if len(nativeRawBody) == 0 || controls.Enabled() {
			return nil, errors.New("dispatcher: official direct requires raw body without body controls")
		}
		in.InboundBody = applyIdentityRewrite(nativeRawBody, identityRewrite)
		return a.BuildRequest(ctx, in)
	}
	if b, ok := a.(envelopeRequestBuilder); ok {
		req, err := b.BuildRequestFromEnvelope(ctx, in, env)
		if err != nil {
			return nil, err
		}
		return applyRequestBodyControls(req, controls)
	}
	if hcsfProviderRequestUsesNativeRawBody(endpointFamily, ingressFamily) {
		if err := validateNativeRawBodyIngress(ingressFamily, endpointFamily); err != nil {
			return nil, err
		}
		if len(nativeRawBody) == 0 {
			return nil, fmt.Errorf("dispatcher: HCSF native raw body missing for endpoint family %q", endpointFamily)
		}
		body, err := ApplyDispatchBodyControls(nativeRawBody, controls)
		if err != nil {
			return nil, err
		}
		// RR-03: 转发前强制 Anthropic extended-thinking 的有效性(temperature=1 /
		// tool_choice auto), 否则会被 upstream 返回 400; 当 body 没有激活的
		// thinking 字段时为空操作(字节等价)。
		body = thinkingnorm.NormalizeThinkingValidity(body)
		// R7 身份改写施加在最终上游 body 上(native-raw 子路:bedrock/codex)。
		// 默认关时钩子空操作= 字节等价。
		body = applyIdentityRewrite(body, identityRewrite)
		in.InboundBody = body
		return a.BuildRequest(ctx, in)
	}
	resolveURLImagesForFamily(ctx, env, endpointFamily, defaultImageFetcher)
	body, err := hcsfRequestBody(env, endpointFamily)
	if err != nil {
		return nil, err
	}
	body, err = mergeHCSFRawPassthroughFields(body, ingressFamily, endpointFamily, nativeRawBody)
	if err != nil {
		return nil, err
	}
	body, err = ApplyDispatchBodyControls(body, controls)
	if err != nil {
		return nil, err
	}
	// R7 身份改写施加在最终上游 body 上(canonical-marshal 子路;anthropic 默认走此)。
	// 此处 body 是 MarshalToProviderRequest 的产物 —— anthropic marshal 不带 metadata,
	// 钩子开关开时走 inject 路径补出含池账号身份的 metadata.user_id;默认关时空操作
	// = 字节等价,保 HCSF 默认路径零行为变化。
	body = applyIdentityRewrite(body, identityRewrite)
	in.InboundBody = body
	return a.BuildRequest(ctx, in)
}

func applyRequestBodyControls(req *http.Request, controls DispatchBodyControls) (*http.Request, error) {
	if req == nil || req.Body == nil || !controls.Enabled() {
		return req, nil
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if err := req.Body.Close(); err != nil {
		return nil, err
	}
	body, err := ApplyDispatchBodyControls(raw, controls)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return req, nil
}

func hcsfRequestBody(env *proto.HCSF, endpointFamily string) ([]byte, error) {
	modeledFamily := hcsfProviderRequestModelFamily(endpointFamily)
	body, err := MarshalToProviderRequest(env, endpointFamily)
	if err != nil {
		return nil, err
	}
	body, err = injectRequestControls(body, env, modeledFamily)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: HCSF request controls 注入失败: %w", err)
	}
	return body, nil
}

// hcsfProviderRequestModelFamily 把 endpoint family 归一到它的 HCSF marshal
// 形态族(线格式形态同形 ⇒ 同一 marshal 投影)。这张表是族集对称守卫的第 4 个
// 站点:出站 registrydefault / 入站 protocol_selector / stream_scanner 三表
// 之外,每个注册族还必须在此有 marshal 形态(或在守卫测试的 fail-closed
// 例外表里有文档化理由)——此前 kimi/qwen/glm/yi/baichuan/doubao/ernie/step/
// hunyuan/minimax/cohere/ollama 12 族缺失,导致流式翻译路径 501、非流式
// HCSF 路径 502(MarshalToProviderRequest unsupported)。守卫:
// gateway.TestMarshalSupportsEveryRegisteredProtocolFamily。
//
// 刻意 fail-closed、不在表内的族(映射前提"请求 body 与形态族同形"不成立):
//   - openai_codex            chat/messages 入站复用 Responses 形 marshal;Responses 形/同族入站仍走 native-raw 直通保真。
//   - cursor_session          上游是 Connect/proto 帧(application/connect+proto,
//     见 provider/cursor),openai_chat JSON 投影必不可解析。
//   - gemini_advanced_session 上游是 f.req= form-urlencoded 包装(见
//     provider/gemini/gemini_advanced_session.go),非 Gemini API JSON。
//   - bedrock_invoke          binary EventStream;anthropic 入站走 native-raw +
//     adapter 内 AutoTranslate;openai 入站在 marshal 处 fail-closed(501)。
func hcsfProviderRequestModelFamily(endpointFamily string) string {
	switch endpointFamily {
	case "openrouter_chat", "grok_chat", "deepseek_chat", "mistral_chat", "groqcloud_chat", "together_chat", "perplexity_chat", "fireworks_chat",
		"kimi_chat", "qwen_chat", "glm_chat", "yi_chat", "baichuan_chat", "doubao_chat", "ernie_chat", "step_chat", "hunyuan_chat", "minimax_chat", "cohere_chat", "ollama_chat",
		"copilot_session", "kiro_session", "windsurf_session":
		return "openai_chat"
	case "vertex_gemini":
		// Gemini-on-Vertex 请求 body 与 generativelanguage Gemini 同形:HCSF 先
		// marshal 出标准 gemini_messages body,vertex.PassthroughAdapter（ModeGemini）
		// 再原样直通到 publishers/google endpoint。
		return "gemini_messages"
	case "gemini_code_assist", "antigravity_session":
		// 两条 Cloud Code 车道都先 marshal 出标准 gemini_messages body 作内层，
		// provider adapter 再包 {model,project,request} envelope。Antigravity 额外
		// 注入已确认的客户端头与 GOOGLE_ONE_AI credit 类型。
		return "gemini_messages"
	case "vertex_anthropic":
		// Anthropic-on-Vertex:HCSF marshal 出标准 anthropic_messages body,
		// vertex.PassthroughAdapter（ModeAnthropic）再剥 model/stream + 注
		// anthropic_version 改写成 Vertex rawPredict 形（两步串联）。
		return "anthropic_messages"
	case "anthropic_claude_session":
		// OAuth/session 只改变凭据与准入策略，请求线形仍是 Anthropic Messages。
		return "anthropic_messages"
	case "openai_codex":
		// 非 responses/非同族入站(chat/messages)复用 Responses 形投影;
		// adapter 出站前再剥离 codex 不接受的请求控制字段。
		return "openai_responses"
	default:
		return endpointFamily
	}
}

// HCSFEndpointModelFamily 是 hcsfProviderRequestModelFamily 的导出形式,供
// gatewayhttp 流式翻译门(needsStreamingHCSFTranslation)判断"上游族与客户端
// 协议同形 ⇒ raw 直通"。保持单一映射真相源,禁止在调用方复制这张表。
func HCSFEndpointModelFamily(endpointFamily string) string {
	return hcsfProviderRequestModelFamily(endpointFamily)
}

func hcsfProviderRequestUsesNativeRawBody(endpointFamily, ingressFamily string) bool {
	switch endpointFamily {
	case "bedrock_invoke":
		return true
	case "openai_codex":
		// 仅 Responses 形/同族/空 ingress 走字节直通;chat/messages 入站落到 canonical marshal,避免把异族 raw body 原样投给 codex。
		return ingressFamily == "" || ingressFamily == endpointFamily || ingressFamily == "openai_responses"
	default:
		return false
	}
}

// validateNativeRawBodyIngress 是 native-raw 族直转前的跨协议守卫,许可集与
// 流式翻译门(gatewayhttp needsStreamingHCSFTranslation)严格镜像,流式/非流式
// 不得分叉:
//   - 同族 ingress(native lane;含 ClientProtocol 空回退到族名)直通;
//   - anthropic_messages→bedrock_invoke 直通:bedrock PassthroughAdapter 内
//     AutoTranslate 承接,且按真实 ingress 判定——替代只查 "messages" 顶层键的
//     body 形态嗅探(openai_chat body 同样含 "messages",嗅探会误译);
//   - 其余跨协议 ingress fail-closed:anthropic body 原样直发 codex、openai
//     body 误译 bedrock 都是把垃圾投给上游,必须在投递前挡下(此前流式已
//     fail-closed 而非流式 fail-open,DM-20 评审 S2)。
func validateNativeRawBodyIngress(ingressFamily, endpointFamily string) error {
	if ingressFamily == "" || ingressFamily == endpointFamily {
		return nil
	}
	if ingressFamily == "anthropic_messages" && endpointFamily == "bedrock_invoke" {
		return nil
	}
	if ingressFamily == "openai_responses" && endpointFamily == "openai_codex" {
		return nil
	}
	return fmt.Errorf("dispatcher: native raw-body endpoint family %q does not accept ingress %q (cross-protocol raw forward is fail-closed)", endpointFamily, ingressFamily)
}

func mergeHCSFRawPassthroughFields(body []byte, ingressFamily string, endpointFamily string, raw []byte) ([]byte, error) {
	if ingressFamily != "openai_chat" || hcsfProviderRequestModelFamily(endpointFamily) != "openai_chat" || len(raw) == 0 {
		return body, nil
	}
	var original map[string]json.RawMessage
	if err := json.Unmarshal(raw, &original); err != nil {
		return nil, fmt.Errorf("dispatcher: HCSF raw passthrough controls parse failed: %w", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	for k, v := range original {
		if k == "max_completion_tokens" {
			if canonicalLimit, ok := out["max_tokens"]; ok {
				out["max_completion_tokens"] = canonicalLimit
				delete(out, "max_tokens")
			}
			continue
		}
		if _, modeled := hcsfModeledOpenAIChatRequestFields[k]; modeled {
			continue
		}
		if _, blocked := hcsfBlockedOpenAIChatRawPassthroughFields[k]; blocked {
			continue
		}
		out[k] = v
	}
	return json.Marshal(out)
}

var hcsfModeledOpenAIChatRequestFields = map[string]struct{}{
	"model":                 {},
	"messages":              {},
	"stream":                {},
	"max_tokens":            {},
	"max_completion_tokens": {},
	"temperature":           {},
	"top_p":                 {},
	"stop":                  {},
	"tools":                 {},
	"tool_choice":           {},
	"parallel_tool_calls":   {},
	"response_format":       {},
	"seed":                  {},
}

var hcsfBlockedOpenAIChatRawPassthroughFields = map[string]struct{}{
	"n": {},
}

func cloneHCSF(env *proto.HCSF) (*proto.HCSF, error) {
	b, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: clone HCSF marshal 失败: %w", err)
	}
	var out proto.HCSF
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("dispatcher: clone HCSF unmarshal 失败: %w", err)
	}
	return &out, nil
}

func fillUpstreamReported(env *proto.HCSF) {
	if env.Accounting.ModelChain == nil {
		env.Accounting.ModelChain = &proto.ModelChain{}
	}
	if env.Accounting.ModelChain.Requested == "" {
		env.Accounting.ModelChain.Requested = env.RequestMeta.Model
	}
	if env.Accounting.ModelChain.RouteDecided == "" {
		env.Accounting.ModelChain.RouteDecided = firstNonEmpty(env.RequestMeta.UpstreamModel, env.RequestMeta.Model)
	}
	if env.BufferedResponse != nil && env.BufferedResponse.Model != "" {
		env.Accounting.ModelChain.UpstreamReported = env.BufferedResponse.Model
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
