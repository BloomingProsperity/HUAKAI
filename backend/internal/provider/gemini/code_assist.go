// 包 gemini — Gemini Code Assist（cloudcode-pa v1internal）出站请求适配器。
//
// CodeAssistAdapter 把客户 Gemini 形请求出站到 Google 内部 Code Assist
// 后端 cloudcode-pa.googleapis.com/v1internal，纯 OAuth Bearer 鉴权（无
// api-key、无 x-goog-api-key）。这是 Gemini CLI / Code Assist 实际使用的
// 私有端点，与标准 generativelanguage AI Studio API key 路径不同。
//
// 承重差异（与 PassthroughAdapter 不同的两点）：
//
//   - 请求 envelope：cloudcode-pa 不收裸 generateContent body，外层必须包
//     {"model":<model>,"project":<projectID>,"request":<标准 gemini body>}，
//     真实 gemini body 嵌在 "request" 字段。
//   - 响应 envelope：上游回 {"response":{<gemini body>}}（非流与每个 SSE
//     chunk 都包一层），入站解析（proto/geminicodeassist 包）先 unwrap
//     "response" 再喂既有 gemini SSE 解析。
//
// 反封禁姿态（antiban-off-disclaimer memory）：仅加让调用能工作的最小必需
// header（User-Agent + X-Goog-Api-Client），不加额外 mimicry。
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// codeAssistBase 是 Code Assist 后端默认 base（不含 path），可被 Endpoint 覆盖。
const codeAssistBase = "https://cloudcode-pa.googleapis.com"

// codeAssistVersion 是 Code Assist 内部 API 版本段。
const codeAssistVersion = "v1internal"

const (
	codeAssistClientVersion = "0.51.0"
	codeAssistDefaultModel  = "gemini-2.5-pro"
)

// codeAssistAPIClient 是 X-Goog-Api-Client 的 genai-sdk 形 wire 值（事实
// wire 值，非可版权表达）。Code Assist 后端按此识别 SDK 客户端。
const codeAssistAPIClient = "google-genai-sdk/1.0 gl-go/1.0"

// 编译期接口合规断言。
var _ provider.Adapter = (*CodeAssistAdapter)(nil)

// ApplyCodeAssistHeaders 统一生成请求与项目初始化请求使用的客户端身份头。
func ApplyCodeAssistHeaders(header http.Header) {
	if header == nil {
		return
	}
	header.Set("User-Agent", defaultCodeAssistUserAgent(codeAssistDefaultModel))
	header.Set("X-Goog-Api-Client", codeAssistAPIClient)
}

func defaultCodeAssistUserAgent(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		model = codeAssistDefaultModel
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	} else if arch == "386" {
		arch = "ia32"
	}
	return fmt.Sprintf("GeminiCLI/%s/%s (%s; %s; terminal)", codeAssistClientVersion, model, runtime.GOOS, arch)
}

// CodeAssistAdapter 将客户 Gemini 形请求出站到 cloudcode-pa Code Assist 后端。
type CodeAssistAdapter struct {
	// Endpoint 覆盖默认 base（含 scheme+host，不含 path），供 httptest 注入。
	// 空值时使用 codeAssistBase。
	Endpoint string
	// UserAgent 与 APIClient 可为共用 Cloud Code 线协议的客户端设置已确认的
	// 静态身份头；空值保持 Gemini Code Assist 既有值。
	UserAgent string
	APIClient string
	// EnabledCreditTypes 只在调用方明确提供时进入顶层 envelope；nil/空切片
	// 完全保持既有 Gemini Code Assist 请求形态。
	EnabledCreditTypes []string
}

// Platform 返回平台标识。
func (a *CodeAssistAdapter) Platform() string {
	return "gemini_code_assist"
}

// AcceptableCredentialTypes 声明接受的凭据形态：纯 OAuth/session token 与
// upstream_passthrough；显式拒 apikey（cloudcode-pa 不收 api-key）。
func (a *CodeAssistAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeOAuthAccessToken,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// codeAssistEnvelope 是出站请求的承重外层 envelope。
//
// inner gemini body 用 json.RawMessage 携带，避免重 marshal（保 byte-for-byte）。
type codeAssistEnvelope struct {
	Model              string          `json:"model"`
	Project            string          `json:"project"`
	Request            json.RawMessage `json:"request"`
	EnabledCreditTypes []string        `json:"enabledCreditTypes,omitempty"`
}

// BuildRequest 构造出站到 cloudcode-pa 的 *http.Request。
//
// 校验顺序：
//  1. 凭据形态白名单（apikey 不在白名单内，自然被拒）
//  2. Credential.Value 去空白后非空（OAuth Bearer token）
//  3. UpstreamModelID 去空白后非空（envelope 顶层 model）
//  4. InboundBody 非空（envelope 内层 request 不能空）
//  5. project_id 非空（cloudcode-pa 拒空 project，fail loud）
//  6. 流式判定 → action generateContent / streamGenerateContent
//  7. endpoint = base + "/v1internal:" + action，经 EndpointForBuildInput 统一
//     SSRF 守卫 + 流式追加 ?alt=sse
//  8. body envelope {model, project, request} 请求信封
//  9. headers 请求头：Authorization Bearer + Content-Type/Accept + UA + X-Goog-Api-Client
func (a *CodeAssistAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("gemini code assist: 不支持的凭据形态 %q（cloudcode-pa 仅 OAuth/session，拒 apikey）", in.Credential.Type)
	}
	if strings.TrimSpace(in.Credential.Value) == "" {
		return nil, errors.New("gemini code assist: 凭据 Value 为空（需 OAuth access token）")
	}
	discoveryRequest := isCodeAssistModelDiscovery(in)
	if !discoveryRequest && strings.TrimSpace(in.UpstreamModelID) == "" {
		return nil, errors.New("gemini code assist: UpstreamModelID 不能为空（envelope 需要 model 名）")
	}
	if !discoveryRequest && len(bytes.TrimSpace(in.InboundBody)) == 0 {
		return nil, errors.New("gemini code assist: InboundBody 为空（envelope 内层 request 不能空）")
	}

	// project：cloudcode-pa 拒空 project，fail loud（不静默送空）。
	projectID := strings.TrimSpace(in.Credential.Extra["project_id"])
	if projectID == "" {
		return nil, errors.New("gemini code assist: code assist requires project_id（cloudcode-pa 拒空 project，需 onboarding 解析）")
	}

	// 流式判定(OR 链):Extra["stream"]=="true"(gemini ingress 经
	// credentialWithNativeStreamMode 注入,gated on clientProtocol==gemini)|
	// ClientStreamIntent(跨协议 ingress 的 resolved 流式意图——内层 gemini
	// body 用 URL action 表达流式、无顶层 stream 字段,body 探测对它恒 false;
	// Extra 已带显式值时不取 intent)| body 探测(直灌带顶层 stream 的 body
	// 的兜底;显式 Extra="false" 压 intent 但不压 body 探测,既有契约)。
	stream := !discoveryRequest && (in.Credential.Extra["stream"] == "true" ||
		(in.Credential.Extra["stream"] == "" && in.ClientStreamIntent) ||
		inboundGeminiBodyRequestsStream(in.InboundBody))

	action := "generateContent"
	if stream {
		action = "streamGenerateContent"
	}

	base := strings.TrimSpace(a.Endpoint)
	if base == "" {
		base = codeAssistBase
	}
	endpoint := base + "/" + codeAssistVersion + ":" + action

	// EndpointPath override + upstream_passthrough base_url + 统一 SSRF 守卫。
	endpoint, err := provider.EndpointForBuildInput(endpoint, in)
	if err != nil {
		return nil, fmt.Errorf("gemini code assist: endpoint rejected: %w", err)
	}

	// 流式追加 ?alt=sse（已存在则不重复）。
	if stream {
		endpoint, err = provider.EndpointWithQueryParamIfMissing(endpoint, "alt", "sse")
		if err != nil {
			return nil, fmt.Errorf("gemini code assist: 追加 alt=sse 失败: %w", err)
		}
	}

	// body envelope：inner gemini body 原样作 RawMessage 嵌入 "request"。
	var outBody []byte
	if discoveryRequest {
		outBody, err = json.Marshal(map[string]string{"project": projectID})
	} else {
		outBody, err = json.Marshal(codeAssistEnvelope{
			Model:              in.UpstreamModelID,
			Project:            projectID,
			Request:            json.RawMessage(in.InboundBody),
			EnabledCreditTypes: a.EnabledCreditTypes,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("gemini code assist: envelope marshal 失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(outBody))
	if err != nil {
		return nil, fmt.Errorf("gemini code assist: 构造请求失败: %w", err)
	}

	// 鉴权：纯 OAuth Bearer。upstream_passthrough 的 Value 已是完整 header 值
	// （credentialstore RuntimeUpstreamPassthrough 已预置 "Bearer "），原样注入；
	// session/oauth 的 Value 是裸 token，需加 "Bearer " 前缀。
	if in.Credential.Type == provider.CredentialTypeUpstreamPassthrough {
		if authHeader := in.Credential.Extra["auth_header"]; authHeader != "" {
			req.Header.Set(authHeader, in.Credential.Value)
		} else {
			req.Header.Set("Authorization", in.Credential.Value)
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+in.Credential.Value)
	}

	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}

	// 最小必需 header（反封禁姿态：仅功能必需，不加额外 mimicry）。
	userAgent := strings.TrimSpace(a.UserAgent)
	if userAgent == "" {
		userAgent = defaultCodeAssistUserAgent(in.UpstreamModelID)
	}
	apiClient := strings.TrimSpace(a.APIClient)
	if apiClient == "" {
		apiClient = codeAssistAPIClient
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Goog-Api-Client", apiClient)

	return req, nil
}

func isCodeAssistModelDiscovery(in provider.BuildInput) bool {
	if method := strings.ToUpper(strings.TrimSpace(in.HTTPMethod)); method != "" && method != http.MethodPost {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(in.EndpointPath), "/v1internal:fetchAvailableModels")
}

// inboundGeminiBodyRequestsStream 探测内层 gemini body 顶层 "stream":true。
// 裸 gemini generateContent body 通常无顶层 stream 字段（流式由 URL action
// 表达），故此探测几乎总返回 false——它仅作非 gemini-ingress 跨协议路径的
// 保守回退，主信号是 Credential.Extra["stream"]。解析失败/字段缺失返回 false。
func inboundGeminiBodyRequestsStream(body []byte) bool {
	var probe struct {
		Stream *bool `json:"stream"`
	}
	if json.Unmarshal(body, &probe) != nil || probe.Stream == nil {
		return false
	}
	return *probe.Stream
}

func (a *CodeAssistAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, ok := range a.AcceptableCredentialTypes() {
		if ok == t {
			return true
		}
	}
	return false
}
