package credentialstore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestCreateAuditFailureRollsBackCredentialWithTransaction(t *testing.T) {
	db := newCredentialAuditTxFakeDB()
	db.failAuditEvent = CredentialEventCreated
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.Create(context.Background(), CreateCredentialInput{
		TenantID:          db.tenantID,
		ProviderAccountID: db.providerAccountID,
		Vendor:            VendorOpenAI,
		AuthMode:          AuthModeAPIKey,
		Payload:           []byte(`{"api_key":"sk-create-rollback"}`),
		ActorID:           "owner",
	})
	if !errors.Is(err, ErrCredentialAuditWriteFailed) {
		t.Fatalf("Create err=%v want ErrCredentialAuditWriteFailed", err)
	}
	if db.credential != nil {
		t.Fatalf("Create audit failure persisted credential: %+v", db.credential)
	}
	if db.rollbackCount != 1 || db.commitCount != 0 {
		t.Fatalf("tx counts rollback=%d commit=%d want rollback=1 commit=0", db.rollbackCount, db.commitCount)
	}
}

func TestRotateAuditFailureRollsBackVersionWithTransaction(t *testing.T) {
	db := newCredentialAuditTxFakeDB()
	db.credential = &credentialAuditTxFakeCredential{
		id: db.credentialID, tenantID: db.tenantID, providerAccountID: db.providerAccountID,
		vendor: VendorOpenAI, authMode: AuthModeAPIKey, state: StateActive, version: 1,
		payloadFingerprint: "payload-before", refreshFingerprint: "refresh-before",
	}
	db.failAuditEvent = CredentialEventRotated
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.Rotate(context.Background(), RotateCredentialInput{
		TenantID:          db.tenantID,
		ProviderAccountID: db.providerAccountID,
		CredentialID:      db.credentialID,
		Payload:           []byte(`{"api_key":"sk-rotate-rollback"}`),
		ActorID:           "owner",
	})
	if !errors.Is(err, ErrCredentialAuditWriteFailed) {
		t.Fatalf("Rotate err=%v want ErrCredentialAuditWriteFailed", err)
	}
	if db.credential.version != 1 || db.credential.payloadFingerprint != "payload-before" || db.credential.refreshFingerprint != "refresh-before" {
		t.Fatalf("Rotate audit failure mutated credential: %+v", db.credential)
	}
	if db.rollbackCount != 1 || db.commitCount != 0 {
		t.Fatalf("tx counts rollback=%d commit=%d want rollback=1 commit=0", db.rollbackCount, db.commitCount)
	}
}

type credentialAuditTxFakeDB struct {
	tenantID          int64
	providerAccountID int64
	credentialID      int64
	nextCredentialID  int64
	failAuditEvent    string
	credential        *credentialAuditTxFakeCredential
	beginCount        int
	commitCount       int
	rollbackCount     int
}

type credentialAuditTxFakeCredential struct {
	id                 int64
	tenantID           int64
	providerAccountID  int64
	vendor             string
	authMode           string
	state              string
	version            int32
	payloadFingerprint string
	refreshFingerprint string
}

func newCredentialAuditTxFakeDB() *credentialAuditTxFakeDB {
	return &credentialAuditTxFakeDB{
		tenantID: 7, providerAccountID: 77, credentialID: 301, nextCredentialID: 301,
	}
}

func (db *credentialAuditTxFakeDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	db.beginCount++
	tx := &credentialAuditTxFakeTx{parent: db}
	if db.credential != nil {
		cp := *db.credential
		tx.staged = &cp
	}
	return tx, nil
}

func (db *credentialAuditTxFakeDB) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	return db.exec(ctx, sql, args...)
}

func (db *credentialAuditTxFakeDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (db *credentialAuditTxFakeDB) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	return db.queryRow(ctx, sql, args...)
}

func (db *credentialAuditTxFakeDB) exec(_ context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "INSERT INTO credential_audit_events") && len(args) >= 4 && args[3] == db.failAuditEvent {
		return pgconn.CommandTag{}, errors.New("forced audit insert failure")
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (db *credentialAuditTxFakeDB) queryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	switch {
	case strings.Contains(sql, "FROM provider_accounts"):
		return credentialAuditTxScanRow{values: []any{db.providerAccountID}}
	case strings.Contains(sql, "FROM account_credentials ac"):
		if db.credential == nil {
			return credentialAuditTxScanRow{err: pgx.ErrNoRows}
		}
		return credentialAuditTxScanRow{values: db.credential.recordValues()}
	case strings.Contains(sql, "INSERT INTO account_credentials"):
		id := db.nextCredentialID
		db.nextCredentialID++
		db.credential = credentialAuditCredentialFromCreateArgs(id, args)
		return credentialAuditTxScanRow{values: db.credential.createMetadataValues()}
	case strings.Contains(sql, "UPDATE account_credentials") && strings.Contains(sql, "credential_version = credential_version + 1"):
		if db.credential == nil {
			return credentialAuditTxScanRow{err: pgx.ErrNoRows}
		}
		db.credential.applyRotateArgs(args)
		return credentialAuditTxScanRow{values: db.credential.metadataValues()}
	default:
		return credentialAuditTxScanRow{err: errors.New("unexpected QueryRow")}
	}
}

type credentialAuditTxFakeTx struct {
	parent *credentialAuditTxFakeDB
	staged *credentialAuditTxFakeCredential
}

func (tx *credentialAuditTxFakeTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("unexpected nested transaction")
}

func (tx *credentialAuditTxFakeTx) Commit(context.Context) error {
	tx.parent.commitCount++
	if tx.staged == nil {
		tx.parent.credential = nil
		return nil
	}
	cp := *tx.staged
	tx.parent.credential = &cp
	return nil
}

func (tx *credentialAuditTxFakeTx) Rollback(context.Context) error {
	tx.parent.rollbackCount++
	return nil
}

func (tx *credentialAuditTxFakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("unexpected CopyFrom")
}

func (tx *credentialAuditTxFakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}

func (tx *credentialAuditTxFakeTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (tx *credentialAuditTxFakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("unexpected Prepare")
}

func (tx *credentialAuditTxFakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return tx.parent.exec(ctx, sql, args...)
}

func (tx *credentialAuditTxFakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (tx *credentialAuditTxFakeTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "INSERT INTO account_credentials"):
		id := tx.parent.nextCredentialID
		tx.parent.nextCredentialID++
		tx.staged = credentialAuditCredentialFromCreateArgs(id, args)
		return credentialAuditTxScanRow{values: tx.staged.createMetadataValues()}
	case strings.Contains(sql, "UPDATE account_credentials") && strings.Contains(sql, "credential_version = credential_version + 1"):
		if tx.staged == nil {
			return credentialAuditTxScanRow{err: pgx.ErrNoRows}
		}
		tx.staged.applyRotateArgs(args)
		return credentialAuditTxScanRow{values: tx.staged.metadataValues()}
	default:
		return credentialAuditTxScanRow{err: errors.New("unexpected tx QueryRow")}
	}
}

func (tx *credentialAuditTxFakeTx) Conn() *pgx.Conn {
	return nil
}

type credentialAuditTxScanRow struct {
	values []any
	err    error
}

func (r credentialAuditTxScanRow) Scan(dest ...interface{}) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("scan destination count mismatch")
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(r.values[i]))
	}
	return nil
}

func credentialAuditCredentialFromCreateArgs(id int64, args []any) *credentialAuditTxFakeCredential {
	return &credentialAuditTxFakeCredential{
		id: id, tenantID: args[0].(int64), providerAccountID: args[1].(int64),
		vendor: args[2].(string), authMode: args[3].(string), state: StateActive, version: 1,
		payloadFingerprint: credentialAuditStringArg(args[9]), refreshFingerprint: credentialAuditStringArg(args[10]),
	}
}

func (c *credentialAuditTxFakeCredential) applyRotateArgs(args []any) {
	c.state = StateActive
	c.version++
	c.payloadFingerprint = credentialAuditStringArg(args[5])
	c.refreshFingerprint = credentialAuditStringArg(args[6])
}

func (c credentialAuditTxFakeCredential) metadataValues() []any {
	now := pgtype.Timestamptz{Time: time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC), Valid: true}
	emptyTime := pgtype.Timestamptz{}
	var nilString *string
	return []any{
		c.id, c.tenantID, c.providerAccountID, c.vendor, c.authMode, c.state, c.version,
		emptyTime, emptyTime, emptyTime, nilString,
		nilString, int32(0), now, now,
	}
}

// createMetadataValues 与 Create 的 RETURNING 列表保持一致:它在 created_at/updated_at
// 末尾之前先暴露两个 external-identity 列(external_account_id、external_account_email)。
// Rotate 的 RETURNING 省略了它们,因此 Rotate 沿用 metadataValues。
func (c credentialAuditTxFakeCredential) createMetadataValues() []any {
	now := pgtype.Timestamptz{Time: time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC), Valid: true}
	emptyTime := pgtype.Timestamptz{}
	var nilString *string
	return []any{
		c.id, c.tenantID, c.providerAccountID, c.vendor, c.authMode, c.state, c.version,
		emptyTime, emptyTime, emptyTime, nilString,
		nilString, int32(0), nilString, nilString, now, now,
	}
}

func (c credentialAuditTxFakeCredential) recordValues() []any {
	now := pgtype.Timestamptz{Time: time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC), Valid: true}
	emptyTime := pgtype.Timestamptz{}
	var nilString *string
	payloadFP := c.payloadFingerprint
	refreshFP := c.refreshFingerprint
	return []any{
		c.id, c.tenantID, c.providerAccountID, c.vendor, c.authMode, c.state,
		c.version, []byte("ciphertext"), "aes-256-gcm", "test-key",
		[]byte("nonce"), "aad-hash", &payloadFP, &refreshFP,
		emptyTime, emptyTime, emptyTime, emptyTime,
		emptyTime, nilString, nilString, int32(0),
		emptyTime, now, now, emptyTime,
	}
}

func credentialAuditStringArg(v any) string {
	switch typed := v.(type) {
	case *string:
		if typed == nil {
			return ""
		}
		return *typed
	case string:
		return typed
	default:
		return ""
	}
}
