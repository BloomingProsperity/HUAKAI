//go:build integration_pg

package accountsource

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

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

func TestSourceSessionEncryptsScopesAuditsAndExpires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := openSourcePool(t, ctx)
	tenantID := seedSourceTenant(t, ctx, pool, "source")
	otherTenant := seedSourceTenant(t, ctx, pool, "other")
	keys, err := credentialstore.NewStaticKeyProvider("test-key", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 17, 4, 0, 0, 0, time.UTC)
	store := NewStore(pool, keys).WithNow(func() time.Time { return now })
	secret := "upstream-secret-must-be-encrypted"
	session, err := store.Create(ctx, CreateInput{TenantID: tenantID, SourceKind: intake.SourceCRSSync,
		Items: []Item{{Template: AccountTemplate{Name: "alpha", AccountType: "api_key"},
			Candidate: credentialacq.CredentialCandidate{Vendor: "openai", AuthMode: "api_key", Payload: []byte(`{"api_key":"` + secret + `"}`)}}},
		RedactedContext: map[string]any{"source_host": "relay.example.com"},
		ActorID:         "admin_token:2", ActorRole: "tenant_operator", RequestID: "req-source", ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT encrypted_items FROM account_source_intake_sessions WHERE id=$1::uuid AND tenant_id=$2`, session.ID, tenantID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(secret)) {
		t.Fatal("短时账号源会话包含可搜索凭据明文")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM admin_audit_events WHERE tenant_id=$1 AND action='preview_account_source' AND request_id='req-source'`, tenantID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("audit=%d want 1", auditCount)
	}
	loaded, err := store.Load(ctx, tenantID, session.ID)
	if err != nil || len(loaded.Items) != 1 || !bytes.Contains(loaded.Items[0].Candidate.Payload, []byte(secret)) {
		t.Fatalf("loaded=%+v err=%v", loaded.Session, err)
	}
	ZeroizeItems(loaded.Items)
	if _, err := store.Load(ctx, otherTenant, session.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("跨租户 Load err=%v", err)
	}
	expiredStore := store.WithNow(func() time.Time { return now.Add(2 * time.Minute) })
	if _, err := expiredStore.Load(ctx, tenantID, session.ID); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("过期 Load err=%v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status,encrypted_items FROM account_source_intake_sessions WHERE id=$1::uuid`, session.ID).Scan(&status, &encrypted); err != nil {
		t.Fatal(err)
	}
	if status != StatusExpired || encrypted != nil {
		t.Fatalf("status=%s encrypted=%v", status, encrypted)
	}
}

func TestSourceSessionMigrationRefusesActiveRollback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := openSourcePool(t, ctx)
	tenantID := seedSourceTenant(t, ctx, pool, "rollback")
	keys, _ := credentialstore.NewStaticKeyProvider("test-key", bytes.Repeat([]byte{7}, 32))
	store := NewStore(pool, keys)
	_, err := store.Create(ctx, CreateInput{TenantID: tenantID, SourceKind: intake.SourceAccountRecovery,
		Items:   []Item{{Template: AccountTemplate{Name: "alpha", AccountType: "api_key"}, Candidate: credentialacq.CredentialCandidate{Vendor: "openai", AuthMode: "api_key", Payload: []byte(`{"api_key":"secret"}`)}}},
		ActorID: "admin_token:2", ActorRole: "tenant_operator"})
	if err != nil {
		t.Fatal(err)
	}
	down, err := sqlmigrations.Files.ReadFile("migrations/0195_account_source_intake_sessions.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("存在有效短时会话时迁移回滚不应成功")
	}
	if _, err := pool.Exec(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.account_source_intake_sessions') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("table exists=%v err=%v", exists, err)
	}
}

func TestSourceSessionRunsThroughUnifiedAccountIntake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := openSourcePool(t, ctx)
	tenantID := seedSourceTenant(t, ctx, pool, "pipeline")
	providerID, channelID := seedSourceTarget(t, ctx, pool, tenantID)
	keys, err := credentialstore.NewStaticKeyProvider("test-key", bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(pool, keys)
	session, err := store.Create(ctx, CreateInput{
		TenantID: tenantID, SourceKind: intake.SourceAccountRecovery,
		Items: []Item{{
			Template:  AccountTemplate{Name: "restored-account", SourceProvider: "source-openai", AccountType: "api_key", Enabled: true},
			Candidate: credentialacq.CredentialCandidate{Vendor: "openai", AuthMode: "api_key", Payload: []byte(`{"api_key":"sk-restored-secret"}`)},
		}},
		ActorID: "admin_token:2", ActorRole: "tenant_operator", RequestID: "req-preview",
	})
	if err != nil {
		t.Fatal(err)
	}
	intakeService := accountintake.NewService(pool, credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry()), nil)
	service := NewService(store, intakeService)
	mapping := []Mapping{{SourceProvider: "source-openai", ProviderID: providerID, ChannelID: channelID}}
	plan, err := service.Plan(ctx, PlanInput{TenantID: tenantID, SessionID: session.ID, Mappings: mapping})
	if err != nil || len(plan.Items) != 1 || plan.Items[0].Code != "planned" || plan.Items[0].PlanHash == "" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	confirmations := append([]string(nil), plan.Items[0].Plan.Plan.Items[0].RequiredConfirmations...)
	executed, err := service.Execute(ctx, ExecuteInput{
		PlanInput:      PlanInput{TenantID: tenantID, SessionID: session.ID, Mappings: mapping},
		ExpectedSource: intake.SourceAccountRecovery,
		Selections:     []ExecuteSelection{{Index: 0, PlanHash: plan.Items[0].PlanHash, Confirmations: confirmations}},
		ActorID:        "admin_token:2", ActorRole: "tenant_operator", RequestID: "req-execute", Reason: "账号迁移集成测试",
	})
	if err != nil || len(executed.Items) != 1 || executed.Items[0].Status != accountintake.StatusCreated || executed.Items[0].Result == nil {
		t.Fatalf("execution=%+v err=%v", executed, err)
	}
	var accountCount, credentialCount, auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1 AND name='restored-account' AND deleted_at IS NULL`, tenantID).Scan(&accountCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM account_credentials WHERE tenant_id=$1 AND provider_account_id=$2 AND deleted_at IS NULL`, tenantID, executed.Items[0].Result.Items[0].ProviderAccountID).Scan(&credentialCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM admin_audit_events WHERE tenant_id=$1 AND request_id='req-execute'`, tenantID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if accountCount != 1 || credentialCount != 1 || auditCount != 1 {
		t.Fatalf("account=%d credential=%d audit=%d，期望全部为 1", accountCount, credentialCount, auditCount)
	}
}

func openSourcePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
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
	databaseName := "huakai_account_source_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminConnection.Exec(ctx, "CREATE DATABASE "+quoteSourceIdentifier(databaseName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1", databaseName)
		_, _ = cleanup.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quoteSourceIdentifier(databaseName))
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

func seedSourceTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, prefix string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, prefix+"-"+uuid.NewString()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedSourceTarget(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) (int64, int64) {
	t.Helper()
	suffix := uuid.NewString()[:8]
	var providerID, poolGroupID, channelID int64
	if err := pool.QueryRow(ctx, `INSERT INTO providers (tenant_id,code,display_name,upstream_protocol) VALUES ($1,$2,$3,'openai_chat') RETURNING id`, tenantID, "target-"+suffix, "Target "+suffix).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id,name) VALUES ($1,$2) RETURNING id`, tenantID, "pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id,pool_group_id,name) VALUES ($1,$2,$3) RETURNING id`, tenantID, poolGroupID, "channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	return providerID, channelID
}

func quoteSourceIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
