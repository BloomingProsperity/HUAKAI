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

func TestAT_AUDIT_001_001_DeriveReceiptFromLedger(t *testing.T) {
	ctx := context.Background()
	requestID := "host/random-000001"
	createdAt := time.Date(2026, 5, 17, 14, 0, 0, 0, time.UTC)
	signer := testAuditSigner(t, 21)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	_, err = ledger.Append(ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  42,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4.1-mini",
			RouteDecided:     "gpt-4.1-mini",
			UpstreamReported: "gpt-4.1-mini",
			Verdict:          "match",
		},
	})
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

func TestAT_AUDIT_001_004_DeriveReceiptCrossesAuditBilling(t *testing.T) {
	ctx := context.Background()
	requestID := "host/random-000004"
	createdAt := time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC)
	signer := testAuditSigner(t, 24)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  42,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4.1-mini",
			RouteDecided:     "gpt-4.1-mini",
			UpstreamReported: "gpt-4.1-mini",
			Verdict:          "match",
		},
	}); err != nil {
		t.Fatalf("append ledger: %v", err)
	}

	query := &scriptedReceiptQueryer{row: scriptedReceiptRow{values: []any{
		int64(42),
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

func TestAT_AUDIT_001_006_AbortReceiptZeroCost(t *testing.T) {
	ctx := context.Background()
	requestID := "host/random-000006"
	createdAt := time.Date(2026, 5, 17, 17, 10, 0, 0, time.UTC)
	signer := testAuditSigner(t, 26)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  42,
		ModelChain: &proto.ModelChain{
			Requested: "gpt-4.1-mini",
			Verdict:   "match",
		},
	}); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	query := &scriptedReceiptQueryer{row: scriptedReceiptRow{values: []any{
		int64(42),
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
	if _, err := ledger.Append(ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  42,
		ModelChain: &proto.ModelChain{
			Requested: "gpt-4.1-mini",
			Verdict:   "match",
		},
	}); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	query := &scriptedReceiptQueryer{row: scriptedReceiptRow{values: []any{
		int64(42),
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
	if _, err := ledger.Append(ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  42,
		ModelChain: &proto.ModelChain{
			Requested: "gpt-4.1-mini",
			Verdict:   "match",
		},
	}); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	query := &scriptedReceiptQueryer{row: scriptedReceiptRow{values: []any{
		int64(42),
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
		if _, err := ledger.Append(ctx, auditledger.LedgerEntry{
			RequestID: requestID,
			TenantID:  42,
			ModelChain: &proto.ModelChain{
				Requested:        "gpt-4.1-mini",
				RouteDecided:     "gpt-4.1-mini",
				UpstreamReported: "gpt-4.1-mini",
				Verdict:          "match",
			},
		}); err != nil {
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
	if strings.Contains(raw, "cost_total_microcents") {
		t.Fatalf("canonical payload must not use microcents field: %s", raw)
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
	requests map[string]struct{}
}

func newMemoryReceiptDB() *memoryReceiptDB {
	return &memoryReceiptDB{requests: map[string]struct{}{}}
}

func (db *memoryReceiptDB) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	if !strings.Contains(query, "INSERT INTO user_cost_receipts") {
		return receiptTestResult(0), nil
	}
	if len(args) < 2 {
		return nil, errors.New("memory receipt db: request_id arg missing")
	}
	requestID, _ := args[1].(string)
	db.mu.Lock()
	defer db.mu.Unlock()
	if _, exists := db.requests[requestID]; exists {
		return nil, ErrReceiptDuplicate
	}
	db.requests[requestID] = struct{}{}
	return receiptTestResult(1), nil
}

func (db *memoryReceiptDB) QueryRowContext(context.Context, string, ...any) receiptRow {
	return receiptTestRow{err: sql.ErrNoRows}
}

func (db *memoryReceiptDB) hasRequest(requestID string) bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, ok := db.requests[requestID]
	return ok
}

type receiptTestRow struct {
	err error
}

func (r receiptTestRow) Scan(...any) error {
	return r.err
}

type receiptTestResult int64

func (r receiptTestResult) LastInsertId() (int64, error) { return 0, nil }
func (r receiptTestResult) RowsAffected() (int64, error) { return int64(r), nil }

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
