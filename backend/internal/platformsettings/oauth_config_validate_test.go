package platformsettings

import (
	"errors"
	"strings"
	"testing"
)

// 判别核心:OAuth 两张配置表写入校验必须
//  ①放行合法结构;②挡未知 provider;③**挡公开 config 里的 client_secret**(防密钥泄进可读表);
//  ④挡未知字段/非字符串;⑤空值放行(= 未配置)。每条都能被对应变异证红。

func TestValidateOAuthProvidersConfigValue(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "空值放行", value: "", wantErr: false},
		{name: "合法_单provider", value: `{"github":{"client_id":"cid","redirect_uri":"https://x/cb"}}`, wantErr: false},
		{name: "合法_含scopes数组", value: `{"linuxdo":{"client_id":"c","scopes":["read","write"]}}`, wantErr: false},
		{name: "合法_含min_trust_level", value: `{"linuxdo":{"client_id":"c","min_trust_level":2,"trust_level_field":"trust_level"}}`, wantErr: false},
		{name: "min_trust_level负数被拒", value: `{"linuxdo":{"min_trust_level":-1}}`, wantErr: true},
		{name: "min_trust_level非整数被拒", value: `{"linuxdo":{"min_trust_level":"high"}}`, wantErr: true},
		{name: "合法_多provider", value: `{"github":{"client_id":"a"},"google":{"client_id":"b"}}`, wantErr: false},
		{name: "未知provider被拒", value: `{"evilcorp":{"client_id":"x"}}`, wantErr: true},
		{name: "config里含client_secret被拒", value: `{"github":{"client_secret":"leak"}}`, wantErr: true},
		{name: "未知字段被拒", value: `{"github":{"bogus_field":"x"}}`, wantErr: true},
		{name: "非字符串字段被拒", value: `{"github":{"client_id":123}}`, wantErr: true},
		{name: "scopes非数组被拒", value: `{"github":{"scopes":"read"}}`, wantErr: true},
		{name: "顶层非对象被拒", value: `["github"]`, wantErr: true},
		{name: "provider值非对象被拒", value: `{"github":"x"}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateOAuthProvidersConfigValue(KeyOAuthProvidersConfig, tc.value)
			if tc.wantErr && err == nil {
				t.Fatalf("value %q 应被拒但通过了", tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("value %q 应通过但被拒:%v", tc.value, err)
			}
			if tc.wantErr && err != nil && !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("错误应包裹 ErrInvalidValue,得 %v", err)
			}
		})
	}
}

// TestValidateOAuthProvidersConfigRejectsSecretLeak 单独强化「client_secret 不得进公开 config」这条
// 安全不变量:错误消息须点名 oauth_providers_secrets 引导运维改写正确的密钥表。
func TestValidateOAuthProvidersConfigRejectsSecretLeak(t *testing.T) {
	_, err := validateOAuthProvidersConfigValue(KeyOAuthProvidersConfig, `{"google":{"client_id":"x","client_secret":"sk-leak"}}`)
	if err == nil {
		t.Fatal("公开 config 含 client_secret 必须被拒(否则密钥会被读路径以明文返回)")
	}
	if !strings.Contains(err.Error(), "oauth_providers_secrets") {
		t.Fatalf("拒绝消息应引导到 secrets 表,得 %v", err)
	}
}

func TestValidateOAuthProvidersSecretsValue(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "空值放行", value: "", wantErr: false},
		{name: "合法", value: `{"github":"sk1","google":"sk2"}`, wantErr: false},
		{name: "未知provider被拒", value: `{"evilcorp":"sk"}`, wantErr: true},
		{name: "非字符串值被拒", value: `{"github":123}`, wantErr: true},
		{name: "顶层非对象被拒", value: `"sk"`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateOAuthProvidersSecretsValue(KeyOAuthProvidersSecrets, tc.value)
			if tc.wantErr && err == nil {
				t.Fatalf("value %q 应被拒但通过了", tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("value %q 应通过但被拒:%v", tc.value, err)
			}
		})
	}
}

// TestOAuthConfigKeysRoutedThroughValidateValue 确保两 key 真被 ValidateValue 路由到上面的校验器
// (而非落到默认 validatePublicTextValue 而漏掉结构校验)。变异:删掉 ValidateValue 里的路由分支
// → JSON 结构错误将被默认校验放行 → 本断言 RED。
func TestOAuthConfigKeysRoutedThroughValidateValue(t *testing.T) {
	if _, err := ValidateValue(KeyOAuthProvidersConfig, `{"evilcorp":{}}`); err == nil {
		t.Fatal("ValidateValue 应把 oauth_providers_config 路由到结构校验并拒未知 provider")
	}
	if _, err := ValidateValue(KeyOAuthProvidersSecrets, `{"github":123}`); err == nil {
		t.Fatal("ValidateValue 应把 oauth_providers_secrets 路由到结构校验并拒非字符串值")
	}
	// 空值两者都应放行(默认=未配置)。
	if _, err := ValidateValue(KeyOAuthProvidersConfig, ""); err != nil {
		t.Fatalf("oauth_providers_config 空值应放行,得 %v", err)
	}
	if _, err := ValidateValue(KeyOAuthProvidersSecrets, ""); err != nil {
		t.Fatalf("oauth_providers_secrets 空值应放行,得 %v", err)
	}
}
