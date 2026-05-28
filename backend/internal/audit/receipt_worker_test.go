package audit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/trustreceipt"
)

func TestReceiptHookSettlerAppendsAfterSuccessfulSettle(t *testing.T) {
	ctx := context.Background()
	requestID := "req-worker-success"
	signer := testAuditSigner(t, 31)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, mustPrepareAuditLedgerEntry(t, ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  7,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "match",
		},
	})); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	formatter, err := NewReceiptFormatter(ledger, nil, &staticReceiptSource{inputs: ReceiptInputs{
		TenantID:            7,
		UserID:              7001,
		ClaimID:             9001,
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
	settler := NewReceiptHookSettler(&recordingBillingSettler{}, NewReceiptHookHandler(formatter, appender, WithReceiptHookTrustSigner(testTrustReceiptSigner(t, 61))))

	if _, err := settler.Settle(ctx, billing.SettleRequest{AuditRequestID: requestID}); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if appender.receipt == nil || appender.receipt.RequestID != requestID || len(appender.receipt.SignedHash) == 0 {
		t.Fatalf("receipt not appended: %+v", appender.receipt)
	}
	if appender.receipt.UserID != 7001 || appender.receipt.ClaimID != 9001 || appender.receipt.OwnerSource != ReceiptOwnerSourceSettle {
		t.Fatalf("receipt owner mismatch: %+v", appender.receipt)
	}
}

func TestReceiptHookSettlerCacheHitAppendsCacheHitOwnerSource(t *testing.T) {
	ctx := context.Background()
	requestID := "req-worker-cache-hit"
	signer := testAuditSigner(t, 33)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, mustPrepareAuditLedgerEntry(t, ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  7,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "match",
		},
	})); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	formatter, err := NewReceiptFormatter(ledger, nil, &staticReceiptSource{inputs: ReceiptInputs{
		TenantID:            7,
		UserID:              7001,
		ClaimID:             9001,
		OwnerSource:         ReceiptOwnerSourceCacheHit,
		Model:               "gpt-4o",
		InputTokens:         10,
		OutputTokens:        0,
		CostUSDMicros:       0,
		RateTableSnapshotID: 4,
		CreatedAt:           time.Date(2026, 5, 18, 10, 30, 0, 0, time.UTC),
	}}, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}
	appender := &recordingReceiptAppender{}
	settler := NewReceiptHookSettler(&recordingBillingSettler{}, NewReceiptHookHandler(formatter, appender, WithReceiptHookTrustSigner(testTrustReceiptSigner(t, 62))))

	if err := settler.CommitCacheHit(ctx, billing.SettleRequest{AuditRequestID: requestID}); err != nil {
		t.Fatalf("CommitCacheHit: %v", err)
	}
	if appender.receipt == nil || appender.receipt.UserID != 7001 || appender.receipt.ClaimID != 9001 || appender.receipt.OwnerSource != ReceiptOwnerSourceCacheHit {
		t.Fatalf("cache-hit receipt owner mismatch: %+v", appender.receipt)
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
	if _, err := ledger.Append(ctx, mustPrepareAuditLedgerEntry(t, ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  7,
		ModelChain: &proto.ModelChain{
			Requested: "gpt-4o",
			Verdict:   "match",
		},
	})); err != nil {
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

func TestAppendSettledReceiptSignsFinalReceiptWhenSignerAvailable(t *testing.T) {
	ctx := context.Background()
	requestID := "req-worker-final-sign"
	auditSigner := testAuditSigner(t, 34)
	trustSigner := testTrustReceiptSigner(t, 64)
	ledger, err := auditledger.NewMemoryLedger(auditSigner)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, mustPrepareAuditLedgerEntry(t, ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  7,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o-mini",
			UpstreamReported: "gpt-4o-mini-2024-07-18",
			Verdict:          "match",
		},
	})); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	receiptInputs := ReceiptInputs{
		TenantID:            7,
		UserID:              7001,
		ClaimID:             9001,
		Model:               "gpt-4o-mini-2024-07-18",
		InputTokens:         41,
		OutputTokens:        13,
		CachedTokens:        9,
		CostUSDMicros:       120000,
		RateTableSnapshotID: 44,
		CreatedAt:           time.Date(2026, 5, 27, 11, 12, 13, 0, time.UTC),
	}
	formatter, err := NewReceiptFormatter(ledger, nil, &staticReceiptSource{inputs: receiptInputs}, auditSigner)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}
	appender := &recordingReceiptAppender{}
	hook := NewReceiptHookHandler(formatter, appender, WithReceiptHookTrustSigner(trustSigner))
	settleReq := billing.SettleRequest{
		AuditRequestID:  requestID,
		ActualCost:      decimal.RequireFromString("0.12000000"),
		Provider:        "openai",
		RequestedModel:  "gpt-4o",
		UpstreamModel:   "gpt-4o-mini-2024-07-18",
		SnapshotVersion: "registry:7:44;router:v1",
	}

	if err := hook.AppendSettledReceipt(ctx, settleReq); err != nil {
		t.Fatalf("AppendSettledReceipt: %v", err)
	}
	if appender.receipt == nil {
		t.Fatal("receipt not appended")
	}
	if string(appender.receipt.SignerFingerprint) != trustSigner.Fingerprint() {
		t.Fatalf("fingerprint=%q want %q", string(appender.receipt.SignerFingerprint), trustSigner.Fingerprint())
	}
	sig, err := base64.StdEncoding.DecodeString(string(appender.receipt.SignedHash))
	if err != nil {
		t.Fatalf("signed_hash must be base64 final signature: %v", err)
	}
	finalReceipt := trustreceipt.BuildFinalFromSettleEvent(billing.SettleRequest{}, nil, trustreceipt.FinalReceiptFacts{
		RequestID:           requestID,
		ReceiptSequence:     int(appender.receipt.ReceiptSequence),
		TenantID:            appender.receipt.TenantID,
		OccurredAt:          appender.receipt.CreatedAt,
		Model:               appender.receipt.Model,
		InputTokens:         appender.receipt.InputTokens,
		OutputTokens:        appender.receipt.OutputTokens,
		CachedTokens:        appender.receipt.CachedTokens,
		CostUSDMicros:       appender.receipt.CostUSDMicros,
		RateTableSnapshotID: appender.receipt.RateTableSnapshotID,
		ValidationState:     appender.receipt.ValidationState,
	})
	finalReceipt.RedactedMetadataAllowlist = finalTrustReceiptMetadataFromCostReceipt(appender.receipt)
	canonical, err := trustreceipt.Canonical(finalReceipt)
	if err != nil {
		t.Fatalf("canonical final receipt: %v", err)
	}
	if !ed25519.Verify(trustSigner.PublicKey(), canonical, sig) {
		t.Fatal("final signature does not verify against trust receipt canonical bytes")
	}
	enrichedReceipt := trustreceipt.BuildFinalFromSettleEvent(settleReq, nil, trustreceipt.FinalReceiptFacts{
		RequestID:           requestID,
		ReceiptSequence:     int(appender.receipt.ReceiptSequence),
		TenantID:            appender.receipt.TenantID,
		OccurredAt:          appender.receipt.CreatedAt,
		Model:               appender.receipt.Model,
		InputTokens:         appender.receipt.InputTokens,
		OutputTokens:        appender.receipt.OutputTokens,
		CachedTokens:        appender.receipt.CachedTokens,
		CostUSDMicros:       appender.receipt.CostUSDMicros,
		RateTableSnapshotID: appender.receipt.RateTableSnapshotID,
		ValidationState:     appender.receipt.ValidationState,
	})
	enrichedCanonical, err := trustreceipt.Canonical(enrichedReceipt)
	if err != nil {
		t.Fatalf("canonical enriched receipt: %v", err)
	}
	if ed25519.Verify(trustSigner.PublicKey(), enrichedCanonical, sig) {
		t.Fatal("final trust signature must not depend on settle-only fields unavailable to receipt verify")
	}
}

func TestAppendSettledReceiptSkipsSignWhenSignerNil(t *testing.T) {
	ctx := context.Background()
	requestID := "req-worker-final-nil-signer"
	auditSigner := testAuditSigner(t, 35)
	ledger, err := auditledger.NewMemoryLedger(auditSigner)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, mustPrepareAuditLedgerEntry(t, ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  7,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "match",
		},
	})); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	formatter, err := NewReceiptFormatter(ledger, nil, &staticReceiptSource{inputs: ReceiptInputs{
		TenantID:            7,
		UserID:              7001,
		ClaimID:             9001,
		Model:               "gpt-4o",
		InputTokens:         5,
		OutputTokens:        8,
		CostUSDMicros:       10000,
		RateTableSnapshotID: 4,
		CreatedAt:           time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC),
	}}, auditSigner)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}
	appender := &recordingReceiptAppender{}
	var reported error
	hook := NewReceiptHookHandler(formatter, appender,
		WithReceiptHookErrorHandler(func(_ context.Context, _ string, err error) {
			reported = err
		}))

	if err := hook.AppendSettledReceipt(ctx, billing.SettleRequest{
		AuditRequestID: requestID,
		ActualCost:     decimal.RequireFromString("0.01000000"),
		Provider:       "openai",
		RequestedModel: "gpt-4o",
		UpstreamModel:  "gpt-4o",
	}); err != nil {
		t.Fatalf("AppendSettledReceipt must fail open on nil trust signer: %v", err)
	}
	if appender.receipt == nil {
		t.Fatal("receipt must still append when signer is nil")
	}
	if len(appender.receipt.SignedHash) != 0 || len(appender.receipt.SignerFingerprint) != 0 {
		t.Fatalf("nil signer must leave signature fields empty, got fp=%q sig=%q", string(appender.receipt.SignerFingerprint), string(appender.receipt.SignedHash))
	}
	if err := validateReceiptForStorage(appender.receipt); err != nil {
		t.Fatalf("unsigned fail-open receipt must pass storage validation: %v", err)
	}
	if receiptBytea(appender.receipt.SignedHash) == nil || receiptBytea(appender.receipt.SignerFingerprint) == nil {
		t.Fatal("unsigned fail-open receipt must write non-nil empty bytea values")
	}
	if !errors.Is(reported, trustreceipt.ErrSignerNil) {
		t.Fatalf("reported error=%v want ErrSignerNil", reported)
	}
}

func TestFinalReceiptIncludesRealCostAndTokens(t *testing.T) {
	signer := testTrustReceiptSigner(t, 65)
	env := proto.NewEmptyEnvelope()
	env.RequestMeta.RequestID = "req-final-builder"
	env.RequestMeta.TenantID = 7
	env.RequestMeta.Provider = "openai"
	env.RequestMeta.Model = "gpt-4o"
	env.RequestMeta.UpstreamModel = "gpt-4o-mini-2024-07-18"
	env.BufferedResponse = &proto.CanonicalResponse{Model: "gpt-4o-mini-2024-07-18"}
	env.Accounting.ModelChain = &proto.ModelChain{
		Requested:        "gpt-4o",
		RouteDecided:     "gpt-4o-mini",
		UpstreamReported: "gpt-4o-mini-2024-07-18",
	}
	env.Accounting.Usage = proto.CanonicalUsage{
		InputTokens:              31,
		OutputTokens:             7,
		CacheCreationInputTokens: 2,
		CacheReadInputTokens:     3,
	}
	facts := trustreceipt.FinalReceiptFacts{
		RequestID:           "req-final-builder",
		ReceiptSequence:     0,
		TenantID:            7,
		OccurredAt:          time.Date(2026, 5, 27, 13, 0, 0, 0, time.UTC),
		Model:               "gpt-4o-mini-2024-07-18",
		CostUSDMicros:       120000,
		RateTableSnapshotID: 44,
		ValidationState:     ReceiptValidationStateValid,
	}
	baseReq := billing.SettleRequest{
		AuditRequestID:  "req-final-builder",
		ActualCost:      decimal.RequireFromString("0.12000000"),
		Provider:        "openai",
		RequestedModel:  "gpt-4o",
		UpstreamModel:   "gpt-4o-mini-2024-07-18",
		SnapshotVersion: "registry:7:44;router:v1",
	}

	receipt := trustreceipt.BuildFinalFromSettleEvent(baseReq, env, facts)
	if receipt.ValidationState != ReceiptValidationStateValid {
		t.Fatalf("ValidationState=%q want valid", receipt.ValidationState)
	}
	if receipt.CostCents != 12 {
		t.Fatalf("CostCents=%d want 12", receipt.CostCents)
	}
	if receipt.TokenCounts.Input != 31 || receipt.TokenCounts.Output != 7 || receipt.TokenCounts.Cached != 5 {
		t.Fatalf("TokenCounts=%+v want input=31 output=7 cached=5", receipt.TokenCounts)
	}
	if receipt.PriceSnapshot.RateTableSnapshotID != 44 || receipt.PriceSnapshot.SnapshotVersion != "registry:7:44;router:v1" || receipt.PriceSnapshot.CurrencyCode != "USD" {
		t.Fatalf("PriceSnapshot=%+v", receipt.PriceSnapshot)
	}

	mutatedReq := baseReq
	mutatedReq.ActualCost = decimal.RequireFromString("0.13000000")
	mutated := trustreceipt.BuildFinalFromSettleEvent(mutatedReq, env, facts)
	baseCanonical, err := trustreceipt.Canonical(receipt)
	if err != nil {
		t.Fatalf("base canonical: %v", err)
	}
	mutatedCanonical, err := trustreceipt.Canonical(mutated)
	if err != nil {
		t.Fatalf("mutated canonical: %v", err)
	}
	if bytes.Equal(baseCanonical, mutatedCanonical) {
		t.Fatal("changing settled cost must change canonical final receipt bytes")
	}
	baseSig, _, err := trustreceipt.SignReceipt(signer, receipt)
	if err != nil {
		t.Fatalf("base sign: %v", err)
	}
	mutatedSig, _, err := trustreceipt.SignReceipt(signer, mutated)
	if err != nil {
		t.Fatalf("mutated sign: %v", err)
	}
	if baseSig == mutatedSig {
		t.Fatal("changing settled cost must change final detached signature")
	}
}

type recordingBillingSettler struct {
	req billing.SettleRequest
}

func (s *recordingBillingSettler) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	s.req = req
	return &billing.SettleResult{}, nil
}

func (s *recordingBillingSettler) Abort(context.Context, int64, int64, string, string, int64) error {
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

func testTrustReceiptSigner(t *testing.T, seed byte) *sign.Signer {
	t.Helper()
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	signer, err := sign.NewSignerFromKey(private)
	if err != nil {
		t.Fatalf("trust signer: %v", err)
	}
	return signer
}
