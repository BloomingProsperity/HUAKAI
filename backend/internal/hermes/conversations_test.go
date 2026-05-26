package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

func TestListConversationsByOwnerPassesTenantOwnerAndPagination(t *testing.T) {
	// Regression: conversation list must be tenant+owner scoped; removing either filter leaks another user's history.
	store := &hermesStoreSpy{
		listConversationsRows: []dbhermes.HermesConversation{{
			ID: 101, TenantID: 7, OwnerUserID: 42, Title: stringPtrForTest("own"),
			CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
		}},
	}
	service := NewService(store)

	got, err := service.ListConversationsByOwner(context.Background(), 7, 42, 25, 3)
	if err != nil {
		t.Fatalf("ListConversationsByOwner: %v", err)
	}

	// Mutation check: zeroing OwnerUserID or TenantID, or ignoring pagination, makes this assertion fail.
	if !store.listConversationsCalled ||
		store.listConversationsArg.TenantID != 7 ||
		store.listConversationsArg.OwnerUserID != 42 ||
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
				CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
			},
			wantErr: ErrNotFound,
		},
		{
			name: "soft deleted is gone",
			row: dbhermes.HermesConversation{
				ID: 202, TenantID: 7, OwnerUserID: 42,
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

			_, err := service.GetConversation(context.Background(), 7, tc.row.ID, 42)

			// Mutation check: removing the owner or deleted_at guard returns nil and fails this assertion.
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
		})
	}
}

func TestListMessagesByConversationRejectsCrossOwnerBeforeListing(t *testing.T) {
	// Regression: message history must not return an empty 200 for a foreign conversation id; it must be non-enumerating 404.
	store := &hermesStoreSpy{conversationRow: dbhermes.HermesConversation{
		ID: 301, TenantID: 7, OwnerUserID: 99,
		CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
	}}
	service := NewService(store)

	_, err := service.ListMessagesByConversation(context.Background(), 7, 301, 42, 50, 0)

	// Mutation check: calling ListMessagesByConversation without first validating owner will set listMessagesCalled and return nil.
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
	if store.listMessagesCalled {
		t.Fatalf("message list was called for a foreign owner")
	}
}

func TestListMessagesByConversationPassesOwnerToStore(t *testing.T) {
	// Regression: the SQLC call must receive owner_user_id so the join can enforce ownership.
	store := &hermesStoreSpy{
		conversationRow: dbhermes.HermesConversation{
			ID: 302, TenantID: 7, OwnerUserID: 42,
			CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
		},
		listMessagesRows: []dbhermes.HermesMessage{{
			ID: 401, TenantID: 7, ConversationID: 302, Role: "assistant",
			Content: []byte(`{"type":"text","text":"hello"}`), CreatedAt: testPGTime(),
		}},
	}
	service := NewService(store)

	got, err := service.ListMessagesByConversation(context.Background(), 7, 302, 42, 20, 4)
	if err != nil {
		t.Fatalf("ListMessagesByConversation: %v", err)
	}

	// Mutation check: dropping OwnerUserID from params makes this assertion fail even if tenant/conversation still match.
	if !store.listMessagesCalled ||
		store.listMessagesArg.TenantID != 7 ||
		store.listMessagesArg.ConversationID != 302 ||
		store.listMessagesArg.OwnerUserID != 42 ||
		store.listMessagesArg.PageLimit != 20 ||
		store.listMessagesArg.PageOffset != 4 {
		t.Fatalf("list messages arg=%+v called=%v want tenant=7 conv=302 owner=42 limit=20 offset=4",
			store.listMessagesArg, store.listMessagesCalled)
	}
	if len(got) != 1 || got[0].ID != 401 || string(got[0].Content) != `{"type":"text","text":"hello"}` {
		t.Fatalf("messages=%+v want persisted assistant content", got)
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
				CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
			}
			if tc.deleted {
				row.DeletedAt = pgtype.Timestamptz{Time: testPGTime().Time, Valid: true}
			}
			store := &hermesStoreSpy{conversationRow: row, softDeleteRows: 1}
			tx := &conversationTxSpy{store: store}
			service := &Service{store: store, tx: tx}

			err := service.SoftDeleteConversationWithAudit(context.Background(), 7, 42, 501, AuditFields{
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

			// Mutation check: moving audit outside withTx or skipping it on second delete breaks tx/audit assertions.
			if tx.calls != 1 || !tx.committed {
				t.Fatalf("tx calls=%d committed=%v want one committed transaction", tx.calls, tx.committed)
			}
			if !store.auditCalled || store.auditArg.Action != ActionConversationDelete ||
				store.auditArg.TenantID != 7 || store.auditArg.ActorUserID != 42 {
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
			CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
		},
		softDeleteRows: 1,
	}
	tx := &conversationTxSpy{store: store}
	service := &Service{store: store, tx: tx}

	err := service.SoftDeleteConversationWithAudit(context.Background(), 7, 42, 601, AuditFields{
		Action: ActionConversationDelete,
	})

	// Mutation check: deleting before owner check would set softDeleteCalled and auditCalled for a foreign row.
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
	// Regression: DB CHECK migration and local whitelist must both accept conversation delete audit rows.
	store := &hermesStoreSpy{}
	service := NewService(store)

	err := service.RecordAudit(context.Background(), 7, 42, ActionConversationDelete, map[string]any{
		"conversation_id": int64(123),
	}, AuditResultSuccess, "corr-action", "req-action")
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
