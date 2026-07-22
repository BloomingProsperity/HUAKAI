package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

// TestOutputTokensForAttemptIgnoresDeliveredFrames 守 C1(frames/tokens 消歧):usage_records.tokens_output
// 只反映真实输出 token,绝不用 DeliveredTokenCount(SSE 帧/chunk 投递计数)回退充当。缺真实 usage 时
// (TokensOutput==0)即便投递了 40 帧也记 0(帧数另由 delivered_token_count 列承载);有真实 usage 时照实记。
// 这恢复了"tokens_output==0 ⇔ 无真实输出 token"的可靠信号(reconcile 行识别 R4-P2 依赖它)。双路自证。
func TestOutputTokensForAttemptIgnoresDeliveredFrames(t *testing.T) {
	// 缺 usage:上游报告输出 0,但投递了 40 帧。
	// 变异:恢复 `if attempt.DeliveredTokenCount > output { output = attempt.DeliveredTokenCount }`
	// → 返回 40(帧数被当成 token)→ 变红。
	if got := outputTokensForAttempt(
		gateway.UsageRecordDraft{TokensOutput: 0},
		Attempt{DeliveredTokenCount: 40},
	); got != 0 {
		t.Fatalf("missing-usage tokens_output=%d want 0 (delivered frames must NOT count as output tokens)", got)
	}
	// 有真实 usage:上游报告 19,帧 2 → 照实 19,不被帧数影响。
	if got := outputTokensForAttempt(
		gateway.UsageRecordDraft{TokensOutput: 19},
		Attempt{DeliveredTokenCount: 2},
	); got != 19 {
		t.Fatalf("reported tokens_output=%d want 19 (real output tokens authoritative)", got)
	}
}

func TestNormalizeEndClassSeparatesStreamingAndNonStreamingSuccess(t *testing.T) {
	if got := normalizeEndClass(gateway.StreamEndGraceful, false); got != "non_streaming" {
		t.Fatalf("非流式成功终态=%q，期望 non_streaming", got)
	}
	if got := normalizeEndClass(gateway.StreamEndGraceful, true); got != string(gateway.StreamEndGraceful) {
		t.Fatalf("流式成功终态=%q，期望 %q", got, gateway.StreamEndGraceful)
	}
}

func TestMarshalUsageRecordPayloadCarriesCostSnapshot(t *testing.T) {
	payload, err := marshalUsageRecordPayload(dbbilling.InsertUsageRecordParams{
		TenantID:         1,
		ClaimID:          2,
		APIKeyID:         3,
		UserID:           4,
		AttemptSeq:       1,
		ActualCost:       decimal.RequireFromString("0.00250000"),
		EndClass:         "non_streaming",
		UsageSource:      "reported",
		RequestedModel:   "gpt-4o",
		SettlementSource: SettlementSourceProviderUpstream,
		CostSnapshot:     stringPtrForTest("tiered:vtest-policy"),
		BillingEffect:    string(BillingEffectOperationalCost),
	})
	if err != nil {
		t.Fatalf("marshalUsageRecordPayload: %v", err)
	}
	var decoded struct {
		CostSnapshot *string `json:"cost_snapshot"`
		BillingEffect string  `json:"billing_effect"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.CostSnapshot == nil || *decoded.CostSnapshot != "tiered:vtest-policy" {
		t.Fatalf("cost_snapshot=%v want tiered:vtest-policy", decoded.CostSnapshot)
	}
	if decoded.BillingEffect != string(BillingEffectOperationalCost) {
		t.Fatalf("billing_effect=%q", decoded.BillingEffect)
	}
}

func TestAT_AUDIT_001_060_RefundZeroReturnsSkippedCode(t *testing.T) {
	// 查询顺序:退款事实未命中、claim 锁、旧事件未命中。
	tx := newRefundSettlerTestTx(
		refundSettlerRow{err: pgx.ErrNoRows},
		refundSettlerRow{values: []any{"fp-zero", "committed", decimal.RequireFromString("0.02000000"), int64(901), string(BillingEffectUserCharge)}},
		refundSettlerRow{err: pgx.ErrNoRows},
	)
	settler := &DefaultSettler{q: dbbilling.New(tx)}

	res, err := settler.RefundInTx(context.Background(), tx, RefundRequest{
		TenantID:       1,
		ClaimID:        2,
		AmountMicroUSD: 0,
		Reason:         "audit_mismatch",
		IdempotencyKey: "refund-zero",
		AuditRequestID: "req-zero#audit_refund",
	})
	if err != nil {
		t.Fatalf("RefundInTx: %v", err)
	}
	if res == nil || res.RefundMicroUSD != 0 || res.AdjustmentRef != RefundSkippedAmountZeroRef {
		t.Fatalf("zero refund result=%+v", res)
	}
	if strings.Contains(res.AdjustmentRef, "billing_refund:zero") {
		t.Fatalf("zero refund adjustment ref kept ambiguous legacy code: %q", res.AdjustmentRef)
	}
}

func stringPtrForTest(v string) *string {
	return &v
}

func TestAT_AUDIT_001_062_RefundActualCostOverflowRejected(t *testing.T) {
	// 查询顺序:退款事实未命中、claim 锁、旧事件未命中、captured hold。
	tx := newRefundSettlerTestTx(
		refundSettlerRow{err: pgx.ErrNoRows},
		refundSettlerRow{values: []any{"fp-overflow", "committed", decimal.RequireFromString("9223372036854.775808"), int64(901), string(BillingEffectUserCharge)}},
		refundSettlerRow{err: pgx.ErrNoRows},
		refundSettlerRow{values: []any{int64(1), int64(901), decimal.RequireFromString("1.00000000"), decimal.RequireFromString("1.00000000"), "captured"}},
	)
	settler := &DefaultSettler{q: dbbilling.New(tx)}

	_, err := settler.RefundInTx(context.Background(), tx, RefundRequest{
		TenantID:       1,
		ClaimID:        2,
		AmountMicroUSD: 1,
		Reason:         "audit_mismatch",
		IdempotencyKey: "refund-overflow",
		AuditRequestID: "req-overflow#audit_refund",
	})
	if !errors.Is(err, ErrCostOverflow) {
		t.Fatalf("RefundInTx overflow error=%v want %v", err, ErrCostOverflow)
	}
}

func TestRefundWithoutCapturedHoldIsRejected(t *testing.T) {
	tx := newRefundSettlerTestTx(
		refundSettlerRow{err: pgx.ErrNoRows},
		refundSettlerRow{values: []any{"fp-unpaid", "committed", decimal.RequireFromString("0.02000000"), int64(901), string(BillingEffectUserCharge)}},
		refundSettlerRow{err: pgx.ErrNoRows},
		refundSettlerRow{err: pgx.ErrNoRows},
	)
	settler := &DefaultSettler{q: dbbilling.New(tx)}

	_, err := settler.RefundInTx(context.Background(), tx, RefundRequest{
		TenantID:       1,
		ClaimID:        2,
		AmountMicroUSD: 20_000,
		Reason:         "audit_mismatch",
		IdempotencyKey: "refund-unpaid",
		AuditRequestID: "req-unpaid#audit_refund",
	})
	if !errors.Is(err, ErrRefundNoCapturedCharge) {
		t.Fatalf("无 captured hold error=%v want ErrRefundNoCapturedCharge", err)
	}
}

func TestRefundRejectsHoldIdentityMismatch(t *testing.T) {
	tests := []struct {
		name     string
		tenantID int64
		userID   int64
	}{
		{name: "tenant", tenantID: 2, userID: 901},
		{name: "user", tenantID: 1, userID: 902},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := newRefundSettlerTestTx(
				refundSettlerRow{err: pgx.ErrNoRows},
				refundSettlerRow{values: []any{"fp-identity", "committed", decimal.RequireFromString("0.02000000"), int64(901), string(BillingEffectUserCharge)}},
				refundSettlerRow{err: pgx.ErrNoRows},
				refundSettlerRow{values: []any{tt.tenantID, tt.userID, decimal.RequireFromString("0.02000000"), decimal.RequireFromString("0.02000000"), "captured"}},
			)
			settler := &DefaultSettler{q: dbbilling.New(tx)}

			_, err := settler.RefundInTx(context.Background(), tx, RefundRequest{
				TenantID:       1,
				ClaimID:        2,
				AmountMicroUSD: 20_000,
				Reason:         "audit_mismatch",
				IdempotencyKey: "refund-identity-" + tt.name,
				AuditRequestID: "req-identity#audit_refund",
			})
			if err == nil || !strings.Contains(err.Error(), "refund hold identity mismatch") {
				t.Fatalf("身份错配 error=%v want identity mismatch", err)
			}
		})
	}
}

func TestRefundRejectsHoldThatWasNotCaptured(t *testing.T) {
	tests := []struct {
		name     string
		state    string
		captured decimal.Decimal
	}{
		{name: "held", state: "held", captured: decimal.RequireFromString("0.02000000")},
		{name: "released", state: "released", captured: decimal.RequireFromString("0.02000000")},
		{name: "zero", state: "captured", captured: decimal.Zero},
		{name: "negative", state: "captured", captured: decimal.RequireFromString("-0.01000000")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := newRefundSettlerTestTx(
				refundSettlerRow{err: pgx.ErrNoRows},
				refundSettlerRow{values: []any{"fp-state", "committed", decimal.RequireFromString("0.02000000"), int64(901), string(BillingEffectUserCharge)}},
				refundSettlerRow{err: pgx.ErrNoRows},
				refundSettlerRow{values: []any{int64(1), int64(901), decimal.RequireFromString("0.02000000"), tt.captured, tt.state}},
			)
			settler := &DefaultSettler{q: dbbilling.New(tx)}

			_, err := settler.RefundInTx(context.Background(), tx, RefundRequest{
				TenantID:       1,
				ClaimID:        2,
				AmountMicroUSD: 20_000,
				Reason:         "audit_mismatch",
				IdempotencyKey: "refund-state-" + tt.name,
				AuditRequestID: "req-state#audit_refund",
			})
			if !errors.Is(err, ErrRefundNoCapturedCharge) {
				t.Fatalf("非 captured hold error=%v want ErrRefundNoCapturedCharge", err)
			}
		})
	}
}

func TestRefundPropagatesHoldReadFailure(t *testing.T) {
	boom := errors.New("hold storage unavailable")
	tx := newRefundSettlerTestTx(
		refundSettlerRow{err: pgx.ErrNoRows},
		refundSettlerRow{values: []any{"fp-hold-error", "committed", decimal.RequireFromString("0.02000000"), int64(901), string(BillingEffectUserCharge)}},
		refundSettlerRow{err: pgx.ErrNoRows},
		refundSettlerRow{err: boom},
	)
	settler := &DefaultSettler{q: dbbilling.New(tx)}

	_, err := settler.RefundInTx(context.Background(), tx, RefundRequest{
		TenantID:       1,
		ClaimID:        2,
		AmountMicroUSD: 20_000,
		Reason:         "audit_mismatch",
		IdempotencyKey: "refund-hold-error",
		AuditRequestID: "req-hold-error#audit_refund",
	})
	if !errors.Is(err, boom) {
		t.Fatalf("hold read error=%v want wrapped %v", err, boom)
	}
}

type refundSettlerTestTx struct {
	rows []pgx.Row
}

func newRefundSettlerTestTx(rows ...pgx.Row) *refundSettlerTestTx {
	return &refundSettlerTestTx{rows: rows}
}

func (tx *refundSettlerTestTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("unexpected nested transaction")
}

func (tx *refundSettlerTestTx) Commit(context.Context) error {
	return nil
}

func (tx *refundSettlerTestTx) Rollback(context.Context) error {
	return nil
}

func (tx *refundSettlerTestTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("unexpected copy from")
}

func (tx *refundSettlerTestTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}

func (tx *refundSettlerTestTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (tx *refundSettlerTestTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("unexpected prepare")
}

func (tx *refundSettlerTestTx) Exec(_ context.Context, query string, _ ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(query, "pg_advisory_xact_lock"):
		return pgconn.NewCommandTag("SELECT 1"), nil
	case strings.Contains(query, "INSERT INTO billing_refund_operations"):
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected exec: %s", query)
	}
}

func (tx *refundSettlerTestTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected query")
}

func (tx *refundSettlerTestTx) QueryRow(context.Context, string, ...any) pgx.Row {
	if len(tx.rows) == 0 {
		return refundSettlerRow{err: pgx.ErrNoRows}
	}
	row := tx.rows[0]
	tx.rows = tx.rows[1:]
	return row
}

func (tx *refundSettlerTestTx) Conn() *pgx.Conn {
	return nil
}

type refundSettlerRow struct {
	values []any
	err    error
}

func (r refundSettlerRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("refund settler row: destination count mismatch")
	}
	for i := range dest {
		switch d := dest[i].(type) {
		case *int64:
			v, ok := r.values[i].(int64)
			if !ok {
				return errors.New("refund settler row: int64 type mismatch")
			}
			*d = v
		case *string:
			v, ok := r.values[i].(string)
			if !ok {
				return errors.New("refund settler row: string type mismatch")
			}
			*d = v
		case *decimal.Decimal:
			v, ok := r.values[i].(decimal.Decimal)
			if !ok {
				return errors.New("refund settler row: decimal type mismatch")
			}
			*d = v
		default:
			return errors.New("refund settler row: unsupported destination")
		}
	}
	return nil
}

// TestClampInt32Tokens_DiscriminatingFixtures 守 P2-A 修复:
// usage_records 的 token 列是 PostgreSQL integer (int32 范围),Go int 在
// 64 位平台是 int64。clampInt32Tokens 必须:
//   - 输入 > MaxInt32  → 返 MaxInt32   (防 PostgreSQL "integer out of range")
//   - 输入 < 0         → 返 0          (防负值污染审计)
//   - 输入 == MaxInt32 → 返 MaxInt32   (边界保留)
//   - 输入正常         → 返原值        (功能不退化)
//
// 变异:
//   - 把 helper 改回 `return int32(v)` 时 over/negative 用例必红
//   - 把 over 分支改成 return 0 时 over 用例必红
//   - 把 negative 分支改成 return v 时 negative 用例必红
func TestClampInt32Tokens_DiscriminatingFixtures(t *testing.T) {
	const maxInt32 = int64(^uint32(0) >> 1) // 2147483647
	cases := []struct {
		name string
		in   int64
		want int32
	}{
		{"normal_zero", 0, 0},
		{"normal_small", 42, 42},
		{"normal_million", 1_000_000, 1_000_000},
		{"boundary_max_int32", maxInt32, int32(maxInt32)},
		{"boundary_max_int32_plus_1", maxInt32 + 1, int32(maxInt32)},
		{"overflow_3_billion", 3_000_000_000, int32(maxInt32)},
		{"overflow_max_int64", int64(^uint64(0) >> 1), int32(maxInt32)},
		{"negative_one", -1, 0},
		{"negative_large", -1_000_000_000, 0},
		{"negative_min_int64", -int64(^uint64(0)>>1) - 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := clampInt32Tokens(c.in)
			if got != c.want {
				t.Fatalf("clampInt32Tokens(%d) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}
