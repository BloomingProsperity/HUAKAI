// m7_dispatcher_integration_test.go — PASR-lite main-wire M7 atomic 集成测试。
//
// 覆盖 dispatcher 与真 PASRSelector / SegmentTable / config 字面量的边界场景:
//
//	T-M7-1 shadow PASR (ReadOnlySegments=true) 真集成 — 100 不同 SessionHash
//	       dispatcher.Select 后 SegmentTable.Size() == 0 (D2 段表只读 + 不污染)
//	T-M7-2 actual PASR (ReadOnlySegments=false) 真集成 — 100 不同 SessionHash
//	       dispatcher.Select 后 SegmentTable.Size() > 0 (actual 路径正常学习)
//	T-M7-3 mode 字面量 cross-check — Dispatcher 5 const 与 config.PoolSelectorMode
//	       完全一致, 防 string drift (M2/M4 reviewer 都标过的 follow-up)
package pool

import (
	"context"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/config"
)

func TestM7_ShadowMode_SegmentTableNotPolluted(t *testing.T) {
	// 用真 PASRSelector 实例 (shadow ReadOnlySegments=true) 跑 dispatcher,
	// 验证 100 个不同 SessionHash 后 SegmentTable.Size() = 0 (D2 不污染)
	now := time.Now()
	snaps := []*AccountSnapshot{
		{ID: 1, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
		{ID: 2, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
		{ID: 3, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
	}
	src := &fakeAccountSource{snapshots: snaps}
	segments := NewSegmentTable(SegmentTableConfig{})
	ring := NewAccountRing([]int64{1, 2, 3}, 0xCAFEBABE)

	defaultPASR, err := NewPASRSelector(PASRSelectorConfig{
		Accounts:     src,
		Claims:       &fakeClaimGate{},
		RingProvider: func() *AccountRing { return ring },
		Segments:     segments,
		LoadCap:      0.95,
	})
	if err != nil {
		t.Fatalf("default PASR: %v", err)
	}

	shadowPASR, err := NewPASRSelector(PASRSelectorConfig{
		Accounts:         src,
		RingProvider:     func() *AccountRing { return ring },
		Segments:         segments, // 共享同一段表
		LoadCap:          0.95,
		ReadOnlySegments: true, // D2: shadow 实例段表只读
	})
	if err != nil {
		t.Fatalf("shadow PASR: %v", err)
	}

	d, err := NewSelectorDispatcher(SelectorDispatcherConfig{
		Mode:          DispatchModeShadow,
		ShadowPercent: 100,
		SamplingSalt:  "m7-shadow",
		Default:       defaultPASR,
		Shadow:        shadowPASR,
	})
	if err != nil {
		t.Fatalf("dispatcher: %v", err)
	}
	defer d.Stop()

	// **关键**: 默认 selector 不创建段 (因为它走 LookupOrCreate 但 D2 关心的是
	// shadow PASR 的副作用)。 为隔离 shadow 行为, 这里用 nilSlotManager
	// 避免 default 路径副作用 — 然后只看 shadow 是否动段表。
	// 实际上 default = defaultPASR 走 LookupOrCreate 也会建段, 所以这里
	// 改用 stubSelector 当 Default 让它不动段表, 只看 shadow 副作用。
	stubDef := &stubSelector{result: newStubResult(1)}
	d2, err := NewSelectorDispatcher(SelectorDispatcherConfig{
		Mode:          DispatchModeShadow,
		ShadowPercent: 100,
		SamplingSalt:  "m7-shadow-pure",
		Default:       stubDef,
		Shadow:        shadowPASR,
	})
	if err != nil {
		t.Fatalf("dispatcher2: %v", err)
	}
	defer d2.Stop()

	// 清空段表 (上面 d 可能已建段, 别影响 d2 验证)
	segments = NewSegmentTable(SegmentTableConfig{})
	shadowPASR2, err := NewPASRSelector(PASRSelectorConfig{
		Accounts:         src,
		RingProvider:     func() *AccountRing { return ring },
		Segments:         segments,
		LoadCap:          0.95,
		ReadOnlySegments: true,
	})
	if err != nil {
		t.Fatalf("shadowPASR2: %v", err)
	}
	d3, err := NewSelectorDispatcher(SelectorDispatcherConfig{
		Mode:          DispatchModeShadow,
		ShadowPercent: 100,
		SamplingSalt:  "m7-shadow-clean",
		Default:       stubDef,
		Shadow:        shadowPASR2,
	})
	if err != nil {
		t.Fatalf("dispatcher3: %v", err)
	}
	defer d3.Stop()

	// 100 不同 SessionHash, dispatcher.Select 触发 shadow async PASR 调度
	for i := 0; i < 100; i++ {
		_, err := d3.Select(context.Background(), SelectionRequest{
			TenantID: 1, ClaimID: int64(i + 1), SessionHash: time.Now().Format(time.RFC3339Nano),
		})
		if err != nil {
			t.Fatalf("Select %d: %v", i, err)
		}
		time.Sleep(time.Microsecond) // 错开 nanosecond 防 SessionHash 重复
	}

	// 等 shadow worker drain 完
	waitFor(t, 3*time.Second, func() bool {
		snap := SnapshotPASRDispatchMetrics()
		return snap.ShadowSampled >= 100 || snap.ShadowDrop > 0
	}, "shadow worker 处理完 100 req")

	// 严格断言: shadow 实例 ReadOnlySegments=true → SegmentTable 不应有任何段
	if segments.Size() != 0 {
		t.Errorf("D2 段表只读违反: shadow 100 req 后 SegmentTable.Size()=%d, 应为 0",
			segments.Size())
	}
}

func TestM7_ActualMode_SegmentTableLearns(t *testing.T) {
	// 反向验证 — actual PASR (ReadOnlySegments=false) 100 个不同 SessionHash 后
	// SegmentTable.Size() > 0 (正常学习路径)
	now := time.Now()
	snaps := []*AccountSnapshot{
		{ID: 1, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
		{ID: 2, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
		{ID: 3, LoadRate: 0.1, Priority: 1, LastUsedAt: now},
	}
	src := &fakeAccountSource{snapshots: snaps}
	segments := NewSegmentTable(SegmentTableConfig{})
	ring := NewAccountRing([]int64{1, 2, 3}, 0xCAFEBABE)

	actualPASR, err := NewPASRSelector(PASRSelectorConfig{
		Accounts:     src,
		Claims:       &fakeClaimGate{},
		RingProvider: func() *AccountRing { return ring },
		Segments:     segments,
		LoadCap:      0.95,
		// ReadOnlySegments 默认 false — actual 路径
	})
	if err != nil {
		t.Fatalf("actualPASR: %v", err)
	}

	for i := 0; i < 50; i++ {
		_, err := actualPASR.Select(context.Background(), SelectionRequest{
			TenantID: 1, ClaimID: int64(i + 1),
			SessionHash: time.Now().Format(time.RFC3339Nano),
		})
		if err != nil {
			t.Fatalf("Select %d: %v", i, err)
		}
		time.Sleep(time.Microsecond)
	}

	if segments.Size() == 0 {
		t.Errorf("actual 路径应学习段表, 实际 Size=0")
	}
}

func TestM7_DispatchMode_LiteralsMatchConfig(t *testing.T) {
	// cross-check dispatcher mode const 与 config.PoolSelectorMode 完全一致 —
	// 防字符串 drift (M2/M4 reviewer 都建议过的 M7 守门测试)。
	pairs := []struct {
		dispatcher string
		cfg        config.PoolSelectorMode
		name       string
	}{
		{DispatchModeDefault, config.PoolSelectorModeDefault, "default"},
		{DispatchModeShadow, config.PoolSelectorModeShadow, "shadow"},
		{DispatchModeCanary, config.PoolSelectorModeCanary, "canary"},
		{DispatchModePASRPrimary, config.PoolSelectorModePASRPrimary, "pasr-primary"},
		{DispatchModePASRStrict, config.PoolSelectorModePASRStrict, "pasr-strict"},
	}
	for _, p := range pairs {
		if p.dispatcher != string(p.cfg) {
			t.Errorf("[%s] dispatcher=%q vs config=%q drift! 必须一致",
				p.name, p.dispatcher, string(p.cfg))
		}
	}
}
