package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

var (
	ErrStormControllerUnavailable = errors.New("auth: storm controller unavailable")
)

// StormController 在三个 scope 上强制 refresh storm budget:
//
//   - account:           DB 持久化的并发 budget (Postgres), 重启后仍存活,
//     并在各副本间协调。始终生效。
//   - provider-endpoint: 内存版 per-(provider, endpoint) token bucket。
//   - global:            内存版进程级 token bucket。
//
// endpoint/global 两个 scope 是可选叠加式限流 (通过
// StormScopeConfig 配置); 未配置时它们一律 admit, 让始终在线的 account
// budget 成为唯一 guard —— 行为上与此前 account-only 的切片完全一致。
// 它们是进程级的; 跨副本的 endpoint/global budget 是后续工作。
type StormController struct {
	queries *dbauth.Queries
	scope   *stormScopeLimiter // nil = 关闭 endpoint/global scope (admit-all)
	now     func() time.Time
}

// NewStormController 构建一个 account-scope-only 的 controller。endpoint 和 global
// 两个 scope 一律 admit (叠加式限流关闭)。要启用它们请用
// NewStormControllerWithScopeBudget。
func NewStormController(queries *dbauth.Queries) *StormController {
	return NewStormControllerWithScopeBudget(queries, StormScopeConfig{})
}

// NewStormControllerWithScopeBudget 构建一个 controller, 其 endpoint/global
// 两个 scope 强制使用所给的 token budget。零值/部分配置会让这些 scope
// 处于关闭状态 (admit-all) —— account scope 始终被强制。
func NewStormControllerWithScopeBudget(queries *dbauth.Queries, cfg StormScopeConfig) *StormController {
	return &StormController{
		queries: queries,
		scope:   newStormScopeLimiter(cfg),
		now:     time.Now,
	}
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
		return nil, "", err
	}
	if currentInFlight <= 0 {
		return nil, OutcomeStormBudgetExhausted, nil
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			_ = c.queries.ReleaseAccountStormSlot(context.Background(), budget.ID)
		})
	}

	return release, "", nil
}

// AcquireProviderEndpoint 从 per-(provider, endpoint) bucket 消费一个 token。
// scope 未配置时一律 admit。准入时它返回一个 refund 函数, 仅当同一次
// acquire 级联中后续某个 scope 拒绝时, 调用方才调用它 —— 这样级联拒绝
// 不会浪费本 scope 的 budget。scope 被禁用时该 refund 是 no-op。拒绝时返回
// OutcomeStormBudgetExhausted, refund 为 nil 且无 error。
func (c *StormController) AcquireProviderEndpoint(_ context.Context, _ int64, providerCode, endpointFingerprint string) (func(), Outcome, error) {
	if c == nil || c.scope == nil || !c.scope.cfg.endpointEnabled() {
		return func() {}, "", nil
	}
	bucket := c.scope.endpointBucket(providerCode + "|" + endpointFingerprint)
	if !bucket.tryAcquire(c.clock()) {
		return nil, OutcomeStormBudgetExhausted, nil
	}
	return func() { bucket.refund(c.clock()) }, "", nil
}

// AcquireGlobal 从进程级 bucket 消费一个 token。scope 未配置时一律 admit。
// 返回的函数是 no-op (global 是级联中的最后一个 scope, 因此从不需要级联
// refund)。拒绝时返回 OutcomeStormBudgetExhausted, 函数为 nil 且无 error。
func (c *StormController) AcquireGlobal(_ context.Context, _ int64) (func(), Outcome, error) {
	if c == nil || c.scope == nil || c.scope.globalBucket == nil {
		return func() {}, "", nil
	}
	if !c.scope.globalBucket.tryAcquire(c.clock()) {
		return nil, OutcomeStormBudgetExhausted, nil
	}
	return func() {}, "", nil
}
