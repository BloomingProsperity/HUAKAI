package auth

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

var (
	ErrStormControllerUnavailable = errors.New("auth: storm controller unavailable")
	ErrRefreshAccountLeaseMissing = errors.New("auth: refresh account lease missing")
)

type refreshAccountLeaseKey struct{}

type refreshAccountLease struct {
	tenantID  int64
	accountID int64
}

// WithRefreshAccountLease 标记当前调用已经取得账号级刷新槽。该标记只证明
// 调度链完成了跨副本准入，不能替代数据库槽本身。
func WithRefreshAccountLease(ctx context.Context, tenantID, accountID int64) context.Context {
	return context.WithValue(ctx, refreshAccountLeaseKey{}, refreshAccountLease{tenantID: tenantID, accountID: accountID})
}

// RequireRefreshAccountLease 阻止刷新器绕过统一调度直接调用远端凭据端点。
func RequireRefreshAccountLease(ctx context.Context, accountID int64) error {
	lease, ok := ctx.Value(refreshAccountLeaseKey{}).(refreshAccountLease)
	if !ok || lease.tenantID <= 0 || lease.accountID != accountID {
		return ErrRefreshAccountLeaseMissing
	}
	return nil
}

// StormController 在三个 scope 上强制 refresh storm budget:
//
//   - account:           DB 持久化的并发 budget (Postgres), 重启后仍存活,
//     并在各副本间协调。始终生效。
//   - provider-endpoint: per-(provider, endpoint) token bucket。
//   - global:            全局 token bucket。
//
// endpoint/global 两个 scope 是可选叠加式限流 (通过
// StormScopeConfig 配置); 未配置时它们一律 admit, 让始终在线的 account
// budget 成为唯一 guard。接入共享存储时两个 scope 跨副本协调；未接入时仅用于
// 单实例开发模式的进程内保护。
type StormController struct {
	queries *dbauth.Queries
	scope   *stormScopeLimiter // nil = 关闭 endpoint/global scope (admit-all)
	shared  StormScopeStore
	cfg     StormScopeConfig
	now     func() time.Time

	refreshLockPool *pgxpool.Pool
}

// NewStormControllerWithSharedScopeBudget 在配置端点或全局预算时优先使用共享存储，
// 让定时刷新与请求热刷新共同消费跨副本预算。shared 为空时保留单实例内存语义。
func NewStormControllerWithSharedScopeBudget(queries *dbauth.Queries, cfg StormScopeConfig, shared StormScopeStore, opts ...StormControllerOption) *StormController {
	c := &StormController{
		queries: queries,
		scope:   newStormScopeLimiter(cfg),
		shared:  shared,
		cfg:     cfg,
		now:     time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

func (c *StormController) clock() time.Time {
	if c == nil || c.now == nil {
		return time.Now()
	}
	return c.now()
}

// Acquire 预留一个 account-scope 的 refresh slot。返回的 release
// 函数是幂等的, 且有意做成尽力而为。
func (c *StormController) Acquire(ctx context.Context, tenantID, accountID int64) (func(), Outcome, error) {
	if c == nil || c.queries == nil {
		return nil, OutcomeStormBudgetExhausted, ErrStormControllerUnavailable
	}

	budget, err := c.queries.GetOrCreateAccountStormBudget(ctx, dbauth.GetOrCreateAccountStormBudgetParams{
		TenantID:          tenantID,
		ProviderAccountID: &accountID,
	})
	if err != nil {
		return nil, "", err
	}

	currentInFlight, err := c.queries.TryAcquireAccountStormSlot(ctx, budget.ID)
	if err != nil {
		// +1 与结果回读非原子: UPDATE 可能已提交而扫描失败(连接中断), 调用方拿不到
		// release 闭包 → 永久泄漏。补偿 -1: Release 带 GREATEST(...,0) 钳位, 未提交时
		// 补偿只会钳在 0, 净安全; 已提交时立即消除泄漏。
		c.releaseSlotWithRetry(budget.ID)
		return nil, "", err
	}
	if currentInFlight <= 0 {
		return nil, OutcomeStormBudgetExhausted, nil
	}
	lockRelease, locked, err := c.acquireRefreshAccountLock(ctx, tenantID, accountID)
	if err != nil {
		c.releaseSlotWithRetry(budget.ID)
		return nil, "", err
	}
	if !locked {
		c.releaseSlotWithRetry(budget.ID)
		return nil, OutcomeStormBudgetExhausted, nil
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			lockRelease()
			c.releaseSlotWithRetry(budget.ID)
		})
	}

	return release, "", nil
}

// releaseSlotWithRetry 释放 account 槽位: current_in_flight 是持久计数器且 cap 默认 1,
// 一次瞬时 DB 失败若被吞掉即该账号永久无法刷新。带退避重试; 全部失败则大声记日志,
// 残留泄漏由陈旧 reaper (ReapStaleSlots) 兜底自愈。用脱离 ctx: 释放不得随请求取消。
func (c *StormController) releaseSlotWithRetry(budgetID int64) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		lastErr = c.queries.ReleaseAccountStormSlot(ctx, budgetID)
		cancel()
		if lastErr == nil {
			return
		}
	}
	slog.Warn("auth: storm slot release failed after retries; stale reaper will self-heal",
		"budget_id", budgetID, "err", lastErr)
}

// ReapStaleSlots 归零陈旧的 in_flight 计数 (release 全败/进程崩溃留下的永久 +1)。
// staleBefore 之前未被 acquire/release 触碰过的 in_flight>0 行视为泄漏。
func (c *StormController) ReapStaleSlots(ctx context.Context, staleBefore time.Time) (int64, error) {
	if c == nil || c.queries == nil {
		return 0, ErrStormControllerUnavailable
	}
	return c.queries.ReapStaleAccountStormSlots(ctx, pgtype.Timestamptz{Time: staleBefore, Valid: true})
}

// AcquireProviderEndpoint 从 per-(provider, endpoint) bucket 消费一个 token。
// scope 未配置时一律 admit。准入时它返回一个 refund 函数, 仅当同一次
// acquire 级联中后续某个 scope 拒绝时, 调用方才调用它 —— 这样级联拒绝
// 不会浪费本 scope 的 budget。scope 被禁用时该 refund 是 no-op。拒绝时返回
// OutcomeStormBudgetExhausted, refund 为 nil 且无 error。
func (c *StormController) AcquireProviderEndpoint(ctx context.Context, _ int64, providerCode, endpointFingerprint string) (func(), Outcome, error) {
	if c == nil || !c.cfg.endpointEnabled() {
		return func() {}, "", nil
	}
	if c.shared != nil {
		key := "provider_endpoint:" + providerCode + "|" + endpointFingerprint
		acquired, err := c.shared.TryAcquire(ctx, key, c.cfg.PerEndpointRate, c.cfg.PerEndpointBurst)
		if err != nil {
			return nil, "", err
		}
		if !acquired {
			return nil, OutcomeStormBudgetExhausted, nil
		}
		var once sync.Once
		return func() {
			once.Do(func() {
				if err := c.shared.Refund(ctx, key, c.cfg.PerEndpointRate, c.cfg.PerEndpointBurst); err != nil {
					slog.WarnContext(ctx, "auth: shared provider endpoint storm token refund failed", "err", err)
				}
			})
		}, "", nil
	}
	bucket := c.scope.endpointBucket(providerCode + "|" + endpointFingerprint)
	if !bucket.tryAcquire(c.clock()) {
		return nil, OutcomeStormBudgetExhausted, nil
	}
	return func() { bucket.refund(c.clock()) }, "", nil
}

// AcquireGlobal 从全局 bucket 消费一个 token。生产接线使用共享存储，单实例开发
// 接线使用进程内 bucket；scope 未配置时一律 admit。
// 返回的函数是 no-op (global 是级联中的最后一个 scope, 因此从不需要级联
// refund)。拒绝时返回 OutcomeStormBudgetExhausted, 函数为 nil 且无 error。
func (c *StormController) AcquireGlobal(ctx context.Context, _ int64) (func(), Outcome, error) {
	if c == nil || !c.cfg.globalEnabled() {
		return func() {}, "", nil
	}
	if c.shared != nil {
		acquired, err := c.shared.TryAcquire(ctx, "global", c.cfg.GlobalRate, c.cfg.GlobalBurst)
		if err != nil {
			return nil, "", err
		}
		if !acquired {
			return nil, OutcomeStormBudgetExhausted, nil
		}
		return func() {}, "", nil
	}
	if !c.scope.globalBucket.tryAcquire(c.clock()) {
		return nil, OutcomeStormBudgetExhausted, nil
	}
	return func() {}, "", nil
}
