package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

// dlqCustomPayloadStubHandler 是测试用 handler — 实现 CustomDLQPayloadProvider。
type dlqCustomPayloadStubHandler struct {
	id         HandlerID
	tier       Tier
	dlqKind    dlq.EventKind
	payload    []byte
	payloadErr error
}

func (s *dlqCustomPayloadStubHandler) ID() HandlerID                                          { return s.id }
func (s *dlqCustomPayloadStubHandler) Tier() Tier                                             { return s.tier }
func (s *dlqCustomPayloadStubHandler) Order() int                                             { return 0 }
func (s *dlqCustomPayloadStubHandler) Critical() bool                                         { return true }
func (s *dlqCustomPayloadStubHandler) Timeout() time.Duration                                 { return time.Second }
func (s *dlqCustomPayloadStubHandler) DLQKind() dlq.EventKind                                 { return s.dlqKind }
func (s *dlqCustomPayloadStubHandler) Handle(_ context.Context, _ RequestCompletionEvent) error { return nil }
func (s *dlqCustomPayloadStubHandler) DLQPayload(_ RequestCompletionEvent, _ error) ([]byte, error) {
	if s.payloadErr != nil {
		return nil, s.payloadErr
	}
	return s.payload, nil
}

// dlqDefaultStubHandler 不实现 CustomDLQPayloadProvider — 验 default fallback。
type dlqDefaultStubHandler struct{ id HandlerID }

func (s *dlqDefaultStubHandler) ID() HandlerID                                          { return s.id }
func (s *dlqDefaultStubHandler) Tier() Tier                                             { return TierHigh }
func (s *dlqDefaultStubHandler) Order() int                                             { return 0 }
func (s *dlqDefaultStubHandler) Critical() bool                                         { return false }
func (s *dlqDefaultStubHandler) Timeout() time.Duration                                 { return time.Second }
func (s *dlqDefaultStubHandler) DLQKind() dlq.EventKind                                 { return dlq.EventKindUsageRecord }
func (s *dlqDefaultStubHandler) Handle(_ context.Context, _ RequestCompletionEvent) error { return nil }

// TestDLQPayload_UsesCustomProviderWhenAvailable 守 P2/P3 修复:
// handler 实现 CustomDLQPayloadProvider 时,DLQ 行 payload 必须用 handler
// 提供的 bytes(可重放业务 payload),不是 default observability 元数据。
//
// Mutation:删 dlqPayload 内 provider, ok := h.(CustomDLQPayloadProvider)
// 检测 → 本用例必红(返回 default observability payload 而非 custom)。
func TestDLQPayload_UsesCustomProviderWhenAvailable(t *testing.T) {
	customBytes := []byte(`{"source":"eventbus_billing_handler","claim":7001}`)
	h := &dlqCustomPayloadStubHandler{
		id:      "test-billing-persister",
		tier:    TierHigh,
		dlqKind: dlq.EventKindPostDeliverySettlement,
		payload: customBytes,
	}
	event := RequestCompletionEvent{ID: "evt-x", TenantID: 1, ClaimID: 7001}

	got := dlqPayload(event, h, errors.New("settle failed: db down"))
	if string(got) != string(customBytes) {
		t.Fatalf("dlqPayload should use custom provider bytes,\n got=%s\nwant=%s", got, customBytes)
	}
}

// TestDLQPayload_FallsBackOnCustomProviderError 守容错:custom provider 自己
// 出错时 dlqPayload 必须 fall through 到 default,保 DLQ 行至少有 observability
// 元数据,而不是空 payload 让 worker 拿不到 event_id / handler_id。
//
// Mutation:把 `perr == nil && len(raw) > 0` 改成 `perr == nil`(或类似)
// → custom returns nil/empty 时本用例必红。
func TestDLQPayload_FallsBackOnCustomProviderError(t *testing.T) {
	h := &dlqCustomPayloadStubHandler{
		id:         "broken-provider",
		dlqKind:    dlq.EventKindPostDeliverySettlement,
		payloadErr: errors.New("marshal failed"),
	}
	event := RequestCompletionEvent{ID: "evt-fallback", TenantID: 1, ClaimID: 1, CreatedAt: time.Now()}

	got := dlqPayload(event, h, errors.New("orig err"))
	var probe map[string]any
	if err := json.Unmarshal(got, &probe); err != nil {
		t.Fatalf("default fallback payload not valid JSON: %v / %s", err, got)
	}
	if probe["event_id"] != "evt-fallback" {
		t.Fatalf("fallback payload missing default event_id: %+v", probe)
	}
	if probe["handler_id"] != "broken-provider" {
		t.Fatalf("fallback payload missing default handler_id: %+v", probe)
	}
}

// TestDLQPayload_UsesDefaultForLegacyHandler 守不实现 provider 接口的 handler
// (现有 audit/account_health 等)继续用 default — 没有 silent regression。
//
// Mutation:把 dlqPayload 改成所有 handler 都尝试 custom → legacy handler
// 会因没实现接口而拿到空 payload → 本用例必红。
func TestDLQPayload_UsesDefaultForLegacyHandler(t *testing.T) {
	h := &dlqDefaultStubHandler{id: "legacy-audit"}
	event := RequestCompletionEvent{ID: "evt-legacy", TenantID: 1, ClaimID: 1, CreatedAt: time.Now()}

	got := dlqPayload(event, h, errors.New("legacy err"))
	var probe map[string]any
	if err := json.Unmarshal(got, &probe); err != nil {
		t.Fatalf("legacy payload not valid JSON: %v / %s", err, got)
	}
	if probe["handler_id"] != "legacy-audit" {
		t.Fatalf("legacy handler must use default payload schema, got %+v", probe)
	}
}
