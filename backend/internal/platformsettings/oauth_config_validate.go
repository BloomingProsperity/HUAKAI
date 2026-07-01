package platformsettings

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// 本文件集中校验第三方登录(OAuth)两张配置表的写入值:
//   - oauth_providers_config(公开):{"github":{"client_id":"...",...},...}
//   - oauth_providers_secrets(密钥):{"github":"secret",...}
//
// 校验目标(写路径即时反馈,胜过等请求期 buildOAuthProvider 才报错):
//   ① 必须是 JSON 对象;② 顶层 provider 名必须在允许集内(挡笔误 provider);
//   ③ config 的字段必须在允许集内、且值为 string(scopes 为 string 数组);
//   ④ **公开 config 里禁止出现 client_secret 字段**——防运维把密钥误塞进可读的 config 表泄露;
//   ⑤ secrets 表的值必须是 string。
// 端点 URL 的 https/SSRF 校验不在此做(交给请求期 buildOAuthProvider 的 ValidateOAuthEndpointURL,
// 与拨号期 SSRF guard 同源不漂移);此处只做结构与字段名门禁。

// oauthConfigurableProviders 是允许在 OAuth 配置表里出现的 provider 名集合(与 cmd/gateway
// 静态 env 构建的 7 家一致)。telegram 不在此(它走独立的 bot token 密钥,非 OAuth code 流)。
var oauthConfigurableProviders = map[string]struct{}{
	"google":   {},
	"github":   {},
	"qq":       {},
	"dingtalk": {},
	"nodeseek": {},
	"linuxdo":  {},
	"discord":  {},
}

// oauthConfigStringFields 是公开 config 每个 provider 允许的**字符串**字段(snake_case)。
// 刻意不含 client_secret(密钥只走 secrets 表)。
var oauthConfigStringFields = map[string]struct{}{
	"client_id":            {},
	"redirect_uri":         {},
	"auth_url":             {},
	"token_url":            {},
	"user_url":             {},
	"emails_url":           {},
	"openid_url":           {},
	"jwks_url":             {},
	"issuer":               {},
	"subject_field":        {},
	"email_field":          {},
	"email_verified_field": {},
	"display_name_field":   {},
}

// oauthConfigArrayField 是唯一允许的**字符串数组**字段。
const oauthConfigArrayField = "scopes"

func validateOAuthProvidersConfigValue(key SettingKey, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	// 用 json.Number 关闭浮点归一,原样保真;顶层必须是对象。
	raw := map[string]json.RawMessage{}
	dec := json.NewDecoder(strings.NewReader(value))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return "", fmt.Errorf("%w: %s 必须是 JSON 对象 {provider: {字段...}}", ErrInvalidValue, key)
	}
	for provider, rawCfg := range raw {
		name := strings.ToLower(strings.TrimSpace(provider))
		if _, ok := oauthConfigurableProviders[name]; !ok {
			return "", fmt.Errorf("%w: %s 含未知 provider %q(允许:%s)", ErrInvalidValue, key, provider, sortedProviderList())
		}
		fields := map[string]json.RawMessage{}
		if err := json.Unmarshal(rawCfg, &fields); err != nil {
			return "", fmt.Errorf("%w: %s 里 %s 的值必须是字段对象", ErrInvalidValue, key, name)
		}
		for field, rawVal := range fields {
			fname := strings.TrimSpace(field)
			if fname == "client_secret" {
				return "", fmt.Errorf("%w: %s 里 %s 不得含 client_secret(密钥请写入 oauth_providers_secrets)", ErrInvalidValue, key, name)
			}
			if fname == oauthConfigArrayField {
				var arr []string
				if err := json.Unmarshal(rawVal, &arr); err != nil {
					return "", fmt.Errorf("%w: %s 里 %s.scopes 必须是字符串数组", ErrInvalidValue, key, name)
				}
				continue
			}
			if _, ok := oauthConfigStringFields[fname]; !ok {
				return "", fmt.Errorf("%w: %s 里 %s 含未知字段 %q", ErrInvalidValue, key, name, field)
			}
			var s string
			if err := json.Unmarshal(rawVal, &s); err != nil {
				return "", fmt.Errorf("%w: %s 里 %s.%s 必须是字符串", ErrInvalidValue, key, name, fname)
			}
		}
	}
	return value, nil
}

func validateOAuthProvidersSecretsValue(key SettingKey, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	raw := map[string]json.RawMessage{}
	dec := json.NewDecoder(strings.NewReader(value))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return "", fmt.Errorf("%w: %s 必须是 JSON 对象 {provider: \"secret\"}", ErrInvalidValue, key)
	}
	for provider, rawVal := range raw {
		name := strings.ToLower(strings.TrimSpace(provider))
		if _, ok := oauthConfigurableProviders[name]; !ok {
			return "", fmt.Errorf("%w: %s 含未知 provider %q(允许:%s)", ErrInvalidValue, key, provider, sortedProviderList())
		}
		var s string
		if err := json.Unmarshal(rawVal, &s); err != nil {
			return "", fmt.Errorf("%w: %s 里 %s 的 secret 必须是字符串", ErrInvalidValue, key, name)
		}
	}
	return value, nil
}

func sortedProviderList() string {
	names := make([]string, 0, len(oauthConfigurableProviders))
	for name := range oauthConfigurableProviders {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, "/")
}
