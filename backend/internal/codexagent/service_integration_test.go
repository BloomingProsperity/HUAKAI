//go:build integration_pg

package codexagent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

func TestRuntimeServiceCoordinatesRegistrationRecoveryAndCredentialVersions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := openCodexAgentPool(t, ctx)
	tenantID, accountID := seedCodexAgentAccount(t, ctx, pool)
	keys, err := credentialstore.NewStaticKeyProvider("test-key", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	credentialStore := credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry())
	payloadV1 := codexAgentPayload(t, "runtime-v1", "account-upstream", "user-upstream", "")
	metaV1, err := credentialStore.Create(ctx, credentialstore.CreateCredentialInput{
		TenantID: tenantID, ProviderAccountID: accountID, Vendor: credentialstore.VendorOpenAI,
		AuthMode: credentialstore.AuthModeCodexAgentIdentity, Payload: payloadV1, ActorID: "admin_token:test",
		ExternalAccountID: "account-upstream", ExternalSubjectID: "user-upstream",
	})
	if err != nil {
		t.Fatal(err)
	}

	var registrations atomic.Int32
	var failNextRegistration atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sequence := registrations.Add(1)
		if failNextRegistration.Swap(false) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"task_id":"task-%d"}`, sequence)
	}))
	defer server.Close()
	client := newRegistrationClient(server.Client(), nil)
	client.baseURL = server.URL
	store := newTaskStore(pool, keys)
	runtime := NewService(store, credentialStore, client)
	vault := provider.NewPostgresCredentialVaultWithStore(pool, credentialStore, runtime)

	const contenders = 12
	credentials := make([]provider.Credential, contenders)
	errorsByIndex := make([]error, contenders)
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			credentials[index], _, errorsByIndex[index] = vault.Resolve(ctx, tenantID, accountID)
		}(index)
	}
	wait.Wait()
	for index, resolveErr := range errorsByIndex {
		if resolveErr != nil {
			t.Fatalf("并发 Resolve[%d]: %v", index, resolveErr)
		}
		if credentials[index].RuntimeRef == "" || !strings.HasPrefix(credentials[index].Value, assertionPrefix) {
			t.Fatalf("并发 Resolve[%d] 返回非动态断言: %+v", index, credentials[index])
		}
	}
	if registrations.Load() != 1 {
		t.Fatalf("12 个并发请求注册次数=%d want 1", registrations.Load())
	}
	assertEncryptedTaskBindings(t, ctx, pool, metaV1.ID, 1, "task-1")

	subjectV1 := taskSubject{TenantID: tenantID, ProviderAccountID: accountID, AccountCredentialID: metaV1.ID, CredentialVersion: metaV1.Version, RuntimeID: "runtime-v1"}
	if invalidated, err := store.invalidate(ctx, subjectV1, credentials[0].RuntimeRef); err != nil || !invalidated {
		t.Fatalf("invalidate=%v err=%v", invalidated, err)
	}
	if _, acquired, err := store.tryAcquire(ctx, subjectV1, 80*time.Millisecond); err != nil || !acquired {
		t.Fatalf("模拟死亡实例租约 acquired=%v err=%v", acquired, err)
	}
	time.Sleep(120 * time.Millisecond)
	afterTakeover, _, err := vault.Resolve(ctx, tenantID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if registrations.Load() != 2 || afterTakeover.RuntimeRef == credentials[0].RuntimeRef {
		t.Fatalf("租约接管 registrations=%d old_ref=%s new_ref=%s", registrations.Load(), credentials[0].RuntimeRef, afterTakeover.RuntimeRef)
	}

	recovered, applicable, err := runtime.RecoverDynamicCredential(ctx, provider.AccountInfo{
		AccountID: accountID, TenantID: tenantID, AccountType: credentialstore.AuthModeCodexAgentIdentity,
		AccountCredentialID: metaV1.ID, CredentialVersion: int(metaV1.Version),
	}, afterTakeover)
	if err != nil || !applicable {
		t.Fatalf("Recover applicable=%v err=%v", applicable, err)
	}
	if registrations.Load() != 3 || recovered.RuntimeRef == afterTakeover.RuntimeRef {
		t.Fatalf("恢复 registrations=%d old_ref=%s new_ref=%s", registrations.Load(), afterTakeover.RuntimeRef, recovered.RuntimeRef)
	}

	payloadV2 := codexAgentPayload(t, "runtime-v2", "account-upstream", "user-upstream", "")
	metaV2, err := credentialStore.Rotate(ctx, credentialstore.RotateCredentialInput{
		TenantID: tenantID, ProviderAccountID: accountID, CredentialID: metaV1.ID,
		Payload: payloadV2, ActorID: "admin_token:test",
	})
	if err != nil {
		t.Fatal(err)
	}
	credentialV2, infoV2, err := vault.Resolve(ctx, tenantID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if infoV2.CredentialVersion != int(metaV2.Version) || registrations.Load() != 4 || credentialV2.RuntimeRef == recovered.RuntimeRef {
		t.Fatalf("v2 info=%+v registrations=%d old_ref=%s new_ref=%s", infoV2, registrations.Load(), recovered.RuntimeRef, credentialV2.RuntimeRef)
	}
	assertEncryptedTaskBindings(t, ctx, pool, metaV1.ID, 2, "task-1", "task-2", "task-3", "task-4")

	payloadV3 := codexAgentPayload(t, "runtime-v3", "account-upstream", "user-upstream", "task-imported")
	metaV3, err := credentialStore.Rotate(ctx, credentialstore.RotateCredentialInput{
		TenantID: tenantID, ProviderAccountID: accountID, CredentialID: metaV1.ID,
		Payload: payloadV3, ActorID: "admin_token:test",
	})
	if err != nil {
		t.Fatal(err)
	}
	credentialV3, infoV3, err := vault.Resolve(ctx, tenantID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if infoV3.CredentialVersion != int(metaV3.Version) || registrations.Load() != 4 || credentialV3.RuntimeRef == credentialV2.RuntimeRef {
		t.Fatalf("导入 task 未直接建立版本绑定: info=%+v registrations=%d", infoV3, registrations.Load())
	}
	assertEncryptedTaskBindings(t, ctx, pool, metaV1.ID, 3, "task-1", "task-2", "task-3", "task-4", "task-imported")

	subjectV3 := taskSubject{TenantID: tenantID, ProviderAccountID: accountID, AccountCredentialID: metaV3.ID, CredentialVersion: metaV3.Version, RuntimeID: "runtime-v3"}
	if invalidated, err := store.invalidate(ctx, subjectV3, credentialV3.RuntimeRef); err != nil || !invalidated {
		t.Fatalf("退避测试 invalidate=%v err=%v", invalidated, err)
	}
	failNextRegistration.Store(true)
	_, _, firstRegistrationErr := vault.Resolve(ctx, tenantID, accountID)
	if firstRegistrationErr == nil {
		t.Fatal("上游注册失败未返回错误")
	}
	var retryAfter *time.Time
	var leaseToken *string
	if err := pool.QueryRow(ctx, `SELECT retry_after,lease_token FROM codex_agent_task_bindings WHERE account_credential_id=$1 AND credential_version=$2`, metaV3.ID, metaV3.Version).Scan(&retryAfter, &leaseToken); err != nil {
		t.Fatal(err)
	}
	if retryAfter == nil || !retryAfter.After(time.Now().UTC()) || leaseToken != nil {
		t.Fatalf("注册失败未写入退避或未释放租约: first_err=%v retry_after=%v lease_token=%v", firstRegistrationErr, retryAfter, leaseToken)
	}
	registrationCount := registrations.Load()
	started := time.Now()
	if _, _, err := vault.Resolve(ctx, tenantID, accountID); err == nil {
		t.Fatal("退避期内请求未快速失败")
	}
	if registrations.Load() != registrationCount || time.Since(started) > time.Second {
		t.Fatalf("退避期重复注册或等待过久: registrations=%d want %d elapsed=%s",
			registrations.Load(), registrationCount, time.Since(started))
	}
}

func TestCodexAgentIdentityMigrationRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openCodexAgentPool(t, ctx)
	assertCodexAgentSchema(t, ctx, pool, true)

	down, err := sqlmigrations.Files.ReadFile("migrations/0193_codex_agent_identity.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("执行 0193 down 失败：%v", err)
	}
	assertCodexAgentSchema(t, ctx, pool, false)

	up, err := sqlmigrations.Files.ReadFile("migrations/0193_codex_agent_identity.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("重新执行 0193 up 失败：%v", err)
	}
	assertCodexAgentSchema(t, ctx, pool, true)
}

func TestCodexAgentIdentityMigrationRollbackRefusesLiveCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openCodexAgentPool(t, ctx)
	tenantID, accountID := seedCodexAgentAccount(t, ctx, pool)
	keys, err := credentialstore.NewStaticKeyProvider("test-key", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry())
	metadata, err := store.Create(ctx, credentialstore.CreateCredentialInput{
		TenantID: tenantID, ProviderAccountID: accountID, Vendor: credentialstore.VendorOpenAI,
		AuthMode: credentialstore.AuthModeCodexAgentIdentity,
		Payload:  codexAgentPayload(t, "runtime-rollback", "account-rollback", "user-rollback", ""),
		ActorID:  "admin_token:test", ExternalAccountID: "account-rollback", ExternalSubjectID: "user-rollback",
	})
	if err != nil {
		t.Fatal(err)
	}
	down, err := sqlmigrations.Files.ReadFile("migrations/0193_codex_agent_identity.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "codex_agent_identity credentials still exist") {
		t.Fatalf("带存量凭据的回滚未明确拒绝: %v", err)
	}
	assertCodexAgentSchema(t, ctx, pool, true)
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM account_credentials WHERE id=$1`, metadata.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("拒绝回滚后凭据数量=%d want 1", count)
	}
}

func assertCodexAgentSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want bool) {
	t.Helper()
	var tableExists, credentialModeAllowed, flowModeAllowed bool
	if err := pool.QueryRow(ctx, `
SELECT to_regclass('public.codex_agent_task_bindings') IS NOT NULL,
       COALESCE((SELECT pg_get_constraintdef(oid) LIKE '%codex_agent_identity%'
                 FROM pg_constraint WHERE conname='account_credentials_vendor_mode_check'), false),
       COALESCE((SELECT pg_get_constraintdef(oid) LIKE '%codex_agent_identity%'
                 FROM pg_constraint WHERE conname='credential_acq_vendor_mode_check'), false)`).Scan(
		&tableExists, &credentialModeAllowed, &flowModeAllowed,
	); err != nil {
		t.Fatal(err)
	}
	if tableExists != want || credentialModeAllowed != want || flowModeAllowed != want {
		t.Fatalf("schema table=%v credential_mode=%v flow_mode=%v want %v",
			tableExists, credentialModeAllowed, flowModeAllowed, want)
	}
}

func assertEncryptedTaskBindings(t *testing.T, ctx context.Context, pool *pgxpool.Pool, credentialID int64, expected int, plaintexts ...string) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT encrypted_task_id FROM codex_agent_task_bindings WHERE account_credential_id=$1 ORDER BY credential_version`, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var ciphertext []byte
		if err := rows.Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		for _, plaintext := range plaintexts {
			if bytes.Contains(ciphertext, []byte(plaintext)) {
				t.Fatalf("第 %d 条任务绑定密文包含任务明文 %q", count, plaintext)
			}
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("任务绑定数=%d want %d", count, expected)
	}
}

func codexAgentPayload(t *testing.T, runtimeID, accountID, userID, taskID string) []byte {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"runtime_id": runtimeID, "private_key_pkcs8": base64.StdEncoding.EncodeToString(der),
		"upstream_account_id": accountID, "upstream_user_id": userID, "task_id": taskID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func openCodexAgentPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
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
	databaseName := "huakai_codex_agent_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminConnection.Exec(ctx, "CREATE DATABASE "+quoteCodexAgentIdentifier(databaseName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, connectErr := pgx.Connect(context.Background(), dsn)
		if connectErr != nil {
			t.Logf("连接维护库清理临时数据库失败: %v", connectErr)
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1", databaseName)
		_, _ = cleanup.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quoteCodexAgentIdentifier(databaseName))
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + databaseName
	testDSN := parsed.String()
	if err := dbmigrate.Up(sqlmigrations.Files, testDSN); err != nil {
		t.Fatalf("迁移临时数据库失败: %v", err)
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: testDSN})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedCodexAgentAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (tenantID, accountID int64) {
	t.Helper()
	suffix := uuid.NewString()[:8]
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "codex-agent-"+suffix).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	var providerID, poolGroupID, channelID int64
	if err := pool.QueryRow(ctx, `INSERT INTO providers (tenant_id,code,display_name,upstream_protocol) VALUES ($1,$2,$3,'openai_codex') RETURNING id`, tenantID, "openai-"+suffix, "OpenAI "+suffix).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id,name) VALUES ($1,$2) RETURNING id`, tenantID, "group-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id,pool_group_id,name) VALUES ($1,$2,$3) RETURNING id`, tenantID, poolGroupID, "channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO provider_accounts (tenant_id,provider_id,channel_id,name,account_type,credentials) VALUES ($1,$2,$3,$4,'oauth','{}'::jsonb) RETURNING id`, tenantID, providerID, channelID, "account-"+suffix).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	return tenantID, accountID
}

func quoteCodexAgentIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
