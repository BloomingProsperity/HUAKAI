//go:build integration_pg

package accountbundle

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/accountsource"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

func TestBundleExportAndStructureImportEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := openBundlePool(t, ctx)
	sourceTenant, sourceProvider, sourceChannel, sourceCode := seedBundleTarget(t, ctx, pool, "source")
	targetTenant, targetProvider, targetChannel, _ := seedBundleTarget(t, ctx, pool, "target")
	keys, err := credentialstore.NewStaticKeyProvider("test-key", bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	credentialStore := credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry())
	intakeService := accountintake.NewService(pool, credentialStore, nil)
	enabled := true
	input := accountintake.PlanInput{
		TenantID: sourceTenant, SourceKind: intake.SourceJSON,
		DefaultVendor: "openai", DefaultAuthMode: "api_key",
		Content: `{"api_key":"sk-bundle-secret","external_account_id":"upstream-account-7","external_subject_id":"subject-7","external_account_email":"owner@example.com"}`,
		Account: accountintake.AccountDefaults{ProviderID: sourceProvider, ChannelID: sourceChannel, NamePrefix: "bundle-source", AccountType: "api_key", Enabled: &enabled},
	}
	planned, err := intakeService.Plan(ctx, input)
	if err != nil || planned.Plan.Summary.Create != 1 {
		t.Fatalf("账号预检=%+v err=%v", planned, err)
	}
	executed, err := intakeService.Execute(ctx, accountintake.ExecuteInput{
		PlanInput: input, PlanHash: planned.PlanHash,
		Confirmations: planned.Plan.Items[0].RequiredConfirmations,
		ActorID:       "admin_token:1", ActorRole: "tenant_operator", RequestID: "req-seed", Reason: "迁移包导出测试",
	})
	if err != nil || executed.Summary.Created != 1 {
		t.Fatalf("账号创建=%+v err=%v", executed, err)
	}

	now := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	exporter := NewExporter(pool, credentialStore)
	exporter.now = func() time.Time { return now }
	structure, err := exporter.Export(ctx, sourceTenant, ModeStructure, "", 0)
	if err != nil || structure.AccountCount != 1 || structure.CredentialCount != 0 {
		t.Fatalf("structure=%+v err=%v", structure, err)
	}
	if bytes.Contains(structure.Bundle, []byte("sk-bundle-secret")) || bytes.Contains(structure.Bundle, []byte(`"credential"`)) ||
		bytes.Contains(structure.Bundle, []byte(`"auth_mode"`)) || bytes.Contains(structure.Bundle, []byte(`"vendor"`)) {
		t.Fatal("结构包不得包含凭据或认证模式")
	}
	manifest, err := DecodeStructure(structure.Bundle, now)
	if err != nil || len(manifest.Accounts) != 1 || manifest.Accounts[0].Template.SourceProvider != sourceCode {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}

	passphrase := "bundle-transfer-passphrase-2026"
	recovery, err := exporter.Export(ctx, sourceTenant, ModeRecovery, passphrase, 10*time.Minute)
	if err != nil || recovery.AccountCount != 1 || recovery.CredentialCount != 1 {
		t.Fatalf("recovery=%+v err=%v", recovery, err)
	}
	if bytes.Contains(recovery.Bundle, []byte("sk-bundle-secret")) {
		t.Fatal("恢复包外层不得包含凭据明文")
	}
	recovered, err := DecodeRecovery(recovery.Bundle, passphrase, now)
	if err != nil || !bytes.Contains(recovered.Accounts[0].Credential, []byte("sk-bundle-secret")) {
		t.Fatalf("recovered err=%v", err)
	}
	if recovered.Accounts[0].ExternalAccountID != "upstream-account-7" || recovered.Accounts[0].ExternalSubjectID != "subject-7" ||
		recovered.Accounts[0].ExternalAccountEmail != "owner@example.com" || recovered.Accounts[0].IdentitySource == "" {
		t.Fatalf("恢复包身份元数据=%+v", recovered.Accounts[0])
	}
	ZeroizeManifest(&recovered)

	importer := NewStructureImporter(pool)
	mappings := []accountsource.Mapping{{SourceProvider: sourceCode, ProviderID: targetProvider, ChannelID: targetChannel}}
	plan, err := importer.Plan(ctx, StructurePlanInput{TenantID: targetTenant, Manifest: manifest, Mappings: mappings})
	if err != nil || len(plan.Items) != 1 || plan.Items[0].Action != "create" {
		t.Fatalf("结构导入预检=%+v err=%v", plan, err)
	}
	result, err := importer.Execute(ctx, StructureExecuteInput{
		StructurePlanInput: StructurePlanInput{TenantID: targetTenant, Manifest: manifest, Mappings: mappings},
		Selections:         []StructureSelection{{Index: 0, PlanHash: plan.Items[0].PlanHash}},
		ActorID:            "admin_token:2", ActorRole: "tenant_operator", RequestID: "req-structure", Reason: "结构迁移导入测试",
	})
	if err != nil || len(result.Items) != 1 || result.Items[0].Status != "created" {
		t.Fatalf("结构导入=%+v err=%v", result, err)
	}
	var importedEnabled bool
	var credentialCount int
	if err := pool.QueryRow(ctx, `SELECT enabled FROM provider_accounts WHERE id=$1 AND tenant_id=$2`, result.Items[0].ProviderAccountID, targetTenant).Scan(&importedEnabled); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM account_credentials WHERE provider_account_id=$1 AND tenant_id=$2`, result.Items[0].ProviderAccountID, targetTenant).Scan(&credentialCount); err != nil {
		t.Fatal(err)
	}
	if importedEnabled || credentialCount != 0 {
		t.Fatalf("imported enabled=%v credential_count=%d，期望禁用且无凭据", importedEnabled, credentialCount)
	}
	replanned, err := importer.Plan(ctx, StructurePlanInput{TenantID: targetTenant, Manifest: manifest, Mappings: mappings})
	if err != nil || replanned.Items[0].Action != "conflict" || replanned.Items[0].Code != "account_name_exists" {
		t.Fatalf("重复导入预检=%+v err=%v", replanned, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE account_credentials SET state='revoked' WHERE id=$1 AND tenant_id=$2`, executed.Items[0].AccountCredentialID, sourceTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := exporter.Export(ctx, sourceTenant, ModeRecovery, passphrase, 10*time.Minute); !errors.Is(err, ErrRecoveryIncomplete) {
		t.Fatalf("无活动凭据恢复导出 err=%v，期望明确拒绝不完整恢复包", err)
	}
}

func TestStructurePlanRejectsDuplicateMappedNames(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := openBundlePool(t, ctx)
	tenantID, providerID, channelID, sourceCode := seedBundleTarget(t, ctx, pool, "duplicate")
	now := time.Now().UTC()
	manifest := Manifest{Version: ManifestVersion, BundleID: uuid.NewString(), Mode: ModeStructure, CreatedAt: now,
		Accounts: []Account{
			{Template: accountsource.AccountTemplate{Name: "same", SourceProvider: sourceCode, AccountType: "api_key"}},
			{Template: accountsource.AccountTemplate{Name: "same", SourceProvider: sourceCode, AccountType: "api_key"}},
		}}
	plan, err := NewStructureImporter(pool).Plan(ctx, StructurePlanInput{TenantID: tenantID, Manifest: manifest,
		Mappings: []accountsource.Mapping{{SourceProvider: sourceCode, ProviderID: providerID, ChannelID: channelID}}})
	if err != nil || len(plan.Items) != 2 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	for _, item := range plan.Items {
		if item.Action != "conflict" || item.Code != "duplicate_target_name" || item.PlanHash != "" {
			t.Fatalf("item=%+v，期望显式重复冲突", item)
		}
	}
}

func TestStructureImportRequiresAndAcceptsMixedChannelConfirmation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := openBundlePool(t, ctx)
	tenantID, firstProvider, channelID, _ := seedBundleTarget(t, ctx, pool, "risk")
	importer := NewStructureImporter(pool)
	now := time.Now().UTC()
	first := Manifest{Version: ManifestVersion, BundleID: uuid.NewString(), Mode: ModeStructure, CreatedAt: now,
		Accounts: []Account{{Template: accountsource.AccountTemplate{Name: "first", SourceProvider: "first-source", AccountType: "api_key"}}}}
	firstMapping := []accountsource.Mapping{{SourceProvider: "first-source", ProviderID: firstProvider, ChannelID: channelID}}
	firstPlan, err := importer.Plan(ctx, StructurePlanInput{TenantID: tenantID, Manifest: first, Mappings: firstMapping})
	if err != nil || len(firstPlan.Items) != 1 || firstPlan.Items[0].Action != "create" {
		t.Fatalf("first plan=%+v err=%v", firstPlan, err)
	}
	firstResult, err := importer.Execute(ctx, StructureExecuteInput{
		StructurePlanInput: StructurePlanInput{TenantID: tenantID, Manifest: first, Mappings: firstMapping},
		Selections:         []StructureSelection{{Index: 0, PlanHash: firstPlan.Items[0].PlanHash}},
		ActorID:            "admin_token:1", ActorRole: "tenant_operator", Reason: "混合渠道风险测试",
	})
	if err != nil || firstResult.Items[0].Status != "created" {
		t.Fatalf("first result=%+v err=%v", firstResult, err)
	}
	var secondProvider int64
	if err := pool.QueryRow(ctx, `INSERT INTO providers (tenant_id,code,display_name,upstream_protocol) VALUES ($1,$2,$3,'openai_chat') RETURNING id`, tenantID, "second-"+uuid.NewString()[:8], "Second").Scan(&secondProvider); err != nil {
		t.Fatal(err)
	}
	second := Manifest{Version: ManifestVersion, BundleID: uuid.NewString(), Mode: ModeStructure, CreatedAt: now,
		Accounts: []Account{{Template: accountsource.AccountTemplate{Name: "second", SourceProvider: "second-source", AccountType: "api_key"}}}}
	secondMapping := []accountsource.Mapping{{SourceProvider: "second-source", ProviderID: secondProvider, ChannelID: channelID}}
	secondPlan, err := importer.Plan(ctx, StructurePlanInput{TenantID: tenantID, Manifest: second, Mappings: secondMapping})
	if err != nil || len(secondPlan.Items) != 1 || len(secondPlan.Items[0].RequiredConfirmations) != 1 || secondPlan.Items[0].MixedChannelRisk == nil {
		t.Fatalf("second plan=%+v err=%v", secondPlan, err)
	}
	without, err := importer.Execute(ctx, StructureExecuteInput{
		StructurePlanInput: StructurePlanInput{TenantID: tenantID, Manifest: second, Mappings: secondMapping},
		Selections:         []StructureSelection{{Index: 0, PlanHash: secondPlan.Items[0].PlanHash}},
		ActorID:            "admin_token:1", ActorRole: "tenant_operator", Reason: "混合渠道风险测试",
	})
	if err != nil || without.Items[0].Code != "confirmation_required" {
		t.Fatalf("without confirmation=%+v err=%v", without, err)
	}
	with, err := importer.Execute(ctx, StructureExecuteInput{
		StructurePlanInput: StructurePlanInput{TenantID: tenantID, Manifest: second, Mappings: secondMapping},
		Selections:         []StructureSelection{{Index: 0, PlanHash: secondPlan.Items[0].PlanHash, Confirmations: []string{"confirm_mixed_channel_risk"}}},
		ActorID:            "admin_token:1", ActorRole: "tenant_operator", Reason: "混合渠道风险测试",
	})
	if err != nil || with.Items[0].Status != "created" {
		t.Fatalf("with confirmation=%+v err=%v", with, err)
	}
}

func openBundlePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("HUAKAI_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("未设置 HUAKAI_TEST_DATABASE_URL/HUAKAI_DATABASE_URL，跳过 integration_pg")
	}
	adminConnection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminConnection.Close(ctx)
	databaseName := "huakai_account_bundle_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminConnection.Exec(ctx, "CREATE DATABASE "+quoteBundleIdentifier(databaseName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1", databaseName)
		_, _ = cleanup.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quoteBundleIdentifier(databaseName))
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + databaseName
	if err := dbmigrate.Up(sqlmigrations.Files, parsed.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: parsed.String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedBundleTarget(t *testing.T, ctx context.Context, pool *pgxpool.Pool, prefix string) (int64, int64, int64, string) {
	t.Helper()
	suffix := uuid.NewString()[:8]
	providerCode := prefix + "-" + suffix
	var tenantID, providerID, poolGroupID, channelID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, prefix+"-"+suffix).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO providers (tenant_id,code,display_name,upstream_protocol) VALUES ($1,$2,$3,'openai_chat') RETURNING id`, tenantID, providerCode, "Provider "+suffix).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id,name) VALUES ($1,$2) RETURNING id`, tenantID, "pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id,pool_group_id,name) VALUES ($1,$2,$3) RETURNING id`, tenantID, poolGroupID, "channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	return tenantID, providerID, channelID, providerCode
}

func quoteBundleIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
