package observability

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func TestBillingPersisterReceiptHookAppendsAfterAsyncSettle(t *testing.T) {
	ctx := context.Background()
	requestID := "req-observability-receipt-hook"
	signer := observabilityTestSigner(t)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	prepared, err := auditledger.PrepareEntry(ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  7,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "match",
		},
	})
	if err != nil {
		t.Fatalf("prepare ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, prepared); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	formatter, err := audit.NewReceiptFormatter(ledger, nil, &staticReceiptInputSource{inputs: audit.ReceiptInputs{
		TenantID:            7,
		UserID:              7001,
		ClaimID:             9001,
		Model:               "gpt-4o",
		InputTokens:         2,
		OutputTokens:        3,
		CostUSDMicros:       10000,
		RateTableSnapshotID: 77,
		CreatedAt:           time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
	}}, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}
	appender := &recordingReceiptAppender{}
	settler := audit.NewReceiptHookSettler(&fakeSettler{}, audit.NewReceiptHookHandler(formatter, appender))
	handler := NewBillingPersisterHandler(settler, time.Second)

	err = handler.Handle(ctx, eventbus.RequestCompletionEvent{
		ID:        "evt-receipt-hook",
		TenantID:  7,
		ClaimID:   101,
		RequestID: requestID,
		SettleRequest: billing.SettleRequest{
			TenantID:   7,
			ClaimID:    101,
			ActualCost: decimal.RequireFromString("0.01000000"),
		},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if appender.receipt == nil || appender.receipt.RequestID != requestID || len(appender.receipt.SignedHash) == 0 {
		t.Fatalf("receipt not appended after async settle: %+v", appender.receipt)
	}
}

type staticReceiptInputSource struct {
	inputs audit.ReceiptInputs
}

func (s *staticReceiptInputSource) LookupReceiptInputs(context.Context, string, int64) (audit.ReceiptInputs, error) {
	return s.inputs, nil
}

type recordingReceiptAppender struct {
	receipt *audit.CostReceipt
}

func (a *recordingReceiptAppender) AppendReceipt(_ context.Context, receipt *audit.CostReceipt) error {
	clone := *receipt
	clone.SignerFingerprint = append([]byte(nil), receipt.SignerFingerprint...)
	clone.SignedHash = append([]byte(nil), receipt.SignedHash...)
	a.receipt = &clone
	return nil
}

func observabilityTestSigner(t *testing.T) *sign.Signer {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	signer, err := sign.NewSignerFromKey(ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return signer
}
