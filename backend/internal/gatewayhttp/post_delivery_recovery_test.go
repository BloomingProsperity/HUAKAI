package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
)

// postDeliveryFakeSettler 让 settleCompletion 直接走 Settler.Settle 路径
// (绕过 CompletionBus,因为 d.CompletionBus=nil)。Settle 返预设 err。
type postDeliveryFakeSettler struct {
	settleErr error
	calls     int
}

func (s *postDeliveryFakeSettler) Settle(_ context.Context, _ billing.SettleRequest) (*billing.SettleResult, error) {
	s.calls++
	if s.settleErr != nil {
		return nil, s.settleErr
	}
	return &billing.SettleResult{}, nil
}

func (s *postDeliveryFakeSettler) Abort(_ context.Context, _, _ int64, _, _ string, _ int64, _ json.RawMessage) error {
	return errors.New("Abort not used")
}
func (s *postDeliveryFakeSettler) CommitCacheHit(_ context.Context, _ billing.SettleRequest) error {
	return errors.New("CommitCacheHit not used")
}
func (s *postDeliveryFakeSettler) Refund(_ context.Context, _ billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, errors.New("Refund not used")
}

// postDeliverySpyEnqueuer 捕获 EnqueuePayload 调用,验 event 字段。
type postDeliverySpyEnqueuer struct {
	calls    int
	lastEvt  dlq.Event
	returnID int64
	retErr   error
}

func (s *postDeliverySpyEnqueuer) Enqueue(_ context.Context, e dlq.Event) (int64, error) {
	s.calls++
	s.lastEvt = e
	if s.retErr != nil {
		return 0, s.retErr
	}
	if s.returnID == 0 {
		return 42, nil
	}
	return s.returnID, nil
}

// fakeAuditRefPolicy 返"不需要 audit ref" — 让 validateMoneyPathAuditRefForSource
// 不阻断 settle path。
type fakeAuditRefPolicy struct{}

func newPostDeliveryFixtureEvent() eventbus.RequestCompletionEvent {
	return eventbus.RequestCompletionEvent{
		ID:                "evt-pd-1",
		TenantID:          3007,
		ClaimID:           7007,
		AccountID:         9007,
		RequestID:         "req-pd-1",
		AuditLedgerID:     "ledger-1",
		AuditLedgerDLQRef: "ledger-dlq-1",
		SettleRequest: billing.SettleRequest{
			TenantID:       3007,
			ClaimID:        7007,
			APIKeyID:       4007,
			UserID:         5007,
			AuditRequestID: "audit-pd-1",
		},
	}
}

// TestSettleCompletionWithRecovery_SuccessDoesNotEnqueue 守:settle 成功时
// SettleRecoveryDLQ 不被调用 — 防误兜底正常 settle。
//
// Mutation:把 helper 改成"无条件 enqueue" → 本用例 spy.calls=1 必红。
func TestSettleCompletionWithRecovery_SuccessDoesNotEnqueue(t *testing.T) {
	settler := &postDeliveryFakeSettler{settleErr: nil}
	spy := &postDeliverySpyEnqueuer{}
	deps := ChatHandlerDeps{
		Settler:           settler,
		SettleRecoveryDLQ: spy,
	}
	event := newPostDeliveryFixtureEvent()

	_, err := settleCompletionWithRecovery(context.Background(), deps, event, settlementrecovery.SourceStream)
	if err != nil {
		t.Fatalf("settle success path returned err=%v", err)
	}
	if spy.calls != 0 {
		t.Fatalf("Enqueuer.Enqueue calls=%d want=0 on settle success", spy.calls)
	}
}

// TestAT_GW_002_16_PostDeliverySettleFailureEnqueuesRecovery 守 P2/P3 主修复:
// 流式 settle 失败 + source=stream + SettleRecoveryDLQ 已注入 → enqueue 1 次,
// 行 event_kind=post_delivery_settlement,payload 可 decode 回 SettleRequest。
//
// Mutation 1:删 settleCompletionWithRecovery 内 enqueue 调用 → spy.calls=0 红。
// Mutation 2:把 source 改成 source="" 调用 → 同样不 enqueue 红。
// Mutation 3:把 event_kind 改回 EventKindUsageRecord → assertion 红。
func TestAT_GW_002_16_PostDeliverySettleFailureEnqueuesRecovery(t *testing.T) {
	settler := &postDeliveryFakeSettler{settleErr: errors.New("pgx: connection reset")}
	spy := &postDeliverySpyEnqueuer{}
	deps := ChatHandlerDeps{
		Settler:           settler,
		SettleRecoveryDLQ: spy,
	}
	event := newPostDeliveryFixtureEvent()

	_, err := settleCompletionWithRecovery(context.Background(), deps, event, settlementrecovery.SourceStream)
	if err == nil {
		t.Fatal("settleCompletionWithRecovery must propagate settle err (caller may want to log)")
	}
	if spy.calls != 1 {
		t.Fatalf("Enqueuer.Enqueue calls=%d want=1 on post-delivery settle failure", spy.calls)
	}
	if spy.lastEvt.EventKind != dlq.EventKindPostDeliverySettlement {
		t.Fatalf("dlq event_kind=%q want=%q", spy.lastEvt.EventKind, dlq.EventKindPostDeliverySettlement)
	}
	if spy.lastEvt.TenantID != 3007 {
		t.Fatalf("dlq tenant_id=%d want=3007", spy.lastEvt.TenantID)
	}
	if spy.lastEvt.ClaimID != 7007 {
		t.Fatalf("dlq claim_id=%d want=7007", spy.lastEvt.ClaimID)
	}

	// 验:payload 可 Decode 回 SettleRequest,worker 后续重 settle 能用
	decoded, err := settlementrecovery.Decode(spy.lastEvt.Payload)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("payload Validate: %v", err)
	}
	req := decoded.ToSettleRequest()
	if req.ClaimID != 7007 || req.TenantID != 3007 {
		t.Fatalf("decoded settle req mismatch: %+v", req)
	}

	// 验:payload 内 source 是 stream
	var probe map[string]any
	if err := json.Unmarshal(spy.lastEvt.Payload, &probe); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if probe["source"] != string(settlementrecovery.SourceStream) {
		t.Fatalf("payload source=%v want=stream", probe["source"])
	}
}

// TestSettleCompletionWithRecovery_NoSourceMeansNoEnqueue 守 pre-delivery
// 调用方(source="")不触发 enqueue — 非流式 settle 失败 = 500 给客户端,
// 不应进 DLQ 重 settle。
//
// Mutation:把 source==""  check 删 → 本用例 spy.calls=1 必红。
func TestSettleCompletionWithRecovery_NoSourceMeansNoEnqueue(t *testing.T) {
	settler := &postDeliveryFakeSettler{settleErr: errors.New("settle failed")}
	spy := &postDeliverySpyEnqueuer{}
	deps := ChatHandlerDeps{
		Settler:           settler,
		SettleRecoveryDLQ: spy,
	}
	event := newPostDeliveryFixtureEvent()

	_, err := settleCompletionWithRecovery(context.Background(), deps, event, settlementrecovery.Source(""))
	if err == nil {
		t.Fatal("must still propagate settle err")
	}
	if spy.calls != 0 {
		t.Fatalf("Enqueuer.Enqueue calls=%d want=0 when source=\"\"", spy.calls)
	}
}

// TestSettleCompletionWithRecovery_NilDLQDoesNotPanic 守 wire 缺时不 panic —
// 生产部署可能阶段性 wire 缺(测试环境 / 启动 race),不能因此让 stream
// handler 崩溃。
//
// Mutation:删 d.SettleRecoveryDLQ==nil check → settlementrecovery.EnqueuePayload
// 内 q==nil 返 ErrEnqueuerNil 但本测试是 helper level — 实际 helper 跳过
// settlementrecovery.EnqueuePayload 调用,nil DLQ 时不该触发。
func TestSettleCompletionWithRecovery_NilDLQDoesNotPanic(t *testing.T) {
	settler := &postDeliveryFakeSettler{settleErr: errors.New("settle failed")}
	deps := ChatHandlerDeps{
		Settler:           settler,
		SettleRecoveryDLQ: nil,
	}
	event := newPostDeliveryFixtureEvent()

	// 不应 panic,settle err 原样返回
	_, err := settleCompletionWithRecovery(context.Background(), deps, event, settlementrecovery.SourceStream)
	if err == nil {
		t.Fatal("must propagate settle err even when DLQ is nil")
	}
}

// TestSettleCompletionWithRecovery_EnqueueErrLoggedNotPropagated 守 D-4 决策:
// DLQ persist 自己失败 = money path 双环灰区 — log P0 alert 但不影响 caller
// (流式响应已发给客户端,不能反悔)。settle err 是 caller 看到的唯一 err。
//
// Mutation 1:helper 把 enqueue err 也返给 caller → caller 拿到 enqueue err
// 而不是 settle err,日志链断。
// Mutation 2:helper panic on enqueue err → 本用例 panic。
func TestSettleCompletionWithRecovery_EnqueueErrLoggedNotPropagated(t *testing.T) {
	settleErr := errors.New("settle: db serialize")
	settler := &postDeliveryFakeSettler{settleErr: settleErr}
	spy := &postDeliverySpyEnqueuer{retErr: errors.New("dlq enqueue: also down")}
	deps := ChatHandlerDeps{
		Settler:           settler,
		SettleRecoveryDLQ: spy,
	}
	event := newPostDeliveryFixtureEvent()

	_, err := settleCompletionWithRecovery(context.Background(), deps, event, settlementrecovery.SourceStream)
	if !errors.Is(err, settleErr) {
		t.Fatalf("err=%v should be settle err (not enqueue err) — caller logs settle, ops alert reads slog", err)
	}
	if spy.calls != 1 {
		t.Fatalf("Enqueuer must be called once even if it errors, got %d", spy.calls)
	}
}
