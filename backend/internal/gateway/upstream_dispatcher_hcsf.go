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
	ProtocolFamily  string
	UpstreamModelID string
	Account         provider.AccountInfo
	Credential      provider.Credential
	TransportMode   transport.TransportMode
	RawBody         []byte
	BodyControls    DispatchBodyControls
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

type envelopeRequestBuilder interface {
	BuildRequestFromEnvelope(context.Context, provider.BuildInput, *proto.HCSF) (*http.Request, error)
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
		UpstreamModelID: upstreamModel,
		Credential:      in.Credential,
		Account:         account,
	}, env, ingressFamily, endpointFamily, in.RawBody, in.BodyControls)
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

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: HTTP Do 失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("dispatcher: 读取上游响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 用类型化错误把 status + body 透传到 caller, 由 caller 决定 client 返回
		// 状态码 / 走 health classification / 触发 cooldown. 不能塌成 string-only,
		// 否则 chat handler 总是 502 + status=0 health signal 跟流式路径行为分叉。
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
	responseEnv, respLosses, err := upstreamAdapter.ProviderResponseToCanonical(ctx, raw)
	if err != nil {
		if reconstructedEnv, reconstructedLosses, ok := protosse.ReconstructBufferedFromSSE(upstreamAdapter, raw); ok && reconstructedEnv != nil {
			responseEnv = reconstructedEnv
			respLosses = reconstructedLosses
		} else {
			return nil, fmt.Errorf("dispatcher: ProviderResponseToCanonical 失败: %w", err)
		}
	}
	if responseEnv == nil || responseEnv.BufferedResponse == nil {
		return nil, errors.New("dispatcher: upstream adapter 未返回 buffered_response")
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

func buildHCSFProviderRequest(ctx context.Context, a provider.Adapter, in provider.BuildInput, env *proto.HCSF, ingressFamily string, endpointFamily string, nativeRawBody []byte, controlsOpt ...DispatchBodyControls) (*http.Request, error) {
	var controls DispatchBodyControls
	if len(controlsOpt) > 0 {
		controls = controlsOpt[0]
	}
	if b, ok := a.(envelopeRequestBuilder); ok {
		req, err := b.BuildRequestFromEnvelope(ctx, in, env)
		if err != nil {
			return nil, err
		}
		return applyRequestBodyControls(req, controls)
	}
	if hcsfProviderRequestUsesNativeRawBody(endpointFamily) {
		if len(nativeRawBody) == 0 {
			return nil, fmt.Errorf("dispatcher: HCSF native raw body missing for endpoint family %q", endpointFamily)
		}
		body, err := ApplyDispatchBodyControls(nativeRawBody, controls)
		if err != nil {
			return nil, err
		}
		// RR-03: enforce Anthropic extended-thinking validity (temperature=1 /
		// tool_choice auto) before forwarding, else upstream 400; no-op (byte-
		// identical) when the body has no active thinking field.
		body = thinkingnorm.NormalizeThinkingValidity(body)
		in.InboundBody = body
		return a.BuildRequest(ctx, in)
	}
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
	body, err := MarshalToProviderRequest(env, modeledFamily)
	if err != nil {
		return nil, err
	}
	body, err = injectRequestControls(body, env, modeledFamily)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: HCSF request controls 注入失败: %w", err)
	}
	return body, nil
}

func hcsfProviderRequestModelFamily(endpointFamily string) string {
	switch endpointFamily {
	case "openrouter_chat", "grok_chat", "deepseek_chat", "mistral_chat", "groqcloud_chat", "together_chat", "perplexity_chat", "fireworks_chat",
		"copilot_session", "antigravity_session", "kiro_session", "windsurf_session":
		return "openai_chat"
	default:
		return endpointFamily
	}
}

func hcsfProviderRequestUsesNativeRawBody(endpointFamily string) bool {
	switch endpointFamily {
	case "bedrock_invoke", "openai_codex":
		return true
	default:
		return false
	}
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
