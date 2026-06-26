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
	// 回归:audit 插入失败时,enable 不得提交 settings,否则启用动作没有证据轨迹。
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
	// 回归:audit 插入失败时,profile 创建必须回滚,否则会出现没有审计记录的残留 profile。
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

func TestConversationDelete_AtomicWithAudit(t *testing.T) {
	// 回归:conversation 删除与 hermes.conversation.delete 审计必须一同提交或一同回滚。
	recorder := &atomicSQLRecorder{conversationID: 901, conversationTenantID: 7, conversationOwnerUserID: 42}
	tx := &atomicTx{recorder: recorder}
	beginner := &atomicBeginner{tx: tx}
	service := hermes.NewServiceWithTx(dbhermes.New(&atomicBaseDB{recorder: recorder}), beginner)
	router := NewRouter(service, nil)
	req := newAtomicHermesRequest(http.MethodDelete, "/conversations/901", ``)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// 变异检查:把 SoftDeleteConversation 或 InsertAuditEvent 移到 BeginTx 之外,会增加 baseQueries 或丢失 tx 计数。
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s want 204", resp.Code, resp.Body.String())
	}
	if recorder.txConversationReads != 1 || recorder.txConversationSoftDeletes != 1 || recorder.auditWrites != 1 {
		t.Fatalf("tx reads=%d deletes=%d audits=%d want 1/1/1",
			recorder.txConversationReads, recorder.txConversationSoftDeletes, recorder.auditWrites)
	}
	if beginner.beginCount != 1 || tx.commitCount != 1 || tx.rollbackCount != 0 {
		t.Fatalf("tx outcome begin=%d commit=%d rollback=%d want begin=1 commit=1 rollback=0",
			beginner.beginCount, tx.commitCount, tx.rollbackCount)
	}
	if recorder.auditAction != hermes.ActionConversationDelete {
		t.Fatalf("audit action=%q want %q", recorder.auditAction, hermes.ActionConversationDelete)
	}
	if recorder.baseQueries != 0 {
		t.Fatalf("base DB used outside transaction %d times", recorder.baseQueries)
	}
}

func TestConversationDelete_SecondDeleteIsIdempotent(t *testing.T) {
	// 回归:对自己拥有的 conversation 重复 DELETE,不得向客户端暴露 410/404。
	recorder := &atomicSQLRecorder{conversationID: 902, conversationTenantID: 7, conversationOwnerUserID: 42}
	tx := &atomicTx{recorder: recorder}
	beginner := &atomicBeginner{tx: tx}
	service := hermes.NewServiceWithTx(dbhermes.New(&atomicBaseDB{recorder: recorder}), beginner)
	router := NewRouter(service, nil)

	firstReq := newAtomicHermesRequest(http.MethodDelete, "/conversations/902", ``)
	firstResp := httptest.NewRecorder()
	router.ServeHTTP(firstResp, firstReq)
	if firstResp.Code != http.StatusNoContent {
		t.Fatalf("first status=%d body=%s want 204", firstResp.Code, firstResp.Body.String())
	}

	secondReq := newAtomicHermesRequest(http.MethodDelete, "/conversations/902", ``)
	secondResp := httptest.NewRecorder()
	router.ServeHTTP(secondResp, secondReq)

	// 变异检查:为 DELETE 复用 GetConversation 的 ErrGone 路径会返回 410,从而在此处失败。
	if secondResp.Code != http.StatusNoContent {
		t.Fatalf("second status=%d body=%s want 204 for idempotent delete", secondResp.Code, secondResp.Body.String())
	}
	if recorder.auditWrites != 2 || beginner.beginCount != 2 || tx.commitCount != 2 || recorder.txConversationSoftDeletes != 1 {
		t.Fatalf("audit=%d begin=%d commit=%d softDeletes=%d want two audited commits and one physical soft delete",
			recorder.auditWrites, beginner.beginCount, tx.commitCount, recorder.txConversationSoftDeletes)
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

	txSettingsWrites          int
	txProfileCreates          int
	txConversationReads       int
	txConversationSoftDeletes int
	auditWrites               int
	auditAction               string
	baseQueries               int

	conversationID          int64
	conversationTenantID    int64
	conversationOwnerUserID int64
	conversationDeleted     bool
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

func (tx *atomicTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "UPDATE hermes_conversations") {
		tx.recorder.txConversationSoftDeletes++
		tx.recorder.conversationDeleted = true
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
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
	case strings.Contains(sql, "FROM hermes_conversations"):
		tx.recorder.txConversationReads++
		return atomicRow{scan: func(dest ...any) error {
			*dest[0].(*int64) = tx.recorder.conversationID
			*dest[1].(*int64) = tx.recorder.conversationTenantID
			*dest[2].(*int64) = tx.recorder.conversationOwnerUserID
			*dest[3].(**string) = nil
			*dest[4].(*pgtype.Timestamptz) = atomicPGTime()
			*dest[5].(*pgtype.Timestamptz) = atomicPGTime()
			*dest[6].(*pgtype.Timestamptz) = pgtype.Timestamptz{}
			if tx.recorder.conversationDeleted {
				*dest[7].(*pgtype.Timestamptz) = atomicPGTime()
			} else {
				*dest[7].(*pgtype.Timestamptz) = pgtype.Timestamptz{}
			}
			return nil
		}}
	case strings.Contains(sql, "INSERT INTO hermes_audit_events"):
		tx.recorder.auditWrites++
		if len(args) > 3 {
			tx.recorder.auditAction, _ = args[3].(string)
		}
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
