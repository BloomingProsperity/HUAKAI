package embeddingshttp

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

// recordingRecoveryEnqueuerB3 记录 settlementrecovery DLQ enqueue,断言交付前 settle 失败
// 被 durable 兜底持久化。
type recordingRecoveryEnqueuerB3 struct {
	calls   int
	lastEvt dlq.Event
}

func (s *recordingRecoveryEnqueuerB3) Enqueue(_ context.Context, e dlq.Event) (int64, error) {
	s.calls++
	s.lastEvt = e
	return 1, nil
}

// TestEmbeddingsSettleFailurePersistsToDLQ_B3 守 B3[S2]:embeddings 上游已返 2xx(平台已付
// 上游成本)后,交付前结算若因 DB 抖动/锁等待失败,必须经 SettleRecoveryDLQ 持久化,worker 后续
// 重结算——否则上游成本已发生但租户永不被计费(收入泄漏),与 completions 非流式(settleDirectWithRecovery)
// 及 images(settleDeliveredResponse)的 DLQ 兜底不对等。
//
// 断言正确行为:settle 失败 + DLQ 已注入 → DLQ enqueue 恰一次,且 settle err 原样传播(caller 写 500)。
// 缺陷代码(直接 ex.d.Settler.Settle,无 DLQ)下 enqueue calls==0 → RED。
func TestEmbeddingsSettleFailurePersistsToDLQ_B3(t *testing.T) {
	settleErr := errors.New("settle failed: db lock wait timeout")
	settler := &recordingSettler{settleErr: settleErr}
	spy := &recordingRecoveryEnqueuerB3{}
	ex := &execution{
		d:         Deps{Settler: settler, SettleRecoveryDLQ: spy},
		requestID: "req-b3-embeddings",
	}

	err := ex.settleDeliveredResponse(context.Background(), billing.SettleRequest{ClaimID: 42, TenantID: 7})
	if err == nil {
		t.Fatalf("settle err must propagate to caller so client sees 500 and can retry")
	}
	if spy.calls != 1 {
		t.Fatalf("upstream已返2xx后settle失败,DLQ enqueue calls=%d want 1 —— 上游成本已发生却漏计费(收入泄漏,B3)", spy.calls)
	}
	if spy.lastEvt.ClaimID != 42 || spy.lastEvt.TenantID != 7 {
		t.Fatalf("DLQ event 未携带正确 claim/tenant: claim=%d tenant=%d want 42/7 —— worker 无法重结算原 claim", spy.lastEvt.ClaimID, spy.lastEvt.TenantID)
	}
}
