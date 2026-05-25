package hermeshttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

func TestEnableForUser_AtomicWithAudit(t *testing.T) {
	// Regression: enable must not commit settings when audit insert fails, or enablement has no evidence trail.
	recorder := &atomicSQLRecorder{auditErr: errors.New("audit insert failed")}
	tx := &atomicTx{recorder: recorder}
	beginner := &atomicBeginner{tx: tx}
	service := hermes.NewServiceWithTx(dbhermes.New(&atomicBaseDB{recorder: recorder}), beginner)
	router := NewRouter(service, nil)
	req := newAtomicHermesRequest(http.MethodPost, "/settings/enable", `{}`)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// Mutation check: 将 EnableForUserWithAudit 改成不用 BeginTx/Commit 的直接 store 调用,beginCount/rollback/commit 断言会失败。
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503 audit failure", resp.Code, resp.Body.String())
	}
	if recorder.txSettingsWrites != 1 || recorder.auditWrites != 1 {
		t.Fatalf("tx writes settings=%d audit=%d want 1/1", recorder.txSettingsWrites, recorder.auditWrites)
	}
	if beginner.beginCount != 1 || tx.commitCount != 0 || tx.rollbackCount != 1 {
		t.Fatalf("tx outcome begin=%d commit=%d rollback=%d want begin=1 commit=0 rollback=1",
			beginner.beginCount, tx.commitCount, tx.rollbackCount)
	}
	if recorder.baseQueries != 0 {
		t.Fatalf("base DB used outside transaction %d times", recorder.baseQueries)
	}
}

func TestProfileCreate_AtomicWithAudit(t *testing.T) {
	// Regression: profile creation must roll back if audit insert fails, or stale profiles can exist without audit.
	recorder := &atomicSQLRecorder{auditErr: errors.New("audit insert failed")}
	tx := &atomicTx{recorder: recorder}
	beginner := &atomicBeginner{tx: tx}
	service := hermes.NewServiceWithTx(dbhermes.New(&atomicBaseDB{recorder: recorder}), beginner)
	router := NewRouter(service, nil)
	req := newAtomicHermesRequest(http.MethodPost, "/api-profiles", `{"name":"managed","kind":"managed_huakai_api"}`)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// Mutation check: 将 CreateProfileWithAudit 改成 create 后再非事务 audit,profileCreates 会缺少 rollback 防护并让本断言失败。
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503 audit failure", resp.Code, resp.Body.String())
	}
	if recorder.txProfileCreates != 1 || recorder.auditWrites != 1 {
		t.Fatalf("tx writes profile=%d audit=%d want 1/1", recorder.txProfileCreates, recorder.auditWrites)
	}
	if beginner.beginCount != 1 || tx.commitCount != 0 || tx.rollbackCount != 1 {
		t.Fatalf("tx outcome begin=%d commit=%d rollback=%d want begin=1 commit=0 rollback=1",
			beginner.beginCount, tx.commitCount, tx.rollbackCount)
	}
	if recorder.baseQueries != 0 {
		t.Fatalf("base DB used outside transaction %d times", recorder.baseQueries)
	}
}

func newAtomicHermesRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(context.WithValue(req.Context(), authContextKey{}, sessionauth.Identity{
		TenantID: 7,
		UserID:   42,
		APIKeyID: 11,
	}))
}

type atomicSQLRecorder struct {
	auditErr error

	txSettingsWrites int
	txProfileCreates int
	auditWrites      int
	baseQueries      int
}

type atomicBaseDB struct {
	recorder *atomicSQLRecorder
}

func (db *atomicBaseDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	db.recorder.baseQueries++
	return pgconn.CommandTag{}, errors.New("base db used outside Hermes transaction")
}

func (db *atomicBaseDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	db.recorder.baseQueries++
	return nil, errors.New("base db used outside Hermes transaction")
}

func (db *atomicBaseDB) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	db.recorder.baseQueries++
	return atomicRow{err: errors.New("base db used outside Hermes transaction")}
}

type atomicBeginner struct {
	tx         *atomicTx
	beginCount int
}

func (b *atomicBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	b.beginCount++
	return b.tx, nil
}

type atomicTx struct {
	recorder      *atomicSQLRecorder
	commitCount   int
	rollbackCount int
}

func (tx *atomicTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("nested tx not used")
}

func (tx *atomicTx) Commit(context.Context) error {
	tx.commitCount++
	return nil
}

func (tx *atomicTx) Rollback(context.Context) error {
	tx.rollbackCount++
	return nil
}

func (tx *atomicTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("copy not used")
}

func (tx *atomicTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}

func (tx *atomicTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (tx *atomicTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("prepare not used")
}

func (tx *atomicTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (tx *atomicTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("query not used")
}

func (tx *atomicTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "INSERT INTO hermes_settings"):
		tx.recorder.txSettingsWrites++
		return atomicRow{scan: func(dest ...any) error {
			*dest[0].(*int64) = args[0].(int64)
			*dest[1].(*int64) = args[1].(int64)
			*dest[2].(*bool) = args[2].(bool)
			*dest[3].(*string) = args[3].(string)
			*dest[4].(**int64) = optionalInt64Arg(args[4])
			*dest[5].(*pgtype.Timestamptz) = atomicPGTime()
			*dest[6].(*pgtype.Timestamptz) = atomicPGTime()
			return nil
		}}
	case strings.Contains(sql, "INSERT INTO hermes_api_profiles"):
		tx.recorder.txProfileCreates++
		return atomicRow{scan: func(dest ...any) error {
			*dest[0].(*int64) = 9001
			*dest[1].(*int64) = args[0].(int64)
			*dest[2].(*int64) = args[1].(int64)
			*dest[3].(*string) = args[2].(string)
			*dest[4].(*string) = args[3].(string)
			*dest[5].(**int64) = optionalInt64Arg(args[4])
			*dest[6].(**int64) = optionalInt64Arg(args[5])
			*dest[7].(*pgtype.Timestamptz) = atomicPGTime()
			*dest[8].(*pgtype.Timestamptz) = atomicPGTime()
			return nil
		}}
	case strings.Contains(sql, "INSERT INTO hermes_audit_events"):
		tx.recorder.auditWrites++
		return atomicRow{scan: func(dest ...any) error {
			if tx.recorder.auditErr != nil {
				return tx.recorder.auditErr
			}
			*dest[0].(*int64) = 7001
			*dest[1].(*pgtype.Timestamptz) = args[0].(pgtype.Timestamptz)
			*dest[2].(*int64) = args[1].(int64)
			*dest[3].(*int64) = args[2].(int64)
			*dest[4].(*string) = args[3].(string)
			*dest[5].(*[]byte) = args[4].([]byte)
			*dest[6].(*string) = args[5].(string)
			*dest[7].(**string) = optionalStringArg(args[6])
			*dest[8].(**string) = optionalStringArg(args[7])
			return nil
		}}
	default:
		return atomicRow{err: errors.New("unexpected Hermes SQL in atomic test")}
	}
}

func (tx *atomicTx) Conn() *pgx.Conn {
	return nil
}

type atomicRow struct {
	scan func(dest ...any) error
	err  error
}

func (r atomicRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return r.scan(dest...)
}

func optionalInt64Arg(arg any) *int64 {
	value, _ := arg.(*int64)
	return value
}

func optionalStringArg(arg any) *string {
	value, _ := arg.(*string)
	return value
}

func atomicPGTime() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Unix(1700000000, 0).UTC(), Valid: true}
}
