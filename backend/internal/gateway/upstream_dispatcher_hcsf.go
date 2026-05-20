package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

type HCSFDispatchInput struct {
	ProtocolFamily  string
	UpstreamModelID string
	Account         provider.AccountInfo
	Credential      provider.Credential
	TransportMode   transport.TransportMode
	RawBody         []byte
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
	}, env, endpointFamily, in.RawBody)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: BuildRequestFromEnvelope/BuildRequest 失败: %w", err)
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
		rt, err = d.applyProxy(ctx, rt, account.AccountID)
		if err != nil {
			return nil, err
		}
		client = &http.Client{Transport: rt}
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
	responseEnv, _, err := upstreamAdapter.ProviderResponseToCanonical(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: ProviderResponseToCanonical 失败: %w", err)
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
	fillUpstreamReported(out)
	return out, nil
}

func buildHCSFProviderRequest(ctx context.Context, a provider.Adapter, in provider.BuildInput, env *proto.HCSF, endpointFamily string, rawFallback []byte) (*http.Request, error) {
	if b, ok := a.(envelopeRequestBuilder); ok {
		return b.BuildRequestFromEnvelope(ctx, in, env)
	}
	body, err := hcsfRequestBody(env, endpointFamily)
	if err != nil {
		if len(rawFallback) == 0 {
			return nil, err
		}
		body = rawFallback
	}
	in.InboundBody = body
	return a.BuildRequest(ctx, in)
}

func hcsfRequestBody(env *proto.HCSF, endpointFamily string) ([]byte, error) {
	body, err := MarshalToProviderRequest(env, endpointFamily)
	if err != nil {
		return nil, err
	}
	body, err = injectRequestControls(body, env, endpointFamily)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: HCSF request controls 注入失败: %w", err)
	}
	return body, nil
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
