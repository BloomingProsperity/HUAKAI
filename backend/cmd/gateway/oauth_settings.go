package main

import (
	"context"
	"encoding/json"
	"strings"

	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// oauthProviderSettingsResolver 构建请求期 OAuth provider 解析器:后台设置
//(oauth_providers_config 公开 JSON + oauth_providers_secrets 密钥 JSON)按 provider 逐字段
// 覆盖 env 基线(settings-first),再经 buildOAuthProvider(注入拨号期 SSRF guard + 端点 https 校验)
// 现构 provider。对某 provider 无任何后台设置项时返回 (nil,false),调用方回退 boot 期 env 静态
// provider——保证「设置为空 → 与迁移前行为逐字节一致」。
//
// settings 必须传 cipher-enabled 的 platformSettings 实例:secrets 表在库里是密文,读时已解密成
// 明文 client_secret;若传 cipher-less 实例会读到密文导致 OAuth 校验失败(见 wiring 注释)。
func oauthProviderSettingsResolver(settings gatewayPlatformSettings, logger *zap.Logger) func(context.Context, string) (userauth.OAuthProvider, bool) {
	return func(ctx context.Context, name string) (userauth.OAuthProvider, bool) {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || settings == nil {
			return nil, false
		}
		cfgMap := readOAuthConfigMap(ctx, settings)
		secMap := readOAuthSecretMap(ctx, settings)
		pcfg, hasCfg := cfgMap[name]
		psec, hasSec := secMap[name]
		if !hasCfg && !hasSec {
			// 该 provider 后台完全没配 → 回退 env 静态 provider(不改行为)。
			return nil, false
		}
		// env 基线(默认 URL/字段等);无 env 模板则以裸 provider 名起手(纯设置驱动)。
		// nil logger:请求期不重复刷 linuxdo min-trust 告警。
		base, ok := envOAuthConfigFor(nil, name)
		if !ok {
			base = userauth.OAuthConfig{Provider: name}
		}
		merged := overlayOAuthSettings(base, pcfg, psec, hasSec)
		p := buildOAuthProvider(logger, merged)
		if p == nil {
			// 设置合并后仍不完整(缺 client_id / 必需 secret / 非法端点)→ 回退 env 静态,
			// 不让半截设置打断本来能用的 env provider。
			return nil, false
		}
		return p, true
	}
}

func readOAuthConfigMap(ctx context.Context, settings gatewayPlatformSettings) map[string]map[string]any {
	out := map[string]map[string]any{}
	s, err := settings.Get(ctx, platformsettings.KeyOAuthProvidersConfig)
	if err != nil {
		return out
	}
	value := strings.TrimSpace(s.Value)
	if value == "" {
		return out
	}
	raw := map[string]map[string]any{}
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return out
	}
	for k, v := range raw {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}

func readOAuthSecretMap(ctx context.Context, settings gatewayPlatformSettings) map[string]string {
	out := map[string]string{}
	s, err := settings.Get(ctx, platformsettings.KeyOAuthProvidersSecrets)
	if err != nil {
		return out
	}
	value := strings.TrimSpace(s.Value)
	if value == "" {
		return out
	}
	raw := map[string]string{}
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return out
	}
	for k, v := range raw {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}

// overlayOAuthSettings 把后台设置的非空字段覆盖到 env 基线上(settings-first,空字段保留 env)。
func overlayOAuthSettings(base userauth.OAuthConfig, pcfg map[string]any, psec string, hasSec bool) userauth.OAuthConfig {
	str := func(field string) (string, bool) {
		v, ok := pcfg[field]
		if !ok {
			return "", false
		}
		s, ok := v.(string)
		if !ok {
			return "", false
		}
		s = strings.TrimSpace(s)
		return s, s != ""
	}
	if v, ok := str("client_id"); ok {
		base.ClientID = v
	}
	if v, ok := str("redirect_uri"); ok {
		base.RedirectURI = v
	}
	if v, ok := str("auth_url"); ok {
		base.AuthURL = v
	}
	if v, ok := str("token_url"); ok {
		base.TokenURL = v
	}
	if v, ok := str("user_url"); ok {
		base.UserURL = v
	}
	if v, ok := str("emails_url"); ok {
		base.EmailsURL = v
	}
	if v, ok := str("openid_url"); ok {
		base.OpenIDURL = v
	}
	if v, ok := str("jwks_url"); ok {
		base.JWKSURL = v
	}
	if v, ok := str("issuer"); ok {
		base.Issuer = v
	}
	if v, ok := str("subject_field"); ok {
		base.SubjectField = v
	}
	if v, ok := str("email_field"); ok {
		base.EmailField = v
	}
	if v, ok := str("email_verified_field"); ok {
		base.EmailVerifiedField = v
	}
	if v, ok := str("display_name_field"); ok {
		base.DisplayNameField = v
	}
	if v, ok := str("trust_level_field"); ok {
		base.MinimumNumericClaimField = v
	}
	if raw, ok := pcfg["min_trust_level"]; ok {
		if n, ok := oauthNumberFromAny(raw); ok {
			base.MinimumNumericClaimValue = n
		}
	}
	if raw, ok := pcfg["scopes"]; ok {
		if scopes := oauthScopesFromAny(raw); len(scopes) > 0 {
			base.Scopes = scopes
		}
	}
	if hasSec {
		if s := strings.TrimSpace(psec); s != "" {
			base.ClientSecret = s
		}
	}
	return base
}

// oauthNumberFromAny 从 JSON 反序列化出的 any 里取非负整数(json.Unmarshal 数字默认是 float64)。
func oauthNumberFromAny(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		if n < 0 {
			return 0, false
		}
		return int64(n), true
	case json.Number:
		iv, err := n.Int64()
		if err != nil || iv < 0 {
			return 0, false
		}
		return iv, true
	default:
		return 0, false
	}
}

func oauthScopesFromAny(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
