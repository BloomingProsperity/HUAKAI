package channelprobe

import (
	"context"
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
