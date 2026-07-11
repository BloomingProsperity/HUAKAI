// 包 antigravity 提供个人 Google OAuth 凭据到 Cloud Code 上游的反转适配器。
package antigravity

import (
	"context"
	"fmt"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/gemini"
)

const (
	antigravityIDEVersion          = "2.2.1"
	defaultAntigravityUserAgent    = "antigravity/hub/" + antigravityIDEVersion + " darwin/arm64"
	defaultAntigravityAPIClient    = "google-genai-sdk/1.0 gl-go/1.0"
	antigravityGoogleOneCreditType = "GOOGLE_ONE_AI"
)

// 编译期接口合规断言。
var _ provider.Adapter = (*AntigravitySessionAdapter)(nil)

// AntigravitySessionAdapter 复用 Gemini Code Assist 的 Cloud Code wire 形态，
// 仅覆盖 Antigravity 已确认的客户端身份与 Google One AI credit 类型。
type AntigravitySessionAdapter struct {
	// Endpoint 覆盖 Cloud Code base（含 scheme+host，不含版本与动作路径），
	// 仅供受控 wiring 与测试注入；空值使用正式 cloudcode-pa 端点。
	Endpoint string
}

// Platform 返回传输层使用的平台标识。
func (a *AntigravitySessionAdapter) Platform() string {
	return "antigravity"
}

// AcceptableCredentialTypes 与 Cloud Code OAuth 车道保持一致。
func (a *AntigravitySessionAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return (&gemini.CodeAssistAdapter{}).AcceptableCredentialTypes()
}

// BuildRequest 构造 Cloud Code v1internal 生成请求。
func (a *AntigravitySessionAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	endpoint := ""
	if a != nil {
		endpoint = a.Endpoint
	}
	delegate := &gemini.CodeAssistAdapter{
		Endpoint:           endpoint,
		UserAgent:          defaultAntigravityUserAgent,
		APIClient:          defaultAntigravityAPIClient,
		EnabledCreditTypes: []string{antigravityGoogleOneCreditType},
	}
	req, err := delegate.BuildRequest(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("antigravity session: %w", err)
	}
	return req, nil
}
