package channelhealth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresStoreAppendAuditProductionRequiresSigner(t *testing.T) {
	ctx := context.Background()
	db := &auditAppendTxTestDB{}
	store := NewPostgresStore(db, WithProductionRequired())

	err := store.AppendAudit(ctx, testAppendAuditEvent())
	if !errors.Is(err, ErrAuditSignerMissing) {
		t.Fatalf("AppendAudit error=%v want ErrAuditSignerMissing", err)
	}
	if db.txAuditInserts != 0 || db.committedAuditInserts != 0 || db.ledgerInserts != 0 {
		t.Fatalf("missing signer must fail before durable writes: tx_audit=%d committed_audit=%d ledger=%d",
			db.txAuditInserts, db.committedAuditInserts, db.ledgerInserts)
	}
	if db.commitCount != 0 || db.rollbackCount != 1 {
		t.Fatalf("missing signer transaction outcome: commit=%d rollback=%d want commit=0 rollback=1", db.commitCount, db.rollbackCount)
	}
}

func TestPostgresStoreAppendAuditProductionWithSignerAppendsSignedLedger(t *testing.T) {
	ctx := context.Background()
	db := &auditAppendTxTestDB{}
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := NewPostgresStoreWithAuditSigner(db, signer, WithProductionRequired())

	if err := store.AppendAudit(ctx, testAppendAuditEvent()); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
	if db.txAuditInserts != 1 || db.committedAuditInserts != 1 {
		t.Fatalf("audit inserts: tx=%d committed=%d want tx=1 committed=1", db.txAuditInserts, db.committedAuditInserts)
	}
	if db.ledgerInserts != 1 {
		t.Fatalf("ledger inserts=%d want 1", db.ledgerInserts)
	}
	if db.committedLedgerInserts != 1 {
		t.Fatalf("committed ledger inserts=%d want 1", db.committedLedgerInserts)
	}
	if db.ledgerSignature == "" || db.ledgerFingerprint != signer.Fingerprint() {
		t.Fatalf("ledger signing missing: fingerprint=%q signature_len=%d", db.ledgerFingerprint, len(db.ledgerSignature))
	}
	if db.commitCount != 1 || db.rollbackCount != 0 {
		t.Fatalf("signed audit transaction outcome: commit=%d rollback=%d want commit=1 rollback=0", db.commitCount, db.rollbackCount)
	}
}

func TestPostgresStoreAppendAuditLedgerFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	ledgerErr := errors.New("ledger sink down")
	db := &auditAppendTxTestDB{ledgerInsertErr: ledgerErr}
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := NewPostgresStoreWithAuditSigner(db, signer, WithProductionRequired())

	err = store.AppendAudit(ctx, testAppendAuditEvent())
	if !errors.Is(err, ledgerErr) {
		t.Fatalf("AppendAudit error=%v want wrapped ledger error %v", err, ledgerErr)
	}
	if !strings.Contains(err.Error(), "append trust ledger") {
		t.Fatalf("AppendAudit error=%q want ledger append context", err.Error())
	}
	if db.beginTxCount != 1 {
		t.Errorf("BeginTx calls=%d want 1", db.beginTxCount)
	}
	if db.rollbackCount != 1 {
		t.Errorf("Rollback calls=%d want 1", db.rollbackCount)
	}
	if db.commitCount != 0 {
		t.Errorf("Commit calls=%d want 0", db.commitCount)
	}
	if db.txAuditInserts != 1 {
		t.Errorf("tx audit inserts=%d want 1", db.txAuditInserts)
	}
	if db.committedAuditInserts != 0 {
		t.Errorf("committed audit inserts=%d want 0 after rollback", db.committedAuditInserts)
	}
}

func TestPostgresStoreAppendAuditDevFallbackWithoutBeginTx(t *testing.T) {
	ctx := context.Background()
	db := &auditAppendTestDB{}
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := NewPostgresStoreWithAuditSigner(db, signer)

	if err := store.AppendAudit(ctx, testAppendAuditEvent()); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
	if db.channelAuditInserts != 1 {
		t.Fatalf("channel audit inserts=%d want 1", db.channelAuditInserts)
	}
	if db.ledgerInserts != 1 {
		t.Fatalf("ledger inserts=%d want 1", db.ledgerInserts)
	}
	if db.ledgerSignature == "" || db.ledgerFingerprint != signer.Fingerprint() {
		t.Fatalf("ledger signing missing: fingerprint=%q signature_len=%d", db.ledgerFingerprint, len(db.ledgerSignature))
	}
}

func TestPostgresStoreAppendAuditProductionRequiresBeginTx(t *testing.T) {
	ctx := context.Background()
	db := &auditAppendTestDB{}
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := NewPostgresStoreWithAuditSigner(db, signer, WithProductionRequired())

	err = store.AppendAudit(ctx, testAppendAuditEvent())
	if !errors.Is(err, ErrAuditTxMissing) {
		t.Fatalf("AppendAudit error=%v want ErrAuditTxMissing", err)
	}
	if db.channelAuditInserts != 0 || db.ledgerInserts != 0 {
		t.Fatalf("missing transaction must fail before durable writes: audit=%d ledger=%d", db.channelAuditInserts, db.ledgerInserts)
	}
}

func TestPostgresStoreWithTxAppendAuditLedgerFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	ledgerErr := errors.New("ledger sink down")
	db := &auditAppendTxTestDB{ledgerInsertErr: ledgerErr}
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := NewPostgresStoreWithAuditSigner(db, signer, WithProductionRequired())

	err = store.WithTx(ctx, func(store Store) error {
		return store.AppendAudit(ctx, testAppendAuditEvent())
	})
	if !errors.Is(err, ledgerErr) {
		t.Fatalf("WithTx error=%v want wrapped ledger error %v", err, ledgerErr)
	}
	if !strings.Contains(err.Error(), "append trust ledger") {
		t.Fatalf("WithTx error=%q want ledger append context", err.Error())
	}
	if db.beginTxCount != 1 {
		t.Errorf("BeginTx calls=%d want 1", db.beginTxCount)
	}
	if db.commitCount != 0 {
		t.Errorf("Commit calls=%d want 0", db.commitCount)
	}
	if db.rollbackCount != 1 {
		t.Errorf("Rollback calls=%d want 1", db.rollbackCount)
	}
	if db.txAuditInserts != 1 {
		t.Errorf("tx audit inserts=%d want 1", db.txAuditInserts)
	}
	if db.committedAuditInserts != 0 {
		t.Errorf("committed audit inserts=%d want 0 after rollback", db.committedAuditInserts)
	}
}

func testAppendAuditEvent() AuditEvent {
	return AuditEvent{
		Type: EventManualOverride,
		Key: ChannelKey{
			TenantID:            77,
			Vendor:              "openai",
			ProviderAccountID:   88,
			AccountCredentialID: 99,
			CredentialVersion:   1,
		},
		PreviousState: StateActive,
		NewState:      StateManualPaused,
		ReasonClass:   SignalManualOverride,
		PolicyVersion: "test-policy",
		RequestID:     "req-channelhealth-audit",
		ActorID:       "operator-1",
		Payload:       map[string]any{"reason": "ops pause"},
		OccurredAt:    time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC),
	}
}

type auditAppendTestDB struct {
	channelAuditInserts int
	ledgerInserts       int
	ledgerFingerprint   string
	ledgerSignature     string
}

func (db *auditAppendTestDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(sql, "INSERT INTO channel_health_audit_events"):
		db.channelAuditInserts++
	case strings.Contains(sql, "INSERT INTO audit_ledger_entries"):
		db.ledgerInserts++
		db.ledgerFingerprint, _ = args[8].(string)
		db.ledgerSignature, _ = args[9].(string)
	}
	return pgconn.NewCommandTag("OK"), nil
}

func (db *auditAppendTestDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected query in auditAppendTestDB: %s", sql)
}

func (db *auditAppendTestDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "COUNT(*) FROM audit_ledger_entries"):
		return auditAppendIntRow{n: 0}
	case strings.Contains(sql, "SELECT merkle_root FROM audit_ledger_entries"):
		return auditAppendErrRow{err: pgx.ErrNoRows}
	default:
		return auditAppendErrRow{err: fmt.Errorf("unexpected query row: %s", sql)}
	}
}

type auditAppendIntRow struct {
	n int64
}

func (r auditAppendIntRow) Scan(dest ...any) error {
	if len(dest) != 1 {
		return fmt.Errorf("scan dest count=%d want 1", len(dest))
	}
	*dest[0].(*int64) = r.n
	return nil
}

type auditAppendErrRow struct {
	err error
}

func (r auditAppendErrRow) Scan(...any) error {
	return r.err
}

type auditAppendTxTestDB struct {
	beginTxCount           int
	commitCount            int
	rollbackCount          int
	txAuditInserts         int
	committedAuditInserts  int
	ledgerInserts          int
	ledgerFingerprint      string
	ledgerSignature        string
	ledgerInsertErr        error
	committedLedgerInserts int64
}

func (db *auditAppendTxTestDB) BeginTx(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	db.beginTxCount++
	return &auditAppendTx{parent: db}, nil
}

func (db *auditAppendTxTestDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(sql, "SELECT pg_advisory_xact_lock"):
	case strings.Contains(sql, "INSERT INTO channel_health_audit_events"):
		db.committedAuditInserts++
	case strings.Contains(sql, "INSERT INTO audit_ledger_entries"):
		db.ledgerInserts++
		db.ledgerFingerprint, _ = args[8].(string)
		db.ledgerSignature, _ = args[9].(string)
		if db.ledgerInsertErr != nil {
			return pgconn.CommandTag{}, db.ledgerInsertErr
		}
		db.committedLedgerInserts++
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected exec in auditAppendTxTestDB: %s", sql)
	}
	return pgconn.NewCommandTag("OK"), nil
}

func (db *auditAppendTxTestDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected query in auditAppendTxTestDB: %s", sql)
}

func (db *auditAppendTxTestDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "COUNT(*) FROM audit_ledger_entries"):
		return auditAppendIntRow{n: db.committedLedgerInserts}
	case strings.Contains(sql, "SELECT merkle_root FROM audit_ledger_entries"):
		return auditAppendErrRow{err: pgx.ErrNoRows}
	default:
		return auditAppendErrRow{err: fmt.Errorf("unexpected query row in auditAppendTxTestDB: %s", sql)}
	}
}

type auditAppendTx struct {
	parent              *auditAppendTxTestDB
	pendingAuditInserts int
	pendingLedgerWrites int64
	closed              bool
}

func (tx *auditAppendTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("unexpected nested transaction in auditAppendTx")
}

func (tx *auditAppendTx) Commit(context.Context) error {
	if tx.closed {
		return pgx.ErrTxClosed
	}
	tx.closed = true
	tx.parent.commitCount++
	tx.parent.committedAuditInserts += tx.pendingAuditInserts
	tx.parent.committedLedgerInserts += tx.pendingLedgerWrites
	return nil
}

func (tx *auditAppendTx) Rollback(context.Context) error {
	if tx.closed {
		return pgx.ErrTxClosed
	}
	tx.closed = true
	tx.parent.rollbackCount++
	return nil
}

func (tx *auditAppendTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("unexpected CopyFrom in auditAppendTx")
}

func (tx *auditAppendTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return auditAppendErrBatchResults{err: errors.New("unexpected SendBatch in auditAppendTx")}
}

func (tx *auditAppendTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (tx *auditAppendTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("unexpected Prepare in auditAppendTx")
}

func (tx *auditAppendTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(sql, "SELECT pg_advisory_xact_lock"):
	case strings.Contains(sql, "INSERT INTO channel_health_audit_events"):
		tx.pendingAuditInserts++
		tx.parent.txAuditInserts++
	case strings.Contains(sql, "INSERT INTO audit_ledger_entries"):
		tx.parent.ledgerInserts++
		tx.parent.ledgerFingerprint, _ = args[8].(string)
		tx.parent.ledgerSignature, _ = args[9].(string)
		if tx.parent.ledgerInsertErr != nil {
			return pgconn.CommandTag{}, tx.parent.ledgerInsertErr
		}
		tx.pendingLedgerWrites++
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected exec in auditAppendTx: %s", sql)
	}
	return pgconn.NewCommandTag("OK"), nil
}

func (tx *auditAppendTx) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected query in auditAppendTx: %s", sql)
}

func (tx *auditAppendTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "COUNT(*) FROM audit_ledger_entries"):
		return auditAppendIntRow{n: tx.parent.committedLedgerInserts}
	case strings.Contains(sql, "SELECT merkle_root FROM audit_ledger_entries"):
		return auditAppendErrRow{err: pgx.ErrNoRows}
	default:
		return auditAppendErrRow{err: fmt.Errorf("unexpected query row in auditAppendTx: %s", sql)}
	}
}

func (tx *auditAppendTx) Conn() *pgx.Conn {
	return nil
}

type auditAppendErrBatchResults struct {
	err error
}

func (r auditAppendErrBatchResults) Exec() (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, r.err
}

func (r auditAppendErrBatchResults) Query() (pgx.Rows, error) {
	return nil, r.err
}

func (r auditAppendErrBatchResults) QueryRow() pgx.Row {
	return auditAppendErrRow{err: r.err}
}

func (r auditAppendErrBatchResults) Close() error {
	return r.err
}
