// postgres_vault_aws_test.go — AWS SigV4 credential 形态单测（不连 DB）。
//
// 测试 mapAWSSigV4 + mapCredential("aws_sigv4", ...) 路径，覆盖 Bedrock
// PassthroughAdapter 的输入契约：
//   - Credential.Value = secret access key(密钥)
//   - Credential.Extra["aws_access_key_id"]
//   - Credential.Extra["aws_region"]
//   - Credential.Extra["aws_session_token"]（可选）
package provider

import (
	"errors"
	"strings"
	"testing"
)

func TestMapAWSSigV4_HappyPath(t *testing.T) {
	raw := []byte(`{
		"aws_access_key_id":"AKIDEXAMPLE",
		"aws_secret_access_key":"wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		"aws_region":"us-east-1"
	}`)
	cred, err := mapAWSSigV4(raw)
	if err != nil {
		t.Fatalf("mapAWSSigV4 err=%v", err)
	}
	if cred.Type != CredentialTypeAWSSigV4 {
		t.Errorf("Type=%q want CredentialTypeAWSSigV4", cred.Type)
	}
	if cred.Value != "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY" {
		t.Errorf("Value 应为 secret access key, 得 %q", cred.Value)
	}
	if cred.Extra["aws_access_key_id"] != "AKIDEXAMPLE" {
		t.Errorf("Extra[aws_access_key_id]=%q", cred.Extra["aws_access_key_id"])
	}
	if cred.Extra["aws_region"] != "us-east-1" {
		t.Errorf("Extra[aws_region]=%q", cred.Extra["aws_region"])
	}
	if _, ok := cred.Extra["aws_session_token"]; ok {
		t.Errorf("Extra[aws_session_token] 不应存在 (无 STS)")
	}
}

func TestMapAWSSigV4_WithSessionToken(t *testing.T) {
	raw := []byte(`{
		"aws_access_key_id":"AKIDEXAMPLE",
		"aws_secret_access_key":"secret",
		"aws_region":"us-west-2",
		"aws_session_token":"FQoDYXdzEABCDEF1234"
	}`)
	cred, err := mapAWSSigV4(raw)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if cred.Extra["aws_session_token"] != "FQoDYXdzEABCDEF1234" {
		t.Errorf("aws_session_token=%q", cred.Extra["aws_session_token"])
	}
}

func TestMapAWSSigV4_ExtraFieldsCarriedThrough(t *testing.T) {
	raw := []byte(`{
		"aws_access_key_id":"AKIDEXAMPLE",
		"aws_secret_access_key":"secret",
		"aws_region":"us-east-1",
		"extra":{"stream":"true","custom_tag":"prod"}
	}`)
	cred, err := mapAWSSigV4(raw)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if cred.Extra["stream"] != "true" {
		t.Errorf("extra.stream=%q want true", cred.Extra["stream"])
	}
	if cred.Extra["custom_tag"] != "prod" {
		t.Errorf("extra.custom_tag=%q want prod", cred.Extra["custom_tag"])
	}
}

func TestMapAWSSigV4_ExtraCannotOverrideRequiredFields(t *testing.T) {
	// extra.aws_region 应被忽略，使用顶层 aws_region
	raw := []byte(`{
		"aws_access_key_id":"AKID-TOP",
		"aws_secret_access_key":"secret",
		"aws_region":"us-east-1",
		"extra":{"aws_region":"eu-west-1","aws_access_key_id":"AKID-OVERRIDE"}
	}`)
	cred, err := mapAWSSigV4(raw)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if cred.Extra["aws_region"] != "us-east-1" {
		t.Errorf("aws_region 应不被 extra 覆盖, 得 %q", cred.Extra["aws_region"])
	}
	if cred.Extra["aws_access_key_id"] != "AKID-TOP" {
		t.Errorf("aws_access_key_id 应不被 extra 覆盖, 得 %q", cred.Extra["aws_access_key_id"])
	}
}

func TestMapAWSSigV4_RejectsMissingRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			"missing access_key_id",
			`{"aws_secret_access_key":"s","aws_region":"us-east-1"}`,
			"aws_access_key_id",
		},
		{
			"missing secret",
			`{"aws_access_key_id":"a","aws_region":"us-east-1"}`,
			"aws_secret_access_key",
		},
		{
			"missing region",
			`{"aws_access_key_id":"a","aws_secret_access_key":"s"}`,
			"aws_region",
		},
		{
			"all empty",
			`{}`,
			"aws_access_key_id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mapAWSSigV4([]byte(tc.raw))
			if err == nil {
				t.Fatal("应报错")
			}
			if !errors.Is(err, ErrCredentialFormat) {
				t.Errorf("err=%v 应包装 ErrCredentialFormat", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err 应提及 %q, got: %v", tc.want, err)
			}
		})
	}
}

func TestMapAWSSigV4_RejectsInvalidJSON(t *testing.T) {
	_, err := mapAWSSigV4([]byte(`not json`))
	if err == nil {
		t.Fatal("非 JSON 应报错")
	}
	if !errors.Is(err, ErrCredentialFormat) {
		t.Errorf("err=%v want ErrCredentialFormat", err)
	}
}

// TestMapCredential_RoutesAWSSigV4 验证 mapCredential 的 dispatch case
// 把 account_type="aws_sigv4" 路由到 mapAWSSigV4。
func TestMapCredential_RoutesAWSSigV4(t *testing.T) {
	raw := []byte(`{"aws_access_key_id":"a","aws_secret_access_key":"s","aws_region":"us-east-1"}`)
	cred, err := mapCredential("aws_sigv4", raw)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if cred.Type != CredentialTypeAWSSigV4 {
		t.Errorf("Type=%q want CredentialTypeAWSSigV4", cred.Type)
	}
}
