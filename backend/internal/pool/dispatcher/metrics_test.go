// pasr_dispatch_metrics_test.go — PASR-lite main-wire M2 atomic 单测。
//
// 覆盖矩阵:
//   - eager init: package init() 后 /debug/vars 已含 pasr_dispatch 子树 + 全 0 值
//   - 所有 Inc helper 各 +1 (使用前后 snapshot diff 验证, 不依赖绝对值)
//   - IncDispatchMode 5 mode 各 +1 + unknown 静默丢弃
//   - Snapshot 读全 16 字段
package dispatcher

import (
	"expvar"
	"testing"
)

func TestPASRDispatchMetrics_EagerInit(t *testing.T) {
	// 不调任何 Inc / Snapshot, 直接走 expvar.Get — 验证 init() 已注册 map。
	v := expvar.Get(pasrDispatchMapName)
	if v == nil {
		t.Fatalf("expvar.Get(%q) = nil; eager init 失败 (synthesis B1)", pasrDispatchMapName)
	}
	m, ok := v.(*expvar.Map)
	if !ok {
		t.Fatalf("expvar %q 不是 *expvar.Map (实际 %T)", pasrDispatchMapName, v)
	}
	for _, k := range pasrDispatchKeys() {
		got := m.Get(k)
		if got == nil {
			t.Errorf("key %q 未预填 (启动期 dashboard panel 会缺数据)", k)
			continue
		}
		if iv, ok := got.(*expvar.Int); ok {
			if iv.Value() < 0 {
				t.Errorf("key %q 预填值 %d 应 >= 0", k, iv.Value())
			}
		}
	}
}

func TestPASRDispatchMetrics_IncShadowFamily(t *testing.T) {
	pre := SnapshotPASRDispatchMetrics()

	IncDispatchShadowSampled()
	IncDispatchShadowMatch()
	IncDispatchShadowDiff()
	IncDispatchShadowDrop()
	IncDispatchShadowPanic()
	IncDispatchShadowTimeout()
	IncDispatchShadowPASRErr()

	post := SnapshotPASRDispatchMetrics()
	cases := []struct {
		name string
		diff int64
	}{
		{"ShadowSampled", post.ShadowSampled - pre.ShadowSampled},
		{"ShadowMatch", post.ShadowMatch - pre.ShadowMatch},
		{"ShadowDiff", post.ShadowDiff - pre.ShadowDiff},
		{"ShadowDrop", post.ShadowDrop - pre.ShadowDrop},
		{"ShadowPanic", post.ShadowPanic - pre.ShadowPanic},
		{"ShadowTimeout", post.ShadowTimeout - pre.ShadowTimeout},
		{"ShadowPASRErr", post.ShadowPASRErr - pre.ShadowPASRErr},
	}
	for _, c := range cases {
		if c.diff != 1 {
			t.Errorf("%s diff=%d want 1", c.name, c.diff)
		}
	}
}

func TestPASRDispatchMetrics_IncCanaryFamily(t *testing.T) {
	pre := SnapshotPASRDispatchMetrics()

	IncDispatchCanaryPASRUsed()
	IncDispatchCanaryDefaultUsed()
	IncDispatchCanaryPreMutationFallback()
	IncDispatchCanaryPostMutationRelease()

	post := SnapshotPASRDispatchMetrics()
	cases := []struct {
		name string
		diff int64
	}{
		{"CanaryPASRUsed", post.CanaryPASRUsed - pre.CanaryPASRUsed},
		{"CanaryDefaultUsed", post.CanaryDefaultUsed - pre.CanaryDefaultUsed},
		{"CanaryPreMutationFallback", post.CanaryPreMutationFallback - pre.CanaryPreMutationFallback},
		{"CanaryPostMutationRelease", post.CanaryPostMutationRelease - pre.CanaryPostMutationRelease},
	}
	for _, c := range cases {
		if c.diff != 1 {
			t.Errorf("%s diff=%d want 1", c.name, c.diff)
		}
	}
}

func TestPASRDispatchMetrics_IncDispatchMode_AllValid(t *testing.T) {
	pre := SnapshotPASRDispatchMetrics()

	IncDispatchMode("default")
	IncDispatchMode("shadow")
	IncDispatchMode("canary")
	IncDispatchMode("pasr-primary")
	IncDispatchMode("pasr-strict")

	post := SnapshotPASRDispatchMetrics()
	cases := []struct {
		name string
		diff int64
	}{
		{"ModeDefault", post.ModeDefault - pre.ModeDefault},
		{"ModeShadow", post.ModeShadow - pre.ModeShadow},
		{"ModeCanary", post.ModeCanary - pre.ModeCanary},
		{"ModePASRPrimary", post.ModePASRPrimary - pre.ModePASRPrimary},
		{"ModePASRStrict", post.ModePASRStrict - pre.ModePASRStrict},
	}
	for _, c := range cases {
		if c.diff != 1 {
			t.Errorf("%s diff=%d want 1", c.name, c.diff)
		}
	}
}

func TestPASRDispatchMetrics_IncDispatchMode_UnknownSilent(t *testing.T) {
	pre := SnapshotPASRDispatchMetrics()

	// 非合法 mode 字面量 — Validate 已守门, 此处兜底必须静默不 panic。
	for _, bogus := range []string{"", "off", "PASR", "shadow_5", "default "} {
		IncDispatchMode(bogus)
	}

	post := SnapshotPASRDispatchMetrics()
	if post.ModeDefault != pre.ModeDefault ||
		post.ModeShadow != pre.ModeShadow ||
		post.ModeCanary != pre.ModeCanary ||
		post.ModePASRPrimary != pre.ModePASRPrimary ||
		post.ModePASRStrict != pre.ModePASRStrict {
		t.Errorf("unknown mode 不应改任何 mode_<x>_total: pre=%+v post=%+v", pre, post)
	}
}

func TestPASRDispatchMetrics_VendorEagerInit(t *testing.T) {
	// D follow-up: 4 vendor 启动期 eager init, /debug/vars 立即可见
	v := expvar.Get("pasr_dispatch_by_vendor")
	if v == nil {
		t.Fatalf("pasr_dispatch_by_vendor 未注册")
	}
	m, ok := v.(*expvar.Map)
	if !ok {
		t.Fatalf("pasr_dispatch_by_vendor 非 expvar.Map")
	}
	for _, vendor := range PASRDispatchVendors {
		if m.Get(vendor) == nil {
			t.Errorf("vendor %q sub-map 未 eager 注册", vendor)
		}
	}
}

func TestPASRDispatchMetrics_IncVendor_OnlyKnownVendors(t *testing.T) {
	// 合法 vendor 累计 + 非法 vendor 静默 + 空字符串静默
	for _, vendor := range PASRDispatchVendors {
		pre := SnapshotPASRDispatchVendor(vendor)
		IncDispatchVendor(pasrDispKeyShadowMatch, vendor)
		post := SnapshotPASRDispatchVendor(vendor)
		if post[pasrDispKeyShadowMatch]-pre[pasrDispKeyShadowMatch] != 1 {
			t.Errorf("vendor %s ShadowMatch 应 +1", vendor)
		}
	}
	// 非法 vendor 静默
	preBogus := SnapshotPASRDispatchVendor("bogus")
	IncDispatchVendor(pasrDispKeyShadowMatch, "bogus")
	postBogus := SnapshotPASRDispatchVendor("bogus")
	for k := range preBogus {
		if postBogus[k] != preBogus[k] {
			t.Errorf("bogus vendor 不应改任何 sub-counter, key=%s pre=%d post=%d",
				k, preBogus[k], postBogus[k])
		}
	}
	// 空字符串静默
	IncDispatchVendor(pasrDispKeyShadowMatch, "")
	// 不应 panic — 已通过执行验证
}

func TestPASRDispatchMetrics_KeysCount(t *testing.T) {
	// 防回归 — 加新指标必须同步更新 pasrDispatchKeys + Snapshot 字段。
	want := 16
	if got := len(pasrDispatchKeys()); got != want {
		t.Errorf("pasrDispatchKeys() len=%d want %d (新增指标记得三处同步)", got, want)
	}
}
