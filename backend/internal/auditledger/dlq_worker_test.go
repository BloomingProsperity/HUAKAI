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
	// Risk killed: replaying an audit_ledger_entry DLQ record must run the full
	// Append path, not merely mark the DLQ row delivered.
	// Mutation self-check: replace handler Append with return nil and this test
	// fails because MemoryLedger has no row for the request_id.
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
	// Risk killed: if request_id already has a durable ledger row, replay must
	// be idempotently delivered without a second Append attempt.
	// Mutation self-check: remove the GetByRequestID delivered branch and this
	// test fails because the spy records an Append call.
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
	// Risk killed: a corrupt row still proves the request_id exists; replay must
	// not create a duplicate ledger row while B-15 verification handles corrupt
	// evidence separately.
	// Mutation self-check: treat ErrLedgerEntryCorrupt like not-found and this
	// test fails because Append is called.
	spy := &ledgerSpy{getErr: ErrLedgerEntryCorrupt}

	err := NewDLQHandler(spy)(context.Background(), dlqRecordForPrepared(t, preparedDLQFixture(t, "req_dlq_corrupt")))
	if err != nil {
		t.Fatalf("handler returned error for corrupt existing request: %v", err)
	}
	if spy.appendCalls != 0 {
		t.Fatalf("corrupt existing request must not append again, append calls=%d", spy.appendCalls)
	}
}

func TestAuditLedgerDLQHandlerAppendFailureReturnsOriginalError(t *testing.T) {
	// Risk killed: Append errors must propagate so the DLQ framework can
	// MarkFailed and retry with backoff; swallowing the error falsely delivers
	// an unwritten audit intent.
	// Mutation self-check: return nil after Append failure and this test fails
	// because errors.Is no longer sees appendErr.
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
	// Risk killed: a concurrent worker may insert the same request_id after the
	// not-found lookup; ErrDuplicateRequestID is the success race, not a retry.
	// Mutation self-check: remove the duplicate branch and this test fails with
	// ErrDuplicateRequestID.
	spy := &ledgerSpy{
		getErr:    ErrLedgerEntryNotFound,
		appendErr: ErrDuplicateRequestID,
	}

	err := NewDLQHandler(spy)(context.Background(), dlqRecordForPrepared(t, preparedDLQFixture(t, "req_dlq_duplicate_race")))
	if err != nil {
		t.Fatalf("duplicate race must deliver nil error, got %v", err)
	}
	if spy.appendCalls != 1 {
		t.Fatalf("append calls=%d want 1", spy.appendCalls)
	}
}

func TestAuditLedgerDLQHandlerRepreparesPersistedPayloadBeforeAppend(t *testing.T) {
	// Risk killed: a DLQ row is persisted data, not a live sealed
	// PreparedEntry. Replay must run PrepareEntry again so hand-edited or bad
	// enqueue payloads cannot sign raw prompt/key material into the ledger.
	// Mutation self-check: decoding the payload directly to PreparedEntry and
	// appending it makes this test fail because forbidden_marker remains in the
	// persisted hop detail.
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
	// Risk killed: a hand-edited DLQ payload must not sign a non-empty
	// tenant_scope_ref that conflicts with the envelope tenant and later makes
	// the persisted proof unverifiable when the DB scan derives scope by tenant.
	// Mutation self-check: remove the tenant_scope_ref guard and this test fails
	// because the handler returns nil and the spy records an Append call.
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

func TestAuditLedgerDLQHandlerAllowsEmptyTenantScopeRefAndDerivesBeforeSigning(t *testing.T) {
	// Risk killed: empty tenant_scope_ref is a valid DLQ payload shape; replay
	// must derive the canonical scope from the already-verified tenant_id and
	// produce a signature that DB scan/verify can recompute.
	// Mutation self-check: reject empty tenant_scope_ref in the worker and this
	// test fails before the replayed ledger row exists.
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

func TestAuditLedgerDLQHandlerIdempotencyKeyMustMatchPayloadRequestID(t *testing.T) {
	// Risk killed: DLQ replay must not let a bad payload request_id steer
	// duplicate detection and append away from the envelope's request identity.
	// Mutation self-check: remove the idempotency/request_id guard and this test
	// fails because the handler returns nil and Append is called for payload id.
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
	// Risk killed: a bad enqueue or hand-edited DLQ row must not let the
	// payload tenant override the DLQ envelope tenant and create cross-tenant
	// audit evidence.
	// Mutation self-check: remove the tenant mismatch guard and this test fails
	// because the handler returns nil and the spy records an Append for tenant 8.
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
	return mustPrepareForAppend(t, context.Background(), LedgerEntry{
		Timestamp:      "2026-05-22T13:00:00Z",
		RequestID:      requestID,
		TenantID:       77,
		TenantScopeRef: TenantScopeRef(77),
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

type ledgerSpy struct {
	getEntry       LedgerEntry
	getErr         error
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
