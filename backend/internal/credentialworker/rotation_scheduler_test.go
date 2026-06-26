package credentialworker

import (
	"context"
	"testing"
	"time"
)

// 经 WithRotationScan 启用后,RunOnce 在 refresh 过程之后运行 rotation 扫描:它以
// 配置的 limit 与一个 now-maxAge 的截止点查询,并把一个到期的可刷新候选路由进
// refresh 恢复。Mutation guard:删掉 RunOnce 中的 ScanRotationDue 调用,store 就
// 永不被触碰 → 转红。
func TestRunOnce_RunsRotationScanWhenEnabled(t *testing.T) {
	store := &fakeRotationStore{due: []RotationCandidate{oauthCand(3)}}
	s := newTestScheduler(nil, &stormSpy{}, &refresherSpy{}, WithRotationScan(store, 90*24*time.Hour, 10, nil))
	s.now = func() time.Time { return rotNow }

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if store.gotLimit != 10 || len(store.recovered) != 1 {
		t.Fatalf("enabled rotation scan must query(limit=10)+recover(1), got limit=%d recovered=%d", store.gotLimit, len(store.recovered))
	}
	if want := rotNow.Add(-90 * 24 * time.Hour); !store.gotOlderThan.Equal(want) {
		t.Fatalf("cutoff must be now-maxAge=%v, got %v", want, store.gotOlderThan)
	}
}

// 在没有 WithRotationScan 时(rotationMaxAge 保持为 0),即使 store 存在,扫描也会被
// 完全跳过——严格 opt-in,默认零行为变更。
func TestRunOnce_SkipsRotationScanByDefault(t *testing.T) {
	store := &fakeRotationStore{due: []RotationCandidate{oauthCand(1)}}
	s := newTestScheduler(nil, &stormSpy{}, &refresherSpy{})
	s.rotationStore = store // store 已设置,但 maxAge 留为 0 → 禁用

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if store.gotLimit != 0 || len(store.recovered) != 0 || len(store.flagged) != 0 {
		t.Fatalf("maxAge=0 must skip the rotation scan, got limit=%d recovered=%d flagged=%d",
			store.gotLimit, len(store.recovered), len(store.flagged))
	}
}
