package audit

import "testing"

func TestAT_AUDIT_001_026_MismatchDetectsModelDifference(t *testing.T) {
	derived := mismatchTestReceipt("req-model-mismatch")
	submitted := mismatchTestReceipt("req-model-mismatch")
	submitted.Model = "gpt-4o-mini"

	verdict, err := DetectReceiptMismatch(derived, submitted)
	if err != nil {
		t.Fatalf("DetectReceiptMismatch: %v", err)
	}
	if verdict.State != ReceiptValidationStateMismatchPending || verdict.DeltaMicroUSD != 0 {
		t.Fatalf("verdict=%+v", verdict)
	}
	if len(verdict.FieldsMismatch) != 1 || verdict.FieldsMismatch[0] != "model" {
		t.Fatalf("fields_mismatch=%+v", verdict.FieldsMismatch)
	}
}

func TestAT_AUDIT_001_027_MismatchDetectsCachedTokensDifference(t *testing.T) {
	derived := mismatchTestReceipt("req-cached-tokens-mismatch")
	submitted := mismatchTestReceipt("req-cached-tokens-mismatch")
	submitted.CachedTokens = 12

	verdict, err := DetectReceiptMismatch(derived, submitted)
	if err != nil {
		t.Fatalf("DetectReceiptMismatch: %v", err)
	}
	if verdict.State != ReceiptValidationStateMismatchPending || verdict.DeltaMicroUSD != 0 {
		t.Fatalf("verdict=%+v", verdict)
	}
	if len(verdict.FieldsMismatch) != 1 || verdict.FieldsMismatch[0] != "cached_tokens" {
		t.Fatalf("fields_mismatch=%+v", verdict.FieldsMismatch)
	}
}

func mismatchTestReceipt(requestID string) *CostReceipt {
	return &CostReceipt{
		RequestID:           requestID,
		TenantID:            9,
		ClaimID:             1001,
		Model:               "gpt-4o",
		InputTokens:         100,
		OutputTokens:        20,
		CachedTokens:        8,
		CostUSDMicros:       240,
		RateTableSnapshotID: 12,
		ValidationState:     ReceiptValidationStateValid,
		Verdict:             ReceiptVerdictMatch,
	}
}
