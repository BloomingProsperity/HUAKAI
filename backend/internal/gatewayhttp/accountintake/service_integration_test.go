//go:build integration_pg

package accountintake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

func TestServiceCreatesAtomicallyAndRejectsStalePlan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	service := newAccountIntakeService(t, pool)
	input := accountIntakeInput(seed, "create")

	planned, err := service.Plan(ctx, input)
	if err != nil {
		t.Fatalf("Plan 失败：%v", err)
	}
	if planned.Plan.Summary.Create != 1 || len(planned.Plan.Items) != 1 ||
		planned.Plan.Items[0].Action != intake.ActionCreate {
		t.Fatalf("plan=%+v，期望一项 create", planned)
	}
	executed, err := service.Execute(ctx, ExecuteInput{
		PlanInput: input, PlanHash: planned.PlanHash,
		ActorID: "admin_token:9", ActorRole: admin.RoleTenantOperator,
		RequestID: "req-create", Reason: "集成测试",
	})
	if err != nil {
		t.Fatalf("Execute 失败：%v", err)
	}
	if executed.Summary.Created != 1 || len(executed.Items) != 1 ||
		executed.Items[0].Status != StatusCreated ||
		executed.Items[0].ProviderAccountID <= 0 ||
		executed.Items[0].AccountCredentialID <= 0 {
		t.Fatalf("execution=%+v，期望账号与凭据创建成功", executed)
	}
	var accountCount, credentialCount, adminAuditCount, credentialAuditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1 AND name=$2 AND deleted_at IS NULL`,
		seed.tenantID, accountName(input.Account, 0),
	).Scan(&accountCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM account_credentials WHERE tenant_id=$1 AND provider_account_id=$2 AND deleted_at IS NULL`,
		seed.tenantID, executed.Items[0].ProviderAccountID,
	).Scan(&credentialCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM admin_audit_events WHERE tenant_id=$1 AND request_id='req-create'`,
		seed.tenantID,
	).Scan(&adminAuditCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM credential_audit_events WHERE tenant_id=$1 AND provider_account_id=$2`,
		seed.tenantID, executed.Items[0].ProviderAccountID,
	).Scan(&credentialAuditCount); err != nil {
		t.Fatal(err)
	}
	if accountCount != 1 || credentialCount != 1 || adminAuditCount != 1 || credentialAuditCount != 1 {
		t.Fatalf("counts account=%d credential=%d admin_audit=%d credential_audit=%d，期望全为 1",
			accountCount, credentialCount, adminAuditCount, credentialAuditCount)
	}

	_, err = service.Execute(ctx, ExecuteInput{
		PlanInput: input, PlanHash: planned.PlanHash,
		Confirmations: []string{"confirm_credential_rotation"},
		ActorID:       "admin_token:9", ActorRole: admin.RoleTenantOperator,
	})
	if !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("重复执行旧计划 err=%v，期望 ErrPlanChanged", err)
	}
}

func TestServiceCreatesClaudeSetupTokenCredentialEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE providers SET upstream_protocol='anthropic_claude_session' WHERE id=$1 AND tenant_id=$2`, seed.providerID, seed.tenantID); err != nil {
		t.Fatal(err)
	}
	service := newAccountIntakeService(t, pool)
	secret := "setup-token-" + seed.suffix
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	input := PlanInput{
		TenantID: seed.tenantID, SourceKind: intake.SourceClaudeSetupToken,
		DefaultVendor: "untrusted", DefaultAuthMode: credentialstore.AuthModeAPIKey,
		Content: secret,
		Account: AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: "claude-setup-" + seed.suffix, AccountType: "oauth",
		},
		Now: time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
	}
	planned, err := service.Plan(ctx, input)
	if err != nil {
		t.Fatalf("Plan 失败：%v", err)
	}
	if planned.Plan.Summary.Create != 1 || len(planned.Plan.Items) != 1 ||
		planned.Plan.Items[0].Vendor != credentialstore.VendorAnthropic ||
		planned.Plan.Items[0].AuthMode != credentialstore.AuthModeClaudeSetupToken ||
		!containsString(planned.Plan.Items[0].RequiredConfirmations, "confirm_weak_identity") {
		t.Fatalf("plan=%+v，期望强制 Claude Setup Token 模式并要求弱身份确认", planned)
	}
	executed, err := service.Execute(ctx, ExecuteInput{
		PlanInput: input, PlanHash: planned.PlanHash,
		Confirmations: []string{"confirm_weak_identity"},
		ActorID:       "admin_token:9", ActorRole: admin.RoleTenantOperator,
		RequestID: "req-setup-token", Reason: "导入 Claude Setup Token",
	})
	if err != nil {
		t.Fatalf("Execute 失败：%v", err)
	}
	if executed.Summary.Created != 1 || len(executed.Items) != 1 || executed.Items[0].Status != StatusCreated {
		t.Fatalf("execution=%+v，期望账号与凭据创建成功", executed)
	}
	executionJSON, err := json.Marshal(executed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(executionJSON, []byte(secret)) {
		t.Fatalf("执行结果泄漏 Setup Token：%s", executionJSON)
	}
	record, err := service.credentials.ResolveActive(ctx, seed.tenantID, executed.Items[0].ProviderAccountID)
	if err != nil {
		t.Fatalf("ResolveActive 失败：%v", err)
	}
	defer privacy.Zeroize(record.PlaintextPayload)
	if record.Vendor != credentialstore.VendorAnthropic || record.AuthMode != credentialstore.AuthModeClaudeSetupToken ||
		record.State != credentialstore.StateActive {
		t.Fatalf("record vendor/mode/state=%s/%s/%s", record.Vendor, record.AuthMode, record.State)
	}
	handler, err := credentialstore.DefaultHandlerRegistry().MustLookup(record.Vendor, record.AuthMode)
	if err != nil {
		t.Fatal(err)
	}
	material, err := handler.RuntimeMaterial(record.PlaintextPayload)
	if err != nil {
		t.Fatal(err)
	}
	if material.Kind != credentialstore.RuntimeOAuthAccessToken || material.Value != secret {
		t.Fatalf("runtime material kind/value=%q/%q", material.Kind, material.Value)
	}
	var legacyCredentials []byte
	var encryptedPayload []byte
	if err := pool.QueryRow(ctx, `
SELECT pa.credentials, ac.encrypted_payload
FROM provider_accounts pa
JOIN account_credentials ac ON ac.tenant_id=pa.tenant_id AND ac.provider_account_id=pa.id
WHERE pa.tenant_id=$1 AND pa.id=$2 AND ac.id=$3`,
		seed.tenantID, executed.Items[0].ProviderAccountID, executed.Items[0].AccountCredentialID,
	).Scan(&legacyCredentials, &encryptedPayload); err != nil {
		t.Fatal(err)
	}
	if string(legacyCredentials) != "{}" || bytes.Contains(encryptedPayload, []byte(secret)) {
		t.Fatalf("Setup Token 明文落入旧账号字段或可搜索密文：legacy=%s encrypted_contains_secret=%v", legacyCredentials, bytes.Contains(encryptedPayload, []byte(secret)))
	}
	var adminAuditJSON, credentialAuditJSON []byte
	if err := pool.QueryRow(ctx, `
SELECT COALESCE(jsonb_agg(to_jsonb(events)), '[]'::jsonb)
FROM (
    SELECT * FROM admin_audit_events
    WHERE tenant_id=$1 AND target_type='provider_account' AND target_id=$2
) events`, seed.tenantID, executed.Items[0].ProviderAccountID).Scan(&adminAuditJSON); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT COALESCE(jsonb_agg(to_jsonb(events)), '[]'::jsonb)
FROM (
    SELECT * FROM credential_audit_events
    WHERE tenant_id=$1 AND provider_account_id=$2
) events`, seed.tenantID, executed.Items[0].ProviderAccountID).Scan(&credentialAuditJSON); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(adminAuditJSON, []byte(secret)) || bytes.Contains(credentialAuditJSON, []byte(secret)) || strings.Contains(logs.String(), secret) {
		t.Fatalf("Setup Token 泄漏到审计或日志：admin=%s credential=%s logs=%s", adminAuditJSON, credentialAuditJSON, logs.String())
	}
	if string(adminAuditJSON) == "[]" || string(credentialAuditJSON) == "[]" {
		t.Fatalf("缺少可扫描的审计证据：admin=%s credential=%s", adminAuditJSON, credentialAuditJSON)
	}

	down, err := sqlmigrations.Files.ReadFile("migrations/0191_claude_setup_token_mode.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, string(down)); err == nil {
		t.Fatal("存在 Setup Token 凭据时 0191 down 不应成功")
	}
	if _, err := conn.Exec(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("清理失败的 down 事务：%v", err)
	}
	var preserved int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM account_credentials WHERE id=$1 AND auth_mode='claude_setup_token'`, executed.Items[0].AccountCredentialID).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if preserved != 1 {
		t.Fatalf("失败回滚后 Setup Token 凭据数=%d，期望 1", preserved)
	}
	assertVendorModeConstraint(t, ctx, pool, "account_credentials", "account_credentials_vendor_mode_check", "claude_setup_token", true)
}

func TestServiceImportsOfficialCodexAuthJSONEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE providers SET upstream_protocol='openai_codex' WHERE id=$1 AND tenant_id=$2`, seed.providerID, seed.tenantID); err != nil {
		t.Fatal(err)
	}
	service := newAccountIntakeService(t, pool)
	accessToken := "codex-access-" + seed.suffix
	refreshToken := "codex-refresh-" + seed.suffix
	input := PlanInput{
		TenantID: seed.tenantID, SourceKind: intake.SourceCLI,
		DefaultVendor: "anthropic", DefaultAuthMode: credentialstore.AuthModeAPIKey,
		Content: fmt.Sprintf(`{
			"auth_mode":"chatgpt",
			"OPENAI_API_KEY":"must-not-be-imported",
			"tokens":{
				"access_token":%q,
				"refresh_token":%q,
				"account_id":"workspace-%s",
				"vendor":"anthropic",
				"auth_mode":"api_key",
				"oauth_token_endpoint":"https://attacker.invalid/token",
				"client_secret":"must-not-be-imported"
			}
		}`, accessToken, refreshToken, seed.suffix),
		Account: AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: "codex-" + seed.suffix, AccountType: "session",
		},
		Now: time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
	}
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	planned, err := service.Plan(ctx, input)
	if err != nil {
		t.Fatalf("Plan 失败：%v", err)
	}
	if planned.Plan.Summary.Create != 1 || len(planned.Plan.Items) != 1 ||
		planned.Plan.Items[0].Vendor != credentialstore.VendorOpenAI ||
		planned.Plan.Items[0].AuthMode != credentialstore.AuthModeCodexCLIOAuth ||
		planned.Plan.Items[0].Identity.Source != "import_payload" ||
		planned.Plan.Items[0].Identity.SubjectIdentityTrusted {
		t.Fatalf("plan=%+v，期望强制 Codex 模式且导入身份不受信", planned)
	}
	planJSON, err := json.Marshal(planned)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(planJSON, []byte(accessToken)) || bytes.Contains(planJSON, []byte(refreshToken)) {
		t.Fatalf("计划结果泄漏 Codex 凭据：%s", planJSON)
	}

	executed, err := service.Execute(ctx, ExecuteInput{
		PlanInput: input, PlanHash: planned.PlanHash,
		ActorID: "admin_token:9", ActorRole: admin.RoleTenantOperator,
		RequestID: "req-codex-import", Reason: "导入 Codex auth.json",
	})
	if err != nil {
		t.Fatalf("Execute 失败：%v", err)
	}
	if executed.Summary.Created != 1 || len(executed.Items) != 1 || executed.Items[0].Status != StatusCreated {
		t.Fatalf("execution=%+v，期望 Codex 账号与凭据创建成功", executed)
	}
	record, err := service.credentials.ResolveActive(ctx, seed.tenantID, executed.Items[0].ProviderAccountID)
	if err != nil {
		t.Fatalf("ResolveActive 失败：%v", err)
	}
	defer privacy.Zeroize(record.PlaintextPayload)
	if record.Vendor != credentialstore.VendorOpenAI || record.AuthMode != credentialstore.AuthModeCodexCLIOAuth {
		t.Fatalf("record vendor/mode=%s/%s", record.Vendor, record.AuthMode)
	}
	var payload map[string]any
	if err := json.Unmarshal(record.PlaintextPayload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["access_token"] != accessToken || payload["session_token"] != accessToken || payload["refresh_token"] != refreshToken {
		t.Fatalf("Codex 凭据未归一化：%v", payload)
	}
	for _, forbidden := range []string{"vendor", "auth_mode", "oauth_token_endpoint", "client_secret", "OPENAI_API_KEY"} {
		if _, exists := payload[forbidden]; exists {
			t.Fatalf("未批准字段 %q 进入 Codex 凭据：%v", forbidden, payload)
		}
	}
	handler, err := credentialstore.DefaultHandlerRegistry().MustLookup(record.Vendor, record.AuthMode)
	if err != nil {
		t.Fatal(err)
	}
	material, err := handler.RuntimeMaterial(record.PlaintextPayload)
	if err != nil {
		t.Fatal(err)
	}
	if material.Kind != credentialstore.RuntimeSessionToken || material.Value != accessToken {
		t.Fatalf("runtime material kind/value=%q/%q", material.Kind, material.Value)
	}

	var legacyCredentials, adminAuditJSON, credentialAuditJSON []byte
	var identitySource string
	if err := pool.QueryRow(ctx, `
SELECT pa.credentials, COALESCE(ac.external_identity_source, '')
FROM provider_accounts pa
JOIN account_credentials ac ON ac.tenant_id=pa.tenant_id AND ac.provider_account_id=pa.id
WHERE pa.tenant_id=$1 AND pa.id=$2 AND ac.id=$3`,
		seed.tenantID, executed.Items[0].ProviderAccountID, executed.Items[0].AccountCredentialID,
	).Scan(&legacyCredentials, &identitySource); err != nil {
		t.Fatal(err)
	}
	if identitySource != "import_payload" {
		t.Fatalf("external_identity_source=%q，导入身份不得冒充受信来源", identitySource)
	}
	if err := pool.QueryRow(ctx, `
SELECT COALESCE(jsonb_agg(to_jsonb(events)), '[]'::jsonb)
FROM (SELECT * FROM admin_audit_events WHERE tenant_id=$1 AND target_type='provider_account' AND target_id=$2) events`,
		seed.tenantID, executed.Items[0].ProviderAccountID).Scan(&adminAuditJSON); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT COALESCE(jsonb_agg(to_jsonb(events)), '[]'::jsonb)
FROM (SELECT * FROM credential_audit_events WHERE tenant_id=$1 AND provider_account_id=$2) events`,
		seed.tenantID, executed.Items[0].ProviderAccountID).Scan(&credentialAuditJSON); err != nil {
		t.Fatal(err)
	}
	for label, raw := range map[string][]byte{
		"legacy": legacyCredentials, "admin_audit": adminAuditJSON,
		"credential_audit": credentialAuditJSON, "logs": []byte(logs.String()),
	} {
		if bytes.Contains(raw, []byte(accessToken)) || bytes.Contains(raw, []byte(refreshToken)) || bytes.Contains(raw, []byte("must-not-be-imported")) {
			t.Fatalf("Codex 凭据泄漏到 %s：%s", label, raw)
		}
	}
	if string(legacyCredentials) != "{}" || string(adminAuditJSON) == "[]" || string(credentialAuditJSON) == "[]" {
		t.Fatalf("落库边界或审计证据异常：legacy=%s admin=%s credential=%s", legacyCredentials, adminAuditJSON, credentialAuditJSON)
	}
}

func TestServiceRejectsAmbiguousCodexUpstreamIdentityWithoutWriting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE providers SET upstream_protocol='openai_codex' WHERE id=$1 AND tenant_id=$2`, seed.providerID, seed.tenantID); err != nil {
		t.Fatal(err)
	}
	service := newAccountIntakeService(t, pool)

	const upstreamAccountID = "workspace-ambiguous"
	versions := make(map[int64]int32, 2)
	for index := 0; index < 2; index++ {
		var accountID int64
		if err := pool.QueryRow(ctx, `
INSERT INTO provider_accounts (
    tenant_id, provider_id, channel_id, name, account_type, credentials, extra
) VALUES ($1,$2,$3,$4,'session','{}','{}')
RETURNING id`, seed.tenantID, seed.providerID, seed.channelID,
			fmt.Sprintf("ambiguous-%d-%s", index+1, seed.suffix)).Scan(&accountID); err != nil {
			t.Fatal(err)
		}
		created, err := service.credentials.Create(ctx, credentialstore.CreateCredentialInput{
			TenantID: seed.tenantID, ProviderAccountID: accountID,
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			Payload:                []byte(fmt.Sprintf(`{"access_token":"old-%d","session_token":"old-%d","refresh_token":"refresh-old-%d"}`, index, index, index)),
			ExternalAccountID:      upstreamAccountID,
			ExternalIdentitySource: "import_payload",
		})
		if err != nil {
			t.Fatal(err)
		}
		versions[created.ID] = created.Version
	}

	input := PlanInput{
		TenantID: seed.tenantID, SourceKind: intake.SourceCLI,
		Content: `{"tokens":{"access_token":"new-access","refresh_token":"new-refresh","account_id":"workspace-ambiguous"}}`,
		Account: AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: "must-not-create-" + seed.suffix, AccountType: "session",
		},
		Now: time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
	}
	planned, err := service.Plan(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if planned.Plan.Summary.Conflict != 1 || planned.Plan.Items[0].Action != intake.ActionConflict ||
		planned.Plan.Items[0].Code != "account_scope_ambiguous" {
		t.Fatalf("plan=%+v，期望同一上游身份命中两账号时明确冲突", planned)
	}
	executed, err := service.Execute(ctx, ExecuteInput{
		PlanInput: input, PlanHash: planned.PlanHash,
		ActorID: "admin_token:9", ActorRole: admin.RoleTenantOperator,
		RequestID: "req-codex-ambiguous",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed.Summary.Conflict != 1 || executed.Items[0].Status != StatusConflict {
		t.Fatalf("execution=%+v，期望冲突项不执行写入", executed)
	}
	var accountCount, auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1 AND deleted_at IS NULL`,
		seed.tenantID).Scan(&accountCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM admin_audit_events WHERE tenant_id=$1 AND request_id='req-codex-ambiguous'`,
		seed.tenantID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if accountCount != 2 || auditCount != 0 {
		t.Fatalf("冲突执行后 account=%d audit=%d，期望保持 2/0", accountCount, auditCount)
	}
	for credentialID, wantVersion := range versions {
		var gotVersion int32
		if err := pool.QueryRow(ctx,
			`SELECT credential_version FROM account_credentials WHERE tenant_id=$1 AND id=$2`,
			seed.tenantID, credentialID).Scan(&gotVersion); err != nil {
			t.Fatal(err)
		}
		if gotVersion != wantVersion {
			t.Fatalf("credential %d version=%d，冲突路径不得轮换，期望 %d", credentialID, gotVersion, wantVersion)
		}
	}
}

func TestServiceRollsBackAccountAndCredentialWhenAdminAuditFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	service := newAccountIntakeService(t, pool)
	input := accountIntakeInput(seed, "rollback")

	planned, err := service.Plan(ctx, input)
	if err != nil {
		t.Fatalf("Plan 失败：%v", err)
	}
	executed, err := service.Execute(ctx, ExecuteInput{
		PlanInput: input, PlanHash: planned.PlanHash,
		ActorID: "admin_token:9", ActorRole: "invalid-role",
		RequestID: "req-rollback",
	})
	if err != nil {
		t.Fatalf("逐项失败应返回完整结果而非中断整个批次：%v", err)
	}
	if executed.Summary.Failed != 1 || executed.Items[0].Status != StatusFailed {
		t.Fatalf("execution=%+v，期望审计失败项标记 failed", executed)
	}
	var accountCount, credentialCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1 AND name=$2`,
		seed.tenantID, accountName(input.Account, 0),
	).Scan(&accountCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM account_credentials WHERE tenant_id=$1 AND external_account_id=$2`,
		seed.tenantID, "rollback-account",
	).Scan(&credentialCount); err != nil {
		t.Fatal(err)
	}
	if accountCount != 0 || credentialCount != 0 {
		t.Fatalf("审计失败后留下孤儿 account=%d credential=%d", accountCount, credentialCount)
	}
}

func TestServiceRotatesExactCredentialWithVersionGuard(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	service := newAccountIntakeService(t, pool)
	var accountID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO provider_accounts (
    tenant_id, provider_id, channel_id, name, account_type, credentials, extra
) VALUES ($1,$2,$3,$4,'session','{}','{}')
RETURNING id`,
		seed.tenantID, seed.providerID, seed.channelID, "existing-"+seed.suffix,
	).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	created, err := service.credentials.Create(ctx, credentialstore.CreateCredentialInput{
		TenantID: seed.tenantID, ProviderAccountID: accountID,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
		Payload:                []byte(`{"refresh_token":"refresh-old"}`),
		ExternalAccountID:      "workspace-existing",
		ExternalIdentitySource: "import_payload",
	})
	if err != nil {
		t.Fatal(err)
	}
	input := PlanInput{
		TenantID: seed.tenantID, SourceKind: intake.SourceJSON,
		DefaultVendor:   credentialstore.VendorOpenAI,
		DefaultAuthMode: credentialstore.AuthModeCodexCLIOAuth,
		Content:         `{"refresh_token":"refresh-new","external_account_id":"workspace-existing"}`,
		Account: AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: "unused-" + seed.suffix, AccountType: "session",
		},
		Now: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
	}
	planned, err := service.Plan(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if planned.Plan.Summary.Update != 1 || planned.Plan.Items[0].ExistingAccountID != accountID ||
		planned.Plan.Items[0].ExistingCredentialID != created.ID ||
		planned.Plan.Items[0].ExistingCredentialVersion != created.Version {
		t.Fatalf("plan=%+v，期望精确命中已有账号与凭据版本", planned)
	}
	executed, err := service.Execute(ctx, ExecuteInput{
		PlanInput: input, PlanHash: planned.PlanHash,
		Confirmations: []string{"confirm_unverified_account_match", "confirm_credential_rotation"},
		ActorID:       "admin_token:9", ActorRole: admin.RoleTenantOperator,
		RequestID: "req-rotate",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed.Summary.Updated != 1 || executed.Items[0].Status != StatusUpdated ||
		executed.Items[0].ProviderAccountID != accountID ||
		executed.Items[0].AccountCredentialID != created.ID ||
		executed.Items[0].CredentialVersion != created.Version+1 {
		t.Fatalf("execution=%+v，期望原 credential 精确轮换并版本递增", executed)
	}
	var accounts, credentials int
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1 AND deleted_at IS NULL`,
		seed.tenantID,
	).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM account_credentials WHERE tenant_id=$1 AND deleted_at IS NULL`,
		seed.tenantID,
	).Scan(&credentials); err != nil {
		t.Fatal(err)
	}
	if accounts != 1 || credentials != 1 {
		t.Fatalf("轮换后 accounts=%d credentials=%d，期望仍为 1/1", accounts, credentials)
	}
	_, err = service.Execute(ctx, ExecuteInput{
		PlanInput: input, PlanHash: planned.PlanHash,
		Confirmations: []string{"confirm_unverified_account_match", "confirm_credential_rotation"},
		ActorID:       "admin_token:9", ActorRole: admin.RoleTenantOperator,
	})
	if !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("旧版本计划重放 err=%v，期望 ErrPlanChanged", err)
	}
}

func TestAccountIntakeMigrationRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAccountIntakePool(t, ctx)
	assertAccountIntakeSchema(t, ctx, pool, true)

	down, err := sqlmigrations.Files.ReadFile("migrations/0190_account_credential_intake_identity.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("执行 0190 down 失败：%v", err)
	}
	assertAccountIntakeSchema(t, ctx, pool, false)

	up, err := sqlmigrations.Files.ReadFile("migrations/0190_account_credential_intake_identity.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("执行 0190 up 失败：%v", err)
	}
	assertAccountIntakeSchema(t, ctx, pool, true)
}

func TestClaudeSetupTokenMigrationRoundTripWithoutRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAccountIntakePool(t, ctx)
	assertVendorModeConstraint(t, ctx, pool, "account_credentials", "account_credentials_vendor_mode_check", "claude_setup_token", true)
	assertVendorModeConstraint(t, ctx, pool, "credential_acquisition_flow_sessions", "credential_acq_vendor_mode_check", "claude_setup_token", true)

	down, err := sqlmigrations.Files.ReadFile("migrations/0191_claude_setup_token_mode.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("无 Setup Token 行时执行 0191 down：%v", err)
	}
	assertVendorModeConstraint(t, ctx, pool, "account_credentials", "account_credentials_vendor_mode_check", "claude_setup_token", false)
	assertVendorModeConstraint(t, ctx, pool, "credential_acquisition_flow_sessions", "credential_acq_vendor_mode_check", "claude_setup_token", false)

	up, err := sqlmigrations.Files.ReadFile("migrations/0191_claude_setup_token_mode.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("重新执行 0191 up：%v", err)
	}
	assertVendorModeConstraint(t, ctx, pool, "account_credentials", "account_credentials_vendor_mode_check", "claude_setup_token", true)
	assertVendorModeConstraint(t, ctx, pool, "credential_acquisition_flow_sessions", "credential_acq_vendor_mode_check", "claude_setup_token", true)
}

type accountIntakeSeed struct {
	tenantID   int64
	providerID int64
	channelID  int64
	suffix     string
}

func accountIntakeInput(seed accountIntakeSeed, identity string) PlanInput {
	return PlanInput{
		TenantID: seed.tenantID, SourceKind: intake.SourceJSON,
		DefaultVendor:   credentialstore.VendorOpenAI,
		DefaultAuthMode: credentialstore.AuthModeAPIKey,
		Content:         fmt.Sprintf(`{"api_key":"sk-%s-%s","external_account_id":"%s-account"}`, identity, seed.suffix, identity),
		Account: AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: identity + "-" + seed.suffix, AccountType: "api_key",
		},
		Now: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
	}
}

func newAccountIntakeService(t *testing.T, pool *pgxpool.Pool) *Service {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("test-key", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(pool, credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry()), nil)
}

func seedAccountIntake(t *testing.T, ctx context.Context, pool *pgxpool.Pool) accountIntakeSeed {
	t.Helper()
	seed := accountIntakeSeed{suffix: uuid.NewString()[:8]}
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "intake-"+seed.suffix).Scan(&seed.tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM channel_health_audit WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM channel_health_records WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM credential_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM account_credentials WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM provider_accounts WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM channels WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM pool_groups WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM providers WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM tenants WHERE id=$1`, seed.tenantID)
	})
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1,$2,$3,'openai_chat') RETURNING id`,
		seed.tenantID, "provider-"+seed.suffix, "Provider "+seed.suffix,
	).Scan(&seed.providerID); err != nil {
		t.Fatal(err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`,
		seed.tenantID, "pool-"+seed.suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1,$2,$3) RETURNING id`,
		seed.tenantID, poolGroupID, "channel-"+seed.suffix,
	).Scan(&seed.channelID); err != nil {
		t.Fatal(err)
	}
	return seed
}

func openAccountIntakePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("HUAKAI_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("未设置 HUAKAI_TEST_DATABASE_URL/HUAKAI_DATABASE_URL，跳过 integration_pg")
	}
	adminConn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminConn.Close(ctx)
	databaseName := "huakai_account_intake_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+quoteIdentifier(databaseName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Logf("连接维护库清理临时数据库失败：%v", err)
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(),
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1", databaseName)
		if _, err := cleanup.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quoteIdentifier(databaseName)); err != nil {
			t.Logf("删除临时数据库失败：%v", err)
		}
	})
	testDSN := replaceDatabaseName(t, dsn, databaseName)
	if err := dbmigrate.Up(sqlmigrations.Files, testDSN); err != nil {
		t.Fatalf("迁移临时数据库失败：%v", err)
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: testDSN})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func replaceDatabaseName(t *testing.T, dsn, databaseName string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + databaseName
	return parsed.String()
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func assertAccountIntakeSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool, present bool) {
	t.Helper()
	var columns, indexes int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int
FROM information_schema.columns
WHERE table_schema='public'
  AND table_name='account_credentials'
  AND column_name IN ('external_subject_id','external_identity_source','credential_material_fingerprint')`,
	).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int
FROM pg_indexes
WHERE schemaname='public'
  AND indexname IN ('idx_account_credentials_external_subject','idx_account_credentials_material_fingerprint')`,
	).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if present && (columns != 3 || indexes != 2) {
		t.Fatalf("schema columns=%d indexes=%d，期望 3/2", columns, indexes)
	}
	if !present && (columns != 0 || indexes != 0) {
		t.Fatalf("down 后 schema columns=%d indexes=%d，期望 0/0", columns, indexes)
	}
}

func assertVendorModeConstraint(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, constraint, mode string, present bool) {
	t.Helper()
	var definition string
	if err := pool.QueryRow(ctx, `
SELECT pg_get_constraintdef(c.oid)
FROM pg_constraint c
WHERE c.conrelid=$1::regclass AND c.conname=$2`, table, constraint).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(definition, mode) != present {
		t.Fatalf("约束 %s.%s=%q，模式 %q presence=%v，期望 %v", table, constraint, definition, mode, strings.Contains(definition, mode), present)
	}
}
