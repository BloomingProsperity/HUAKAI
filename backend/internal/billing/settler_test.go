package billing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestAT_AUDIT_001_060_RefundZeroReturnsSkippedCode(t *testing.T) {
	tx := newRefundSettlerTestTx(
		refundSettlerRow{err: pgx.ErrNoRows},
		refundSettlerRow{values: []any{"fp-zero", "committed", decimal.RequireFromString("0.02000000")}},
	)
	settler := &DefaultSettler{q: db.New(tx)}

	res, err := settler.RefundInTx(context.Background(), tx, RefundRequest{
		TenantID:       1,
		ClaimID:        2,
		AmountMicroUSD: 0,
		Reason:         "audit_mismatch",
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

func TestAT_AUDIT_001_062_RefundActualCostOverflowRejected(t *testing.T) {
	tx := newRefundSettlerTestTx(
		refundSettlerRow{err: pgx.ErrNoRows},
		refundSettlerRow{values: []any{"fp-overflow", "committed", decimal.RequireFromString("9223372036854.775808")}},
	)
	settler := &DefaultSettler{q: db.New(tx)}

	_, err := settler.RefundInTx(context.Background(), tx, RefundRequest{
		TenantID:       1,
		ClaimID:        2,
		AmountMicroUSD: 1,
		Reason:         "audit_mismatch",
		AuditRequestID: "req-overflow#audit_refund",
	})
	if !errors.Is(err, ErrCostOverflow) {
		t.Fatalf("RefundInTx overflow error=%v want %v", err, ErrCostOverflow)
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

func (tx *refundSettlerTestTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected exec")
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
