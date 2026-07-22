//go:build integration_pg

package hermesrecovery

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	rootdb "github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

func openRecoveryPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("HUAKAI_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("未设置 HUAKAI_TEST_DATABASE_URL 或 HUAKAI_DATABASE_URL")
	}
	pool, err := rootdb.Open(ctx, rootdb.PoolConfig{DSN: dsn, MaxConns: 6})
	if err != nil {
		t.Fatalf("连接 PostgreSQL：%v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func recoveryFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, func()) {
	t.Helper()
	var tenantID int64
	name := "hermes-recovery-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&tenantID); err != nil {
		t.Fatalf("创建恢复测试租户：%v", err)
	}
	return tenantID, func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM hermes_tool_calls WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM hermes_mutation_recovery WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}
}

func testRecoveryRecord(tenantID int64, operationID uuid.UUID) hermesops.MutationAuditRecord {
	return hermesops.MutationAuditRecord{
		OperationID:   operationID,
		TenantID:      tenantID,
		ActorSource:   "token",
		ActorID:       91,
		ActorRole:     hermesops.RolePlatformAdmin,
		ToolName:      hermesops.ToolDLQReplay,
		Args:          map[string]any{"id": int64(77), "credentials": "禁止落库的明文"},
		CorrelationID: "recovery-correlation-" + operationID.String(),
		RequestID:     "recovery-request-" + operationID.String(),
		CalledAt:      time.Now().UTC(),
		AdminAction:   "hermes.tool.dlq_replay",
		TargetType:    "dlq_event",
		TargetID:      77,
		AuditPayload:  map[string]any{"dlq_id": int64(77), "intended_action": "re_deliver"},
	}
}

func TestStoreFinalizesTwoLogsExactlyOnceRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openRecoveryPool(t, ctx)
	tenantID, cleanup := recoveryFixture(t, ctx, pool)
	t.Cleanup(cleanup)
	store := NewStore(pool)
	operationID := uuid.New()
	record := testRecoveryRecord(tenantID, operationID)
	if err := store.Prepare(ctx, record); err != nil {
		t.Fatalf("预登记：%v", err)
	}
	if err := store.RecordOutcome(ctx, operationID, hermesops.ResultOK,
		map[string]any{"status": "delivered"}, "", time.Now().UTC()); err != nil {
		t.Fatalf("记录结果：%v", err)
	}
	if err := store.FinalizeAudit(ctx, operationID); err != nil {
		t.Fatalf("提交日志：%v", err)
	}
	if err := store.FinalizeAudit(ctx, operationID); err != nil {
		t.Fatalf("重复提交日志应幂等：%v", err)
	}

	var toolRows, adminRows int
	var storedCredential string
	if err := pool.QueryRow(ctx, `
SELECT count(*), max(requested_args->>'credentials')
FROM hermes_tool_calls
WHERE operation_id=$1`, operationID).Scan(&toolRows, &storedCredential); err != nil {
		t.Fatalf("读取工具日志：%v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_events WHERE operation_id=$1`, operationID).Scan(&adminRows); err != nil {
		t.Fatalf("读取管理员日志：%v", err)
	}
	if toolRows != 1 || adminRows != 1 || storedCredential != "[REDACTED]" {
		t.Fatalf("日志数量或脱敏错误：tool=%d admin=%d credential=%q", toolRows, adminRows, storedCredential)
	}
}

func TestStoreAuditFailureRollsBackBothLogsAndCanRecoverRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openRecoveryPool(t, ctx)
	tenantID, cleanup := recoveryFixture(t, ctx, pool)
	t.Cleanup(cleanup)
	store := NewStore(pool)
	operationID := uuid.New()
	record := testRecoveryRecord(tenantID, operationID)
	record.AdminAction = "不存在的管理员动作"
	if err := store.Prepare(ctx, record); err != nil {
		t.Fatalf("预登记：%v", err)
	}
	if err := store.RecordOutcome(ctx, operationID, hermesops.ResultOK,
		map[string]any{"status": "delivered"}, "", time.Now().UTC()); err != nil {
		t.Fatalf("记录结果：%v", err)
	}
	if err := store.FinalizeAudit(ctx, operationID); err == nil {
		t.Fatal("非法管理员动作不应提交日志")
	}
	var toolRows, adminRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hermes_tool_calls WHERE operation_id=$1`, operationID).Scan(&toolRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_events WHERE operation_id=$1`, operationID).Scan(&adminRows); err != nil {
		t.Fatal(err)
	}
	if toolRows != 0 || adminRows != 0 {
		t.Fatalf("失败事务留下半套日志：tool=%d admin=%d", toolRows, adminRows)
	}
	if _, err := pool.Exec(ctx, `UPDATE hermes_mutation_recovery SET admin_action='hermes.tool.dlq_replay' WHERE operation_id=$1`, operationID); err != nil {
		t.Fatalf("修正测试记录：%v", err)
	}
	if err := store.FinalizeAudit(ctx, operationID); err != nil {
		t.Fatalf("恢复提交：%v", err)
	}
}

func TestWorkerRecoversPreparedMutationOnceAcrossReplicasRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openRecoveryPool(t, ctx)
	tenantID, cleanup := recoveryFixture(t, ctx, pool)
	t.Cleanup(cleanup)
	store := NewStore(pool)
	operationID := uuid.New()
	if err := store.Prepare(ctx, testRecoveryRecord(tenantID, operationID)); err != nil {
		t.Fatalf("预登记：%v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var replayCalls atomic.Int64
	replay := func(context.Context, int64, string) (*dlq.Record, error) {
		if replayCalls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return &dlq.Record{ID: 77, TenantID: tenantID, Status: dlq.StatusDelivered, ReplayAttempts: 1}, nil
	}
	workerOne, err := NewWorker(store, replay)
	if err != nil {
		t.Fatal(err)
	}
	workerTwo, err := NewWorker(store, replay)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, runErr := workerOne.RunOnce(ctx)
		firstDone <- runErr
	}()
	<-entered
	processed, secondErr := workerTwo.RunOnce(ctx)
	if secondErr != nil || processed {
		t.Fatalf("第二副本不应取得同一租约：processed=%v err=%v", processed, secondErr)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("第一副本恢复：%v", err)
	}
	if replayCalls.Load() != 1 {
		t.Fatalf("重放次数=%d，期望 1", replayCalls.Load())
	}
	var committed bool
	if err := pool.QueryRow(ctx, `SELECT audit_committed_at IS NOT NULL FROM hermes_mutation_recovery WHERE operation_id=$1`, operationID).Scan(&committed); err != nil {
		t.Fatal(err)
	}
	if !committed {
		t.Fatal("恢复后日志未标记完成")
	}
}

func TestWorkerFinalizesExistingOutcomeWithoutReplayingRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openRecoveryPool(t, ctx)
	tenantID, cleanup := recoveryFixture(t, ctx, pool)
	t.Cleanup(cleanup)
	store := NewStore(pool)
	operationID := uuid.New()
	if err := store.Prepare(ctx, testRecoveryRecord(tenantID, operationID)); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOutcome(ctx, operationID, hermesops.ResultError, nil, "mutation_failed", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(store, func(context.Context, int64, string) (*dlq.Record, error) {
		return nil, errors.New("结果已存在时不应再次重放")
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("只补日志失败：processed=%v err=%v", processed, err)
	}
	var status, errorClass string
	if err := pool.QueryRow(ctx, `SELECT result_status, error_class FROM hermes_tool_calls WHERE operation_id=$1`, operationID).Scan(&status, &errorClass); err != nil {
		t.Fatal(err)
	}
	if status != "error" || errorClass != "mutation_failed" {
		t.Fatalf("失败结果日志=%s/%s", status, errorClass)
	}
}

func TestWorkerReleasesTransientReplayFailureForRetryRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openRecoveryPool(t, ctx)
	tenantID, cleanup := recoveryFixture(t, ctx, pool)
	t.Cleanup(cleanup)
	store := NewStore(pool)
	operationID := uuid.New()
	if err := store.Prepare(ctx, testRecoveryRecord(tenantID, operationID)); err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(store, func(context.Context, int64, string) (*dlq.Record, error) {
		return nil, errors.New("临时数据库故障")
	})
	if err != nil {
		t.Fatal(err)
	}
	worker.retryAfter = time.Millisecond
	processed, runErr := worker.RunOnce(ctx)
	if !processed || runErr == nil {
		t.Fatalf("临时错误必须上抛并释放租约：processed=%v err=%v", processed, runErr)
	}
	var status string
	var committed bool
	var leaseOwner *string
	if err := pool.QueryRow(ctx, `
SELECT result_status, audit_committed_at IS NOT NULL, lease_owner
FROM hermes_mutation_recovery
WHERE operation_id=$1`, operationID).Scan(&status, &committed, &leaseOwner); err != nil {
		t.Fatal(err)
	}
	if status != "prepared" || committed || leaseOwner != nil {
		t.Fatalf("临时失败被错误终结：status=%s committed=%v lease=%v", status, committed, leaseOwner)
	}
}
