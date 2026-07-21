// selector_dispatcher_test.go — PASR-lite main-wire M4 atomic 单测。
//
// 覆盖矩阵 (M4 范围, M7 再扩 22 测):
//
//	T-M4-1  default mode 直走 DefaultSelector, 不调 PASR
//	T-M4-2  shadow mode 主路径走 default, 命中桶 → 异步 shadow 比对入队
//	T-M4-3  shadow async match (PASR 选同一 account) → ShadowMatch++
//	T-M4-4  shadow async diff (PASR 选不同 account) → ShadowDiff++
//	T-M4-5  shadow drop (queue 满) → ShadowDrop++ + 主路径不 block
//	T-M4-6  shadow PASR Select panic → ShadowPanic++ + 主路径未损
//	T-M4-7  shadow PASR Select 超时 → ShadowTimeout++
//	T-M4-8  canary 命中桶 → PASR actual, 失败 pre-mutation → fallback default
//	T-M4-9  canary post-mutation fail → fail closed (不 fallback)
//	T-M4-10 canary miss 桶 → DefaultSelector
//	T-M4-11 pasr-primary 在 pre-mutation 阶段失败 → fallback 到 default
//	T-M4-12 pasr-strict 任何错误 → fail closed (不 fallback)
//	T-M4-13 shouldSample deterministic (同 SessionHash 永远落同侧)
//	T-M4-14 shouldSample 分布在 5/25/100 接近期望
//	T-M4-15 shadow ClaimID=0 抹掉 (三层防御之三)
//	T-M4-16 NewSelectorDispatcher misconfigure 拒绝
package dispatcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubSelector 简单 Selector 实现, 返预设 result/err, 计数 Select 调用次数。
type stubSelector struct {
	result    *SelectionResult
	err       error
	calls     atomic.Int64
	delay     time.Duration
	gotClaims atomic.Int64 // ClaimID != 0 的请求计数
	panic     bool         // true 时直接 panic
}

func (s *stubSelector) Select(ctx context.Context, req SelectionRequest) (*SelectionResult, error) {
	s.calls.Add(1)
	if req.ClaimID != 0 {
		s.gotClaims.Add(1)
	}
	if s.panic {
		panic("simulated panic in stub Select")
	}
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	if s.result == nil {
		return nil, ErrNoEligibleAccount
	}
	return s.result, nil
}

func newStubResult(accountID int64) *SelectionResult {
	return &SelectionResult{AccountID: accountID, AcquisitionToken: uuid.New()}
}

// newDispatcher 构造 dispatcher 实例 + 默认 sub-selector stubs。
func newDispatcher(t *testing.T, mode string, shadowPct, canaryPct int, defaultRes int64, pasrRes int64) (*SelectorDispatcher, *stubSelector, *stubSelector, *stubSelector) {
	t.Helper()
	def := &stubSelector{result: newStubResult(defaultRes)}
	pasr := &stubSelector{result: newStubResult(pasrRes)}
	shadow := &stubSelector{result: newStubResult(pasrRes)}
	cfg := SelectorDispatcherConfig{
		Mode:          mode,
		ShadowPercent: shadowPct,
		CanaryPercent: canaryPct,
		SamplingSalt:  "test-salt",
		Default:       def,
		PASR:          pasr,
		Shadow:        shadow,
	}
	d, err := NewSelectorDispatcher(cfg)
	if err != nil {
		t.Fatalf("NewSelectorDispatcher: %v", err)
	}
	t.Cleanup(d.Stop)
	return d, def, pasr, shadow
}

func TestDispatcher_DefaultMode_OnlyCallsDefault(t *testing.T) {
	d, def, pasr, shadow := newDispatcher(t, DispatchModeDefault, 0, 0, 11, 22)

	req := SelectionRequest{TenantID: 1, ClaimID: 1, SessionHash: "x"}
	res, err := d.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.AccountID != 11 {
		t.Errorf("res.AccountID=%d want 11 (default)", res.AccountID)
	}
	if def.calls.Load() != 1 {
		t.Errorf("default calls=%d want 1", def.calls.Load())
	}
	if pasr.calls.Load() != 0 {
		t.Errorf("default mode 不应调 pasr, 实际 %d", pasr.calls.Load())
	}
	if shadow.calls.Load() != 0 {
		t.Errorf("default mode 不应调 shadow, 实际 %d", shadow.calls.Load())
	}
}

func TestDispatcher_ShadowMode_AsyncCompare_MatchAndDiff(t *testing.T) {
	// 100% shadow, default + shadow 选同 account → match; 改 shadow result 看 diff
	d, _, _, shadow := newDispatcher(t, DispatchModeShadow, 100, 0, 11, 11)

	preMatch := SnapshotPASRDispatchMetrics().ShadowMatch
	req := SelectionRequest{TenantID: 1, ClaimID: 1, SessionHash: "shadow-match"}
	res, err := d.Select(context.Background(), req)
	if err != nil || res.AccountID != 11 {
		t.Fatalf("default 主路径应返 11, 实际 res=%+v err=%v", res, err)
	}
	// shadow async — 等到 shadow worker 处理完再断言
	waitFor(t, 2*time.Second, func() bool {
		return SnapshotPASRDispatchMetrics().ShadowMatch > preMatch
	}, "ShadowMatch 应增加 1")

	// 切换 shadow 实例返不同 account → diff 路径
	preDiff := SnapshotPASRDispatchMetrics().ShadowDiff
	shadow.result = newStubResult(99)
	_, err = d.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 2, SessionHash: "shadow-diff"})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return SnapshotPASRDispatchMetrics().ShadowDiff > preDiff
	}, "ShadowDiff 应增加 1")
}

func TestDispatcher_ShadowMode_DropWhenQueueFull(t *testing.T) {
	t.Setenv(dispatcherDrainTimeoutEnv, "0.1")
	var logBuf bytes.Buffer
	origLogWriter := log.Writer()
	origLogFlags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(origLogWriter)
		log.SetFlags(origLogFlags)
	})

	// shadow stub 加 100ms delay 让 queue 容易堆满
	def := &stubSelector{result: newStubResult(11)}
	pasr := &stubSelector{result: newStubResult(22)}
	shadow := &stubSelector{result: newStubResult(11), delay: 100 * time.Millisecond}
	d, err := NewSelectorDispatcher(SelectorDispatcherConfig{
		Mode: DispatchModeShadow, ShadowPercent: 100, SamplingSalt: "drop-test",
		Default: def, PASR: pasr, Shadow: shadow,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	t.Cleanup(d.Stop)

	preDrop := SnapshotPASRDispatchMetrics().ShadowDrop
	// 灌满 queue (1024) + 多发避免 race 窗口
	for i := 0; i < shadowQueueCap+200; i++ {
		_, _ = d.Select(context.Background(), SelectionRequest{
			TenantID: 1, ClaimID: int64(i + 1), SessionHash: "drop",
		})
	}
	postDrop := SnapshotPASRDispatchMetrics().ShadowDrop
	if postDrop-preDrop == 0 {
		t.Errorf("queue 应满后 drop, 实际 ShadowDrop 增量 0")
	}

	stopStart := time.Now()
	d.Stop()
	if elapsed := time.Since(stopStart); elapsed > time.Second {
		t.Fatalf("Stop 超过 drain timeout 后仍阻塞: elapsed=%s", elapsed)
	}
	if got := logBuf.String(); !strings.Contains(got, "reason_class=shadow_drain_timeout") || !strings.Contains(got, "dropped_count=") {
		t.Fatalf("Stop timeout 应记录 reason_class=shadow_drain_timeout dropped_count=N warning, 实际 log=%q", got)
	}
}

func TestDispatcher_ShadowMode_PanicRecovered(t *testing.T) {
	def := &stubSelector{result: newStubResult(11)}
	pasr := &stubSelector{result: newStubResult(22)}
	shadow := &stubSelector{panic: true}
	d, err := NewSelectorDispatcher(SelectorDispatcherConfig{
		Mode: DispatchModeShadow, ShadowPercent: 100, SamplingSalt: "panic-test",
		Default: def, PASR: pasr, Shadow: shadow,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer d.Stop()

	prePanic := SnapshotPASRDispatchMetrics().ShadowPanic
	res, err := d.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 1, SessionHash: "panic"})
	if err != nil {
		t.Fatalf("主路径不应受 shadow panic 影响, 实际 err=%v", err)
	}
	if res.AccountID != 11 {
		t.Errorf("主路径应返 default account 11")
	}
	waitFor(t, 2*time.Second, func() bool {
		return SnapshotPASRDispatchMetrics().ShadowPanic > prePanic
	}, "ShadowPanic 应+1")
}

func TestDispatcher_ShadowMode_TimeoutCounted(t *testing.T) {
	def := &stubSelector{result: newStubResult(11)}
	pasr := &stubSelector{result: newStubResult(22)}
	// shadow stub 拖比 shadowSelectTimeout 长一倍, ctx 超时
	shadow := &stubSelector{result: newStubResult(11), delay: shadowSelectTimeout * 2}
	d, err := NewSelectorDispatcher(SelectorDispatcherConfig{
		Mode: DispatchModeShadow, ShadowPercent: 100, SamplingSalt: "timeout",
		Default: def, PASR: pasr, Shadow: shadow,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer d.Stop()

	preTimeout := SnapshotPASRDispatchMetrics().ShadowTimeout
	_, _ = d.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 1, SessionHash: "to"})
	waitFor(t, 3*time.Second, func() bool {
		return SnapshotPASRDispatchMetrics().ShadowTimeout > preTimeout
	}, "ShadowTimeout 应+1")
}

func TestDispatcher_ShadowMode_StripsClaimID(t *testing.T) {
	// shadow goroutine 调 PASR 时 ClaimID 必须为 0 (三层防御之三)
	def := &stubSelector{result: newStubResult(11)}
	shadow := &stubSelector{result: newStubResult(11)}
	d, err := NewSelectorDispatcher(SelectorDispatcherConfig{
		Mode: DispatchModeShadow, ShadowPercent: 100, SamplingSalt: "strip",
		Default: def, Shadow: shadow,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer d.Stop()

	_, _ = d.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 555, SessionHash: "strip"})
	waitFor(t, 2*time.Second, func() bool {
		return shadow.calls.Load() > 0
	}, "shadow 应被调")
	if shadow.gotClaims.Load() != 0 {
		t.Errorf("shadow 收到的 ClaimID 应被抹为 0, 实际 gotClaims=%d", shadow.gotClaims.Load())
	}
}

func TestDispatcher_CanaryMode_PreMutationFail_FallbackDefault(t *testing.T) {
	def := &stubSelector{result: newStubResult(11)}
	pasr := &stubSelector{err: ErrPASRPreMutationFail}
	d, err := NewSelectorDispatcher(SelectorDispatcherConfig{
		Mode: DispatchModeCanary, CanaryPercent: 100, SamplingSalt: "canary",
		Default: def, PASR: pasr,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer d.Stop()

	preFB := SnapshotPASRDispatchMetrics().CanaryPreMutationFallback
	res, err := d.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 1, SessionHash: "c"})
	if err != nil {
		t.Fatalf("pre-mutation fail 应 fallback default, 实际 err=%v", err)
	}
	if res.AccountID != 11 {
		t.Errorf("应返 default 11, 实际 %d", res.AccountID)
	}
	if pasr.calls.Load() != 1 || def.calls.Load() != 1 {
		t.Errorf("pasr+default 各应调一次, 实际 pasr=%d def=%d", pasr.calls.Load(), def.calls.Load())
	}
	if SnapshotPASRDispatchMetrics().CanaryPreMutationFallback-preFB != 1 {
		t.Errorf("CanaryPreMutationFallback 应+1")
	}
}

func TestDispatcher_CanaryMode_PostMutationFail_FailClosed(t *testing.T) {
	def := &stubSelector{result: newStubResult(11)}
	pasr := &stubSelector{err: ErrPASRPostMutationFail}
	d, err := NewSelectorDispatcher(SelectorDispatcherConfig{
		Mode: DispatchModeCanary, CanaryPercent: 100, SamplingSalt: "post",
		Default: def, PASR: pasr,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer d.Stop()

	prePost := SnapshotPASRDispatchMetrics().CanaryPostMutationRelease
	_, err = d.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 1, SessionHash: "p"})
	if err == nil {
		t.Fatalf("post-mutation fail 应 fail closed")
	}
	if !errors.Is(err, ErrPASRPostMutationFail) {
		t.Errorf("err=%v want wraps ErrPASRPostMutationFail", err)
	}
	if def.calls.Load() != 0 {
		t.Errorf("post-mutation fail 后 default 不应被调, 实际 %d", def.calls.Load())
	}
	if SnapshotPASRDispatchMetrics().CanaryPostMutationRelease-prePost != 1 {
		t.Errorf("CanaryPostMutationRelease 应+1")
	}
}

func TestDispatcher_CanaryMode_GroupPolicyUnavailableDoesNotFallbackDefault(t *testing.T) {
	def := &stubSelector{result: newStubResult(11)}
	pasr := &stubSelector{err: fmt.Errorf("策略读取失败: %w", ErrGroupPolicyUnavailable)}
	d, err := NewSelectorDispatcher(SelectorDispatcherConfig{
		Mode: DispatchModeCanary, CanaryPercent: 100, SamplingSalt: "group-policy",
		Default: def, PASR: pasr,
	})
	if err != nil {
		t.Fatalf("NewSelectorDispatcher: %v", err)
	}
	defer d.Stop()

	res, err := d.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 1, SessionHash: "policy"})
	if res != nil || !errors.Is(err, ErrGroupPolicyUnavailable) {
		t.Fatalf("res=%+v err=%v want nil+ErrGroupPolicyUnavailable", res, err)
	}
	if pasr.calls.Load() != 1 || def.calls.Load() != 0 {
		t.Fatalf("策略失败不得换选号器: pasr=%d default=%d", pasr.calls.Load(), def.calls.Load())
	}
}

func TestDispatcher_CanaryMode_MissBucket_GoesDefault(t *testing.T) {
	d, def, pasr, _ := newDispatcher(t, DispatchModeCanary, 0, 0, 11, 22)

	preDef := SnapshotPASRDispatchMetrics().CanaryDefaultUsed
	res, err := d.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 1, SessionHash: "miss"})
	if err != nil || res.AccountID != 11 {
		t.Fatalf("canary 0%% 应全走 default, 实际 res=%+v err=%v", res, err)
	}
	if pasr.calls.Load() != 0 {
		t.Errorf("0%% canary 不应调 pasr")
	}
	if def.calls.Load() != 1 {
		t.Errorf("default 应调一次")
	}
	if SnapshotPASRDispatchMetrics().CanaryDefaultUsed-preDef != 1 {
		t.Errorf("CanaryDefaultUsed 应+1")
	}
}

func TestDispatcher_PASRPrimary_PreMutationFail_FallbackDefault(t *testing.T) {
	def := &stubSelector{result: newStubResult(11)}
	pasr := &stubSelector{err: ErrPASRPreMutationFail}
	d, err := NewSelectorDispatcher(SelectorDispatcherConfig{
		Mode: DispatchModePASRPrimary, SamplingSalt: "primary",
		Default: def, PASR: pasr,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer d.Stop()

	res, err := d.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 1, SessionHash: "x"})
	if err != nil {
		t.Fatalf("primary pre-mutation fail 应 fallback default, 实际 err=%v", err)
	}
	if res.AccountID != 11 {
		t.Errorf("应返 default 11")
	}
}

func TestDispatcher_PASRStrict_PreMutationFail_FailClosed(t *testing.T) {
	// strict 模式 pre-mutation 也不 fallback (验收终态)
	def := &stubSelector{result: newStubResult(11)}
	pasr := &stubSelector{err: ErrPASRPreMutationFail}
	d, err := NewSelectorDispatcher(SelectorDispatcherConfig{
		Mode: DispatchModePASRStrict, SamplingSalt: "strict",
		Default: def, PASR: pasr,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer d.Stop()

	_, err = d.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 1, SessionHash: "x"})
	if err == nil {
		t.Fatalf("strict 应 fail closed, 不允许 fallback default")
	}
	if !errors.Is(err, ErrPASRPreMutationFail) {
		t.Errorf("err=%v want wraps ErrPASRPreMutationFail", err)
	}
	if def.calls.Load() != 0 {
		t.Errorf("strict 不应 fallback default, 实际 def=%d", def.calls.Load())
	}
}

func TestDispatcher_ShouldSample_DeterministicSameHash(t *testing.T) {
	d := &SelectorDispatcher{samplingSalt: "det"}
	hash := "session-stable-1"
	first := d.shouldSample(hash, 50)
	for i := 0; i < 1000; i++ {
		if d.shouldSample(hash, 50) != first {
			t.Fatalf("同 SessionHash 应永远落同侧")
		}
	}
}

func TestDispatcher_ShouldSample_DistributionWithin1Percent(t *testing.T) {
	d := &SelectorDispatcher{samplingSalt: "dist"}
	const N = 20000
	for _, want := range []int{5, 25, 50, 75} {
		hits := 0
		for i := 0; i < N; i++ {
			h := uuid.New().String()
			if d.shouldSample(h, want) {
				hits++
			}
		}
		got := float64(hits) / float64(N) * 100.0
		if got < float64(want)-1.5 || got > float64(want)+1.5 {
			t.Errorf("pct=%d 实际命中率=%.2f%% 偏差超过 ±1.5%%", want, got)
		}
	}
}

func TestDispatcher_NewSelectorDispatcher_Misconfigure(t *testing.T) {
	def := &stubSelector{result: newStubResult(11)}
	cases := []struct {
		name string
		cfg  SelectorDispatcherConfig
		want string
	}{
		{
			name: "nil Default",
			cfg:  SelectorDispatcherConfig{Mode: DispatchModeDefault},
			want: "Default selector",
		},
		{
			name: "shadow without Shadow",
			cfg:  SelectorDispatcherConfig{Mode: DispatchModeShadow, Default: def},
			want: "Shadow selector",
		},
		{
			name: "canary without PASR",
			cfg:  SelectorDispatcherConfig{Mode: DispatchModeCanary, Default: def},
			want: "PASR selector",
		},
		{
			name: "invalid mode",
			cfg:  SelectorDispatcherConfig{Mode: "bogus", Default: def},
			want: "invalid mode",
		},
		{
			name: "shadow_pct 越界",
			cfg:  SelectorDispatcherConfig{Mode: DispatchModeDefault, ShadowPercent: 200, Default: def},
			want: "ShadowPercent",
		},
	}
	for _, tc := range cases {
		_, err := NewSelectorDispatcher(tc.cfg)
		if err == nil {
			t.Errorf("[%s] err=nil want %q", tc.name, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("[%s] err=%q want contains %q", tc.name, err.Error(), tc.want)
		}
	}
}

func TestDispatcher_VendorMetric_WiredFromRequest(t *testing.T) {
	// D2: dispatcher.Select 时按 req.Vendor 切片记录 mode 命中。
	// dispatcher 内部调 IncDispatchVendorMode(d.mode, req.Vendor) 之后,
	// SnapshotPASRDispatchVendor(vendor)[mode_<x>_total] 应 +1。
	d, _, _, _ := newDispatcher(t, DispatchModeDefault, 0, 0, 11, 22)

	for _, vendor := range []string{"anthropic", "openai", "gemini", "codex"} {
		pre := SnapshotPASRDispatchVendor(vendor)
		req := SelectionRequest{TenantID: 1, ClaimID: 1, SessionHash: "v-" + vendor, Vendor: vendor}
		_, err := d.Select(context.Background(), req)
		if err != nil {
			t.Fatalf("[%s] Select err=%v", vendor, err)
		}
		post := SnapshotPASRDispatchVendor(vendor)
		got := post[pasrDispKeyModeDefault] - pre[pasrDispKeyModeDefault]
		if got != 1 {
			t.Errorf("[%s] vendor sub-counter mode_default_total 应 +1, 实增 %d", vendor, got)
		}
	}

	// vendor="" → dispatcher 仍记全局 mode_default_total, 但不记 vendor 切片
	preBogus := SnapshotPASRDispatchVendor("anthropic")
	_, _ = d.Select(context.Background(), SelectionRequest{TenantID: 1, ClaimID: 99, SessionHash: "novendor"})
	postBogus := SnapshotPASRDispatchVendor("anthropic")
	if postBogus[pasrDispKeyModeDefault] != preBogus[pasrDispKeyModeDefault] {
		t.Errorf("vendor 空 不应改 anthropic sub-counter")
	}
}

// waitFor 轮询直到 cond 返回 true 或超时,随后记录失败。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waitFor 超时 (%v): %s", timeout, msg)
}
