package audit

import (
	"errors"
	"testing"
	"time"
)

func TestReceiptStorageValidationRejectsMissingOwner(t *testing.T) {
	receipt := receiptForStorageTest("req-owner-required", 88, 0)
	receipt.UserID = 0

	err := validateReceiptOwner(receiptOwnerFromReceipt(receipt))
	if !errors.Is(err, ErrReceiptInvalidDerivedData) {
		t.Fatalf("缺少用户归属时错误=%v，期望 %v", err, ErrReceiptInvalidDerivedData)
	}
}

func TestReceiptStorageValidationRejectsOneSidedSignature(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CostReceipt)
	}{
		{
			name: "仅有签名",
			mutate: func(receipt *CostReceipt) {
				receipt.SignerFingerprint = nil
			},
		},
		{
			name: "仅有签名方指纹",
			mutate: func(receipt *CostReceipt) {
				receipt.SignedHash = nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := receiptForStorageTest("req-signature-pair", 88, 0)
			tt.mutate(receipt)

			err := validateReceiptForStorage(receipt)
			if !errors.Is(err, ErrReceiptInvalidDerivedData) {
				t.Fatalf("签名字段不成对时错误=%v，期望 %v", err, ErrReceiptInvalidDerivedData)
			}
		})
	}
}

func receiptForStorageTest(requestID string, tenantID int64, sequence int32) *CostReceipt {
	return &CostReceipt{
		RequestID:           requestID,
		TenantID:            tenantID,
		UserID:              7001,
		ClaimID:             9001,
		OwnerSource:         ReceiptOwnerSourceSettle,
		ReceiptSequence:     sequence,
		Model:               "gpt-4.1-mini",
		InputTokens:         10,
		OutputTokens:        5,
		CostUSDMicros:       42,
		RateTableSnapshotID: 5,
		ValidationState:     ReceiptValidationStateValid,
		Verdict:             ReceiptVerdictMatch,
		SignerFingerprint:   []byte("0123456789abcdef"),
		SignedHash:          []byte("signed-receipt"),
		CreatedAt:           time.Date(2026, 5, 17, 15, 0, 0, 0, time.UTC),
	}
}
