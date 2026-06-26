package hermesadmin

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/email"
)

// MessageSender 是 worker 所需的网关通知邮件发送方的窄子集。线上的
// *email.AuthSender 通过 SendTenantMessage 满足它——这与订阅提醒所复用的是同一条
// 租户感知的发送路径（加载 SMTP 设置、校验、SMTP 派发、短暂失败 -> outbox/DLQ
// 重试）。worker 不会构建任何新的传输。
type MessageSender interface {
	SendTenantMessage(ctx context.Context, tenantID int64, msg email.Message) error
}

// RunRecorder 记录每次巡检运行的结果（已发送 / 失败）。默认实现是一条结构化 zap
// 日志——本波次不新增任何 schema（一条日志携带的脱敏 enums/counts 与审计行相同，
// 而无论如何报告都会以邮件形式发出，因此一行 DB 记录只会增加存储 + 一个 CHECK
// 迁移，在这里对运维并无可见收益）。可注入，以便测试断言哪些被记录了、哪些没有。
type RunRecorder interface {
	RecordRun(ctx context.Context, outcome RunOutcome)
}

// RunOutcome 是每次运行的脱敏记录。它仅携带 enums/counts 以及一个固定的收件人形态
// 标志——绝不携带报告正文、绝不携带原始收件人地址（只携带是否解析出收件人）、
// 绝不携带任何诊断细节字符串。
type RunOutcome struct {
	At            time.Time
	Sent          bool
	IssueCount    int
	CriticalCount int
	SourceErrors  int
	HasRecipient  bool
	FailureClass  string // 成功时为 ""；失败时为一个固定分类（不含错误文本）
}

// zapRecorder 是默认的结构化日志记录器。
type zapRecorder struct{ log *zap.Logger }

func (z zapRecorder) RecordRun(_ context.Context, o RunOutcome) {
	if z.log == nil {
		return
	}
	z.log.Info("hermes daily inspection run",
		zap.Bool("sent", o.Sent),
		zap.Int("issue_count", o.IssueCount),
		zap.Int("critical_count", o.CriticalCount),
		zap.Int("source_errors", o.SourceErrors),
		zap.Bool("has_recipient", o.HasRecipient),
		zap.String("failure_class", o.FailureClass),
	)
}

// InspectionWorker 是采用 ExpiryWorker 模式的定时 worker：一个单独的 ticker
// goroutine，在每个 tick 运行巡检、格式化报告，并将其邮件发送给解析出的收件人。
// 它绝不 panic；发送失败会记录日志 + 计数，循环继续。
type InspectionWorker struct {
	svc       *InspectionService
	sender    MessageSender
	recorder  RunRecorder
	recipient string
	tenantID  int64
	interval  time.Duration
	log       *zap.Logger

	mu      sync.Mutex
	started bool
	stop    chan struct{}
	done    chan struct{}

	running     atomic.Bool   // 重入保护
	tickCount   atomic.Uint64 // 总 tick 数
	reportsSent atomic.Uint64 // 成功发送数
	failedTicks atomic.Uint64 // 发送失败的 tick 数
}

// InspectionWorkerConfig 用于构造 worker。
type InspectionWorkerConfig struct {
	Service   *InspectionService
	Sender    MessageSender
	Recorder  RunRecorder // nil => 结构化 zap 日志
	Recipient string
	TenantID  int64
	Interval  time.Duration // <=0 => DefaultInterval
	Logger    *zap.Logger
}

// NewInspectionWorker 构建 worker。它不校验收件人——由调用方（接线层）解析并检查
// 收件人，仅在存在收件人时才构造 worker，因此未经配置的部署永远不会走到 Start。
func NewInspectionWorker(cfg InspectionWorkerConfig) *InspectionWorker {
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	tenantID := cfg.TenantID
	if tenantID <= 0 {
		tenantID = 1
	}
	log := cfg.Logger
	if log == nil {
		log = zap.NewNop()
	}
	recorder := cfg.Recorder
	if recorder == nil {
		recorder = zapRecorder{log: log}
	}
	return &InspectionWorker{
		svc:       cfg.Service,
		sender:    cfg.Sender,
		recorder:  recorder,
		recipient: cfg.Recipient,
		tenantID:  tenantID,
		interval:  interval,
		log:       log,
	}
}

// Start 启动 ticker goroutine。重复 Start 是空操作；先 Stop 再 Start 会重启。
// 若 service/sender/recipient 缺失则拒绝启动——这是一个硬性故障安全机制，
// 使一个半接线的 worker 绝不会在没有发送目标的情况下空转、白白消耗 tick。
func (w *InspectionWorker) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started || w.svc == nil || w.sender == nil || w.recipient == "" {
		return
	}
	w.stop = make(chan struct{})
	w.done = make(chan struct{})
	w.started = true
	go w.loop(ctx)
}

func (w *InspectionWorker) loop(ctx context.Context) {
	defer close(w.done)
	// 退出时输出累计的运行指标，使运维无需单独的指标面板即可了解每日巡检 worker
	// 在其生命周期内的表现（tick 数 / 已被接受投递的报告数 / 失败的 tick 数）。
	defer func() {
		w.log.Info("hermes daily inspection worker stopped",
			zap.Uint64("ticks", w.TickCount()),
			zap.Uint64("reports_sent", w.ReportsSent()),
			zap.Uint64("failed_ticks", w.FailedTicks()),
		)
	}()
	t := time.NewTicker(w.interval)
	defer t.Stop()
	w.tick(ctx) // 启动时立即运行一次
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

// tick 运行一次 巡检 -> 格式化 -> 发送 的周期。重入保护会让慢 tick 被跳过而非重叠。
// 周期内任何位置的 panic 都会被 recover，使单次出错的运行绝不会让循环崩溃。
func (w *InspectionWorker) tick(ctx context.Context) {
	if !w.running.CompareAndSwap(false, true) {
		return
	}
	defer w.running.Store(false)
	defer func() {
		if rec := recover(); rec != nil {
			w.failedTicks.Add(1)
			w.log.Error("hermes daily inspection tick panicked", zap.Any("recover", rec))
		}
	}()
	w.tickCount.Add(1)

	report := w.svc.Inspect(ctx)
	formatted := Format(report)

	outcome := RunOutcome{
		At:            report.GeneratedAt,
		IssueCount:    report.IssueCount(),
		CriticalCount: report.CriticalCount(),
		SourceErrors:  len(report.SourceErrors),
		HasRecipient:  w.recipient != "",
	}

	err := w.sender.SendTenantMessage(ctx, w.tenantID, email.Message{
		TenantID: w.tenantID,
		To:       w.recipient,
		Subject:  formatted.Subject,
		HTMLBody: formatted.HTMLBody,
	})
	if err != nil {
		w.failedTicks.Add(1)
		outcome.Sent = false
		outcome.FailureClass = "send_failed"
		w.log.Warn("hermes daily inspection send failed", zap.Error(err))
	} else {
		w.reportsSent.Add(1)
		outcome.Sent = true
	}
	w.recorder.RecordRun(ctx, outcome)
}

// Stop 优雅地停止循环并等待其退出。重复 Stop 是空操作。
func (w *InspectionWorker) Stop() {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return
	}
	close(w.stop)
	w.started = false
	doneChan := w.done
	w.mu.Unlock()
	if doneChan != nil {
		<-doneChan
	}
}

// TickCount 返回总 tick 数。
func (w *InspectionWorker) TickCount() uint64 { return w.tickCount.Load() }

// ReportsSent 返回成功发送的计数。
func (w *InspectionWorker) ReportsSent() uint64 { return w.reportsSent.Load() }

// FailedTicks 返回发送失败（或发生 panic）的 tick 计数。
func (w *InspectionWorker) FailedTicks() uint64 { return w.failedTicks.Load() }
