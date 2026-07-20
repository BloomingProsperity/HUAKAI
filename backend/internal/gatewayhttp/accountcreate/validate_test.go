package accountcreate

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// TestValidateProtocolCompatibilityG1 咬住 G1:account 的 vendor/auth 必须与 family 契约
// 一致,防特权误配把 A 厂 key 绑 B 厂 family(错投密钥/错误 transport-health/计价归因分裂)。
// 变异:把 ValidateProtocolCompatibility 恢复成"非 session 族 return nil"→ 跨厂错配用例
// 不再被拒,本测试红。
func TestValidateProtocolCompatibilityG1(t *testing.T) {
	cases := []struct {
		name        string
		family      string
		accountType string
		vendor      string
		authMode    string
		wantErr     bool
	}{
		{"anthropic_messages 正配", "anthropic_messages", "api_key", "anthropic", "api_key", false},
		{"openai_chat 正配", "openai_chat", "api_key", "openai", "api_key", false},
		{"gemini 正配", "gemini_messages", "api_key", "gemini", "aistudio_api_key", false},
		// 跨厂错配:合法的 openai key 绑到 anthropic_messages family → 必拒。
		{"openai key 绑 anthropic_messages", "anthropic_messages", "api_key", "openai", "api_key", true},
		{"anthropic key 绑 openai_chat", "openai_chat", "api_key", "anthropic", "api_key", true},
		// session 族的 account_type 硬约束。
		{"session 族拒 api_key 账号类型", "anthropic_claude_session", "api_key", "anthropic", "claude_ai_oauth", true},
		{"session 族 oauth 正配", "anthropic_claude_session", "oauth", "anthropic", "claude_ai_oauth", false},
		// account-first:裸账号无 vendor/auth,推迟到凭据创建期,create 期放行。
		{"account-first 空 vendor/auth 放行", "openai_chat", "api_key", "", "", false},
		// 无契约 family 保守跳过。
		{"无契约 family 跳过", "some_unregistered_family", "api_key", "whatever", "whatever", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateProtocolCompatibility(c.family, c.accountType, c.vendor, c.authMode)
			if c.wantErr && !errors.Is(err, ErrProtocolIncompatible) {
				t.Fatalf("期望 ErrProtocolIncompatible,得 %v", err)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("合法组合不应拒,得 %v", err)
			}
		})
	}
}

type credentialCompatLookupStub struct {
	accountErr  error
	protocolErr error
}

func (s credentialCompatLookupStub) GetAdminProviderAccount(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	return admindb.AdminProviderAccountRow{ProviderID: 9, AccountType: "api_key"}, s.accountErr
}

func (s credentialCompatLookupStub) GetProviderProtocolForAccountCreate(context.Context, admindb.GetProviderProtocolForAccountCreateParams) (string, error) {
	return "openai_chat", s.protocolErr
}

func TestValidateCredentialCompatibilityFailsClosedWhenLookupFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		lookup credentialCompatLookupStub
	}{
		{name: "账号不存在", lookup: credentialCompatLookupStub{accountErr: pgx.ErrNoRows}},
		{name: "协议查询失败", lookup: credentialCompatLookupStub{protocolErr: errors.New("database unavailable")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCredentialCompatibility(context.Background(), tc.lookup, 7, 77, "openai", "api_key"); err == nil {
				t.Fatal("兼容性查询失败时不应放行凭据写入")
			}
		})
	}
}
