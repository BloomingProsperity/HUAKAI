package audit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

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
		UserID:              7001,
		ClaimID:             9001,
		Model:               "gpt-4.1-mini",
		InputTokens:         123,
		OutputTokens:        45,
		CachedTokens:        7,
		CostUSDMicros:       3210,
		RateTableSnapshotID: 9,
		CreatedAt:           createdAt,
	}}
	formatter, err := NewReceiptFormatter(ledger, source, signer)
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
	// 走【生产验签口径】:trust.v1 canonical(FinalTrustReceiptCanonical,同 cost_receipt_handler
	// 验签侧)+ base64 解码 SignedHash。此前用 v2 canonical + 原始 SignedHash 验签是伪绿(B7)——
	// 退款回执实际按 trust.v1 验签,若签名口径不符则用户验签恒失败(B2)。
	// Mutation: 把 SignReceipt 改回签 v2 canonical 哈希 / 存原始字节 → 本处 trust.v1 验签失败 → RED。
	canonical, err := FinalTrustReceiptCanonical(signed1)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(string(signed1.SignedHash))
	if err != nil {
		t.Fatalf("SignedHash 必须是 base64 编码: %v", err)
	}
	ok, err := signer.Verify(ctx, canonical, sig, string(signed1.SignerFingerprint))
	if err != nil || !ok {
		t.Fatalf("退款回执签名必须通过生产 trust.v1 验签口径: ok=%v err=%v", ok, err)
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
		int64(8004),
		sql.NullString{String: "gpt-4.1-mini", Valid: true},
		sql.NullInt64{Int64: 100, Valid: true},
		sql.NullInt64{Int64: 25, Valid: true},
		sql.NullInt64{Int64: 5, Valid: true},
		"0.001234",
		sql.NullString{String: "registry:42:9;router:v1", Valid: true},
		sql.NullInt64{Int64: 77, Valid: true},
		sql.NullTime{Time: createdAt, Valid: true},
		sql.NullString{String: "provider_upstream", Valid: true},
	}}}
	source := &PGXReceiptSource{query: query}
	formatter, err := NewReceiptFormatter(ledger, source, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}

	receipt, err := formatter.DeriveReceipt(ctx, requestID)
	if err != nil {
		t.Fatalf("DeriveReceipt: %v", err)
	}
	if receipt.TenantID != 42 || receipt.InputTokens != 100 || receipt.OutputTokens != 25 ||
		receipt.UserID != 8004 || receipt.ClaimID != 7004 ||
		receipt.CachedTokens != 5 || receipt.CostUSDMicros != 1234 ||
		receipt.RateTableSnapshotID != 9 || !receipt.CreatedAt.Equal(createdAt) {
		t.Fatalf("receipt mismatch: %+v", receipt)
	}
	if len(query.args) != 2 || query.args[0] != requestID || query.args[1] != int64(42) {
		t.Fatalf("source args mismatch: %#v", query.args)
	}
	if !strings.Contains(query.sql, "be.audit_request_id = $1") || !strings.Contains(query.sql, "be.tenant_id = $2") {
		t.Fatalf("回执输入必须按日志请求号和租户同时收窄账务事实:\n%s", query.sql)
	}
	if strings.Contains(query.sql, "audit_ledger_entries") {
		t.Fatalf("输入源不得重复依赖 PostgreSQL 日志表，开发态内存日志也必须能生成回执:\n%s", query.sql)
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
		int64(8006),
		sql.NullString{String: "gpt-4.1-mini", Valid: true},
		sql.NullInt64{Int64: 0, Valid: true},
		sql.NullInt64{Int64: 0, Valid: true},
		sql.NullInt64{Int64: 0, Valid: true},
		"0.00000000",
		sql.NullString{String: "registry:42:9;router:v1", Valid: true},
		sql.NullInt64{Int64: 106, Valid: true},
		sql.NullTime{Time: createdAt, Valid: true},
		sql.NullString{String: "provider_upstream", Valid: true},
	}}}
	source := &PGXReceiptSource{query: query}
	formatter, err := NewReceiptFormatter(ledger, source, signer)
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
	if !strings.Contains(query.sql, "claim_aborted") || !strings.Contains(query.sql, "be.audit_request_id = $1") || !strings.Contains(query.sql, "be.tenant_id = $2") {
		t.Fatalf("中止回执必须按日志请求号与租户读取中止账务事件:\n%s", query.sql)
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
		int64(8007),
		sql.NullString{String: "gpt-4.1-mini", Valid: true},
		sql.NullInt64{},
		sql.NullInt64{},
		sql.NullInt64{},
		"0.001234",
		sql.NullString{},
		sql.NullInt64{},
		sql.NullTime{Time: time.Date(2026, 5, 17, 17, 20, 0, 0, time.UTC), Valid: true},
		sql.NullString{},
	}}}
	source := &PGXReceiptSource{query: query}
	formatter, err := NewReceiptFormatter(ledger, source, signer)
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
		int64(8008),
		sql.NullString{String: "gpt-4.1-mini", Valid: true},
		sql.NullInt64{Int64: 81, Valid: true},
		sql.NullInt64{Int64: 13, Valid: true},
		sql.NullInt64{Int64: 3, Valid: true},
		"0.000987",
		sql.NullString{String: "registry:42:12;router:v9", Valid: true},
		sql.NullInt64{Int64: 888, Valid: true},
		sql.NullTime{Time: createdAt, Valid: true},
		sql.NullString{String: "provider_upstream", Valid: true},
	}}}
	source := &PGXReceiptSource{query: query}
	formatter, err := NewReceiptFormatter(ledger, source, signer)
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
		UserID:              7001,
		ClaimID:             9001,
		Model:               "gpt-4.1-mini",
		InputTokens:         81,
		OutputTokens:        13,
		CachedTokens:        3,
		CostUSDMicros:       987,
		RateTableSnapshotID: 11,
		CreatedAt:           time.Date(2026, 5, 17, 16, 30, 0, 0, time.UTC),
	}}
	formatter, err := NewReceiptFormatter(ledger, source, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}
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
		if signed.RequestID != requestID {
			t.Fatalf("签名后的 request_id=%q，期望 %q", signed.RequestID, requestID)
		}
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

type scriptedReceiptQueryer struct {
	sql  string
	args []any
	row  scriptedReceiptRow
}

func (q *scriptedReceiptQueryer) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
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
		case *sql.NullTime:
			v, ok := r.values[i].(sql.NullTime)
			if !ok {
				return errors.New("scripted receipt row: null time type mismatch")
			}
			*d = v
		default:
			return errors.New("scripted receipt row: unsupported destination")
		}
	}
	return nil
}

func testFormatter(t *testing.T, signer *auditledger.LocalEd25519Signer) *ReceiptFormatter {
	t.Helper()
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	formatter, err := NewReceiptFormatter(ledger, &staticReceiptSource{inputs: ReceiptInputs{
		TenantID:            1,
		UserID:              7001,
		ClaimID:             9001,
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
