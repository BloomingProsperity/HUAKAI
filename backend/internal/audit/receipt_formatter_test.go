package audit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func mustPrepareAuditLedgerEntry(t testing.TB, ctx context.Context, entry auditledger.LedgerEntry) auditledger.PreparedEntry {
	t.Helper()
	prepared, err := auditledger.PrepareEntry(ctx, entry)
	if err != nil {
		t.Fatalf("PrepareEntry: %v", err)
	}
	return prepared
}

func TestAT_AUDIT_001_001_DeriveReceiptFromLedger(t *testing.T) {
	ctx := context.Background()
	requestID := "host/random-000001"
	createdAt := time.Date(2026, 5, 17, 14, 0, 0, 0, time.UTC)
	signer := testAuditSigner(t, 21)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	_, err = ledger.Append(ctx, mustPrepareAuditLedgerEntry(t, ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  42,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4.1-mini",
			RouteDecided:     "gpt-4.1-mini",
			UpstreamReported: "gpt-4.1-mini",
			Verdict:          "match",
		},
	}))
	if err != nil {
		t.Fatalf("append ledger: %v", err)
	}

	source := &staticReceiptSource{inputs: ReceiptInputs{
		TenantID:            42,
		Model:               "gpt-4.1-mini",
		InputTokens:         123,
		OutputTokens:        45,
		CachedTokens:        7,
		CostUSDMicros:       3210,
		RateTableSnapshotID: 9,
		CreatedAt:           createdAt,
	}}
	formatter, err := NewReceiptFormatter(ledger, nil, source, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}

	receipt, err := formatter.DeriveReceipt(ctx, requestID)
	if err != nil {
		t.Fatalf("DeriveReceipt: %v", err)
	}
	if source.seenRequestID != requestID || source.seenTenantID != 42 {
		t.Fatalf("source args mismatch: request=%s tenant=%d", source.seenRequestID, source.seenTenantID)
	}
	if receipt.RequestID != requestID || receipt.TenantID != 42 || receipt.Model != "gpt-4.1-mini" {
		t.Fatalf("identity mismatch: %+v", receipt)
	}
	if receipt.InputTokens != 123 || receipt.OutputTokens != 45 || receipt.CachedTokens != 7 {
		t.Fatalf("token mismatch: %+v", receipt)
	}
	if receipt.CostUSDMicros != 3210 || receipt.RateTableSnapshotID != 9 || !receipt.CreatedAt.Equal(createdAt) {
		t.Fatalf("cost/snapshot/time mismatch: %+v", receipt)
	}
}

func TestAT_AUDIT_001_002_SignReceiptDeterministic(t *testing.T) {
	ctx := context.Background()
	signer := testAuditSigner(t, 22)
	formatter := testFormatter(t, signer)
	receipt := &CostReceipt{
		RequestID:           "host/random-000002",
		TenantID:            77,
		Model:               "claude-3-5-sonnet",
		InputTokens:         1000,
		OutputTokens:        200,
		CachedTokens:        50,
		CostUSDMicros:       1500,
		RateTableSnapshotID: 33,
		CreatedAt:           time.Date(2026, 5, 17, 14, 30, 0, 0, time.UTC),
	}

	signed1, err := formatter.SignReceipt(ctx, receipt)
	if err != nil {
		t.Fatalf("SignReceipt first: %v", err)
	}
	signed2, err := formatter.SignReceipt(ctx, receipt)
	if err != nil {
		t.Fatalf("SignReceipt second: %v", err)
	}
	if !bytes.Equal(signed1.SignedHash, signed2.SignedHash) {
		t.Fatalf("signature must be deterministic for same canonical receipt")
	}
	if string(signed1.SignerFingerprint) != signer.Fingerprint() {
		t.Fatalf("fingerprint=%q want %q", string(signed1.SignerFingerprint), signer.Fingerprint())
	}
	canonical, err := canonicalReceiptHash(signed1)
	if err != nil {
		t.Fatalf("canonical hash: %v", err)
	}
	ok, err := signer.Verify(ctx, canonical, signed1.SignedHash, string(signed1.SignerFingerprint))
	if err != nil || !ok {
		t.Fatalf("signature verify: ok=%v err=%v", ok, err)
	}
}

func TestAT_AUDIT_001_003_AppendReceiptUniqueConstraint(t *testing.T) {
	ctx := context.Background()
	db := newMemoryReceiptDB()
	store := &ReceiptStorage{exec: db}
	receipt := &CostReceipt{
		RequestID:           "req-at-001",
		TenantID:            88,
		Model:               "gpt-4.1-mini",
		InputTokens:         10,
		OutputTokens:        5,
		CachedTokens:        0,
		CostUSDMicros:       42,
		RateTableSnapshotID: 5,
		SignerFingerprint:   []byte("0123456789abcdef"),
		SignedHash:          []byte("signed-receipt"),
		CreatedAt:           time.Date(2026, 5, 17, 15, 0, 0, 0, time.UTC),
	}
	if err := store.AppendReceipt(ctx, receipt); err != nil {
		t.Fatalf("first AppendReceipt: %v", err)
	}
	if !db.hasRequest(receipt.RequestID) {
		t.Fatalf("receipt storage must persist request_id as text string")
	}
	if err := store.AppendReceipt(ctx, receipt); !errors.Is(err, ErrReceiptDuplicate) {
		t.Fatalf("duplicate AppendReceipt: got %v want %v", err, ErrReceiptDuplicate)
	}
}

func TestAT_AUDIT_001_030_AppendOriginalAndRefundedReceiptLatest(t *testing.T) {
	ctx := context.Background()
	db := newMemoryReceiptDB()
	store := &ReceiptStorage{exec: db}
	original := receiptForStorageTest("req-at-030", 88, 0)
	refunded := cloneReceipt(original)
	refunded.ReceiptSequence = 1
	refunded.ValidationState = ReceiptValidationStateMismatchRefunded
	refunded.Verdict = ReceiptVerdictMismatchRefundPending
	refunded.AdjustmentRefs = []string{"billing_event:123"}
	refunded.SignedHash = []byte("signed-refunded")

	if err := store.AppendReceipt(ctx, original); err != nil {
		t.Fatalf("append original receipt: %v", err)
	}
	if err := store.AppendReceipt(ctx, refunded); err != nil {
		t.Fatalf("append refunded receipt: %v", err)
	}
	got, err := store.GetReceipt(ctx, original.RequestID, original.TenantID)
	if err != nil {
		t.Fatalf("GetReceipt latest: %v", err)
	}
	if got.ReceiptSequence != 1 ||
		got.ValidationState != ReceiptValidationStateMismatchRefunded ||
		got.Verdict != ReceiptVerdictMismatchRefundPending ||
		len(got.AdjustmentRefs) != 1 ||
		got.AdjustmentRefs[0] != "billing_event:123" {
		t.Fatalf("latest receipt mismatch: %+v", got)
	}
}

func TestAT_AUDIT_001_031_DuplicateSameReceiptSequenceRejected(t *testing.T) {
	ctx := context.Background()
	db := newMemoryReceiptDB()
	store := &ReceiptStorage{exec: db}
	receipt := receiptForStorageTest("req-at-031", 88, 0)
	duplicate := cloneReceipt(receipt)
	duplicate.SignedHash = []byte("signed-duplicate")

	if err := store.AppendReceipt(ctx, receipt); err != nil {
		t.Fatalf("first AppendReceipt: %v", err)
	}
	if err := store.AppendReceipt(ctx, duplicate); !errors.Is(err, ErrReceiptDuplicate) {
		t.Fatalf("duplicate same sequence got %v want %v", err, ErrReceiptDuplicate)
	}
}

func TestAT_AUDIT_001_validation_state_unknown_persisted(t *testing.T) {
	ctx := context.Background()
	db := newMemoryReceiptDB()
	store := &ReceiptStorage{exec: db}
	receipt := receiptForStorageTest("req-at-unknown-state", 88, 0)
	receipt.ValidationState = ReceiptValidationStateUnknown

	if err := store.AppendReceipt(ctx, receipt); err != nil {
		t.Fatalf("AppendReceipt unknown validation_state: %v", err)
	}
	got, err := store.GetReceipt(ctx, receipt.RequestID, receipt.TenantID)
	if err != nil {
		t.Fatalf("GetReceipt unknown validation_state: %v", err)
	}
	if got.ValidationState != ReceiptValidationStateUnknown {
		t.Fatalf("validation_state=%q want %q", got.ValidationState, ReceiptValidationStateUnknown)
	}
}

func TestAT_AUDIT_001_004_DeriveReceiptCrossesAuditBilling(t *testing.T) {
	ctx := context.Background()
	requestID := "host/random-000004"
	createdAt := time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC)
	signer := testAuditSigner(t, 24)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, mustPrepareAuditLedgerEntry(t, ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  42,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4.1-mini",
			RouteDecided:     "gpt-4.1-mini",
			UpstreamReported: "gpt-4.1-mini",
			Verdict:          "match",
		},
	})); err != nil {
		t.Fatalf("append ledger: %v", err)
	}

	query := &scriptedReceiptQueryer{row: scriptedReceiptRow{values: []any{
		int64(42),
		int64(7004),
		sql.NullString{String: "gpt-4.1-mini", Valid: true},
		sql.NullInt64{Int64: 100, Valid: true},
		sql.NullInt64{Int64: 25, Valid: true},
		sql.NullInt64{Int64: 5, Valid: true},
		"0.001234",
		sql.NullString{String: "registry:42:9;router:v1", Valid: true},
		sql.NullInt64{Int64: 77, Valid: true},
		createdAt,
	}}}
	source := &SQLReceiptSource{query: query}
	formatter, err := NewReceiptFormatter(ledger, nil, source, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}

	receipt, err := formatter.DeriveReceipt(ctx, requestID)
	if err != nil {
		t.Fatalf("DeriveReceipt: %v", err)
	}
	if receipt.TenantID != 42 || receipt.InputTokens != 100 || receipt.OutputTokens != 25 ||
		receipt.CachedTokens != 5 || receipt.CostUSDMicros != 1234 ||
		receipt.RateTableSnapshotID != 9 || !receipt.CreatedAt.Equal(createdAt) {
		t.Fatalf("receipt mismatch: %+v", receipt)
	}
	if len(query.args) != 2 || query.args[0] != requestID || query.args[1] != int64(42) {
		t.Fatalf("source args mismatch: %#v", query.args)
	}
	if !strings.Contains(query.sql, "be.audit_request_id = ale.request_id") {
		t.Fatalf("receipt SQL must join billing_events.audit_request_id to audit ledger request_id:\n%s", query.sql)
	}
	if strings.Contains(query.sql, "logical_request_id") || strings.Contains(query.sql, "request_fingerprint = ale.request_id") {
		t.Fatalf("receipt SQL must not join audit request_id to idempotency/fingerprint fields:\n%s", query.sql)
	}
	if !strings.Contains(query.sql, "LEFT JOIN usage_records") {
		t.Fatalf("receipt SQL must keep billing_events visible while usage_records is pending DLQ:\n%s", query.sql)
	}
}

func TestAT_AUDIT_001_005_RateTableSnapshotIDFromRegistryFormat(t *testing.T) {
	if got := rateTableSnapshotID("registry:42:9;router:v1", 77); got != 9 {
		t.Fatalf("registry snapshot id=%d want 9", got)
	}
	if got := rateTableSnapshotID("rate:12", 77); got != 12 {
		t.Fatalf("legacy rate snapshot id=%d want 12", got)
	}
	if got := rateTableSnapshotID("pricing:v3", 77); got != 3 {
		t.Fatalf("legacy pricing snapshot id=%d want 3", got)
	}
}

func TestAT_AUDIT_001_062_USDDecimalCostOverflowRejected(t *testing.T) {
	if _, err := usdDecimalStringToMicros("9223372036854.775808"); !errors.Is(err, ErrCostOverflow) {
		t.Fatalf("usdDecimalStringToMicros overflow error=%v want %v", err, ErrCostOverflow)
	}
}

func TestNormalizeReceiptUnknownValuesStayUnknown(t *testing.T) {
	if got := NormalizeReceiptValidationState("future_state"); got != ReceiptValidationStateUnknown {
		t.Fatalf("validation state=%q want %q", got, ReceiptValidationStateUnknown)
	}
	if got := NormalizeReceiptVerdict("future_verdict"); got != ReceiptVerdictUnknown {
		t.Fatalf("verdict=%q want %q", got, ReceiptVerdictUnknown)
	}
	if got := NormalizeReceiptValidationState(""); got != ReceiptValidationStateValid {
		t.Fatalf("empty validation state=%q want %q", got, ReceiptValidationStateValid)
	}
	if got := NormalizeReceiptVerdict(""); got != ReceiptVerdictMatch {
		t.Fatalf("empty verdict=%q want %q", got, ReceiptVerdictMatch)
	}
}

func TestAT_AUDIT_001_006_AbortReceiptZeroCost(t *testing.T) {
	ctx := context.Background()
	requestID := "host/random-000006"
	createdAt := time.Date(2026, 5, 17, 17, 10, 0, 0, time.UTC)
	signer := testAuditSigner(t, 26)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, mustPrepareAuditLedgerEntry(t, ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  42,
		ModelChain: &proto.ModelChain{
			Requested: "gpt-4.1-mini",
			Verdict:   "match",
		},
	})); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	query := &scriptedReceiptQueryer{row: scriptedReceiptRow{values: []any{
		int64(42),
		int64(7006),
		sql.NullString{String: "gpt-4.1-mini", Valid: true},
		sql.NullInt64{Int64: 0, Valid: true},
		sql.NullInt64{Int64: 0, Valid: true},
		sql.NullInt64{Int64: 0, Valid: true},
		"0.00000000",
		sql.NullString{String: "registry:42:9;router:v1", Valid: true},
		sql.NullInt64{Int64: 106, Valid: true},
		createdAt,
	}}}
	source := &SQLReceiptSource{query: query}
	formatter, err := NewReceiptFormatter(ledger, nil, source, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}

	receipt, err := formatter.DeriveReceipt(ctx, requestID)
	if err != nil {
		t.Fatalf("DeriveReceipt: %v", err)
	}
	if receipt.CostUSDMicros != 0 || receipt.InputTokens != 0 || receipt.OutputTokens != 0 ||
		receipt.CachedTokens != 0 || receipt.RateTableSnapshotID != 9 {
		t.Fatalf("abort receipt must be zero-cost with stable snapshot: %+v", receipt)
	}
	if !strings.Contains(query.sql, "claim_aborted") || !strings.Contains(query.sql, "be.audit_request_id = ale.request_id") {
		t.Fatalf("abort receipt SQL must use billing audit_request_id and include claim_aborted:\n%s", query.sql)
	}
}

func TestAT_AUDIT_001_007_UsageInDLQReturnsUnavailable(t *testing.T) {
	ctx := context.Background()
	requestID := "host/random-000007"
	signer := testAuditSigner(t, 27)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, mustPrepareAuditLedgerEntry(t, ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  42,
		ModelChain: &proto.ModelChain{
			Requested: "gpt-4.1-mini",
			Verdict:   "match",
		},
	})); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	query := &scriptedReceiptQueryer{row: scriptedReceiptRow{values: []any{
		int64(42),
		int64(7007),
		sql.NullString{String: "gpt-4.1-mini", Valid: true},
		sql.NullInt64{},
		sql.NullInt64{},
		sql.NullInt64{},
		"0.001234",
		sql.NullString{},
		sql.NullInt64{},
		time.Date(2026, 5, 17, 17, 20, 0, 0, time.UTC),
	}}}
	source := &SQLReceiptSource{query: query}
	formatter, err := NewReceiptFormatter(ledger, nil, source, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}

	_, err = formatter.DeriveReceipt(ctx, requestID)
	if !errors.Is(err, ErrReceiptUnavailable) {
		t.Fatalf("DeriveReceipt got %v want %v", err, ErrReceiptUnavailable)
	}
	if errors.Is(err, ErrReceiptInputsNotFound) {
		t.Fatalf("DLQ window must not be reported as not found: %v", err)
	}
}

func TestAT_AUDIT_001_008_RateSnapshotIDFromRegistryFormat(t *testing.T) {
	ctx := context.Background()
	requestID := "host/random-000008"
	createdAt := time.Date(2026, 5, 17, 17, 30, 0, 0, time.UTC)
	signer := testAuditSigner(t, 28)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, mustPrepareAuditLedgerEntry(t, ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  42,
		ModelChain: &proto.ModelChain{
			Requested: "gpt-4.1-mini",
			Verdict:   "match",
		},
	})); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	query := &scriptedReceiptQueryer{row: scriptedReceiptRow{values: []any{
		int64(42),
		int64(7008),
		sql.NullString{String: "gpt-4.1-mini", Valid: true},
		sql.NullInt64{Int64: 81, Valid: true},
		sql.NullInt64{Int64: 13, Valid: true},
		sql.NullInt64{Int64: 3, Valid: true},
		"0.000987",
		sql.NullString{String: "registry:42:12;router:v9", Valid: true},
		sql.NullInt64{Int64: 888, Valid: true},
		createdAt,
	}}}
	source := &SQLReceiptSource{query: query}
	formatter, err := NewReceiptFormatter(ledger, nil, source, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}

	receipt, err := formatter.DeriveReceipt(ctx, requestID)
	if err != nil {
		t.Fatalf("DeriveReceipt: %v", err)
	}
	if receipt.RateTableSnapshotID != 12 {
		t.Fatalf("rate snapshot id=%d want registry version 12", receipt.RateTableSnapshotID)
	}
}

func TestAT_AUDIT_001_009_NonUUIDRequestIDWorks(t *testing.T) {
	ctx := context.Background()
	signer := testAuditSigner(t, 25)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	source := &staticReceiptSource{inputs: ReceiptInputs{
		TenantID:            42,
		Model:               "gpt-4.1-mini",
		InputTokens:         81,
		OutputTokens:        13,
		CachedTokens:        3,
		CostUSDMicros:       987,
		RateTableSnapshotID: 11,
		CreatedAt:           time.Date(2026, 5, 17, 16, 30, 0, 0, time.UTC),
	}}
	formatter, err := NewReceiptFormatter(ledger, nil, source, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}
	db := newMemoryReceiptDB()
	store := &ReceiptStorage{exec: db}

	for _, requestID := range []string{"req-at-001", "host/abc-000001"} {
		if _, err := ledger.Append(ctx, mustPrepareAuditLedgerEntry(t, ctx, auditledger.LedgerEntry{
			RequestID: requestID,
			TenantID:  42,
			ModelChain: &proto.ModelChain{
				Requested:        "gpt-4.1-mini",
				RouteDecided:     "gpt-4.1-mini",
				UpstreamReported: "gpt-4.1-mini",
				Verdict:          "match",
			},
		})); err != nil {
			t.Fatalf("append ledger %s: %v", requestID, err)
		}
		receipt, err := formatter.DeriveReceipt(ctx, requestID)
		if err != nil {
			t.Fatalf("DeriveReceipt %s: %v", requestID, err)
		}
		if receipt.RequestID != requestID || source.seenRequestID != requestID {
			t.Fatalf("request_id must remain text: receipt=%q source=%q", receipt.RequestID, source.seenRequestID)
		}
		signed, err := formatter.SignReceipt(ctx, receipt)
		if err != nil {
			t.Fatalf("SignReceipt %s: %v", requestID, err)
		}
		if err := store.AppendReceipt(ctx, signed); err != nil {
			t.Fatalf("AppendReceipt %s: %v", requestID, err)
		}
		if !db.hasRequest(requestID) {
			t.Fatalf("receipt storage missing request_id %q", requestID)
		}
	}
}

func TestReceiptCanonicalPayloadUsesMicroUSDField(t *testing.T) {
	ctx := context.Background()
	redactor := &capturingReceiptRedactor{}
	receipt := &CostReceipt{
		RequestID:           "host/random-000005",
		TenantID:            7,
		ReceiptSequence:     2,
		Model:               "gpt-4.1-mini",
		InputTokens:         10,
		OutputTokens:        2,
		CostUSDMicros:       1234,
		RateTableSnapshotID: 8,
		CreatedAt:           time.Date(2026, 5, 17, 17, 0, 0, 0, time.UTC),
	}
	if _, err := canonicalReceiptHashWithRedactor(ctx, redactor, receipt); err != nil {
		t.Fatalf("canonicalReceiptHashWithRedactor: %v", err)
	}
	raw := string(redactor.lastRaw)
	if !strings.Contains(raw, `"cost_total_micro_usd":1234`) {
		t.Fatalf("canonical payload missing micro-USD field: %s", raw)
	}
	if !strings.Contains(raw, `"receipt_sequence":2`) {
		t.Fatalf("canonical payload missing receipt_sequence field: %s", raw)
	}
	if strings.Contains(raw, "cost_total_microcents") {
		t.Fatalf("canonical payload must not use microcents field: %s", raw)
	}
	if !strings.Contains(raw, `"validation_state":"valid"`) {
		t.Fatalf("canonical payload missing validation_state field: %s", raw)
	}
	if !strings.Contains(raw, `"verdict":"match"`) {
		t.Fatalf("canonical payload missing verdict field: %s", raw)
	}
	if !strings.Contains(raw, `"adjustment_refs":[]`) {
		t.Fatalf("canonical payload missing adjustment_refs field: %s", raw)
	}
}

type staticReceiptSource struct {
	inputs        ReceiptInputs
	err           error
	seenRequestID string
	seenTenantID  int64
}

func (s *staticReceiptSource) LookupReceiptInputs(_ context.Context, requestID string, tenantID int64) (ReceiptInputs, error) {
	s.seenRequestID = requestID
	s.seenTenantID = tenantID
	if s.err != nil {
		return ReceiptInputs{}, s.err
	}
	return s.inputs, nil
}

type memoryReceiptDB struct {
	mu       sync.Mutex
	receipts map[memoryReceiptKey]*CostReceipt
}

func newMemoryReceiptDB() *memoryReceiptDB {
	return &memoryReceiptDB{receipts: map[memoryReceiptKey]*CostReceipt{}}
}

func (db *memoryReceiptDB) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	if !strings.Contains(query, "INSERT INTO user_cost_receipts") {
		return receiptTestResult(0), nil
	}
	if len(args) < 15 {
		return nil, errors.New("memory receipt db: insert args missing")
	}
	receipt, err := receiptFromInsertArgs(args)
	if err != nil {
		return nil, err
	}
	key := memoryReceiptKey{
		tenantID:  receipt.TenantID,
		requestID: receipt.RequestID,
		sequence:  receipt.ReceiptSequence,
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if _, exists := db.receipts[key]; exists {
		return nil, ErrReceiptDuplicate
	}
	db.receipts[key] = receipt
	return receiptTestResult(1), nil
}

func (db *memoryReceiptDB) QueryRowContext(_ context.Context, query string, args ...any) receiptRow {
	if !strings.Contains(query, "FROM user_cost_receipts") || len(args) < 2 {
		return receiptTestRow{err: sql.ErrNoRows}
	}
	requestID, _ := args[0].(string)
	tenantID, _ := args[1].(int64)
	db.mu.Lock()
	defer db.mu.Unlock()
	if strings.Contains(query, "MAX(receipt_sequence)") {
		var maxSequence int32
		for key := range db.receipts {
			if key.requestID == requestID && key.tenantID == tenantID && key.sequence > maxSequence {
				maxSequence = key.sequence
			}
		}
		return receiptMaxSequenceRow{maxSequence: maxSequence}
	}
	if len(args) >= 3 && strings.Contains(query, "receipt_sequence = $3") {
		sequence, _ := args[2].(int32)
		if receipt := db.receipts[memoryReceiptKey{tenantID: tenantID, requestID: requestID, sequence: sequence}]; receipt != nil {
			return receiptTestRow{receipt: cloneReceipt(receipt)}
		}
		return receiptTestRow{err: sql.ErrNoRows}
	}
	if len(args) >= 3 && strings.Contains(query, "validation_state = $3") {
		state, _ := args[2].(string)
		return latestMemoryReceiptRow(db.receipts, requestID, tenantID, func(receipt *CostReceipt) bool {
			return NormalizeReceiptValidationState(receipt.ValidationState) == state
		})
	}
	if len(args) >= 3 && strings.Contains(query, "adjustment_refs @> $3::jsonb") {
		needle, _ := args[2].(string)
		var refs []string
		_ = json.Unmarshal([]byte(needle), &refs)
		return latestMemoryReceiptRow(db.receipts, requestID, tenantID, func(receipt *CostReceipt) bool {
			for _, ref := range refs {
				if !adjustmentRefsContain(receipt.AdjustmentRefs, ref) {
					return false
				}
			}
			return true
		})
	}
	return latestMemoryReceiptRow(db.receipts, requestID, tenantID, nil)
}

func latestMemoryReceiptRow(receipts map[memoryReceiptKey]*CostReceipt, requestID string, tenantID int64, match func(*CostReceipt) bool) receiptRow {
	var latest *CostReceipt
	for key, receipt := range receipts {
		if key.requestID != requestID || key.tenantID != tenantID {
			continue
		}
		if match != nil && !match(receipt) {
			continue
		}
		if latest == nil || receipt.ReceiptSequence > latest.ReceiptSequence {
			latest = receipt
		}
	}
	if latest == nil {
		return receiptTestRow{err: sql.ErrNoRows}
	}
	return receiptTestRow{receipt: cloneReceipt(latest)}
}

func (db *memoryReceiptDB) hasRequest(requestID string) bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	for key := range db.receipts {
		if key.requestID == requestID {
			return true
		}
	}
	return false
}

type memoryReceiptKey struct {
	tenantID  int64
	requestID string
	sequence  int32
}

type receiptTestRow struct {
	receipt *CostReceipt
	err     error
}

type receiptMaxSequenceRow struct {
	maxSequence int32
}

func (r receiptMaxSequenceRow) Scan(dest ...any) error {
	if len(dest) != 1 {
		return errors.New("memory receipt max row: destination count mismatch")
	}
	d, ok := dest[0].(*int32)
	if !ok {
		return errors.New("memory receipt max row: int32 destination required")
	}
	*d = r.maxSequence
	return nil
}

func (r receiptTestRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.receipt == nil {
		return sql.ErrNoRows
	}
	if len(dest) != 15 {
		return errors.New("memory receipt row: destination count mismatch")
	}
	adjustments, err := json.Marshal(normalizedAdjustmentRefs(r.receipt.AdjustmentRefs))
	if err != nil {
		return err
	}
	values := []any{
		r.receipt.RequestID,
		r.receipt.TenantID,
		r.receipt.Model,
		r.receipt.InputTokens,
		r.receipt.OutputTokens,
		r.receipt.CachedTokens,
		r.receipt.CostUSDMicros,
		r.receipt.RateTableSnapshotID,
		append([]byte(nil), r.receipt.SignerFingerprint...),
		append([]byte(nil), r.receipt.SignedHash...),
		r.receipt.CreatedAt,
		r.receipt.ValidationState,
		r.receipt.Verdict,
		adjustments,
		r.receipt.ReceiptSequence,
	}
	for i := range dest {
		if err := assignReceiptScanValue(dest[i], values[i]); err != nil {
			return err
		}
	}
	return nil
}

type receiptTestResult int64

func (r receiptTestResult) LastInsertId() (int64, error) { return 0, nil }
func (r receiptTestResult) RowsAffected() (int64, error) { return int64(r), nil }

func receiptForStorageTest(requestID string, tenantID int64, sequence int32) *CostReceipt {
	return &CostReceipt{
		RequestID:           requestID,
		TenantID:            tenantID,
		ReceiptSequence:     sequence,
		Model:               "gpt-4.1-mini",
		InputTokens:         10,
		OutputTokens:        5,
		CachedTokens:        0,
		CostUSDMicros:       42,
		RateTableSnapshotID: 5,
		ValidationState:     ReceiptValidationStateValid,
		Verdict:             ReceiptVerdictMatch,
		SignerFingerprint:   []byte("0123456789abcdef"),
		SignedHash:          []byte("signed-receipt"),
		CreatedAt:           time.Date(2026, 5, 17, 15, 0, 0, 0, time.UTC),
	}
}

func receiptFromInsertArgs(args []any) (*CostReceipt, error) {
	adjustments, ok := args[14].([]byte)
	if !ok {
		return nil, errors.New("memory receipt db: adjustment refs type mismatch")
	}
	return &CostReceipt{
		TenantID:            args[0].(int64),
		RequestID:           args[1].(string),
		ReceiptSequence:     args[2].(int32),
		Model:               args[3].(string),
		InputTokens:         args[4].(int64),
		OutputTokens:        args[5].(int64),
		CachedTokens:        args[6].(int64),
		CostUSDMicros:       args[7].(int64),
		RateTableSnapshotID: args[8].(int64),
		SignerFingerprint:   append([]byte(nil), args[9].([]byte)...),
		SignedHash:          append([]byte(nil), args[10].([]byte)...),
		CreatedAt:           args[11].(time.Time),
		ValidationState:     args[12].(string),
		Verdict:             args[13].(string),
		AdjustmentRefs:      decodeReceiptAdjustmentRefs(adjustments),
	}, nil
}

func assignReceiptScanValue(dest any, value any) error {
	switch d := dest.(type) {
	case *int32:
		v, ok := value.(int32)
		if !ok {
			return errors.New("memory receipt row: int32 type mismatch")
		}
		*d = v
	case *int64:
		v, ok := value.(int64)
		if !ok {
			return errors.New("memory receipt row: int64 type mismatch")
		}
		*d = v
	case *string:
		v, ok := value.(string)
		if !ok {
			return errors.New("memory receipt row: string type mismatch")
		}
		*d = v
	case *[]byte:
		v, ok := value.([]byte)
		if !ok {
			return errors.New("memory receipt row: bytes type mismatch")
		}
		*d = append([]byte(nil), v...)
	case *time.Time:
		v, ok := value.(time.Time)
		if !ok {
			return errors.New("memory receipt row: time type mismatch")
		}
		*d = v
	default:
		return errors.New("memory receipt row: unsupported destination")
	}
	return nil
}

type scriptedReceiptQueryer struct {
	sql  string
	args []any
	row  scriptedReceiptRow
}

func (q *scriptedReceiptQueryer) QueryRowContext(_ context.Context, query string, args ...any) receiptRow {
	q.sql = query
	q.args = append([]any(nil), args...)
	return q.row
}

type scriptedReceiptRow struct {
	values []any
	err    error
}

func (r scriptedReceiptRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("scripted receipt row: destination count mismatch")
	}
	for i := range dest {
		switch d := dest[i].(type) {
		case *int64:
			v, ok := r.values[i].(int64)
			if !ok {
				return errors.New("scripted receipt row: int64 type mismatch")
			}
			*d = v
		case *string:
			v, ok := r.values[i].(string)
			if !ok {
				return errors.New("scripted receipt row: string type mismatch")
			}
			*d = v
		case *sql.NullString:
			v, ok := r.values[i].(sql.NullString)
			if !ok {
				return errors.New("scripted receipt row: null string type mismatch")
			}
			*d = v
		case *sql.NullInt64:
			v, ok := r.values[i].(sql.NullInt64)
			if !ok {
				return errors.New("scripted receipt row: null int64 type mismatch")
			}
			*d = v
		case *time.Time:
			v, ok := r.values[i].(time.Time)
			if !ok {
				return errors.New("scripted receipt row: time type mismatch")
			}
			*d = v
		default:
			return errors.New("scripted receipt row: unsupported destination")
		}
	}
	return nil
}

type capturingReceiptRedactor struct {
	lastRaw []byte
}

func (r *capturingReceiptRedactor) SanitizePayload(_ context.Context, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	r.lastRaw = append([]byte(nil), raw...)
	return raw, nil
}

func (r *capturingReceiptRedactor) SanitizeError(_ context.Context, err error) (string, error) {
	if err == nil {
		return "", nil
	}
	return err.Error(), nil
}

func (r *capturingReceiptRedactor) AllowlistField(string) bool {
	return true
}

func testFormatter(t *testing.T, signer *auditledger.LocalEd25519Signer) *ReceiptFormatter {
	t.Helper()
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	formatter, err := NewReceiptFormatter(ledger, nil, &staticReceiptSource{inputs: ReceiptInputs{
		TenantID:            1,
		InputTokens:         1,
		OutputTokens:        1,
		CostUSDMicros:       1,
		RateTableSnapshotID: 1,
	}}, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}
	return formatter
}

func testAuditSigner(t *testing.T, seed byte) *auditledger.LocalEd25519Signer {
	t.Helper()
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	signer, err := auditledger.NewLocalEd25519Signer(private, nil)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return signer
}
