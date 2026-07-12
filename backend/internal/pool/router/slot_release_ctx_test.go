// HUAKAI · iKun

// 补偿 release 的 ctx 判别测试: claim 写失败恰发生在请求已取消/超时的场景,
// 用原 ctx 释放会连带丢 release, 槽位只能等孤儿扫兜底 (镜像 pasr HIGH-2 语义)。

package router

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// ctxRecordingSlotManager 记录 Release 收到的 ctx 是否已取消。
type ctxRecordingSlotManager struct {
	released      bool
	releaseCtxErr error
}

func (m *ctxRecordingSlotManager) Acquire(context.Context, *AccountSnapshot, SelectionRequest) (*AcquireResult, error) {
	return &AcquireResult{
		AcquisitionToken: uuid.New(),
		Release: func(ctx context.Context) error {
			m.released = true
			m.releaseCtxErr = ctx.Err()
			return ctx.Err()
		},
	}, nil
}

// cancelingClaimGate 在 WriteAcquisition 期间取消请求 ctx 再报错
// (模拟客户端断连与 claim 写失败同时发生的补偿场景)。
type cancelingClaimGate struct {
	cancel context.CancelFunc
}

func (g cancelingClaimGate) WriteAcquisition(context.Context, int64, int64, int64, uuid.UUID) error {
	g.cancel()
	return errors.New("claim write failed")
}

// TestSelectorCompensationReleaseSurvivesCanceledCtx 守 A#9: claim 写失败 + 请求 ctx
// 已取消 → 槽位补偿 release 必须在脱钩 ctx 上执行成功。
// mutation: releaseSlotDetached 退回 `_ = acquired.release(ctx)` → release 收到已取消
// ctx (releaseCtxErr != nil) → 红。
func TestSelectorCompensationReleaseSurvivesCanceledCtx(t *testing.T) {
	accounts := []*AccountSnapshot{{
		ID: 301, TenantID: 7, Priority: 1, LoadRate: 0.01,
		MaxConcurrency: 4, HealthState: "healthy",
	}}
	slots := &ctxRecordingSlotManager{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sel := NewDefaultSelector(&stubAccountSource{accounts: accounts},
		WithSlotManager(slots),
		WithClaimGate(cancelingClaimGate{cancel: cancel}),
	)

	_, err := sel.Select(ctx, SelectionRequest{TenantID: 7, ClaimID: 99, RequestedModel: "m"})
	if err == nil {
		t.Fatal("claim 写失败应上抛")
	}
	if !slots.released {
		t.Fatal("补偿 release 未执行 —— 槽位泄漏")
	}
	if slots.releaseCtxErr != nil {
		t.Fatalf("release 收到已取消 ctx (err=%v) —— 补偿随请求取消一起丢, 槽位只能等孤儿扫", slots.releaseCtxErr)
	}
}
