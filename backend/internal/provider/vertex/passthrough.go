// 包 vertex — Google Vertex AI serving 平台的出站请求适配器。
//
// Vertex AI 在 Google Cloud 的 aiplatform endpoint 上托管两类模型形态：
//   - Gemini-on-Vertex（publisher=google）：body 与 generativelanguage 的
//     Gemini API 同形（passthrough，不 reshape），action 走
//     generateContent / streamGenerateContent。
//   - Anthropic-on-Vertex（publisher=anthropic）：body 是 Anthropic Messages
//     形但需剥 model/stream + 注 anthropic_version（见 anthropic_body.go），
//     action 走 rawPredict / streamRawPredict。
//
// 两类由 PassthroughAdapter.Mode 在注册时确定（router 已知 protocol family，
// 不从 model 前缀推断），保持 adapter 行为确定。
//
// 鉴权：本 adapter 不签 JWT。凭据 Value 已是 "Bearer <accessToken>"（由
// credentialworker 的 metadata token 刷新链 materialize），adapter 只把它
// 注入 Authorization header（凭据 Extra["auth_header"]）+ X-Goog-User-Project。
//
// SSRF/path-injection 加固：host 恒为 *.googleapis.com 不可被攻击者控制；
// project / location / model 三个路径段全部经字符白名单校验（见
// validProjectOrLocation / validVertexLocation / validModelID）——白名单拒绝
// 任何不在合法字符集内的输入，比转义后再拼更严且不依赖转义往返，dated model
// 的 @ 因在白名单内得以原样保留；upstream_passthrough 的 base_url override 仍走
// EndpointForCredential 统一守卫。location 还拒首尾连字符，防畸形 host。
package vertex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// vertexMode 区分 Gemini-on-Vertex 与 Anthropic-on-Vertex 两种出站形态。
type vertexMode int

const (
	// ModeGemini Gemini-on-Vertex（publisher=google，body passthrough 直通）。
	ModeGemini vertexMode = iota
	// ModeAnthropic Anthropic-on-Vertex（publisher=anthropic，body reshape 重塑）。
	ModeAnthropic
)

// defaultLocation 是 location 未声明时的默认区域（GCP Vertex 公共默认区）。
const defaultLocation = "us-central1"

// globalLocation 是无区域前缀 host 的特殊 location 值。
const globalLocation = "global"

// PassthroughAdapter 把客户请求直通到 Vertex AI publishers/models endpoint。
type PassthroughAdapter struct {
	// Mode 决定 publisher / action / body 处理形态，注册时设定。
	Mode vertexMode
}

// Platform 返回平台标识。
func (a *PassthroughAdapter) Platform() string {
	return "vertex"
}

// AcceptableCredentialTypes 仅 upstream_passthrough（Value 已是 Bearer token）。
func (a *PassthroughAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 构造出站 *http.Request。
//
// 必需：in.Credential.Type=upstream_passthrough、Value 非空（Bearer token）、
// UpstreamModelID 非空且字符合法、Extra["project_id"] 非空且字符合法。
// 可选：Extra["location"]（默认 us-central1）、Extra["stream"]=="true" 流式
// （未设时回退读 InboundBody 顶层 "stream" 字段，见下）、Extra["auth_header"]。
func (a *PassthroughAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("vertex passthrough: 不支持的凭据形态 %q", in.Credential.Type)
	}
	if in.Credential.Value == "" {
		return nil, errors.New("vertex passthrough: 凭据 Value 为空")
	}
	if in.UpstreamModelID == "" {
		return nil, errors.New("vertex passthrough: UpstreamModelID 不能为空（URL 需要 model 名）")
	}
	if !validModelID(in.UpstreamModelID) {
		return nil, fmt.Errorf("vertex passthrough: UpstreamModelID %q 含非法字符（防 path 注入）", in.UpstreamModelID)
	}

	projectID := strings.TrimSpace(in.Credential.Extra["project_id"])
	if projectID == "" {
		return nil, errors.New("vertex passthrough: Extra[\"project_id\"] 不能为空（Vertex URL 需要 project）")
	}
	if !validProjectOrLocation(projectID) {
		return nil, fmt.Errorf("vertex passthrough: project_id %q 含非法字符（防 host/path 注入）", projectID)
	}

	location := strings.TrimSpace(in.Credential.Extra["location"])
	if location == "" {
		location = defaultLocation
	}
	if !validVertexLocation(location) {
		return nil, fmt.Errorf("vertex passthrough: location %q 非法（仅 global 或 region 形小写字母数字段，防 host/path 注入）", location)
	}

	// 流式判定(OR 链):Extra["stream"]=="true"(gemini ingress 经
	// credentialWithNativeStreamMode 注入)| ClientStreamIntent(跨协议 ingress
	// 的 resolved 流式意图——ModeGemini 的 marshal body 无顶层 stream 字段,
	// body 探测对它恒 false,无此兜底 openai/anthropic→vertex_gemini 流式会
	// 错选非流 :generateContent;Extra 已带显式值时不取 intent)| body 探测
	// (Anthropic ingress /v1/messages raw body 自带 "stream":true,必须据此选
	// streamRawPredict,同 bedrock adapter 从 body 派生 stream 的契约)。
	// 注意:显式 Extra="false" 压得住 intent,压不住 body 探测——raw body 的
	// stream 字段是 anthropic ingress 的真相源(既有契约,刻意保留)。
	stream := in.Credential.Extra["stream"] == "true" ||
		(in.Credential.Extra["stream"] == "" && in.ClientStreamIntent) ||
		inboundRequestsStream(in.InboundBody)
	publisher, action := a.publisherAction(stream)

	endpoint := buildVertexURL(location, projectID, publisher, in.UpstreamModelID, action)

	// upstream_passthrough 凭据自带 base_url override 仍走统一 SSRF 守卫。
	endpoint, err := provider.EndpointForCredential(endpoint, in.Credential)
	if err != nil {
		return nil, fmt.Errorf("vertex passthrough: endpoint rejected: %w", err)
	}

	// 流式追加 ?alt=sse（Vertex 用 query 标记 SSE 投影；已存在则不重复）。
	if stream {
		endpoint, err = provider.EndpointWithQueryParamIfMissing(endpoint, "alt", "sse")
		if err != nil {
			return nil, fmt.Errorf("vertex passthrough: 追加 alt=sse 失败: %w", err)
		}
	}

	// body：Gemini 原样直通；Anthropic 剥 model/stream + 注 anthropic_version。
	body := in.InboundBody
	if a.Mode == ModeAnthropic {
		reshaped, rerr := reshapeAnthropicBody(in.InboundBody)
		if rerr != nil {
			return nil, fmt.Errorf("vertex passthrough: Anthropic body reshape 失败: %w", rerr)
		}
		body = reshaped
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("vertex passthrough: 构造请求失败: %w", err)
	}

	// 凭据注入：Value 已是 "Bearer <tok>"，不再加前缀（避免双 Bearer）。
	if authHeader := in.Credential.Extra["auth_header"]; authHeader != "" {
		req.Header.Set(authHeader, in.Credential.Value)
	} else {
		req.Header.Set("Authorization", in.Credential.Value)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// X-Goog-User-Project：Cloud quota / billing 归属。
	req.Header.Set("X-Goog-User-Project", projectID)

	return req, nil
}

// publisherAction 按 Mode + stream 返回 (publisher, action)。
func (a *PassthroughAdapter) publisherAction(stream bool) (publisher, action string) {
	if a.Mode == ModeAnthropic {
		if stream {
			return "anthropic", "streamRawPredict"
		}
		return "anthropic", "rawPredict"
	}
	if stream {
		return "google", "streamGenerateContent"
	}
	return "google", "generateContent"
}

// vertexHost 按 location 返回 aiplatform host。global → 无区域前缀。
func vertexHost(location string) string {
	if location == globalLocation {
		return "aiplatform.googleapis.com"
	}
	return location + "-aiplatform.googleapis.com"
}

// buildVertexURL 构造 publishers/models endpoint：
//
//	https://{host}/v1/projects/{project}/locations/{location}/publishers/{publisher}/models/{model}:{action}
//
// project/location/model 已由 BuildRequest 经字符白名单校验，故原样拼接——
// 白名单已排除任何会破坏 routing 的字符（含 dated model 的 @），无需转义往返。
func buildVertexURL(location, project, publisher, model, action string) string {
	return fmt.Sprintf(
		"https://%s/v1/projects/%s/locations/%s/publishers/%s/models/%s:%s",
		vertexHost(location), project, location, publisher, model, action,
	)
}

// validProjectOrLocation 白名单校验 GCP project id / region 段：小写字母、
// 数字、连字符。空串视为非法。
func validProjectOrLocation(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// validVertexLocation 校验 location：global 直接放行；region 形要求首尾字母
// 数字、中间允许连字符——拒 '-foo'/'foo-'/'--' 这类会拼出畸形 host 的值。
func validVertexLocation(loc string) bool {
	if loc == globalLocation {
		return true
	}
	if loc == "" {
		return false
	}
	for i := 0; i < len(loc); i++ {
		c := loc[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && i > 0 && i < len(loc)-1:
		default:
			return false
		}
	}
	return true
}

// validModelID 白名单校验 Vertex model id：字母数字 + 点/下划线/连字符/@/斜杠
// （覆盖 owner/name 与 dated model name@YYYYMMDD 两种合法形）；其它字符拒。
func validModelID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == '@', r == '/':
		default:
			return false
		}
	}
	return true
}

// inboundRequestsStream 探测 InboundBody 顶层 "stream":true（OpenAI/Anthropic
// 形请求体的流式开关）。解析失败或字段缺失返回 false（保守按非流式）。
func inboundRequestsStream(body []byte) bool {
	var probe struct {
		Stream *bool `json:"stream"`
	}
	if json.Unmarshal(body, &probe) != nil || probe.Stream == nil {
		return false
	}
	return *probe.Stream
}

func (a *PassthroughAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, ok := range a.AcceptableCredentialTypes() {
		if ok == t {
			return true
		}
	}
	return false
}
