// routing_policy_source_test.go — 路由加权激活闭环测试(A 点亮 / B 默认不翻转 / C 注入真实)。
//
// 这些测试用生产 RoutingPolicySource(newBindingRoutingPolicySource)接真 DefaultSelector,
// 经公开 Select 路径驱动,断言 req.SelectionMode 真影响选号:
//   - "priority_weighted" → 按账号 Weight 加权,高 weight 账号显著更易被选(测试 A)。
//   - ""/"strict_priority" → 均匀,高 weight 账号不被偏向,与未接 policy source 时一致(测试 B)。
//   - GetRoutingPolicy 对 priority_weighted 返回非 nil 且模式正确(测试 C)。
package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	poolrouter "github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

var errUnexpectedRoutingPolicyFallback = errors.New("routing policy fallback 配置不符合预期")

// weightedFakeAccountSource 返回三个同优先级、同负载、同 LastUsedAt 的账号(故落在同一 tie-band),
// 唯一差异是 Weight,用于观察加权选号是否真按 Weight 倾斜。
type weightedFakeAccountSource struct {
	accounts []*poolrouter.AccountSnapshot
}

func (s *weightedFakeAccountSource) ListAccounts(context.Context, poolrouter.SelectionRequest) ([]*poolrouter.AccountSnapshot, error) {
	// 返回副本指针切片(Select 内部可能重排,避免跨调用污染)。
	out := make([]*poolrouter.AccountSnapshot, len(s.accounts))
	copy(out, s.accounts)
	return out, nil
}

// alwaysAcquireSlotManager 让每个候选都能拿到 slot,故 Select 必返回 rank 后的首选账号——
// 把"选号策略"从"slot 抢占"中隔离出来,使统计断言只反映加权/均匀逻辑本身。
type alwaysAcquireSlotManager struct{}

func (alwaysAcquireSlotManager) Acquire(context.Context, *poolrouter.AccountSnapshot, poolrouter.SelectionRequest) (*poolrouter.AcquireResult, error) {
	return &poolrouter.AcquireResult{AcquisitionToken: uuid.New()}, nil
}

func sameBandWeightedAccounts() []*poolrouter.AccountSnapshot {
	now := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	mk := func(id int64, w int32) *poolrouter.AccountSnapshot {
		return &poolrouter.AccountSnapshot{
			ID: id, TenantID: 1, Priority: 100, LoadRate: 0.2, LastUsedAt: now, Weight: w,
		}
	}
	// 账号 10 重(weight=10),11/12 轻(weight=1)。
	return []*poolrouter.AccountSnapshot{mk(10, 10), mk(11, 1), mk(12, 1)}
}

// runSelectDraws 用给定 selector 跑 draws 次 Select,返回各账号被选计数。
func runSelectDraws(t *testing.T, sel *poolrouter.DefaultSelector, mode string, draws int) map[int64]int {
	t.Helper()
	counts := map[int64]int{}
	for i := 0; i < draws; i++ {
		res, err := sel.Select(context.Background(), poolrouter.SelectionRequest{
			TenantID:       1,
			RequestedModel: "x",
			SelectionMode:  mode,
		})
		if err != nil {
			t.Fatalf("draw %d Select 失败: %v", i, err)
		}
		if res == nil || res.AccountID == 0 {
			t.Fatalf("draw %d 无选中账号: %+v", i, res)
		}
		counts[res.AccountID]++
	}
	return counts
}

// newWeightedSelector 构造接生产 policy source 的 selector(测试 A/C 走加权路径用)。
func newWeightedSelector(withPolicySource bool) *poolrouter.DefaultSelector {
	opts := []poolrouter.SelectorOption{
		poolrouter.WithSlotManager(alwaysAcquireSlotManager{}),
	}
	if withPolicySource {
		opts = append(opts, poolrouter.WithRoutingPolicySource(newBindingRoutingPolicySource()))
	}
	return poolrouter.NewDefaultSelector(
		&weightedFakeAccountSource{accounts: sameBandWeightedAccounts()},
		opts...,
	)
}

// TestRoutingWeightActivation_PriorityWeightedSkewsByWeight —— 测试 A:闭环点亮。
// binding=priority_weighted + 账号不同 Weight → 高 weight 账号被选概率显著更高。
// MUTATION:把 dispatch 穿线改回丢 selection_mode(等价于此处不传 priority_weighted)→ 退回均匀
// (见测试 B 的 ratio≈1)→ 本测试的 ratio≈10 断言红。
func TestRoutingWeightActivation_PriorityWeightedSkewsByWeight(t *testing.T) {
	sel := newWeightedSelector(true)
	const draws = 18000
	counts := runSelectDraws(t, sel, string(poolrouter.SelectionModePriorityWeighted), draws)

	lightAvg := float64(counts[11]+counts[12]) / 2
	if lightAvg == 0 {
		t.Fatalf("轻账号从未被选,counts=%v", counts)
	}
	ratio := float64(counts[10]) / lightAvg
	// 权重比 10:1,期望重账号被选频次约为轻账号均值的 10 倍。给宽容窗防抽样噪声。
	if ratio < 7.5 || ratio > 13.0 {
		t.Fatalf("加权选号 重:轻 比 = %.2f, 期望约 10; counts=%v", ratio, counts)
	}
}

// TestRoutingWeightActivation_DefaultStrictNotFlipped —— 测试 B:默认不翻转(self-proving)。
// binding 默认(SelectionMode="")→ 走均匀 Shuffle,高 weight 账号【不】被偏向;
// 且与"未接 RoutingPolicySource"时分布同性质(均匀)。self-proving:同一组账号,
// 接 policy source 的 strict 路径 与 不接 policy source 的路径,重账号占比都接近 1/3(均匀),
// 二者差异在抽样噪声内——证明接线【没有】改变默认行为。
// MUTATION:让 strict 也走加权(例如 GetRoutingPolicy 恒返回 priority_weighted)→ 重账号占比
// 跳到 ~10/12 ≈ 0.83 → 下面两个 near-uniform 断言齐红。
func TestRoutingWeightActivation_DefaultStrictNotFlipped(t *testing.T) {
	const draws = 18000

	// 接生产 policy source,但 mode 留空(默认 strict)。
	wired := newWeightedSelector(true)
	wiredCounts := runSelectDraws(t, wired, "", draws)

	// 完全不接 policy source(断点1 原状,policy() 恒 nil)。
	unwired := newWeightedSelector(false)
	unwiredCounts := runSelectDraws(t, unwired, "", draws)

	heavyShareWired := float64(wiredCounts[10]) / float64(draws)
	heavyShareUnwired := float64(unwiredCounts[10]) / float64(draws)

	// 三账号均匀时,重账号占比应约 1/3,绝不接近加权时的 ~0.83。
	if heavyShareWired < 0.28 || heavyShareWired > 0.39 {
		t.Fatalf("strict 接线后 重账号占比 = %.3f, 期望约 0.333(均匀);counts=%v", heavyShareWired, wiredCounts)
	}
	if heavyShareUnwired < 0.28 || heavyShareUnwired > 0.39 {
		t.Fatalf("未接 policy source 重账号占比 = %.3f, 期望约 0.333(均匀);counts=%v", heavyShareUnwired, unwiredCounts)
	}
	// self-proving:接线前后差异须在抽样噪声内(同为均匀分布,不应有系统性偏移)。
	delta := heavyShareWired - heavyShareUnwired
	if delta < -0.04 || delta > 0.04 {
		t.Fatalf("接线前后 重账号占比差 = %.3f 超噪声窗,默认行为被改变了;wired=%v unwired=%v", delta, wiredCounts, unwiredCounts)
	}
}

// TestBindingRoutingPolicySource_ReturnsNonNilByMode —— 测试 C:注入真实。
// 生产 RoutingPolicySource 对 priority_weighted 的请求返回非 nil 且 SelectionMode 正确;
// 对默认/strict 返回 strict_priority(非 priority_weighted)。
// MUTATION:若注入点漏接(回到断点1,policy() 恒 nil)→ 加权分支不可达 → 测试 A 红;
// 若本源恒返回 strict → 测试 A 红。本测试直接钉死映射本身。
func TestBindingRoutingPolicySource_ReturnsNonNilByMode(t *testing.T) {
	src := newBindingRoutingPolicySource()

	weighted, err := src.GetRoutingPolicy(context.Background(), poolrouter.SelectionRequest{
		SelectionMode: string(poolrouter.SelectionModePriorityWeighted),
	})
	if err != nil {
		t.Fatalf("priority_weighted GetRoutingPolicy 失败: %v", err)
	}
	if weighted == nil {
		t.Fatalf("priority_weighted policy 为 nil(注入失效,加权分支永不可达)")
	}
	if weighted.SelectionMode != poolrouter.SelectionModePriorityWeighted {
		t.Fatalf("priority_weighted policy.SelectionMode = %q, 期望 priority_weighted", weighted.SelectionMode)
	}

	for _, mode := range []string{"", "strict_priority", "garbage_unknown"} {
		strict, err := src.GetRoutingPolicy(context.Background(), poolrouter.SelectionRequest{SelectionMode: mode})
		if err != nil {
			t.Fatalf("mode=%q GetRoutingPolicy 失败: %v", mode, err)
		}
		if strict == nil {
			t.Fatalf("mode=%q policy 为 nil", mode)
		}
		if strict.SelectionMode == poolrouter.SelectionModePriorityWeighted {
			t.Fatalf("mode=%q 误判为 priority_weighted, 默认不该走加权", mode)
		}
	}
}

type countingRoutingPolicyPoolStore struct {
	mu    sync.Mutex
	rows  map[routingPolicyCacheKey]dbbilling.PoolGroup
	calls []dbbilling.GetPoolParams
}

func newCountingRoutingPolicyPoolStore() *countingRoutingPolicyPoolStore {
	return &countingRoutingPolicyPoolStore{rows: make(map[routingPolicyCacheKey]dbbilling.PoolGroup)}
}

func (s *countingRoutingPolicyPoolStore) set(tenantID, poolGroupID int64, maxWaiting, timeoutMS int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[routingPolicyCacheKey{tenantID: tenantID, poolGroupID: poolGroupID}] = dbbilling.PoolGroup{
		ID:                     poolGroupID,
		TenantID:               tenantID,
		FallbackWaitMaxWaiting: maxWaiting,
		FallbackWaitTimeoutMs:  timeoutMS,
	}
}

func (s *countingRoutingPolicyPoolStore) GetPool(_ context.Context, arg dbbilling.GetPoolParams) (dbbilling.PoolGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, arg)
	if row, ok := s.rows[routingPolicyCacheKey{tenantID: arg.TenantID, poolGroupID: arg.ID}]; ok {
		return row, nil
	}
	return dbbilling.PoolGroup{
		ID:                     arg.ID,
		TenantID:               arg.TenantID,
		FallbackWaitMaxWaiting: int32(10 + arg.ID),
		FallbackWaitTimeoutMs:  int32(1000 + arg.ID),
	}, nil
}

func (s *countingRoutingPolicyPoolStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

type routingPolicyGetPoolGate struct {
	entered     chan struct{}
	release     chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
}

// inflightWaitObservedContext 在 singleflight 等待分支读取 Done 时发出信号。
// 返回的 done 永不关闭，保证测试只观察等待者已经就位，不改变生产控制流。
type inflightWaitObservedContext struct {
	context.Context
	observed chan struct{}
	done     chan struct{}
	once     sync.Once
}

func newInflightWaitObservedContext() *inflightWaitObservedContext {
	return &inflightWaitObservedContext{
		Context:  context.Background(),
		observed: make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (c *inflightWaitObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.done
}

func newRoutingPolicyGetPoolGate() *routingPolicyGetPoolGate {
	return &routingPolicyGetPoolGate{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (g *routingPolicyGetPoolGate) markEntered() {
	g.enterOnce.Do(func() { close(g.entered) })
}

func (g *routingPolicyGetPoolGate) releaseQuery() {
	g.releaseOnce.Do(func() { close(g.release) })
}

type blockingRoutingPolicyPoolStore struct {
	mu    sync.Mutex
	rows  map[routingPolicyCacheKey]dbbilling.PoolGroup
	errs  map[routingPolicyCacheKey]error
	gates map[routingPolicyCacheKey]*routingPolicyGetPoolGate
	calls map[routingPolicyCacheKey]int
}

func newBlockingRoutingPolicyPoolStore() *blockingRoutingPolicyPoolStore {
	return &blockingRoutingPolicyPoolStore{
		rows:  make(map[routingPolicyCacheKey]dbbilling.PoolGroup),
		errs:  make(map[routingPolicyCacheKey]error),
		gates: make(map[routingPolicyCacheKey]*routingPolicyGetPoolGate),
		calls: make(map[routingPolicyCacheKey]int),
	}
}

func (s *blockingRoutingPolicyPoolStore) set(tenantID, poolGroupID int64, maxWaiting, timeoutMS int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[routingPolicyCacheKey{tenantID: tenantID, poolGroupID: poolGroupID}] = dbbilling.PoolGroup{
		ID:                     poolGroupID,
		TenantID:               tenantID,
		FallbackWaitMaxWaiting: maxWaiting,
		FallbackWaitTimeoutMs:  timeoutMS,
	}
}

func (s *blockingRoutingPolicyPoolStore) setErr(tenantID, poolGroupID int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := routingPolicyCacheKey{tenantID: tenantID, poolGroupID: poolGroupID}
	if err == nil {
		delete(s.errs, key)
		return
	}
	s.errs[key] = err
}

func (s *blockingRoutingPolicyPoolStore) setGate(tenantID, poolGroupID int64, gate *routingPolicyGetPoolGate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := routingPolicyCacheKey{tenantID: tenantID, poolGroupID: poolGroupID}
	if gate == nil {
		delete(s.gates, key)
		return
	}
	s.gates[key] = gate
}

func (s *blockingRoutingPolicyPoolStore) GetPool(ctx context.Context, arg dbbilling.GetPoolParams) (dbbilling.PoolGroup, error) {
	key := routingPolicyCacheKey{tenantID: arg.TenantID, poolGroupID: arg.ID}
	s.mu.Lock()
	s.calls[key]++
	row, ok := s.rows[key]
	err := s.errs[key]
	gate := s.gates[key]
	s.mu.Unlock()

	if gate != nil {
		gate.markEntered()
		select {
		case <-gate.release:
		case <-ctx.Done():
			return dbbilling.PoolGroup{}, ctx.Err()
		}
	}
	if err != nil {
		return dbbilling.PoolGroup{}, err
	}
	if ok {
		return row, nil
	}
	return dbbilling.PoolGroup{
		ID:                     arg.ID,
		TenantID:               arg.TenantID,
		FallbackWaitMaxWaiting: int32(10 + arg.ID),
		FallbackWaitTimeoutMs:  int32(1000 + arg.ID),
	}, nil
}

func (s *blockingRoutingPolicyPoolStore) callCountFor(tenantID, poolGroupID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[routingPolicyCacheKey{tenantID: tenantID, poolGroupID: poolGroupID}]
}

func TestBindingRoutingPolicySource_CachesFallbackForTenantPoolGroup(t *testing.T) {
	now := time.Unix(1000, 0)
	store := newCountingRoutingPolicyPoolStore()
	store.set(7, 42, 3, 2500)
	src := &bindingRoutingPolicySource{
		q:        store,
		cacheTTL: time.Minute,
		now:      func() time.Time { return now },
	}
	req := poolrouter.SelectionRequest{TenantID: 7, PoolGroupID: 42}

	first, err := src.GetRoutingPolicy(context.Background(), req)
	if err != nil {
		t.Fatalf("first GetRoutingPolicy 失败: %v", err)
	}
	second, err := src.GetRoutingPolicy(context.Background(), req)
	if err != nil {
		t.Fatalf("second GetRoutingPolicy 失败: %v", err)
	}
	if got := store.callCount(); got != 1 {
		t.Fatalf("GetPool calls=%d want 1; MUTATION:删除缓存会变成 2", got)
	}
	for i, policy := range []*poolrouter.RoutingPolicy{first, second} {
		if policy.FallbackMaxWaiting != 3 || policy.FallbackTimeoutMS != 2500 {
			t.Fatalf("policy[%d] fallback=%d/%d want 3/2500", i, policy.FallbackMaxWaiting, policy.FallbackTimeoutMS)
		}
	}
}

func TestBindingRoutingPolicySource_RefreshesAfterCacheTTL(t *testing.T) {
	now := time.Unix(1000, 0)
	store := newCountingRoutingPolicyPoolStore()
	store.set(7, 42, 3, 2500)
	src := &bindingRoutingPolicySource{
		q:        store,
		cacheTTL: time.Minute,
		now:      func() time.Time { return now },
	}
	req := poolrouter.SelectionRequest{TenantID: 7, PoolGroupID: 42}

	if _, err := src.GetRoutingPolicy(context.Background(), req); err != nil {
		t.Fatalf("first GetRoutingPolicy 失败: %v", err)
	}
	store.set(7, 42, 5, 4100)
	now = now.Add(time.Minute + time.Nanosecond)
	refreshed, err := src.GetRoutingPolicy(context.Background(), req)
	if err != nil {
		t.Fatalf("expired GetRoutingPolicy 失败: %v", err)
	}
	if got := store.callCount(); got != 2 {
		t.Fatalf("GetPool calls=%d want 2", got)
	}
	if refreshed.FallbackMaxWaiting != 5 || refreshed.FallbackTimeoutMS != 4100 {
		t.Fatalf("refreshed fallback=%d/%d want 5/4100", refreshed.FallbackMaxWaiting, refreshed.FallbackTimeoutMS)
	}
}

func TestBindingRoutingPolicySource_CacheSeparatesPoolGroups(t *testing.T) {
	now := time.Unix(1000, 0)
	store := newCountingRoutingPolicyPoolStore()
	store.set(7, 42, 3, 2500)
	store.set(7, 43, 4, 3500)
	src := &bindingRoutingPolicySource{
		q:        store,
		cacheTTL: time.Minute,
		now:      func() time.Time { return now },
	}

	first, err := src.GetRoutingPolicy(context.Background(), poolrouter.SelectionRequest{TenantID: 7, PoolGroupID: 42})
	if err != nil {
		t.Fatalf("pool 42 GetRoutingPolicy 失败: %v", err)
	}
	second, err := src.GetRoutingPolicy(context.Background(), poolrouter.SelectionRequest{TenantID: 7, PoolGroupID: 43})
	if err != nil {
		t.Fatalf("pool 43 GetRoutingPolicy 失败: %v", err)
	}
	if got := store.callCount(); got != 2 {
		t.Fatalf("GetPool calls=%d want 2", got)
	}
	if first.FallbackTimeoutMS == second.FallbackTimeoutMS {
		t.Fatalf("不同 pool_group 被缓存串用:first=%+v second=%+v", first, second)
	}
}

func TestBindingRoutingPolicySource_CacheConcurrentSafe(t *testing.T) {
	now := time.Unix(1000, 0)
	store := newCountingRoutingPolicyPoolStore()
	store.set(7, 42, 3, 2500)
	src := &bindingRoutingPolicySource{
		q:        store,
		cacheTTL: time.Minute,
		now:      func() time.Time { return now },
	}
	req := poolrouter.SelectionRequest{TenantID: 7, PoolGroupID: 42}

	const workers = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			policy, err := src.GetRoutingPolicy(context.Background(), req)
			if err != nil {
				errs <- err
				return
			}
			if policy.FallbackMaxWaiting != 3 || policy.FallbackTimeoutMS != 2500 {
				errs <- errUnexpectedRoutingPolicyFallback
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent GetRoutingPolicy 失败: %v", err)
		}
	}
	if got := store.callCount(); got != 1 {
		t.Fatalf("并发 GetPool calls=%d want 1", got)
	}
}

func TestBindingRoutingPolicySource_SingleFlightSameKeyMiss(t *testing.T) {
	now := time.Unix(1000, 0)
	store := newBlockingRoutingPolicyPoolStore()
	store.set(7, 42, 3, 2500)
	gate := newRoutingPolicyGetPoolGate()
	store.setGate(7, 42, gate)
	src := &bindingRoutingPolicySource{
		q:        store,
		cacheTTL: time.Minute,
		now:      func() time.Time { return now },
	}
	req := poolrouter.SelectionRequest{TenantID: 7, PoolGroupID: 42}

	const workers = 24
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			policy, err := src.GetRoutingPolicy(context.Background(), req)
			if err != nil {
				errs <- err
				return
			}
			if policy.FallbackMaxWaiting != 3 || policy.FallbackTimeoutMS != 2500 {
				errs <- errUnexpectedRoutingPolicyFallback
				return
			}
			errs <- nil
		}()
	}
	close(start)
	<-gate.entered
	gate.releaseQuery()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("single-flight 同 key miss 返回错误: %v", err)
		}
	}
	if got := store.callCountFor(7, 42); got != 1 {
		t.Fatalf("同 key 并发 miss GetPool calls=%d want 1", got)
	}
}

func TestBindingRoutingPolicySource_CacheHitUnaffectedByOtherKeyInflight(t *testing.T) {
	now := time.Unix(1000, 0)
	store := newBlockingRoutingPolicyPoolStore()
	store.set(7, 42, 3, 2500)
	store.set(7, 43, 4, 3500)
	src := &bindingRoutingPolicySource{
		q:        store,
		cacheTTL: time.Minute,
		now:      func() time.Time { return now },
	}
	cachedReq := poolrouter.SelectionRequest{TenantID: 7, PoolGroupID: 43}
	if _, err := src.GetRoutingPolicy(context.Background(), cachedReq); err != nil {
		t.Fatalf("prime cached key 失败: %v", err)
	}

	gate := newRoutingPolicyGetPoolGate()
	defer gate.releaseQuery()
	store.setGate(7, 42, gate)
	blockedDone := make(chan error, 1)
	go func() {
		_, err := src.GetRoutingPolicy(context.Background(), poolrouter.SelectionRequest{TenantID: 7, PoolGroupID: 42})
		blockedDone <- err
	}()
	<-gate.entered

	cachedDone := make(chan error, 1)
	go func() {
		policy, err := src.GetRoutingPolicy(context.Background(), cachedReq)
		if err != nil {
			cachedDone <- err
			return
		}
		if policy.FallbackMaxWaiting != 4 || policy.FallbackTimeoutMS != 3500 {
			cachedDone <- errUnexpectedRoutingPolicyFallback
			return
		}
		cachedDone <- nil
	}()

	select {
	case err := <-cachedDone:
		if err != nil {
			t.Fatalf("cached key 返回错误: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("key A 查询阻塞时,已缓存 key B 被全局锁卡住")
	}

	gate.releaseQuery()
	if err := <-blockedDone; err != nil {
		t.Fatalf("blocked key 查询释放后返回错误: %v", err)
	}
	if got := store.callCountFor(7, 43); got != 1 {
		t.Fatalf("cached key GetPool calls=%d want 1", got)
	}
}

func TestBindingRoutingPolicySource_InflightErrorPropagatesAndDoesNotCache(t *testing.T) {
	now := time.Unix(1000, 0)
	boom := errors.New("routing policy db down")
	store := newBlockingRoutingPolicyPoolStore()
	store.set(7, 42, 3, 2500)
	store.setErr(7, 42, boom)
	gate := newRoutingPolicyGetPoolGate()
	store.setGate(7, 42, gate)
	src := &bindingRoutingPolicySource{
		q:        store,
		cacheTTL: time.Minute,
		now:      func() time.Time { return now },
	}
	req := poolrouter.SelectionRequest{TenantID: 7, PoolGroupID: 42}

	const followers = 7
	errs := make(chan error, followers+1)
	go func() {
		_, err := src.GetRoutingPolicy(context.Background(), req)
		errs <- err
	}()
	<-gate.entered

	observed := make([]<-chan struct{}, 0, followers)
	for i := 0; i < followers; i++ {
		waitCtx := newInflightWaitObservedContext()
		observed = append(observed, waitCtx.observed)
		go func() {
			_, err := src.GetRoutingPolicy(waitCtx, req)
			errs <- err
		}()
	}
	for _, waiterObserved := range observed {
		<-waiterObserved
	}
	gate.releaseQuery()
	for i := 0; i < followers+1; i++ {
		if err := <-errs; !errors.Is(err, boom) {
			t.Fatalf("waiter[%d] err=%v want %v", i, err, boom)
		}
	}
	if got := store.callCountFor(7, 42); got != 1 {
		t.Fatalf("失败 in-flight GetPool calls=%d want 1", got)
	}

	store.setErr(7, 42, nil)
	policy, err := src.GetRoutingPolicy(context.Background(), req)
	if err != nil {
		t.Fatalf("失败后重查不应命中错误缓存: %v", err)
	}
	if policy.FallbackMaxWaiting != 3 || policy.FallbackTimeoutMS != 2500 {
		t.Fatalf("失败后重查 fallback=%d/%d want 3/2500", policy.FallbackMaxWaiting, policy.FallbackTimeoutMS)
	}
	if got := store.callCountFor(7, 42); got != 2 {
		t.Fatalf("失败不应污染缓存,重查 GetPool calls=%d want 2", got)
	}
}
