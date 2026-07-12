package authcooldown

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// inspect 白盒读取账号条目内部状态(strike/authUntil/hardDisabled),供断言退避算法与去抖行为。
func inspect(s *Store, id int64) (strike int, authUntil time.Time, hard bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[id]
	if e == nil {
		return 0, time.Time{}, false
	}
	return e.strike, e.authUntil, e.hardDisabled
}

func testCfg() Config {
	return Config{Base: 30 * time.Second, Cap: 30 * time.Minute, HardDisableStrikeK: 3}
}

// TestBackoffCappedExponential:封顶指数退避 base<<(strike-1),撞顶后稳定在 cap。
// 判别:若退避退化为定长(如恒 base),strike>=2 的期望值会不匹配;若不封顶,大 strike 溢出。
func TestBackoffCappedExponential(t *testing.T) {
	s := NewStore(testCfg())
	cases := []struct {
		strike int
		want   time.Duration
	}{
		{1, 30 * time.Second},
		{2, 60 * time.Second},
		{3, 120 * time.Second},
		{4, 240 * time.Second},
		{5, 480 * time.Second},
		{6, 960 * time.Second},
		{7, 30 * time.Minute}, // 1920s > 1800s cap → 封顶
		{50, 30 * time.Minute}, // 大 shift 不溢出 → 封顶
	}
	for _, c := range cases {
		if got := s.backoffFor(c.strike); got != c.want {
			t.Fatalf("backoffFor(%d)=%v, 期望 %v", c.strike, got, c.want)
		}
	}
}

// TestSuspendFirstStrikeRemovesFromSelection:首发 auth 失败即把账号移出选号(base 退避)。
func TestSuspendFirstStrikeRemovesFromSelection(t *testing.T) {
	s := NewStore(testCfg())
	now := time.Unix(1_000_000, 0)
	s.Suspend(context.Background(), 7, ClassAmbiguous, 1, now)
	if ok, _ := s.Eligible(7, now); ok {
		t.Fatal("首发 auth 失败后账号应被移出选号(Eligible=false)")
	}
	// 退避窗口结束后应重新合格。
	if ok, _ := s.Eligible(7, now.Add(30*time.Second)); !ok {
		t.Fatal("退避到期后账号应重新合格")
	}
	strike, _, _ := inspect(s, 7)
	if strike != 1 {
		t.Fatalf("首发应 strike=1,实际 %d", strike)
	}
}

// TestSuspendDebounceWithinWindow(修正3 并发去抖核心):同一退避窗口内的多发不升级 strike/TTL。
// 判别:去除去抖 guard 后,窗口内每发都 strike++,本断言必红。
func TestSuspendDebounceWithinWindow(t *testing.T) {
	s := NewStore(testCfg())
	now := time.Unix(1_000_000, 0)
	s.Suspend(context.Background(), 7, ClassAmbiguous, 1, now)
	_, until1, _ := inspect(s, 7)
	// 同窗口内再来 5 发(now 未越过 until1)。
	for i := 0; i < 5; i++ {
		s.Suspend(context.Background(), 7, ClassAmbiguous, 1, now.Add(time.Duration(i)*time.Second))
	}
	strike, until2, _ := inspect(s, 7)
	if strike != 1 {
		t.Fatalf("窗口内去抖失效:strike=%d, 期望 1", strike)
	}
	if !until2.Equal(until1) {
		t.Fatalf("窗口内 AuthUntil 不应被抬高:%v → %v", until1, until2)
	}
}

// TestSuspendEscalatesAcrossWindows:跨越退避窗口的失败按几何升级 strike/TTL。
func TestSuspendEscalatesAcrossWindows(t *testing.T) {
	s := NewStore(testCfg())
	now := time.Unix(1_000_000, 0)
	s.Suspend(context.Background(), 7, ClassAmbiguous, 1, now)
	_, until1, _ := inspect(s, 7)
	// 越过第一个窗口后再失败 → strike=2,退避=60s。
	now2 := until1.Add(time.Millisecond)
	s.Suspend(context.Background(), 7, ClassAmbiguous, 1, now2)
	strike, until2, _ := inspect(s, 7)
	if strike != 2 {
		t.Fatalf("跨窗口应升级:strike=%d, 期望 2", strike)
	}
	if got := until2.Sub(now2); got != 60*time.Second {
		t.Fatalf("第二次退避应为 60s,实际 %v", got)
	}
}

// TestIronCladHardDisableAtStrikeK:iron-clad 类连续失败达 strike 上限 K → HardDisabled。
// 判别:若分级升级失效(iron-clad 不升 hard),Eligible 的 hardDisabled 必为 false,断言红。
func TestIronCladHardDisableAtStrikeK(t *testing.T) {
	s := NewStore(testCfg()) // K=3
	now := time.Unix(1_000_000, 0)
	for i := 0; i < 3; i++ {
		s.Suspend(context.Background(), 7, ClassIronClad, 1, now)
		_, until, _ := inspect(s, 7)
		now = until.Add(time.Millisecond) // 越过窗口触发下一次升级
	}
	ok, hard := s.Eligible(7, now)
	if ok {
		t.Fatal("HardDisabled 账号应不合格")
	}
	if !hard {
		t.Fatal("iron-clad 达 strike K 应升 HardDisabled")
	}
}

// TestAmbiguousNeverHardDisables:ambiguous 通用 401 即使远超 K 次也永不 HardDisabled(自愈)。
// 判别:若把 ambiguous 也纳入硬禁,hard 会变 true,断言红。修 new-api「瞬时 401 误禁好号」。
func TestAmbiguousNeverHardDisables(t *testing.T) {
	s := NewStore(testCfg())
	now := time.Unix(1_000_000, 0)
	for i := 0; i < 10; i++ {
		s.Suspend(context.Background(), 7, ClassAmbiguous, 1, now)
		_, until, hard := inspect(s, 7)
		if hard {
			t.Fatalf("ambiguous 第 %d 次不应 HardDisabled", i+1)
		}
		now = until.Add(time.Millisecond)
	}
	// 撞顶后仍只是软退避:cap 到期即重新合格。
	if ok, hard := s.Eligible(7, now.Add(30*time.Minute)); !ok || hard {
		t.Fatalf("ambiguous 撞顶后到期应重新合格且非硬禁:ok=%v hard=%v", ok, hard)
	}
}

// TestClearResetsEverything:Clear 彻底清除(含 HardDisabled),运营 resume 能救回死号。
// 判别:若 Clear 不清 HardDisabled,resume 后仍不合格,断言红(§17 修正2)。
func TestClearResetsEverything(t *testing.T) {
	s := NewStore(testCfg())
	now := time.Unix(1_000_000, 0)
	for i := 0; i < 3; i++ {
		s.Suspend(context.Background(), 7, ClassIronClad, 1, now)
		_, until, _ := inspect(s, 7)
		now = until.Add(time.Millisecond)
	}
	if _, hard := s.Eligible(7, now); !hard {
		t.Fatal("前置:应已 HardDisabled")
	}
	s.Clear(context.Background(), 7, ClearReasonOperatorResume)
	if ok, hard := s.Eligible(7, now); !ok || hard {
		t.Fatalf("Clear 后应完全恢复:ok=%v hard=%v", ok, hard)
	}
	strike, _, _ := inspect(s, 7)
	if strike != 0 {
		t.Fatalf("Clear 后 strike 应归零,实际 %d", strike)
	}
}

// TestOnRefreshResultSuccessDoesNotClear(审查 S1):刷新「成功」绝不解除冷却/硬禁——
// RefreshHotPath 返回 nil ≠ 真刷新(去抖跳过/storm 拒绝/静态 key 无可刷新都是 nil),
// 把 no-op 当成功会在并发 401 下毫秒级拆冷却、复活硬禁死号。
// 判别:改回 success→Clear → 冷却被解除/硬禁被复活,两个断言都红。
func TestOnRefreshResultSuccessDoesNotClear(t *testing.T) {
	s := NewStore(testCfg())
	now := time.Unix(1_000_000, 0)
	s.Suspend(context.Background(), 7, ClassIronClad, 1, now)
	if ok, _ := s.Eligible(7, now); ok {
		t.Fatal("前置:应被暂停")
	}
	s.OnRefreshResult(context.Background(), 7, true, false)
	if ok, _ := s.Eligible(7, now); ok {
		t.Fatal("刷新 success(可能只是 no-op nil)不得解除冷却")
	}
	// 硬禁死号也不得被假成功复活。
	s.OnRefreshResult(context.Background(), 7, false, true) // 先证实永久失效 → HardDisabled
	s.OnRefreshResult(context.Background(), 7, true, false) // 随后的假成功
	if ok, hard := s.Eligible(7, now.Add(time.Hour)); ok || !hard {
		t.Fatalf("假成功不得复活硬禁死号:ok=%v hard=%v", ok, hard)
	}
	// 真恢复路径仍在:一次成功请求/运营 resume 走 Clear。
	s.Clear(context.Background(), 7, ClearReasonSuccess)
	if ok, hard := s.Eligible(7, now); !ok || hard {
		t.Fatalf("Clear 后应完全恢复:ok=%v hard=%v", ok, hard)
	}
}

// TestOnRefreshResultPermanentHardDisables:热刷新拿到 invalid_grant → 即时 HardDisabled。
// 判别:若不处理 permanentFailure,hard 为 false,断言红。
func TestOnRefreshResultPermanentHardDisables(t *testing.T) {
	s := NewStore(testCfg())
	now := time.Unix(1_000_000, 0)
	s.Suspend(context.Background(), 7, ClassAmbiguous, 1, now) // 先 ambiguous 暂停
	s.OnRefreshResult(context.Background(), 7, false, true)    // 刷新证实永久失效
	if ok, hard := s.Eligible(7, now.Add(time.Hour)); ok || !hard {
		t.Fatalf("刷新证实 invalid_grant 应升 HardDisabled:ok=%v hard=%v", ok, hard)
	}
}

// TestOnRefreshResultTransientKeepsBackoff:transient 刷新失败不动退避,继续走 TTL 自愈(不硬禁)。
func TestOnRefreshResultTransientKeepsBackoff(t *testing.T) {
	s := NewStore(testCfg())
	now := time.Unix(1_000_000, 0)
	s.Suspend(context.Background(), 7, ClassAmbiguous, 1, now)
	_, until1, _ := inspect(s, 7)
	s.OnRefreshResult(context.Background(), 7, false, false)
	_, until2, hard := inspect(s, 7)
	if hard {
		t.Fatal("transient 刷新失败不应 HardDisabled")
	}
	if !until2.Equal(until1) {
		t.Fatalf("transient 刷新失败不应改退避:%v → %v", until1, until2)
	}
	// TTL 到期后仍能自愈。
	if ok, _ := s.Eligible(7, until1.Add(time.Second)); !ok {
		t.Fatal("TTL 到期应自愈")
	}
}

// TestCredentialVersionResetsStrike(修正1 版本感知):凭证轮换(版本变化)→ 重置 strike/hard。
// 判别:若不做版本重置,新凭证会沿用旧号的 strike 直接接近/触发硬禁,断言红。
func TestCredentialVersionResetsStrike(t *testing.T) {
	s := NewStore(testCfg())
	now := time.Unix(1_000_000, 0)
	// v1 连续失败 2 次(接近 K=3)。
	for i := 0; i < 2; i++ {
		s.Suspend(context.Background(), 7, ClassIronClad, 1, now)
		_, until, _ := inspect(s, 7)
		now = until.Add(time.Millisecond)
	}
	if strike, _, _ := inspect(s, 7); strike != 2 {
		t.Fatalf("前置:v1 应 strike=2,实际 %d", strike)
	}
	// 凭证轮换到 v2 后再失败一次 → strike 应从 1 重新起(而非 3 触发硬禁)。
	s.Suspend(context.Background(), 7, ClassIronClad, 2, now)
	strike, _, hard := inspect(s, 7)
	if strike != 1 {
		t.Fatalf("凭证轮换应重置 strike 为 1,实际 %d", strike)
	}
	if hard {
		t.Fatal("轮换后单次失败不应触发 HardDisabled")
	}
}

// TestStaleCredentialVersionDoesNotReset(审查 S3):迟到的「旧版本」事件(长流式在途请求
// 携轮换前 credVersion)不得反向重置新版本已积累的状态。判别:版本比较改回 `!=` →
// 旧版本事件把 strike/HardDisabled 全清,两个断言都红。
func TestStaleCredentialVersionDoesNotReset(t *testing.T) {
	s := NewStore(testCfg())
	now := time.Unix(1_000_000, 0)
	// v2 下连续 iron-clad 失败 3 次 → HardDisabled。
	for i := 0; i < 3; i++ {
		s.Suspend(context.Background(), 7, ClassIronClad, 2, now)
		_, until, _ := inspect(s, 7)
		now = until.Add(time.Millisecond)
	}
	if _, hard := s.Eligible(7, now); !hard {
		t.Fatal("前置:v2 应已 HardDisabled")
	}
	// 迟到的 v1(旧版本)401 事件到达 → 不得重置。
	s.Suspend(context.Background(), 7, ClassIronClad, 1, now)
	strike, _, hard := inspect(s, 7)
	if !hard {
		t.Fatal("迟到旧版本事件不得解除 HardDisabled")
	}
	if strike < 3 {
		t.Fatalf("迟到旧版本事件不得重置 strike:实际 %d", strike)
	}
}

// TestConcurrentBurstDoesNotSaturate(修正3 真实并发压测):N 并发瞬时 401(同一 now)
// 不得把好号打进 cap——去抖保证只升一次 strike。判别:去除去抖 guard 后 strike≈N、退避被抬满,断言红。
func TestConcurrentBurstDoesNotSaturate(t *testing.T) {
	s := NewStore(testCfg())
	now := time.Unix(1_000_000, 0)
	const n = 256
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Suspend(context.Background(), 7, ClassAmbiguous, 1, now)
		}()
	}
	wg.Wait()
	strike, until, hard := inspect(s, 7)
	if strike != 1 {
		t.Fatalf("并发去抖失效:%d 并发 401 把 strike 抬到 %d(期望 1)", n, strike)
	}
	if hard {
		t.Fatal("并发瞬时 401(ambiguous)不应把好号硬禁")
	}
	if want := now.Add(30 * time.Second); !until.Equal(want) {
		t.Fatalf("并发把退避抬满:AuthUntil=%v 期望 base 窗口 %v", until, want)
	}
}

// TestConcurrentSuspendClearRace:并发 Suspend↔Clear 无数据竞争(-race 下跑),最后写入语义明确。
func TestConcurrentSuspendClearRace(t *testing.T) {
	s := NewStore(testCfg())
	now := time.Unix(1_000_000, 0)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); s.Suspend(context.Background(), 9, ClassIronClad, 1, now) }()
		go func() { defer wg.Done(); s.Clear(context.Background(), 9, ClearReasonSuccess) }()
	}
	wg.Wait()
	// 不校验最终态(取决于调度顺序),仅证无 panic/竞争;-race 会抓到未加锁访问。
	_, _ = s.Eligible(9, now)
}

// TestEligibleUnknownAccountAllows:未记录的账号恒合格(接线前默认行为)。
func TestEligibleUnknownAccountAllows(t *testing.T) {
	s := NewStore(testCfg())
	if ok, hard := s.Eligible(42, time.Now()); !ok || hard {
		t.Fatalf("未知账号应恒合格:ok=%v hard=%v", ok, hard)
	}
}

// TestNilStoreSafe:nil Store(车道未接线)所有方法安全 no-op、Eligible 恒放行。
func TestNilStoreSafe(t *testing.T) {
	var s *Store
	s.Suspend(context.Background(), 1, ClassIronClad, 1, time.Now())
	s.Clear(context.Background(), 1, ClearReasonSuccess)
	s.OnRefreshResult(context.Background(), 1, false, true)
	if ok, hard := s.Eligible(1, time.Now()); !ok || hard {
		t.Fatalf("nil Store 应恒放行:ok=%v hard=%v", ok, hard)
	}
}

// TestIsPermanentRefreshError:只有 invalid_grant/撤销/授权过期类判永久,transient 不判。
func TestIsPermanentRefreshError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("oauth token exchange failed: invalid_grant"), true},
		{errors.New("upstream returned token_revoked"), true},
		{errors.New("refresh token revoked by provider"), true},
		{errors.New("authorization expired"), true},
		{errors.New("upstream 503 service unavailable"), false},
		{errors.New("context deadline exceeded"), false},
		{errors.New("rate limit exceeded"), false},
	}
	for _, c := range cases {
		if got := IsPermanentRefreshError(c.err); got != c.want {
			t.Fatalf("IsPermanentRefreshError(%v)=%v, 期望 %v", c.err, got, c.want)
		}
	}
}
