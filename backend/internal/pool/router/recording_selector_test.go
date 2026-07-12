package router

import (
	"context"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
)

type fakeRecSelector struct {
	res *SelectionResult
	err error
}

func (f fakeRecSelector) Select(context.Context, SelectionRequest) (*SelectionResult, error) {
	return f.res, f.err
}

func recCounter() *precheck.Counter {
	base := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	return precheck.New(time.Minute, func() time.Time { return base })
}

// 一次确定的选号消费该账号预算中的一个请求。变异守卫:如果 RecordingSelector
// 停止调用 Record,选号后的 Check 仍为 Allowed,本测试变红。
func TestRecordingSelector_RecordsOnValidSelection(t *testing.T) {
	c := recCounter()
	rs := NewRecordingSelector(fakeRecSelector{res: &SelectionResult{AccountID: 5}}, c)
	lim := precheck.Limits{RPM: 1}
	if d := c.Check(5, lim, 0); !d.Allowed {
		t.Fatalf("pre-select with empty budget must allow, got %+v", d)
	}
	if _, err := rs.Select(context.Background(), SelectionRequest{EstimatedInputTokens: 0}); err != nil {
		t.Fatalf("select err: %v", err)
	}
	if d := c.Check(5, lim, 0); d.Allowed {
		t.Fatalf("after recording one request, RPM 1 must be exhausted, got %+v", d)
	}
}

// 估算的输入 token 被计入 TPM。
func TestRecordingSelector_RecordsTokens(t *testing.T) {
	c := recCounter()
	rs := NewRecordingSelector(fakeRecSelector{res: &SelectionResult{AccountID: 9}}, c)
	rs.Select(context.Background(), SelectionRequest{EstimatedInputTokens: 100})
	if d := c.Check(9, precheck.Limits{TPM: 100}, 1); d.Allowed {
		t.Fatalf("100 tokens recorded should exhaust TPM 100, got %+v", d)
	}
}

// wait-plan 准入(Layer-3 队列)和错误都不消费预算 —— 只有已落定的
// 确定账号才消费。
func TestRecordingSelector_SkipsWaitPlanAndError(t *testing.T) {
	t.Run("wait plan", func(t *testing.T) {
		c := recCounter()
		res := &SelectionResult{AccountID: 5, WaitPlan: &WaitPlan{AccountID: 5}}
		NewRecordingSelector(fakeRecSelector{res: res}, c).Select(context.Background(), SelectionRequest{})
		if d := c.Check(5, precheck.Limits{RPM: 1}, 0); !d.Allowed {
			t.Fatalf("wait-plan result must not consume budget, got %+v", d)
		}
	})
	t.Run("error", func(t *testing.T) {
		c := recCounter()
		NewRecordingSelector(fakeRecSelector{res: nil, err: context.Canceled}, c).Select(context.Background(), SelectionRequest{})
		if d := c.Check(5, precheck.Limits{RPM: 1}, 0); !d.Allowed {
			t.Fatalf("error result must not consume budget, got %+v", d)
		}
	})
}

// counter 为 nil 时,包装器成为透明直通(不 panic,原样返回 inner 的结果)。
func TestRecordingSelector_NilCounter_PassThrough(t *testing.T) {
	want := &SelectionResult{AccountID: 7}
	got, err := NewRecordingSelector(fakeRecSelector{res: want}, nil).Select(context.Background(), SelectionRequest{})
	if err != nil || got != want {
		t.Fatalf("nil counter must pass through inner result, got=%v err=%v", got, err)
	}
}
