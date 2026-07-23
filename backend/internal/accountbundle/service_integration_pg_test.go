//go:build integration_pg

package accountbundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/accountproxyimport"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/proxysecret"
	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

func TestEncryptedAccountBundleRoundTripAndReplayGuard(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := openAccountBundleIntegrationPool(t, ctx)
	keys, err := credentialstore.NewStaticKeyProvider("bundle-test-key", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	credentials := credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry())
	intakeService := accountintake.NewService(pool, credentials).WithProxyResolver(accountproxyimport.New(keys))
	service := NewService(pool, credentials, keys, intakeService)
	source := seedAccountBundleTenant(t, ctx, pool, "source")
	destination := seedAccountBundleTenant(t, ctx, pool, "destination")

	apiSecret := "sk-bundle-" + uuid.NewString()
	proxyPassword := "proxy-bundle-" + uuid.NewString()
	enabled := true
	concurrency, stickyQueue, fallbackQueue := int32(7), int32(3), int32(5)
	priority, weight := int32(13), int32(4)
	costRatio := 0.8
	rpm, tpm, costLimit := int64(31), int64(32000), int64(900)
	maxSessions, refreshLead := int32(2), int32(1200)
	disableCooling, tlsRotate, customEnabled, poolMode, tempEnabled := true, true, true, true, true
	createInput := accountintake.PlanInput{
		TenantID: source.tenantID, SourceKind: intake.SourceJSON,
		DefaultVendor: credentialstore.VendorOpenAI, DefaultAuthMode: credentialstore.AuthModeAPIKey,
		Content: fmt.Sprintf(`{"api_key":%q,"external_account_id":%q}`, apiSecret, "bundle-upstream-"+source.suffix),
		Account: accountintake.AccountDefaults{
			ProviderID: source.providerID, ChannelID: source.channelID,
			ExactName: "迁移账号-" + source.suffix, AccountType: "api_key", Enabled: &enabled,
			CapConcurrency: &concurrency, CapQueueSticky: &stickyQueue, CapQueueFallback: &fallbackQueue,
			Priority: &priority, StaticWeight: &weight, UpstreamCostRatio: &costRatio,
			Tags: []string{"max", "production"}, Extra: json.RawMessage(`{"origin":"bundle-test"}`),
			ModelAllowList: []string{"gpt-4.1"}, CapabilityFlags: []string{"chat"},
			RPMLimit: &rpm, TPMLimit: &tpm, WindowCostLimitCents: &costLimit, MaxSessions: &maxSessions,
			DisableCooling: &disableCooling, RefreshLeadSeconds: &refreshLead, TLSFingerprintRotate: &tlsRotate,
			CustomErrorCodesEnabled: &customEnabled, CustomErrorCodes: []int32{429, 503}, PoolMode: &poolMode,
			TempUnschedulableEnabled: &tempEnabled,
			TempUnschedulableRules:   json.RawMessage(`[{"status":429,"seconds":90}]`),
			Proxy: &accountintake.ProxyMaterial{
				Protocol: "http", Host: "proxy.example.test", Port: 8080,
				AuthUsername: "bundle-operator", AuthSecret: proxyPassword, SourceRef: "bundle-source-proxy",
			},
		},
		Now: time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC),
	}
	createPlan, err := intakeService.Plan(ctx, createInput)
	if err != nil {
		t.Fatalf("创建源账号预检失败：%v", err)
	}
	created, err := intakeService.Execute(ctx, accountintake.ExecuteInput{
		PlanInput: createInput, PlanHash: createPlan.PlanHash,
		Confirmations: createPlan.Plan.Items[0].RequiredConfirmations,
		ActorID:       "admin_token:source", ActorRole: admin.RoleTenantOperator,
		RequestID: "req-bundle-source", Reason: "建立迁移源账号",
	})
	if err != nil || created.Summary.Created != 1 {
		t.Fatalf("创建源账号结果=%+v err=%v", created, err)
	}
	sourceAccountID := created.Items[0].ProviderAccountID

	exportPlan, err := service.PlanExport(ctx, ExportPlanInput{
		TenantID: source.tenantID, AccountIDs: []int64{sourceAccountID},
		ActorID: "admin_token:source", ActorRole: admin.RoleTenantOperator, ActorScopeTenantID: source.tenantID, Reason: "跨部署迁移",
	})
	if err != nil || exportPlan.Ready != 1 || exportPlan.Conflict != 0 {
		t.Fatalf("导出预检=%+v err=%v", exportPlan, err)
	}
	password := "bundle-password-" + uuid.NewString()
	exported, err := service.ExecuteExport(ctx, ExportExecuteInput{
		ExportPlanInput: ExportPlanInput{
			TenantID: source.tenantID, AccountIDs: []int64{sourceAccountID},
			ActorID: "admin_token:source", ActorRole: admin.RoleTenantOperator, ActorScopeTenantID: source.tenantID,
			RequestID: "req-bundle-export", Reason: "跨部署迁移",
		},
		PlanHash: exportPlan.PlanHash, Password: password, Confirmation: exportConfirmation,
	})
	if err != nil || exported.AccountCount != 1 || exported.ProxyCount != 1 {
		t.Fatalf("导出结果=%+v err=%v", exported, err)
	}
	exportedJSON, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(exportedJSON, []byte(apiSecret)) || bytes.Contains(exportedJSON, []byte(proxyPassword)) || bytes.Contains(exportedJSON, []byte(password)) {
		t.Fatal("加密迁移包响应泄露账号、代理或包密码")
	}

	destinations := map[string]Destination{
		fmt.Sprintf("provider:%d/channel:%d", source.providerID, source.channelID): {
			ProviderID: destination.providerID, ChannelID: destination.channelID,
		},
	}
	importInput := ImportPlanInput{
		TenantID: destination.tenantID, Envelope: exported.Envelope, Password: password,
		Destinations: destinations, ActorID: "admin_token:destination", ActorRole: admin.RoleTenantOperator,
		ActorScopeTenantID: destination.tenantID, Reason: "恢复账号迁移包",
	}
	importPlan, err := service.PlanImport(ctx, importInput)
	if err != nil || importPlan.Ready != 1 || importPlan.Conflict != 0 || len(importPlan.Items) != 1 {
		t.Fatalf("导入预检=%+v err=%v", importPlan, err)
	}
	item := importPlan.Items[0]
	result, err := service.ExecuteImport(ctx, ImportExecuteInput{
		ImportPlanInput: importInput, BundleHash: importPlan.BundleHash,
		Entries: []ImportExecuteEntry{{
			AccountRef: item.AccountRef, PlanHash: item.PlanHash, Confirmations: item.RequiredConfirmations,
		}},
	})
	if err != nil || result.Completed != 1 || result.Conflict != 0 || result.Failed != 0 {
		t.Fatalf("导入执行=%+v err=%v", result, err)
	}
	destinationAccountID := result.Items[0].Result.Items[0].ProviderAccountID
	assertImportedAccountBundleState(t, ctx, pool, credentials, keys, destination.tenantID, destinationAccountID, apiSecret, proxyPassword)

	_, err = service.ExecuteImport(ctx, ImportExecuteInput{
		ImportPlanInput: importInput, BundleHash: importPlan.BundleHash,
		Entries: []ImportExecuteEntry{{
			AccountRef: item.AccountRef, PlanHash: item.PlanHash, Confirmations: item.RequiredConfirmations,
		}},
	})
	if !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("旧计划重放 err=%v，期望 %v", err, ErrPlanChanged)
	}
	assertAccountBundleLogs(t, ctx, pool, source.tenantID, destination.tenantID, apiSecret, proxyPassword)
}

func TestOAuthAccountBundleImportsAndKeepsClaimedIdentityUntrusted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := openAccountBundleIntegrationPool(t, ctx)
	keys, err := credentialstore.NewStaticKeyProvider("bundle-oauth-test-key", []byte("abcdef0123456789abcdef0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	credentials := credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry())
	intakeService := accountintake.NewService(pool, credentials)
	service := NewService(pool, credentials, keys, intakeService)
	source := seedAccountBundleTenant(t, ctx, pool, "oauth-source")
	destination := seedAccountBundleTenant(t, ctx, pool, "oauth-destination")
	for _, providerID := range []int64{source.providerID, destination.providerID} {
		if _, err := pool.Exec(ctx, `
UPDATE providers SET upstream_protocol='anthropic_claude_session' WHERE id=$1`, providerID); err != nil {
			t.Fatal(err)
		}
	}

	accessToken := "oauth-bundle-access-" + uuid.NewString()
	candidate, err := intake.EncodeOAuthCandidate(credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		Payload:              []byte(fmt.Sprintf(`{"access_token":%q,"refresh_token":"refresh"}`, accessToken)),
		ExternalAccountID:    "claimed-upstream-account",
		ExternalAccountEmail: "claimed@example.test",
		AccountIDSource:      accountident.SourceAnthropicAccountID,
	})
	if err != nil {
		t.Fatal(err)
	}
	createInput := accountintake.PlanInput{
		TenantID: source.tenantID, SourceKind: intake.SourceOAuth,
		DefaultVendor: credentialstore.VendorAnthropic, DefaultAuthMode: credentialstore.AuthModeClaudeAIOAuth,
		Content: candidate,
		Account: accountintake.AccountDefaults{
			ProviderID: source.providerID, ChannelID: source.channelID,
			ExactName: "OAuth 迁移源-" + source.suffix, AccountType: "oauth",
		},
		Now: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
	}
	createPlan, err := intakeService.Plan(ctx, createInput)
	if err != nil {
		t.Fatal(err)
	}
	created, err := intakeService.Execute(ctx, accountintake.ExecuteInput{
		PlanInput: createInput, PlanHash: createPlan.PlanHash,
		Confirmations: createPlan.Plan.Items[0].RequiredConfirmations,
		ActorID:       "admin_token:oauth-source", ActorRole: admin.RoleTenantOperator,
		RequestID: "req-oauth-bundle-source", Reason: "建立 OAuth 迁移源账号",
	})
	if err != nil || created.Summary.Created != 1 {
		t.Fatalf("创建 OAuth 源账号结果=%+v err=%v", created, err)
	}

	trustedDestinationCandidate, err := intake.EncodeOAuthCandidate(credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		Payload:              []byte(`{"access_token":"trusted-old-access","refresh_token":"trusted-old-refresh"}`),
		ExternalAccountID:    "claimed-upstream-account",
		ExternalAccountEmail: "verified@example.test",
		AccountIDSource:      accountident.SourceAnthropicAccountID,
	})
	if err != nil {
		t.Fatal(err)
	}
	trustedDestinationInput := accountintake.PlanInput{
		TenantID: destination.tenantID, SourceKind: intake.SourceOAuth,
		DefaultVendor: credentialstore.VendorAnthropic, DefaultAuthMode: credentialstore.AuthModeClaudeAIOAuth,
		Content: trustedDestinationCandidate,
		Account: accountintake.AccountDefaults{
			ProviderID: destination.providerID, ChannelID: destination.channelID,
			ExactName: "可信 OAuth 目标-" + destination.suffix, AccountType: "oauth",
		},
		Now: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
	}
	trustedDestinationPlan, err := intakeService.Plan(ctx, trustedDestinationInput)
	if err != nil {
		t.Fatal(err)
	}
	trustedDestination, err := intakeService.Execute(ctx, accountintake.ExecuteInput{
		PlanInput: trustedDestinationInput, PlanHash: trustedDestinationPlan.PlanHash,
		Confirmations: trustedDestinationPlan.Plan.Items[0].RequiredConfirmations,
		ActorID:       "admin_token:oauth-destination", ActorRole: admin.RoleTenantOperator,
		RequestID: "req-oauth-bundle-trusted-destination", Reason: "建立可信身份目标账号",
	})
	if err != nil || trustedDestination.Summary.Created != 1 {
		t.Fatalf("创建可信目标账号结果=%+v err=%v", trustedDestination, err)
	}
	trustedAccountID := trustedDestination.Items[0].ProviderAccountID
	trustedCredentialID := trustedDestination.Items[0].AccountCredentialID
	trustedCredentialVersion := trustedDestination.Items[0].CredentialVersion

	exportPlan, err := service.PlanExport(ctx, ExportPlanInput{
		TenantID: source.tenantID, AccountIDs: []int64{created.Items[0].ProviderAccountID},
		ActorID: "admin_token:oauth-source", ActorRole: admin.RoleTenantOperator,
		ActorScopeTenantID: source.tenantID, Reason: "迁移 OAuth 账号",
	})
	if err != nil || exportPlan.Ready != 1 {
		t.Fatalf("OAuth 导出预检=%+v err=%v", exportPlan, err)
	}
	password := "oauth-bundle-password-" + uuid.NewString()
	exported, err := service.ExecuteExport(ctx, ExportExecuteInput{
		ExportPlanInput: ExportPlanInput{
			TenantID: source.tenantID, AccountIDs: []int64{created.Items[0].ProviderAccountID},
			ActorID: "admin_token:oauth-source", ActorRole: admin.RoleTenantOperator,
			ActorScopeTenantID: source.tenantID, RequestID: "req-oauth-bundle-export", Reason: "迁移 OAuth 账号",
		},
		PlanHash: exportPlan.PlanHash, Password: password, Confirmation: exportConfirmation,
	})
	if err != nil || exported.AccountCount != 1 {
		t.Fatalf("OAuth 导出结果=%+v err=%v", exported, err)
	}

	destinations := map[string]Destination{
		fmt.Sprintf("provider:%d/channel:%d", source.providerID, source.channelID): {
			ProviderID: destination.providerID, ChannelID: destination.channelID,
		},
	}
	importInput := ImportPlanInput{
		TenantID: destination.tenantID, Envelope: exported.Envelope, Password: password,
		Destinations: destinations, ActorID: "admin_token:oauth-destination",
		ActorRole: admin.RoleTenantOperator, ActorScopeTenantID: destination.tenantID,
		Reason: "恢复 OAuth 账号",
	}
	importPlan, err := service.PlanImport(ctx, importInput)
	if err != nil || importPlan.Ready != 1 || len(importPlan.Items) != 1 {
		t.Fatalf("OAuth 导入预检=%+v err=%v", importPlan, err)
	}
	item := importPlan.Items[0]
	if item.Plan == nil || len(item.Plan.Items) != 1 ||
		item.Plan.Items[0].Action != intake.ActionUpdate ||
		item.Plan.Items[0].ExistingAccountID != trustedAccountID {
		t.Fatalf("不可信迁移身份没有显式指向待确认的已有账号：%+v", item)
	}
	if missing := missingConfirmations(
		[]string{"confirm_unverified_account_match", "confirm_credential_rotation", "confirm_account_config_replace"},
		item.RequiredConfirmations,
	); len(missing) != 0 {
		t.Fatalf("不可信迁移身份缺少人工确认：%v", missing)
	}
	unconfirmed, err := service.ExecuteImport(ctx, ImportExecuteInput{
		ImportPlanInput: importInput, BundleHash: importPlan.BundleHash,
		Entries: []ImportExecuteEntry{{
			AccountRef: item.AccountRef, PlanHash: item.PlanHash,
		}},
	})
	if err != nil || unconfirmed.Conflict != 1 || unconfirmed.Completed != 0 ||
		len(unconfirmed.Items) != 1 || unconfirmed.Items[0].Code != "confirmation_required" {
		t.Fatalf("未确认迁移身份结果=%+v err=%v", unconfirmed, err)
	}
	var versionAfterUnconfirmed int32
	var sourceAfterUnconfirmed string
	if err := pool.QueryRow(ctx, `
SELECT credential_version, COALESCE(external_identity_source, '')
FROM account_credentials WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL`,
		destination.tenantID, trustedCredentialID).Scan(&versionAfterUnconfirmed, &sourceAfterUnconfirmed); err != nil {
		t.Fatal(err)
	}
	if versionAfterUnconfirmed != trustedCredentialVersion ||
		sourceAfterUnconfirmed != accountident.SourceAnthropicAccountID {
		t.Fatalf("未确认迁移错误轮换可信凭据 version=%d source=%q",
			versionAfterUnconfirmed, sourceAfterUnconfirmed)
	}
	result, err := service.ExecuteImport(ctx, ImportExecuteInput{
		ImportPlanInput: importInput, BundleHash: importPlan.BundleHash,
		Entries: []ImportExecuteEntry{{
			AccountRef: item.AccountRef, PlanHash: item.PlanHash,
			Confirmations: item.RequiredConfirmations,
		}},
	})
	if err != nil || result.Completed != 1 || result.Failed != 0 || result.Conflict != 0 {
		t.Fatalf("OAuth 导入执行=%+v err=%v", result, err)
	}
	accountID := result.Items[0].Result.Items[0].ProviderAccountID
	if accountID != trustedAccountID {
		t.Fatalf("显式确认后更新账号=%d，期望可信目标账号=%d", accountID, trustedAccountID)
	}
	record, err := credentials.ResolveActive(ctx, destination.tenantID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	defer privacy.Zeroize(record.PlaintextPayload)
	if record.Vendor != credentialstore.VendorAnthropic ||
		record.AuthMode != credentialstore.AuthModeClaudeAIOAuth ||
		!bytes.Contains(record.PlaintextPayload, []byte(accessToken)) {
		t.Fatalf("OAuth 迁移凭据不完整：vendor=%s mode=%s", record.Vendor, record.AuthMode)
	}
	var externalAccountID, identitySource string
	if err := pool.QueryRow(ctx, `
SELECT COALESCE(external_account_id, ''), COALESCE(external_identity_source, '')
FROM account_credentials WHERE tenant_id=$1 AND provider_account_id=$2 AND deleted_at IS NULL`,
		destination.tenantID, accountID).Scan(&externalAccountID, &identitySource); err != nil {
		t.Fatal(err)
	}
	if externalAccountID != "claimed-upstream-account" || identitySource != accountident.SourceImportPayload {
		t.Fatalf("迁移身份信任错误：id=%q source=%q", externalAccountID, identitySource)
	}

	emptyDestination := seedAccountBundleTenant(t, ctx, pool, "oauth-empty-destination")
	if _, err := pool.Exec(ctx, `
UPDATE providers SET upstream_protocol='anthropic_claude_session' WHERE id=$1`,
		emptyDestination.providerID); err != nil {
		t.Fatal(err)
	}
	createDestinations := map[string]Destination{
		fmt.Sprintf("provider:%d/channel:%d", source.providerID, source.channelID): {
			ProviderID: emptyDestination.providerID, ChannelID: emptyDestination.channelID,
		},
	}
	createImportInput := ImportPlanInput{
		TenantID: emptyDestination.tenantID, Envelope: exported.Envelope, Password: password,
		Destinations: createDestinations, ActorID: "admin_token:oauth-empty-destination",
		ActorRole: admin.RoleTenantOperator, ActorScopeTenantID: emptyDestination.tenantID,
		Reason: "恢复 OAuth 账号到空目标租户",
	}
	createImportPlan, err := service.PlanImport(ctx, createImportInput)
	if err != nil || createImportPlan.Ready != 1 || len(createImportPlan.Items) != 1 ||
		createImportPlan.Items[0].Plan == nil || len(createImportPlan.Items[0].Plan.Items) != 1 ||
		createImportPlan.Items[0].Plan.Items[0].Action != intake.ActionCreate {
		t.Fatalf("OAuth 空目标导入预检=%+v err=%v", createImportPlan, err)
	}
	createItem := createImportPlan.Items[0]
	createdImport, err := service.ExecuteImport(ctx, ImportExecuteInput{
		ImportPlanInput: createImportInput, BundleHash: createImportPlan.BundleHash,
		Entries: []ImportExecuteEntry{{
			AccountRef: createItem.AccountRef, PlanHash: createItem.PlanHash,
			Confirmations: createItem.RequiredConfirmations,
		}},
	})
	if err != nil || createdImport.Completed != 1 || createdImport.Conflict != 0 ||
		createdImport.Failed != 0 || len(createdImport.Items) != 1 ||
		createdImport.Items[0].Result == nil || createdImport.Items[0].Result.Summary.Created != 1 {
		t.Fatalf("OAuth 空目标导入执行=%+v err=%v", createdImport, err)
	}
	createdAccountID := createdImport.Items[0].Result.Items[0].ProviderAccountID
	createdRecord, err := credentials.ResolveActive(ctx, emptyDestination.tenantID, createdAccountID)
	if err != nil {
		t.Fatal(err)
	}
	defer privacy.Zeroize(createdRecord.PlaintextPayload)
	if createdRecord.Vendor != credentialstore.VendorAnthropic ||
		createdRecord.AuthMode != credentialstore.AuthModeClaudeAIOAuth ||
		!bytes.Contains(createdRecord.PlaintextPayload, []byte(accessToken)) {
		t.Fatalf("OAuth 空目标迁移凭据不完整：vendor=%s mode=%s",
			createdRecord.Vendor, createdRecord.AuthMode)
	}
	var createdIdentitySource string
	var createdHealthCount int
	if err := pool.QueryRow(ctx, `
SELECT COALESCE(external_identity_source, '')
FROM account_credentials
WHERE tenant_id=$1 AND provider_account_id=$2 AND state='active' AND deleted_at IS NULL`,
		emptyDestination.tenantID, createdAccountID).Scan(&createdIdentitySource); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM channel_health_state
WHERE tenant_id=$1 AND provider_account_id=$2`,
		emptyDestination.tenantID, createdAccountID).Scan(&createdHealthCount); err != nil {
		t.Fatal(err)
	}
	if createdIdentitySource != accountident.SourceImportPayload || createdHealthCount != 1 {
		t.Fatalf("OAuth 空目标迁移终态 source=%q health=%d",
			createdIdentitySource, createdHealthCount)
	}
}

type accountBundleSeed struct {
	tenantID   int64
	providerID int64
	channelID  int64
	suffix     string
}

func seedAccountBundleTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) accountBundleSeed {
	t.Helper()
	seed := accountBundleSeed{suffix: strings.ReplaceAll(uuid.NewString(), "-", "")[:10]}
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "bundle-"+label+"-"+seed.suffix).Scan(&seed.tenantID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
VALUES ($1,$2,$3,'openai_chat') RETURNING id`, seed.tenantID, "bundle-provider-"+seed.suffix, "Bundle Provider "+seed.suffix).Scan(&seed.providerID); err != nil {
		t.Fatal(err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`, seed.tenantID, "bundle-pool-"+seed.suffix).Scan(&poolGroupID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1,$2,$3) RETURNING id`, seed.tenantID, poolGroupID, "bundle-channel-"+seed.suffix).Scan(&seed.channelID); err != nil {
		t.Fatal(err)
	}
	return seed
}

func assertImportedAccountBundleState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, credentials *credentialstore.Store, keys credentialstore.KeyProvider, tenantID, accountID int64, apiSecret, proxyPassword string) {
	t.Helper()
	var name string
	var concurrency, stickyQueue, fallbackQueue, priority, weight, maxSessions int32
	var rpm, tpm, costLimit int64
	var ratio float64
	var enabled, disableCooling, tlsRotate, customEnabled, poolMode, tempEnabled bool
	var tags, models, flags []string
	var customCodes []int32
	var extra, tempRules, legacyCredentials []byte
	var proxyID *int64
	err := pool.QueryRow(ctx, `
SELECT name, enabled, cap_concurrency, cap_queue_sticky, cap_queue_fallback, priority, static_weight,
       upstream_cost_ratio, tags, extra, model_allow_list, capability_flags,
       rpm_limit, tpm_limit, window_cost_limit_cents, max_sessions, disable_cooling,
       tls_fingerprint_rotate, custom_error_codes_enabled, custom_error_codes,
       pool_mode, temp_unschedulable_enabled, temp_unschedulable_rules, proxy_id, credentials
FROM provider_accounts WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL`, tenantID, accountID).Scan(
		&name, &enabled, &concurrency, &stickyQueue, &fallbackQueue, &priority, &weight,
		&ratio, &tags, &extra, &models, &flags, &rpm, &tpm, &costLimit, &maxSessions,
		&disableCooling, &tlsRotate, &customEnabled, &customCodes, &poolMode, &tempEnabled,
		&tempRules, &proxyID, &legacyCredentials,
	)
	if err != nil {
		t.Fatal(err)
	}
	if name == "" || !enabled || concurrency != 7 || stickyQueue != 3 || fallbackQueue != 5 || priority != 13 || weight != 4 || ratio != 0.8 ||
		rpm != 31 || tpm != 32000 || costLimit != 900 || maxSessions != 2 || !disableCooling || !tlsRotate || !customEnabled || !poolMode || !tempEnabled ||
		len(tags) != 2 || len(models) != 1 || len(flags) != 1 || len(customCodes) != 2 || !bytes.Contains(extra, []byte("bundle-test")) || !bytes.Contains(tempRules, []byte("90")) || proxyID == nil || string(legacyCredentials) != "{}" {
		t.Fatalf("导入账号配置不完整：name=%s enabled=%v concurrency=%d queues=%d/%d priority=%d weight=%d ratio=%v limits=%d/%d/%d sessions=%d tags=%v models=%v flags=%v codes=%v proxy=%v legacy=%s",
			name, enabled, concurrency, stickyQueue, fallbackQueue, priority, weight, ratio, rpm, tpm, costLimit, maxSessions, tags, models, flags, customCodes, proxyID, legacyCredentials)
	}
	record, err := credentials.ResolveActive(ctx, tenantID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	defer privacy.Zeroize(record.PlaintextPayload)
	if !bytes.Contains(record.PlaintextPayload, []byte(apiSecret)) {
		t.Fatal("导入凭据无法恢复原始认证材料")
	}
	var protocol, host string
	var port int32
	var username, encryptedSecret *string
	if err := pool.QueryRow(ctx, `SELECT protocol, host, port, auth_username, auth_secret FROM proxies WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL`, tenantID, *proxyID).Scan(&protocol, &host, &port, &username, &encryptedSecret); err != nil {
		t.Fatal(err)
	}
	if protocol != "http" || host != "proxy.example.test" || port != 8080 || username == nil || *username != "bundle-operator" || encryptedSecret == nil {
		t.Fatalf("导入代理配置不完整：%s://%s:%d username=%v secret=%v", protocol, host, port, username, encryptedSecret != nil)
	}
	plainProxy, err := proxysecret.Decode(ctx, keys, tenantID, *encryptedSecret)
	if err != nil || plainProxy != proxyPassword {
		t.Fatalf("代理秘密恢复失败：match=%v err=%v", plainProxy == proxyPassword, err)
	}
}

func assertAccountBundleLogs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sourceTenantID, destinationTenantID int64, secrets ...string) {
	t.Helper()
	for _, check := range []struct {
		tenantID int64
		action   string
	}{
		{sourceTenantID, "account_bundle_exported"},
		{destinationTenantID, "account_bundle_imported"},
	} {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM admin_audit_events WHERE tenant_id=$1 AND action=$2 AND target_type='account_bundle'`, check.tenantID, check.action).Scan(&count); err != nil || count != 1 {
			t.Fatalf("迁移日志 tenant=%d action=%s count=%d err=%v", check.tenantID, check.action, count, err)
		}
	}
	for _, secret := range secrets {
		var leaks int
		if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM admin_audit_events WHERE payload::text LIKE $1`, "%"+secret+"%").Scan(&leaks); err != nil || leaks != 0 {
			t.Fatalf("迁移秘密进入管理员日志：leaks=%d err=%v", leaks, err)
		}
	}
}

func openAccountBundleIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("HUAKAI_TEST_DATABASE_URL"))
	if baseDSN == "" {
		baseDSN = strings.TrimSpace(os.Getenv("HUAKAI_DATABASE_URL"))
	}
	if baseDSN == "" {
		t.Skip("未设置 HUAKAI_TEST_DATABASE_URL/HUAKAI_DATABASE_URL，跳过 integration_pg")
	}
	adminConn, err := pgx.Connect(ctx, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := "huakai_account_bundle_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+quoteAccountBundleIdentifier(databaseName)); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatal(err)
	}
	if err := adminConn.Close(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, err := pgx.Connect(context.Background(), baseDSN)
		if err != nil {
			t.Logf("连接维护库清理临时数据库失败：%v", err)
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1", databaseName)
		if _, err := cleanup.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quoteAccountBundleIdentifier(databaseName)); err != nil {
			t.Logf("删除临时数据库失败：%v", err)
		}
	})
	parsed, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + databaseName
	testDSN := parsed.String()
	if err := dbmigrate.Up(sqlmigrations.Files, testDSN); err != nil {
		t.Fatalf("迁移临时数据库失败：%v", err)
	}
	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func quoteAccountBundleIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
