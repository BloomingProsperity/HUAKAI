package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestAT_AUDIT_001_025_RefundWorkerReceiptVisibleThroughGet(t *testing.T) {
	ctx := context.Background()
	const (
		tenantID  int64 = 7
		claimID   int64 = 909
		requestID       = "req-refund-visible"
	)
	now := time.Date(2026, 5, 18, 12, 30, 0, 0, time.UTC)
	signer := mustReceiptSigner(t)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  tenantID,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "mismatch",
		},
	}); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	source := &refundVisibleReceiptSource{inputs: audit.ReceiptInputs{
		TenantID:            tenantID,
		ClaimID:             claimID,
		Model:               "gpt-4o",
		InputTokens:         100,
		OutputTokens:        20,
		CachedTokens:        0,
		CostUSDMicros:       240,
		RateTableSnapshotID: 12,
		CreatedAt:           now,
	}}
	formatter, err := audit.NewReceiptFormatter(ledger, nil, source, signer, audit.WithReceiptNow(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}
	pending := refundVisibleReceipt(t, formatter, ctx, requestID, audit.ReceiptValidationStateMismatchPending)
	store := newRefundVisibleReceiptStore(pending)
	worker := audit.NewMismatchRefundWorker(audit.NewMemoryRefundPendingStore(), &refundVisibleSettler{}, formatter,
		audit.WithRefundLedger(ledger),
		audit.WithRefundReceiptSink(store),
		audit.WithRefundNow(func() time.Time { return now }))

	if err := worker.Apply(ctx, audit.MismatchRefundPayload{
		TenantID:       tenantID,
		ClaimID:        claimID,
		RequestID:      requestID,
		DeltaMicroUSD:  40,
		FieldsMismatch: []string{"cost_total_micro_usd"},
		CreatedAt:      now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("refund Apply: %v", err)
	}

	rec := doReceiptRequest(t, receiptRouter(CostReceiptHandlerDeps{
		Receipts: store,
		Signer:   signer,
	}), http.MethodGet, "/v1/receipts/"+requestID, nil, sessionauth.SessionIdentity{TenantID: tenantID, UserID: 42})
	if rec.Code != http.StatusOK {
		t.Fatalf("receipt status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got UserCostReceipt
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if got.RequestID != requestID ||
		got.ValidationState != audit.ReceiptValidationStateMismatchRefunded ||
		got.Verdict != audit.ReceiptVerdictMismatchRefundPending ||
		got.Signature == "" ||
		got.CanonicalHash == "" ||
		len(got.AdjustmentRefs) == 0 {
		t.Fatalf("refunded receipt response mismatch: %+v", got)
	}
}

func refundVisibleReceipt(t *testing.T, formatter *audit.ReceiptFormatter, ctx context.Context, requestID, state string) *audit.CostReceipt {
	t.Helper()
	receipt, err := formatter.DeriveReceipt(ctx, requestID)
	if err != nil {
		t.Fatalf("derive receipt: %v", err)
	}
	receipt.ValidationState = state
	receipt.Verdict = audit.ReceiptVerdictMismatchRefundPending
	signed, err := formatter.SignReceipt(ctx, receipt)
	if err != nil {
		t.Fatalf("sign receipt: %v", err)
	}
	return signed
}

type refundVisibleReceiptSource struct {
	inputs audit.ReceiptInputs
}

func (s *refundVisibleReceiptSource) LookupReceiptInputs(_ context.Context, _ string, _ int64) (audit.ReceiptInputs, error) {
	return s.inputs, nil
}

type refundVisibleReceiptStore struct {
	receipts map[string][]*audit.CostReceipt
}

func newRefundVisibleReceiptStore(receipts ...*audit.CostReceipt) *refundVisibleReceiptStore {
	store := &refundVisibleReceiptStore{receipts: map[string][]*audit.CostReceipt{}}
	for _, receipt := range receipts {
		store.append(receipt)
	}
	return store
}

func (s *refundVisibleReceiptStore) AppendRefundReceipt(_ context.Context, receipt *audit.CostReceipt) error {
	s.append(receipt)
	return nil
}

func (s *refundVisibleReceiptStore) GetReceipt(_ context.Context, requestID string, tenantID int64) (*audit.CostReceipt, error) {
	history := s.receipts[requestID]
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].TenantID == tenantID {
			return cloneRefundVisibleReceipt(history[i]), nil
		}
	}
	return nil, audit.ErrReceiptNotFound
}

func (s *refundVisibleReceiptStore) append(receipt *audit.CostReceipt) {
	if receipt == nil {
		return
	}
	if s.receipts == nil {
		s.receipts = map[string][]*audit.CostReceipt{}
	}
	s.receipts[receipt.RequestID] = append(s.receipts[receipt.RequestID], cloneRefundVisibleReceipt(receipt))
}

func cloneRefundVisibleReceipt(receipt *audit.CostReceipt) *audit.CostReceipt {
	if receipt == nil {
		return nil
	}
	cloned := *receipt
	cloned.AdjustmentRefs = append([]string(nil), receipt.AdjustmentRefs...)
	cloned.SignerFingerprint = append([]byte(nil), receipt.SignerFingerprint...)
	cloned.SignedHash = append([]byte(nil), receipt.SignedHash...)
	return &cloned
}

type refundVisibleSettler struct{}

func (s *refundVisibleSettler) Settle(context.Context, billing.SettleRequest) (*billing.SettleResult, error) {
	return &billing.SettleResult{}, nil
}

func (s *refundVisibleSettler) Abort(context.Context, int64, int64, string, string) error {
	return nil
}

func (s *refundVisibleSettler) CommitCacheHit(context.Context, int64, int64, string) error {
	return nil
}

func (s *refundVisibleSettler) Refund(_ context.Context, req billing.RefundRequest) (*billing.RefundResult, error) {
	return &billing.RefundResult{
		RefundMicroUSD: req.AmountMicroUSD,
		BillingEventID: 313,
		AdjustmentRef:  "billing_event:313",
	}, nil
}
