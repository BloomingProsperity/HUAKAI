package autolisting

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/workerlease"
)

type fakeGate struct{ on bool }

func (g fakeGate) Enabled(context.Context) bool { return g.on }

type fakeRefresher struct{ calls int }

func (r *fakeRefresher) RefreshReversedAccounts(context.Context) (RefreshResult, error) {
	r.calls++
	return RefreshResult{}, nil
}

type fakePromoter struct{ calls int }

func (p *fakePromoter) ProcessPending(context.Context) (Result, error) {
	p.calls++
	return Result{Enabled: true}, nil
}

// fakeSession 在第 healthyOKUntil 次 Healthy 调用后开始返回错误,模拟租约会话中途失效。
type fakeSession struct {
	healthyOKUntil int
	healthyCalls   int
	released       bool
}

func (s *fakeSession) Healthy(context.Context) error {
	s.healthyCalls++
	if s.healthyCalls > s.healthyOKUntil {
		return errors.New("session lost")
	}
	return nil
}
func (s *fakeSession) Release() { s.released = true }

type fakeLease struct {
	session  *fakeSession
	acquired bool
	calls    int
}

func (l *fakeLease) TryAcquireSession(context.Context) (bool, workerlease.Session, error) {
	l.calls++
	if !l.acquired {
		return false, nil, nil
	}
	return true, l.session, nil
}

// 总闸关:不抢 leader、不保鲜、不上架(纯人工挡)。
func TestWorkerTickGateOffDoesNothing(t *testing.T) {
	lease := &fakeLease{acquired: true, session: &fakeSession{healthyOKUntil: 99}}
	ref := &fakeRefresher{}
	prom := &fakePromoter{}
	w := NewWorker(ref, prom, fakeGate{on: false}, WorkerConfig{LeaderLease: lease})
	w.tick(context.Background(), "test")
	if lease.calls != 0 || ref.calls != 0 || prom.calls != 0 {
		t.Fatalf("总闸关时不应有任何动作:lease=%d refresh=%d promote=%d", lease.calls, ref.calls, prom.calls)
	}
}

// 抢不到 leader:不保鲜、不上架。
func TestWorkerTickNotLeaderSkips(t *testing.T) {
	lease := &fakeLease{acquired: false}
	ref := &fakeRefresher{}
	prom := &fakePromoter{}
	w := NewWorker(ref, prom, fakeGate{on: true}, WorkerConfig{LeaderLease: lease})
	w.tick(context.Background(), "test")
	if ref.calls != 0 || prom.calls != 0 {
		t.Fatalf("非 leader 不应保鲜/上架:refresh=%d promote=%d", ref.calls, prom.calls)
	}
}

// 保鲜后租约会话失效:上架步骤必须弃(不 promote),且租约释放。
// 守卫:删掉 promote 前的 leaderHealthy 复检 → promote 仍被调用 → 本测红。
func TestWorkerTickAbortsPromoteOnSessionLoss(t *testing.T) {
	// healthyOKUntil=1:保鲜前那次 Healthy 通过,上架前那次失败。
	session := &fakeSession{healthyOKUntil: 1}
	lease := &fakeLease{acquired: true, session: session}
	ref := &fakeRefresher{}
	prom := &fakePromoter{}
	w := NewWorker(ref, prom, fakeGate{on: true}, WorkerConfig{LeaderLease: lease})
	w.tick(context.Background(), "test")
	if ref.calls != 1 {
		t.Fatalf("会话首检通过时应保鲜一次,got %d", ref.calls)
	}
	if prom.calls != 0 {
		t.Fatalf("上架前会话失效必须弃 promote,却调用了 %d 次", prom.calls)
	}
	if !session.released {
		t.Fatal("租约会话必须释放")
	}
}

// 会话全程健康:保鲜 + 上架都执行,租约释放。
func TestWorkerTickHealthyRunsBoth(t *testing.T) {
	session := &fakeSession{healthyOKUntil: 99}
	lease := &fakeLease{acquired: true, session: session}
	ref := &fakeRefresher{}
	prom := &fakePromoter{}
	w := NewWorker(ref, prom, fakeGate{on: true}, WorkerConfig{LeaderLease: lease})
	w.tick(context.Background(), "test")
	if ref.calls != 1 || prom.calls != 1 {
		t.Fatalf("健康会话应保鲜+上架各一次:refresh=%d promote=%d", ref.calls, prom.calls)
	}
	if !session.released {
		t.Fatal("租约会话必须释放")
	}
}
