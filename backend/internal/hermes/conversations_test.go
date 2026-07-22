package hermes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/jackc/pgx/v5/pgtype"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

func TestListConversationsByOwnerPassesTenantOwnerAndPagination(t *testing.T) {
	// 回归守护:会话列表必须按 tenant+owner 限定范围;去掉任一过滤条件都会泄露其他用户的历史记录。
	store := &hermesStoreSpy{
		listConversationsRows: []dbhermes.HermesConversation{{
			ID: 101, TenantID: 7, OwnerUserID: 42, Title: stringPtrForTest("own"),
			ActorSource: "token", ActorID: 42,
			CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
		}},
	}
	service := NewService(store)

	got, err := service.ListConversationsByOwner(context.Background(), 7, 42, "token", 42, 25, 3)
	if err != nil {
		t.Fatalf("ListConversationsByOwner: %v", err)
	}

	// 变异检测:将 OwnerUserID 或 TenantID 置零,或忽略分页,都会使此断言失败。
	if !store.listConversationsCalled ||
		store.listConversationsArg.TenantID != 7 ||
		store.listConversationsArg.OwnerUserID != 42 ||
		store.listConversationsArg.ActorSource != "token" ||
		store.listConversationsArg.ActorID != 42 ||
		store.listConversationsArg.PageLimit != 25 ||
		store.listConversationsArg.PageOffset != 3 {
		t.Fatalf("list arg=%+v called=%v want tenant=7 owner=42 limit=25 offset=3",
			store.listConversationsArg, store.listConversationsCalled)
	}
	if len(got) != 1 || got[0].ID != 101 || got[0].OwnerUserID != 42 {
		t.Fatalf("rows=%+v want own conversation 101", got)
	}
}

func TestGetConversationRejectsCrossOwnerAndReportsDeleted(t *testing.T) {
	tests := []struct {
		name    string
		row     dbhermes.HermesConversation
		wantErr error
	}{
		{
			name: "cross owner is not found",
			row: dbhermes.HermesConversation{
				ID: 201, TenantID: 7, OwnerUserID: 99,
				ActorSource: "token", ActorID: 42,
				CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
			},
			wantErr: ErrNotFound,
		},
		{
			name: "same principal but another administrator is not found",
			row: dbhermes.HermesConversation{
				ID: 203, TenantID: 7, OwnerUserID: 42,
				ActorSource: "token", ActorID: 99,
				CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
			},
			wantErr: ErrNotFound,
		},
		{
			name: "soft deleted is gone",
			row: dbhermes.HermesConversation{
				ID: 202, TenantID: 7, OwnerUserID: 42,
				ActorSource: "token", ActorID: 42,
				CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
				DeletedAt: pgtype.Timestamptz{Time: testPGTime().Time, Valid: true},
			},
			wantErr: ErrGone,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &hermesStoreSpy{conversationRow: tc.row}
			service := NewService(store)

			_, err := service.GetConversation(context.Background(), 7, tc.row.ID, 42, "token", 42)

			// 变异检测:去掉 owner 或 deleted_at 守卫会返回 nil 并使此断言失败。
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
		})
	}
}

func TestListMessagesByConversationRejectsCrossOwnerBeforeListing(t *testing.T) {
	// 回归守护:对他人拥有的 conversation id,消息历史不能返回空的 200;必须是不可枚举的 404。
	store := &hermesStoreSpy{conversationRow: dbhermes.HermesConversation{
		ID: 301, TenantID: 7, OwnerUserID: 99,
		ActorSource: "token", ActorID: 42,
		CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
	}}
	service := NewService(store)

	_, err := service.ListMessagesByConversation(context.Background(), 7, 301, 42, "token", 42, 50, 0)

	// 变异检测:未先校验 owner 就调用 ListMessagesByConversation 会置 listMessagesCalled 为 true 并返回 nil。
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
	if store.listMessagesCalled {
		t.Fatalf("message list was called for a foreign owner")
	}
}

func TestListMessagesByConversationPassesOwnerToStore(t *testing.T) {
	// 回归守护:SQLC 调用必须接收 owner_user_id,这样 join 才能强制校验归属权。
	store := &hermesStoreSpy{
		conversationRow: dbhermes.HermesConversation{
			ID: 302, TenantID: 7, OwnerUserID: 42,
			ActorSource: "token", ActorID: 42,
			CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
		},
		listMessagesRows: []dbhermes.ListMessagesByConversationRow{{
			ID: 401, TenantID: 7, ConversationID: 302, Role: "assistant",
			Content: []byte(`{"type":"text","text":"hello"}`), CreatedAt: testPGTime(),
		}},
	}
	service := NewService(store)

	got, err := service.ListMessagesByConversation(context.Background(), 7, 302, 42, "token", 42, 20, 4)
	if err != nil {
		t.Fatalf("ListMessagesByConversation: %v", err)
	}

	// 变异检测:即便 tenant/conversation 仍匹配,从参数中丢掉 OwnerUserID 也会使此断言失败。
	if !store.listMessagesCalled ||
		store.listMessagesArg.TenantID != 7 ||
		store.listMessagesArg.ConversationID != 302 ||
		store.listMessagesArg.OwnerUserID != 42 ||
		store.listMessagesArg.ActorSource != "token" ||
		store.listMessagesArg.ActorID != 42 ||
		store.listMessagesArg.PageLimit != 20 ||
		store.listMessagesArg.PageOffset != 4 {
		t.Fatalf("list messages arg=%+v called=%v want tenant=7 conv=302 owner=42 limit=20 offset=4",
			store.listMessagesArg, store.listMessagesCalled)
	}
	if len(got) != 1 || got[0].ID != 401 || string(got[0].Content) != `{"type":"text","text":"hello"}` {
		t.Fatalf("messages=%+v want persisted assistant content", got)
	}
}

func TestListMessagesByConversationDecryptsCiphertextAndKeepsLegacyPlaintext(t *testing.T) {
	// 回归守护:新的 Hermes 记录必须从加密内容读取,而 0091 之前的明文记录在保留期清理掉之前仍保持可读。
	keys := mustHermesContentKeys(t)
	encryptedPlain := []byte(`{"type":"text","text":"HERMES_READ_SENTINEL_from_ciphertext"}`)
	ciphertext, err := EncodeMessageContent(context.Background(), keys, 7, 302, encryptedPlain)
	if err != nil {
		t.Fatalf("EncodeMessageContent: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("HERMES_READ_SENTINEL")) {
		t.Fatalf("ciphertext contains plaintext sentinel: %q", ciphertext)
	}
	store := &hermesStoreSpy{
		conversationRow: dbhermes.HermesConversation{
			ID: 302, TenantID: 7, OwnerUserID: 42,
			ActorSource: "token", ActorID: 42,
			CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
		},
		listMessagesRows: []dbhermes.ListMessagesByConversationRow{
			{
				ID: 401, TenantID: 7, ConversationID: 302, Role: "assistant",
				Content: []byte(EncryptedMessageContentPlaceholder), ContentCiphertext: ciphertext, CreatedAt: testPGTime(),
			},
			{
				ID: 402, TenantID: 7, ConversationID: 302, Role: "assistant",
				Content: []byte(`{"type":"text","text":"legacy plaintext still readable"}`), CreatedAt: testPGTime(),
			},
		},
	}
	service := NewService(store).WithMessageContentKeys(keys)

	got, err := service.ListMessagesByConversation(context.Background(), 7, 302, 42, "token", 42, 20, 0)
	if err != nil {
		t.Fatalf("ListMessagesByConversation: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("messages=%d want encrypted plus legacy", len(got))
	}
	if string(got[0].Content) != string(encryptedPlain) {
		t.Fatalf("encrypted row content=%s want decrypted plaintext JSON", string(got[0].Content))
	}
	if string(got[1].Content) != `{"type":"text","text":"legacy plaintext still readable"}` {
		t.Fatalf("legacy row content=%s", string(got[1].Content))
	}
	// 变异检测:若 messageFromRow 忽略 content_ciphertext,row 401 会返回占位符而非 encryptedPlain。
	if string(got[0].Content) == EncryptedMessageContentPlaceholder {
		t.Fatalf("encrypted row leaked placeholder instead of decrypted content")
	}
}

func TestSoftDeleteConversationWithAuditIsAtomicAndIdempotent(t *testing.T) {
	tests := []struct {
		name    string
		deleted bool
	}{
		{name: "active conversation"},
		{name: "already deleted conversation", deleted: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := dbhermes.HermesConversation{
				ID: 501, TenantID: 7, OwnerUserID: 42,
				ActorSource: "token", ActorID: 42,
				CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
			}
			if tc.deleted {
				row.DeletedAt = pgtype.Timestamptz{Time: testPGTime().Time, Valid: true}
			}
			store := &hermesStoreSpy{conversationRow: row, softDeleteRows: 1}
			tx := &conversationTxSpy{store: store}
			service := &Service{store: store, tx: tx}

			err := service.SoftDeleteConversationWithAudit(context.Background(), 7, 42, 501, "token", 42, AuditFields{
				ActorSource: "token", ActorID: 42, ActorRole: "platform_admin",
				Action: ActionConversationDelete,
				SanitizedArgs: map[string]any{
					"conversation_id": int64(501),
					"token":           "must-redact",
				},
				CorrelationID: "corr-delete",
				RequestID:     "req-delete",
			})
			if err != nil {
				t.Fatalf("SoftDeleteConversationWithAudit: %v", err)
			}

			// 变异检测:把审计移出 withTx,或在第二次删除时跳过它,都会破坏 tx/audit 断言。
			if tx.calls != 1 || !tx.committed {
				t.Fatalf("tx calls=%d committed=%v want one committed transaction", tx.calls, tx.committed)
			}
			if !store.auditCalled || store.auditArg.Action != ActionConversationDelete ||
				store.auditArg.TenantID != 7 || store.auditArg.ActorSource != "token" || store.auditArg.ActorID != 42 {
				t.Fatalf("audit=%+v called=%v want conversation delete success", store.auditArg, store.auditCalled)
			}
			var args map[string]any
			if err := json.Unmarshal(store.auditArg.SanitizedArgs, &args); err != nil {
				t.Fatalf("audit args json: %v", err)
			}
			if args["conversation_id"] != float64(501) || args["token"] != "[REDACTED]" {
				t.Fatalf("audit args=%v want conversation id and redacted token", args)
			}
		})
	}
}

func TestSoftDeleteConversationWithAuditRejectsCrossOwnerBeforeDelete(t *testing.T) {
	store := &hermesStoreSpy{
		conversationRow: dbhermes.HermesConversation{
			ID: 601, TenantID: 7, OwnerUserID: 99,
			ActorSource: "token", ActorID: 42,
			CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
		},
		softDeleteRows: 1,
	}
	tx := &conversationTxSpy{store: store}
	service := &Service{store: store, tx: tx}

	err := service.SoftDeleteConversationWithAudit(context.Background(), 7, 42, 601, "token", 42, AuditFields{
		ActorSource: "token", ActorID: 42, ActorRole: "platform_admin", Action: ActionConversationDelete,
	})

	// 变异检测:在 owner 检查之前就删除,会对他人拥有的记录置 softDeleteCalled 和 auditCalled 为 true。
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
	if store.softDeleteCalled || store.auditCalled {
		t.Fatalf("foreign delete touched delete=%v audit=%v", store.softDeleteCalled, store.auditCalled)
	}
	if tx.committed {
		t.Fatalf("foreign delete committed transaction")
	}
}

func TestActionConversationDeleteIsValidAuditAction(t *testing.T) {
	// 回归守护:DB CHECK 迁移和本地白名单都必须接受会话删除审计记录。
	store := &hermesStoreSpy{}
	service := NewService(store)

	err := service.RecordAudit(context.Background(), AuditFields{
		TenantID: 7, ActorSource: "token", ActorID: 42, ActorRole: "platform_admin",
		Action: ActionConversationDelete, SanitizedArgs: map[string]any{"conversation_id": int64(123)},
		Result: AuditResultSuccess, CorrelationID: "corr-action", RequestID: "req-action",
	})
	if err != nil {
		t.Fatalf("RecordAudit(ActionConversationDelete): %v", err)
	}
	if store.auditArg.Action != ActionConversationDelete {
		t.Fatalf("audit action=%q want %q", store.auditArg.Action, ActionConversationDelete)
	}
}

type conversationTxSpy struct {
	store     Store
	calls     int
	committed bool
}

func (tx *conversationTxSpy) withTx(ctx context.Context, fn func(Store) error) error {
	tx.calls++
	err := fn(tx.store)
	if err == nil {
		tx.committed = true
	}
	return err
}

func stringPtrForTest(value string) *string {
	return &value
}

func mustHermesContentKeys(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("hermes-test", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	return keys
}
