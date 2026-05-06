// 包 bedrock — AWS Bedrock 平台的出站请求适配器。
//
// 实现 provider.Adapter 接口，将客户请求直通转发到 AWS Bedrock Runtime
// endpoint，并完成 AWS SigV4 签名。
//
// 设计边界：
//   - 本 adapter 不对 body 做任何 reshape（Bedrock 各模型 body 形态差异极大，
//     由上游调用方或 protocol-translation 层保证 body 已是目标模型期望的形态）。
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
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// endpointInvoke 是 Bedrock Runtime invoke endpoint 模板（非流式）。
// {region} 和 {model_id} 在运行时替换。
const endpointInvoke = "https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke"

// endpointInvokeStream 是 Bedrock Runtime invoke-with-response-stream endpoint 模板。
const endpointInvokeStream = "https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke-with-response-stream"

// PassthroughAdapter 实现 provider.Adapter，把客户原始请求直通转发到
// AWS Bedrock Runtime，使用 SigV4 签名（aws_sigv4 模式）或透传
// Authorization header（upstream_passthrough 模式）。
type PassthroughAdapter struct {
	// Endpoint 覆盖默认 endpoint 模板。空串时按 region + model_id 自动拼接。
	// 主要供单元测试注入本地 httptest.Server。
	Endpoint string

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
	region := in.Credential.Extra["aws_region"]
	if region == "" {
		return nil, errors.New("bedrock passthrough: Extra[\"aws_region\"] 不能为空")
	}

	// 模型 ID 必填
	if in.UpstreamModelID == "" {
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

	// 构造 endpoint URL
	endpoint, err := a.buildEndpoint(region, in.UpstreamModelID, in.Credential.Extra["stream"] == "true")
	if err != nil {
		return nil, fmt.Errorf("bedrock passthrough: 构造 endpoint 失败: %w", err)
	}

	// 构造请求（body 原样透传）
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(in.InboundBody))
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
		if err := signer.Sign(req, in.InboundBody); err != nil {
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
