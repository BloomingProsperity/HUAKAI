package hermeshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

func TestListConversationsReadsPostgresWithOwnerAndPagination(t *testing.T) {
	// 回归:list 不得代理到 runner,且必须在 PG 调用中保留 tenant+owner+有界分页。
	store := &conversationHTTPStore{
		listConversationsRows: []dbhermes.HermesConversation{{
			ID: 701, TenantID: 7, OwnerUserID: 42, Title: stringPtrHTTPTest("own"),
			ActorSource: "token", ActorID: 99,
			CreatedAt: httpTestPGTime(), UpdatedAt: httpTestPGTime(),
		}},
	}
	router := NewRouter(hermes.NewService(store), nil)
	req := newConversationHTTPRequest(http.MethodGet, "/conversations?limit=500&offset=3", "")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// 变异检查:若仍走代理模式,会因 runner 为 nil 而返回 503;去掉 owner/tenant/limit 处理会破坏参数断言。
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", resp.Code, resp.Body.String())
	}
	if !store.listConversationsCalled ||
		store.listConversationsArg.TenantID != 7 ||
		store.listConversationsArg.OwnerUserID != 42 ||
		store.listConversationsArg.ActorSource != "token" ||
		store.listConversationsArg.ActorID != 99 ||
		store.listConversationsArg.PageLimit != 200 ||
		store.listConversationsArg.PageOffset != 3 {
		t.Fatalf("list arg=%+v called=%v want tenant=7 owner=42 limit=200 offset=3",
			store.listConversationsArg, store.listConversationsCalled)
	}
	var body struct {
		Conversations []struct {
			ID          int64  `json:"id"`
			OwnerUserID int64  `json:"owner_user_id"`
			Title       string `json:"title"`
		} `json:"conversations"`
		Limit  int32 `json:"limit"`
		Offset int32 `json:"offset"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("response json: %v body=%s", err, resp.Body.String())
	}
	if body.Limit != 200 || body.Offset != 3 || len(body.Conversations) != 1 ||
		body.Conversations[0].ID != 701 || body.Conversations[0].OwnerUserID != 42 {
		t.Fatalf("body=%+v want one owner-scoped conversation with capped limit", body)
	}
}

func TestGetConversationCrossOwner404AndDeleted410(t *testing.T) {
	tests := []struct {
		name       string
		row        dbhermes.HermesConversation
		wantStatus int
	}{
		{
			name: "cross owner returns 404",
			row: dbhermes.HermesConversation{
				ID: 801, TenantID: 7, OwnerUserID: 99,
				ActorSource: "token", ActorID: 99,
				CreatedAt: httpTestPGTime(), UpdatedAt: httpTestPGTime(),
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "same tenant other administrator returns 404",
			row: dbhermes.HermesConversation{
				ID: 803, TenantID: 7, OwnerUserID: 42,
				ActorSource: "token", ActorID: 777,
				CreatedAt: httpTestPGTime(), UpdatedAt: httpTestPGTime(),
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "soft deleted returns 410",
			row: dbhermes.HermesConversation{
				ID: 802, TenantID: 7, OwnerUserID: 42,
				ActorSource: "token", ActorID: 99,
				CreatedAt: httpTestPGTime(), UpdatedAt: httpTestPGTime(),
				DeletedAt: pgtype.Timestamptz{Time: httpTestPGTime().Time, Valid: true},
			},
			wantStatus: http.StatusGone,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &conversationHTTPStore{conversationRow: tc.row}
			router := NewRouter(hermes.NewService(store), nil)
			req := newConversationHTTPRequest(http.MethodGet, "/conversations/"+itoaHTTPTest(tc.row.ID), "")
			resp := httptest.NewRecorder()

			router.ServeHTTP(resp, req)

			// 变异检查:缺少 owner/deleted 检查会返回 200,从而使本断言失败。
			if resp.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s want %d", resp.Code, resp.Body.String(), tc.wantStatus)
			}
		})
	}
}

func TestListConversationMessagesRejectsCrossOwner404(t *testing.T) {
	// 回归:对他人的 conversation id 不得返回 200 + 空 messages,那会通过行为泄露其存在性。
	store := &conversationHTTPStore{conversationRow: dbhermes.HermesConversation{
		ID: 901, TenantID: 7, OwnerUserID: 99,
		ActorSource: "token", ActorID: 99,
		CreatedAt: httpTestPGTime(), UpdatedAt: httpTestPGTime(),
	}}
	router := NewRouter(hermes.NewService(store), nil)
	req := newConversationHTTPRequest(http.MethodGet, "/conversations/901/messages", "")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// 变异检查:直接调用 list 查询并编码一个空切片会返回 200 而非 404。
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s want 404", resp.Code, resp.Body.String())
	}
	if store.listMessagesCalled {
		t.Fatalf("message list was called for foreign owner")
	}
}

func TestListConversationMessagesReadsPostgresWithOwner(t *testing.T) {
	// 回归:message list 必须使用带 tenant、conversation id 和 owner id 的 PG store。
	store := &conversationHTTPStore{
		conversationRow: dbhermes.HermesConversation{
			ID: 902, TenantID: 7, OwnerUserID: 42,
			ActorSource: "token", ActorID: 99,
			CreatedAt: httpTestPGTime(), UpdatedAt: httpTestPGTime(),
		},
		listMessagesRows: []dbhermes.ListMessagesByConversationRow{{
			ID: 903, TenantID: 7, ConversationID: 902, Role: "assistant",
			Content: []byte(`{"type":"text","text":"hi"}`), CreatedAt: httpTestPGTime(),
		}},
	}
	router := NewRouter(hermes.NewService(store), nil)
	req := newConversationHTTPRequest(http.MethodGet, "/conversations/902/messages?limit=5&offset=1", "")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// 变异检查:若仍走代理模式会返回 503;去掉 owner/limit 参数会破坏这些断言。
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", resp.Code, resp.Body.String())
	}
	if !store.listMessagesCalled ||
		store.listMessagesArg.TenantID != 7 ||
		store.listMessagesArg.ConversationID != 902 ||
		store.listMessagesArg.OwnerUserID != 42 ||
		store.listMessagesArg.ActorSource != "token" ||
		store.listMessagesArg.ActorID != 99 ||
		store.listMessagesArg.PageLimit != 5 ||
		store.listMessagesArg.PageOffset != 1 {
		t.Fatalf("list messages arg=%+v called=%v want tenant=7 conv=902 owner=42 limit=5 offset=1",
			store.listMessagesArg, store.listMessagesCalled)
	}
	if !strings.Contains(resp.Body.String(), `"messages"`) || !strings.Contains(resp.Body.String(), `"hi"`) {
		t.Fatalf("body=%s want messages with JSON content", resp.Body.String())
	}
}

func TestDeleteConversationUnknownWritesFailureAudit(t *testing.T) {
	// 回归:针对未知 id 的破坏性删除尝试必须在失败审计轨迹中保持可见。
	recorder := &conversationDeleteAuditRecorder{
		existingConversationID: 1001,
		existingTenantID:       7,
		existingOwnerUserID:    42,
	}
	router, tx, beginner := newConversationDeleteAuditRouter(recorder)
	req := newConversationHTTPRequest(http.MethodDelete, "/conversations/9999", "")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// 变异检查:删掉 handler 的失败审计调用会让 auditEvents 为空,而 404 仍然通过。
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s want 404", resp.Code, resp.Body.String())
	}
	requireConversationDeleteAudit(t, recorder, hermes.AuditResultFailure, 9999)
	if recorder.baseAuditWrites != 1 || recorder.txAuditWrites != 0 {
		t.Fatalf("audit writes base=%d tx=%d want one failure audit outside failed delete tx",
			recorder.baseAuditWrites, recorder.txAuditWrites)
	}
	if recorder.txConversationSoftDeletes != 0 || tx.commitCount != 0 || tx.rollbackCount != 1 || beginner.beginCount != 1 {
		t.Fatalf("delete tx softDeletes=%d begin=%d commit=%d rollback=%d want failed tx rollback without delete",
			recorder.txConversationSoftDeletes, beginner.beginCount, tx.commitCount, tx.rollbackCount)
	}
}

func TestDeleteConversationSuccessWritesSingleSuccessAudit(t *testing.T) {
	// 回归:成功审计由 SoftDeleteConversationWithAudit 原子写入;handler 不得重复审计。
	recorder := &conversationDeleteAuditRecorder{
		existingConversationID: 1002,
		existingTenantID:       7,
		existingOwnerUserID:    42,
	}
	router, tx, beginner := newConversationDeleteAuditRouter(recorder)
	req := newConversationHTTPRequest(http.MethodDelete, "/conversations/1002", "")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// 变异检查:再加一个 handler 层的成功审计会产生两行,从而使本断言失败。
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s want 204", resp.Code, resp.Body.String())
	}
	requireConversationDeleteAudit(t, recorder, hermes.AuditResultSuccess, 1002)
	if recorder.baseAuditWrites != 0 || recorder.txAuditWrites != 1 {
		t.Fatalf("audit writes base=%d tx=%d want one success audit inside delete tx",
			recorder.baseAuditWrites, recorder.txAuditWrites)
	}
	if recorder.txConversationSoftDeletes != 1 || tx.commitCount != 1 || tx.rollbackCount != 0 || beginner.beginCount != 1 {
		t.Fatalf("delete tx softDeletes=%d begin=%d commit=%d rollback=%d want committed delete with one soft delete",
			recorder.txConversationSoftDeletes, beginner.beginCount, tx.commitCount, tx.rollbackCount)
	}
}

func TestDeleteConversationCrossTenantWritesFailureAudit(t *testing.T) {
	// 回归:对属于另一 tenant 的 id 做删除探测必须表现为 404,且仍要留下失败审计证据。
	recorder := &conversationDeleteAuditRecorder{
		existingConversationID: 1003,
		existingTenantID:       8,
		existingOwnerUserID:    42,
	}
	router, tx, beginner := newConversationDeleteAuditRouter(recorder)
	req := newConversationHTTPRequest(http.MethodDelete, "/conversations/1003", "")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// 变异检查:返回 204/省略失败审计,都会要么泄露、要么隐藏这次破坏性探测。
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s want 404", resp.Code, resp.Body.String())
	}
	requireConversationDeleteAudit(t, recorder, hermes.AuditResultFailure, 1003)
	if recorder.baseAuditWrites != 1 || recorder.txAuditWrites != 0 {
		t.Fatalf("audit writes base=%d tx=%d want one failure audit outside failed delete tx",
			recorder.baseAuditWrites, recorder.txAuditWrites)
	}
	if recorder.txConversationSoftDeletes != 0 || tx.commitCount != 0 || tx.rollbackCount != 1 || beginner.beginCount != 1 {
		t.Fatalf("delete tx softDeletes=%d begin=%d commit=%d rollback=%d want failed tx rollback without delete",
			recorder.txConversationSoftDeletes, beginner.beginCount, tx.commitCount, tx.rollbackCount)
	}
}

func newConversationHTTPRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := context.WithValue(req.Context(), authContextKey{}, sessionauth.Identity{
		TenantID: 7,
		UserID:   42,
		APIKeyID: 11,
	})
	ctx = context.WithValue(ctx, adminActorContextKey{}, adminActor{
		Source: admin.AdminSourceToken,
		ID:     99,
		Role:   admin.RolePlatformAdmin,
	})
	return req.WithContext(ctx)
}

type conversationHTTPStore struct {
	chatStoreStub

	conversationRow dbhermes.HermesConversation
	conversationErr error

	listConversationsCalled bool
	listConversationsArg    dbhermes.ListConversationsByOwnerParams
	listConversationsRows   []dbhermes.HermesConversation
	listConversationsErr    error

	listMessagesCalled bool
	listMessagesArg    dbhermes.ListMessagesByConversationParams
	listMessagesRows   []dbhermes.ListMessagesByConversationRow
	listMessagesErr    error

	softDeleteCalled bool
	softDeleteArg    dbhermes.SoftDeleteConversationParams
	softDeleteRows   int64
	softDeleteErr    error
}

func (s *conversationHTTPStore) GetConversation(_ context.Context, arg dbhermes.GetConversationParams) (dbhermes.HermesConversation, error) {
	if s.conversationErr != nil {
		return dbhermes.HermesConversation{}, s.conversationErr
	}
	if s.conversationRow.ID != 0 {
		return s.conversationRow, nil
	}
	return dbhermes.HermesConversation{
		ID: arg.ID, TenantID: arg.TenantID, OwnerUserID: arg.OwnerUserID,
		ActorSource: arg.ActorSource, ActorID: arg.ActorID,
	}, nil
}

func (s *conversationHTTPStore) ListConversationsByOwner(_ context.Context, arg dbhermes.ListConversationsByOwnerParams) ([]dbhermes.HermesConversation, error) {
	s.listConversationsCalled = true
	s.listConversationsArg = arg
	if s.listConversationsErr != nil {
		return nil, s.listConversationsErr
	}
	return s.listConversationsRows, nil
}

func (s *conversationHTTPStore) ListMessagesByConversation(_ context.Context, arg dbhermes.ListMessagesByConversationParams) ([]dbhermes.ListMessagesByConversationRow, error) {
	s.listMessagesCalled = true
	s.listMessagesArg = arg
	if s.listMessagesErr != nil {
		return nil, s.listMessagesErr
	}
	return s.listMessagesRows, nil
}

func (s *conversationHTTPStore) SoftDeleteConversation(_ context.Context, arg dbhermes.SoftDeleteConversationParams) (int64, error) {
	s.softDeleteCalled = true
	s.softDeleteArg = arg
	if s.softDeleteErr != nil {
		return 0, s.softDeleteErr
	}
	return s.softDeleteRows, nil
}

func httpTestPGTime() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Unix(1700000000, 0).UTC(), Valid: true}
}

func stringPtrHTTPTest(value string) *string {
	return &value
}

func itoaHTTPTest(value int64) string {
	return strconv.FormatInt(value, 10)
}

func newConversationDeleteAuditRouter(recorder *conversationDeleteAuditRecorder) (http.Handler, *conversationDeleteAuditTx, *conversationDeleteAuditBeginner) {
	tx := &conversationDeleteAuditTx{recorder: recorder}
	beginner := &conversationDeleteAuditBeginner{tx: tx}
	service := hermes.NewServiceWithTx(dbhermes.New(&conversationDeleteAuditBaseDB{recorder: recorder}), beginner)
	return NewRouter(service, nil), tx, beginner
}

func requireConversationDeleteAudit(t *testing.T, recorder *conversationDeleteAuditRecorder, result string, conversationID int64) {
	t.Helper()
	if len(recorder.auditEvents) != 1 {
		t.Fatalf("audit events=%d want exactly 1 (%+v)", len(recorder.auditEvents), recorder.auditEvents)
	}
	event := recorder.auditEvents[0]
	if event.TenantID != 7 || event.ActorSource != admin.AdminSourceToken || event.ActorID != 99 || event.ActorRole != admin.RolePlatformAdmin ||
		event.Action != hermes.ActionConversationDelete || event.Result != result {
		t.Fatalf("日志=%+v，期望租户=7、管理员令牌=99、动作=%q、结果=%q",
			event, hermes.ActionConversationDelete, result)
	}
	var args map[string]any
	if err := json.Unmarshal(event.SanitizedArgs, &args); err != nil {
		t.Fatalf("audit args json: %v bytes=%s", err, string(event.SanitizedArgs))
	}
	if args["conversation_id"] != float64(conversationID) {
		t.Fatalf("audit args=%v want conversation_id=%d", args, conversationID)
	}
}

type conversationDeleteAuditRecorder struct {
	existingConversationID int64
	existingTenantID       int64
	existingOwnerUserID    int64
	conversationDeleted    bool

	txConversationReads       int
	txConversationSoftDeletes int
	txAuditWrites             int
	baseAuditWrites           int
	auditEvents               []dbhermes.InsertAuditEventParams
}

func (r *conversationDeleteAuditRecorder) auditRow(args []any) pgx.Row {
	event := dbhermes.InsertAuditEventParams{
		Ts:            args[0].(pgtype.Timestamptz),
		TenantID:      args[1].(int64),
		ActorSource:   args[2].(string),
		ActorID:       args[3].(int64),
		ActorRole:     args[4].(string),
		Action:        args[5].(string),
		SanitizedArgs: append([]byte(nil), args[6].([]byte)...),
		Result:        args[7].(string),
		CorrelationID: conversationDeleteAuditStringArg(args[8]),
		RequestID:     conversationDeleteAuditStringArg(args[9]),
		LogCategory:   args[10].(string),
	}
	r.auditEvents = append(r.auditEvents, event)
	return conversationDeleteAuditRow{scan: func(dest ...any) error {
		*dest[0].(*int64) = int64(7000 + len(r.auditEvents))
		*dest[1].(*pgtype.Timestamptz) = event.Ts
		*dest[2].(*int64) = event.TenantID
		*dest[3].(*string) = event.Action
		*dest[4].(*[]byte) = event.SanitizedArgs
		*dest[5].(*string) = event.Result
		*dest[6].(**string) = event.CorrelationID
		*dest[7].(**string) = event.RequestID
		*dest[8].(*pgtype.Timestamptz) = event.Ts
		*dest[9].(*string) = event.LogCategory
		*dest[10].(*string) = event.ActorSource
		*dest[11].(*int64) = event.ActorID
		*dest[12].(**string) = stringPtrHTTPTest(event.ActorRole)
		return nil
	}}
}

func (r *conversationDeleteAuditRecorder) conversationRow() pgx.Row {
	return conversationDeleteAuditRow{scan: func(dest ...any) error {
		*dest[0].(*int64) = r.existingConversationID
		*dest[1].(*int64) = r.existingTenantID
		*dest[2].(*int64) = r.existingOwnerUserID
		*dest[3].(**string) = nil
		*dest[4].(*pgtype.Timestamptz) = httpTestPGTime()
		*dest[5].(*pgtype.Timestamptz) = httpTestPGTime()
		*dest[6].(*pgtype.Timestamptz) = pgtype.Timestamptz{}
		if r.conversationDeleted {
			*dest[7].(*pgtype.Timestamptz) = httpTestPGTime()
		} else {
			*dest[7].(*pgtype.Timestamptz) = pgtype.Timestamptz{}
		}
		*dest[8].(*string) = admin.AdminSourceToken
		*dest[9].(*int64) = 99
		*dest[10].(**string) = stringPtrHTTPTest(admin.RolePlatformAdmin)
		return nil
	}}
}

type conversationDeleteAuditBaseDB struct {
	recorder *conversationDeleteAuditRecorder
}

func (db *conversationDeleteAuditBaseDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected base exec")
}

func (db *conversationDeleteAuditBaseDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("unexpected base query")
}

func (db *conversationDeleteAuditBaseDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	if strings.Contains(sql, "INSERT INTO hermes_audit_events") {
		db.recorder.baseAuditWrites++
		return db.recorder.auditRow(args)
	}
	return conversationDeleteAuditRow{err: errors.New("unexpected base query row")}
}

type conversationDeleteAuditBeginner struct {
	tx         *conversationDeleteAuditTx
	beginCount int
}

func (b *conversationDeleteAuditBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	b.beginCount++
	return b.tx, nil
}

type conversationDeleteAuditTx struct {
	recorder      *conversationDeleteAuditRecorder
	commitCount   int
	rollbackCount int
}

func (tx *conversationDeleteAuditTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("nested tx not used")
}

func (tx *conversationDeleteAuditTx) Commit(context.Context) error {
	tx.commitCount++
	return nil
}

func (tx *conversationDeleteAuditTx) Rollback(context.Context) error {
	tx.rollbackCount++
	return nil
}

func (tx *conversationDeleteAuditTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("copy not used")
}

func (tx *conversationDeleteAuditTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}

func (tx *conversationDeleteAuditTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (tx *conversationDeleteAuditTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("prepare not used")
}

func (tx *conversationDeleteAuditTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "UPDATE hermes_conversations") {
		tx.recorder.txConversationSoftDeletes++
		if tx.recorder.matchesConversationArgs(args) {
			tx.recorder.conversationDeleted = true
			return pgconn.NewCommandTag("UPDATE 1"), nil
		}
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	return pgconn.CommandTag{}, errors.New("unexpected tx exec")
}

func (tx *conversationDeleteAuditTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("query not used")
}

func (tx *conversationDeleteAuditTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "FROM hermes_conversations"):
		tx.recorder.txConversationReads++
		if !tx.recorder.matchesConversationArgs(args) {
			return conversationDeleteAuditRow{err: pgx.ErrNoRows}
		}
		return tx.recorder.conversationRow()
	case strings.Contains(sql, "INSERT INTO hermes_audit_events"):
		tx.recorder.txAuditWrites++
		return tx.recorder.auditRow(args)
	default:
		return conversationDeleteAuditRow{err: errors.New("unexpected tx query row")}
	}
}

func (tx *conversationDeleteAuditTx) Conn() *pgx.Conn {
	return nil
}

func (r *conversationDeleteAuditRecorder) matchesConversationArgs(args []any) bool {
	if len(args) < 5 || r.existingConversationID == 0 {
		return false
	}
	id, idOK := args[0].(int64)
	tenantID, tenantOK := args[1].(int64)
	ownerUserID, ownerOK := args[2].(int64)
	actorSource, sourceOK := args[3].(string)
	actorID, actorOK := args[4].(int64)
	return idOK && tenantOK && ownerOK && sourceOK && actorOK &&
		id == r.existingConversationID && tenantID == r.existingTenantID &&
		ownerUserID == r.existingOwnerUserID && actorSource == admin.AdminSourceToken && actorID == 99
}

type conversationDeleteAuditRow struct {
	scan func(dest ...any) error
	err  error
}

func (r conversationDeleteAuditRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.scan == nil {
		return errors.New("missing row scanner")
	}
	return r.scan(dest...)
}

func conversationDeleteAuditStringArg(arg any) *string {
	value, _ := arg.(*string)
	return value
}
