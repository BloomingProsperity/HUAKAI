//go:build e2e_codex_live || e2e_grok_live || e2e_chatgpt_session || e2e_openai_image_live || e2e_grok_image_live || e2e_grok_video_live || e2e_gemini_video_live || e2e_concurrency || e2e_upstream || smoke

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

const specializedLiveImportCapability = "advanced_account_intake"

type specializedLiveAccountImportRequest struct {
	TenantID        int64                         `json:"tenant_id"`
	SourceKind      intake.SourceKind             `json:"source_kind,omitempty"`
	DefaultVendor   string                        `json:"default_vendor,omitempty"`
	DefaultAuthMode string                        `json:"default_auth_mode,omitempty"`
	Content         string                        `json:"content"`
	Account         accountintake.AccountDefaults `json:"account"`
	PlanHash        string                        `json:"plan_hash,omitempty"`
	Confirmations   []string                      `json:"confirmations,omitempty"`
	Reason          string                        `json:"reason,omitempty"`
}

type specializedLiveImportResult struct {
	AccountID int64
	Item      accountintake.ExecutionItem
}

type specializedLiveProcesses struct {
	gateway    *exec.Cmd
	sidecar    *exec.Cmd
	socketPath string
}

// useDisposableSpecializedLiveDatabase 为每个顶层活体测试创建一座全新数据库，
// 迁移到当前源码的最新版本，并在所有连接与进程退出后删除整库。调用方提供的
// DSN 只用于取得同一 PostgreSQL 实例的管理连接，不再承载测试业务数据。
func useDisposableSpecializedLiveDatabase(t *testing.T, baseDSN string) string {
	t.Helper()
	baseConfig, err := pgx.ParseConfig(strings.TrimSpace(baseDSN))
	if err != nil {
		t.Fatalf("解析活体测试 PostgreSQL DSN: %v", err)
	}
	databaseName := "huakai_e2e_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	maintenanceDSN, err := specializedLiveDatabaseDSN(baseConfig.ConnString(), "postgres")
	if err != nil {
		t.Fatalf("构造活体测试 PostgreSQL 管理 DSN: %v", err)
	}
	targetDSN, err := specializedLiveDatabaseDSN(baseConfig.ConnString(), databaseName)
	if err != nil {
		t.Fatalf("构造一次性活体测试库 DSN: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, maintenanceDSN)
	if err != nil {
		t.Fatalf("连接活体测试 PostgreSQL 管理库: %v", err)
	}
	identifier := pgx.Identifier{databaseName}.Sanitize()
	// 固定从系统只读模板创建，避免本机 template1 被历史测试污染后把旧表、
	// 所有者和权限复制进一次性数据库，导致迁移结果依赖服务器遗留状态。
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+identifier+" TEMPLATE template0"); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("创建一次性活体测试数据库: %v", err)
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatalf("关闭活体测试 PostgreSQL 管理连接: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanupAdmin, connectErr := pgx.Connect(cleanupCtx, maintenanceDSN)
		if connectErr != nil {
			t.Errorf("连接管理库以删除一次性活体测试数据库: %v", connectErr)
			return
		}
		defer cleanupAdmin.Close(cleanupCtx)
		if _, terminateErr := cleanupAdmin.Exec(cleanupCtx, `
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname=$1 AND pid<>pg_backend_pid()`, databaseName); terminateErr != nil {
			t.Errorf("终止一次性活体测试数据库残余连接: %v", terminateErr)
			return
		}
		if _, dropErr := cleanupAdmin.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+identifier); dropErr != nil {
			t.Errorf("删除一次性活体测试数据库: %v", dropErr)
		}
	})

	if err := dbmigrate.Up(sqlmigrations.Files, targetDSN); err != nil {
		t.Fatalf("迁移一次性活体测试数据库到最新版本: %v", err)
	}
	return targetDSN
}

func specializedLiveDatabaseDSN(baseDSN, databaseName string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseDSN))
	if err != nil {
		return "", err
	}
	if (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return "", fmt.Errorf("只支持 postgres:// 或 postgresql:// URL")
	}
	if strings.TrimSpace(databaseName) == "" || strings.Contains(databaseName, "/") {
		return "", fmt.Errorf("数据库名不合法")
	}
	parsed.Path = "/" + databaseName
	parsed.RawPath = ""
	return parsed.String(), nil
}

func TestSpecializedLiveDatabaseDSNReplacesDatabaseAndPreservesOptions(t *testing.T) {
	t.Parallel()
	got, err := specializedLiveDatabaseDSN(
		"postgres://user:secret@db.example:5432/old_database?sslmode=require&application_name=e2e",
		"huakai_e2e_new",
	)
	if err != nil {
		t.Fatalf("重写数据库 DSN: %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("解析重写后 DSN: %v", err)
	}
	if parsed.Path != "/huakai_e2e_new" || parsed.Query().Get("sslmode") != "require" ||
		parsed.Query().Get("application_name") != "e2e" || parsed.User.Username() != "user" {
		t.Fatalf("重写后 DSN 丢失连接信息: %s", got)
	}
}

func seedSpecializedLiveImportAuthorization(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID int64,
	name string,
) (string, int64) {
	t.Helper()
	bearer := "hk_admin_e2e_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	prefix := bearer
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(bearer), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("生成活体测试管理令牌哈希: %v", err)
	}
	var tokenID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO admin_tokens (name, key_hash, key_prefix, role, scope_tenant_id, status)
VALUES ($1,$2,$3,'tenant_operator',$4,'active')
RETURNING id`, name, string(hash), prefix, tenantID).Scan(&tokenID); err != nil {
		t.Fatalf("写入活体测试租户管理令牌: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO tenant_admin_capability_grants (
    tenant_id, capability, enabled, updated_by, reason, granted_at, revoked_at
) VALUES ($1,$2,true,$3,$4,clock_timestamp(),NULL)`,
		tenantID, specializedLiveImportCapability,
		fmt.Sprintf("admin_token:%d", tokenID), "账号转 API 活体端到端测试",
	); err != nil {
		t.Fatalf("写入活体测试账号导入能力授权: %v", err)
	}
	return bearer, tokenID
}

func executeSpecializedLiveAccountImport(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	addr, planPath, executePath, bearer string,
	request specializedLiveAccountImportRequest,
	secrets ...string,
) specializedLiveImportResult {
	t.Helper()
	planBody := postSpecializedLiveAccountImport(t, ctx, client, addr, planPath, bearer, request)
	defer privacy.Zeroize(planBody)
	var planned accountintake.PlanResult
	if err := json.Unmarshal(planBody, &planned); err != nil {
		t.Fatalf("解析活体账号导入预检响应: %v", err)
	}
	if planned.PlanHash == "" || planned.Plan.Summary.Create != 1 || len(planned.Plan.Items) != 1 {
		t.Fatalf("活体账号导入预检未形成唯一创建动作: create=%d items=%d",
			planned.Plan.Summary.Create, len(planned.Plan.Items))
	}
	request.PlanHash = planned.PlanHash
	request.Confirmations = append([]string(nil), planned.Plan.Items[0].RequiredConfirmations...)
	request.Reason = "账号转 API 活体端到端测试正式导入"

	executeBody := postSpecializedLiveAccountImport(t, ctx, client, addr, executePath, bearer, request)
	defer privacy.Zeroize(executeBody)
	var executed accountintake.ExecutionResult
	if err := json.Unmarshal(executeBody, &executed); err != nil {
		t.Fatalf("解析活体账号导入执行响应: %v", err)
	}
	if executed.Summary.Created != 1 || len(executed.Items) != 1 ||
		executed.Items[0].Status != accountintake.StatusCreated ||
		executed.Items[0].ProviderAccountID <= 0 {
		t.Fatalf("活体正式导入未原子创建账号与凭据: created=%d items=%d",
			executed.Summary.Created, len(executed.Items))
	}
	if !executed.Items[0].ChannelHealthInitialized {
		t.Fatal("活体正式导入未初始化渠道健康")
	}
	assertSpecializedLiveImportRedacted(t, planBody, executeBody, request.Content, secrets)
	return specializedLiveImportResult{
		AccountID: executed.Items[0].ProviderAccountID,
		Item:      executed.Items[0],
	}
}

func postSpecializedLiveAccountImport(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	addr, path, bearer string,
	payload specializedLiveAccountImportRequest,
) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("编码活体账号导入请求: %v", err)
	}
	defer privacy.Zeroize(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("构造活体账号导入请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("调用活体账号导入接口: %v", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		t.Fatalf("读取活体账号导入响应: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("活体账号导入接口 %s status=%d，响应正文因可能包含凭据已隐去", path, resp.StatusCode)
	}
	return responseBody
}

func assertSpecializedLiveImportRedacted(
	t *testing.T,
	planBody, executeBody []byte,
	content string,
	secrets []string,
) {
	t.Helper()
	for _, response := range [][]byte{planBody, executeBody} {
		if content != "" && bytes.Contains(response, []byte(content)) {
			t.Fatal("活体账号导入响应回显了完整凭据载荷")
		}
		for _, secret := range secrets {
			secret = strings.TrimSpace(secret)
			if secret != "" && bytes.Contains(response, []byte(secret)) {
				t.Fatal("活体账号导入响应回显了凭据字段")
			}
		}
	}
}

func startSpecializedLiveSidecar(t *testing.T, moduleRoot string, blockedEnvNames ...string) (*exec.Cmd, string) {
	t.Helper()
	repoRoot := filepath.Dir(moduleRoot)
	rustRoot := filepath.Join(repoRoot, "exploratory", "rust-core-gateway", "merged")
	targetRoot := strings.TrimSpace(os.Getenv("CARGO_TARGET_DIR"))
	if targetRoot == "" {
		targetRoot = filepath.Join(rustRoot, "target")
	} else if !filepath.IsAbs(targetRoot) {
		targetRoot = filepath.Join(rustRoot, targetRoot)
	}
	build := exec.Command("cargo", "build", "-p", "tls-sidecar")
	build.Dir = rustRoot
	build.Env = append(specializedLiveChildEnv(blockedEnvNames...), "CARGO_TARGET_DIR="+targetRoot)
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("构建活体测试 Rust TLS sidecar: %v", err)
	}

	runtimeRoot := specializedLiveRuntimeDir(t)
	if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
		t.Fatalf("创建活体测试运行目录: %v", err)
	}
	socketPath := filepath.Join(runtimeRoot, "huakai-specialized-live-"+uuid.NewString()+".sock")
	binaryPath := filepath.Join(targetRoot, "debug", "tls-sidecar")
	sidecar := exec.Command(binaryPath, socketPath)
	sidecar.Env = specializedLiveChildEnv(blockedEnvNames...)
	sidecar.Stdout = os.Stdout
	sidecar.Stderr = os.Stderr
	if err := sidecar.Start(); err != nil {
		t.Fatalf("启动活体测试 Rust TLS sidecar: %v", err)
	}
	for attempt := 0; attempt < 30; attempt++ {
		check := exec.Command(binaryPath, "--check", socketPath)
		check.Env = specializedLiveChildEnv(blockedEnvNames...)
		if err := check.Run(); err == nil {
			return sidecar, socketPath
		}
		time.Sleep(200 * time.Millisecond)
	}
	stopSpecializedLiveProcess(sidecar)
	_ = os.Remove(socketPath)
	t.Fatalf("Rust TLS sidecar 未在 6s 内就绪: %s", socketPath)
	return nil, ""
}

func specializedLiveRuntimeDir(t *testing.T) string {
	t.Helper()
	if configured := strings.TrimSpace(os.Getenv("HUAKAI_E2E_RUNTIME_DIR")); configured != "" {
		return configured
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheRoot) == "" {
		t.Fatalf("定位当前用户缓存目录: %v", err)
	}
	return filepath.Join(cacheRoot, "huakai-e2e")
}

func specializedLiveArtifactPath(t *testing.T, name string) string {
	t.Helper()
	root := specializedLiveRuntimeDir(t)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("创建活体测试产物目录: %v", err)
	}
	return filepath.Join(root, uuid.NewString()+"-"+name)
}

func specializedLiveChildEnv(blockedEnvNames ...string) []string {
	blocked := make(map[string]struct{}, len(blockedEnvNames))
	for _, name := range blockedEnvNames {
		if name = strings.TrimSpace(name); name != "" {
			blocked[name] = struct{}{}
		}
	}
	env := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		name, _, ok := strings.Cut(item, "=")
		if ok {
			if _, denied := blocked[name]; denied {
				continue
			}
		}
		env = append(env, item)
	}
	return env
}

func cleanupSpecializedLiveSubscriptionObservations(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID int64,
) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM provider_account_subscription_states WHERE tenant_id=$1`, tenantID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE provider_account_subscription_observations DISABLE TRIGGER provider_account_subscription_observations_append_only`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM provider_account_subscription_observations WHERE tenant_id=$1`, tenantID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE provider_account_subscription_observations ENABLE TRIGGER provider_account_subscription_observations_append_only`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func cleanupSpecializedLiveMoneyRows(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID int64,
) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, statement := range []string{
		`DELETE FROM quota_audit_events WHERE tenant_id=$1`,
		`DELETE FROM quota_concurrency_slots WHERE tenant_id=$1`,
		`DELETE FROM quota_concurrency_scope_locks WHERE tenant_id=$1`,
		`DELETE FROM quota_reconciliation_jobs WHERE tenant_id=$1`,
		`DELETE FROM quota_reservations WHERE tenant_id=$1`,
		`DELETE FROM quota_windows WHERE tenant_id=$1`,
		`DELETE FROM quota_policies WHERE tenant_id=$1`,
		`DELETE FROM idempotency_replay_records WHERE tenant_id=$1`,
		`ALTER TABLE user_cost_receipt_owners DISABLE TRIGGER enforce_user_cost_receipt_owners_append_only_delete`,
		`DELETE FROM user_cost_receipt_owners WHERE tenant_id=$1`,
		`ALTER TABLE user_cost_receipt_owners ENABLE TRIGGER enforce_user_cost_receipt_owners_append_only_delete`,
		`ALTER TABLE user_cost_receipts DISABLE TRIGGER enforce_user_cost_receipts_append_only_delete`,
		`DELETE FROM user_cost_receipts WHERE tenant_id=$1`,
		`ALTER TABLE user_cost_receipts ENABLE TRIGGER enforce_user_cost_receipts_append_only_delete`,
		`ALTER TABLE audit_ledger_entries DISABLE TRIGGER ledger_append_only_delete`,
		`DELETE FROM audit_ledger_entries WHERE tenant_id=$1`,
		`ALTER TABLE audit_ledger_entries ENABLE TRIGGER ledger_append_only_delete`,
		`DELETE FROM scheduler_outbox WHERE tenant_id=$1`,
		`DELETE FROM usage_record_dlq WHERE tenant_id=$1`,
		`ALTER TABLE usage_records DISABLE TRIGGER usage_records_append_only_delete`,
		`DELETE FROM usage_records WHERE tenant_id=$1`,
		`ALTER TABLE usage_records ENABLE TRIGGER usage_records_append_only_delete`,
		`ALTER TABLE billing_events DISABLE TRIGGER billing_events_append_only_delete`,
		`DELETE FROM billing_events WHERE tenant_id=$1`,
		`ALTER TABLE billing_events ENABLE TRIGGER billing_events_append_only_delete`,
		`DELETE FROM balance_holds WHERE tenant_id=$1`,
		`DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`,
		`DELETE FROM billing_ledger_claims WHERE tenant_id=$1`,
	} {
		var err error
		if strings.Contains(statement, "$1") {
			_, err = tx.Exec(ctx, statement, tenantID)
		} else {
			_, err = tx.Exec(ctx, statement)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func stopSpecializedLiveProcesses(processes *specializedLiveProcesses) {
	if processes == nil {
		return
	}
	stopSpecializedLiveProcess(processes.gateway)
	stopSpecializedLiveProcess(processes.sidecar)
	if processes.socketPath != "" {
		_ = os.Remove(processes.socketPath)
	}
}

func stopSpecializedLiveProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
}
