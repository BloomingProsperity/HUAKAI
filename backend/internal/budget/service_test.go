package budget

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServiceRPMDenyIncludesRetryAfter(t *testing.T) {
	// 变异检查:把 limit 比较从 ">" 改成 ">=" 会拒掉第一个请求;删掉 RetryAfter
	// 则留下一个确定的 429,但缺少给客户端的退避指引。
	clock := fixedClock(time.Unix(120, 0).UTC())
	svc := NewService(NewMemoryStore(clock), StaticLimitsProvider{
		Default: LimitPair{RPM: 1},
	}, WithClock(clock))

	ctx := context.Background()
	first, err := svc.Reserve(ctx, reserveFixture(101, 1, 10, 0))
	if err != nil || !first.Allowed {
		t.Fatalf("first reserve allowed=%v err=%v, want allowed", first.Allowed, err)
	}
	second, err := svc.Reserve(ctx, reserveFixture(101, 2, 10, 0))
	if !IsDenied(err) {
		t.Fatalf("second err=%v, want deny", err)
	}
	if second.Decision.Counter != CounterRPM {
		t.Fatalf("counter=%s want rpm", second.Decision.Counter)
	}
	if second.Decision.Current != 1 || second.Decision.Limit != 1 {
		t.Fatalf("current/limit=%d/%d want 1/1", second.Decision.Current, second.Decision.Limit)
	}
	if second.Decision.RetryAfter <= 0 || second.Decision.RetryAfter > time.Minute {
		t.Fatalf("retry_after=%s want within (0,60s]", second.Decision.RetryAfter)
	}
}

func TestServiceTPMAccumulationAndSettlementDelta(t *testing.T) {
	// 变异检查:把 INCRBY 换成 SET 会让第二个请求覆盖第一个并错误地通过;把结算
	// delta 应用到 "now" 而非预留的那一分钟,会让原分钟停在错误的值上。
	clock := fixedClock(time.Unix(180, 0).UTC())
	store := NewMemoryStore(clock)
	svc := NewService(store, StaticLimitsProvider{
		Default: LimitPair{TPM: 900},
	}, WithClock(clock))
	ctx := context.Background()

	if _, err := svc.Reserve(ctx, reserveFixture(202, 1, 20, 500)); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if _, err := svc.Reserve(ctx, reserveFixture(202, 2, 20, 400)); err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	if _, err := svc.Reserve(ctx, reserveFixture(202, 3, 20, 1)); !IsDenied(err) {
		t.Fatalf("third err=%v, want TPM deny at 901 > 900", err)
	}

	if err := svc.Settle(ctx, SettleRequest{TenantID: 202, ClaimID: 1, ActualTokens: 300}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	got := store.CounterValue(scopeUser(202, 20), 3, CounterTPM)
	if got != 700 {
		t.Fatalf("minute 3 TPM=%d want 700 after 500->300 delta", got)
	}
}

func TestServiceAbortRefundIsIdempotent(t *testing.T) {
	// 变异检查:删掉 release 标记会让第二次 abort 把计数器压成负数;删掉退款则
	// 在该分钟留下幽灵 TPM。
	clock := fixedClock(time.Unix(240, 0).UTC())
	store := NewMemoryStore(clock)
	svc := NewService(store, StaticLimitsProvider{
		Default: LimitPair{RPM: 10, TPM: 1000},
	}, WithClock(clock))
	ctx := context.Background()

	if _, err := svc.Reserve(ctx, reserveFixture(303, 44, 30, 650)); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := svc.Release(ctx, ReleaseRequest{TenantID: 303, ClaimID: 44, Reason: "abort"}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := svc.Release(ctx, ReleaseRequest{TenantID: 303, ClaimID: 44, Reason: "abort_replay"}); err != nil {
		t.Fatalf("release replay: %v", err)
	}
	if got := store.CounterValue(scopeUser(303, 30), 4, CounterRPM); got != 0 {
		t.Fatalf("RPM after double release=%d want 0", got)
	}
	if got := store.CounterValue(scopeUser(303, 30), 4, CounterTPM); got != 0 {
		t.Fatalf("TPM after double release=%d want 0", got)
	}
}

func TestServiceWindowUsesStoreClockNotCallerClock(t *testing.T) {
	// 变异检查:从调用方时间派生 key 会把第二次 reserve 放到不同分钟并错误放行,
	// 尽管 store 时间是共享的。
	serverClock := fixedClock(time.Unix(300, 0).UTC())
	svc := NewService(NewMemoryStore(serverClock), StaticLimitsProvider{
		Default: LimitPair{RPM: 1},
	}, WithClock(func() time.Time { return time.Unix(3599, 0).UTC() }))
	ctx := context.Background()

	if _, err := svc.Reserve(ctx, reserveFixture(404, 1, 40, 0)); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	second := reserveFixture(404, 2, 40, 0)
	second.At = time.Unix(3600, 0).UTC()
	if _, err := svc.Reserve(ctx, second); !IsDenied(err) {
		t.Fatalf("second err=%v, want deny because store clock minute did not flip", err)
	}
}

func TestServiceFailModes(t *testing.T) {
	// 变异检查:在 open 模式下把基础设施错误当作确定性拒绝,或在 closed 模式下
	// 当作放行,都会让这些断言翻转。
	ctx := context.Background()
	req := reserveFixture(505, 1, 50, 100)

	openSvc := NewService(&failingStore{}, StaticLimitsProvider{
		Default: LimitPair{RPM: 1, TPM: 1},
	}, WithFailMode(FailModeOpen))
	open, err := openSvc.Reserve(ctx, req)
	if err != nil || !open.Allowed || !open.FailOpen {
		t.Fatalf("open mode result=%+v err=%v, want fail-open allow", open, err)
	}

	closedSvc := NewService(&failingStore{}, StaticLimitsProvider{
		Default: LimitPair{RPM: 1, TPM: 1},
	}, WithFailMode(FailModeClosed))
	closed, err := closedSvc.Reserve(ctx, req)
	if !IsDenied(err) || closed.Allowed || closed.Decision.Code != CodeBudgetUnavailable {
		t.Fatalf("closed mode result=%+v err=%v, want unavailable deny", closed, err)
	}

	mem := NewMemoryStore(fixedClock(time.Unix(60, 0).UTC()))
	fallbackSvc := NewService(&failingStore{}, StaticLimitsProvider{
		Default: LimitPair{RPM: 1},
	}, WithFailMode(FailModeMemoryFallback), WithMemoryFallback(mem))
	if _, err := fallbackSvc.Reserve(ctx, req); err != nil {
		t.Fatalf("memory fallback first reserve: %v", err)
	}
	if _, err := fallbackSvc.Reserve(ctx, reserveFixture(505, 2, 50, 0)); !IsDenied(err) {
		t.Fatalf("memory fallback second err=%v, want single-instance deny", err)
	}
}

func TestServiceClaimIDRetryCountsOnce(t *testing.T) {
	// 变异检查:对每次尝试都计数而不是按逻辑 claim 计数,会让重试消耗 RPM 并
	// 拒掉下一个不同的 claim。
	clock := fixedClock(time.Unix(360, 0).UTC())
	store := NewMemoryStore(clock)
	svc := NewService(store, StaticLimitsProvider{
		Default: LimitPair{RPM: 2, TPM: 1000},
	}, WithClock(clock))
	ctx := context.Background()

	req := reserveFixture(606, 88, 60, 100)
	if _, err := svc.Reserve(ctx, req); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if replay, err := svc.Reserve(ctx, req); err != nil || !replay.IdempotencyHit {
		t.Fatalf("replay result=%+v err=%v, want idempotency hit", replay, err)
	}
	if got := store.CounterValue(scopeUser(606, 60), 6, CounterRPM); got != 1 {
		t.Fatalf("RPM after retry=%d want 1", got)
	}
	if _, err := svc.Reserve(ctx, reserveFixture(606, 89, 60, 100)); err != nil {
		t.Fatalf("second distinct claim should fit remaining RPM: %v", err)
	}
}

func TestServiceSettlementCanRunOnDifferentInstance(t *testing.T) {
	// 变异检查:把预留元数据只保存在 service 对象上,会让第二个实例无法应用
	// actual-token delta。
	clock := fixedClock(time.Unix(390, 0).UTC())
	store := NewMemoryStore(clock)
	limits := StaticLimitsProvider{Default: LimitPair{TPM: 1000}}
	reserver := NewService(store, limits, WithClock(clock))
	settler := NewService(store, limits, WithClock(clock))
	ctx := context.Background()

	if _, err := reserver.Reserve(ctx, reserveFixture(660, 99, 66, 500)); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := settler.Settle(ctx, SettleRequest{TenantID: 660, ClaimID: 99, ActualTokens: 300}); err != nil {
		t.Fatalf("cross-instance settle: %v", err)
	}
	if got := store.CounterValue(scopeUser(660, 66), 6, CounterTPM); got != 300 {
		t.Fatalf("TPM after cross-instance settle=%d want 300", got)
	}
}

func TestServiceUserHardCapAppliesWhenKeyUnlimited(t *testing.T) {
	// 变异检查:在无限制的 key 作用域处就停下的 else 链会绕过用户硬上限并
	// 放行第二个请求。
	clock := fixedClock(time.Unix(420, 0).UTC())
	svc := NewService(NewMemoryStore(clock), StaticLimitsProvider{
		Default: LimitPair{},
		Keys: map[int64]LimitSpec{
			700: {LimitPair: LimitPair{}},
		},
		Users: map[int64]LimitSpec{
			70: {LimitPair: LimitPair{RPM: 1}},
		},
	}, WithClock(clock))
	ctx := context.Background()

	if _, err := svc.Reserve(ctx, ReserveRequest{TenantID: 707, ClaimID: 1, UserID: 70, APIKeyID: 700}); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if _, err := svc.Reserve(ctx, ReserveRequest{TenantID: 707, ClaimID: 2, UserID: 70, APIKeyID: 700}); !IsDenied(err) {
		t.Fatalf("second err=%v, want user hard cap deny despite unlimited key", err)
	}
}

func TestServiceAllOrNothingRefundsEarlierScopes(t *testing.T) {
	// 变异检查:移除补偿性退款会让 key 作用域在分组拒绝后停在 +1,造成幽灵
	// 预算扣减。
	clock := fixedClock(time.Unix(480, 0).UTC())
	store := NewMemoryStore(clock)
	svc := NewService(store, StaticLimitsProvider{
		Default: LimitPair{},
		Keys: map[int64]LimitSpec{
			800: {LimitPair: LimitPair{RPM: 10}},
		},
		Users: map[int64]LimitSpec{
			80: {LimitPair: LimitPair{RPM: 10}},
		},
		PoolGroups: map[int64]LimitSpec{
			9: {LimitPair: LimitPair{RPM: 1}},
		},
	}, WithClock(clock))
	ctx := context.Background()

	if _, err := svc.Reserve(ctx, ReserveRequest{TenantID: 808, ClaimID: 1, UserID: 80, APIKeyID: 800, PoolGroupID: 9}); err != nil {
		t.Fatalf("warmup consuming group limit: %v", err)
	}
	if _, err := svc.Reserve(ctx, ReserveRequest{TenantID: 808, ClaimID: 2, UserID: 80, APIKeyID: 800, PoolGroupID: 9}); !IsDenied(err) {
		t.Fatalf("second err=%v, want group deny", err)
	}
	if got := store.CounterValue(scopeKey(808, 800), 8, CounterRPM); got != 1 {
		t.Fatalf("key RPM after denied second claim=%d want warmup-only 1", got)
	}
}

func TestScopeEncodingRejectsInjectionAndKeepsHashTag(t *testing.T) {
	// 变异检查:直接拼接原始 ID / model 字符串会让 ':'/'{}'/换行符 改变 Redis
	// key 形状或集群 hash-tag。
	scope := Scope{TenantID: 909, Kind: ScopeAPIKey, ID: "12:{evil}\n", Model: "gpt:{4}\nmini"}
	encoded, err := EncodeScope(scope)
	if err != nil {
		t.Fatalf("EncodeScope: %v", err)
	}
	if strings.ContainsAny(encoded, "{}\n") {
		t.Fatalf("encoded scope %q contains forbidden key-shaping characters", encoded)
	}
	key := RedisCounterKey(scope, CounterRPM, 15)
	if strings.Count(key, "{") != 1 || strings.Count(key, "}") != 1 {
		t.Fatalf("redis key %q does not have exactly one hash tag pair", key)
	}
	if !strings.Contains(key, "{"+encoded+"}") {
		t.Fatalf("redis key %q missing encoded hash tag %q", key, encoded)
	}
}

func TestMemoryStoreConcurrentLimitIsExact(t *testing.T) {
	// 变异检查:把 store 锁换成 GET+INCR 风格的逻辑在 100 goroutine 竞态下会
	// 放行超过 50 个。
	clock := fixedClock(time.Unix(540, 0).UTC())
	svc := NewService(NewMemoryStore(clock), StaticLimitsProvider{
		Default: LimitPair{RPM: 50},
	}, WithClock(clock))
	ctx := context.Background()

	var wg sync.WaitGroup
	results := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := svc.Reserve(ctx, reserveFixture(1001, int64(i+1), 100, 0))
			results <- err == nil && res.Allowed
		}(i)
	}
	wg.Wait()
	close(results)
	allowed := 0
	for ok := range results {
		if ok {
			allowed++
		}
	}
	if allowed != 50 {
		t.Fatalf("allowed=%d want exactly 50", allowed)
	}
}

func reserveFixture(tenantID, claimID, userID, tokens int64) ReserveRequest {
	return ReserveRequest{
		TenantID:       tenantID,
		ClaimID:        claimID,
		UserID:         userID,
		ReservedTokens: tokens,
	}
}

func scopeUser(tenantID, userID int64) Scope {
	return Scope{TenantID: tenantID, Kind: ScopeUser, ID: intString(userID)}
}

func scopeKey(tenantID, keyID int64) Scope {
	return Scope{TenantID: tenantID, Kind: ScopeAPIKey, ID: intString(keyID)}
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

type failingStore struct{}

func (s *failingStore) CheckAndIncrement(context.Context, CounterRequest) (CounterResult, error) {
	return CounterResult{}, errors.New("redis down")
}

func (s *failingStore) Adjust(context.Context, AdjustRequest) error {
	return errors.New("redis down")
}
