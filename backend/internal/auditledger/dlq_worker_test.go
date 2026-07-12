package auditledger

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func TestAuditLedgerDLQHandlerHappyPathAppendsPreparedEntry(t *testing.T) {
	// 消除的风险：重放 audit_ledger_entry 的 DLQ 记录必须走完整的
	// Append 路径，而不能只把 DLQ 行标记为已投递。
	// 变异自检：把 handler 的 Append 改成 return nil，本测试就会失败，
	// 因为 MemoryLedger 里没有对应 request_id 的行。
	ctx := context.Background()
	signer, _ := sign.GenerateKey()
	ledger, err := NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("NewMemoryLedger: %v", err)
	}
	prepared := preparedDLQFixture(t, "req_dlq_happy")

	if err := NewDLQHandler(ledger)(ctx, dlqRecordForPrepared(t, prepared)); err != nil {
		t.Fatalf("handler: %v", err)
	}

	got, err := ledger.GetByRequestID(ctx, "req_dlq_happy")
	if err != nil {
		t.Fatalf("GetByRequestID after replay: %v", err)
	}
	if got.RequestID != "req_dlq_happy" || got.TenantID != 77 || got.TenantScopeRef != "" {
		t.Fatalf("replayed ledger entry mismatch: %+v", got)
	}
	if len(got.HopChain) != 1 || got.HopChain[0].DecisionRef != "decision-req_dlq_happy" {
		t.Fatalf("replayed hop_chain mismatch: %+v", got.HopChain)
	}
	if got.ModelChain == nil || got.ModelChain.Verdict != "match" {
		t.Fatalf("replayed model_chain mismatch: %+v", got.ModelChain)
	}
}

func TestAuditLedgerDLQHandlerExistingRequestDoesNotAppendAgain(t *testing.T) {
	// 消除的风险：若 request_id 已有持久化的 ledger 行，重放必须
	// 幂等地标记为已投递，而不应再尝试第二次 Append。
	// 变异自检：删除 GetByRequestID 的已投递分支，本测试就会失败，
	// 因为 spy 记录到了一次 Append 调用。
	spy := &ledgerSpy{
		getEntry: preparedDLQFixture(t, "req_dlq_duplicate").AsLedgerEntry(),
		getErr:   nil,
	}

	err := NewDLQHandler(spy)(context.Background(), dlqRecordForPrepared(t, preparedDLQFixture(t, "req_dlq_duplicate")))
	if err != nil {
		t.Fatalf("handler returned error for existing request: %v", err)
	}
	if spy.appendCalls != 0 {
		t.Fatalf("existing request must not append again, append calls=%d", spy.appendCalls)
	}
}

func TestAuditLedgerDLQHandlerCorruptExistingRequestDoesNotAppendAgain(t *testing.T) {
	// 消除的风险：损坏的行仍能证明 request_id 存在；重放必须
	// 不创建重复的 ledger 行，损坏证据由 B-15 verification 单独
	// 处理。
	// 变异自检：把 ErrLedgerEntryCorrupt 当作 not-found 处理，本
	// 测试就会失败，因为 Append 被调用了。
	spy := &ledgerSpy{
		getEntry: preparedDLQFixture(t, "req_dlq_corrupt").AsLedgerEntry(),
		getErr:   ErrLedgerEntryCorrupt,
	}

	err := NewDLQHandler(spy)(context.Background(), dlqRecordForPrepared(t, preparedDLQFixture(t, "req_dlq_corrupt")))
	if err != nil {
		t.Fatalf("handler returned error for corrupt existing request: %v", err)
	}
	if spy.appendCalls != 0 {
		t.Fatalf("corrupt existing request must not append again, append calls=%d", spy.appendCalls)
	}
}

func TestAuditLedgerDLQHandlerAppendFailureReturnsOriginalError(t *testing.T) {
	// 消除的风险：Append 的错误必须向上传播，让 DLQ 框架能够
	// MarkFailed 并带退避重试；吞掉错误会把一条未写入的 audit
	// 意图错误地标记为已投递。
	// 变异自检：在 Append 失败后 return nil，本测试就会失败，
	// 因为 errors.Is 再也看不到 appendErr。
	appendErr := errors.New("append unavailable")
	spy := &ledgerSpy{
		getErr:    ErrLedgerEntryNotFound,
		appendErr: appendErr,
	}

	err := NewDLQHandler(spy)(context.Background(), dlqRecordForPrepared(t, preparedDLQFixture(t, "req_dlq_append_fail")))
	if !errors.Is(err, appendErr) {
		t.Fatalf("handler error=%v want appendErr", err)
	}
	if spy.appendCalls != 1 {
		t.Fatalf("append calls=%d want 1", spy.appendCalls)
	}
}

func TestAuditLedgerDLQHandlerDuplicateRaceDelivered(t *testing.T) {
	// 消除的风险：并发 worker 可能在 not-found 查询之后插入相同的
	// request_id；只有在证明已存在的行属于该 DLQ 记录所在 tenant
	// 之后，才会把 ErrDuplicateRequestID 标记为已投递。
	// 变异自检：删除重复后的二次查询，本测试就会失败，因为 spy
	// 记录到一次而不是两次 GetByRequestID 调用。
	requestID := "req_dlq_duplicate_race"
	spy := &ledgerSpy{
		getResults: []ledgerSpyGetResult{
			{err: ErrLedgerEntryNotFound},
			{entry: preparedDLQFixture(t, requestID).AsLedgerEntry()},
		},
		appendErr: ErrDuplicateRequestID,
	}

	err := NewDLQHandler(spy)(context.Background(), dlqRecordForPrepared(t, preparedDLQFixture(t, requestID)))
	if err != nil {
		t.Fatalf("duplicate race must deliver nil error, got %v", err)
	}
	if spy.appendCalls != 1 {
		t.Fatalf("append calls=%d want 1", spy.appendCalls)
	}
	if spy.getCalls != 2 {
		t.Fatalf("duplicate race must verify owner after ErrDuplicateRequestID, get calls=%d want 2", spy.getCalls)
	}
}

func TestAuditLedgerDLQHandlerRejectsCrossTenantExistingRequestID(t *testing.T) {
	// 消除的风险：request_id 全局唯一，且可能来自 client header；
	// 属于另一个 tenant 的已存在行，不能让本 tenant 的 DLQ 行在没有
	// 自己的 audit 证据时就被标记为已投递。
	// 变异自检：删除对已存在行的 tenant 归属校验，本测试就会失败，
	// 因为 handler 返回了 nil。
	const requestID = "req_dlq_cross_tenant_existing"
	spy := &ledgerSpy{
		getEntry: preparedDLQFixtureForTenant(t, requestID, 101).AsLedgerEntry(),
		getErr:   nil,
	}

	err := NewDLQHandler(spy)(context.Background(), dlqRecordForPrepared(t, preparedDLQFixtureForTenant(t, requestID, 202)))
	if err == nil || !strings.Contains(err.Error(), "duplicate request_id tenant mismatch") {
		t.Fatalf("handler error=%v want duplicate request_id tenant mismatch", err)
	}
	if spy.appendCalls != 0 {
		t.Fatalf("cross-tenant existing request must not append, append calls=%d", spy.appendCalls)
	}
}

func TestAuditLedgerDLQHandlerRejectsCrossTenantDuplicateRace(t *testing.T) {
	// 消除的风险：若 Append 在 duplicate request_id 竞争中落败，worker
	// 必须重新读取胜出的那一行，并在该行属于另一个 tenant 时拒绝它，
	// 而不是错误地把这条 DLQ 记录标记为已投递。
	// 变异自检：遇到 ErrDuplicateRequestID 直接 return nil，本测试就会
	// 失败，因为没有返回归属错误。
	const requestID = "req_dlq_cross_tenant_race"
	spy := &ledgerSpy{
		getResults: []ledgerSpyGetResult{
			{err: ErrLedgerEntryNotFound},
			{entry: preparedDLQFixtureForTenant(t, requestID, 101).AsLedgerEntry()},
		},
		appendErr: ErrDuplicateRequestID,
	}

	err := NewDLQHandler(spy)(context.Background(), dlqRecordForPrepared(t, preparedDLQFixtureForTenant(t, requestID, 202)))
	if err == nil || !strings.Contains(err.Error(), "duplicate request_id tenant mismatch") {
		t.Fatalf("handler error=%v want duplicate request_id tenant mismatch", err)
	}
	if spy.appendCalls != 1 {
		t.Fatalf("append calls=%d want 1", spy.appendCalls)
	}
	if spy.getCalls != 2 {
		t.Fatalf("duplicate race must perform second lookup, get calls=%d want 2", spy.getCalls)
	}
}

func TestAuditLedgerDLQHandlerRepreparesPersistedPayloadBeforeAppend(t *testing.T) {
	// 消除的风险：DLQ 行是持久化数据，而非活的、已封箱的
	// PreparedEntry。重放必须重新运行 PrepareEntry，这样手工篡改或
	// 入队错误的 payload 才无法把原始 prompt / key 材料签进 ledger。
	// 变异自检：把 payload 直接解码成 PreparedEntry 再 append，本测试
	// 就会失败，因为 forbidden_marker 仍留在持久化的 hop detail 里。
	const marker = "w4a-dlq-replay-marker-sk-never-persist"
	previousRedactor := ledgerRedactor
	ledgerRedactor = func() ledgerPayloadRedactor {
		return fieldLevelLedgerPayloadRedactor{blockedField: "forbidden_marker"}
	}
	defer func() { ledgerRedactor = previousRedactor }()

	ctx := context.Background()
	signer, _ := sign.GenerateKey()
	ledger, err := NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("NewMemoryLedger: %v", err)
	}
	raw, err := json.Marshal(preparedEntryJSON{
		RequestID:      "req_dlq_reprepare",
		TenantID:       77,
		CreatedAt:      "2026-05-22T13:10:00Z",
		TenantScopeRef: TenantScopeRef(77),
		HopChain: []proto.HopAttestation{{
			Hop:         proto.HopProvider,
			HopKind:     "provider",
			HopIndex:    1,
			DecisionRef: "decision-req_dlq_reprepare",
			FeatureRefs: []string{"F-DLQ-REPLAY"},
			Timestamp:   "2026-05-22T13:10:00Z",
			Provider:    "openai",
			Detail:      json.RawMessage(`{"safe_metric":"kept","forbidden_marker":"` + marker + `"}`),
		}},
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "match",
		},
	})
	if err != nil {
		t.Fatalf("Marshal persisted DLQ payload: %v", err)
	}

	err = NewDLQHandler(ledger)(ctx, dlq.Record{
		ID:             9002,
		TenantID:       77,
		EventKind:      dlq.EventKindAuditLedgerEntry,
		Lane:           dlq.LaneHigh,
		Payload:        raw,
		IdempotencyKey: "audit_ledger:req_dlq_reprepare",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	got, err := ledger.GetByRequestID(ctx, "req_dlq_reprepare")
	if err != nil {
		t.Fatalf("GetByRequestID after replay: %v", err)
	}
	if bytes.Contains(CanonicalPayload(got), []byte(marker)) || bytes.Contains(CanonicalPayload(got), []byte("forbidden_marker")) {
		t.Fatalf("replayed signed payload still contains unredacted marker: %s", CanonicalPayload(got))
	}
	if len(got.HopChain) != 1 || !bytes.Contains(got.HopChain[0].Detail, []byte(`"safe_metric":"kept"`)) {
		t.Fatalf("replay lost non-forbidden hop detail: %+v", got.HopChain)
	}
}

func TestAuditLedgerDLQHandlerRejectsMismatchedTenantScopeRefWithoutAppend(t *testing.T) {
	// 消除的风险：手工篡改的 DLQ payload 不能签入一个与 envelope
	// tenant 冲突的非空 tenant_scope_ref，否则当 DB 扫描按 tenant 推导
	// scope 时，会让持久化的证明无法验签。
	// 变异自检：删除 tenant_scope_ref 的守卫，本测试就会失败，因为
	// handler 返回 nil 且 spy 记录到一次 Append 调用。
	raw, err := json.Marshal(preparedEntryJSON{
		RequestID:      "req_dlq_scope_mismatch",
		TenantID:       77,
		CreatedAt:      "2026-05-22T13:15:00Z",
		TenantScopeRef: TenantScopeRef(78),
		HopChain: []proto.HopAttestation{{
			Hop:         proto.HopProvider,
			HopKind:     "provider",
			HopIndex:    1,
			DecisionRef: "decision-req_dlq_scope_mismatch",
			FeatureRefs: []string{"F-DLQ-REPLAY"},
			Timestamp:   "2026-05-22T13:15:00Z",
			Provider:    "openai",
			Detail:      json.RawMessage(`{"status":200}`),
		}},
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "match",
		},
	})
	if err != nil {
		t.Fatalf("Marshal mismatched-scope DLQ payload: %v", err)
	}
	spy := &ledgerSpy{getErr: ErrLedgerEntryNotFound}

	err = NewDLQHandler(spy)(context.Background(), dlq.Record{
		ID:             9005,
		TenantID:       77,
		EventKind:      dlq.EventKindAuditLedgerEntry,
		Lane:           dlq.LaneHigh,
		Payload:        raw,
		IdempotencyKey: "audit_ledger:req_dlq_scope_mismatch",
	})
	if err == nil || !strings.Contains(err.Error(), "tenant_scope_ref mismatch") {
		t.Fatalf("handler error=%v want tenant_scope_ref mismatch", err)
	}
	if spy.appendCalls != 0 {
		t.Fatalf("tenant_scope_ref mismatch must not append, append calls=%d appended tenant=%d", spy.appendCalls, spy.appendTenantID)
	}
}

func TestAuditLedgerDLQHandlerAllowsMatchingTenantScopeRefAndClearsBeforeSigning(t *testing.T) {
	// 消除的风险：来自较旧 DLQ payload 的合法非空 tenant_scope_ref
	// 只有在与已验证的 tenant 匹配时才接受，随后将其清空，使
	// canonical 签名推导出的值与 DB 扫描 / verify 之后推导的值一致。
	// 变异自检：拒绝所有非空 tenant_scope_ref 值，本测试就会在重放的
	// ledger 行出现之前失败。
	ctx := context.Background()
	signer, _ := sign.GenerateKey()
	ledger, err := NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("NewMemoryLedger: %v", err)
	}
	requestID := "req_dlq_matching_scope"
	prepared := preparedDLQFixture(t, requestID)

	err = NewDLQHandler(ledger)(ctx, dlqRecordForPrepared(t, prepared))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	got, err := ledger.GetByRequestID(ctx, requestID)
	if err != nil {
		t.Fatalf("GetByRequestID after replay: %v", err)
	}
	if got.TenantScopeRef != "" {
		t.Fatalf("matching tenant_scope_ref must be cleared before append, got %q", got.TenantScopeRef)
	}
	scanned := got
	scanned.TenantScopeRef = ""
	hash, err := EntryHash(&scanned)
	if err != nil {
		t.Fatalf("EntryHash scanned replay: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(got.Signature)
	if err != nil {
		t.Fatalf("DecodeString signature: %v", err)
	}
	if err := sign.Verify(signer.PublicKey(), hash[:], sig); err != nil {
		t.Fatalf("signature must verify after matching tenant_scope_ref is cleared: %v", err)
	}
}

func TestAuditLedgerDLQHandlerAllowsEmptyTenantScopeRefAndDerivesBeforeSigning(t *testing.T) {
	// 消除的风险：空 tenant_scope_ref 是一种合法的 DLQ payload 形态；
	// 重放必须从已验证的 tenant_id 推导出 canonical scope，并产出一个
	// DB 扫描 / verify 能够重算的 signature。
	// 变异自检：在 worker 里拒绝空 tenant_scope_ref，本测试就会在重放的
	// ledger 行出现之前失败。
	ctx := context.Background()
	signer, _ := sign.GenerateKey()
	ledger, err := NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("NewMemoryLedger: %v", err)
	}
	requestID := "req_dlq_empty_scope"
	prepared := preparedDLQFixture(t, requestID)
	raw, err := json.Marshal(preparedEntryJSON{
		RequestID:      requestID,
		TenantID:       prepared.AsLedgerEntry().TenantID,
		CreatedAt:      prepared.AsLedgerEntry().Timestamp,
		TenantScopeRef: "",
		HopChain:       prepared.AsLedgerEntry().HopChain,
		ModelChain:     prepared.AsLedgerEntry().ModelChain,
	})
	if err != nil {
		t.Fatalf("Marshal empty-scope DLQ payload: %v", err)
	}

	err = NewDLQHandler(ledger)(ctx, dlq.Record{
		ID:             9004,
		TenantID:       77,
		EventKind:      dlq.EventKindAuditLedgerEntry,
		Lane:           dlq.LaneHigh,
		Payload:        raw,
		IdempotencyKey: "audit_ledger:" + requestID,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	got, err := ledger.GetByRequestID(ctx, requestID)
	if err != nil {
		t.Fatalf("GetByRequestID after replay: %v", err)
	}
	scanned := got
	scanned.TenantScopeRef = ""
	hash, err := EntryHash(&scanned)
	if err != nil {
		t.Fatalf("EntryHash scanned replay: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(got.Signature)
	if err != nil {
		t.Fatalf("DecodeString signature: %v", err)
	}
	if err := sign.Verify(signer.PublicKey(), hash[:], sig); err != nil {
		t.Fatalf("signature must verify after scan drops tenant_scope_ref: %v", err)
	}
}

func TestAuditLedgerDLQHandlerReplaysCredentialWorkerPayloadWithoutHopChain(t *testing.T) {
	// 消除的风险：credentialworker 的 audit 条目合法地不准备任何
	// HopChain。DLQ 重放不能在 decode 时卡住这些行；它必须重新运行
	// PrepareEntry，Append 该条目，并返回已投递。
	// 变异自检：恢复旧的 empty-hop_chain decode 守卫，本测试就会失败，
	// 因为 handler 在 ledger 行写入之前返回了 decode 错误。
	ctx := context.Background()
	signer, _ := sign.GenerateKey()
	ledger, err := NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("NewMemoryLedger: %v", err)
	}
	raw := []byte(`{"request_id":"req_dlq_credentialworker_no_hop_chain","tenant_id":77,"created_at":"2026-05-22T13:30:00Z","tenant_scope_ref":"` + TenantScopeRef(77) + `"}`)

	err = NewDLQHandler(ledger)(ctx, dlq.Record{
		ID:             9006,
		TenantID:       77,
		EventKind:      dlq.EventKindAuditLedgerEntry,
		Lane:           dlq.LaneHigh,
		Payload:        raw,
		IdempotencyKey: "audit_ledger:req_dlq_credentialworker_no_hop_chain",
	})
	if err != nil {
		t.Fatalf("handler should replay credentialworker-shaped payload: %v", err)
	}

	got, err := ledger.GetByRequestID(ctx, "req_dlq_credentialworker_no_hop_chain")
	if err != nil {
		t.Fatalf("GetByRequestID after credentialworker replay: %v", err)
	}
	if got.RequestID != "req_dlq_credentialworker_no_hop_chain" || got.TenantID != 77 {
		t.Fatalf("replayed credentialworker ledger entry mismatch: %+v", got)
	}
	if got.Timestamp != "2026-05-22T13:30:00Z" {
		t.Fatalf("credentialworker timestamp=%q want created_at", got.Timestamp)
	}
	if len(got.HopChain) != 0 {
		t.Fatalf("credentialworker replay must preserve empty hop_chain, got %+v", got.HopChain)
	}
	if got.ModelChain != nil {
		t.Fatalf("credentialworker replay must preserve nil model_chain, got %+v", got.ModelChain)
	}
}

func TestAuditLedgerDLQHandlerIdempotencyKeyMustMatchPayloadRequestID(t *testing.T) {
	// 消除的风险：DLQ 重放不能让一个错误的 payload request_id 把重复
	// 检测与 append 引向偏离 envelope 请求身份的方向。
	// 变异自检：删除 idempotency/request_id 守卫，本测试就会失败，因为
	// handler 返回 nil 且 Append 是针对 payload id 调用的。
	spy := &ledgerSpy{getErr: ErrLedgerEntryNotFound}

	err := NewDLQHandler(spy)(context.Background(), dlqRecordForPreparedWithKey(t,
		preparedDLQFixture(t, "req_dlq_payload_id"),
		"audit_ledger:req_dlq_envelope_id",
	))
	if err == nil || !strings.Contains(err.Error(), "idempotency/request_id mismatch") {
		t.Fatalf("handler error=%v want idempotency/request_id mismatch", err)
	}
	if spy.appendCalls != 0 {
		t.Fatalf("mismatched request_id must not append, append calls=%d", spy.appendCalls)
	}
}

func TestAuditLedgerDLQHandlerTenantMismatchReturnsErrorWithoutAppend(t *testing.T) {
	// 消除的风险：错误的入队或手工篡改的 DLQ 行，不能让 payload
	// 里的 tenant 覆盖 DLQ envelope 的 tenant，从而创建跨 tenant 的
	// audit 证据。
	// 变异自检：删除 tenant mismatch 守卫，本测试就会失败，因为
	// handler 返回 nil 且 spy 记录到一次针对 tenant 8 的 Append。
	raw, err := json.Marshal(preparedEntryJSON{
		RequestID:      "req_dlq_tenant_mismatch",
		TenantID:       8,
		CreatedAt:      "2026-05-22T13:20:00Z",
		TenantScopeRef: TenantScopeRef(8),
		HopChain: []proto.HopAttestation{{
			Hop:         proto.HopProvider,
			HopKind:     "provider",
			HopIndex:    1,
			DecisionRef: "decision-req_dlq_tenant_mismatch",
			FeatureRefs: []string{"F-DLQ-REPLAY"},
			Timestamp:   "2026-05-22T13:20:00Z",
			Provider:    "openai",
			Detail:      json.RawMessage(`{"status":200}`),
		}},
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "match",
		},
	})
	if err != nil {
		t.Fatalf("Marshal tenant mismatch payload: %v", err)
	}
	spy := &ledgerSpy{getErr: ErrLedgerEntryNotFound}

	err = NewDLQHandler(spy)(context.Background(), dlq.Record{
		ID:             9003,
		TenantID:       7,
		EventKind:      dlq.EventKindAuditLedgerEntry,
		Lane:           dlq.LaneHigh,
		Payload:        raw,
		IdempotencyKey: "audit_ledger:req_dlq_tenant_mismatch",
	})
	if err == nil || !strings.Contains(err.Error(), "tenant mismatch") {
		t.Errorf("handler error=%v want tenant mismatch", err)
	}
	if spy.appendCalls != 0 {
		t.Fatalf("tenant mismatch must not append, append calls=%d appended tenant=%d", spy.appendCalls, spy.appendTenantID)
	}
}

func preparedDLQFixture(t testing.TB, requestID string) PreparedEntry {
	t.Helper()
	return preparedDLQFixtureForTenant(t, requestID, 77)
}

func preparedDLQFixtureForTenant(t testing.TB, requestID string, tenantID int64) PreparedEntry {
	t.Helper()
	return mustPrepareForAppend(t, context.Background(), LedgerEntry{
		Timestamp:      "2026-05-22T13:00:00Z",
		RequestID:      requestID,
		TenantID:       tenantID,
		TenantScopeRef: TenantScopeRef(tenantID),
		HopChain: []proto.HopAttestation{{
			Hop:         proto.HopProvider,
			HopKind:     "provider",
			HopIndex:    1,
			DecisionRef: "decision-" + requestID,
			FeatureRefs: []string{"F-DLQ-REPLAY"},
			Timestamp:   "2026-05-22T13:00:00Z",
			Provider:    "openai",
			Detail:      json.RawMessage(`{"status":200}`),
		}},
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "match",
		},
	})
}

func dlqRecordForPrepared(t testing.TB, prepared PreparedEntry) dlq.Record {
	return dlqRecordForPreparedWithKey(t, prepared, "audit_ledger:"+prepared.AsLedgerEntry().RequestID)
}

func dlqRecordForPreparedWithKey(t testing.TB, prepared PreparedEntry, idempotencyKey string) dlq.Record {
	t.Helper()
	raw, err := json.Marshal(prepared)
	if err != nil {
		t.Fatalf("Marshal PreparedEntry: %v", err)
	}
	return dlq.Record{
		ID:             9001,
		TenantID:       prepared.AsLedgerEntry().TenantID,
		EventKind:      dlq.EventKindAuditLedgerEntry,
		Lane:           dlq.LaneHigh,
		Payload:        raw,
		IdempotencyKey: idempotencyKey,
	}
}

type ledgerSpyGetResult struct {
	entry LedgerEntry
	err   error
}

type ledgerSpy struct {
	getEntry       LedgerEntry
	getErr         error
	getResults     []ledgerSpyGetResult
	getCalls       int
	appendErr      error
	appendCalls    int
	appendTenantID int64
}

func (s *ledgerSpy) Append(_ context.Context, entry PreparedEntry) (LedgerEntry, error) {
	s.appendCalls++
	s.appendTenantID = entry.AsLedgerEntry().TenantID
	return LedgerEntry{}, s.appendErr
}

func (s *ledgerSpy) GetByRequestID(context.Context, string) (LedgerEntry, error) {
	if len(s.getResults) > 0 {
		idx := s.getCalls
		s.getCalls++
		if idx >= len(s.getResults) {
			idx = len(s.getResults) - 1
		}
		result := s.getResults[idx]
		return result.entry, result.err
	}
	s.getCalls++
	return s.getEntry, s.getErr
}

func (s *ledgerSpy) GetByRequestIDAndTenantScope(context.Context, string, string) (LedgerEntry, error) {
	return LedgerEntry{}, ErrLedgerEntryNotFound
}

func (s *ledgerSpy) LatestMerkleRoot(context.Context) ([32]byte, error) {
	return ZeroRoot, nil
}

func (s *ledgerSpy) Size(context.Context) int {
	return 0
}
