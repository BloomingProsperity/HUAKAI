package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
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
	calls      int
	lastEvt    dlq.Event
	returnID   int64
	retErr     error
	lastCtxErr error // ctx.Err() 在 Enqueue 被调那一刻的快照，守 enqueue 用 fresh(非过期 settle)ctx
}

func (s *postDeliverySpyEnqueuer) Enqueue(ctx context.Context, e dlq.Event) (int64, error) {
	s.calls++
	s.lastEvt = e
	s.lastCtxErr = ctx.Err()
	if s.retErr != nil {
		return 0, s.retErr
	}
	if s.returnID == 0 {
		return 42, nil
	}
	return s.returnID, nil
}

// TestSettleCompletionWithRecovery_EnqueueUsesFreshContext 守住已交付结算超时后的恢复写
// 使用独立未过期上下文，数据库受压时仍能持久化恢复意图。
func TestSettleCompletionWithRecovery_EnqueueUsesFreshContext(t *testing.T) {
	settler := &postDeliveryFakeSettler{settleErr: errors.New("settle deadline exceeded")}
	spy := &postDeliverySpyEnqueuer{}
	deps := ChatHandlerDeps{
		Settler:           settler,
		SettleRecoveryDLQ: spy,
	}
	event := newPostDeliveryFixtureEvent()

	// 模拟 settle ctx 已过期/取消(deadline 耗尽场景)。
	expired, cancel := context.WithCancel(context.Background())
	cancel()

	_, recoveryEnqueued, err := settleCompletionWithRecovery(expired, deps, event, settlementrecovery.SourceStream)
	if err == nil {
		t.Fatal("settle err must propagate")
	}
	if spy.calls != 1 {
		t.Fatalf("enqueue calls=%d want 1 (recovery must run on deadline-exceeded settle ctx)", spy.calls)
	}
	if !recoveryEnqueued {
		t.Fatal("恢复行已成功持久化时必须向调用方报告成功入队")
	}
	if spy.lastCtxErr != nil {
		t.Fatalf("DLQ enqueue ran on already-expired settle ctx (err=%v) — recovery intent would never persist (S2 #2)", spy.lastCtxErr)
	}
}

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

// TestSettleCompletionWithRecovery_SuccessDoesNotEnqueue 守住主结算成功时不创建恢复行。
func TestSettleCompletionWithRecovery_SuccessDoesNotEnqueue(t *testing.T) {
	settler := &postDeliveryFakeSettler{settleErr: nil}
	spy := &postDeliverySpyEnqueuer{}
	deps := ChatHandlerDeps{
		Settler:           settler,
		SettleRecoveryDLQ: spy,
	}
	event := newPostDeliveryFixtureEvent()

	_, recoveryEnqueued, err := settleCompletionWithRecovery(context.Background(), deps, event, settlementrecovery.SourceStream)
	if err != nil {
		t.Fatalf("settle success path returned err=%v", err)
	}
	if spy.calls != 0 {
		t.Fatalf("Enqueuer.Enqueue calls=%d want=0 on settle success", spy.calls)
	}
	if recoveryEnqueued {
		t.Fatal("主结算成功时不得报告恢复入队")
	}
}

// TestSettleCompletionWithRecovery_DirectSettleEnqueues 守住非流式响应已交付后的结算
// 失败同样创建恢复行，避免已交付请求丢账。
func TestSettleCompletionWithRecovery_DirectSettleEnqueues(t *testing.T) {
	settler := &postDeliveryFakeSettler{settleErr: errors.New("settle db error")}
	spy := &postDeliverySpyEnqueuer{}
	deps := ChatHandlerDeps{
		Settler:           settler,
		SettleRecoveryDLQ: spy,
	}
	event := newPostDeliveryFixtureEvent()

	_, recoveryEnqueued, err := settleCompletionWithRecovery(context.Background(), deps, event, settlementrecovery.SourceDirectSettle)
	if err == nil {
		t.Fatal("settle err must propagate")
	}
	if spy.calls != 1 {
		t.Fatalf("非流式 SourceDirectSettle settle 失败必入 DLQ 恢复,enqueue calls=%d want 1", spy.calls)
	}
	if !recoveryEnqueued {
		t.Fatal("非流式结算失败且恢复持久化成功时必须报告成功入队")
	}
}

// TestAT_GW_002_16_PostDeliverySettleFailureEnqueuesRecovery 守住流式结算失败只入队
// 一次，事件类型和可重放结算载荷保持完整。
func TestAT_GW_002_16_PostDeliverySettleFailureEnqueuesRecovery(t *testing.T) {
	settler := &postDeliveryFakeSettler{settleErr: errors.New("pgx: connection reset")}
	spy := &postDeliverySpyEnqueuer{}
	deps := ChatHandlerDeps{
		Settler:           settler,
		SettleRecoveryDLQ: spy,
	}
	event := newPostDeliveryFixtureEvent()

	_, recoveryEnqueued, err := settleCompletionWithRecovery(context.Background(), deps, event, settlementrecovery.SourceStream)
	if err == nil {
		t.Fatal("settleCompletionWithRecovery must propagate settle err (caller may want to log)")
	}
	if spy.calls != 1 {
		t.Fatalf("Enqueuer.Enqueue calls=%d want=1 on post-delivery settle failure", spy.calls)
	}
	if !recoveryEnqueued {
		t.Fatal("流式结算失败且恢复持久化成功时必须报告成功入队")
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

// TestSettleCompletionWithRecovery_NoSourceMeansNoEnqueue 守住未声明交付后来源的失败
// 不创建恢复行。
func TestSettleCompletionWithRecovery_NoSourceMeansNoEnqueue(t *testing.T) {
	settler := &postDeliveryFakeSettler{settleErr: errors.New("settle failed")}
	spy := &postDeliverySpyEnqueuer{}
	deps := ChatHandlerDeps{
		Settler:           settler,
		SettleRecoveryDLQ: spy,
	}
	event := newPostDeliveryFixtureEvent()

	_, recoveryEnqueued, err := settleCompletionWithRecovery(context.Background(), deps, event, settlementrecovery.Source(""))
	if err == nil {
		t.Fatal("must still propagate settle err")
	}
	if spy.calls != 0 {
		t.Fatalf("Enqueuer.Enqueue calls=%d want=0 when source=\"\"", spy.calls)
	}
	if recoveryEnqueued {
		t.Fatal("无恢复来源时不得报告恢复入队")
	}
}

// TestSettleCompletionWithRecovery_NilDLQDoesNotPanic 守住恢复队列缺失时仍返回原结算
// 错误且不崩溃，也不得报告成功入队。
func TestSettleCompletionWithRecovery_NilDLQDoesNotPanic(t *testing.T) {
	settler := &postDeliveryFakeSettler{settleErr: errors.New("settle failed")}
	deps := ChatHandlerDeps{
		Settler:           settler,
		SettleRecoveryDLQ: nil,
	}
	event := newPostDeliveryFixtureEvent()

	// 不应 panic,settle err 原样返回
	_, recoveryEnqueued, err := settleCompletionWithRecovery(context.Background(), deps, event, settlementrecovery.SourceStream)
	if err == nil {
		t.Fatal("must propagate settle err even when DLQ is nil")
	}
	if recoveryEnqueued {
		t.Fatal("恢复队列未配置时不得报告恢复入队")
	}
}

// TestSettleCompletionWithRecovery_EnqueueErrLoggedNotPropagated 守住双失败时记录脱敏
// 高优先级信号，同时向调用方保留原结算错误并报告恢复未入队。
func TestSettleCompletionWithRecovery_EnqueueErrLoggedNotPropagated(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})

	const marker = "RAWPROMPT_SECRET_MARKER"
	const token = "sk-settle-secret-marker"
	settleErr := errors.New("settle raw prompt " + marker + " token " + token)
	settler := &postDeliveryFakeSettler{settleErr: settleErr}
	spy := &postDeliverySpyEnqueuer{retErr: errors.New("dlq enqueue raw upstream body " + marker + " bearer " + token)}
	deps := ChatHandlerDeps{
		Settler:           settler,
		SettleRecoveryDLQ: spy,
	}
	event := newPostDeliveryFixtureEvent()

	_, recoveryEnqueued, err := settleCompletionWithRecovery(context.Background(), deps, event, settlementrecovery.SourceStream)
	if !errors.Is(err, settleErr) {
		t.Fatalf("err=%v should be settle err (not enqueue err) — caller logs settle, ops alert reads slog", err)
	}
	if spy.calls != 1 {
		t.Fatalf("Enqueuer must be called once even if it errors, got %d", spy.calls)
	}
	if recoveryEnqueued {
		t.Fatal("恢复队列写失败时不得报告成功入队")
	}
	if spy.lastEvt.FailureReason != "internal_error" {
		t.Fatalf("DLQ FailureReason=%q want error class internal_error", spy.lastEvt.FailureReason)
	}
	gotLog := logs.String()
	for _, want := range []string{"req-pd-1", "money_lost_double_fault", "critical", "P0", "error_class"} {
		if !strings.Contains(gotLog, want) {
			t.Fatalf("sanitized settle recovery log missing %q: %s", want, gotLog)
		}
	}
	for _, forbidden := range []string{marker, token, "raw prompt", "raw upstream body"} {
		if strings.Contains(spy.lastEvt.FailureReason, forbidden) {
			t.Fatalf("settle recovery DLQ failure reason leaked %q: %s", forbidden, spy.lastEvt.FailureReason)
		}
		if strings.Contains(gotLog, forbidden) {
			t.Fatalf("settle recovery log leaked %q: %s", forbidden, gotLog)
		}
	}
}
