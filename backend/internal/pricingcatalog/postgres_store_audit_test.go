package pricingcatalog

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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

func TestPostgresStoreUpsertWritesSignedRatioAudit(t *testing.T) {
	ctx := context.Background()
	db := newRatioAuditTxDB()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := newPostgresStoreWithAuditDB(db, signer, fixedRatioAuditNow)

	row, err := store.UpsertRatio(ctx, UpsertRatioParams{
		TenantID:    7,
		PoolGroupID: 42,
		Ratio:       decimal.RequireFromString("1.25"),
		PublicRatio: true,
		Actor:       "admin_token:99",
		ActorRole:   "platform_admin",
	})
	if err != nil {
		t.Fatalf("UpsertRatio: %v", err)
	}
	if row.RatioString() != "1.25000000" {
		t.Fatalf("upserted ratio=%q want DB-normalized 1.25000000", row.RatioString())
	}
	if got := db.ratios[ratioAuditScope{tenantID: 7, poolGroupID: 42}]; got != "1.25000000" {
		t.Fatalf("committed ratio=%q want 1.25000000", got)
	}
	if len(db.auditRows) != 1 {
		t.Fatalf("audit rows=%d want 1", len(db.auditRows))
	}
	audit := db.auditRows[0]
	if audit.Action != RatioAuditActionUpsert || audit.OldRatio != nil || audit.NewRatio == nil || *audit.NewRatio != "1.25000000" {
		t.Fatalf("audit action/old/new=%s/%v/%v", audit.Action, audit.OldRatio, audit.NewRatio)
	}
	if audit.ActorID != "admin_token:99" || audit.ActorRole != "platform_admin" {
		t.Fatalf("audit actor=%q role=%q want authenticated admin", audit.ActorID, audit.ActorRole)
	}
	if err := sign.Verify(signer.PublicKey(), audit.EntryHash, audit.Signature); err != nil {
		t.Fatalf("audit signature verify: %v", err)
	}
	if result, err := store.VerifyChain(ctx); err != nil || !result.OK {
		t.Fatalf("VerifyChain result=%+v err=%v want OK", result, err)
	}
}

func TestPostgresStoreDeleteWritesOldRatioAudit(t *testing.T) {
	ctx := context.Background()
	db := newRatioAuditTxDB()
	db.ratios[ratioAuditScope{tenantID: 7, poolGroupID: 42}] = "1.75000000"
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := newPostgresStoreWithAuditDB(db, signer, fixedRatioAuditNow)

	err = store.DeleteRatio(ctx, DeleteRatioParams{
		TenantID:    7,
		PoolGroupID: 42,
		Actor:       "admin_token:100",
		ActorRole:   "platform_admin",
	})
	if err != nil {
		t.Fatalf("DeleteRatio: %v", err)
	}
	if _, ok := db.ratios[ratioAuditScope{tenantID: 7, poolGroupID: 42}]; ok {
		t.Fatalf("ratio still present after delete")
	}
	if len(db.auditRows) != 1 {
		t.Fatalf("audit rows=%d want 1", len(db.auditRows))
	}
	audit := db.auditRows[0]
	if audit.Action != RatioAuditActionDelete || audit.OldRatio == nil || *audit.OldRatio != "1.75000000" || audit.NewRatio != nil {
		t.Fatalf("delete audit action/old/new=%s/%v/%v", audit.Action, audit.OldRatio, audit.NewRatio)
	}
	if result, err := store.VerifyChain(ctx); err != nil || !result.OK {
		t.Fatalf("VerifyChain result=%+v err=%v want OK", result, err)
	}
}

func TestPostgresStoreAuditInsertFailureRollsBackRatioMutation(t *testing.T) {
	ctx := context.Background()
	auditErr := errors.New("audit insert rejected")
	db := newRatioAuditTxDB()
	db.ratios[ratioAuditScope{tenantID: 7, poolGroupID: 42}] = "1.75000000"
	db.auditInsertErr = auditErr
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := newPostgresStoreWithAuditDB(db, signer, fixedRatioAuditNow)

	_, err = store.UpsertRatio(ctx, UpsertRatioParams{
		TenantID:    7,
		PoolGroupID: 42,
		Ratio:       decimal.RequireFromString("0.30"),
		PublicRatio: true,
		Actor:       "admin_token:99",
		ActorRole:   "platform_admin",
	})
	if !errors.Is(err, auditErr) {
		t.Fatalf("UpsertRatio error=%v want wrapped audit error", err)
	}
	if got := db.ratios[ratioAuditScope{tenantID: 7, poolGroupID: 42}]; got != "1.75000000" {
		t.Fatalf("ratio after failed audit=%q want old 1.75000000", got)
	}
	if len(db.auditRows) != 0 {
		t.Fatalf("audit rows after failed insert=%d want 0", len(db.auditRows))
	}
	if db.commitCount != 0 || db.rollbackCount != 1 {
		t.Fatalf("tx outcome commit=%d rollback=%d want 0/1", db.commitCount, db.rollbackCount)
	}
	// 变异检查:若 ratio 更新在 audit insert 之前提交,ratio 会变成
	// 0.30000000,本测试就会失败。
}

func fixedRatioAuditNow() time.Time {
	return time.Date(2026, 6, 4, 12, 30, 0, 0, time.UTC)
}

type ratioAuditScope struct {
	tenantID    int64
	poolGroupID int64
}

type ratioAuditTxDB struct {
	ratios         map[ratioAuditScope]string
	auditRows      []PricingRatioAuditEntry
	auditInsertErr error
	beginTxCount   int
	commitCount    int
	rollbackCount  int
}

func newRatioAuditTxDB() *ratioAuditTxDB {
	return &ratioAuditTxDB{ratios: map[ratioAuditScope]string{}}
}

func (db *ratioAuditTxDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	db.beginTxCount++
	snapshot := make(map[ratioAuditScope]string, len(db.ratios))
	for k, v := range db.ratios {
		snapshot[k] = v
	}
	return &ratioAuditTx{parent: db, ratios: snapshot}, nil
}

func (db *ratioAuditTxDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected direct Exec outside pricing ratio transaction")
}

func (db *ratioAuditTxDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "FROM pricing_ratio_audit_log") {
		return &ratioAuditRows{rows: cloneAuditEntriesForTest(db.auditRows)}, nil
	}
	return nil, fmt.Errorf("unexpected direct Query: %s", sql)
}

func (db *ratioAuditTxDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	return ratioAuditErrRow{err: fmt.Errorf("unexpected direct QueryRow: %s", sql)}
}

type ratioAuditTx struct {
	parent       *ratioAuditTxDB
	ratios       map[ratioAuditScope]string
	pendingAudit []PricingRatioAuditEntry
	closed       bool
}

func (tx *ratioAuditTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("unexpected nested transaction")
}

func (tx *ratioAuditTx) Commit(context.Context) error {
	if tx.closed {
		return pgx.ErrTxClosed
	}
	tx.closed = true
	tx.parent.commitCount++
	tx.parent.ratios = tx.ratios
	tx.parent.auditRows = append(tx.parent.auditRows, tx.pendingAudit...)
	return nil
}

func (tx *ratioAuditTx) Rollback(context.Context) error {
	if tx.closed {
		return pgx.ErrTxClosed
	}
	tx.closed = true
	tx.parent.rollbackCount++
	return nil
}

func (tx *ratioAuditTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("unexpected CopyFrom")
}

func (tx *ratioAuditTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return ratioAuditBatchResults{err: errors.New("unexpected SendBatch")}
}

func (tx *ratioAuditTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (tx *ratioAuditTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("unexpected Prepare")
}

func (tx *ratioAuditTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(sql, "SELECT pg_advisory_xact_lock"):
		return pgconn.NewCommandTag("OK"), nil
	case strings.Contains(sql, "INSERT INTO pricing_ratio_audit_log"):
		if tx.parent.auditInsertErr != nil {
			return pgconn.CommandTag{}, tx.parent.auditInsertErr
		}
		entry, err := auditEntryFromInsertArgsForTest(int64(len(tx.parent.auditRows)+len(tx.pendingAudit)+1), args)
		if err != nil {
			return pgconn.CommandTag{}, err
		}
		tx.pendingAudit = append(tx.pendingAudit, entry)
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec in tx: %s", sql)
	}
}

func (tx *ratioAuditTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query in tx")
}

func (tx *ratioAuditTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "INSERT INTO pool_group_pricing_ratios"):
		scope := ratioAuditScope{tenantID: args[0].(int64), poolGroupID: args[1].(int64)}
		ratio := decimal.RequireFromString(args[2].(string)).StringFixed(8)
		tx.ratios[scope] = ratio
		return ratioAuditRatioRow{tenantID: scope.tenantID, poolGroupID: scope.poolGroupID, ratio: ratio, publicRatio: args[3].(bool), createdBy: args[4].(string), updatedBy: args[4].(string)}
	case strings.Contains(sql, "DELETE FROM pool_group_pricing_ratios"):
		scope := ratioAuditScope{tenantID: args[0].(int64), poolGroupID: args[1].(int64)}
		if _, ok := tx.ratios[scope]; !ok {
			return ratioAuditErrRow{err: pgx.ErrNoRows}
		}
		delete(tx.ratios, scope)
		return ratioAuditInt64Row{n: 1}
	case strings.Contains(sql, "FROM pool_group_pricing_ratios") && strings.Contains(sql, "WHERE tenant_id = $1 AND pool_group_id = $2"):
		scope := ratioAuditScope{tenantID: args[0].(int64), poolGroupID: args[1].(int64)}
		ratio, ok := tx.ratios[scope]
		if !ok {
			return ratioAuditErrRow{err: pgx.ErrNoRows}
		}
		return ratioAuditRatioRow{tenantID: scope.tenantID, poolGroupID: scope.poolGroupID, ratio: ratio, createdBy: "seed", updatedBy: "seed"}
	case strings.Contains(sql, "SELECT entry_hash FROM pricing_ratio_audit_log"):
		entries := append(cloneAuditEntriesForTest(tx.parent.auditRows), tx.pendingAudit...)
		if len(entries) == 0 {
			return ratioAuditErrRow{err: pgx.ErrNoRows}
		}
		return ratioAuditBytesRow{b: entries[len(entries)-1].EntryHash}
	default:
		return ratioAuditErrRow{err: fmt.Errorf("unexpected QueryRow in tx: %s", sql)}
	}
}

func (tx *ratioAuditTx) Conn() *pgx.Conn {
	return nil
}

type ratioAuditRatioRow struct {
	tenantID    int64
	poolGroupID int64
	ratio       string
	publicRatio bool
	createdBy   string
	updatedBy   string
}

func (r ratioAuditRatioRow) Scan(dest ...any) error {
	if len(dest) != 9 {
		return fmt.Errorf("ratio row scan dest count=%d want 9", len(dest))
	}
	*dest[0].(*int64) = 1
	*dest[1].(*int64) = r.tenantID
	*dest[2].(*int64) = r.poolGroupID
	*dest[3].(*string) = r.ratio
	*dest[4].(*bool) = r.publicRatio
	*dest[5].(*string) = r.createdBy
	*dest[6].(*string) = r.updatedBy
	now := pgtype.Timestamptz{Time: fixedRatioAuditNow(), Valid: true}
	*dest[7].(*pgtype.Timestamptz) = now
	*dest[8].(*pgtype.Timestamptz) = now
	return nil
}

type ratioAuditInt64Row struct {
	n int64
}

func (r ratioAuditInt64Row) Scan(dest ...any) error {
	*dest[0].(*int64) = r.n
	return nil
}

type ratioAuditBytesRow struct {
	b []byte
}

func (r ratioAuditBytesRow) Scan(dest ...any) error {
	*dest[0].(*[]byte) = append([]byte(nil), r.b...)
	return nil
}

type ratioAuditErrRow struct {
	err error
}

func (r ratioAuditErrRow) Scan(...any) error {
	return r.err
}

type ratioAuditRows struct {
	rows []PricingRatioAuditEntry
	idx  int
	err  error
}

func (r *ratioAuditRows) Close() {}

func (r *ratioAuditRows) Err() error {
	return r.err
}

func (r *ratioAuditRows) CommandTag() pgconn.CommandTag {
	return pgconn.NewCommandTag("SELECT")
}

func (r *ratioAuditRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}

func (r *ratioAuditRows) Next() bool {
	return r.idx < len(r.rows)
}

func (r *ratioAuditRows) Scan(dest ...any) error {
	if r.idx >= len(r.rows) {
		return errors.New("scan after rows exhausted")
	}
	entry := r.rows[r.idx]
	r.idx++
	*dest[0].(*int64) = entry.ID
	*dest[1].(*time.Time) = entry.OccurredAt
	*dest[2].(*string) = entry.ActorID
	*dest[3].(*string) = entry.ActorRole
	*dest[4].(*int64) = entry.TenantID
	*dest[5].(*int64) = entry.PoolGroupID
	*dest[6].(*string) = entry.Action
	assignAuditTextForTest(dest[7], entry.OldRatio)
	assignAuditTextForTest(dest[8], entry.NewRatio)
	*dest[9].(*[]byte) = append([]byte(nil), entry.PrevHash...)
	*dest[10].(*[]byte) = append([]byte(nil), entry.EntryHash...)
	*dest[11].(*[]byte) = append([]byte(nil), entry.Signature...)
	*dest[12].(*string) = entry.KeyID
	return nil
}

func (r *ratioAuditRows) Values() ([]any, error) {
	return nil, errors.New("unexpected Values")
}

func (r *ratioAuditRows) RawValues() [][]byte {
	return nil
}

func (r *ratioAuditRows) Conn() *pgx.Conn {
	return nil
}

type ratioAuditBatchResults struct {
	err error
}

func (r ratioAuditBatchResults) Exec() (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, r.err
}

func (r ratioAuditBatchResults) Query() (pgx.Rows, error) {
	return nil, r.err
}

func (r ratioAuditBatchResults) QueryRow() pgx.Row {
	return ratioAuditErrRow(r)
}

func (r ratioAuditBatchResults) Close() error {
	return r.err
}

func auditEntryFromInsertArgsForTest(id int64, args []any) (PricingRatioAuditEntry, error) {
	if len(args) != 12 {
		return PricingRatioAuditEntry{}, fmt.Errorf("audit insert arg count=%d want 12", len(args))
	}
	return PricingRatioAuditEntry{
		ID:          id,
		OccurredAt:  args[0].(time.Time),
		ActorID:     args[1].(string),
		ActorRole:   args[2].(string),
		TenantID:    args[3].(int64),
		PoolGroupID: args[4].(int64),
		Action:      args[5].(string),
		OldRatio:    auditRatioArgToStringPtrForTest(args[6]),
		NewRatio:    auditRatioArgToStringPtrForTest(args[7]),
		PrevHash:    auditHashArgToBytesForTest(args[8]),
		EntryHash:   append([]byte(nil), args[9].([]byte)...),
		Signature:   append([]byte(nil), args[10].([]byte)...),
		KeyID:       args[11].(string),
	}, nil
}

func auditRatioArgToStringPtrForTest(v any) *string {
	if v == nil {
		return nil
	}
	s := v.(string)
	return &s
}

func auditHashArgToBytesForTest(v any) []byte {
	if v == nil {
		return nil
	}
	return append([]byte(nil), v.([]byte)...)
}

func assignAuditTextForTest(dest any, value *string) {
	text := dest.(*pgtype.Text)
	if value == nil {
		*text = pgtype.Text{}
		return
	}
	*text = pgtype.Text{String: *value, Valid: true}
}
