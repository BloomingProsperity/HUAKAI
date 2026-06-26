package hermeschat

import (
	"bytes"
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

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

func TestBridgeDoneEventTriggersPersist(t *testing.T) {
	// 回归:gateway 在解析完 token SSE 并持久化 assistant 消息之前,不得转发 done。
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
	// 变异检查:若正常路径在校验之后不再创建会话,本夹具就会丢失 gateway 事件。
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
	plain, err := hermes.DecodeMessageContent(context.Background(), mustHermesChatContentKeys(t), msg.TenantID, msg.ConversationID, msg.ContentCiphertext)
	if err != nil {
		t.Fatalf("DecodeMessageContent: %v", err)
	}
	var content map[string]string
	if err := json.Unmarshal(plain, &content); err != nil {
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
	// 回归:若消息持久化在 done 栅栏处失败,客户端必须看到 error,而非一个虚假的 done。
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
	// 回归:在 DELETE 之前已开始的流,不得追加删除后的消息,也不得转发 done。
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
	// 变异检查:若去掉 AppendMessage 或 SQL 的活跃会话守卫,这里就会记录一条删除后的消息。
	if len(store.appended) != 0 || store.touchedConversationID != 0 || store.auditArg.Action != "" {
		t.Fatalf("append/touch/audit=%d/%d/%q want no post-delete writes", len(store.appended), store.touchedConversationID, store.auditArg.Action)
	}
	if store.committedTxs != commitsBeforeDelete {
		t.Fatalf("committed txs=%d before=%d want rollback after deleted conversation", store.committedTxs, commitsBeforeDelete)
	}
}

func TestBridgePersistDone_AuditInsertFailureAbortsTransaction(t *testing.T) {
	// 回归:消息持久化与主审计必须原子;审计失败必须回滚消息写入。
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

	// 变异检查:若 RunHermesTx 仍忽略消息审计错误,commitCount 会变成 1。
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
	// 回归:正常路径在一个事务里持久化消息与审计,并提交一次。
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

func TestBridgePersistDoneEncryptsMessageContentBeforeStore(t *testing.T) {
	// 回归:Hermes 允许保留用户可见的聊天历史,但新写入的行不得以明文持久化消息文本。
	store := newBridgeStore()
	store.conversations[conversationKey{tenantID: 7, id: 77}] = dbhermes.HermesConversation{ID: 77, TenantID: 7, OwnerUserID: 42}
	keys := mustHermesChatContentKeys(t)
	bridge := mustBridgeWithOptions(t, store, WithMessageContentKeys(keys))
	sentinel := "HERMES_PRIVACY_SENTINEL_plaintext_must_not_be_stored"
	var state streamState
	state.assistantText.WriteString(sentinel)
	err := bridge.persistDone(context.Background(), PreparedRequest{
		TenantID: 7, UserID: 42, ConversationID: 77, RequestID: "req-encrypt",
	}, &state, []byte(`{"total_tokens":2}`))
	if err != nil {
		t.Fatalf("persistDone encrypted: %v", err)
	}
	if len(store.appended) != 1 {
		t.Fatalf("persisted messages=%d want 1", len(store.appended))
	}
	msg := store.appended[0]
	if len(msg.ContentCiphertext) == 0 {
		t.Fatalf("content_ciphertext empty; mutation storing plaintext must fail this test")
	}
	if bytes.Contains(msg.ContentCiphertext, []byte(sentinel)) {
		t.Fatalf("content_ciphertext contains plaintext sentinel: %q", msg.ContentCiphertext)
	}
	if bytes.Contains(msg.Content, []byte(sentinel)) {
		t.Fatalf("content placeholder contains plaintext sentinel: %s", string(msg.Content))
	}
	if string(msg.Content) != hermes.EncryptedMessageContentPlaceholder {
		t.Fatalf("content placeholder=%s want %s", string(msg.Content), hermes.EncryptedMessageContentPlaceholder)
	}
	plain, err := hermes.DecodeMessageContent(context.Background(), keys, msg.TenantID, msg.ConversationID, msg.ContentCiphertext)
	if err != nil {
		t.Fatalf("DecodeMessageContent: %v", err)
	}
	var content map[string]string
	if err := json.Unmarshal(plain, &content); err != nil {
		t.Fatalf("decrypted content json: %v", err)
	}
	// 变异检查:若 persistDone 写明文或跳过加密,密文就会为空/为明文,这一次精确的往返就会失败。
	if content["text"] != sentinel {
		t.Fatalf("decrypted text=%q want sentinel", content["text"])
	}
}

func TestBridgeRejectsRunnerConversationRetarget(t *testing.T) {
	// 守护的回归:runner 提供的会话 id 不得把已持久化的消息跨用户改向到别处。
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
	// 回归:gateway 已经发出了准备好的会话 id,因此 runner 的回显不得到达客户端。
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
	// 回归:会话归属必须按 tenant id 校验,否则相同数字 id 可能跨 tenant 串号。
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
	// 回归:conversation_id=0 是合法的"新聊天"哨兵值;只有负数 id 才是非法输入。
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
	// 守护的回归:非法的聊天输入必须在 CreateConversation 之前被拒绝,否则用户历史会被污染。
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
			// 变异检查:把校验挪到 CreateConversation 之后,或把负数 conversation_id 当成缺省值,这条行数断言就会失败。
			if store.createdConversation || len(store.conversations) != 0 {
				t.Fatalf("created=%v conversations=%d want no row for invalid payload", store.createdConversation, len(store.conversations))
			}
		})
	}
}

func TestInternalTokenSignedAndVerifiesSecret(t *testing.T) {
	// 回归:面向 runner 的请求体必须携带一个已签名的 internal token,而非任何 secret 都接受的 bearer 字符串。
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
	// 回归:在 Slice 2.0 的 DB CHECK 迁移之后,hermes.message.send 必须被本地白名单接受。
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
		WithMessageContentKeys(mustHermesChatContentKeys(t)),
	}
	allOpts = append(allOpts, opts...)
	bridge, err := NewBridge(store, allOpts...)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	return bridge
}

func mustHermesChatContentKeys(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("hermes-chat-test", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	return keys
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
	// 回归:gateway 会重写 SSE 字节,因此 runner 的 framing 头会让 net/http 截断或拒绝 body。
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

func TestBridgeStreamFiltersSensitiveRunnerHeaders(t *testing.T) {
	// 回归:runner 的响应头不得把凭证或基础设施元数据夹带给客户端。
	store := newBridgeStore()
	store.conversations[conversationKey{tenantID: 7, id: 77}] = dbhermes.HermesConversation{ID: 77, TenantID: 7, OwnerUserID: 42}
	bridge := mustBridge(t, store)
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-header-firewall",
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
	resp.Header.Set("Set-Cookie", "session=redacted")
	resp.Header.Set("Authorization", "Bearer redacted")
	resp.Header.Set("CF-Ray", "edge-redacted")
	resp.Header.Set("X-Amz-Request-Id", "aws-redacted")
	resp.Header.Set("X-Runner-Trace", "keep")

	rec := httptest.NewRecorder()
	if err := bridge.Stream(context.Background(), rec, resp, prepared); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	for _, name := range []string{"Set-Cookie", "Authorization", "CF-Ray", "X-Amz-Request-Id"} {
		if rec.Header().Get(name) != "" {
			t.Fatalf("%s leaked through Hermes chat bridge response", name)
		}
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type=%q want text/event-stream", got)
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

func (s *bridgeStore) ListMessagesByConversation(context.Context, dbhermes.ListMessagesByConversationParams) ([]dbhermes.ListMessagesByConversationRow, error) {
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
