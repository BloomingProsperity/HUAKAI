// 包 provider — postgres_vault 私有 mapCredential 单测（不需要真 DB）。
//
// 已有 postgres_vault_test.go 走 integration_pg 构建标签 + 真实 DB 路径；
// 本文件覆盖纯函数 mapSession，不与 DB 集成测试冲突。
package provider

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestMapCredential_SessionHappyPath(t *testing.T) {
	raw := []byte(`{"session_token":"sess-abc","extra":{"cookie":"c=1","user_agent":"x"}}`)
	cred, err := mapCredential("session", raw)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Type != CredentialTypeSessionToken {
		t.Errorf("Type=%q want session_token", cred.Type)
	}
	if cred.Value != "sess-abc" {
		t.Errorf("Value=%q want sess-abc", cred.Value)
	}
	if cred.Extra["cookie"] != "c=1" || cred.Extra["user_agent"] != "x" {
		t.Errorf("Extra=%v want cookie+user_agent 透传", cred.Extra)
	}
}

func TestMapCredential_SessionWithoutExtra(t *testing.T) {
	raw := []byte(`{"session_token":"sess-only"}`)
	cred, err := mapCredential("session", raw)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Type != CredentialTypeSessionToken {
		t.Errorf("Type=%q want session_token", cred.Type)
	}
	if cred.Value != "sess-only" {
		t.Errorf("Value=%q want sess-only", cred.Value)
	}
	// extra 为空时不应分配 map
	if cred.Extra != nil {
		t.Errorf("Extra 应为 nil 当 JSONB 无 extra 字段，得到 %v", cred.Extra)
	}
}

func TestMapCredential_SessionEmptyTokenRejected(t *testing.T) {
	raw := []byte(`{"session_token":""}`)
	_, err := mapCredential("session", raw)
	if !errors.Is(err, ErrCredentialFormat) {
		t.Errorf("空 session_token 应返回 ErrCredentialFormat，得到 %v", err)
	}
}

func TestMapCredential_SessionMalformedJSONRejected(t *testing.T) {
	raw := []byte(`{not json`)
	_, err := mapCredential("session", raw)
	if !errors.Is(err, ErrCredentialFormat) {
		t.Errorf("格式错误 JSON 应返回 ErrCredentialFormat，得到 %v", err)
	}
}

func TestMapCredential_UnknownAccountTypeStillRejected(t *testing.T) {
	// 回归：未知 account_type 仍返回 ErrCredentialFormat（防止未来误改 mapCredential 默认分支）
	_, err := mapCredential("totally_unknown", []byte(`{}`))
	if !errors.Is(err, ErrCredentialFormat) {
		t.Errorf("未知 account_type 应返回 ErrCredentialFormat，得到 %v", err)
	}
}

func TestMapRuntimeMaterialFromAccountCredentials(t *testing.T) {
	tests := []struct {
		name     string
		material credentialstore.RuntimeMaterial
		wantType CredentialType
	}{
		{
			name: "上游透传",
			material: credentialstore.RuntimeMaterial{
				Kind:  credentialstore.RuntimeUpstreamPassthrough,
				Value: "Bearer oauth-token",
				Extra: map[string]string{"auth_header": "Authorization"},
			},
			wantType: CredentialTypeUpstreamPassthrough,
		},
		{
			name: "静态 OAuth access token",
			material: credentialstore.RuntimeMaterial{
				Kind: credentialstore.RuntimeOAuthAccessToken, Value: "setup-token-value",
			},
			wantType: CredentialTypeOAuthAccessToken,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cred := mapRuntimeMaterial(tc.material)
			if cred.Type != tc.wantType || cred.Value != tc.material.Value {
				t.Fatalf("cred=%+v", cred)
			}
			if tc.material.Extra["auth_header"] != "" && cred.Extra["auth_header"] != "Authorization" {
				t.Fatalf("extra=%v", cred.Extra)
			}
		})
	}
}

func TestPostgresCredentialVaultResolveBlocksLegacyFallbackWhenV2CredentialNotActive(t *testing.T) {
	calls := 0
	storeDB := &vaultCredentialStoreDBStub{
		queryRow: func(_ context.Context, sql string, args ...interface{}) pgx.Row {
			calls++
			if !strings.Contains(sql, "credential_row_count") {
				t.Fatalf("ResolveActive SQL missing credential_row_count:\n%s", sql)
			}
			if len(args) != 2 || args[0] != int64(42) || args[1] != int64(7) {
				t.Fatalf("ResolveActive args=%#v, want account=42 tenant=7", args)
			}
			return vaultCredentialStoreRowValuesStub{values: vaultInactiveCredentialRecordValues(1)}
		},
	}
	store := credentialstore.NewStore(storeDB, mustVaultTestKeyProvider(t), credentialstore.DefaultHandlerRegistry())
	vault := NewPostgresCredentialVaultWithStore(nil, store)

	_, _, err := vault.Resolve(context.Background(), 7, 42)
	if !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("Resolve err=%v, want %v", err, ErrAccountDisabled)
	}
	if calls != 1 {
		t.Fatalf("ResolveActive QueryRow calls=%d, want 1 atomic query", calls)
	}
}

func TestLegacyServiceAccountFailClosed(t *testing.T) {
	cred, err := mapServiceAccount([]byte(`{
		"client_email":"sa@project.iam.gserviceaccount.com",
		"private_key":"-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----",
		"token_uri":"https://oauth2.googleapis.com/token"
	}`))
	if err == nil {
		t.Fatalf("legacy service_account 返回了空 Value 可转发凭据: %+v", cred)
	}
	if !errors.Is(err, ErrCredentialFormat) {
		t.Fatalf("err=%v, want ErrCredentialFormat", err)
	}
}

func mustVaultTestKeyProvider(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	provider, err := credentialstore.NewStaticKeyProvider("test-key", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key provider: %v", err)
	}
	return provider
}

type vaultCredentialStoreDBStub struct {
	queryRow func(context.Context, string, ...interface{}) pgx.Row
}

func (s *vaultCredentialStoreDBStub) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (s *vaultCredentialStoreDBStub) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (s *vaultCredentialStoreDBStub) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	if s.queryRow == nil {
		return vaultCredentialStoreRowStub{err: pgx.ErrNoRows}
	}
	return s.queryRow(ctx, sql, args...)
}

type vaultCredentialStoreRowStub struct {
	err error
}

func (r vaultCredentialStoreRowStub) Scan(...interface{}) error {
	if r.err != nil {
		return r.err
	}
	return nil
}

type vaultCredentialStoreRowValuesStub struct {
	values []any
}

func (r vaultCredentialStoreRowValuesStub) Scan(dest ...interface{}) error {
	if len(dest) != len(r.values) {
		return errors.New("scan destination count mismatch")
	}
	for i := range dest {
		if err := vaultSetScanValue(dest[i], r.values[i]); err != nil {
			return err
		}
	}
	return nil
}

func vaultInactiveCredentialRecordValues(credentialRowCount int64) []any {
	return []any{
		int64(0), int64(7), int64(42), "", "", "",
		int32(0), []byte{}, "", "",
		[]byte{}, "", (*string)(nil), (*string)(nil),
		pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{},
		pgtype.Timestamptz{}, (*string)(nil), (*string)(nil), int32(0),
		pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{},
		(*string)(nil), // external_account_id 列(迁移 0141),no_serving 分支返 NULL
		int64(0), credentialRowCount, true,
	}
}

func vaultSetScanValue(dest any, value any) error {
	target := reflect.ValueOf(dest)
	if target.Kind() != reflect.Ptr || target.IsNil() {
		return errors.New("scan destination is not a non-nil pointer")
	}
	elem := target.Elem()
	if value == nil {
		elem.Set(reflect.Zero(elem.Type()))
		return nil
	}
	source := reflect.ValueOf(value)
	if source.Type().AssignableTo(elem.Type()) {
		elem.Set(source)
		return nil
	}
	if source.Type().ConvertibleTo(elem.Type()) {
		elem.Set(source.Convert(elem.Type()))
		return nil
	}
	return errors.New("scan destination type mismatch")
}

// TestProviderAccountExtraCodexCLIOnlyAndMerge 验证账号级 codex_cli_only 开关解析与泄漏隔离:
// 布尔正确投影进 AccountInfo 入站门控字段,且绝不并入出站 Credential.Extra,同时不影响其余
// 可透传 extra 键。
func TestProviderAccountExtraCodexCLIOnlyAndMerge(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want bool
	}{
		{
			name: "true 开启 AccountInfo 入站门控字段",
			raw:  []byte(`{"codex_cli_only":true,"passthrough":"kept"}`),
			want: true,
		},
		{
			name: "false 保持关闭",
			raw:  []byte(`{"codex_cli_only":false,"passthrough":"kept"}`),
			want: false,
		},
		{
			name: "缺省保持关闭",
			raw:  []byte(`{"passthrough":"kept"}`),
			want: false,
		},
		{
			name: "空 extra 保持关闭",
			raw:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accountExtra := decodeProviderAccountExtra(tt.raw)
			info := AccountInfo{
				CodexCLIOnly: codexCLIOnlyFromAccountExtra(accountExtra),
			}
			if info.CodexCLIOnly != tt.want {
				t.Fatalf("CodexCLIOnly = %v, want %v", info.CodexCLIOnly, tt.want)
			}

			cred := mergeCredentialAccountExtra(Credential{}, accountExtra)
			if _, exists := cred.Extra[providerAccountExtraCodexCLIOnlyKey]; exists {
				t.Fatalf("Credential.Extra 不应泄漏 %q", providerAccountExtraCodexCLIOnlyKey)
			}
			if len(accountExtra) > 0 {
				if got := cred.Extra["passthrough"]; got != "kept" {
					t.Fatalf("passthrough extra = %q, want kept", got)
				}
			}
		})
	}
}

// TestMergeCredentialAccountExtraScrubsPreexistingPolicyKey 验证绝对泄漏保证:即便凭据载荷
// 本身(非 account extra)误带 codex_cli_only,merge 也会把它从出站 Extra 清除。
func TestMergeCredentialAccountExtraScrubsPreexistingPolicyKey(t *testing.T) {
	// 凭据载荷预带内部策略键 + account extra 为空(走不并入路径),仍须清除。
	cred := Credential{Extra: map[string]string{
		providerAccountExtraCodexCLIOnlyKey: "true",
		"org_id":                            "org-keep",
	}}
	got := mergeCredentialAccountExtra(cred, nil)
	if _, exists := got.Extra[providerAccountExtraCodexCLIOnlyKey]; exists {
		t.Fatalf("凭据载荷预带的 %q 必须被清除,不得出站", providerAccountExtraCodexCLIOnlyKey)
	}
	if got.Extra["org_id"] != "org-keep" {
		t.Fatalf("org_id=%q want org-keep;scrub 不应误删其他键", got.Extra["org_id"])
	}
}

func TestMergeCredentialAccountExtraCredentialProjectWins(t *testing.T) {
	credential := Credential{Extra: map[string]string{"project_id": "project-from-credential"}}
	accountExtra := map[string]string{"project_id": "project-from-account"}
	merged := mergeCredentialAccountExtra(credential, accountExtra)
	if merged.Extra["project_id"] != "project-from-credential" {
		t.Fatalf("project_id=%q，凭证载荷必须覆盖账号 extra", merged.Extra["project_id"])
	}
}
