package hermesconfirm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresStore跨副本只允许一次确认且不落原文(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("HUAKAI_TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("HUAKAI_DATABASE_URL"))
	}
	if dsn == "" {
		t.Skip("未设置 HUAKAI_TEST_DATABASE_URL 或 HUAKAI_DATABASE_URL")
	}
	ctx := context.Background()
	poolA, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接副本 A: %v", err)
	}
	defer poolA.Close()
	poolB, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接副本 B: %v", err)
	}
	defer poolB.Close()

	var tenantID int64
	if err := poolA.QueryRow(ctx, `SELECT id FROM tenants WHERE id > 0 ORDER BY id LIMIT 1`).Scan(&tenantID); err != nil {
		t.Fatalf("读取测试租户: %v", err)
	}
	const actorID = int64(9_150_221)
	_, _ = poolA.Exec(ctx, `DELETE FROM hermes_pending_confirmations WHERE actor_id = $1`, actorID)
	t.Cleanup(func() {
		_, _ = poolA.Exec(context.Background(), `DELETE FROM hermes_pending_confirmations WHERE actor_id = $1`, actorID)
	})

	storeA := NewPostgresStore(poolA)
	storeB := NewPostgresStore(poolB)
	pending := PendingConfirmation{
		ToolName: "account_pause", TenantID: tenantID, ActorSource: "token",
		ActorID: actorID, TargetID: 771,
	}
	pending.ArgsDigest, _ = DigestArguments(map[string]any{"account_id": 771})
	pending.PlanDigest, _ = DigestPlan("provider_account", 771, "lock:771", map[string]any{"state": "active"})
	id, err := storeA.Issue(ctx, pending)
	if err != nil {
		t.Fatalf("副本 A 签发确认: %v", err)
	}
	var storedHash []byte
	var storedArgsBinding []byte
	var storedPlanBinding []byte
	if err := poolB.QueryRow(ctx, `
SELECT token_hash, args_binding_hash, plan_binding_hash
FROM hermes_pending_confirmations
WHERE actor_id = $1 AND target_id = $2
`, actorID, pending.TargetID).Scan(&storedHash, &storedArgsBinding, &storedPlanBinding); err != nil {
		t.Fatalf("副本 B 读取确认哈希: %v", err)
	}
	wantHash := sha256.Sum256([]byte(id))
	if !bytes.Equal(storedHash, wantHash[:]) {
		t.Fatalf("数据库未保存确认值的 SHA-256 哈希")
	}
	if bytes.Contains(storedHash, []byte(id)) {
		t.Fatal("数据库保存了确认原文")
	}
	wantArgsBinding := confirmationBindingDigest(id, "args", pending.ArgsDigest)
	wantPlanBinding := confirmationBindingDigest(id, "plan", pending.PlanDigest)
	if !bytes.Equal(storedArgsBinding, wantArgsBinding[:]) || !bytes.Equal(storedPlanBinding, wantPlanBinding[:]) {
		t.Fatal("数据库没有保存确认值盐化后的参数与计划绑定")
	}
	if bytes.Equal(storedArgsBinding, pending.ArgsDigest[:]) || bytes.Equal(storedPlanBinding, pending.PlanDigest[:]) {
		t.Fatal("数据库保存了未盐化的参数或计划摘要")
	}

	type outcome struct {
		status ConsumeStatus
		err    error
	}
	results := make(chan outcome, 32)
	var wg sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			store := storeA
			if index%2 == 1 {
				store = storeB
			}
			_, status, consumeErr := store.ConsumeWithStatus(ctx, id, pending)
			results <- outcome{status: status, err: consumeErr}
		}(i)
	}
	wg.Wait()
	close(results)
	okCount := 0
	missingCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("并发消费失败: %v", result.err)
		}
		switch result.status {
		case ConsumeOK:
			okCount++
		case ConsumeMissing:
			missingCount++
		default:
			t.Fatalf("并发消费得到意外状态 %s", result.status)
		}
	}
	if okCount != 1 || missingCount != 31 {
		t.Fatalf("跨副本消费结果 ok=%d missing=%d，期望 1/31", okCount, missingCount)
	}

	mismatchPending := testPending("account_pause", tenantID, "session", actorID, 772, "active")
	mismatchID, err := storeA.Issue(ctx, mismatchPending)
	if err != nil {
		t.Fatalf("签发绑定测试确认: %v", err)
	}
	wrongSource := mismatchPending
	wrongSource.ActorSource = "token"
	if _, status, err := storeB.ConsumeWithStatus(ctx, mismatchID, wrongSource); err != nil || status != ConsumeMismatch {
		t.Fatalf("错误管理员来源状态=%s err=%v，期望 mismatch", status, err)
	}
	if _, status, err := storeA.ConsumeWithStatus(ctx, mismatchID, mismatchPending); err != nil || status != ConsumeMissing {
		t.Fatalf("绑定冲突后仍可复用: status=%s err=%v", status, err)
	}
}

func TestPostgresStore过期确认被原子销毁(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("HUAKAI_TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("HUAKAI_DATABASE_URL"))
	}
	if dsn == "" {
		t.Skip("未设置 HUAKAI_TEST_DATABASE_URL 或 HUAKAI_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接数据库: %v", err)
	}
	defer pool.Close()
	var tenantID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM tenants WHERE id > 0 ORDER BY id LIMIT 1`).Scan(&tenantID); err != nil {
		t.Fatalf("读取测试租户: %v", err)
	}
	const (
		id      = "hmc_00000000000000000000000000000000"
		actorID = int64(9_150_222)
	)
	digest := confirmationDigest(id)
	pending := testPending("account_pause", tenantID, "token", actorID, 773, "active")
	argsBinding := confirmationBindingDigest(id, "args", pending.ArgsDigest)
	planBinding := confirmationBindingDigest(id, "plan", pending.PlanDigest)
	_, err = pool.Exec(ctx, `
INSERT INTO hermes_pending_confirmations (
    token_hash, tool_name, tenant_id, actor_source, actor_id, target_id,
    args_binding_hash, plan_binding_hash, created_at, expires_at
) VALUES ($1, 'account_pause', $2, 'token', $3, 773, $4, $5, $6, $7)
ON CONFLICT (token_hash) DO UPDATE
SET actor_id = EXCLUDED.actor_id,
    args_binding_hash = EXCLUDED.args_binding_hash,
    plan_binding_hash = EXCLUDED.plan_binding_hash,
    created_at = EXCLUDED.created_at,
    expires_at = EXCLUDED.expires_at
`, digest[:], tenantID, actorID, argsBinding[:], planBinding[:], time.Now().Add(-10*time.Minute), time.Now().Add(-5*time.Minute))
	if err != nil {
		t.Fatalf("写入过期确认: %v", err)
	}
	store := NewPostgresStore(pool)
	if _, status, err := store.ConsumeWithStatus(ctx, id, pending); err != nil || status != ConsumeExpired {
		t.Fatalf("过期消费 status=%s err=%v，期望 expired", status, err)
	}
	if _, status, err := store.ConsumeWithStatus(ctx, id, pending); err != nil || status != ConsumeMissing {
		t.Fatalf("过期确认没有被销毁: status=%s err=%v", status, err)
	}
}
