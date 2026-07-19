package channelprobe

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
)

func TestSchedulerTicker(t *testing.T) {
	// 变异:把 tick 分支留空或跳过 ActiveProbe;那样 probe 调用次数会停在 0,而非活跃 channel 的数量。
	ticker := newFakeSchedulerTicker()
	lister := &activeChannelListerStub{channels: []ActiveChannel{
		activeChannelFixture(7, "openai", 101, 1001, 1),
		activeChannelFixture(7, "anthropic", 202, 2002, 3),
	}}
	probe := newActiveProbeStub()
	scheduler := NewChannelHealthScheduler(SchedulerConfig{
		Channels: lister,
		Health:   channelhealth.NewService(channelhealth.NewMemoryStore(), channelhealth.DefaultPolicy(), nil),
		Interval: time.Minute,
		NewTicker: func(interval time.Duration) SchedulerTicker {
			if interval != time.Minute {
				t.Fatalf("ticker interval=%s want 1m", interval)
			}
			return ticker
		},
		ActiveProbe: probe.Probe,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- scheduler.Run(ctx) }()

	ticker.tick()
	probe.waitCalls(t, len(lister.channels))
	cancel()
	waitSchedulerRunReturned(t, errCh)

	if got := probe.channelIDs(); !reflect.DeepEqual(got, []string{
		lister.channels[0].ChannelID,
		lister.channels[1].ChannelID,
	}) {
		t.Fatalf("probe channel IDs=%v want active channel IDs", got)
	}
}

func TestSchedulerDefaultOffWithoutActiveProbe(t *testing.T) {
	scheduler := NewChannelHealthScheduler(SchedulerConfig{
		Channels: &activeChannelListerStub{channels: []ActiveChannel{activeChannelFixture(7, "openai", 101, 1001, 1)}},
		Health:   channelhealth.NewService(channelhealth.NewMemoryStore(), channelhealth.DefaultPolicy(), nil),
	})

	if err := scheduler.Run(context.Background()); err != nil {
		t.Fatalf("Run without ActiveProbe err=%v want nil no-op", err)
	}
}

func activeChannelFixture(tenantID int64, vendor string, providerAccountID, credentialID int64, version int) ActiveChannel {
	key := channelhealth.ChannelKey{
		TenantID:            tenantID,
		Vendor:              vendor,
		ProviderAccountID:   providerAccountID,
		AccountCredentialID: credentialID,
		CredentialVersion:   version,
	}
	key.ChannelID = key.StableChannelID()
	return ActiveChannel{ChannelID: key.ChannelID, Key: key}
}

type fakeSchedulerTicker struct {
	ch       chan time.Time
	stopped  chan struct{}
	stopOnce sync.Once
}

func newFakeSchedulerTicker() *fakeSchedulerTicker {
	return &fakeSchedulerTicker{
		ch:      make(chan time.Time, 1),
		stopped: make(chan struct{}),
	}
}

func (t *fakeSchedulerTicker) C() <-chan time.Time {
	return t.ch
}

func (t *fakeSchedulerTicker) Stop() {
	t.stopOnce.Do(func() {
		close(t.stopped)
	})
}

func (t *fakeSchedulerTicker) tick() {
	t.ch <- time.Now().UTC()
}

func waitSchedulerRunReturned(t *testing.T, errCh <-chan error) {
	t.Helper()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler Run did not return after cancellation")
	}
}

type activeChannelListerStub struct {
	channels []ActiveChannel
}

func (s *activeChannelListerStub) ListActiveChannels(context.Context) ([]ActiveChannel, error) {
	return append([]ActiveChannel(nil), s.channels...), nil
}

type activeProbeStub struct {
	mu      sync.Mutex
	seen    []string
	called  chan struct{}
	closeMu sync.Once
}

func newActiveProbeStub() *activeProbeStub {
	return &activeProbeStub{called: make(chan struct{})}
}

func (s *activeProbeStub) Probe(_ context.Context, channelID string) (ProbeResult, error) {
	s.mu.Lock()
	s.seen = append(s.seen, channelID)
	if len(s.seen) >= 2 {
		s.closeMu.Do(func() { close(s.called) })
	}
	s.mu.Unlock()
	return ProbeResult{StatusCode: 200, LatencyMS: 12}, nil
}

func (s *activeProbeStub) waitCalls(t *testing.T, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		s.mu.Lock()
		got := len(s.seen)
		s.mu.Unlock()
		if got >= want {
			return
		}
		select {
		case <-s.called:
			return
		case <-deadline:
			t.Fatalf("probe calls=%d want %d", got, want)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func (s *activeProbeStub) channelIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

// 调度器对每个待恢复渠道先尝试开始放量，再推进已有放量阶段。
func TestSchedulerReconcilesCoolingAndRampingChannels(t *testing.T) {
	ticker := newFakeSchedulerTicker()
	lister := &activeChannelListerStub{channels: []ActiveChannel{
		activeChannelFixture(7, "openai", 101, 1001, 1),
		activeChannelFixture(7, "anthropic", 202, 2002, 3),
	}}
	reconciler := newRampReconcilerStub()
	scheduler := NewChannelHealthScheduler(SchedulerConfig{
		Channels:  lister,
		Ramp:      reconciler,
		Interval:  time.Minute,
		NewTicker: func(time.Duration) SchedulerTicker { return ticker },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- scheduler.Run(ctx) }()

	ticker.tick()
	reconciler.waitCalls(t, len(lister.channels))
	cancel()
	waitSchedulerRunReturned(t, errCh)

	if got := reconciler.accountIDs(); !reflect.DeepEqual(got, []int64{101, 202}) {
		t.Fatalf("advanced provider account IDs=%v want [101 202]", got)
	}
	if got := reconciler.startedAccountIDs(); !reflect.DeepEqual(got, []int64{101, 202}) {
		t.Fatalf("ramp-start provider account IDs=%v want [101 202]", got)
	}
}

type rampReconcilerStub struct {
	mu      sync.Mutex
	started []int64
	seen    []int64
	called  chan struct{}
	closeMu sync.Once
}

func newRampReconcilerStub() *rampReconcilerStub {
	return &rampReconcilerStub{called: make(chan struct{})}
}

func (s *rampReconcilerStub) MaybeStartRamp(_ context.Context, key channelhealth.ChannelKey) (channelhealth.Record, error) {
	s.mu.Lock()
	s.started = append(s.started, key.ProviderAccountID)
	s.mu.Unlock()
	return channelhealth.Record{}, nil
}

func (s *rampReconcilerStub) AdvanceRamp(_ context.Context, key channelhealth.ChannelKey) (channelhealth.Record, error) {
	s.mu.Lock()
	s.seen = append(s.seen, key.ProviderAccountID)
	if len(s.seen) >= 2 {
		s.closeMu.Do(func() { close(s.called) })
	}
	s.mu.Unlock()
	return channelhealth.Record{}, nil
}

func (s *rampReconcilerStub) waitCalls(t *testing.T, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		s.mu.Lock()
		got := len(s.seen)
		s.mu.Unlock()
		if got >= want {
			return
		}
		select {
		case <-s.called:
			return
		case <-deadline:
			t.Fatalf("ramp advance calls=%d want %d", got, want)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func (s *rampReconcilerStub) accountIDs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.seen...)
}

func (s *rampReconcilerStub) startedAccountIDs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.started...)
}

type leaderLeaseStub struct {
	acquired bool
	err      error
	calls    int
	released bool
}

func (s *leaderLeaseStub) TryAcquire(context.Context) (bool, func(), error) {
	s.calls++
	if s.err != nil {
		return false, nil, s.err
	}
	if !s.acquired {
		return false, nil, nil
	}
	return true, func() { s.released = true }, nil
}

func TestSchedulerLeaderLeaseAllowsOnlyWinner(t *testing.T) {
	lister := &activeChannelListerStub{channels: []ActiveChannel{activeChannelFixture(7, "openai", 101, 1001, 1)}}
	reconciler := newRampReconcilerStub()
	loser := &leaderLeaseStub{}
	s := NewChannelHealthScheduler(SchedulerConfig{Channels: lister, Ramp: reconciler, LeaderLease: loser})
	s.probeOnce(context.Background())
	if loser.calls != 1 || len(reconciler.accountIDs()) != 0 {
		t.Fatalf("non-leader executed recovery: lease_calls=%d accounts=%v", loser.calls, reconciler.accountIDs())
	}

	winner := &leaderLeaseStub{acquired: true}
	s.leaderLease = winner
	s.probeOnce(context.Background())
	if !winner.released || !reflect.DeepEqual(reconciler.accountIDs(), []int64{101}) {
		t.Fatalf("leader did not execute and release: released=%v accounts=%v", winner.released, reconciler.accountIDs())
	}
}

func TestSchedulerLeaderLeaseFailureSkipsSideEffects(t *testing.T) {
	reconciler := newRampReconcilerStub()
	s := NewChannelHealthScheduler(SchedulerConfig{
		Channels:    &activeChannelListerStub{channels: []ActiveChannel{activeChannelFixture(7, "openai", 101, 1001, 1)}},
		Ramp:        reconciler,
		LeaderLease: &leaderLeaseStub{err: errors.New("database unavailable")},
	})
	s.probeOnce(context.Background())
	if len(reconciler.accountIDs()) != 0 {
		t.Fatalf("lease failure must fail closed, got side effects for %v", reconciler.accountIDs())
	}
}
