// 包 replicate — Replicate 图片生成 API 的出站请求适配器。
//
// 端点形态:POST https://api.replicate.com/v1/models/{model}/predictions,
// model 是 "owner/name" 双段 id,直接落在 URL path 里(逐段 PathEscape,
// 段间 "/" 是字面路径分隔)。鉴权 Authorization: Bearer <token>。
//
// Prefer: wait 与 Cancel-After 是计费正确性的两道承重墙:前者让上游同步等待
// prediction 完成，后者保证响应丢失、客户端断连或显式取消失败时，上游任务也
// 不会越过同一等待窗口继续烧成本。响应侧对非 succeeded 状态 fail-loud，
// 三道都在，删任何一道相应测试转红。
//
// 请求 body 由 request_translator 把 OpenAI images 形翻译为 {"input":{...}}
// (in-adapter 翻译先例:bedrock 的 AutoTranslateAnthropicAPIBody)。
package replicate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// defaultPredictionsEndpointTemplate 是默认 endpoint 模板。{model} 由
// UpstreamModelID 逐段 escape 后替换。
const defaultPredictionsEndpointTemplate = "https://api.replicate.com/v1/models/{model}/predictions"

// imagesGenerationsLanePath 是图片 lane(imageshttp)对所有 family 统一下发的
// 入站 endpoint path。对 Replicate 它只是"generations 语义"的标记——真实出站
// 路径由 model 推导,不能拿它当 path 用。
const imagesGenerationsLanePath = "/v1/images/generations"

// preferWaitSecondsDefault / preferWaitSecondsMax:Prefer: wait 同步等待窗口的
// 默认与上限秒数(上游对 wait 的硬上限即 60)。
const (
	preferWaitSecondsDefault = 60
	preferWaitSecondsMax     = 60
)

// Adapter 把 OpenAI images 形请求翻译并发往 Replicate predictions 端点。
type Adapter struct {
	// Endpoint 覆盖默认 endpoint 模板(必须含 {model} 占位符)。空值保持
	// defaultPredictionsEndpointTemplate。
	Endpoint string
}

// Platform 返回平台标识。
func (a *Adapter) Platform() string {
	return "replicate"
}

// AcceptableCredentialTypes 仅 apikey 与 upstream_passthrough。
func (a *Adapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeAPIKey,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 构造出站请求。UpstreamModelID 必填(进 URL path);
// in.InboundBody 是 OpenAI images 形 JSON,经 TranslateImageRequest 翻译。
func (a *Adapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("replicate: 不支持的凭据形态 %q", in.Credential.Type)
	}
	if in.Credential.Value == "" {
		return nil, errors.New("replicate: 凭据 Value 为空")
	}
	if cancelID, ok := predictionIDFromCancelPath(in.EndpointPath); ok {
		return buildCancelRequest(ctx, in.Credential, cancelID)
	}
	if strings.TrimSpace(in.UpstreamModelID) == "" {
		return nil, errors.New("replicate: UpstreamModelID 不能为空(endpoint path 需要 model id)")
	}

	template, err := a.endpointTemplate(in.EndpointPath)
	if err != nil {
		return nil, err
	}
	// 先替换 {model} 占位再走 EndpointForCredential。顺序倒置的话其内部
	// url.Parse 会把 "{" "}" 转成 %7B %7D,占位符再也替换不掉(同 gemini
	// passthrough 的顺序坑)。model 逐段 PathEscape:owner/name 间的 "/" 是
	// 字面路径分隔必须保留,段内保留字符必须转义。
	substituted := strings.ReplaceAll(template, "{model}", escapeModelPath(in.UpstreamModelID))
	// API key 或 upstream_passthrough 凭据自带 base_url 时优先使用；统一
	// SSRF 守卫在其内部，adapter 不得自行拼 endpoint 绕过。
	endpoint, err := provider.EndpointForCredential(substituted, in.Credential)
	if err != nil {
		return nil, fmt.Errorf("replicate: endpoint rejected: %w", err)
	}

	body, err := TranslateImageRequest(in.InboundBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("replicate: 构造请求失败: %w", err)
	}

	applyCredentialAuth(req, in.Credential)
	// 等待与自动取消共用同一秒数，避免两套配置漂移：同步窗口结束时任务必须
	// 同时进入上游取消边界，不能在本侧退款后继续运行。
	waitSeconds := configuredWaitSeconds(in.Credential)
	req.Header.Set("Prefer", fmt.Sprintf("wait=%d", waitSeconds))
	req.Header.Set("Cancel-After", fmt.Sprintf("%ds", waitSeconds))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// applyCredentialAuth 按凭据形态设置鉴权 header;BuildRequest 与 NewCancelRequest
// 共用同一口径,口径分叉=cancel 对自托管/代理凭据失效。
func applyCredentialAuth(req *http.Request, cred provider.Credential) {
	switch cred.Type {
	case provider.CredentialTypeAPIKey:
		req.Header.Set("Authorization", "Bearer "+cred.Value)
	case provider.CredentialTypeUpstreamPassthrough:
		// 透传凭据自带前缀;header 名可由 Extra["auth_header"] 覆盖。
		header := strings.TrimSpace(cred.Extra["auth_header"])
		if header == "" {
			header = "Authorization"
		}
		req.Header.Set(header, cred.Value)
	}
}

// configuredWaitSeconds 解析同步等待与自动取消共用的账号级窗口。非法值
// fail-safe 回默认并告警：默认值永远合法，不能让一个垃圾配置把整账号打成 502。
func configuredWaitSeconds(cred provider.Credential) int {
	rawValue := strings.TrimSpace(cred.Extra["prefer_wait_seconds"])
	if rawValue == "" {
		return preferWaitSecondsDefault
	}
	seconds, err := strconv.Atoi(rawValue)
	if err != nil || seconds < 1 || seconds > preferWaitSecondsMax {
		slog.Warn("replicate: prefer_wait_seconds 非法,回落默认",
			"value", rawValue,
			"default_seconds", preferWaitSecondsDefault,
		)
		return preferWaitSecondsDefault
	}
	return seconds
}

// endpointTemplate 解析 EndpointPath 覆盖语义:
//   - 空值 / 图片 lane 的 generations path → 默认(或 a.Endpoint)模板
//   - 含 {model} 占位符 → 自定义模板(完整 URL,或拼到默认 host 的 path)
//   - 其它(edits/variations/未知 path)→ fail-loud。edits/variations 需要
//     multipart 文件上传子请求,adapter 契约禁止,v1 范围外(roadmap)。
func (a *Adapter) endpointTemplate(endpointPath string) (string, error) {
	template := strings.TrimSpace(a.Endpoint)
	if template == "" {
		template = defaultPredictionsEndpointTemplate
	}
	path := strings.TrimSpace(endpointPath)
	switch {
	case path == "" || path == imagesGenerationsLanePath:
		return template, nil
	case strings.Contains(path, "{model}"):
		if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
			return path, nil
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return "https://api.replicate.com" + path, nil
	default:
		return "", fmt.Errorf("replicate: endpoint path %q 不支持(仅 generations;edits/variations 需上传子请求,v1 范围外)", path)
	}
}

// escapeModelPath 对 "owner/name" 形 model id 逐段 PathEscape:整体 escape
// 会把段间 "/" 转成 %2F,Replicate 端点的 owner、name 是两个独立路径段。
func escapeModelPath(model string) string {
	segments := strings.Split(model, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func (a *Adapter) acceptsCredential(t provider.CredentialType) bool {
	for _, ok := range a.AcceptableCredentialTypes() {
		if ok == t {
			return true
		}
	}
	return false
}

var _ provider.Adapter = (*Adapter)(nil)
