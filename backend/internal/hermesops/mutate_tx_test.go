package hermesops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// --- fake pgx tx + beginner -------------------------------------------------
// (伪 pgx 事务 + beginner)

type fakeMutateTx struct {
	rec *txRecorder
}

type txRecorder struct {
	lockAcquired   bool
	lockKey        string
	adminActorID   string // 捕获 admin_audit_events 写入的 actor_id,守其格式统一走 AuditActor()
	toolCallInsert int
	// toolCallTokenID 捕获 hermes_tool_calls 的 admin_actor_token_id 实参($3)。
	// 它是 *int64:token>0 时指向该 id、token==0 时为 nil(持久化为 SQL NULL)。
	// 守 insertToolCallRow 的 `if rec.AdminActorTokenID > 0` 分支——nil vs 非 nil 判别性。
	toolCallTokenID    *int64
	toolCallTokenIDSet bool // 是否见过一次 tool_calls insert(区分「未插入」与「插了个 nil」)
	adminInsert        int
	commitCount        int
	rollbackCount      int
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
	return pgconn.NewCommandTag("OK"), nil
}

func (tx *fakeMutateTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "INSERT INTO hermes_tool_calls"):
		tx.rec.toolCallInsert++
		// admin_actor_token_id 是第 3 个占位符($3)→ args 下标 2,类型 *int64。
		tx.rec.toolCallTokenIDSet = true
		if len(args) > 2 {
			tx.rec.toolCallTokenID, _ = args[2].(*int64)
		}
		if tx.rec.toolCallErr != nil {
			return errRow{err: tx.rec.toolCallErr}
		}
		return toolCallRow{}
	case strings.Contains(sql, "INSERT INTO admin_audit_events"):
		tx.rec.adminInsert++
		// InsertAdminAuditEvent 列序 (tenant_id, actor_id, ...) → actor_id 为第 2 个参数。
		if len(args) > 1 {
			tx.rec.adminActorID, _ = args[1].(string)
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
		TenantID: 7, ActorUserID: 42, AdminActorTokenID: 99,
		ToolName: ToolAccountPause, Status: ResultOK,
		AdminAction: "hermes.tool.account_pause", AdminRole: RoleTenantOperator,
		TargetType: "provider_account", TargetID: 5,
		AuditPayload: map[string]any{"account_id": int64(5)},
	}
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
	if rec.toolCallInsert != 1 || rec.adminInsert != 1 {
		t.Fatalf("audit inserts toolcall=%d admin=%d want 1/1", rec.toolCallInsert, rec.adminInsert)
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

func TestOrchestrator_ActorAttributionByTokenID(t *testing.T) {
	// 回归(actor 归属、有区分度、刚修的 S1 区):Hermes mutation 镜像进 admin_audit_events 时,
	// actor_id MUST(必须)走 admin.AdminIdentity.AuditActor()(admin_token:<id>);同一次 mutation
	// 在 hermes_tool_calls 里落 admin_actor_token_id FK 列:token>0 写具体 id、token==0(非 admin 模式)
	// 写 NULL(nil *int64)。两条腿一起锁,证明 AuditActor 归属格式与 FK NULL/非NULL 分支都不回退。
	//
	// 关键补充:token==0 这条腿此前完全无覆盖。若把 mutate_tx.go 里
	//   actorID := admin.AdminIdentity{TokenID: rec.AdminActorTokenID, ...}.AuditActor()
	// 退回裸 fmt.Sprintf("%d", rec.AdminActorTokenID),token=99 那腿仍得 "99"≠"admin_token:99" 会红,
	// 但真正的语义漂移(token=0 该不该带 admin_token: 前缀、FK 列该不该是 NULL)只有 token==0 的用例能锁死。
	cases := []struct {
		name          string
		tokenID       int64
		wantActorID   string // admin_audit_events.actor_id 期望值(AuditActor 统一格式)
		wantTokenNull bool   // hermes_tool_calls.admin_actor_token_id 是否应为 NULL
	}{
		// token>0:admin token 模式。actor_id 带 admin_token:<id>,FK 列写具体 id。
		{name: "admin_token_present", tokenID: 99, wantActorID: "admin_token:99", wantTokenNull: false},
		// token==0:非 admin 模式(未接 admin token 通道)。走 AuditActor→admin_token:0(与其它 handler
		// 同格式,不因 token 缺失被分裂成裸 "0" 归属);FK 列 admin_actor_token_id 写 NULL——绝不能把 0
		// 当成一个真实存在的 admin_tokens.id(那会撞 FK 或错误归因到 id=0 的 token)。
		{name: "non_admin_actor_zero", tokenID: 0, wantActorID: "admin_token:0", wantTokenNull: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &txRecorder{}
			o := NewMutateOrchestrator(&fakeBeginner{rec: rec})
			audit := baseRecord()
			audit.AdminActorTokenID = tc.tokenID
			_, err := o.Execute(context.Background(), "lock:actor", audit, func(context.Context, pgx.Tx) (ToolResult, error) {
				return ToolResult{Summary: map[string]any{"enabled": false}}, nil
			})
			if err != nil {
				t.Fatalf("execute err=%v want nil", err)
			}
			// (a) admin_audit_events 镜像 actor_id 走 AuditActor 统一格式。
			if rec.adminActorID != tc.wantActorID {
				t.Fatalf("admin_audit actor_id=%q want %q(须走 AuditActor 统一格式,token=%d)", rec.adminActorID, tc.wantActorID, tc.tokenID)
			}
			// (b) hermes_tool_calls.admin_actor_token_id FK 列:token>0 非 nil 且等于 id;token==0 为 nil(NULL)。
			if !rec.toolCallTokenIDSet {
				t.Fatalf("tool_calls insert 从未发生(无法断言 admin_actor_token_id 分支)")
			}
			if tc.wantTokenNull {
				if rec.toolCallTokenID != nil {
					t.Fatalf("admin_actor_token_id=%v want NULL(nil)对 token=0——绝不能把 0 当真实 FK", *rec.toolCallTokenID)
				}
			} else {
				if rec.toolCallTokenID == nil {
					t.Fatalf("admin_actor_token_id=NULL want %d——token>0 必须落具体 FK id", tc.tokenID)
				}
				if *rec.toolCallTokenID != tc.tokenID {
					t.Fatalf("admin_actor_token_id=%d want %d", *rec.toolCallTokenID, tc.tokenID)
				}
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

func TestOrchestrator_CommitFailureAfterOwnTxMutationIsCommitUncertain(t *testing.T) {
	// 回归(H4 S2、有区分度):mutation 成功(mErr=nil),但 FINAL(最终)的 orchestrator 提交失败。
	// 对一个 OWN-TX 工具(dlq_replay/renew_trigger),mutation 已在它自己的 tx 中提交,所以返回的
	// error MUST(必须)包裹 ErrCommitAfterOwnTxMutation(-> commit_uncertain)。对一个 IN-TX 工具
	// (account_pause/resume),同样的故障会把 mutation 原子地回滚,所以它必须 NOT(不)携带该哨兵
	// (-> mutation_failed)。
	//
	// 变异检查(自证):本测试对 OwnTx=true 与 OwnTx=false 运行 EXACT(完全)相同的强制提交故障,
	// 并断言哨兵的存在性 DIFFERS(不同)。如果 Execute 忽略 rec.OwnTx 而无条件地包裹(或不包裹)该哨兵,
	// own 与 in-tx 两条腿就会一致,`ownWrapped == inWrapped` 的断言就会变红。
	run := func(ownTx bool) error {
		rec := &txRecorder{commitErr: errors.New("connection reset by peer")}
		o := NewMutateOrchestrator(&fakeBeginner{rec: rec})
		audit := baseRecord()
		audit.OwnTx = ownTx
		mutated := 0
		_, err := o.Execute(context.Background(), "lock:commit", audit, func(context.Context, pgx.Tx) (ToolResult, error) {
			mutated++
			return ToolResult{Summary: map[string]any{"ok": true}}, nil
		})
		if err == nil {
			t.Fatalf("ownTx=%v: execute err=nil want commit failure", ownTx)
		}
		if mutated != 1 {
			t.Fatalf("ownTx=%v: mutate ran %d times want 1 (mutation runs before the failing commit)", ownTx, mutated)
		}
		if rec.commitCount != 1 || rec.rollbackCount != 1 {
			// commit 被尝试一次(失败),随后 defer 回滚。
			t.Fatalf("ownTx=%v: commit=%d rollback=%d want 1/1", ownTx, rec.commitCount, rec.rollbackCount)
		}
		return err
	}

	ownErr := run(true)
	inErr := run(false)

	ownWrapped := errors.Is(ownErr, ErrCommitAfterOwnTxMutation)
	inWrapped := errors.Is(inErr, ErrCommitAfterOwnTxMutation)

	if !ownWrapped {
		t.Fatalf("own-tx commit fault did NOT wrap ErrCommitAfterOwnTxMutation: %v", ownErr)
	}
	if inWrapped {
		t.Fatalf("in-tx commit fault WRONGLY wrapped ErrCommitAfterOwnTxMutation (in-tx mutation rolled back, must stay mutation_failed): %v", inErr)
	}
	if ownWrapped == inWrapped {
		t.Fatalf("tx-mode did not change the classification (own=%v in=%v) — rec.OwnTx is not threaded into the commit-failure path", ownWrapped, inWrapped)
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
