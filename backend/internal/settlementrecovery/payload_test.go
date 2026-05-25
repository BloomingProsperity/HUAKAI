package settlementrecovery

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

// fixtureCompletionEvent 构造非空 RequestCompletionEvent,所有 SettleRequest
// 字段都赋判别值,验 round-trip 不丢字段。
func fixtureCompletionEvent(t *testing.T) eventbus.RequestCompletionEvent {
	t.Helper()
	tok := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	requestedAt := time.Date(2026, 5, 24, 10, 30, 0, 0, time.UTC)
	streamAttempt := &billing.Attempt{
		State:                  billing.StreamStatePartial,
		DeliveredTokenCount:    1234,
		StreamTerminatedReason: "stream_complete",
	}
	return eventbus.RequestCompletionEvent{
		ID:                "evt-123",
		RequestID:         "req-456",
		AuditLedgerDLQRef: "audit-dlq-7",
		SettleRequest: billing.SettleRequest{
			ClaimID:             7001,
			AccountID:           9001,
			AcquisitionToken:    tok,
			UsageRecordPayload:  []byte(`{"u":1}`),
			BillingEventPayload: []byte(`{"b":2}`),
			ActualCost:          decimal.RequireFromString("0.12345678"),
			TenantID:            3001,
			APIKeyID:            4001,
			UserID:              5001,
			ProviderAccountID:   6001,
			AttemptSeq:          2,
			RequestedModel:      "claude-3-5-sonnet",
			RequestedAt:         requestedAt,
			UpstreamModel:       "claude-sonnet-up",
			Provider:            "anthropic",
			Stream:              true,
			Draft:               gateway.UsageRecordDraft{TokensInput: 100, TokensOutput: 200},
			StreamAttempt:       streamAttempt,
			Fingerprint:         "fp-xyz",
			AuditRequestID:      "audit-req-7",
			OutboxEmitter:       func() bool { return true }, // 不应被持久化
			SnapshotVersion:     "registry:3001:v9;router:rv2",
		},
	}
}

// TestPayload_RoundTrip 守 payload 字段对齐 — FromCompletionEvent + Encode +
// Decode + ToSettleRequest 后所有非 func 字段必须 byte-identical 回 SettleRequest。
//
// Mutation:把 FromCompletionEvent 漏一个字段(比如 SnapshotVersion 不赋值),
// ToSettleRequest 出来的 req 该字段为空 → 本用例必红。
func TestPayload_RoundTrip_SettleRequestFieldsByteIdentical(t *testing.T) {
	event := fixtureCompletionEvent(t)
	original := event.SettleRequest

	payload := FromCompletionEvent(SourceStream, event)
	raw, err := payload.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	got := decoded.ToSettleRequest()

	if got.ClaimID != original.ClaimID {
		t.Fatalf("ClaimID: got=%d want=%d", got.ClaimID, original.ClaimID)
	}
	if got.AccountID != original.AccountID {
		t.Fatalf("AccountID: got=%d want=%d", got.AccountID, original.AccountID)
	}
	if got.AcquisitionToken != original.AcquisitionToken {
		t.Fatalf("AcquisitionToken: got=%v want=%v", got.AcquisitionToken, original.AcquisitionToken)
	}
	if string(got.UsageRecordPayload) != string(original.UsageRecordPayload) {
		t.Fatalf("UsageRecordPayload mismatch: got=%s want=%s", got.UsageRecordPayload, original.UsageRecordPayload)
	}
	if string(got.BillingEventPayload) != string(original.BillingEventPayload) {
		t.Fatalf("BillingEventPayload mismatch: got=%s want=%s", got.BillingEventPayload, original.BillingEventPayload)
	}
	if !got.ActualCost.Equal(original.ActualCost) {
		t.Fatalf("ActualCost: got=%s want=%s", got.ActualCost.String(), original.ActualCost.String())
	}
	if got.TenantID != original.TenantID || got.APIKeyID != original.APIKeyID || got.UserID != original.UserID {
		t.Fatalf("tenant/api/user mismatch: got=(%d,%d,%d) want=(%d,%d,%d)",
			got.TenantID, got.APIKeyID, got.UserID,
			original.TenantID, original.APIKeyID, original.UserID)
	}
	if got.ProviderAccountID != original.ProviderAccountID {
		t.Fatalf("ProviderAccountID: got=%d want=%d", got.ProviderAccountID, original.ProviderAccountID)
	}
	if got.AttemptSeq != original.AttemptSeq {
		t.Fatalf("AttemptSeq: got=%d want=%d", got.AttemptSeq, original.AttemptSeq)
	}
	if got.RequestedModel != original.RequestedModel || got.UpstreamModel != original.UpstreamModel || got.Provider != original.Provider {
		t.Fatalf("model/provider mismatch: %+v vs %+v", got, original)
	}
	if !got.RequestedAt.Equal(original.RequestedAt) {
		t.Fatalf("RequestedAt: got=%v want=%v", got.RequestedAt, original.RequestedAt)
	}
	if got.Stream != original.Stream {
		t.Fatalf("Stream: got=%v want=%v", got.Stream, original.Stream)
	}
	if got.Draft.TokensInput != original.Draft.TokensInput || got.Draft.TokensOutput != original.Draft.TokensOutput {
		t.Fatalf("Draft: got=%+v want=%+v", got.Draft, original.Draft)
	}
	if got.StreamAttempt == nil || got.StreamAttempt.DeliveredTokenCount != original.StreamAttempt.DeliveredTokenCount {
		t.Fatalf("StreamAttempt: got=%+v want=%+v", got.StreamAttempt, original.StreamAttempt)
	}
	if got.Fingerprint != original.Fingerprint || got.AuditRequestID != original.AuditRequestID || got.SnapshotVersion != original.SnapshotVersion {
		t.Fatalf("fingerprint/audit/snapshot mismatch: got=(%q,%q,%q) want=(%q,%q,%q)",
			got.Fingerprint, got.AuditRequestID, got.SnapshotVersion,
			original.Fingerprint, original.AuditRequestID, original.SnapshotVersion)
	}
}

// TestPayload_ToSettleRequest_OutboxEmitterIsNil 守 codex D 决策:重 settle 时
// OutboxEmitter 必 nil,防与原 attempt 已 emit 的 cross-threshold scheduler
// outbox 事件重复 emit account_quota_changed。
//
// Mutation:把 ToSettleRequest 改成把原 func 也填回去 → 本用例必红。
func TestPayload_ToSettleRequest_OutboxEmitterIsNil(t *testing.T) {
	event := fixtureCompletionEvent(t)
	if event.SettleRequest.OutboxEmitter == nil {
		t.Fatal("fixture should have OutboxEmitter set to verify it gets stripped")
	}
	payload := FromCompletionEvent(SourceStream, event)
	got := payload.ToSettleRequest()
	if got.OutboxEmitter != nil {
		t.Fatalf("OutboxEmitter must be nil after round-trip, got non-nil (would re-emit cross-threshold outbox)")
	}
}

// TestPayload_JSONShape_NoOutboxEmitterField 守 settleRequestPersisted 不带
// OutboxEmitter 字段,即使 marshal 出错也不会持久 func。
func TestPayload_JSONShape_NoOutboxEmitterField(t *testing.T) {
	event := fixtureCompletionEvent(t)
	payload := FromCompletionEvent(SourceStream, event)
	raw, err := payload.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("probe unmarshal: %v", err)
	}
	settle, ok := probe["settle"].(map[string]any)
	if !ok {
		t.Fatalf("settle key missing or wrong type: %v", probe["settle"])
	}
	if _, present := settle["outbox_emitter"]; present {
		t.Fatalf("settle.outbox_emitter must not be serialized: %+v", settle)
	}
	if _, present := settle["OutboxEmitter"]; present {
		t.Fatalf("settle.OutboxEmitter must not be serialized: %+v", settle)
	}
}

// TestValidate_RejectsInvalidSource Mutation: 把 switch case 改成 default 接受
// → 本用例必红。
func TestValidate_RejectsInvalidSource(t *testing.T) {
	p := Payload{Source: "bogus", Settle: settleRequestPersisted{ClaimID: 1, TenantID: 1}}
	if err := p.Validate(); err == nil {
		t.Fatal("Validate must reject unknown Source")
	}
}

// TestValidate_RejectsMissingClaimID Mutation: 删 ClaimID==0 check → 红。
func TestValidate_RejectsMissingClaimID(t *testing.T) {
	p := Payload{Source: SourceStream, Settle: settleRequestPersisted{ClaimID: 0, TenantID: 1}}
	if err := p.Validate(); err == nil {
		t.Fatal("Validate must reject ClaimID==0")
	}
}

// TestValidate_RejectsMissingTenantID Mutation: 删 TenantID==0 check → 红。
func TestValidate_RejectsMissingTenantID(t *testing.T) {
	p := Payload{Source: SourceStream, Settle: settleRequestPersisted{ClaimID: 1, TenantID: 0}}
	if err := p.Validate(); err == nil {
		t.Fatal("Validate must reject TenantID==0")
	}
}

// TestPayload_FromCompletionEvent_NormalizesEmptyAuditRequestID
//
// Owner P2 finding (2026-05-24) 判别 fixture:event.RequestID 有值,但
// SettleRequest.AuditRequestID 为空(stream 路径就是这样,见
// chat_completions_stream.go:542 没填 AuditRequestID);settleCompletion
// 在栈本地副本上补,recovery payload 取的是外层 event,会得到空 audit_request_id
// → worker 重放写 NULL → audit/receipt 链断。
//
// Mutation 自检:删 FromCompletionEvent 里的 `if auditRequestID == "" { = event.RequestID }`
// → payload.Settle.AuditRequestID 为空,本用例必 red。
func TestPayload_FromCompletionEvent_NormalizesEmptyAuditRequestID(t *testing.T) {
	event := fixtureCompletionEvent(t)
	// 模拟 stream 路径:event.RequestID 有值,但 SettleRequest.AuditRequestID 空。
	event.SettleRequest.AuditRequestID = ""
	if event.RequestID == "" {
		t.Fatalf("fixture event.RequestID must be non-empty for this test")
	}

	payload := FromCompletionEvent(SourceStream, event)

	if payload.Settle.AuditRequestID != event.RequestID {
		t.Fatalf("FromCompletionEvent MUST normalize empty SettleRequest.AuditRequestID to event.RequestID; "+
			"got %q want %q (event.RequestID)", payload.Settle.AuditRequestID, event.RequestID)
	}

	// 端到端验:Encode + Decode 后 ToSettleRequest 出来的 SettleRequest 也带规范化值。
	raw, err := payload.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	req := decoded.ToSettleRequest()
	if req.AuditRequestID != event.RequestID {
		t.Fatalf("round-trip ToSettleRequest.AuditRequestID drift: got %q want %q",
			req.AuditRequestID, event.RequestID)
	}
}

// TestPayload_FromCompletionEvent_PreservesExplicitAuditRequestID
//
// 守 defense-in-depth 规范化**不应**覆盖 caller 显式填的值。non-stream 路径
// (chat_completions_handler_headers.go:264)/billing_persister_handler 都
// 自己规范化过 SettleRequest.AuditRequestID,FromCompletionEvent 不应再二次覆盖。
//
// Mutation 自检:把 normalize 改成 `auditRequestID = event.RequestID` 无条件赋值
// → 本用例必 red (preserve 失效)。
func TestPayload_FromCompletionEvent_PreservesExplicitAuditRequestID(t *testing.T) {
	event := fixtureCompletionEvent(t)
	if event.SettleRequest.AuditRequestID == "" {
		t.Fatalf("fixture SettleRequest.AuditRequestID must be non-empty for this test")
	}
	if event.RequestID == event.SettleRequest.AuditRequestID {
		t.Fatalf("fixture must use different values to be discriminating")
	}
	explicit := event.SettleRequest.AuditRequestID

	payload := FromCompletionEvent(SourceStream, event)

	if payload.Settle.AuditRequestID != explicit {
		t.Fatalf("FromCompletionEvent must preserve explicit SettleRequest.AuditRequestID; "+
			"got %q want %q", payload.Settle.AuditRequestID, explicit)
	}
}
