package audit

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestReceiptStorageAppendReceiptRollsBackReceiptWhenOwnerInsertFails(t *testing.T) {
	ctx := context.Background()
	db := newSidecarReceiptDB()
	db.failOwnerInsert = errors.New("sidecar insert failed")
	store := &ReceiptStorage{exec: db}
	receipt := receiptForStorageTest("req-sidecar-rollback", 88, 0)
	receipt.UserID = 7001
	receipt.ClaimID = 9001
	receipt.OwnerSource = ReceiptOwnerSourceSettle

	err := store.AppendReceipt(ctx, receipt)
	if err == nil {
		t.Fatalf("AppendReceipt error=nil, want sidecar insert failure")
	}
	if db.hasReceipt(receipt.TenantID, receipt.RequestID, receipt.ReceiptSequence) {
		t.Fatalf("receipt row persisted after sidecar insert failed; append must be atomic")
	}
}

func TestReceiptStorageGetReceiptForUserRejectsCrossUser(t *testing.T) {
	ctx := context.Background()
	db := newSidecarReceiptDB()
	receipt := receiptForStorageTest("req-sidecar-cross-user", 88, 0)
	receipt.UserID = 7001
	receipt.ClaimID = 9001
	receipt.OwnerSource = ReceiptOwnerSourceSettle
	db.seedReceipt(receipt)
	db.seedOwner(receipt.TenantID, receipt.RequestID, receipt.ReceiptSequence, receipt.UserID, receipt.ClaimID, receipt.OwnerSource)
	store := &ReceiptStorage{exec: db}

	got, err := store.GetReceiptForUser(ctx, receipt.RequestID, receipt.TenantID, 7002)
	if !errors.Is(err, ErrReceiptNotFound) {
		t.Fatalf("GetReceiptForUser cross-user got receipt=%+v err=%v, want ErrReceiptNotFound", got, err)
	}
}

func TestReceiptStorageGetReceiptForUserFindsSameUser(t *testing.T) {
	ctx := context.Background()
	db := newSidecarReceiptDB()
	receipt := receiptForStorageTest("req-sidecar-same-user", 88, 0)
	receipt.UserID = 7001
	receipt.ClaimID = 9001
	receipt.OwnerSource = ReceiptOwnerSourceSettle
	db.seedReceipt(receipt)
	db.seedOwner(receipt.TenantID, receipt.RequestID, receipt.ReceiptSequence, receipt.UserID, receipt.ClaimID, receipt.OwnerSource)
	store := &ReceiptStorage{exec: db}

	got, err := store.GetReceiptForUser(ctx, receipt.RequestID, receipt.TenantID, receipt.UserID)
	if err != nil {
		t.Fatalf("GetReceiptForUser same user: %v", err)
	}
	if got.RequestID != receipt.RequestID || got.UserID != receipt.UserID || got.ClaimID != receipt.ClaimID {
		t.Fatalf("receipt owner mismatch: got %+v want user=%d claim=%d", got, receipt.UserID, receipt.ClaimID)
	}
}

func TestReceiptStorageAppendReceiptThenGetReceiptForUserUsesSidecarOwner(t *testing.T) {
	ctx := context.Background()
	db := newSidecarReceiptDB()
	store := &ReceiptStorage{exec: db}
	receipt := receiptForStorageTest("req-sidecar-append-user", 88, 0)
	receipt.UserID = 7001
	receipt.ClaimID = 9001
	receipt.OwnerSource = ReceiptOwnerSourceSettle

	if err := store.AppendReceipt(ctx, receipt); err != nil {
		t.Fatalf("AppendReceipt: %v", err)
	}
	got, err := store.GetReceiptForUser(ctx, receipt.RequestID, receipt.TenantID, receipt.UserID)
	if err != nil {
		t.Fatalf("GetReceiptForUser same user after append: %v", err)
	}
	if got.UserID != receipt.UserID || got.ClaimID != receipt.ClaimID || got.OwnerSource != ReceiptOwnerSourceSettle {
		t.Fatalf("owner sidecar mismatch: got %+v want user=%d claim=%d source=%s", got, receipt.UserID, receipt.ClaimID, ReceiptOwnerSourceSettle)
	}
	crossUser, err := store.GetReceiptForUser(ctx, receipt.RequestID, receipt.TenantID, 7002)
	if !errors.Is(err, ErrReceiptNotFound) {
		t.Fatalf("GetReceiptForUser cross-user after append got receipt=%+v err=%v, want ErrReceiptNotFound", crossUser, err)
	}
}

func TestReceiptStorageGetReceiptForAdminIgnoresOwnerUser(t *testing.T) {
	ctx := context.Background()
	db := newSidecarReceiptDB()
	receipt := receiptForStorageTest("req-sidecar-admin", 88, 0)
	receipt.UserID = 0
	receipt.ClaimID = 0
	receipt.OwnerSource = ""
	db.seedReceipt(receipt)
	store := &ReceiptStorage{exec: db}

	got, err := store.GetReceiptForAdmin(ctx, receipt.RequestID, receipt.TenantID)
	if err != nil {
		t.Fatalf("GetReceiptForAdmin: %v", err)
	}
	if got.RequestID != receipt.RequestID || got.UserID != 0 {
		t.Fatalf("admin receipt mismatch: got %+v want request=%s with no owner filter", got, receipt.RequestID)
	}
}

func TestReceiptStorageGetReceiptForUserRejectsLegacyReceiptWithoutOwner(t *testing.T) {
	ctx := context.Background()
	db := newSidecarReceiptDB()
	receipt := receiptForStorageTest("req-sidecar-legacy", 88, 0)
	db.seedReceipt(receipt)
	store := &ReceiptStorage{exec: db}

	got, err := store.GetReceiptForUser(ctx, receipt.RequestID, receipt.TenantID, 7001)
	if !errors.Is(err, ErrReceiptNotFound) {
		t.Fatalf("legacy receipt without sidecar got receipt=%+v err=%v, want ErrReceiptNotFound", got, err)
	}
}

func TestReceiptStorageAppendReceiptRejectsMissingUserID(t *testing.T) {
	ctx := context.Background()
	db := newSidecarReceiptDB()
	store := &ReceiptStorage{exec: db}
	receipt := receiptForStorageTest("req-sidecar-missing-user", 88, 0)
	receipt.UserID = 0
	receipt.ClaimID = 9001
	receipt.OwnerSource = ReceiptOwnerSourceSettle

	err := store.AppendReceipt(ctx, receipt)
	if err == nil {
		t.Fatalf("AppendReceipt with UserID=0 error=nil, want fail-closed owner validation")
	}
	if db.hasReceipt(receipt.TenantID, receipt.RequestID, receipt.ReceiptSequence) {
		t.Fatalf("receipt row persisted with missing user owner")
	}
}

type sidecarReceiptDB struct {
	mu              sync.Mutex
	receipts        map[memoryReceiptKey]*CostReceipt
	owners          map[memoryReceiptKey]sidecarReceiptOwner
	failOwnerInsert error
}

type sidecarReceiptOwner struct {
	userID      int64
	claimID     int64
	ownerSource string
}

func newSidecarReceiptDB() *sidecarReceiptDB {
	return &sidecarReceiptDB{
		receipts: map[memoryReceiptKey]*CostReceipt{},
		owners:   map[memoryReceiptKey]sidecarReceiptOwner{},
	}
}

func (db *sidecarReceiptDB) BeginTx(context.Context, *sql.TxOptions) (receiptTx, error) {
	return &sidecarReceiptTx{
		parent:   db,
		receipts: map[memoryReceiptKey]*CostReceipt{},
		owners:   map[memoryReceiptKey]sidecarReceiptOwner{},
	}, nil
}

func (db *sidecarReceiptDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return receiptTestResult(1), nil
}

func (db *sidecarReceiptDB) QueryRowContext(_ context.Context, query string, args ...any) receiptRow {
	db.mu.Lock()
	defer db.mu.Unlock()
	return sidecarReceiptRowForQuery(db.receipts, db.owners, query, args)
}

func (db *sidecarReceiptDB) seedReceipt(receipt *CostReceipt) {
	db.mu.Lock()
	defer db.mu.Unlock()
	key := memoryReceiptKey{tenantID: receipt.TenantID, requestID: receipt.RequestID, sequence: receipt.ReceiptSequence}
	db.receipts[key] = cloneReceipt(receipt)
}

func (db *sidecarReceiptDB) seedOwner(tenantID int64, requestID string, sequence int32, userID, claimID int64, ownerSource string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	key := memoryReceiptKey{tenantID: tenantID, requestID: requestID, sequence: sequence}
	db.owners[key] = sidecarReceiptOwner{userID: userID, claimID: claimID, ownerSource: ownerSource}
}

func (db *sidecarReceiptDB) hasReceipt(tenantID int64, requestID string, sequence int32) bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, ok := db.receipts[memoryReceiptKey{tenantID: tenantID, requestID: requestID, sequence: sequence}]
	return ok
}

type sidecarReceiptTx struct {
	parent    *sidecarReceiptDB
	receipts  map[memoryReceiptKey]*CostReceipt
	owners    map[memoryReceiptKey]sidecarReceiptOwner
	committed bool
	rolled    bool
}

func (tx *sidecarReceiptTx) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	switch {
	case strings.Contains(query, "INSERT INTO user_cost_receipt_owners"):
		if tx.parent.failOwnerInsert != nil {
			return nil, tx.parent.failOwnerInsert
		}
		key := memoryReceiptKey{tenantID: args[0].(int64), requestID: args[1].(string), sequence: args[2].(int32)}
		tx.owners[key] = sidecarReceiptOwner{
			userID:      args[3].(int64),
			claimID:     args[4].(int64),
			ownerSource: args[5].(string),
		}
		return receiptTestResult(1), nil
	case strings.Contains(query, "INSERT INTO user_cost_receipts"):
		receipt, err := receiptFromInsertArgs(args)
		if err != nil {
			return nil, err
		}
		key := memoryReceiptKey{tenantID: receipt.TenantID, requestID: receipt.RequestID, sequence: receipt.ReceiptSequence}
		tx.receipts[key] = receipt
		return receiptTestResult(1), nil
	default:
		return receiptTestResult(0), nil
	}
}

func (tx *sidecarReceiptTx) QueryRowContext(_ context.Context, query string, args ...any) receiptRow {
	tx.parent.mu.Lock()
	defer tx.parent.mu.Unlock()
	receipts := map[memoryReceiptKey]*CostReceipt{}
	owners := map[memoryReceiptKey]sidecarReceiptOwner{}
	for key, receipt := range tx.parent.receipts {
		receipts[key] = receipt
	}
	for key, owner := range tx.parent.owners {
		owners[key] = owner
	}
	for key, receipt := range tx.receipts {
		receipts[key] = receipt
	}
	for key, owner := range tx.owners {
		owners[key] = owner
	}
	return sidecarReceiptRowForQuery(receipts, owners, query, args)
}

func (tx *sidecarReceiptTx) Commit() error {
	if tx.rolled {
		return errors.New("sidecar receipt tx already rolled back")
	}
	tx.parent.mu.Lock()
	defer tx.parent.mu.Unlock()
	for key, receipt := range tx.receipts {
		tx.parent.receipts[key] = receipt
	}
	for key, owner := range tx.owners {
		tx.parent.owners[key] = owner
	}
	tx.committed = true
	return nil
}

func (tx *sidecarReceiptTx) Rollback() error {
	tx.rolled = true
	return nil
}

func sidecarReceiptRowForQuery(receipts map[memoryReceiptKey]*CostReceipt, owners map[memoryReceiptKey]sidecarReceiptOwner, query string, args []any) receiptRow {
	if !strings.Contains(query, "FROM user_cost_receipts") || len(args) < 2 {
		return receiptTestRow{err: sql.ErrNoRows}
	}
	requestID, _ := args[0].(string)
	tenantID, _ := args[1].(int64)
	var userID int64
	if strings.Contains(query, "o.user_id = $3") && len(args) >= 3 {
		userID, _ = args[2].(int64)
	}
	var latest *CostReceipt
	for key, receipt := range receipts {
		if key.requestID != requestID || key.tenantID != tenantID {
			continue
		}
		owner, hasOwner := owners[key]
		if strings.Contains(query, "INNER JOIN user_cost_receipt_owners") {
			if !hasOwner || owner.userID != userID {
				continue
			}
		}
		candidate := cloneReceipt(receipt)
		if hasOwner {
			candidate.UserID = owner.userID
			candidate.ClaimID = owner.claimID
			candidate.OwnerSource = owner.ownerSource
		}
		if latest == nil || candidate.ReceiptSequence > latest.ReceiptSequence {
			latest = candidate
		}
	}
	if latest == nil {
		return receiptTestRow{err: sql.ErrNoRows}
	}
	return receiptTestRow{receipt: latest}
}
