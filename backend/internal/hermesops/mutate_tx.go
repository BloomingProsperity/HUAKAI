package hermesops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops/mutateguard"
)

// txBeginner 是 mutating orchestrator 开启自身事务所需的、窄的连接池表面。*pgxpool.Pool
// 满足它;该接口让 orchestrator 可用一个伪 begin/commit/rollback recorder 做单元测试。
type txBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// MutationAuditRecord 是一次已确认 mutation 的原子审计足迹:hermes_tool_calls 行 AND
// (以及)admin_audit_events 镜像,二者写在 orchestrator 事务内部,因此谁都不能在缺少另一方
// (以及按下面的次序,缺少 mutation)的情况下存在。
type MutationAuditRecord struct {
	// tool-call 流水字段(hermes_tool_calls)。
	OperationID   uuid.UUID
	TenantID      int64
	ActorSource   string
	ActorID       int64
	ActorRole     string
	ToolName      string
	Args          map[string]any
	ResultSummary map[string]any
	Status        ResultStatus
	ErrorClass    string
	CorrelationID string
	RequestID     string
	CalledAt      time.Time
	ReturnedAt    time.Time
	DryRun        bool

	// admin_audit_events 镜像字段。
	AdminAction string // 例如 hermes.tool.account_pause
	TargetType  string
	TargetID    int64
	// AuditPayload 是已脱敏的 previous->next 状态载荷。作为纵深防御,insert 前会再脱敏一次。
	AuditPayload map[string]any
}

// MutationRecoveryJournal 是独立事务工具的持久恢复合同。Prepare 必须先于真实变更成功；
// RecordOutcome 在变更返回后保存最终结果；FinalizeAudit 原子补齐工具日志、管理员日志和完成标记。
type MutationRecoveryJournal interface {
	Prepare(context.Context, MutationAuditRecord) error
	RecordOutcome(context.Context, uuid.UUID, ResultStatus, map[string]any, string, time.Time) error
	FinalizeAudit(context.Context, uuid.UUID) error
}

// MutateOrchestrator 在 L3(atomic audit)+ L4(advisory lock)下运行一个已确认的 mutating
// 工具。它拥有一个单一事务,该事务:
//
//  1. 获取一个每目标的 pg advisory xact lock(跨运营者/副本串行化对 SAME(同一)目标的并发
//     mutation),
//  2. 插入 hermes_tool_calls 行 + admin_audit_events 镜像,并 VERIFIES(校验)两次插入都成功,
//  3. 只在那之后 THEN(才)执行 mutation,
//  4. 当且仅当 mutation 成功时提交;否则回滚。
//
// 因为审计插入在 mutation 运行 BEFORE(之前)就被校验,损坏的审计路径会以目标 UNCHANGED(不变)
// 的方式中止请求(fail-closed)——没有一条持久审计行先被数据库接受,mutation 就永远无法被施加。
// 残余窗口(mutation 成功,随后最终的 COMMIT 因传输故障失败)作为已知风险被记录在案;此时审计行
// 已被数据库接受,所以这是一次连接故障,而非静默的审计缺失。
type MutateOrchestrator struct {
	begin    txBeginner
	recovery MutationRecoveryJournal
	// sem 把进程级 mutating 并发上限压到连接池 MaxConns 之下(BELOW),这样一阵 mutation 风暴
	// 就无法耗尽共享的 pgxpool / advisory-lock 槽位、把核心网关拖垮。nil 的 sem(或被禁用的)
	// 即旧的无上限行为。
	sem *mutateguard.Semaphore
	// txDeadline 给单个 mutation 事务设界(客户端 ctx 截止 + 服务端 SET LOCAL statement_timeout)。
	// 零是禁用哨兵:无截止、无 statement_timeout——逐字节的旧行为。
	txDeadline time.Duration
	// acquireWait 设定 Execute 在返回 ErrMutateBusy 之前等待一个并发槽位的时长上界。零则只退回到
	// 父 ctx 的截止。
	acquireWait time.Duration
}

// MutateOption 以可叠加(additive)的方式配置 orchestrator。不带任何选项时,orchestrator
// 逐字节就是那个旧的、无上限、无截止的 orchestrator(每个 guard 都带一个禁用哨兵)。
type MutateOption func(*MutateOrchestrator)

// WithConcurrencyGuard 给并发 mutation 设上限(在 BeginTx BEFORE(之前)获取)。nil 或被禁用的
// Semaphore 是 no-op(旧的无上限)。acquireWait 设定在 ErrMutateBusy 之前等待槽位的上界;零则
// 只用父 ctx 的截止。
func WithConcurrencyGuard(sem *mutateguard.Semaphore, acquireWait time.Duration) MutateOption {
	return func(o *MutateOrchestrator) {
		o.sem = sem
		o.acquireWait = acquireWait
	}
}

// WithTxDeadline 给单个 mutation 事务设界(客户端 ctx 截止 + 服务端 statement_timeout)。
// 零/负值禁用它(旧行为:无截止)。
func WithTxDeadline(d time.Duration) MutateOption {
	return func(o *MutateOrchestrator) {
		if d > 0 {
			o.txDeadline = d
		}
	}
}

// WithMutationRecoveryJournal 为包含外部投递或独立事务的工具接入持久恢复日志。
// 未接入时，这类工具在执行真实变更前拒绝运行。
func WithMutationRecoveryJournal(journal MutationRecoveryJournal) MutateOption {
	return func(o *MutateOrchestrator) {
		o.recovery = journal
	}
}

// NewMutateOrchestrator 基于事务开启器构造编排器。事务开启器为空时，Execute 失败关闭；
// 选项可以增加并发保护、事务期限和持久恢复。
func NewMutateOrchestrator(begin txBeginner, opts ...MutateOption) *MutateOrchestrator {
	o := &MutateOrchestrator{begin: begin}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o
}

// errAuditUnavailable 表示事务或日志路径未接线，此时业务变更不得继续。
var errAuditUnavailable = errors.New("hermesops: mutation audit transaction unavailable")

// ErrMutateBusy 表示改动并发槽在等待期限内未释放。此时尚未开始业务写，可安全映射为 429。
var ErrMutateBusy = errors.New("hermesops: mutating concurrency saturated, retry")

// ErrMutationOutcomeAudited 表示独立事务工具已返回失败，而且失败结果已经完整写入两类日志。
// 调用层不得再补写第二条错误日志，也不得谎称该操作已由外层事务回滚。
var ErrMutationOutcomeAudited = errors.New("hermesops: recoverable mutation failed and outcome was audited")

// ErrMutationRecoveryPending 表示独立事务工具已经开始或已经返回，但最终日志尚未完成提交。
// 持久恢复任务会继续处理；调用层不得把它报告成“变更已回滚”。
var ErrMutationRecoveryPending = errors.New("hermesops: recoverable mutation outcome awaits audit recovery")

// IsOwnTxMutation 报告工具是否通过独立事务和外部副作用完成变更。这是事务模式的单一
// 事实来源，用于正确区分结果待恢复和普通事务失败。
//
//   - account_pause / account_resume 在编排器事务内修改 enabled，与日志行原子提交。
//   - dlq_replay 包含独立的投递事务与副作用，属于独立事务。
//   - renew_trigger 使用编排器传入的事务，凭据轮换及两类日志原子提交。
func IsOwnTxMutation(toolName string) bool {
	switch toolName {
	case ToolDLQReplay:
		return true
	default:
		return false
	}
}

// Execute 在原子日志和 advisory lock 事务中运行 mutate。变更只在日志行被数据库接受后
// 执行一次，lockKey 用于串行化同一目标。
//
// 业务写提交前出现任何错误都会回滚，避免为未发生的变更留下成功日志。
func (o *MutateOrchestrator) Execute(
	ctx context.Context,
	lockKey string,
	rec MutationAuditRecord,
	mutate func(ctx context.Context, tx pgx.Tx) (ToolResult, error),
) (ToolResult, error) {
	if o == nil {
		return ToolResult{}, errAuditUnavailable
	}
	if rec.OperationID == uuid.Nil {
		rec.OperationID = uuid.New()
	}

	// 在开启事务前预留并发槽，避免等待保护本身先占用数据库连接。等待超时返回纯背压信号。
	release, err := o.sem.Acquire(ctx, o.acquireWait)
	if err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: %w: %w", ErrMutateBusy, err)
	}
	defer release()

	if IsOwnTxMutation(rec.ToolName) {
		return o.executeRecoverableMutation(ctx, rec, mutate)
	}
	if o.begin == nil {
		return ToolResult{}, errAuditUnavailable
	}

	tx, err := o.begin.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: begin mutation tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// 使用独立上下文回滚；即使业务上下文已超时，也要释放连接和目标锁。
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(rollbackCtx)
		}
	}()

	// 客户端期限和事务内 statement_timeout 共同限制改动时长；SET LOCAL 在事务结束时
	// 自动复位，不污染连接池中的后续请求。零值表示不额外设置期限。
	mutCtx := ctx
	if o.txDeadline > 0 {
		var cancel context.CancelFunc
		mutCtx, cancel = context.WithTimeout(ctx, o.txDeadline)
		defer cancel()
		millis := o.txDeadline.Milliseconds()
		if _, err := tx.Exec(mutCtx, "SET LOCAL statement_timeout = $1", millis); err != nil {
			return ToolResult{}, fmt.Errorf("hermesops: set mutation statement_timeout: %w", err)
		}
	}

	// 串行化同一目标的并发改动。锁在本事务提交或回滚时释放。
	if _, err := tx.Exec(mutCtx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, lockKey); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: acquire advisory lock: %w", err)
	}

	// 先写入并校验两类日志；任一插入失败都回滚，业务变更不会运行。
	toolCallID, err := insertToolCallRow(mutCtx, tx, rec)
	if err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: tool-call audit insert failed: %w", err)
	}
	if err := insertAdminAuditRow(mutCtx, tx, rec); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: admin audit insert failed: %w", err)
	}

	// 业务变更只在日志行被数据库接受后运行，并通过同一事务与日志原子提交。
	result, mErr := mutate(withMutationTx(mutCtx, tx), tx)
	if mErr != nil {
		return ToolResult{}, mErr
	}
	rec.Status = ResultOK
	rec.ResultSummary = result.Summary
	rec.ErrorClass = result.ErrorClass
	rec.ReturnedAt = time.Now().UTC()
	if err := updateToolCallOutcome(mutCtx, tx, toolCallID, rec); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: tool-call outcome update failed: %w", err)
	}

	if err := tx.Commit(mutCtx); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: commit mutation tx: %w", err)
	}
	committed = true
	return result, nil
}

func (o *MutateOrchestrator) executeRecoverableMutation(
	ctx context.Context,
	rec MutationAuditRecord,
	mutate func(context.Context, pgx.Tx) (ToolResult, error),
) (ToolResult, error) {
	if o.recovery == nil {
		return ToolResult{}, errAuditUnavailable
	}
	if err := o.recovery.Prepare(ctx, rec); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: prepare mutation recovery: %w", err)
	}

	mutCtx := ctx
	if o.txDeadline > 0 {
		var cancel context.CancelFunc
		mutCtx, cancel = context.WithTimeout(ctx, o.txDeadline)
		defer cancel()
	}
	result, mutateErr := mutate(mutCtx, nil)
	status := ResultOK
	errorClass := result.ErrorClass
	if mutateErr != nil {
		status = ResultError
		errorClass = "mutation_failed"
		if errors.Is(mutateErr, context.DeadlineExceeded) || errors.Is(mutateErr, context.Canceled) {
			errorClass = "mutate_timeout"
		}
	}

	// 真实变更一旦开始，结果持久化不能再依赖已断开的客户端上下文。
	finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	returnedAt := time.Now().UTC()
	if err := o.recovery.RecordOutcome(finishCtx, rec.OperationID, status, result.Summary, errorClass, returnedAt); err != nil {
		return ToolResult{}, errors.Join(ErrMutationRecoveryPending, mutateErr, err)
	}
	if err := o.recovery.FinalizeAudit(finishCtx, rec.OperationID); err != nil {
		return ToolResult{}, errors.Join(ErrMutationRecoveryPending, mutateErr, err)
	}
	if mutateErr != nil {
		return ToolResult{}, errors.Join(ErrMutationOutcomeAudited, mutateErr)
	}
	return result, nil
}

// insertToolCallRow 在事务上追加已脱敏的 hermes_tool_calls 行。
func insertToolCallRow(ctx context.Context, tx pgx.Tx, rec MutationAuditRecord) (int64, error) {
	argsJSON, err := sanitizedJSON(rec.Args)
	if err != nil {
		return 0, err
	}
	summaryJSON, err := sanitizedJSON(rec.ResultSummary)
	if err != nil {
		return 0, err
	}
	called := rec.CalledAt
	if called.IsZero() {
		called = time.Now()
	}
	params := hermestoolsdb.InsertHermesToolCallParams{
		OperationID:   pgUUID(rec.OperationID),
		TenantID:      rec.TenantID,
		ActorSource:   rec.ActorSource,
		ActorID:       rec.ActorID,
		ActorRole:     rec.ActorRole,
		ToolName:      rec.ToolName,
		RequestedArgs: argsJSON,
		ResultStatus:  string(rec.Status),
		ResultSummary: summaryJSON,
		CorrelationID: nilIfEmpty(rec.CorrelationID),
		RequestID:     nilIfEmpty(rec.RequestID),
		ErrorClass:    nilIfEmpty(rec.ErrorClass),
		CalledAt:      pgtype.Timestamptz{Time: called.UTC(), Valid: true},
		DryRun:        rec.DryRun,
		LogCategory:   toolLogCategory(rec.Status, ""),
	}
	if !rec.ReturnedAt.IsZero() {
		params.ReturnedAt = pgtype.Timestamptz{Time: rec.ReturnedAt.UTC(), Valid: true}
	}
	row, err := hermestoolsdb.New(tx).InsertHermesToolCall(ctx, params)
	return row.ID, err
}

func updateToolCallOutcome(ctx context.Context, tx pgx.Tx, id int64, rec MutationAuditRecord) error {
	summaryJSON, err := sanitizedJSON(rec.ResultSummary)
	if err != nil {
		return err
	}
	rows, err := hermestoolsdb.New(tx).UpdateHermesToolCallOutcome(ctx, hermestoolsdb.UpdateHermesToolCallOutcomeParams{
		ResultStatus:  string(rec.Status),
		ResultSummary: summaryJSON,
		ErrorClass:    nilIfEmpty(rec.ErrorClass),
		ReturnedAt:    pgtype.Timestamptz{Time: rec.ReturnedAt.UTC(), Valid: true},
		LogCategory:   toolLogCategory(rec.Status, rec.ErrorClass),
		ID:            id,
		OperationID:   pgUUID(rec.OperationID),
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("hermesops: tool-call outcome row missing")
	}
	return nil
}

// insertAdminAuditRow 在同一事务中写入 admin_audit_events。动作必须在数据库允许清单中，
// 载荷已脱敏且不得包含密钥。
func insertAdminAuditRow(ctx context.Context, tx pgx.Tx, rec MutationAuditRecord) error {
	payload := hermes.SanitizeArgs(rec.AuditPayload)
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("hermesops: admin audit payload not json encodable: %w", err)
	}
	tenant := rec.TenantID
	// 与其它 handler 一致走 AuditActor()(admin_token:<id>),否则同一 operator 在同一
	// admin_audit_events 表里被分裂成两种归属串、按新格式检索会漏掉 Hermes mutation 行。
	identity := admin.AdminIdentity{Source: rec.ActorSource, Role: rec.ActorRole}
	switch rec.ActorSource {
	case admin.AdminSourceToken:
		identity.TokenID = rec.ActorID
	case admin.AdminSourceSession:
		identity.UserID = rec.ActorID
	default:
		return fmt.Errorf("hermesops: invalid admin actor source")
	}
	if rec.ActorID <= 0 {
		return fmt.Errorf("hermesops: invalid admin actor id")
	}
	actorID := identity.AuditActor()
	reqID := nilIfEmpty(rec.RequestID)
	targetID := rec.TargetID
	_, err = admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		OperationID: pgUUID(rec.OperationID),
		TenantID:    &tenant,
		ActorID:     actorID,
		ActorRole:   rec.ActorRole,
		Action:      rec.AdminAction,
		TargetType:  rec.TargetType,
		TargetID:    &targetID,
		RequestID:   reqID,
		Reason:      nil,
		Payload:     raw,
	})
	return err
}

// InsertMutationAuditRows 供持久恢复事务复用同一套两类日志写入合同。
func InsertMutationAuditRows(ctx context.Context, tx pgx.Tx, rec MutationAuditRecord) error {
	if _, err := insertToolCallRow(ctx, tx, rec); err != nil {
		return err
	}
	return insertAdminAuditRow(ctx, tx, rec)
}

func pgUUID(value uuid.UUID) pgtype.UUID {
	if value == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: value, Valid: true}
}
