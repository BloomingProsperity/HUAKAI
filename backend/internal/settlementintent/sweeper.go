package settlementintent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	defaultSweepInterval     = time.Minute
	defaultStaleAfter        = 10 * time.Minute
	defaultCreatedGrace      = 10 * time.Second
	defaultSweepBatch        = int32(100)
	settlementSweepComponent = "settlement_intent_sweeper"
)

var (
	errClaimAuthorityNotConfigured = errors.New("权威 claim 查询未配置")
	errAuthoritativeClaimNotFound  = errors.New("权威 claim 不存在")
	errSweepPanicked               = errors.New("结算意图对账轮次发生异常")
	errSweepItemPanicked           = errors.New("单条结算意图对账发生异常")
)

// ClaimSnapshot 只暴露意图追平所需的权威主账本字段。
type ClaimSnapshot struct {
	Status     string
	AttemptSeq int32
	ActualCost decimal.NullDecimal
}

// ClaimAuthority 按租户隔离读取单个权威 claim。
type ClaimAuthority interface {
	GetClaim(ctx context.Context, tenantID, claimID int64) (ClaimSnapshot, error)
}

type claimByIDQuerier interface {
	GetClaimByID(context.Context, dbbilling.GetClaimByIDParams) (dbbilling.GetClaimByIDRow, error)
}

// PostgresClaimAuthority 把宽查询集合收窄为 sweeper 所需的只读接口。
type PostgresClaimAuthority struct {
	queries claimByIDQuerier
}

func NewPostgresClaimAuthority(queries claimByIDQuerier) *PostgresClaimAuthority {
	return &PostgresClaimAuthority{queries: queries}
}

func (a *PostgresClaimAuthority) GetClaim(ctx context.Context, tenantID, claimID int64) (ClaimSnapshot, error) {
	if a == nil || a.queries == nil {
		return ClaimSnapshot{}, errClaimAuthorityNotConfigured
	}
	row, err := a.queries.GetClaimByID(ctx, dbbilling.GetClaimByIDParams{
		ID:       claimID,
		TenantID: tenantID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimSnapshot{}, errAuthoritativeClaimNotFound
	}
	if err != nil {
		return ClaimSnapshot{}, fmt.Errorf("读取权威 claim: %w", err)
	}
	return ClaimSnapshot{
		Status:     row.Status,
		AttemptSeq: row.AttemptSeq,
		ActualCost: row.ActualCost,
	}, nil
}

// SweeperOptions 允许测试或部署装配调整周期、宽限、批量和时钟。
type SweeperOptions struct {
	Interval     time.Duration
	StaleAfter   time.Duration
	CreatedGrace time.Duration
	Batch        int32
	Logger       *slog.Logger
	Now          func() time.Time
}

// SweepResult 是单轮结构化计数；Skipped 包含在途 claim 与并发 CAS 让位。
type SweepResult struct {
	Scanned    int
	Settled    int
	Aborted    int
	Superseded int
	Skipped    int
	Failed     int
}

func (r SweepResult) changed() int {
	return r.Settled + r.Aborted + r.Superseded
}

// SettlementIntentSweeper 把悬挂意图追平到权威 claim 的既有终态。
type SettlementIntentSweeper struct {
	store          Store
	claimAuthority ClaimAuthority
	interval       time.Duration
	staleAfter     time.Duration
	createdGrace   time.Duration
	batch          int32
	logger         *slog.Logger
	now            func() time.Time

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
}

func NewSettlementIntentSweeper(store Store, claimAuthority ClaimAuthority, opts SweeperOptions) *SettlementIntentSweeper {
	if opts.Interval <= 0 {
		opts.Interval = defaultSweepInterval
	}
	// 十分钟是保守起点；部署允许的最长单次请求生命周期必须短于该值，
	// 否则应通过装配注入更长阈值，避免把长流式请求误判为悬挂。
	if opts.StaleAfter <= 0 {
		opts.StaleAfter = defaultStaleAfter
	}
	// 创建宽限避免事务已经分配时间戳、但提交可见性晚于扫描边界时被跨过。
	if opts.CreatedGrace <= 0 {
		opts.CreatedGrace = defaultCreatedGrace
	}
	if opts.Batch <= 0 {
		opts.Batch = defaultSweepBatch
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	return &SettlementIntentSweeper{
		store:          store,
		claimAuthority: claimAuthority,
		interval:       opts.Interval,
		staleAfter:     opts.StaleAfter,
		createdGrace:   opts.CreatedGrace,
		batch:          opts.Batch,
		logger:         opts.Logger,
		now:            opts.Now,
	}
}

func (w *SettlementIntentSweeper) Start(ctx context.Context) {
	if w == nil || w.store == nil || w.claimAuthority == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		return
	}
	w.stop = make(chan struct{})
	w.done = make(chan struct{})
	w.running = true
	w.logger.InfoContext(ctx, "结算意图对账 worker 已启动", "component", settlementSweepComponent)
	go w.loop(ctx)
}

func (w *SettlementIntentSweeper) loop(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			w.runScheduledRound(ctx)
		}
	}
}

func (w *SettlementIntentSweeper) runScheduledRound(ctx context.Context) {
	defer func() {
		if recover() != nil {
			// 注入 logger 自身异常时改用进程默认 logger，避免恢复路径再次触发同一异常。
			slog.Default().WarnContext(ctx, "结算意图定时对账异常，下一周期将继续", "component", settlementSweepComponent)
		}
	}()
	result, err := w.RunOnce(ctx, time.Time{})
	w.logRound(ctx, result, err)
}

// RunOnce 扫描一个有界批次；单条失败或异常只计入 Failed，不中断其余意图。
func (w *SettlementIntentSweeper) RunOnce(ctx context.Context, now time.Time) (result SweepResult, err error) {
	if w == nil || w.store == nil || w.claimAuthority == nil {
		return SweepResult{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		if recover() == nil {
			return
		}
		result.Failed++
		err = errors.Join(err, errSweepPanicked)
		w.logger.WarnContext(ctx, "结算意图对账轮次异常，已隔离", "component", settlementSweepComponent)
	}()
	if now.IsZero() {
		now = w.now()
	}
	now = now.UTC()
	intents, err := w.store.ListStaleNonTerminalSettlementIntents(
		ctx,
		now.Add(-w.staleAfter),
		now.Add(-w.createdGrace),
		w.batch,
	)
	if err != nil {
		return result, fmt.Errorf("扫描悬挂结算意图: %w", err)
	}
	result.Scanned = len(intents)
	var itemErrors []error
	for _, intent := range intents {
		outcome, reconcileErr := w.reconcileSafely(ctx, intent, now)
		if reconcileErr != nil {
			result.Failed++
			w.logItemFailure(ctx, intent, reconcileErr)
			itemErrors = append(itemErrors, fmt.Errorf("意图 %d: %w", intent.ID, reconcileErr))
			continue
		}
		switch outcome {
		case outcomeSettled:
			result.Settled++
		case outcomeAborted:
			result.Aborted++
		case outcomeSuperseded:
			result.Superseded++
		case outcomeSkipped:
			result.Skipped++
		}
	}
	return result, errors.Join(itemErrors...)
}

type reconcileOutcome uint8

const (
	outcomeSkipped reconcileOutcome = iota
	outcomeSettled
	outcomeAborted
	outcomeSuperseded
)

func (w *SettlementIntentSweeper) reconcileSafely(ctx context.Context, intent StaleSettlementIntent, now time.Time) (outcome reconcileOutcome, err error) {
	defer func() {
		if recover() != nil {
			outcome = outcomeSkipped
			err = errSweepItemPanicked
		}
	}()
	claim, err := w.claimAuthority.GetClaim(ctx, intent.TenantID, intent.ClaimID)
	if err != nil {
		return outcomeSkipped, err
	}

	// attempt proof 优先于 claim 当前状态：一旦主账本已进入更高 attempt，
	// 旧意图只能作废，绝不能拿新 attempt 的终态或金额冒充旧 attempt 的证据。
	switch {
	case claim.AttemptSeq > intent.AttemptSeq:
		return w.applyCAS(intent, outcomeSuperseded, func() (int32, error) {
			return w.store.MarkSupersededIfStale(ctx, intent.ID, intent.Version)
		})
	case claim.AttemptSeq < intent.AttemptSeq:
		return outcomeSkipped, fmt.Errorf("权威 attempt=%d 早于意图 attempt=%d", claim.AttemptSeq, intent.AttemptSeq)
	}

	switch claim.Status {
	case "committed":
		if !claim.ActualCost.Valid {
			return outcomeSkipped, errors.New("committed claim 缺少权威 actual_cost")
		}
		return w.applyCAS(intent, outcomeSettled, func() (int32, error) {
			return w.store.MarkSettledIfStale(ctx, intent.ID, intent.Version, claim.ActualCost.Decimal, now)
		})
	case "aborted":
		return w.applyCAS(intent, outcomeAborted, func() (int32, error) {
			return w.store.MarkAbortedIfStale(ctx, intent.ID, intent.Version)
		})
	case "reserving":
		return outcomeSkipped, nil
	default:
		return outcomeSkipped, fmt.Errorf("未知权威 claim 状态 %q", claim.Status)
	}
}

func (w *SettlementIntentSweeper) applyCAS(intent StaleSettlementIntent, success reconcileOutcome, update func() (int32, error)) (reconcileOutcome, error) {
	nextVersion, err := update()
	if errors.Is(err, pgx.ErrNoRows) {
		// 正向 hook 或另一副本已抢先推进；让位是预期的并发结果。
		return outcomeSkipped, nil
	}
	if err != nil {
		return outcomeSkipped, err
	}
	if nextVersion != intent.Version+1 {
		return outcomeSkipped, fmt.Errorf("CAS 返回 version=%d，期望 %d", nextVersion, intent.Version+1)
	}
	return success, nil
}

func (w *SettlementIntentSweeper) logItemFailure(ctx context.Context, intent StaleSettlementIntent, err error) {
	reason := "reconcile_failed"
	switch {
	case errors.Is(err, errAuthoritativeClaimNotFound):
		reason = "claim_not_found"
	case errors.Is(err, errSweepItemPanicked):
		reason = "panic_recovered"
	}
	w.logger.WarnContext(ctx, "结算意图单条对账失败，已继续处理其余记录",
		"component", settlementSweepComponent,
		"reason", reason,
		"intent_id", intent.ID,
		"tenant_id", intent.TenantID,
		"claim_id", intent.ClaimID,
		"attempt_seq", intent.AttemptSeq,
		"intent_status", intent.Status,
		"error", err.Error(),
	)
}

func (w *SettlementIntentSweeper) logRound(ctx context.Context, result SweepResult, err error) {
	args := []any{
		"component", settlementSweepComponent,
		"scanned", result.Scanned,
		"settled", result.Settled,
		"aborted", result.Aborted,
		"superseded", result.Superseded,
		"skipped", result.Skipped,
		"failed", result.Failed,
	}
	switch {
	case err != nil:
		args = append(args, "error", err.Error())
		w.logger.WarnContext(ctx, "结算意图对账轮次完成但存在失败", args...)
	case result.changed() > 0:
		w.logger.InfoContext(ctx, "结算意图对账轮次已追平记录", args...)
	default:
		w.logger.DebugContext(ctx, "结算意图对账轮次无变更", args...)
	}
}

func (w *SettlementIntentSweeper) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	close(w.stop)
	w.running = false
	done := w.done
	w.mu.Unlock()
	if done != nil {
		<-done
	}
	w.logger.Info("结算意图对账 worker 已停止", "component", settlementSweepComponent)
}
