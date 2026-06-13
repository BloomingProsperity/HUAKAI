//go:build integration_pg

// 集成测试：account_type='aws_sigv4' 端到端落库 + vault 解析。
//
// 守护的具体回归（一句话）：
//
//	provider_accounts.account_type CHECK 约束必须接受 'aws_sigv4'，
//	且 PostgresCredentialVault.Resolve 必须把该行经 mapAWSSigV4 映射为
//	Bedrock PassthroughAdapter 期望的 CredentialTypeAWSSigV4 凭据。
//
// 判别性（mutation 说明）：
//   - 若 0140 migration 缺失（CHECK 仍是 0011 的 5 值），下方 INSERT 会被
//     provider_accounts_account_type_check 拒绝 → 测试在插入处即 FAIL。
//     这正是本切片修复前的真实状态（aws_sigv4 行无法落库 → Bedrock 路径 dead）。
//   - 测试用 DB 错误码 23514 (check_violation) 显式断言 mutation 路径，
//     而非笼统“插入失败”，避免把无关错误误判为通过条件。
//   - 解析断言要求 Value=secret + Extra[aws_access_key_id/aws_region/aws_session_token]，
//     若 mapCredential 把 aws_sigv4 错误路由（如漏掉 case）则解析结果会偏离，
//     测试 FAIL。
package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestPostgresCredentialVault_AWSSigV4InsertAndResolve 验证 aws_sigv4 账号
// 既能落库（0140 CHECK 已含 aws_sigv4）又能被 vault 解析为 SigV4 凭据。
func TestPostgresCredentialVault_AWSSigV4InsertAndResolve(t *testing.T) {
	ctx := context.Background()
	suffix := "aws-sigv4-resolve"
	f := setupFixture(ctx, t, suffix)
	defer cleanupFixture(ctx, t, testDB, f)

	const (
		wantAccessKeyID  = "AKIDEXAMPLE"
		wantSecret       = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
		wantRegion       = "us-east-1"
		wantSessionToken = "FQoDYXdzEXAMPLE-STS-TOKEN"
	)
	creds := map[string]interface{}{
		"aws_access_key_id":     wantAccessKeyID,
		"aws_secret_access_key": wantSecret,
		"aws_region":            wantRegion,
		"aws_session_token":     wantSessionToken,
	}

	// 关键：account_type='aws_sigv4'。0140 之前此 INSERT 会撞 CHECK 约束。
	f.providerAccountID = insertProviderAccount(ctx, t, testDB,
		f.tenantID, f.providerID, f.channelID,
		"test-account-"+suffix, "aws_sigv4", true, creds)

	vault := NewPostgresCredentialVault(testDB)
	cred, info, err := vault.Resolve(ctx, f.tenantID, f.providerAccountID)
	if err != nil {
		t.Fatalf("Resolve aws_sigv4 期望成功，但得到错误: %v", err)
	}

	// vault 必须经 mapAWSSigV4 输出 Bedrock 期望的 SigV4 凭据形态。
	if cred.Type != CredentialTypeAWSSigV4 {
		t.Errorf("Type=%q，期望 CredentialTypeAWSSigV4 (%q)", cred.Type, CredentialTypeAWSSigV4)
	}
	if cred.Value != wantSecret {
		t.Errorf("Value=%q，期望 secret access key %q", cred.Value, wantSecret)
	}
	if cred.Extra["aws_access_key_id"] != wantAccessKeyID {
		t.Errorf("Extra[aws_access_key_id]=%q，期望 %q", cred.Extra["aws_access_key_id"], wantAccessKeyID)
	}
	if cred.Extra["aws_region"] != wantRegion {
		t.Errorf("Extra[aws_region]=%q，期望 %q", cred.Extra["aws_region"], wantRegion)
	}
	if cred.Extra["aws_session_token"] != wantSessionToken {
		t.Errorf("Extra[aws_session_token]=%q，期望 %q", cred.Extra["aws_session_token"], wantSessionToken)
	}
	if info.AccountType != "aws_sigv4" {
		t.Errorf("AccountType=%q，期望 'aws_sigv4'", info.AccountType)
	}
	if info.AccountID != f.providerAccountID {
		t.Errorf("AccountID=%d，期望 %d", info.AccountID, f.providerAccountID)
	}
}

// TestProviderAccountsCheck_RejectsUnknownAccountType 是上面用例的判别性对照：
// 一个 0140 仍未加入的 account_type（'bogus_type'）必须被 CHECK 约束拒绝，
// 且错误码为 23514 (check_violation)。这证明 INSERT 路径确实受 CHECK 约束守护，
// 因此上面 aws_sigv4 能插入 == 0140 已把 aws_sigv4 加入白名单，而非约束被整体放开。
func TestProviderAccountsCheck_RejectsUnknownAccountType(t *testing.T) {
	ctx := context.Background()
	suffix := "aws-sigv4-mutation-guard"
	f := setupFixture(ctx, t, suffix)
	defer cleanupFixture(ctx, t, testDB, f)

	_, err := testDB.Exec(ctx,
		`INSERT INTO provider_accounts
		   (tenant_id, provider_id, channel_id, name, account_type, enabled, credentials)
		 VALUES ($1, $2, $3, $4, 'bogus_type', true, '{}'::jsonb)`,
		f.tenantID, f.providerID, f.channelID, "test-account-"+suffix,
	)
	if err == nil {
		t.Fatal("期望 account_type='bogus_type' 被 CHECK 约束拒绝，但 INSERT 成功了 —— 约束可能被整体放开")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("期望 *pgconn.PgError，得到: %v", err)
	}
	if pgErr.Code != "23514" {
		t.Errorf("错误码=%q，期望 23514 (check_violation)；约束名=%q", pgErr.Code, pgErr.ConstraintName)
	}
	if pgErr.ConstraintName != "provider_accounts_account_type_check" {
		t.Errorf("约束名=%q，期望 provider_accounts_account_type_check", pgErr.ConstraintName)
	}
}
