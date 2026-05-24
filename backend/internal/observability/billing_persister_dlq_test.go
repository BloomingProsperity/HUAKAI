package observability

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
)

// TestBillingPersisterHandler_DLQKindIsPostDeliverySettlement 守 P2/P3 修复:
// billing persister 失败时 DLQ 行的 event_kind 必须是 post_delivery_settlement,
// 让 settlementrecovery worker 能拿到,而不是被 generic usage_record worker
// 当成普通 usage 行重写。
//
// Mutation:把 DLQKind() 改回 dlq.EventKindUsageRecord → 本用例必红
// (DLQ 行被错路由,worker 跑普通 usage replay 不会重 settle 整个 claim)。
func TestBillingPersisterHandler_DLQKindIsPostDeliverySettlement(t *testing.T) {
	h := &BillingPersisterHandler{}
	got := h.DLQKind()
	if got != dlq.EventKindPostDeliverySettlement {
		t.Fatalf("DLQKind = %q, want %q (settlementrecovery routing depends on this)",
			got, dlq.EventKindPostDeliverySettlement)
	}
}

// TestBillingPersisterHandler_DLQPayloadIsReplayable 守 codex D 决策:
// DLQ 行 payload 必须是 settlementrecovery.Payload 编码,settlementrecovery
// worker decode 后能拿到 SettleRequest 重调 Settler.Settle。
//
// Mutation 1:把 DLQPayload 改成返 nil(让 eventbus 走 default observability
// payload)→ Decode 失败或 ToSettleRequest 拿空字段 → 本用例必红。
// Mutation 2:Source 设错(不是 eventbus_billing_handler)→ Validate 红。
func TestBillingPersisterHandler_DLQPayloadIsReplayable(t *testing.T) {
	h := &BillingPersisterHandler{}
	tok := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	event := eventbus.RequestCompletionEvent{
		ID:        "evt-replay",
		RequestID: "req-replay",
		SettleRequest: billing.SettleRequest{
			ClaimID:          8002,
			TenantID:         3002,
			AccountID:        9002,
			AcquisitionToken: tok,
			ActualCost:       decimal.RequireFromString("0.42000000"),
			APIKeyID:         4002,
			UserID:           5002,
			RequestedModel:   "test-model",
			RequestedAt:      time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC),
			Stream:           true,
			Draft:            gateway.UsageRecordDraft{TokensInput: 50, TokensOutput: 60},
			AuditRequestID:   "audit-rep-1",
			SnapshotVersion:  "registry:3002:v3;router:rv1",
		},
	}

	raw, err := h.DLQPayload(event, errors.New("settle failed"))
	if err != nil {
		t.Fatalf("DLQPayload err=%v", err)
	}
	if len(raw) == 0 {
		t.Fatal("DLQPayload returned empty bytes — eventbus would fall back to non-replayable default")
	}

	// 验:Decode + Validate + ToSettleRequest 能拿回原字段
	decoded, err := settlementrecovery.Decode(raw)
	if err != nil {
		t.Fatalf("settlementrecovery.Decode: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded payload Validate: %v", err)
	}
	req := decoded.ToSettleRequest()
	if req.ClaimID != 8002 {
		t.Fatalf("decoded ClaimID=%d want=8002 (round-trip lost field)", req.ClaimID)
	}
	if req.TenantID != 3002 {
		t.Fatalf("decoded TenantID=%d want=3002", req.TenantID)
	}
	if req.OutboxEmitter != nil {
		t.Fatalf("ToSettleRequest must strip OutboxEmitter (prevent re-emit)")
	}

	// 验:Source 标识正确,worker observability 能区分 enqueue 入口
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("probe unmarshal: %v", err)
	}
	if probe["source"] != string(settlementrecovery.SourceEventbusBillingHandler) {
		t.Fatalf("payload.source=%v want=%q", probe["source"], settlementrecovery.SourceEventbusBillingHandler)
	}
}

// TestBillingPersisterHandler_DLQPayload_NoOutboxEmitterPersisted 守 codex
// 关键 bug fix:SettleRequest.OutboxEmitter 是 func() bool 不可 JSON 化,
// 必须 strip。worker decode 后 OutboxEmitter 必 nil,防 cross-threshold
// outbox 重复 emit。
//
// 同 settlementrecovery package 的 payload_test.go 同名守护,这里加一层在
// observability 包内验集成边界 — 万一未来 BillingPersister.DLQPayload 改
// 实现绕过 settlementrecovery.FromCompletionEvent,本用例也能拦住。
func TestBillingPersisterHandler_DLQPayload_NoOutboxEmitterPersisted(t *testing.T) {
	h := &BillingPersisterHandler{}
	event := eventbus.RequestCompletionEvent{
		SettleRequest: billing.SettleRequest{
			ClaimID:       1,
			TenantID:      1,
			OutboxEmitter: func() bool { return true },
		},
	}
	raw, err := h.DLQPayload(event, nil)
	if err != nil {
		t.Fatalf("DLQPayload: %v", err)
	}
	if string(raw) == "" {
		t.Fatal("empty payload")
	}
	// JSON 解出后 settle 子对象不应出现 outbox_emitter / OutboxEmitter key
	var probe struct {
		Settle map[string]any `json:"settle"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if _, present := probe.Settle["outbox_emitter"]; present {
		t.Fatalf("outbox_emitter must not be persisted in DLQ payload: %+v", probe.Settle)
	}
	if _, present := probe.Settle["OutboxEmitter"]; present {
		t.Fatalf("OutboxEmitter must not be persisted: %+v", probe.Settle)
	}
}
