package settlementrecovery

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

type spyEnqueuer struct {
	calls  int
	last   dlq.Event
	retID  int64
	retErr error
}

func (s *spyEnqueuer) Enqueue(_ context.Context, e dlq.Event) (int64, error) {
	s.calls++
	s.last = e
	if s.retErr != nil {
		return 0, s.retErr
	}
	if s.retID == 0 {
		return 100, nil
	}
	return s.retID, nil
}

func validPayload() Payload {
	return Payload{
		Source:    SourceStream,
		RequestID: "req-456",
		Settle: settleRequestPersisted{
			ClaimID:  7001,
			TenantID: 3001,
		},
	}
}

// TestEnqueuePayload_BuildsCorrectEvent 守 EventKind / Lane / SourceTable /
// SourceID / IdempotencyKey 字段对齐 schema 0053 + LaneForKind HIGH 映射。
//
// Mutation:把 EventKind 改成 EventKindUsageRecord → 本用例必红(SQL CHECK
// 拒 + Lane 错位 + 检索过滤错)。
func TestEnqueuePayload_BuildsCorrectEvent(t *testing.T) {
	spy := &spyEnqueuer{}
	p := validPayload()

	id, err := EnqueuePayload(context.Background(), spy, p, "settle failed: connection lost")
	if err != nil {
		t.Fatalf("EnqueuePayload: %v", err)
	}
	if id == 0 {
		t.Fatal("EnqueuePayload should return non-zero id from Enqueuer")
	}
	if spy.calls != 1 {
		t.Fatalf("Enqueuer.Enqueue calls=%d want=1", spy.calls)
	}
	e := spy.last
	if e.EventKind != dlq.EventKindPostDeliverySettlement {
		t.Fatalf("event_kind=%q want=%q", e.EventKind, dlq.EventKindPostDeliverySettlement)
	}
	if e.Lane != dlq.LaneHigh {
		t.Fatalf("lane=%q want=%q (post_delivery_settlement is money path, must be HIGH)", e.Lane, dlq.LaneHigh)
	}
	if e.SourceTable != "billing_ledger_claims" {
		t.Fatalf("source_table=%q want=billing_ledger_claims", e.SourceTable)
	}
	if e.SourceID != p.Settle.ClaimID {
		t.Fatalf("source_id=%d want=%d (must point back to claim row)", e.SourceID, p.Settle.ClaimID)
	}
	if e.TenantID != p.Settle.TenantID {
		t.Fatalf("tenant_id=%d want=%d", e.TenantID, p.Settle.TenantID)
	}
	if e.ClaimID != p.Settle.ClaimID {
		t.Fatalf("claim_id=%d want=%d", e.ClaimID, p.Settle.ClaimID)
	}
	if e.FailureReason != "settle failed: connection lost" {
		t.Fatalf("failure_reason=%q want pass-through", e.FailureReason)
	}
	wantIdem := "post_delivery_settlement:3001:7001:req-456"
	if e.IdempotencyKey != wantIdem {
		t.Fatalf("idempotency_key=%q want=%q", e.IdempotencyKey, wantIdem)
	}
	if len(e.Payload) == 0 {
		t.Fatal("event Payload must contain encoded Payload bytes")
	}
}

// TestEnqueuePayload_NilEnqueuer 守 P0 alert 兜底 — wire 缺时不能默默吞,
// 必须返特定 sentinel error 让调用方触发告警。
// Mutation:删 q==nil check → 本用例必红(会 panic 而不是返 ErrEnqueuerNil)。
func TestEnqueuePayload_NilEnqueuer(t *testing.T) {
	_, err := EnqueuePayload(context.Background(), nil, validPayload(), "x")
	if !errors.Is(err, ErrEnqueuerNil) {
		t.Fatalf("err=%v want=ErrEnqueuerNil", err)
	}
}

// TestEnqueuePayload_ValidationFailsBeforeEnqueue 守 invalid payload 不入表 —
// 缺 ClaimID 的 row 进 DLQ 没意义且会让 worker 卡死在 decode/proof 失败。
// Mutation:把 EnqueuePayload 改成 skip Validate → 本用例必红(spy 计数 1)。
func TestEnqueuePayload_ValidationFailsBeforeEnqueue(t *testing.T) {
	spy := &spyEnqueuer{}
	bad := Payload{Source: SourceStream, Settle: settleRequestPersisted{ClaimID: 0, TenantID: 1}}
	_, err := EnqueuePayload(context.Background(), spy, bad, "x")
	if err == nil {
		t.Fatal("EnqueuePayload should reject invalid payload")
	}
	if spy.calls != 0 {
		t.Fatalf("Enqueuer.Enqueue should not be called on invalid payload, got %d calls", spy.calls)
	}
}

// TestEnqueuePayload_PropagatesEnqueuerError 守 DLQ persist 自己失败时
// 调用方能拿到错(用来触发 P0 alert)。
// Mutation:把 EnqueuePayload 改成 swallow err 返 nil → 本用例必红。
func TestEnqueuePayload_PropagatesEnqueuerError(t *testing.T) {
	spy := &spyEnqueuer{retErr: errors.New("db down")}
	_, err := EnqueuePayload(context.Background(), spy, validPayload(), "x")
	if err == nil {
		t.Fatal("EnqueuePayload should propagate Enqueuer err for P0 alert path")
	}
}

// TestEnqueueFailureReturnsReplayEvidenceOnQueueFailure 守住正式队列不可用时，
// 调用方仍能把同一份脱敏载荷写入结算意图，不能只剩一条易丢日志。
func TestEnqueueFailureReturnsReplayEvidenceOnQueueFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		queue Enqueuer
	}{
		{name: "队列写失败", queue: &spyEnqueuer{retErr: errors.New("db down")}},
		{name: "队列未接线", queue: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidence, err := EnqueueFailure(
				context.Background(),
				tc.queue,
				validPayload(),
				errors.New("settle failed"),
				"settlementrecovery.test",
			)
			if err == nil {
				t.Fatal("队列失败必须返回错误")
			}
			if len(evidence.Payload) == 0 || evidence.FailureClass == "" {
				t.Fatalf("双故障恢复证据=%+v", evidence)
			}
			decoded, decodeErr := Decode(evidence.Payload)
			if decodeErr != nil ||
				decoded.ToSettleRequest().ClaimID != validPayload().Settle.ClaimID ||
				decoded.RequestID != validPayload().RequestID {
				t.Fatalf("恢复证据 decoded=%+v err=%v", decoded, decodeErr)
			}
		})
	}
}
