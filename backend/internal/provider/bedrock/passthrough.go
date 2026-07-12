// 包 bedrock — AWS Bedrock 平台的出站请求适配器。
//
// 实现 provider.Adapter 接口，将客户请求直通转发到 AWS Bedrock Runtime
// endpoint，并完成 AWS SigV4 签名。
//
// 设计边界：
//   - 默认 adapter 不对 body 做任何 reshape（Bedrock 各模型 body 形态差异极大，
//     由上游调用方或 protocol-translation 层保证 body 已是目标模型期望的形态）。
//   - 当 AutoTranslateAnthropicAPIBody=true 时，adapter 在签名前会做两步可控
//     的 body 变换：(1) Anthropic Messages API → Bedrock invoke 翻译；
//     (2) Track C 自动 cache_control 注入（长 system prompt 自动命中缓存）。
//     默认 false 保持纯 passthrough。
//   - SigV4 签名完全自实现（见 sigv4.go），不依赖 aws-sdk-go 或任何第三方库。
//   - CredentialTypeUpstreamPassthrough 模式下 caller 已预签名，adapter 仅
//     注入 Authorization header value，不重新签名。
package bedrock

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/cache_routing"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// endpointInvoke 是 Bedrock Runtime invoke endpoint 模板（非流式）。
// {region} 和 {model_id} 在运行时替换。
const endpointInvoke = "https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke"

// endpointInvokeStream 是 Bedrock Runtime invoke-with-response-stream endpoint 模板。
const endpointInvokeStream = "https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke-with-response-stream"

// validBedrockRegions 是 Bedrock Runtime 当前允许拼入官方 endpoint 的区域白名单。
// 新增 AWS 区域时必须显式补表,避免运营配置污染把任意 host 片段拼进出站地址。
var validBedrockRegions = map[string]struct{}{
	"af-south-1":     {},
	"ap-east-2":      {},
	"ap-northeast-1": {},
	"ap-northeast-2": {},
	"ap-northeast-3": {},
	"ap-south-1":     {},
	"ap-south-2":     {},
	"ap-southeast-1": {},
	"ap-southeast-2": {},
	"ap-southeast-3": {},
	"ap-southeast-4": {},
	"ap-southeast-5": {},
	"ap-southeast-6": {},
	"ap-southeast-7": {},
	"ca-central-1":   {},
	"ca-west-1":      {},
	"eu-central-1":   {},
	"eu-central-2":   {},
	"eu-north-1":     {},
	"eu-south-1":     {},
	"eu-south-2":     {},
	"eu-west-1":      {},
	"eu-west-2":      {},
	"eu-west-3":      {},
	"il-central-1":   {},
	"me-central-1":   {},
	"me-south-1":     {},
	"sa-east-1":      {},
	"us-east-1":      {},
	"us-east-2":      {},
	"us-gov-east-1":  {},
	"us-gov-west-1":  {},
	"us-west-1":      {},
	"us-west-2":      {},
}

// PassthroughAdapter 实现 provider.Adapter，把客户原始请求直通转发到
// AWS Bedrock Runtime，使用 SigV4 签名（aws_sigv4 模式）或透传
// Authorization header（upstream_passthrough 模式）。
type PassthroughAdapter struct {
	// Endpoint 覆盖默认 endpoint 模板。空串时按 region + model_id 自动拼接。
	// 主要供单元测试注入本地 httptest.Server。
	Endpoint string

	// AutoTranslateAnthropicAPIBody 启用 Anthropic Messages API → Bedrock
	// 形态自动翻译（A8 闭环原子）。
	//
	// 当 true 时，BuildRequest 在签名前调 TranslateAnthropicAPIToBedrock：
	//   - 剥离 body 中的 model 字段（Bedrock URL 已含 model_id）
	//   - 剥离 body 中的 stream 字段（Bedrock 用 endpoint 选流式）
	//   - 注入 anthropic_version: "bedrock-2023-05-31"
	// 让 Anthropic CLI 等客户端的原始请求体可直接路由到 Bedrock。
	//
	// 默认 false 保持原有 passthrough 语义（caller 已发 Bedrock 形态 body）。
	AutoTranslateAnthropicAPIBody bool

	// nowFunc 注入当前时间（测试用），nil 时使用 time.Now()。
	nowFunc func() time.Time
}

// Platform 返回平台标识。
func (a *PassthroughAdapter) Platform() string {
	return "bedrock"
}

// AcceptableCredentialTypes 列出本 adapter 支持的凭据形态：
//   - aws_sigv4：AKID + SecretKey（+ 可选 SessionToken），adapter 负责签名；
//   - upstream_passthrough：caller 已完成签名，Authorization header value 直注入。
func (a *PassthroughAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeAWSSigV4,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 构造出站 *http.Request。
//
// 必需字段：
//   - in.Credential.Extra["aws_region"]          — AWS 区域
//   - in.UpstreamModelID                         — Bedrock 模型 ID（含 vendor 前缀，如
//     "anthropic.claude-3-5-sonnet-20241022-v2:0"）
//   - aws_sigv4 模式：in.Credential.Extra["aws_access_key_id"] + in.Credential.Value（SecretKey）
//
// 可选字段：
//   - in.Credential.Extra["aws_session_token"]   — STS 临时令牌
//   - in.Credential.Extra["stream"] == "true"    — 使用流式 endpoint
func (a *PassthroughAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	// 凭据形态校验
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("bedrock passthrough: 不支持的凭据形态 %q", in.Credential.Type)
	}

	// 区域必填
	region := strings.TrimSpace(in.Credential.Extra["aws_region"])
	if region == "" {
		return nil, errors.New("bedrock passthrough: Extra[\"aws_region\"] 不能为空")
	}
	if !validBedrockRegion(region) {
		return nil, fmt.Errorf("bedrock passthrough: aws_region %q 非法（仅允许 Bedrock Runtime 区域代码）", region)
	}

	// AutoTranslate 是否对当前请求生效：
	//   - upstream_passthrough 模式: caller 已签 SigV4 over original body，
	//     adapter 改 body 会让 SigV4 hash 失配 → 必须保持 raw passthrough
	//   - 非 Anthropic Messages 形态 body: bedrock_invoke 同时承载 Cohere /
	//     Llama / Mistral / Titan 等多家 vendor，盲翻译会破坏其它 vendor
	//     body（注 anthropic_version 等无关字段）→ 仅 Anthropic 形态才翻译
	autoTranslate := a.AutoTranslateAnthropicAPIBody &&
		in.Credential.Type != provider.CredentialTypeUpstreamPassthrough &&
		IsAnthropicMessagesShape(in.InboundBody)

	// 模型 ID 必填——但 AutoTranslate 模式下若 caller 未填，从 body 抽取
	// （sonnet F1/F7 闭环修复——否则 Anthropic CLI body 含 "model" 字段时
	// translator 抽到的 UpstreamModelID 被丢弃，闭环失败）。
	modelID := in.UpstreamModelID
	if modelID == "" && autoTranslate {
		// 提前 peek 一次 translator（在主翻译块之前），仅用于 fallback model ID。
		// 解析失败让主翻译块在下面正常报错，不在这里 short-circuit。
		if peek, perr := TranslateAnthropicAPIToBedrock(in.InboundBody); perr == nil {
			modelID = peek.UpstreamModelID
		}
	}
	if modelID == "" {
		return nil, errors.New("bedrock passthrough: UpstreamModelID 不能为空")
	}

	// aws_sigv4 模式额外校验
	if in.Credential.Type == provider.CredentialTypeAWSSigV4 {
		if in.Credential.Extra["aws_access_key_id"] == "" {
			return nil, errors.New("bedrock passthrough: aws_sigv4 模式下 Extra[\"aws_access_key_id\"] 不能为空")
		}
		if in.Credential.Value == "" {
			return nil, errors.New("bedrock passthrough: aws_sigv4 模式下凭据 Value（SecretKey）不能为空")
		}
	}

	// A8 闭环原子: AutoTranslateAnthropicAPIBody 开启时把 Anthropic API 形
	// body 翻译成 Bedrock 形，并允许翻译结果决定 stream 标志（caller 仍可
	// 通过 Extra["stream"] 显式覆盖）。in.UpstreamModelID 始终优先（管理员
	// model alias 配置权威）。autoTranslate 已经把 credential type + body
	// shape 两道闸门考虑进去。
	body := in.InboundBody
	stream := in.Credential.Extra["stream"] == "true"
	if autoTranslate {
		translated, terr := TranslateAnthropicAPIToBedrock(in.InboundBody)
		if terr != nil {
			return nil, fmt.Errorf("bedrock passthrough: Anthropic API 翻译失败: %w", terr)
		}
		body = translated.Body
		// 仅当 caller 没有显式声明 stream Extra 时使用翻译器结果
		if _, set := in.Credential.Extra["stream"]; !set {
			stream = translated.Stream
		}
		// Track C: 翻译后顺手自动注入 system cache_control 让 vendor 缓存
		// 长 system prompt（≥ 4096 bytes 默认阈值）。caller 已显式声明
		// cache_control 时不动. 与 Track B sticky routing 联动 → 万人级
		// SaaS 共享 system prompt 自动命中缓存。
		// hot-path 优化: 只在 body 还没 cache_control marker 时才尝试注入,
		// 避免每请求都跑 unmarshal/marshal cycle (sonnet SHOULD_FIX)。
		if !cache_routing.HasCacheControlMarker(body) {
			body = cache_routing.AutoInjectSystemCacheControl(body, 0)
		}
	}

	// 构造 endpoint URL（用 modelID，可能是 caller 给的或 translator 抽的）
	endpoint, err := a.buildEndpoint(region, modelID, stream)
	if err != nil {
		return nil, fmt.Errorf("bedrock passthrough: 构造 endpoint 失败: %w", err)
	}

	// 构造请求（body 已可能被翻译）
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bedrock passthrough: 构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// 注入凭据
	switch in.Credential.Type {
	case provider.CredentialTypeAWSSigV4:
		// 自签名路径：用 sigV4Signer 注入所有签名 header
		now := time.Now()
		if a.nowFunc != nil {
			now = a.nowFunc()
		}
		signer := &sigV4Signer{
			region:  region,
			service: sigV4Service,
			creds: sigV4Credentials{
				AccessKeyID:  in.Credential.Extra["aws_access_key_id"],
				SecretKey:    in.Credential.Value,
				SessionToken: in.Credential.Extra["aws_session_token"],
			},
			now: now,
		}
		// 注意: 用 body（可能已翻译）签名，而不是原始 InboundBody——
		// 否则 SigV4 hash 与实际发出的 body 不匹配。
		if err := signer.Sign(req, body); err != nil {
			return nil, fmt.Errorf("bedrock passthrough: SigV4 签名失败: %w", err)
		}

	case provider.CredentialTypeUpstreamPassthrough:
		// caller 已预签名，直接注入 Authorization header value
		req.Header.Set("Authorization", in.Credential.Value)
	}

	return req, nil
}

// buildEndpoint 根据区域、模型 ID（URL 编码）和流式标志构造完整 endpoint URL。
// 若 PassthroughAdapter.Endpoint 非空则直接使用（测试覆盖用）。
func (a *PassthroughAdapter) buildEndpoint(region, modelID string, stream bool) (string, error) {
	if a.Endpoint != "" {
		return a.Endpoint, nil
	}

	// 对 modelID 做 path segment 编码：Bedrock model id 含 "." 和 ":" 等字符。
	// url.PathEscape 不编码 ":"（RFC 3986 路径中允许），但 AWS Bedrock URL 规范
	// 要求对 ":" 编码（避免与端口或协议混淆）。使用 awsURIEncode 确保严格编码。
	encodedModel := awsURIEncode(modelID)

	if stream {
		return fmt.Sprintf(endpointInvokeStream, region, encodedModel), nil
	}
	return fmt.Sprintf(endpointInvoke, region, encodedModel), nil
}

// acceptsCredential 检查凭据形态是否在可接受集合内。
func (a *PassthroughAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, ok := range a.AcceptableCredentialTypes() {
		if ok == t {
			return true
		}
	}
	return false
}

func validBedrockRegion(region string) bool {
	_, ok := validBedrockRegions[region]
	return ok
}
