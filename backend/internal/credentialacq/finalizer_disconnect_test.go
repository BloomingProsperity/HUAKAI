// HUAKAI · iKun

// Finalize 交付后写脱钩判别测试: creator.Create 已提交后客户端断连(请求 ctx 取消),
// MarkFinalized/MarkFailed 若随 ctx 一起死, flow 留在 consumed 非终态 —— 活凭据孤儿 +
// 重试恒 ErrFlowReplay 卡死。fake DB 默认忽略 ctx, 这里用 ctx 敏感包装复现真 pgx 行为。

package credentialacq

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// ctxSensitiveSessionDB 模拟真 pgx: ctx 已取消时任何操作返回 ctx.Err();
// 另可对 MarkFinalized 的 UPDATE 注入瞬时失败 (测重试)。
type ctxSensitiveSessionDB struct {
	inner                 db.DBTX
	markFinalizedFailures int
	markFinalizedCalls    int
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

func (d *ctxSensitiveSessionDB) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if err := ctx.Err(); err != nil {
		return pgconn.CommandTag{}, err
	}
	return d.inner.Exec(ctx, sql, args...)
}

func (d *ctxSensitiveSessionDB) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.inner.Query(ctx, sql, args...)
}

func (d *ctxSensitiveSessionDB) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	if err := ctx.Err(); err != nil {
		return errRow{err: err}
	}
	if strings.Contains(sql, "SET status = 'finalized'") {
		d.markFinalizedCalls++
		if d.markFinalizedFailures > 0 {
			d.markFinalizedFailures--
			return errRow{err: errors.New("transient db blip")}
		}
	}
	return d.inner.QueryRow(ctx, sql, args...)
}

// cancelingCreator 在 Create 期间取消请求 ctx (模拟客户端在 Create 提交窗口断连)。
type cancelingCreator struct {
	inner  *fakeCredentialCreator
	cancel context.CancelFunc
	err    error
}

func (c *cancelingCreator) Create(ctx context.Context, in credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error) {
	meta, err := c.inner.Create(ctx, in)
	c.cancel()
	if c.err != nil {
		return credentialstore.CredentialMetadata{}, c.err
	}
	return meta, err
}

func newDisconnectTestStore(t *testing.T, flowID string, dbw db.DBTX) *PostgresSessionStore {
	t.Helper()
	now := time.Date(2026, 7, 5, 15, 0, 0, 0, time.UTC)
	store := NewPostgresSessionStore(dbw).WithNow(func() time.Time { return now })
	if _, err := store.Create(context.Background(), Session{
		ID: flowID, TenantID: 1, ProviderAccountID: 2,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		Kind: FlowKindPaste, Status: StatusStarted,
		ActorID: "admin-1", ActorRole: "platform_admin",
		ClientIdentitySource: ClientSourceNone, RedactedContext: map[string]any{},
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return store
}

func disconnectCandidate() CredentialCandidate {
	return CredentialCandidate{
		TenantID: 1, ProviderAccountID: 2,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		Payload: samplePayloadForMode(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth),
		ActorID: "admin-1",
	}
}

// TestFinalizeSurvivesDisconnectAfterCreate 守 A#8 核心: Create 提交后断连,
// MarkFinalized 必须脱钩完成, flow 终态 finalized、凭据回链完整。
// mutation: markFinalizedWithRetry 退回直接 MarkFinalized(ctx) → flow 卡 consumed
// 非终态 → 断言红。
func TestFinalizeSurvivesDisconnectAfterCreate(t *testing.T) {
	now := time.Date(2026, 7, 5, 15, 0, 0, 0, time.UTC)
	dbw := &ctxSensitiveSessionDB{inner: newTestSessionDB(now)}
	store := newDisconnectTestStore(t, "flow-disc-ok", dbw)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	creator := &cancelingCreator{inner: &fakeCredentialCreator{}, cancel: cancel}
	finalizer := NewFinalizer(store, credentialstore.DefaultHandlerRegistry(), creator, nil)

	res, err := finalizer.Finalize(ctx, "flow-disc-ok", disconnectCandidate(), "admin-1", "req-1")
	if err != nil {
		t.Fatalf("断连后 finalize 应成功: %v (活凭据孤儿 + flow 卡死)", err)
	}
	if res.Session.Status != StatusFinalized || res.Session.ResultAccountCredentialID != res.Credential.ID {
		t.Fatalf("session=%+v credential=%d, want finalized+回链", res.Session.Status, res.Credential.ID)
	}
	stored, err := store.Get(context.Background(), "flow-disc-ok")
	if err != nil || stored.Status != StatusFinalized {
		t.Fatalf("库内 status=%v err=%v, want finalized", stored.Status, err)
	}
}

// TestFinalizeCreateFailureMarksFailedDespiteDisconnect 守 A#9b: Create 失败且断连,
// MarkFailed 补偿必须脱钩完成, flow 终态 failed 而非卡 consumed 非终态。
// mutation: create-fail 分支退回 MarkFailed(ctx) → flow 卡死 → 库内断言红。
func TestFinalizeCreateFailureMarksFailedDespiteDisconnect(t *testing.T) {
	now := time.Date(2026, 7, 5, 15, 0, 0, 0, time.UTC)
	dbw := &ctxSensitiveSessionDB{inner: newTestSessionDB(now)}
	store := newDisconnectTestStore(t, "flow-disc-fail", dbw)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	createErr := errors.New("upstream rejected")
	creator := &cancelingCreator{inner: &fakeCredentialCreator{}, cancel: cancel, err: createErr}
	finalizer := NewFinalizer(store, credentialstore.DefaultHandlerRegistry(), creator, nil)

	_, err := finalizer.Finalize(ctx, "flow-disc-fail", disconnectCandidate(), "admin-1", "req-1")
	if !errors.Is(err, createErr) {
		t.Fatalf("err=%v, want create 错误上抛", err)
	}
	stored, gerr := store.Get(context.Background(), "flow-disc-fail")
	if gerr != nil || stored.Status != StatusFailed {
		t.Fatalf("库内 status=%v err=%v, want failed (补偿写随断连丢失 → flow 卡死)", stored.Status, gerr)
	}
}

// TestMarkFinalizedRetriesTransientFailure 守重试: 凭据已建成, 状态写两次瞬时失败
// 后第三次成功 → finalize 仍成功。
// mutation: markFinalizedWithRetry 退回单次尝试 → 返回错误 → 红。
func TestMarkFinalizedRetriesTransientFailure(t *testing.T) {
	now := time.Date(2026, 7, 5, 15, 0, 0, 0, time.UTC)
	dbw := &ctxSensitiveSessionDB{inner: newTestSessionDB(now), markFinalizedFailures: 2}
	store := newDisconnectTestStore(t, "flow-retry", dbw)
	finalizer := NewFinalizer(store, credentialstore.DefaultHandlerRegistry(), &fakeCredentialCreator{}, nil)

	res, err := finalizer.Finalize(context.Background(), "flow-retry", disconnectCandidate(), "admin-1", "req-1")
	if err != nil {
		t.Fatalf("两次瞬时失败后应重试成功: %v", err)
	}
	if res.Session.Status != StatusFinalized || dbw.markFinalizedCalls != 3 {
		t.Fatalf("status=%v calls=%d, want finalized/3", res.Session.Status, dbw.markFinalizedCalls)
	}
}
