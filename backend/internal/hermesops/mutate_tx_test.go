package hermesops

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// --- fake pgx tx + beginner -------------------------------------------------
// (伪 pgx 事务 + beginner)

type fakeMutateTx struct {
	rec *txRecorder
}

type txRecorder struct {
	lockAcquired    bool
	lockKey         string
	adminActorID    string // 捕获 admin_audit_events 写入的 actor_id,守其格式统一走 AuditActor()
	toolCallInsert  int
	toolActorSource string
	toolActorID     int64
	toolActorRole   string
	adminInsert     int
	outcomeUpdate   int
	commitCount     int
	rollbackCount   int
	// rollbackLiveCtx 计数那些以 NON-cancelled(未被取消)context 调用的回滚。orchestrator
	// 必须在一个 INDEPENDENT(独立)ctx 上回滚,而非那个已死的截止 ctx——否则回滚本身就会被取消,
	// 连接池连接 + advisory lock 就会泄漏。一次以已取消 ctx 看到的回滚,意味着那份独立性丢失了。
	rollbackLiveCtx int
	// 注入的失败
	toolCallErr error
	adminErr    error
	commitErr   error
}

type fakeBeginner struct {
	rec        *txRecorder
	beginCount int
}

func (b *fakeBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	b.beginCount++
	return &fakeMutateTx{rec: b.rec}, nil
}

func (tx *fakeMutateTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "pg_advisory_xact_lock") {
		tx.rec.lockAcquired = true
		if len(args) > 0 {
			tx.rec.lockKey, _ = args[0].(string)
		}
		return pgconn.NewCommandTag("SELECT 1"), nil
	}
	if strings.Contains(sql, "UPDATE hermes_tool_calls") {
		tx.rec.outcomeUpdate++
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return pgconn.NewCommandTag("OK"), nil
}

func (tx *fakeMutateTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "INSERT INTO hermes_tool_calls"):
		tx.rec.toolCallInsert++
		if len(args) > 4 {
			tx.rec.toolActorSource, _ = args[2].(string)
			tx.rec.toolActorID, _ = args[3].(int64)
			tx.rec.toolActorRole, _ = args[4].(string)
		}
		if tx.rec.toolCallErr != nil {
			return errRow{err: tx.rec.toolCallErr}
		}
		return toolCallRow{}
	case strings.Contains(sql, "INSERT INTO admin_audit_events"):
		tx.rec.adminInsert++
		// InsertAdminAuditEvent 列序为 operation_id、tenant_id、actor_id，actor_id 是第 3 个参数。
		if len(args) > 2 {
			tx.rec.adminActorID, _ = args[2].(string)
		}
		if tx.rec.adminErr != nil {
			return errRow{err: tx.rec.adminErr}
		}
		return adminAuditRow{}
	default:
		return errRow{err: errors.New("unexpected SQL in fake mutate tx: " + sql)}
	}
}

func (tx *fakeMutateTx) Commit(context.Context) error {
	tx.rec.commitCount++
	return tx.rec.commitErr
}
func (tx *fakeMutateTx) Rollback(ctx context.Context) error {
	tx.rec.rollbackCount++
	if ctx != nil && ctx.Err() == nil {
		tx.rec.rollbackLiveCtx++
	}
	return nil
}
func (tx *fakeMutateTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("nested tx unused")
}
func (tx *fakeMutateTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (tx *fakeMutateTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (tx *fakeMutateTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (tx *fakeMutateTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (tx *fakeMutateTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("query unused")
}
func (tx *fakeMutateTx) Conn() *pgx.Conn { return nil }

// toolCallRow 扫描 InsertHermesToolCall 的 RETURNING id, called_at。
type toolCallRow struct{}

func (toolCallRow) Scan(dest ...any) error {
	if len(dest) > 0 {
		if p, ok := dest[0].(*int64); ok {
			*p = 1
		}
	}
	return nil
}

// adminAuditRow 扫描 InsertAdminAuditEvent 的 RETURNING id, occurred_at。
type adminAuditRow struct{}

func (adminAuditRow) Scan(dest ...any) error {
	if len(dest) > 0 {
		if p, ok := dest[0].(*int64); ok {
			*p = 1
		}
	}
	return nil
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// --- tests ------------------------------------------------------------------
// (测试)

func baseRecord() MutationAuditRecord {
	return MutationAuditRecord{
		OperationID: uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		TenantID:    7, ActorSource: "token", ActorID: 99, ActorRole: RoleTenantOperator,
		ToolName: ToolAccountPause, Status: ResultOK,
		AdminAction: "hermes.tool.account_pause",
		TargetType:  "provider_account", TargetID: 5,
		AuditPayload: map[string]any{"account_id": int64(5)},
	}
}

type fakeMutationRecovery struct {
	prepareCount  int
	outcomeCount  int
	finalizeCount int
	prepareErr    error
	outcomeErr    error
	finalizeErr   error
	status        ResultStatus
	errorClass    string
	summary       map[string]any
}

func (f *fakeMutationRecovery) Prepare(context.Context, MutationAuditRecord) error {
	f.prepareCount++
	return f.prepareErr
}

func (f *fakeMutationRecovery) RecordOutcome(_ context.Context, _ uuid.UUID, status ResultStatus, summary map[string]any, errorClass string, _ time.Time) error {
	f.outcomeCount++
	f.status = status
	f.summary = summary
	f.errorClass = errorClass
	return f.outcomeErr
}

func (f *fakeMutationRecovery) FinalizeAudit(context.Context, uuid.UUID) error {
	f.finalizeCount++
	return f.finalizeErr
}

func TestOrchestrator_CommitsMutationWithAuditAndLock(t *testing.T) {
	// 回归(L3+L4):一个已确认的 mutation 在获取 advisory lock 之后,把 mutation + tool_calls 行 +
	// admin_audit 行一并提交。变异检查:去掉 pg_advisory_xact_lock 的 Exec 会让 lockAcquired=false;
	// 去掉任一审计 insert 会让其计数器降到 1 以下。
	rec := &txRecorder{}
	o := NewMutateOrchestrator(&fakeBeginner{rec: rec})
	mutated := 0
	_, err := o.Execute(context.Background(), "hermes:account_toggle:7:5", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
		mutated++
		return ToolResult{Summary: map[string]any{"enabled": false}}, nil
	})
	if err != nil {
		t.Fatalf("execute err=%v want nil", err)
	}
	if !rec.lockAcquired {
		t.Fatalf("advisory lock not acquired")
	}
	if rec.lockKey != "hermes:account_toggle:7:5" {
		t.Fatalf("lock key=%q want the per-target key", rec.lockKey)
	}
	if rec.toolCallInsert != 1 || rec.adminInsert != 1 || rec.outcomeUpdate != 1 {
		t.Fatalf("日志写入 toolcall=%d admin=%d outcome=%d，期望 1/1/1", rec.toolCallInsert, rec.adminInsert, rec.outcomeUpdate)
	}
	// admin_audit_events.actor_id 必须与其它 handler 同格式(AuditActor()=admin_token:<id>),
	// 否则同一 operator 在同表被分裂成两种归属串、按新格式检索漏掉 Hermes mutation 行。
	// 变异:把 mutate_tx.go 的 actorID 退回裸 fmt.Sprintf("%d", ...) → 得 "99" → RED。
	if rec.adminActorID != "admin_token:99" {
		t.Fatalf("admin_audit actor_id=%q want admin_token:99(须走 AuditActor 统一格式)", rec.adminActorID)
	}
	if mutated != 1 {
		t.Fatalf("mutate ran %d times want 1", mutated)
	}
	if rec.commitCount != 1 || rec.rollbackCount != 0 {
		t.Fatalf("commit=%d rollback=%d want 1/0", rec.commitCount, rec.rollbackCount)
	}
}

func TestOrchestrator_ActorAttributionBySource(t *testing.T) {
	cases := []struct {
		name        string
		source      string
		actorID     int64
		wantAuditID string
	}{
		{name: "令牌管理员", source: "token", actorID: 99, wantAuditID: "admin_token:99"},
		{name: "会话管理员", source: "session", actorID: 88, wantAuditID: "admin_user:88"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &txRecorder{}
			o := NewMutateOrchestrator(&fakeBeginner{rec: rec})
			audit := baseRecord()
			audit.ActorSource = tc.source
			audit.ActorID = tc.actorID
			_, err := o.Execute(context.Background(), "lock:actor", audit, func(context.Context, pgx.Tx) (ToolResult, error) {
				return ToolResult{Summary: map[string]any{"enabled": false}}, nil
			})
			if err != nil {
				t.Fatalf("execute err=%v want nil", err)
			}
			if rec.adminActorID != tc.wantAuditID {
				t.Fatalf("管理日志 actor_id=%q，期望 %q", rec.adminActorID, tc.wantAuditID)
			}
			if rec.toolActorSource != tc.source || rec.toolActorID != tc.actorID || rec.toolActorRole != RoleTenantOperator {
				t.Fatalf("工具日志管理员归属错误: %s:%d/%s", rec.toolActorSource, rec.toolActorID, rec.toolActorRole)
			}
		})
	}
}

func TestOrchestrator_AuditFailureAbortsMutation(t *testing.T) {
	// 回归(L3 / P1 fail-closed、有区分度):若 admin_audit insert 失败,mutation MUST NOT(绝不能)
	// 运行,且 tx 回滚——账号保持启用。换成尽力而为(非原子)的路径,mutate 回调就会运行,账号会在
	// 没有审计行的情况下被暂停。变异检查:把 mutate() 调用挪到审计 insert BEFORE(之前),`mutated`
	// 就变成 1(变红)。
	rec := &txRecorder{adminErr: errors.New("audit check violation")}
	o := NewMutateOrchestrator(&fakeBeginner{rec: rec})
	mutated := 0
	_, err := o.Execute(context.Background(), "lock:1", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
		mutated++
		return ToolResult{}, nil
	})
	if err == nil {
		t.Fatalf("execute err=nil want audit failure")
	}
	if mutated != 0 {
		t.Fatalf("mutation RAN despite audit failure (mutated=%d) — account would be paused with no audit row", mutated)
	}
	if rec.commitCount != 0 || rec.rollbackCount != 1 {
		t.Fatalf("commit=%d rollback=%d want 0/1 (no commit on audit failure)", rec.commitCount, rec.rollbackCount)
	}
}

func TestOrchestrator_ToolCallAuditFailureAbortsMutation(t *testing.T) {
	// 回归(L3):一次 tool_calls 审计 insert 失败同样会在 mutation 之前中止(tool-call 流水是
	// 权威轨迹)。变异检查:调换 insert 次序,让 mutation 先于 tool_calls insert,`mutated` 就变成 1。
	rec := &txRecorder{toolCallErr: errors.New("tool_call insert failed")}
	o := NewMutateOrchestrator(&fakeBeginner{rec: rec})
	mutated := 0
	_, err := o.Execute(context.Background(), "lock:2", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
		mutated++
		return ToolResult{}, nil
	})
	if err == nil || mutated != 0 {
		t.Fatalf("err=%v mutated=%d want non-nil err + mutated=0", err, mutated)
	}
	if rec.adminInsert != 0 {
		t.Fatalf("admin audit ran after tool_call failure (admin=%d) — inserts must short-circuit", rec.adminInsert)
	}
	if rec.rollbackCount != 1 {
		t.Fatalf("rollback=%d want 1", rec.rollbackCount)
	}
}

func TestOrchestrator_MutationFailureRollsBack(t *testing.T) {
	// 回归:若 mutation 本身在审计行已就绪之后失败,整个 tx 回滚,这样就不会有孤儿审计行为一个未发生
	// 的 mutation 持久化下来。变异检查:把 defer 改成 commit-on-error,commitCount 的断言就会失败。
	rec := &txRecorder{}
	o := NewMutateOrchestrator(&fakeBeginner{rec: rec})
	_, err := o.Execute(context.Background(), "lock:3", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
		return ToolResult{}, errors.New("mutation boom")
	})
	if err == nil {
		t.Fatalf("err=nil want mutation failure")
	}
	if rec.commitCount != 0 || rec.rollbackCount != 1 {
		t.Fatalf("commit=%d rollback=%d want 0/1 (orphan audit must roll back)", rec.commitCount, rec.rollbackCount)
	}
}

func TestOrchestrator_RecoverableMutationUsesJournalWithoutOuterTransaction(t *testing.T) {
	recorder := &txRecorder{}
	beginner := &fakeBeginner{rec: recorder}
	recovery := &fakeMutationRecovery{}
	orchestrator := NewMutateOrchestrator(beginner, WithMutationRecoveryJournal(recovery))
	record := baseRecord()
	record.ToolName = ToolDLQReplay
	mutated := 0
	result, err := orchestrator.Execute(context.Background(), "unused", record, func(_ context.Context, tx pgx.Tx) (ToolResult, error) {
		mutated++
		if tx != nil {
			t.Fatal("独立事务工具不应收到外层事务")
		}
		return ToolResult{Summary: map[string]any{"status": "delivered"}}, nil
	})
	if err != nil {
		t.Fatalf("执行独立事务工具：%v", err)
	}
	if mutated != 1 || beginner.beginCount != 0 {
		t.Fatalf("变更次数=%d 外层事务=%d，期望 1/0", mutated, beginner.beginCount)
	}
	if recovery.prepareCount != 1 || recovery.outcomeCount != 1 || recovery.finalizeCount != 1 {
		t.Fatalf("恢复阶段次数=%d/%d/%d，期望 1/1/1", recovery.prepareCount, recovery.outcomeCount, recovery.finalizeCount)
	}
	if recovery.status != ResultOK || result.Summary["status"] != "delivered" {
		t.Fatalf("结果未持久化：status=%s result=%+v", recovery.status, result)
	}
}

func TestOrchestrator_RecoverablePrepareFailureStopsMutation(t *testing.T) {
	recovery := &fakeMutationRecovery{prepareErr: errors.New("数据库不可用")}
	orchestrator := NewMutateOrchestrator(&fakeBeginner{rec: &txRecorder{}}, WithMutationRecoveryJournal(recovery))
	record := baseRecord()
	record.ToolName = ToolDLQReplay
	ran := false
	_, err := orchestrator.Execute(context.Background(), "unused", record, func(context.Context, pgx.Tx) (ToolResult, error) {
		ran = true
		return ToolResult{}, nil
	})
	if err == nil || ran {
		t.Fatalf("预登记失败后 err=%v ran=%v，期望失败且不执行", err, ran)
	}
}

func TestOrchestrator_RecoverableAuditFailureEntersDurableRecovery(t *testing.T) {
	recovery := &fakeMutationRecovery{finalizeErr: errors.New("提交日志失败")}
	orchestrator := NewMutateOrchestrator(&fakeBeginner{rec: &txRecorder{}}, WithMutationRecoveryJournal(recovery))
	record := baseRecord()
	record.ToolName = ToolDLQReplay
	_, err := orchestrator.Execute(context.Background(), "unused", record, func(context.Context, pgx.Tx) (ToolResult, error) {
		return ToolResult{Summary: map[string]any{"status": "delivered"}}, nil
	})
	if !errors.Is(err, ErrMutationRecoveryPending) {
		t.Fatalf("日志提交失败=%v，期望 ErrMutationRecoveryPending", err)
	}
	if recovery.outcomeCount != 1 || recovery.finalizeCount != 1 {
		t.Fatalf("结果/日志阶段=%d/%d，期望 1/1", recovery.outcomeCount, recovery.finalizeCount)
	}
}

func TestOrchestrator_RecoverableMutationFailureIsAuditedOnce(t *testing.T) {
	recovery := &fakeMutationRecovery{}
	orchestrator := NewMutateOrchestrator(&fakeBeginner{rec: &txRecorder{}}, WithMutationRecoveryJournal(recovery))
	record := baseRecord()
	record.ToolName = ToolDLQReplay
	mutationErr := errors.New("重放失败")
	_, err := orchestrator.Execute(context.Background(), "unused", record, func(context.Context, pgx.Tx) (ToolResult, error) {
		return ToolResult{}, mutationErr
	})
	if !errors.Is(err, ErrMutationOutcomeAudited) || !errors.Is(err, mutationErr) {
		t.Fatalf("失败=%v，期望保留原错误并标记已记日志", err)
	}
	if recovery.status != ResultError || recovery.errorClass != "mutation_failed" || recovery.finalizeCount != 1 {
		t.Fatalf("失败结果未完整持久化：status=%s class=%s finalize=%d", recovery.status, recovery.errorClass, recovery.finalizeCount)
	}
}

func TestOrchestrator_NilBeginnerFailsClosed(t *testing.T) {
	// 回归:没有原子审计事务,mutating 工具绝不能继续。nil 的 beginner 返回一个 error,且永不运行
	// mutation。
	o := NewMutateOrchestrator(nil)
	ran := false
	_, err := o.Execute(context.Background(), "lock", baseRecord(), func(context.Context, pgx.Tx) (ToolResult, error) {
		ran = true
		return ToolResult{}, nil
	})
	if err == nil || ran {
		t.Fatalf("nil beginner: err=%v ran=%v want error + no mutation", err, ran)
	}
}
