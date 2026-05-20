package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestReceiptHookSettlerAppendsAfterSuccessfulSettle(t *testing.T) {
	ctx := context.Background()
	requestID := "req-worker-success"
	signer := testAuditSigner(t, 31)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  7,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "match",
		},
	}); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	formatter, err := NewReceiptFormatter(ledger, nil, &staticReceiptSource{inputs: ReceiptInputs{
		TenantID:            7,
		Model:               "gpt-4o",
		InputTokens:         10,
		OutputTokens:        3,
		CostUSDMicros:       100,
		RateTableSnapshotID: 4,
		CreatedAt:           time.Date(2026, 5, 18, 10, 30, 0, 0, time.UTC),
	}}, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}
	appender := &recordingReceiptAppender{}
	settler := NewReceiptHookSettler(&recordingBillingSettler{}, NewReceiptHookHandler(formatter, appender))

	if _, err := settler.Settle(ctx, billing.SettleRequest{AuditRequestID: requestID}); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if appender.receipt == nil || appender.receipt.RequestID != requestID || len(appender.receipt.SignedHash) == 0 {
		t.Fatalf("receipt not appended: %+v", appender.receipt)
	}
}

func TestReceiptHookSettlerBestEffortDoesNotBlockSettle(t *testing.T) {
	ctx := context.Background()
	requestID := "req-worker-best-effort"
	signer := testAuditSigner(t, 32)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  7,
		ModelChain: &proto.ModelChain{
			Requested: "gpt-4o",
			Verdict:   "match",
		},
	}); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	formatter, err := NewReceiptFormatter(ledger, nil, &staticReceiptSource{err: ErrReceiptInputsNotFound}, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}
	var reported error
	hook := NewReceiptHookHandler(formatter, &recordingReceiptAppender{},
		WithReceiptHookErrorHandler(func(_ context.Context, _ string, err error) {
			reported = err
		}))
	settler := NewReceiptHookSettler(&recordingBillingSettler{}, hook)

	if _, err := settler.Settle(ctx, billing.SettleRequest{AuditRequestID: requestID}); err != nil {
		t.Fatalf("Settle must not fail on receipt hook error: %v", err)
	}
	if !errors.Is(reported, ErrReceiptInputsNotFound) {
		t.Fatalf("reported error=%v want ErrReceiptInputsNotFound", reported)
	}
}

type recordingBillingSettler struct {
	req billing.SettleRequest
}

func (s *recordingBillingSettler) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	s.req = req
	return &billing.SettleResult{}, nil
}

func (s *recordingBillingSettler) Abort(context.Context, int64, int64, string, string) error {
	return nil
}

func (s *recordingBillingSettler) CommitCacheHit(context.Context, billing.SettleRequest) error {
	return nil
}

func (s *recordingBillingSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return &billing.RefundResult{}, nil
}

type recordingReceiptAppender struct {
	receipt *CostReceipt
	err     error
}

func (a *recordingReceiptAppender) AppendReceipt(_ context.Context, receipt *CostReceipt) error {
	if a.err != nil {
		return a.err
	}
	clone := *receipt
	clone.SignerFingerprint = append([]byte(nil), receipt.SignerFingerprint...)
	clone.SignedHash = append([]byte(nil), receipt.SignedHash...)
	a.receipt = &clone
	return nil
}
