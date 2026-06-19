package mediatask

import (
	"context"
	"log/slog"
)

// OrphanPersister 是 PersistingOrphanReporter 依赖的持久化能力(生产由 *PostgresStore 实现)。
type OrphanPersister interface {
	PersistOrphan(ctx context.Context, rec OrphanRecord) error
}

// PersistingOrphanReporter 把孤儿上游任务线索持久化到 media_task_orphans,实现 OrphanReporter。
// fire-and-forget:持久化失败只记错误日志,绝不阻塞或 panic worker 主循环——worker 在调用本上报器
// 之前已对孤儿做了结构化 Warn 日志兜底,故即便落库失败,线索也不会完全丢失。
type PersistingOrphanReporter struct {
	store  OrphanPersister
	logger *slog.Logger
}

// NewPersistingOrphanReporter 构造持久化孤儿上报器;logger 为 nil 时回退 slog 默认实例。
func NewPersistingOrphanReporter(store OrphanPersister, logger *slog.Logger) *PersistingOrphanReporter {
	if logger == nil {
		logger = slog.Default()
	}
	return &PersistingOrphanReporter{store: store, logger: logger}
}

// ReportOrphanProviderTask 把一条孤儿事件落库。store 为 nil 时静默跳过(等价于未配置持久化),
// 落库报错时只记日志、不向上抛(契约要求不得阻塞 worker)。
func (r *PersistingOrphanReporter) ReportOrphanProviderTask(ctx context.Context, ev OrphanProviderTask) {
	if r == nil || r.store == nil {
		return
	}
	err := r.store.PersistOrphan(ctx, OrphanRecord{
		TaskID:         ev.TaskID,
		TenantID:       ev.TenantID,
		UserID:         ev.UserID,
		Provider:       ev.Provider,
		ProviderTaskID: ev.ProviderTaskID,
		LeaseOwner:     ev.Owner,
		ObservedAt:     ev.ObservedAt,
	})
	if err != nil {
		r.logger.ErrorContext(ctx, "mediatask 孤儿线索持久化失败(已有结构化日志兜底)",
			slog.Int64("task_id", ev.TaskID),
			slog.String("provider_task_id", ev.ProviderTaskID),
			slog.Any("error", err),
		)
	}
}
