package provider

import (
	"net/http"
	"net/url"
	"strings"
)

const (
	// GrokCLIChatProxyHost 是 Grok 订阅会话默认文本出站主机。
	// 官方 API Key 与媒体轮询仍走 api.x.ai。
	GrokCLIChatProxyHost = "cli-chat-proxy.grok.com"
	// DefaultGrokCLIVersion 在凭据未声明客户端版本时使用,与当前官方 CLI 稳定版对齐。
	DefaultGrokCLIVersion = "1.0.25"
	grokCLITokenAuth      = "xai-grok-cli"
)

// ApplyGrokCLIIdentityHeaders 给打到 CLI 聊天代理的请求盖上会话身份头。
// 官方 API 主机不得调用本函数,以免把订阅令牌伪装成 Key 流量。
func ApplyGrokCLIIdentityHeaders(req *http.Request, cred Credential) {
	if req == nil {
		return
	}
	version := strings.TrimSpace(cred.Extra["client_version"])
	if version == "" {
		version = DefaultGrokCLIVersion
	}
	req.Header.Set("x-xai-token-auth", grokCLITokenAuth)
	req.Header.Set("x-grok-client-version", version)
	req.Header.Set("User-Agent", "HUAKAI-GrokCLI/"+version)
}

func applyGrokSessionOutbound(platform string, req *http.Request, in BuildInput) error {
	if req == nil || req.URL == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(platform), "grok") {
		return nil
	}
	if in.Credential.Type != CredentialTypeOAuthAccessToken {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(in.Credential.Extra["using_api"]), "true") {
		return nil
	}
	if grokMediaStaysOnOfficialAPI(in.EndpointPath, req.URL.Path) {
		return nil
	}
	if base := strings.TrimSpace(in.Credential.Extra["base_url"]); base != "" {
		rewritten, err := EndpointForCredential(req.URL.String(), Credential{
			Type:  CredentialTypeAPIKey,
			Extra: in.Credential.Extra,
		})
		if err != nil {
			return err
		}
		next, err := url.Parse(rewritten)
		if err != nil {
			return err
		}
		req.URL = next
		if isGrokCLIChatProxyHost(next.Hostname()) {
			ApplyGrokCLIIdentityHeaders(req, in.Credential)
		}
		return nil
	}
	req.URL.Scheme = "https"
	req.URL.Host = GrokCLIChatProxyHost
	ApplyGrokCLIIdentityHeaders(req, in.Credential)
	return nil
}

func grokMediaStaysOnOfficialAPI(paths ...string) bool {
	for _, raw := range paths {
		path := strings.ToLower(strings.TrimSpace(raw))
		if path == "" {
			continue
		}
		if strings.Contains(path, "/videos") || strings.Contains(path, "/images") || strings.Contains(path, "/imagine") {
			return true
		}
	}
	return false
}

func isGrokCLIChatProxyHost(host string) bool {
	return strings.EqualFold(strings.TrimSpace(host), GrokCLIChatProxyHost)
}
