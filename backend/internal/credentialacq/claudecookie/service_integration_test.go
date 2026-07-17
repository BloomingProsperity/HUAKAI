//go:build integration_pg

package claudecookie

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

type fixedExchanger struct {
	result ExchangeResult
}

func (e fixedExchanger) Exchange(context.Context, string, string) (ExchangeResult, error) {
	return e.result, nil
}

func TestClaudeCookieIntakeEncryptsExpiresAndConsumesExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := openClaudeCookiePool(t, ctx)
	tenantID, providerID, channelID := seedClaudeCookieGraph(t, ctx, pool)
	keys, err := credentialstore.NewStaticKeyProvider("test-key", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	store := NewStore(pool, keys).WithNow(func() time.Time { return now })
	credentialStore := credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry())
	accounts := accountintake.NewService(pool, credentialStore, nil)
	service := NewService(fixedExchanger{result: ExchangeResult{
		AccessToken: "access-secret", RefreshToken: "refresh-secret", TokenType: "Bearer",
		Scope: "user:profile user:inference", ExpiresIn: []byte(`3600`),
		AccountUUID: "account-upstream-1", AccountEmailAddress: "owner@example.com",
		Organization: Organization{ID: "org-1", Name: "Primary"},
	}}, store, accounts).WithNow(func() time.Time { return now })

	session, err := service.Convert(ctx, ConvertInput{
		TenantID: tenantID, SessionKey: "raw-cookie-must-not-persist",
		ActorID: "admin_token:9", ActorRole: admin.RoleTenantOperator, RequestID: "req-convert",
	})
	if err != nil {
		t.Fatalf("Convert 失败：%v", err)
	}
	var encrypted []byte
	var status string
	if err := pool.QueryRow(ctx, `
		SELECT encrypted_candidate,status FROM account_credential_intake_sessions
		WHERE id=$1::uuid AND tenant_id=$2`, session.ID, tenantID).Scan(&encrypted, &status); err != nil {
		t.Fatal(err)
	}
	if status != StatusReady || len(encrypted) == 0 || bytes.Contains(encrypted, []byte("access-secret")) ||
		bytes.Contains(encrypted, []byte("refresh-secret")) || bytes.Contains(encrypted, []byte("raw-cookie-must-not-persist")) {
		t.Fatalf("短时会话未正确加密或泄漏明文，status=%s bytes=%d", status, len(encrypted))
	}
	if _, err := service.Plan(ctx, PlanInput{
		TenantID: tenantID, SessionID: session.ID,
		Account: accountintake.AccountDefaults{ProviderID: providerID, ChannelID: channelID, NamePrefix: "claude-cookie", AccountType: "oauth"},
		ActorID: "admin_token:other",
	}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("其他租户管理令牌接管 err=%v，期望 ErrSessionNotFound", err)
	}
	planInput := PlanInput{
		TenantID: tenantID, SessionID: session.ID,
		Account: accountintake.AccountDefaults{ProviderID: providerID, ChannelID: channelID, NamePrefix: "claude-cookie", AccountType: "oauth"},
		ActorID: "admin_token:9",
	}
	planned, err := service.Plan(ctx, planInput)
	if err != nil {
		t.Fatalf("Plan 失败：%v", err)
	}
	if planned.Plan.Summary.Create != 1 || len(planned.Plan.Items) != 1 {
		t.Fatalf("plan=%+v，期望一项 create", planned)
	}

	const contenders = 12
	start := make(chan struct{})
	type outcome struct {
		result accountintake.ExecutionResult
		err    error
	}
	outcomes := make(chan outcome, contenders)
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			result, err := service.Execute(ctx, ExecuteInput{
				PlanInput: planInput, PlanHash: planned.PlanHash,
				ActorRole: admin.RoleTenantOperator, RequestID: fmt.Sprintf("req-execute-%d", index),
			})
			outcomes <- outcome{result: result, err: err}
		}(index)
	}
	close(start)
	wait.Wait()
	close(outcomes)
	created := 0
	for outcome := range outcomes {
		if outcome.err == nil && outcome.result.Summary.Created == 1 {
			created++
			continue
		}
		if outcome.err == nil && outcome.result.Summary.Failed == 1 {
			continue
		}
		if errors.Is(outcome.err, ErrSessionConsumed) || errors.Is(outcome.err, ErrSessionExpired) ||
			errors.Is(outcome.err, accountintake.ErrPlanChanged) {
			continue
		}
		t.Fatalf("并发失败结果未分类：result=%+v err=%v", outcome.result, outcome.err)
	}
	if created != 1 {
		t.Fatalf("并发成功数=%d，期望恰好 1", created)
	}
	var accountCount, credentialCount int
	var encryptedAfter []byte
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1 AND name='claude-cookie-001'`, tenantID).Scan(&accountCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM account_credentials WHERE tenant_id=$1 AND vendor='anthropic' AND auth_mode='claude_ai_oauth'`, tenantID).Scan(&credentialCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status,encrypted_candidate FROM account_credential_intake_sessions WHERE id=$1::uuid`, session.ID).Scan(&status, &encryptedAfter); err != nil {
		t.Fatal(err)
	}
	if accountCount != 1 || credentialCount != 1 || status != StatusConsumed || encryptedAfter != nil {
		t.Fatalf("account=%d credential=%d status=%s encrypted=%v", accountCount, credentialCount, status, encryptedAfter)
	}

	updatedService := NewService(fixedExchanger{result: ExchangeResult{
		AccessToken: "access-secret-2", RefreshToken: "refresh-secret-2", TokenType: "Bearer",
		Scope: "user:profile user:inference", ExpiresIn: []byte(`3600`),
		AccountUUID: "account-upstream-1", AccountEmailAddress: "owner@example.com",
		Organization: Organization{ID: "org-1", Name: "Primary"},
	}}, store, accounts).WithNow(func() time.Time { return now })
	updatedSession, err := updatedService.Convert(ctx, ConvertInput{
		TenantID: tenantID, SessionKey: "second-cookie-must-not-persist",
		ActorID: "admin_token:9", ActorRole: admin.RoleTenantOperator, RequestID: "req-convert-update",
	})
	if err != nil {
		t.Fatalf("第二次 Convert 失败：%v", err)
	}
	updatedInput := planInput
	updatedInput.SessionID = updatedSession.ID
	updatedPlan, err := updatedService.Plan(ctx, updatedInput)
	if err != nil {
		t.Fatalf("第二次 Plan 失败：%v", err)
	}
	if updatedPlan.Plan.Summary.Update != 1 || len(updatedPlan.Plan.Items) != 1 ||
		updatedPlan.Plan.Items[0].ExistingAccountID <= 0 || updatedPlan.Plan.Items[0].ExistingCredentialID <= 0 {
		t.Fatalf("第二次 plan=%+v，期望命中原账号并轮换", updatedPlan)
	}
	updatedResult, err := updatedService.Execute(ctx, ExecuteInput{
		PlanInput: updatedInput, PlanHash: updatedPlan.PlanHash,
		Confirmations: updatedPlan.Plan.Items[0].RequiredConfirmations,
		ActorRole:     admin.RoleTenantOperator, RequestID: "req-execute-update",
	})
	if err != nil || updatedResult.Summary.Updated != 1 {
		t.Fatalf("第二次 Execute result=%+v err=%v", updatedResult, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1 AND name='claude-cookie-001'`, tenantID).Scan(&accountCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM account_credentials WHERE tenant_id=$1 AND vendor='anthropic' AND auth_mode='claude_ai_oauth'`, tenantID).Scan(&credentialCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status,encrypted_candidate FROM account_credential_intake_sessions WHERE id=$1::uuid`, updatedSession.ID).Scan(&status, &encryptedAfter); err != nil {
		t.Fatal(err)
	}
	updatedCredential, err := credentialStore.LoadForProviderAccountTest(ctx, tenantID, updatedPlan.Plan.Items[0].ExistingAccountID)
	if err != nil {
		t.Fatal(err)
	}
	defer privacy.Zeroize(updatedCredential.PlaintextPayload)
	if accountCount != 1 || credentialCount != 1 || updatedCredential.CredentialVersion != 2 ||
		!bytes.Contains(updatedCredential.PlaintextPayload, []byte("access-secret-2")) ||
		bytes.Contains(updatedCredential.PlaintextPayload, []byte("access-secret\"")) ||
		status != StatusConsumed || encryptedAfter != nil {
		t.Fatalf("更新后 account=%d credential=%d version=%d status=%s payload=%s",
			accountCount, credentialCount, updatedCredential.CredentialVersion, status, updatedCredential.PlaintextPayload)
	}

	expiringCandidate, err := service.exchanger.Exchange(ctx, "another-cookie", "")
	if err != nil {
		t.Fatal(err)
	}
	built, err := credentialCandidateForTest(tenantID, expiringCandidate, now)
	if err != nil {
		t.Fatal(err)
	}
	defer privacy.Zeroize(built.Payload)
	expiring, err := store.Create(ctx, CreateInput{
		TenantID: tenantID, Candidate: built, Organization: expiringCandidate.Organization,
		ActorID: "admin_token:9", ActorRole: admin.RoleTenantOperator, ExpiresAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	store = store.WithNow(func() time.Time { return now.Add(2 * time.Second) })
	if _, err := store.Load(ctx, tenantID, expiring.ID); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("过期 Load err=%v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status,encrypted_candidate FROM account_credential_intake_sessions WHERE id=$1::uuid`, expiring.ID).Scan(&status, &encryptedAfter); err != nil {
		t.Fatal(err)
	}
	if status != StatusExpired || encryptedAfter != nil {
		t.Fatalf("过期会话未擦除：status=%s encrypted=%v", status, encryptedAfter)
	}
}

func TestClaudeCookieMigrationRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openClaudeCookiePool(t, ctx)
	assertClaudeCookieTable(t, ctx, pool, true)

	down, err := sqlmigrations.Files.ReadFile("migrations/0192_account_credential_intake_sessions.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("执行 0192 down 失败：%v", err)
	}
	assertClaudeCookieTable(t, ctx, pool, false)

	up, err := sqlmigrations.Files.ReadFile("migrations/0192_account_credential_intake_sessions.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("重新执行 0192 up 失败：%v", err)
	}
	assertClaudeCookieTable(t, ctx, pool, true)
}

func assertClaudeCookieTable(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want bool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.account_credential_intake_sessions') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != want {
		t.Fatalf("account_credential_intake_sessions exists=%v want %v", exists, want)
	}
}

func credentialCandidateForTest(tenantID int64, result ExchangeResult, now time.Time) (credentialacq.CredentialCandidate, error) {
	return credentialacq.BuildClaudeAIOAuthCandidate(tenantID, "admin_token:9", credentialacq.ClaudeAIOAuthTokenInput{
		AccessToken: result.AccessToken, RefreshToken: result.RefreshToken, TokenType: result.TokenType,
		Scope: result.Scope, ExpiresIn: result.ExpiresIn, AccountUUID: result.AccountUUID,
		AccountEmailAddress: result.AccountEmailAddress, Email: result.Email,
	}, now)
}

func openClaudeCookiePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
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
	databaseName := "huakai_claude_cookie_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminConnection.Exec(ctx, "CREATE DATABASE "+quoteIdentifier(databaseName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Logf("连接维护库清理临时数据库失败：%v", err)
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1", databaseName)
		_, _ = cleanup.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quoteIdentifier(databaseName))
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + databaseName
	testDSN := parsed.String()
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

func seedClaudeCookieGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (tenantID, providerID, channelID int64) {
	t.Helper()
	suffix := uuid.NewString()[:8]
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "cookie-"+suffix).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id,name) VALUES ($1,$2) RETURNING id`, tenantID, "group-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id,pool_group_id,name) VALUES ($1,$2,$3) RETURNING id`, tenantID, poolGroupID, "channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO providers (tenant_id,code,display_name,upstream_protocol)
		VALUES ($1,$2,$3,'anthropic_claude_session') RETURNING id`, tenantID, "provider-"+suffix, "Provider "+suffix).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	return tenantID, providerID, channelID
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
