package tlsfphealth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

type fakeLister struct{ recs []ProfileRecord }

func (f fakeLister) ListActive(context.Context) ([]ProfileRecord, error) { return f.recs, nil }

type fakeMarker struct{ drifted []int64 }

func (f *fakeMarker) MarkDrift(_ context.Context, _, id int64) error {
	f.drifted = append(f.drifted, id)
	return nil
}

type countingLister struct {
	calls int
}

func (l *countingLister) ListActive(context.Context) ([]ProfileRecord, error) {
	l.calls++
	return nil, nil
}

type fakeLeaderLease struct {
	acquired bool
	err      error
	releases int
}

func (l *fakeLeaderLease) TryAcquire(context.Context) (bool, func(), error) {
	if l.err != nil || !l.acquired {
		return false, nil, l.err
	}
	return true, func() { l.releases++ }, nil
}

// 只有无法转换为 Rust IPC 合同的 profile 才被标记 drift_detected。
func TestWorker_Tick_FlagsOnlyInvalid(t *testing.T) {
	recs := []ProfileRecord{
		{ID: 1, TenantID: 9, Fields: mimicry.ProfileFields{
			ID:                   1,
			Name:                 "valid-custom",
			GreaseEnabled:        true,
			CipherSuites:         []int{0x1301, 0x1302, 0xc02b},
			SupportedCurves:      []int{29, 23, 24},
			EcPointFormats:       []int{0},
			SignatureAlgorithms:  []int{0x0403, 0x0804},
			AlpnProtocols:        []string{"h2", "http/1.1"},
			TLSSupportedVersions: []int{0x0304, 0x0303},
			KeyShareGroups:       []int{29, 23},
			PskModes:             []int{1},
			ExtensionsOrder:      []int{0, 23, 65281, 10, 11, 13, 16, 43, 45, 51},
		}},
		{ID: 2, TenantID: 9, Fields: mimicry.ProfileFields{CipherSuites: []int{0x10000}, SupportedCurves: []int{29}, TLSSupportedVersions: []int{0x0304}}}, // 无效:超出范围
		{ID: 3, TenantID: 9, Fields: mimicry.ProfileFields{Name: "incomplete-custom"}},                                                                     // 无效:不完整
	}
	marker := &fakeMarker{}
	w := NewWorker(fakeLister{recs: recs}, marker, time.Minute, nil)
	w.tick(context.Background())

	// 变异守卫：跳过校验、误标有效项或只标一类坏数据都会变红。
	if len(marker.drifted) != 2 || marker.drifted[0] != 2 || marker.drifted[1] != 3 {
		t.Fatalf("应只标记 profile 2/3，得到 %v", marker.drifted)
	}
}

func TestWorker_Tick_OnlyLeaderProducesSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lease *fakeLeaderLease
		want  int
	}{
		{name: "其它副本持有租约", lease: &fakeLeaderLease{}, want: 0},
		{name: "协调存储不可用", lease: &fakeLeaderLease{err: errors.New("database unavailable")}, want: 0},
		{name: "本副本取得租约", lease: &fakeLeaderLease{acquired: true}, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lister := &countingLister{}
			w := NewWorker(lister, &fakeMarker{}, time.Minute, nil, WithLeaderLease(tc.lease))
			w.tick(context.Background())
			if lister.calls != tc.want {
				t.Fatalf("列举次数=%d，期望 %d", lister.calls, tc.want)
			}
			if tc.lease.releases != tc.want {
				t.Fatalf("租约释放次数=%d，期望 %d", tc.lease.releases, tc.want)
			}
		})
	}
}

func TestWorker_WaitBlocksUntilStartContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := NewWorker(fakeLister{}, &fakeMarker{}, time.Hour, nil)
	w.Start(ctx)

	waitDone := make(chan error, 1)
	go func() {
		waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
		defer waitCancel()
		waitDone <- w.Wait(waitCtx)
	}()
	select {
	case err := <-waitDone:
		t.Fatalf("context 取消前 Wait 已返回: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("context 取消后 worker 未退出")
	}
}
