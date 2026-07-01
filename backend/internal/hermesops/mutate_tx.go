package hermesops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	TenantID          int64
	ActorUserID       int64
	AdminActorTokenID int64
	ToolName          string
	Args              map[string]any
	ResultSummary     map[string]any
	Status            ResultStatus
	ErrorClass        string
	CorrelationID     string
	RequestID         string
	CalledAt          time.Time
	ReturnedAt        time.Time
	DryRun            bool

	// admin_audit_events 镜像字段。
	AdminAction string // 例如 hermes.tool.account_pause
	AdminRole   string
	TargetType  string
	TargetID    int64
	// AuditPayload 是已脱敏的 previous->next 状态载荷。作为纵深防御,insert 前会再脱敏一次。
	AuditPayload map[string]any

	// OwnTx 标记这样一种工具:其底层 mutation 在自己 OWN(独立)的事务中提交(在 orchestrator
	// 提交 BEFORE(之前)于内部已提交),而非在 orchestrator 事务内运行。对这类工具,当 orchestrator
	// 到达自己的 COMMIT 时 mutation 已经持久化,因此一次提交阶段的故障会让 mutation 已生效而审计行
	// 回滚——这是一种 "commit_uncertain"(需要对账)状况,NOT(而非)"mutation_failed"。对一个
	// in-tx 工具,同样的故障会把 mutation 原子地回滚,所以它仍是 mutation_failed。IsOwnTxMutation
	// 推导此值。
	OwnTx bool
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
	begin txBeginner
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

// NewMutateOrchestrator 基于一个事务 beginner(pgx 连接池)构造 orchestrator。nil 的 beginner
// 会让 Execute fail-closed。不带任何选项时,它逐字节就是旧的无上限行为;传入
// WithConcurrencyGuard / WithTxDeadline 来启用可叠加的 S2 guard。
func NewMutateOrchestrator(begin txBeginner, opts ...MutateOption) *MutateOrchestrator {
	o := &MutateOrchestrator{begin: begin}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o
}

// errAuditUnavailable 在 orchestrator 未接线任何事务 beginner 时返回——没有原子审计路径,
// mutation 绝不能继续。
var errAuditUnavailable = errors.New("hermesops: mutation audit transaction unavailable")

// ErrCommitAfterOwnTxMutation 标记这一特定的残余故障:一个 OWN-TX 工具的 mutation 已在它自己的
// 事务中提交,orchestrator 随后成功插入了审计行,但 FINAL(最终)的 orchestrator COMMIT 失败了
// (传输/连接故障)。mutation 已持久化而审计行回滚,所以结果不确定(需要对账)——它 NOT(不是)一种
// "mutation 没有发生"的失败。HTTP 层把它尽力映射到 error_class "commit_uncertain"。只有 Execute
// 用此哨兵包裹一次提交阶段的故障,且仅当 rec.OwnTx 被置位时;一个 in-tx 工具的提交故障会把 mutation
// 原子地回滚,仍是 mutation_failed。
var ErrCommitAfterOwnTxMutation = errors.New("hermesops: orchestrator commit failed after own-tx mutation persisted")

// ErrMutateBusy 标记在 mutating 并发上限上的一次获取超时:已有太多 mutation 在途,且有界的获取
// 窗口在某个槽位空出之前就已过去。它是一个干净的背压信号(在上游映射到 HTTP 429),NOT(不是)一次
// 失败的 mutation——什么都还没开始。仅在并发 guard 被启用时返回。
var ErrMutateBusy = errors.New("hermesops: mutating concurrency saturated, retry")

// ErrMutateTimeoutUncertain 是 ErrCommitAfterOwnTxMutation 在 tx-deadline 路径上的 OWN-TX
// 对应物:一个 own-tx 工具(dlq_replay/renew_trigger)在其内层 own-tx 已经提交 AFTER(之后)
// 触发了 tx 截止。mutation 可能已持久化而 orchestrator 审计行回滚,所以结果是 UNCERTAIN(不确定,
// 需要对账),MUST NOT(绝不能)被报告成干净的 "rolled back"/mutation_failed。HTTP 层把它尽力
// 映射到 error_class "mutate_timeout_uncertain"。一个 in-tx 工具的截止会把 mutation 原子地回滚,
// 仍是 timeout/mutation_failed 的默认。
var ErrMutateTimeoutUncertain = errors.New("hermesops: mutation tx deadline hit after own-tx mutation may have persisted")

// IsOwnTxMutation 报告一个 mutating 工具是否在它自己 OWN(独立)的事务中运行其 mutation
// (在 orchestrator 提交之前于内部提交),而非在 orchestrator 事务内部。这是每个 H4 mutating
// 工具事务模式的单一事实来源,紧挨着工具名常量保存,这样 orchestrator 提交失败的分类
// (commit_uncertain 还是 mutation_failed)就不会与每个工具实际的持久化方式分叉。
//
//   - account_pause / account_resume 在 orchestrator 事务 INSIDE(内部)运行其 enabled 翻转
//     (经 txFromContext)——in-tx,与审计行原子。
//   - dlq_replay / renew_trigger 委托给 dlq.Service.Replay / credentialstore.Store.Rotate,
//     二者各自拥有自己的事务并在返回前提交——own-tx。
func IsOwnTxMutation(toolName string) bool {
	switch toolName {
	case ToolDLQReplay, ToolRenewTrigger:
		return true
	default:
		return false
	}
}

// Execute 在 atomic-audit + advisory-lock 事务内部运行 mutate()。mutate 收到请求与已 resolve
// 的 plan,并返回最终的 mutation 后 summary;它恰好被调用一次,且只在审计行被接受之后、并且只在
// 持有 advisory lock 期间调用。lockKey 判别 advisory lock(L4)。
//
// 在 mutation 之前/之时出现任何 error,整个事务都会回滚,这样就不会有任何审计行(以及任何锁副作用)
// 为一个未发生的 mutation 持久化下来。成功时返回的 summary 即 mutate() 的 summary。
func (o *MutateOrchestrator) Execute(
	ctx context.Context,
	lockKey string,
	rec MutationAuditRecord,
	mutate func(ctx context.Context, tx pgx.Tx) (ToolResult, error),
) (ToolResult, error) {
	if o == nil || o.begin == nil {
		return ToolResult{}, errAuditUnavailable
	}

	// S2 (a) 并发上限:在 BeginTx BEFORE(之前)预留一个 mutating 槽位,这样该上限约束的是同时
	// 有多少 mutation 持有(HOLD)一个连接池连接 / advisory-lock 槽位(在 BeginTx 之后再获取就已经
	// 占用了连接)。被禁用的 guard 是 no-op(旧的无上限)。获取超时时干净地返回 ErrMutateBusy——
	// 什么都还没开始,所以这是纯背压,绝不是挂起。
	release, err := o.sem.Acquire(ctx, o.acquireWait)
	if err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: %w: %w", ErrMutateBusy, err)
	}
	defer release()

	tx, err := o.begin.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: begin mutation tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// 在它自己 OWN(独立)的 ctx 上回滚,这样即便 tx 截止已触发(mutCtx 已 done),回滚
			// 仍能运行——释放连接 + advisory lock。
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(rollbackCtx)
		}
	}()

	// S2 (b) 事务截止:用客户端 ctx 截止(mutCtx,贯穿下面的 lock/audit/mutate/commit)与服务端
	// SET LOCAL statement_timeout 一起给 THIS(本)mutation 事务设界。SET LOCAL 仅作用于 THIS
	// (本)事务,并在事务结束时自动复位,所以它永不触及核心路径上的连接。零截止 = 禁用
	// (mutCtx == ctx,无 statement_timeout)——旧行为。
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

	// L4:串行化对 SAME(同一)目标的并发 mutation。该锁在本事务的整个生命周期内持有
	// (commit/rollback 时释放),所以第二个运营者或副本会阻塞,直到本 mutation + 审计提交或中止。
	if _, err := tx.Exec(mutCtx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, lockKey); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: acquire advisory lock: %w", err)
	}

	// L3:在 mutation BEFORE(之前)写入 + VERIFY(校验)审计行。若任一插入失败,我们就在此返回,
	// 事务回滚,mutation 从未运行。
	if err := insertToolCallRow(mutCtx, tx, rec); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: tool-call audit insert failed: %w", err)
	}
	if err := insertAdminAuditRow(mutCtx, tx, rec); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: admin audit insert failed: %w", err)
	}

	// mutation 只在审计行被数据库接受之后才运行。事务经 context 贯穿,这样一个工具的 Mutate
	// 就能与审计行原子地运行受事务约束的写(例如翻转 provider_accounts.enabled)。
	result, mErr := mutate(withMutationTx(mutCtx, tx), tx)
	if mErr != nil {
		return ToolResult{}, o.classifyMutateErr(rec, mErr)
	}

	if err := tx.Commit(mutCtx); err != nil {
		// mutation 已经运行(mErr 为 nil)。若本工具在此之前已在它自己 OWN(独立)的事务中提交了
		// 其 mutation,那么即便这次(承载审计行的)提交失败,该 mutation 也已持久化——把它分类为
		// commit_uncertain 以发出对账信号,而非报告 "mutation_failed"。一个 in-tx 工具的 mutation
		// 属于 THIS(本)事务,所以这次提交失败会把它原子地回滚,仍是默认值。
		if rec.OwnTx {
			return ToolResult{}, fmt.Errorf("hermesops: commit mutation tx: %w: %w", ErrCommitAfterOwnTxMutation, err)
		}
		return ToolResult{}, fmt.Errorf("hermesops: commit mutation tx: %w", err)
	}
	committed = true
	return result, nil
}

// classifyMutateErr 为 tx-deadline 路径包裹一个 mutate() 阶段的 error。
//
// 它防范的危险情形(正确性约束):一个 OWN-TX 工具(dlq_replay/renew_trigger),其内层 own-tx 在
// orchestrator 的 tx 截止(mutCtx)触发某个后续步骤 BEFORE(之前)已经 COMMITTED(提交)。mutation
// 可能已持久化,所以该截止 error MUST(必须)被分类为 ErrMutateTimeoutUncertain(需要对账)——
// NEVER(绝不)分类为干净的、已回滚的 mutation_failed,否则会谎称 replay/rotate 没有发生。
//
// 它只在 (1) tx 截止已启用、(2) error 是一个 context 截止、(3) 工具是 own-tx 时触发。一个 in-tx
// 工具的截止会把 mutation 原子地回滚(它属于 THIS(本)事务),所以原样返回,在上游仍是
// timeout/mutation_failed 的默认。一个非截止的 mutate error(真正的工具失败)也原样返回。
func (o *MutateOrchestrator) classifyMutateErr(rec MutationAuditRecord, mErr error) error {
	if o.txDeadline <= 0 || !rec.OwnTx {
		return mErr
	}
	if !errors.Is(mErr, context.DeadlineExceeded) {
		return mErr
	}
	return fmt.Errorf("hermesops: own-tx mutation deadline: %w: %w", ErrMutateTimeoutUncertain, mErr)
}

// insertToolCallRow 在事务上追加已脱敏的 hermes_tool_calls 行。
func insertToolCallRow(ctx context.Context, tx pgx.Tx, rec MutationAuditRecord) error {
	argsJSON, err := sanitizedJSON(rec.Args)
	if err != nil {
		return err
	}
	summaryJSON, err := sanitizedJSON(rec.ResultSummary)
	if err != nil {
		return err
	}
	called := rec.CalledAt
	if called.IsZero() {
		called = time.Now()
	}
	params := hermestoolsdb.InsertHermesToolCallParams{
		TenantID:      rec.TenantID,
		ActorUserID:   rec.ActorUserID,
		ToolName:      rec.ToolName,
		RequestedArgs: argsJSON,
		ResultStatus:  string(rec.Status),
		ResultSummary: summaryJSON,
		CorrelationID: nilIfEmpty(rec.CorrelationID),
		RequestID:     nilIfEmpty(rec.RequestID),
		ErrorClass:    nilIfEmpty(rec.ErrorClass),
		CalledAt:      pgtype.Timestamptz{Time: called.UTC(), Valid: true},
		DryRun:        rec.DryRun,
	}
	if rec.AdminActorTokenID > 0 {
		id := rec.AdminActorTokenID
		params.AdminActorTokenID = &id
	}
	if !rec.ReturnedAt.IsZero() {
		params.ReturnedAt = pgtype.Timestamptz{Time: rec.ReturnedAt.UTC(), Valid: true}
	}
	_, err = hermestoolsdb.New(tx).InsertHermesToolCall(ctx, params)
	return err
}

// insertAdminAuditRow 在同一事务上把 mutation 镜像进 admin_audit_events。action MUST(必须)
// 在迁移白名单中;payload 已脱敏(previous->next 状态,只含枚举/id——绝不含密钥)。
func insertAdminAuditRow(ctx context.Context, tx pgx.Tx, rec MutationAuditRecord) error {
	payload := hermes.SanitizeArgs(rec.AuditPayload)
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("hermesops: admin audit payload not json encodable: %w", err)
	}
	tenant := rec.TenantID
	// 与其它 handler 一致走 AuditActor()(admin_token:<id>),否则同一 operator 在同一
	// admin_audit_events 表里被分裂成两种归属串、按新格式检索会漏掉 Hermes mutation 行。
	actorID := admin.AdminIdentity{TokenID: rec.AdminActorTokenID, Source: admin.AdminSourceToken}.AuditActor()
	reqID := nilIfEmpty(rec.RequestID)
	targetID := rec.TargetID
	_, err = admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID:   &tenant,
		ActorID:    actorID,
		ActorRole:  rec.AdminRole,
		Action:     rec.AdminAction,
		TargetType: rec.TargetType,
		TargetID:   &targetID,
		RequestID:  reqID,
		Reason:     nil,
		Payload:    raw,
	})
	return err
}
