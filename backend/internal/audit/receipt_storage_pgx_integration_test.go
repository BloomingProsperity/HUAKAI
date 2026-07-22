//go:build integration_pg

package audit

import (
	"context"
	"errors"
	"testing"
)

func TestPGXReceiptStorageRollsBackWhenOwnerInsertFails(t *testing.T) {
	ctx := context.Background()
	pool := openRefundAtomicTestPool(t, ctx)
	seed := seedRefundAtomicClaim(t, ctx, pool, "receipt-owner-rollback")
	store, err := NewPGXReceiptStorage(pool)
	if err != nil {
		t.Fatalf("构造回执存储：%v", err)
	}
	receipt := receiptForStorageTest(seed.requestID, seed.tenantID, 0)
	receipt.UserID = seed.userID
	receipt.ClaimID = seed.claimID + 9_000_000_000

	if err := store.AppendReceipt(ctx, receipt); err == nil {
		t.Fatal("归属外键失败时 AppendReceipt 未返回错误")
	}
	var receiptCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM user_cost_receipts
WHERE tenant_id=$1 AND request_id=$2`, seed.tenantID, seed.requestID).Scan(&receiptCount); err != nil {
		t.Fatalf("读取回执行数：%v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("归属写入失败后仍有 %d 条回执，期望事务完整回滚", receiptCount)
	}
}

func TestPGXReceiptStorageReadIsolationAndRefundLookup(t *testing.T) {
	ctx := context.Background()
	pool := openRefundAtomicTestPool(t, ctx)
	seed := seedRefundAtomicClaim(t, ctx, pool, "receipt-read-isolation")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启回执测试事务：%v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	store, err := NewPGXReceiptStorage(pool)
	if err != nil {
		t.Fatalf("构造回执存储：%v", err)
	}
	reader := &PGXReceiptStorage{query: tx}
	original := receiptForStorageTest(seed.requestID, seed.tenantID, 0)
	original.UserID = seed.userID
	original.ClaimID = seed.claimID
	if err := store.AppendInTx(ctx, tx, original); err != nil {
		t.Fatalf("写入原始回执：%v", err)
	}

	owned, err := reader.GetReceiptForUser(ctx, seed.requestID, seed.tenantID, seed.userID)
	if err != nil {
		t.Fatalf("用户读取自己的回执：%v", err)
	}
	if owned.UserID != seed.userID || owned.ClaimID != seed.claimID {
		t.Fatalf("回执归属 user=%d claim=%d，期望 %d/%d", owned.UserID, owned.ClaimID, seed.userID, seed.claimID)
	}
	if crossUser, err := reader.GetReceiptForUser(ctx, seed.requestID, seed.tenantID, seed.userID+1); !errors.Is(err, ErrReceiptNotFound) {
		t.Fatalf("跨用户读取结果=%+v 错误=%v，期望 %v", crossUser, err, ErrReceiptNotFound)
	}

	refundRef := "refund-operation:receipt-read-isolation"
	refunded := cloneReceipt(original)
	refunded.ReceiptSequence = 1
	refunded.ValidationState = ReceiptValidationStateMismatchRefunded
	refunded.Verdict = ReceiptVerdictSubstitutionRefund
	refunded.AdjustmentRefs = []string{refundRef}
	refunded.SignedHash = []byte("signed-refunded")
	if err := store.AppendInTx(ctx, tx, refunded); err != nil {
		t.Fatalf("写入退款回执：%v", err)
	}

	latest, err := reader.GetReceiptForAdmin(ctx, seed.requestID, seed.tenantID)
	if err != nil {
		t.Fatalf("管理端读取最新回执：%v", err)
	}
	if latest.ReceiptSequence != 1 || latest.ValidationState != ReceiptValidationStateMismatchRefunded {
		t.Fatalf("最新回执序号/状态=%d/%q，期望 1/%q", latest.ReceiptSequence, latest.ValidationState, ReceiptValidationStateMismatchRefunded)
	}
	bySequence, err := reader.GetReceiptBySequence(ctx, seed.requestID, seed.tenantID, 0)
	if err != nil || bySequence.ReceiptSequence != 0 {
		t.Fatalf("按序号读取原始回执结果=%+v 错误=%v", bySequence, err)
	}
	byRefund, err := reader.GetByRefundIdempotency(ctx, seed.requestID, seed.tenantID, refundRef)
	if err != nil || byRefund.ReceiptSequence != 1 {
		t.Fatalf("按退款幂等键读取结果=%+v 错误=%v", byRefund, err)
	}
	refundedReceipt, err := reader.GetRefundedReceipt(ctx, seed.requestID, seed.tenantID)
	if err != nil || refundedReceipt.ReceiptSequence != 1 {
		t.Fatalf("读取已退款回执结果=%+v 错误=%v", refundedReceipt, err)
	}
	maxSequence, err := reader.MaxReceiptSequence(ctx, seed.requestID, seed.tenantID)
	if err != nil || maxSequence != 1 {
		t.Fatalf("最大回执序号=%d 错误=%v，期望 1", maxSequence, err)
	}
	unknown := cloneReceipt(original)
	unknown.ReceiptSequence = 2
	unknown.ValidationState = ReceiptValidationStateUnknown
	unknown.SignedHash = []byte("signed-unknown")
	if err := store.AppendInTx(ctx, tx, unknown); err != nil {
		t.Fatalf("写入未知校验状态回执：%v", err)
	}
	unknownRead, err := reader.GetReceiptBySequence(ctx, seed.requestID, seed.tenantID, 2)
	if err != nil || unknownRead.ValidationState != ReceiptValidationStateUnknown {
		t.Fatalf("未知校验状态读取结果=%+v 错误=%v", unknownRead, err)
	}
}

func TestPGXReceiptStorageRejectsDuplicateSequence(t *testing.T) {
	ctx := context.Background()
	pool := openRefundAtomicTestPool(t, ctx)
	seed := seedRefundAtomicClaim(t, ctx, pool, "receipt-duplicate")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启回执测试事务：%v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	store, err := NewPGXReceiptStorage(pool)
	if err != nil {
		t.Fatalf("构造回执存储：%v", err)
	}
	receipt := receiptForStorageTest(seed.requestID, seed.tenantID, 0)
	receipt.UserID = seed.userID
	receipt.ClaimID = seed.claimID
	if err := store.AppendInTx(ctx, tx, receipt); err != nil {
		t.Fatalf("首次写入回执：%v", err)
	}
	if err := store.AppendInTx(ctx, tx, cloneReceipt(receipt)); !errors.Is(err, ErrReceiptDuplicate) {
		t.Fatalf("重复序号错误=%v，期望 %v", err, ErrReceiptDuplicate)
	}
}

func TestPGXReceiptStorageAdminCanReadLegacyReceiptWithoutOwner(t *testing.T) {
	ctx := context.Background()
	pool := openRefundAtomicTestPool(t, ctx)
	seed := seedRefundAtomicClaim(t, ctx, pool, "receipt-legacy-owner")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启回执测试事务：%v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	receipt := receiptForStorageTest(seed.requestID, seed.tenantID, 0)
	if _, err := tx.Exec(ctx, `
INSERT INTO user_cost_receipts (
    tenant_id, request_id, receipt_sequence, model, input_tokens, output_tokens, cached_tokens,
    cost_usd_micros, rate_table_snapshot_id, signer_fingerprint, signed_hash,
    created_at, validation_state, verdict, adjustment_refs
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, '[]'::jsonb)`,
		receipt.TenantID, receipt.RequestID, receipt.ReceiptSequence, receipt.Model,
		receipt.InputTokens, receipt.OutputTokens, receipt.CachedTokens, receipt.CostUSDMicros,
		receipt.RateTableSnapshotID, receipt.SignerFingerprint, receipt.SignedHash,
		receipt.CreatedAt, receipt.ValidationState, receipt.Verdict,
	); err != nil {
		t.Fatalf("写入历史无归属回执：%v", err)
	}

	reader := &PGXReceiptStorage{query: tx}
	adminReceipt, err := reader.GetReceiptForAdmin(ctx, seed.requestID, seed.tenantID)
	if err != nil || adminReceipt.UserID != 0 {
		t.Fatalf("管理端读取历史回执结果=%+v 错误=%v", adminReceipt, err)
	}
	userReceipt, err := reader.GetReceiptForUser(ctx, seed.requestID, seed.tenantID, seed.userID)
	if !errors.Is(err, ErrReceiptNotFound) {
		t.Fatalf("用户读取无归属历史回执结果=%+v 错误=%v，期望 %v", userReceipt, err, ErrReceiptNotFound)
	}
}
