package opsinspection

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/workerlease"
)

// fakeSender 记录每一次发送调用（租户、收件人、主题、正文），并可被配置为失败。
type fakeSender struct {
	mu    sync.Mutex
	calls []email.Message
	err   error
}

func (f *fakeSender) SendTenantMessage(_ context.Context, tenantID int64, msg email.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	msg.TenantID = tenantID
	f.calls = append(f.calls, msg)
	return f.err
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSender) last() email.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func newTestWorker(sender MessageSender, recipient string) *InspectionWorker {
	return NewInspectionWorker(InspectionWorkerConfig{
		Service:   NewInspectionService(healthySources(), testTenant, fixedNow),
		Sender:    sender,
		Recipient: recipient,
		TenantID:  testTenant,
	})
}

// TestWorkerTickSendsOnce：一次 tick 运行巡检，并以解析出的收件人 + 非空正文
// 恰好调用一次发送方；reports_sent 自增，failed 保持为 0。
// 捕获的回归：若该 tick 重复发送（例如每段发一次）或发给了错误的收件人，
// 调用计数 / 收件人断言就会变红。
func TestWorkerTickSendsOnce(t *testing.T) {
	sender := &fakeSender{}
	w := newTestWorker(sender, "ops@huakai.test")
	w.tick(context.Background())

	if sender.count() != 1 {
		t.Fatalf("expected exactly 1 send, got %d", sender.count())
	}
	msg := sender.last()
	if msg.To != "ops@huakai.test" {
		t.Fatalf("expected recipient ops@huakai.test, got %q", msg.To)
	}
	if msg.TenantID != testTenant {
		t.Fatalf("expected tenant %d, got %d", testTenant, msg.TenantID)
	}
	if len(msg.HTMLBody) == 0 || len(msg.Subject) == 0 {
		t.Fatalf("expected non-empty subject+body, got subject=%q body-len=%d", msg.Subject, len(msg.HTMLBody))
	}
	if w.ReportsSent() != 1 || w.FailedTicks() != 0 {
		t.Fatalf("expected sent=1 failed=0, got sent=%d failed=%d", w.ReportsSent(), w.FailedTicks())
	}
}

// TestWorkerSendErrorCountsAndContinues：一次发送错误会让 failed_ticks 自增、
// 使 reports_sent 保持为 0，且循环能撑过第二次 tick（无 panic / 不停止）。
// 捕获的回归：若发送错误以 panic 形式传播或停掉了循环，第二次 tick 就不会运行，
// TickCount 会是 1 而非 2。
func TestWorkerSendErrorCountsAndContinues(t *testing.T) {
	sender := &fakeSender{err: errors.New("smtp down")}
	w := newTestWorker(sender, "ops@huakai.test")

	w.tick(context.Background())
	w.tick(context.Background())

	if w.FailedTicks() != 2 {
		t.Fatalf("expected failed=2, got %d", w.FailedTicks())
	}
	if w.ReportsSent() != 0 {
		t.Fatalf("expected sent=0 on send errors, got %d", w.ReportsSent())
	}
	if w.TickCount() != 2 {
		t.Fatalf("expected 2 ticks (loop survived send error), got %d", w.TickCount())
	}
}

// TestWorkerRecorderReceivesSanitizedOutcome：recorder 会以运行结果被调用，
// 且失败分类是固定的 "send_failed" 枚举，绝不会是底层错误文本。
// 捕获的回归：若 worker 把 err.Error() 塞进了结果，原始的 "smtp down" 文本
// （一个潜在的泄露途径）就会出现，而非那个枚举。
func TestWorkerRecorderReceivesSanitizedOutcome(t *testing.T) {
	var got RunOutcome
	rec := captureRecorder{out: &got}
	w := NewInspectionWorker(InspectionWorkerConfig{
		Service:   NewInspectionService(healthySources(), testTenant, fixedNow),
		Sender:    &fakeSender{err: errors.New("smtp down secret-host:25")},
		Recorder:  rec,
		Recipient: "ops@huakai.test",
		TenantID:  testTenant,
	})
	w.tick(context.Background())

	if got.Sent {
		t.Fatalf("expected sent=false on send error")
	}
	if got.FailureClass != "send_failed" {
		t.Fatalf("expected fixed failure class, got %q", got.FailureClass)
	}
}

// TestWorkerNotStartedWhenNoRecipient：在收件人为空时调用 Start 是空操作
// （循环永不运行），即便 worker 其余部分都已接线。不会 panic。
// 捕获的回归：若 Start 不对收件人做门控，一个已启用但未经配置的部署就会开始
// tick 并尝试向 "" 发送。
func TestWorkerNotStartedWhenNoRecipient(t *testing.T) {
	sender := &fakeSender{}
	w := newTestWorker(sender, "")
	w.Start(context.Background())
	defer w.Stop()

	if w.started {
		t.Fatalf("worker must not start without a recipient")
	}
	if sender.count() != 0 {
		t.Fatalf("no send should occur without a recipient, got %d", sender.count())
	}
}

// TestWorkerNotStartedWhenSenderNil：在 sender 为 nil 时调用 Start 是空操作
// （故障安全）。捕获的回归：sender 为 nil 时若仍 Start，之后发送时会发生
// nil-panic。
func TestWorkerNotStartedWhenSenderNil(t *testing.T) {
	w := NewInspectionWorker(InspectionWorkerConfig{
		Service:   NewInspectionService(healthySources(), testTenant, fixedNow),
		Sender:    nil,
		Recipient: "ops@huakai.test",
	})
	w.Start(context.Background())
	if w.started {
		t.Fatalf("worker must not start without a sender")
	}
}

// TestWorkerStartStopLifecycle：Start 会运行立即 tick（>=1 次发送），Stop
// 干净返回，且重启可用。
// 捕获的回归：会死锁的 Stop，或不运行立即 tick 的 Start，都会卡住或让 sent=0。
func TestWorkerStartStopLifecycle(t *testing.T) {
	sender := &fakeSender{}
	w := newTestWorker(sender, "ops@huakai.test")
	w.Start(context.Background())
	// Stop 会等待循环退出；到那时立即 tick 已经运行过了。
	w.Stop()
	if w.ReportsSent() < 1 {
		t.Fatalf("expected the immediate-on-start tick to send at least once, got %d", w.ReportsSent())
	}
	// 重启必须可用（Stop 之后再 Start）。
	w.Start(context.Background())
	w.Stop()
	if w.ReportsSent() < 2 {
		t.Fatalf("expected a restart to tick again, got %d", w.ReportsSent())
	}
}

type inspectionLeaseSession struct {
	healthErr error
	released  atomic.Bool
}

func (s *inspectionLeaseSession) Healthy(context.Context) error { return s.healthErr }
func (s *inspectionLeaseSession) Release()                      { s.released.Store(true) }

type inspectionLeaseProvider struct {
	acquired bool
	session  *inspectionLeaseSession
	err      error
	called   chan struct{}
	once     sync.Once
}

type inspectionWindowClaim struct {
	acquired bool
	err      error
	calls    atomic.Uint64
}

func (c *inspectionWindowClaim) TryClaim(context.Context, time.Duration) (bool, error) {
	c.calls.Add(1)
	return c.acquired, c.err
}

func TestInspectionWorkerClaimedWindowSendsOnce(t *testing.T) {
	sender := &fakeSender{}
	claim := &inspectionWindowClaim{acquired: true}
	w := NewInspectionWorker(InspectionWorkerConfig{
		Service:     NewInspectionService(healthySources(), testTenant, fixedNow),
		Sender:      sender,
		Recipient:   "ops@huakai.test",
		TenantID:    testTenant,
		Interval:    time.Hour,
		WindowClaim: claim,
	})
	w.runClaimedTick(context.Background())
	if got := sender.count(); got != 1 {
		t.Fatalf("取得时间窗后必须发送一次，got=%d", got)
	}
	if got := claim.calls.Load(); got != 1 {
		t.Fatalf("每轮只能认领一次，got=%d", got)
	}
}

func TestInspectionWorkerClaimedByOtherReplicaDoesNotSend(t *testing.T) {
	sender := &fakeSender{}
	claim := &inspectionWindowClaim{}
	w := NewInspectionWorker(InspectionWorkerConfig{
		Service:     NewInspectionService(healthySources(), testTenant, fixedNow),
		Sender:      sender,
		Recipient:   "ops@huakai.test",
		TenantID:    testTenant,
		Interval:    time.Hour,
		WindowClaim: claim,
	})
	w.runClaimedTick(context.Background())
	if got := sender.count(); got != 0 {
		t.Fatalf("时间窗已被其他副本认领时不得发送，got=%d", got)
	}
	if w.FailedTicks() != 0 {
		t.Fatalf("正常未取得认领不应记作故障，got=%d", w.FailedTicks())
	}
}

func TestInspectionWorkerWindowClaimFailureIsFailClosed(t *testing.T) {
	sender := &fakeSender{}
	claim := &inspectionWindowClaim{err: errors.New("redis unavailable")}
	w := NewInspectionWorker(InspectionWorkerConfig{
		Service:     NewInspectionService(healthySources(), testTenant, fixedNow),
		Sender:      sender,
		Recipient:   "ops@huakai.test",
		TenantID:    testTenant,
		Interval:    time.Hour,
		WindowClaim: claim,
	})
	w.runClaimedTick(context.Background())
	if got := sender.count(); got != 0 {
		t.Fatalf("时间窗存储故障时必须停止发送，got=%d", got)
	}
	if w.FailedTicks() != 1 {
		t.Fatalf("时间窗存储故障必须进入失败计数，got=%d", w.FailedTicks())
	}
}

func (p *inspectionLeaseProvider) TryAcquireSession(context.Context) (bool, workerlease.Session, error) {
	p.once.Do(func() { close(p.called) })
	return p.acquired, p.session, p.err
}

func TestInspectionWorkerFollowerDoesNotSend(t *testing.T) {
	sender := &fakeSender{}
	lease := &inspectionLeaseProvider{called: make(chan struct{})}
	w := NewInspectionWorker(InspectionWorkerConfig{
		Service:     NewInspectionService(healthySources(), testTenant, fixedNow),
		Sender:      sender,
		Recipient:   "ops@huakai.test",
		TenantID:    testTenant,
		Interval:    time.Hour,
		LeaderLease: lease,
	})
	w.Start(context.Background())
	select {
	case <-lease.called:
	case <-time.After(time.Second):
		t.Fatal("worker 未尝试取得 leader 租约")
	}
	w.Stop()
	if got := sender.count(); got != 0 {
		t.Fatalf("未取得租约的副本不得发送巡检邮件，got=%d", got)
	}
}

func TestInspectionWorkerLeaderSendsAndReleasesLease(t *testing.T) {
	sender := &fakeSender{}
	session := &inspectionLeaseSession{}
	lease := &inspectionLeaseProvider{acquired: true, session: session, called: make(chan struct{})}
	w := NewInspectionWorker(InspectionWorkerConfig{
		Service:     NewInspectionService(healthySources(), testTenant, fixedNow),
		Sender:      sender,
		Recipient:   "ops@huakai.test",
		TenantID:    testTenant,
		Interval:    time.Hour,
		LeaderLease: lease,
	})
	w.Start(context.Background())
	deadline := time.Now().Add(time.Second)
	for sender.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := sender.count(); got != 1 {
		w.Stop()
		t.Fatalf("leader 应立即发送一次巡检邮件，got=%d", got)
	}
	w.Stop()
	if !session.released.Load() {
		t.Fatal("worker 停止时必须释放 leader 租约")
	}
}

func TestInspectionWorkerLostLeaseDoesNotSend(t *testing.T) {
	sender := &fakeSender{}
	session := &inspectionLeaseSession{healthErr: errors.New("连接已失效")}
	lease := &inspectionLeaseProvider{acquired: true, session: session, called: make(chan struct{})}
	w := NewInspectionWorker(InspectionWorkerConfig{
		Service:     NewInspectionService(healthySources(), testTenant, fixedNow),
		Sender:      sender,
		Recipient:   "ops@huakai.test",
		TenantID:    testTenant,
		Interval:    time.Hour,
		LeaderLease: lease,
	})
	w.Start(context.Background())
	select {
	case <-lease.called:
	case <-time.After(time.Second):
		w.Stop()
		t.Fatal("worker 未尝试取得 leader 租约")
	}
	deadline := time.Now().Add(time.Second)
	for !session.released.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	w.Stop()
	if got := sender.count(); got != 0 {
		t.Fatalf("租约会话失效后不得发送巡检邮件，got=%d", got)
	}
	if !session.released.Load() {
		t.Fatal("失效租约必须释放后重新参与选主")
	}
}
