package hermeschat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

func TestBridgeDoneEventTriggersPersist(t *testing.T) {
	// Regression: the gateway must not forward done until it has parsed token SSE and persisted the assistant message.
	store := newBridgeStore()
	store.nextConversationID = 1001
	bridge := mustBridge(t, store)
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-done",
		Body: []byte(`{"messages":[{"role":"user","content":"hi"}],"model":"gpt-test"}`),
	})
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}
	// Mutation check: if the happy path stops creating a conversation after validation, this fixture loses the gateway event.
	if !store.createdConversation || len(store.conversations) != 1 {
		t.Fatalf("created=%v conversations=%d want one conversation for valid new chat", store.createdConversation, len(store.conversations))
	}
	var runnerBody map[string]any
	if err := json.Unmarshal(prepared.Body, &runnerBody); err != nil {
		t.Fatalf("runner body json: %v", err)
	}
	if runnerBody["conversation_id"] != float64(1001) || runnerBody["internal_base_url"] != testInternalBaseURL {
		t.Fatalf("runner body conversation/base=%v/%v want 1001/%s", runnerBody["conversation_id"], runnerBody["internal_base_url"], testInternalBaseURL)
	}
	token, _ := runnerBody["internal_token"].(string)
	if token == "" {
		t.Fatalf("internal_token missing from runner body: %+v", runnerBody)
	}
	claims, err := VerifyInternalToken(token, []byte(testInternalSecret), time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatalf("VerifyInternalToken: %v", err)
	}
	if claims.TenantID != 7 || claims.UserID != 42 || claims.RequestID != "req-done" {
		t.Fatalf("internal token claims=%+v want tenant 7 user 42 request req-done", claims)
	}

	rec := httptest.NewRecorder()
	err = bridge.Stream(context.Background(), rec, sseResponse(
		"event: token\n"+
			"data: {\"delta\":\"hel\"}\n\n"+
			"event: token\n"+
			"data: {\"delta\":\"lo\"}\n\n"+
			"event: done\n"+
			"data: {\"finish_reason\":\"stop\",\"total_tokens\":12}\n\n",
	), prepared)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	got := rec.Body.String()
	if !strings.HasPrefix(got, "event: conversation\ndata: {\"id\":1001}\n\n") {
		t.Fatalf("first event=%q want gateway conversation event before runner tokens", got)
	}
	if !strings.Contains(got, "event: done") {
		t.Fatalf("body=%q missing done after successful persist", got)
	}
	if len(store.appended) != 1 {
		t.Fatalf("messages persisted=%d want 1", len(store.appended))
	}
	msg := store.appended[0]
	if msg.TenantID != 7 || msg.ConversationID != 1001 || msg.Role != "assistant" {
		t.Fatalf("message tenant/conversation/role=%d/%d/%s want 7/1001/assistant", msg.TenantID, msg.ConversationID, msg.Role)
	}
	var content map[string]string
	if err := json.Unmarshal(msg.Content, &content); err != nil {
		t.Fatalf("message content json: %v", err)
	}
	if content["text"] != "hello" {
		t.Fatalf("content text=%q want accumulated token text hello", content["text"])
	}
	if msg.TokenCount == nil || *msg.TokenCount != 12 {
		t.Fatalf("token_count=%v want 12", msg.TokenCount)
	}
	if store.touchedConversationID != 1001 || store.auditArg.Action != hermes.ActionMessageSend {
		t.Fatalf("touch/audit conversation=%d action=%q want 1001/%s", store.touchedConversationID, store.auditArg.Action, hermes.ActionMessageSend)
	}
	var auditArgs map[string]any
	if err := json.Unmarshal(store.auditArg.SanitizedArgs, &auditArgs); err != nil {
		t.Fatalf("audit args json: %v", err)
	}
	if auditArgs["conversation_id"] != float64(1001) || auditArgs["message_role"] != "assistant" {
		t.Fatalf("audit args=%v want conversation_id and message_role only", auditArgs)
	}
	if _, leaked := auditArgs["content"]; leaked {
		t.Fatalf("audit args leaked message content: %v", auditArgs)
	}
}

func TestBridgePersistFailureEmitsErrorAndSuppressesDone(t *testing.T) {
	// Regression: if message persistence fails at the done fence, clients must see error instead of a false done.
	store := newBridgeStore()
	store.conversations[conversationKey{tenantID: 7, id: 55}] = dbhermes.HermesConversation{ID: 55, TenantID: 7, OwnerUserID: 42}
	store.appendPanic = true
	bridge := mustBridge(t, store)
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-persist-fail",
		Body: []byte(`{"conversation_id":55,"messages":[{"role":"user","content":"hi"}]}`),
	})
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}

	rec := httptest.NewRecorder()
	err = bridge.Stream(context.Background(), rec, sseResponse(
		"event: token\n"+
			"data: {\"delta\":\"partial\"}\n\n"+
			"event: done\n"+
			"data: {\"finish_reason\":\"stop\",\"total_tokens\":3}\n\n",
	), prepared)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	got := rec.Body.String()
	if strings.Contains(got, "event: done") {
		t.Fatalf("body=%q must not forward done after persist failure", got)
	}
	if !strings.Contains(got, "event: error") || !strings.Contains(got, "persist_failed") {
		t.Fatalf("body=%q want persist_failed error event", got)
	}
}

func TestBridgeDoesNotPersistDoneAfterConversationDelete(t *testing.T) {
	// Regression: a stream that started before DELETE must not append a post-delete message or forward done.
	store := newBridgeStore()
	key := conversationKey{tenantID: 7, id: 66}
	store.conversations[key] = dbhermes.HermesConversation{ID: 66, TenantID: 7, OwnerUserID: 42}
	bridge := mustBridge(t, store)
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-delete-race",
		Body: []byte(`{"conversation_id":66,"messages":[{"role":"user","content":"hi"}]}`),
	})
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}
	commitsBeforeDelete := store.committedTxs
	row := store.conversations[key]
	row.DeletedAt = pgtype.Timestamptz{Time: time.Unix(1700000001, 0).UTC(), Valid: true}
	store.conversations[key] = row

	rec := httptest.NewRecorder()
	err = bridge.Stream(context.Background(), rec, sseResponse(
		"event: token\n"+
			"data: {\"delta\":\"late\"}\n\n"+
			"event: done\n"+
			"data: {\"finish_reason\":\"stop\",\"total_tokens\":5}\n\n",
	), prepared)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	got := rec.Body.String()
	if strings.Contains(got, "event: done") {
		t.Fatalf("body=%q must not forward done after conversation delete", got)
	}
	if !strings.Contains(got, "event: error") || !strings.Contains(got, "persist_failed") {
		t.Fatalf("body=%q want persist_failed error event after delete", got)
	}
	// Mutation check: if AppendMessage or the SQL active-conversation guard is removed, this records a post-delete message.
	if len(store.appended) != 0 || store.touchedConversationID != 0 || store.auditArg.Action != "" {
		t.Fatalf("append/touch/audit=%d/%d/%q want no post-delete writes", len(store.appended), store.touchedConversationID, store.auditArg.Action)
	}
	if store.committedTxs != commitsBeforeDelete {
		t.Fatalf("committed txs=%d before=%d want rollback after deleted conversation", store.committedTxs, commitsBeforeDelete)
	}
}

func TestBridgePersistDone_AuditInsertFailureAbortsTransaction(t *testing.T) {
	// Regression: message persistence and primary audit must be atomic; audit failure must roll back message write.
	store := newBridgeStore()
	store.conversations[conversationKey{tenantID: 7, id: 77}] = dbhermes.HermesConversation{ID: 77, TenantID: 7, OwnerUserID: 42}
	store.auditErr = errors.New("audit insert failed")
	bridge := mustBridge(t, store)
	var err error
	prepared := PreparedRequest{
		TenantID: 7, UserID: 42, ConversationID: 77, RequestID: "req-audit-fail",
		CorrelationID: "corr-audit-fail",
	}
	err = bridge.persistDone(context.Background(), prepared, &streamState{
		assistantText: strings.Builder{},
	}, []byte(`{"total_tokens":2}`))
	if err == nil {
		t.Fatalf("persistDone should fail on audit insert error")
	}

	// Mutation check: if RunHermesTx still ignores message-audit errors, commitCount becomes 1.
	if len(store.appended) != 0 {
		t.Fatalf("persisted messages=%d want 0", len(store.appended))
	}
	if store.commitCount != 0 {
		t.Fatalf("commit count=%d want 0", store.commitCount)
	}
	if store.rollbackCount != 1 {
		t.Fatalf("rollback count=%d want 1", store.rollbackCount)
	}
}

func TestBridgePersistDone_AuditInsertSuccessCommitsTransaction(t *testing.T) {
	// Regression: normal path persists message and audit in one transaction and commits once.
	store := newBridgeStore()
	store.conversations[conversationKey{tenantID: 7, id: 77}] = dbhermes.HermesConversation{ID: 77, TenantID: 7, OwnerUserID: 42}
	bridge := mustBridge(t, store)
	var err error
	prepared := PreparedRequest{
		TenantID: 7, UserID: 42, ConversationID: 77, RequestID: "req-audit-ok",
		CorrelationID: "corr-audit-ok",
	}
	err = bridge.persistDone(context.Background(), prepared, &streamState{
		assistantText: strings.Builder{},
	}, []byte(`{"total_tokens":2}`))
	if err != nil {
		t.Fatalf("persistDone: %v", err)
	}

	if len(store.appended) != 1 {
		t.Fatalf("persisted messages=%d want 1", len(store.appended))
	}
	if store.commitCount != 1 {
		t.Fatalf("commit count=%d want 1", store.commitCount)
	}
	if store.rollbackCount != 0 {
		t.Fatalf("rollback count=%d want 0", store.rollbackCount)
	}
	if store.auditWrites != 1 {
		t.Fatalf("audit writes=%d want 1", store.auditWrites)
	}
}

func TestBridgeRejectsRunnerConversationRetarget(t *testing.T) {
	// Regression guarded: runner-supplied conversation ids must not retarget persisted messages across users.
	store := newBridgeStore()
	store.conversations[conversationKey{tenantID: 7, id: 77}] = dbhermes.HermesConversation{ID: 77, TenantID: 7, OwnerUserID: 42}
	store.conversations[conversationKey{tenantID: 7, id: 88}] = dbhermes.HermesConversation{ID: 88, TenantID: 7, OwnerUserID: 99}
	bridge := mustBridge(t, store)
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-retarget",
		Body: []byte(`{"conversation_id":77,"messages":[{"role":"user","content":"hi"}]}`),
	})
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}

	rec := httptest.NewRecorder()
	err = bridge.Stream(context.Background(), rec, sseResponse(
		"event: conversation\n"+
			"data: {\"id\":88}\n\n"+
			"event: token\n"+
			"data: {\"delta\":\"safe\"}\n\n"+
			"event: done\n"+
			"data: {\"finish_reason\":\"stop\",\"total_tokens\":4}\n\n",
	), prepared)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	got := rec.Body.String()
	if strings.Contains(got, "event: done") {
		t.Fatalf("body=%q must not forward done after runner retarget", got)
	}
	if !strings.Contains(got, "event: error") || !strings.Contains(got, "conversation_mismatch") {
		t.Fatalf("body=%q want conversation_mismatch error", got)
	}
	if len(store.appended) != 0 || store.touchedConversationID != 0 {
		t.Fatalf("persisted=%d touched=%d want no persistence after runner retarget", len(store.appended), store.touchedConversationID)
	}
}

func TestBridgeSuppressesRunnerConversationDuplicate(t *testing.T) {
	// Regression: gateway already emits the prepared conversation id, so the runner echo must not reach clients.
	store := newBridgeStore()
	store.nextConversationID = 1001
	bridge := mustBridge(t, store)
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-conversation-echo",
		Body: []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	})
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}

	rec := httptest.NewRecorder()
	err = bridge.Stream(context.Background(), rec, sseResponse(
		"event: conversation\n"+
			"data: {\"id\":1001}\n\n"+
			"event: token\n"+
			"data: {\"delta\":\"ok\"}\n\n"+
			"event: done\n"+
			"data: {\"finish_reason\":\"stop\",\"total_tokens\":2}\n\n",
	), prepared)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	got := rec.Body.String()
	if strings.Count(got, "event: conversation") != 1 {
		t.Fatalf("body=%q want only gateway conversation event", got)
	}
	if !strings.Contains(got, "event: token") || !strings.Contains(got, "event: done") {
		t.Fatalf("body=%q want token and done after suppressing duplicate conversation event", got)
	}
	if len(store.appended) != 1 || store.appended[0].ConversationID != 1001 {
		t.Fatalf("persisted=%+v want one assistant message on conversation 1001", store.appended)
	}
}

func TestBridgeUsesTenantScopedConversationOwner(t *testing.T) {
	// Regression: conversation ownership must be checked by tenant id, or same numeric ids can cross tenants.
	store := newBridgeStore()
	store.conversations[conversationKey{tenantID: 7, id: 501}] = dbhermes.HermesConversation{ID: 501, TenantID: 7, OwnerUserID: 42}
	store.conversations[conversationKey{tenantID: 8, id: 501}] = dbhermes.HermesConversation{ID: 501, TenantID: 8, OwnerUserID: 99}
	bridge := mustBridge(t, store)

	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-tenant-scope",
		Body: []byte(`{"conversation_id":501,"messages":[{"role":"user","content":"hi"}]}`),
	})
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}

	if prepared.ConversationID != 501 || store.createdConversation {
		t.Fatalf("conversation=%d created=%v want existing 501", prepared.ConversationID, store.createdConversation)
	}
	if store.getConversationArg.TenantID != 7 || store.getConversationArg.ID != 501 {
		t.Fatalf("GetConversation arg tenant/id=%d/%d want 7/501", store.getConversationArg.TenantID, store.getConversationArg.ID)
	}
}

func TestBridgePrepareRequestConversationIDContract(t *testing.T) {
	// Regression: conversation_id=0 is a valid new-chat sentinel; only negative ids are bad input.
	cases := []struct {
		name      string
		body      string
		wantID    int64
		wantError bool
	}{
		{
			name:   "explicit zero starts new chat",
			body:   `{"conversation_id":0,"messages":[{"role":"user","content":"hi"}]}`,
			wantID: 701,
		},
		{
			name:   "omitted starts new chat",
			body:   `{"messages":[{"role":"user","content":"hi"}]}`,
			wantID: 702,
		},
		{
			name:      "negative rejects",
			body:      `{"conversation_id":-1,"messages":[{"role":"user","content":"hi"}]}`,
			wantError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newBridgeStore()
			store.nextConversationID = tc.wantID
			bridge := mustBridge(t, store)

			prepared, err := bridge.PrepareRequest(context.Background(), Request{
				TenantID: 7, UserID: 42, RequestID: "req-conversation-contract", Body: []byte(tc.body),
			})
			if tc.wantError {
				if !errors.Is(err, hermes.ErrInvalidInput) {
					t.Fatalf("PrepareRequest error=%v want ErrInvalidInput", err)
				}
				if store.createdConversation || len(store.conversations) != 0 {
					t.Fatalf("created=%v conversations=%d want no row for negative conversation_id", store.createdConversation, len(store.conversations))
				}
				return
			}
			if err != nil {
				t.Fatalf("PrepareRequest: %v", err)
			}
			if !prepared.CreatedConversation || prepared.ConversationID != tc.wantID {
				t.Fatalf("created=%v conversation=%d want new conversation %d", prepared.CreatedConversation, prepared.ConversationID, tc.wantID)
			}
			if !store.createdConversation || len(store.conversations) != 1 {
				t.Fatalf("created=%v conversations=%d want exactly one new row", store.createdConversation, len(store.conversations))
			}
			if _, ok := store.conversations[conversationKey{tenantID: 7, id: tc.wantID}]; !ok {
				t.Fatalf("conversation row %d not created in tenant 7", tc.wantID)
			}
			var runnerBody map[string]any
			if err := json.Unmarshal(prepared.Body, &runnerBody); err != nil {
				t.Fatalf("runner body json: %v", err)
			}
			if runnerBody["conversation_id"] != float64(tc.wantID) {
				t.Fatalf("runner conversation_id=%v want %d", runnerBody["conversation_id"], tc.wantID)
			}

			rec := httptest.NewRecorder()
			err = bridge.Stream(context.Background(), rec, sseResponse(
				"event: token\n"+
					"data: {\"delta\":\"ok\"}\n\n"+
					"event: done\n"+
					"data: {\"finish_reason\":\"stop\",\"total_tokens\":2}\n\n",
			), prepared)
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d want 200", rec.Code)
			}
			if !strings.HasPrefix(rec.Body.String(), fmt.Sprintf("event: conversation\ndata: {\"id\":%d}\n\n", tc.wantID)) {
				t.Fatalf("body=%q want gateway conversation event for new chat %d", rec.Body.String(), tc.wantID)
			}
			if len(store.appended) != 1 || store.appended[0].ConversationID != tc.wantID {
				t.Fatalf("persisted=%+v want one assistant message on conversation %d", store.appended, tc.wantID)
			}
		})
	}
}

func TestBridgePrepareRequestRejectsInvalidChatPayloadBeforeCreate(t *testing.T) {
	// Regression guarded: invalid chat inputs must be rejected before CreateConversation, or user history gets polluted.
	cases := []struct {
		name      string
		requestID string
		body      string
	}{
		{
			name:      "empty messages",
			requestID: "req-empty-messages",
			body:      `{"messages":[]}`,
		},
		{
			name:      "negative conversation id",
			requestID: "req-negative-conversation",
			body:      `{"conversation_id":-1,"messages":[{"role":"user","content":"hi"}]}`,
		},
		{
			name:      "pipe in request id",
			requestID: "abc|def",
			body:      `{"messages":[{"role":"user","content":"hi"}]}`,
		},
		{
			name:      "missing role",
			requestID: "req-missing-role",
			body:      `{"messages":[{"content":"hi"}]}`,
		},
		{
			name:      "blank content",
			requestID: "req-blank-content",
			body:      `{"messages":[{"role":"user","content":"   "}]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newBridgeStore()
			bridge := mustBridge(t, store)

			_, err := bridge.PrepareRequest(context.Background(), Request{
				TenantID: 7, UserID: 42, RequestID: tc.requestID, Body: []byte(tc.body),
			})
			if !errors.Is(err, hermes.ErrInvalidInput) {
				t.Fatalf("PrepareRequest error=%v want ErrInvalidInput", err)
			}
			// Mutation check: move validation after CreateConversation or treat negative conversation_id as absent, and this row-count assertion fails.
			if store.createdConversation || len(store.conversations) != 0 {
				t.Fatalf("created=%v conversations=%d want no row for invalid payload", store.createdConversation, len(store.conversations))
			}
		})
	}
}

func TestInternalTokenSignedAndVerifiesSecret(t *testing.T) {
	// Regression: the runner-facing body must carry a signed internal token, not a bearer string that any secret accepts.
	now := time.Unix(1700000000, 0).UTC()
	token, err := SignInternalToken([]byte(testInternalSecret), InternalTokenClaims{
		TenantID:  7,
		UserID:    42,
		RequestID: "req-token",
		ExpiresAt: now.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("SignInternalToken: %v", err)
	}

	claims, err := VerifyInternalToken(token, []byte(testInternalSecret), now)
	if err != nil {
		t.Fatalf("VerifyInternalToken: %v", err)
	}
	if claims.TenantID != 7 || claims.UserID != 42 || claims.RequestID != "req-token" {
		t.Fatalf("claims=%+v want tenant/user/request", claims)
	}
	if _, err := VerifyInternalToken(token, []byte("wrong-secret-32-bytes-minimum-value"), now); err == nil {
		t.Fatalf("VerifyInternalToken with wrong secret succeeded")
	}
}

func TestActionMessageSendIsValidAuditAction(t *testing.T) {
	// Regression: hermes.message.send must be accepted by the local whitelist after the Slice 2.0 DB CHECK migration.
	store := newBridgeStore()
	svc := hermes.NewService(store)

	err := svc.RecordAudit(context.Background(), 7, 42, hermes.ActionMessageSend, map[string]any{
		"conversation_id": int64(123),
		"message_role":    "assistant",
	}, hermes.AuditResultSuccess, "corr-action", "req-action")
	if err != nil {
		t.Fatalf("RecordAudit(ActionMessageSend): %v", err)
	}
	if store.auditArg.Action != hermes.ActionMessageSend {
		t.Fatalf("audit action=%q want %q", store.auditArg.Action, hermes.ActionMessageSend)
	}
}

const (
	testInternalSecret  = "test-internal-secret-32-byte-value"
	testInternalBaseURL = "http://gateway.internal/internal/v1/openai"
)

func mustBridge(t *testing.T, store *bridgeStore) *Bridge {
	t.Helper()
	return mustBridgeWithOptions(t, store)
}

func mustBridgeWithOptions(t *testing.T, store *bridgeStore, opts ...Option) *Bridge {
	t.Helper()
	allOpts := []Option{
		WithInternalTokenSecret([]byte(testInternalSecret)),
		WithInternalBaseURL(testInternalBaseURL),
		WithClock(func() time.Time { return time.Unix(1700000000, 0).UTC() }),
	}
	allOpts = append(allOpts, opts...)
	bridge, err := NewBridge(store, allOpts...)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	return bridge
}

func sseResponse(body string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "text/event-stream")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestBridgeDoesNotForwardRunnerFramingHeaders(t *testing.T) {
	// Regression: gateway rewrites SSE bytes, so runner framing headers would make net/http truncate or reject the body.
	store := newBridgeStore()
	store.conversations[conversationKey{tenantID: 7, id: 77}] = dbhermes.HermesConversation{ID: 77, TenantID: 7, OwnerUserID: 42}
	bridge := mustBridge(t, store)
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-framing",
		Body: []byte(`{"conversation_id":77,"messages":[{"role":"user","content":"hi"}]}`),
	})
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}
	resp := sseResponse(
		"event: token\n" +
			"data: {\"delta\":\"ok\"}\n\n" +
			"event: done\n" +
			"data: {\"finish_reason\":\"stop\",\"total_tokens\":2}\n\n",
	)
	resp.Header.Set("Content-Length", "17")
	resp.Header.Set("Transfer-Encoding", "chunked")
	resp.Header.Set("X-Runner-Trace", "keep")

	rec := httptest.NewRecorder()
	if err := bridge.Stream(context.Background(), rec, resp, prepared); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length=%q want omitted", got)
	}
	if got := rec.Header().Get("Transfer-Encoding"); got != "" {
		t.Fatalf("Transfer-Encoding=%q want omitted", got)
	}
	if got := rec.Header().Get("X-Runner-Trace"); got != "keep" {
		t.Fatalf("X-Runner-Trace=%q want keep", got)
	}
}

type conversationKey struct {
	tenantID int64
	id       int64
}

type bridgeStore struct {
	conversations      map[conversationKey]dbhermes.HermesConversation
	nextConversationID int64

	createdConversation bool
	getConversationArg  dbhermes.GetConversationParams

	appendErr   error
	appendPanic bool
	appended    []dbhermes.AppendMessageParams

	stagedAppended        []dbhermes.AppendMessageParams
	touchedConversationID int64
	stagedTouchedID       int64
	stagedTouched         bool
	auditArg              dbhermes.InsertAuditEventParams
	stagedAuditArg        dbhermes.InsertAuditEventParams
	stagedAuditWrites     int
	auditWrites           int
	auditErr              error
	auditPanic            bool
	txAborted             bool
	inTransaction         bool
	committedTxs          int
	commitCount           int
	rollbackCount         int
}

func newBridgeStore() *bridgeStore {
	return &bridgeStore{conversations: make(map[conversationKey]dbhermes.HermesConversation)}
}

func (s *bridgeStore) RunHermesTx(ctx context.Context, fn func(hermes.Store) error) error {
	s.stagedAppended = nil
	s.stagedTouched = false
	s.stagedTouchedID = 0
	s.stagedAuditWrites = 0
	s.stagedAuditArg = dbhermes.InsertAuditEventParams{}
	s.inTransaction = true
	defer func() {
		s.inTransaction = false
	}()

	s.txAborted = false
	if err := fn(s); err != nil {
		s.rollbackCount++
		s.stagedAppended = nil
		s.stagedTouched = false
		s.stagedTouchedID = 0
		s.stagedAuditWrites = 0
		s.stagedAuditArg = dbhermes.InsertAuditEventParams{}
		return err
	}
	if s.txAborted {
		s.rollbackCount++
		s.stagedAppended = nil
		s.stagedTouched = false
		s.stagedTouchedID = 0
		s.stagedAuditWrites = 0
		s.stagedAuditArg = dbhermes.InsertAuditEventParams{}
		return fmt.Errorf("commit hermes tx: current transaction is aborted")
	}
	if len(s.stagedAppended) > 0 {
		s.appended = append(s.appended, s.stagedAppended...)
	}
	if s.stagedTouched {
		s.touchedConversationID = s.stagedTouchedID
	}
	if s.stagedAuditWrites > 0 {
		s.auditWrites += s.stagedAuditWrites
		s.auditArg = s.stagedAuditArg
	}
	s.committedTxs++
	s.commitCount++
	s.stagedAppended = nil
	s.stagedTouched = false
	s.stagedTouchedID = 0
	s.stagedAuditWrites = 0
	s.stagedAuditArg = dbhermes.InsertAuditEventParams{}
	return nil
}

func (s *bridgeStore) AppendMessage(_ context.Context, arg dbhermes.AppendMessageParams) (int64, error) {
	if s.appendPanic {
		panic("append message panic")
	}
	if s.appendErr != nil {
		return 0, s.appendErr
	}
	row, ok := s.conversations[conversationKey{tenantID: arg.TenantID, id: arg.ConversationID}]
	if !ok || row.DeletedAt.Valid {
		return 0, pgx.ErrNoRows
	}
	if !s.inTransaction {
		s.appended = append(s.appended, arg)
		return int64(len(s.appended)), nil
	}
	s.stagedAppended = append(s.stagedAppended, arg)
	return int64(len(s.stagedAppended)), nil
}

func (s *bridgeStore) CreateConversation(_ context.Context, arg dbhermes.CreateConversationParams) (int64, error) {
	s.createdConversation = true
	id := s.nextConversationID
	if id == 0 {
		id = 9001
	}
	s.conversations[conversationKey{tenantID: arg.TenantID, id: id}] = dbhermes.HermesConversation{
		ID: id, TenantID: arg.TenantID, OwnerUserID: arg.OwnerUserID,
	}
	return id, nil
}

func (s *bridgeStore) GetConversation(_ context.Context, arg dbhermes.GetConversationParams) (dbhermes.HermesConversation, error) {
	s.getConversationArg = arg
	row, ok := s.conversations[conversationKey{tenantID: arg.TenantID, id: arg.ID}]
	if !ok {
		return dbhermes.HermesConversation{}, pgx.ErrNoRows
	}
	return row, nil
}

func (s *bridgeStore) ListConversationsByOwner(context.Context, dbhermes.ListConversationsByOwnerParams) ([]dbhermes.HermesConversation, error) {
	return nil, nil
}

func (s *bridgeStore) ListMessagesByConversation(context.Context, dbhermes.ListMessagesByConversationParams) ([]dbhermes.HermesMessage, error) {
	return nil, nil
}

func (s *bridgeStore) UpdateConversationLastMessageAt(_ context.Context, arg dbhermes.UpdateConversationLastMessageAtParams) (int64, error) {
	row, ok := s.conversations[conversationKey{tenantID: arg.TenantID, id: arg.ID}]
	if !ok || row.DeletedAt.Valid {
		return 0, nil
	}
	if !s.inTransaction {
		s.touchedConversationID = arg.ID
		return 1, nil
	}
	s.stagedTouched = true
	s.stagedTouchedID = arg.ID
	return 1, nil
}

func (s *bridgeStore) InsertAuditEvent(_ context.Context, arg dbhermes.InsertAuditEventParams) (dbhermes.HermesAuditEvent, error) {
	if !s.inTransaction {
		s.auditWrites++
		s.auditArg = arg
		if s.auditErr != nil {
			if s.auditPanic {
				panic("audit panic")
			}
			return dbhermes.HermesAuditEvent{}, s.auditErr
		}
		if s.auditPanic {
			panic("audit panic")
		}
		return dbhermes.HermesAuditEvent{
			ID: 1, Ts: arg.Ts, TenantID: arg.TenantID, ActorUserID: arg.ActorUserID,
			Action: arg.Action, SanitizedArgs: arg.SanitizedArgs, Result: arg.Result,
			CorrelationID: arg.CorrelationID, RequestID: arg.RequestID,
		}, nil
	}

	s.stagedAuditWrites++
	s.stagedAuditArg = arg
	if s.auditErr != nil {
		if s.auditPanic {
			panic("audit panic")
		}
		return dbhermes.HermesAuditEvent{}, s.auditErr
	}
	if s.auditPanic {
		panic("audit panic")
	}
	return dbhermes.HermesAuditEvent{
		ID: 1, Ts: arg.Ts, TenantID: arg.TenantID, ActorUserID: arg.ActorUserID,
		Action: arg.Action, SanitizedArgs: arg.SanitizedArgs, Result: arg.Result,
		CorrelationID: arg.CorrelationID, RequestID: arg.RequestID,
	}, nil
}

func (s *bridgeStore) Exec(_ context.Context, sql string, _ ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected sql: %s", sql)
}

func (s *bridgeStore) CreateProfile(context.Context, dbhermes.CreateProfileParams) (dbhermes.HermesApiProfile, error) {
	return dbhermes.HermesApiProfile{}, nil
}

func (s *bridgeStore) DeleteProfile(context.Context, dbhermes.DeleteProfileParams) (int64, error) {
	return 0, nil
}

func (s *bridgeStore) DisableHermes(context.Context, dbhermes.DisableHermesParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}

func (s *bridgeStore) GetAPIKeyOwner(context.Context, dbhermes.GetAPIKeyOwnerParams) (int64, error) {
	return 0, nil
}

func (s *bridgeStore) GetProfile(context.Context, dbhermes.GetProfileParams) (dbhermes.HermesApiProfile, error) {
	return dbhermes.HermesApiProfile{}, nil
}

func (s *bridgeStore) GetSettings(context.Context, dbhermes.GetSettingsParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}

func (s *bridgeStore) ListProfilesByOwner(context.Context, dbhermes.ListProfilesByOwnerParams) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}

func (s *bridgeStore) ListProfilesByTenant(context.Context, int64) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}

func (s *bridgeStore) ProfileInUse(context.Context, dbhermes.ProfileInUseParams) (bool, error) {
	return false, nil
}

func (s *bridgeStore) SoftDeleteConversation(context.Context, dbhermes.SoftDeleteConversationParams) (int64, error) {
	return 0, nil
}

func (s *bridgeStore) UpsertSettings(context.Context, dbhermes.UpsertSettingsParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}

type warnRecorder struct {
	messages []string
}

func (r *warnRecorder) Warnf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

type dlqRecorder struct {
	id     int64
	events []legacydlq.Event
	err    error
}

func (r *dlqRecorder) Enqueue(_ context.Context, event legacydlq.Event) (int64, error) {
	r.events = append(r.events, event)
	if r.err != nil {
		return 0, r.err
	}
	return r.id, nil
}
