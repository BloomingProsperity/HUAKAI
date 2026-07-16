//go:build integration_pg

package accountintake

import (
	"bytes"
	"context"
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

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
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
		seed.tenantID, accountName(input.Account.NamePrefix, 0),
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
		seed.tenantID, accountName(input.Account.NamePrefix, 0),
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
