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
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

func TestBridgeDoneEventTriggersPersist(t *testing.T) {
	// 回归:gateway 在解析完 token SSE 并持久化 assistant 消息之前,不得转发 done。
	store := newBridgeStore()
	store.nextConversationID = 1001
	bridge := mustBridge(t, store)
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-done", Model: "gpt-4o", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"),
		Body: []byte(`{"messages":[{"role":"user","content":"hi"}],"model":"gpt-test","context_window":999}`), Operator: testBridgeOperator(),
	})
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}
	var preparedBody map[string]any
	if err := json.Unmarshal(prepared.Body, &preparedBody); err != nil {
		t.Fatalf("解析准备后的请求体：%v", err)
	}
	if preparedBody["model"] != "gpt-4o" {
		t.Fatalf("准备后的模型=%v，期望服务端配置覆盖客户端模型", preparedBody["model"])
	}
	if _, exists := preparedBody["context_window"]; exists {
		t.Fatalf("外部兼容模型的上下文窗口不得由 HUAKAI 目录伪造：%v", preparedBody["context_window"])
	}
	// 变异检查:若正常路径在校验之后不再创建会话,本夹具就会丢失 gateway 事件。
	if !store.createdConversation || len(store.conversations) != 1 {
		t.Fatalf("created=%v conversations=%d want one conversation for valid new chat", store.createdConversation, len(store.conversations))
	}
	var runnerBody map[string]any
	if err := json.Unmarshal(prepared.Body, &runnerBody); err != nil {
		t.Fatalf("runner body json: %v", err)
	}
	if runnerBody["conversation_id"] != float64(1001) || runnerBody["model_base_url"] != "https://model.example.com/v1" {
		t.Fatalf("runner body conversation/base=%v/%v want 1001/external model url", runnerBody["conversation_id"], runnerBody["model_base_url"])
	}
	mcpToken, _ := runnerBody["mcp_token"].(string)
	if mcpToken == "" || runnerBody["model_api_key"] != "sk-test" {
		t.Fatalf("运行器请求未携带官方模型配置或 MCP 令牌：%+v", runnerBody)
	}
	claims, err := VerifyInternalToken(mcpToken, []byte(testInternalSecret), time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatalf("VerifyInternalToken: %v", err)
	}
	if claims.Purpose != InternalTokenPurposeMCP || claims.TenantID != 7 || claims.UserID != 42 || claims.RequestID != "req-done" {
		t.Fatalf("internal token claims=%+v want tenant 7 user 42 request req-done", claims)
	}
	if runnerBody["internal_token_expires_at"] != float64(claims.ExpiresAt.Unix()) {
		t.Fatalf("令牌到期时间=%v，期望与签名声明 %d 一致", runnerBody["internal_token_expires_at"], claims.ExpiresAt.Unix())
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
	store.conversations[conversationKey{tenantID: 7, id: 55}] = testBridgeConversation(55, 7, 42)
	store.appendPanic = true
	bridge := mustBridge(t, store)
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-persist-fail", Model: "gpt-4o", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"),
		Body: []byte(`{"conversation_id":55,"messages":[{"role":"user","content":"hi"}]}`), Operator: testBridgeOperator(),
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
	if store.auditWrites != 1 || store.auditArg.Result != hermes.AuditResultFailure || store.auditArg.LogCategory != "error" {
		t.Fatalf("失败日志=%d/%s/%s，期望 1/failure/error", store.auditWrites, store.auditArg.Result, store.auditArg.LogCategory)
	}
}

func TestBridgeRunner错误会脱敏转发并记录失败日志(t *testing.T) {
	store := newBridgeStore()
	store.conversations[conversationKey{tenantID: 7, id: 56}] = testBridgeConversation(56, 7, 42)
	bridge := mustBridge(t, store)
	prepared := PreparedRequest{
		TenantID: 7, UserID: 42, ConversationID: 56, RequestID: "req-runner-error",
		CorrelationID: "corr-runner-error", ActorSource: "token", ActorID: 99, ActorRole: "platform_admin",
	}

	rec := httptest.NewRecorder()
	err := bridge.Stream(context.Background(), rec, sseResponse(
		"event: error\n"+
			"data: {\"code\":\"provider_request_rejected\",\"message\":\"上游秘密 sk-live-do-not-leak\"}\n\n",
	), prepared)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"provider_request_rejected"`) || strings.Contains(body, "sk-live-do-not-leak") {
		t.Fatalf("错误事件未保留官方 code 或泄露 runner 原文：%q", body)
	}
	if store.auditWrites != 1 || store.auditArg.Result != hermes.AuditResultFailure || store.auditArg.LogCategory != "error" {
		t.Fatalf("失败日志=%d/%s/%s，期望 1/failure/error", store.auditWrites, store.auditArg.Result, store.auditArg.LogCategory)
	}
	var args map[string]any
	if err := json.Unmarshal(store.auditArg.SanitizedArgs, &args); err != nil {
		t.Fatalf("解析失败日志参数：%v", err)
	}
	if args["error_class"] != "provider_request_rejected" || args["conversation_id"] != float64(56) {
		t.Fatalf("失败日志参数=%v", args)
	}
}

func TestBridgeRunner意外结束会生成明确错误和失败日志(t *testing.T) {
	store := newBridgeStore()
	store.conversations[conversationKey{tenantID: 7, id: 57}] = testBridgeConversation(57, 7, 42)
	bridge := mustBridge(t, store)
	prepared := PreparedRequest{
		TenantID: 7, UserID: 42, ConversationID: 57, RequestID: "req-runner-eof",
		CorrelationID: "corr-runner-eof", ActorSource: "session", ActorID: 88, ActorRole: "tenant_operator",
	}

	rec := httptest.NewRecorder()
	err := bridge.Stream(context.Background(), rec, sseResponse(
		"event: token\n"+
			"data: {\"delta\":\"未完成\"}\n\n",
	), prepared)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "runner_stream_incomplete") || strings.Contains(body, "event: done") {
		t.Fatalf("意外结束响应=%q", body)
	}
	if store.auditWrites != 1 || store.auditArg.Result != hermes.AuditResultFailure || store.auditArg.LogCategory != "error" {
		t.Fatalf("失败日志=%d/%s/%s，期望 1/failure/error", store.auditWrites, store.auditArg.Result, store.auditArg.LogCategory)
	}
}

func TestBridgeDoesNotPersistDoneAfterConversationDelete(t *testing.T) {
	// 回归:在 DELETE 之前已开始的流,不得追加删除后的消息,也不得转发 done。
	store := newBridgeStore()
	key := conversationKey{tenantID: 7, id: 66}
	store.conversations[key] = testBridgeConversation(66, 7, 42)
	bridge := mustBridge(t, store)
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-delete-race", Model: "gpt-4o", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"),
		Body: []byte(`{"conversation_id":66,"messages":[{"role":"user","content":"hi"}]}`), Operator: testBridgeOperator(),
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
	if len(store.appended) != 0 || store.touchedConversationID != 0 {
		t.Fatalf("删除后消息写入/会话触碰=%d/%d，期望 0/0", len(store.appended), store.touchedConversationID)
	}
	if store.auditWrites != 1 || store.auditArg.Result != hermes.AuditResultFailure || store.auditArg.LogCategory != "error" {
		t.Fatalf("删除竞争失败日志=%d/%s/%s，期望 1/failure/error", store.auditWrites, store.auditArg.Result, store.auditArg.LogCategory)
	}
	if store.committedTxs != commitsBeforeDelete+1 {
		t.Fatalf("提交事务=%d，原值=%d；期望业务事务回滚且仅失败日志事务提交", store.committedTxs, commitsBeforeDelete)
	}
}

func TestBridgePersistDone_AuditInsertFailureAbortsTransaction(t *testing.T) {
	// 回归:消息持久化与主审计必须原子;审计失败必须回滚消息写入。
	store := newBridgeStore()
	store.conversations[conversationKey{tenantID: 7, id: 77}] = testBridgeConversation(77, 7, 42)
	store.auditErr = errors.New("audit insert failed")
	bridge := mustBridge(t, store)
	var err error
	prepared := PreparedRequest{
		TenantID: 7, UserID: 42, ConversationID: 77, RequestID: "req-audit-fail",
		CorrelationID: "corr-audit-fail", ActorSource: "token", ActorID: 99, ActorRole: "platform_admin",
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
	store.conversations[conversationKey{tenantID: 7, id: 77}] = testBridgeConversation(77, 7, 42)
	bridge := mustBridge(t, store)
	var err error
	prepared := PreparedRequest{
		TenantID: 7, UserID: 42, ConversationID: 77, RequestID: "req-audit-ok",
		CorrelationID: "corr-audit-ok", ActorSource: "token", ActorID: 99, ActorRole: "platform_admin",
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
	store.conversations[conversationKey{tenantID: 7, id: 77}] = testBridgeConversation(77, 7, 42)
	keys := mustHermesChatContentKeys(t)
	bridge := mustBridgeWithOptions(t, store, WithMessageContentKeys(keys))
	sentinel := "HERMES_PRIVACY_SENTINEL_plaintext_must_not_be_stored"
	var state streamState
	state.assistantText.WriteString(sentinel)
	err := bridge.persistDone(context.Background(), PreparedRequest{
		TenantID: 7, UserID: 42, ConversationID: 77, RequestID: "req-encrypt",
		ActorSource: "token", ActorID: 99, ActorRole: "platform_admin",
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
	store.conversations[conversationKey{tenantID: 7, id: 77}] = testBridgeConversation(77, 7, 42)
	store.conversations[conversationKey{tenantID: 7, id: 88}] = testBridgeConversation(88, 7, 99)
	bridge := mustBridge(t, store)
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-retarget", Model: "gpt-4o", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"),
		Body: []byte(`{"conversation_id":77,"messages":[{"role":"user","content":"hi"}]}`), Operator: testBridgeOperator(),
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
		TenantID: 7, UserID: 42, RequestID: "req-conversation-echo", Model: "gpt-4o", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"),
		Body: []byte(`{"messages":[{"role":"user","content":"hi"}]}`), Operator: testBridgeOperator(),
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
	store.conversations[conversationKey{tenantID: 7, id: 501}] = testBridgeConversation(501, 7, 42)
	store.conversations[conversationKey{tenantID: 8, id: 501}] = testBridgeConversation(501, 8, 99)
	bridge := mustBridge(t, store)

	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-tenant-scope", Model: "gpt-4o", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"),
		Body: []byte(`{"conversation_id":501,"messages":[{"role":"user","content":"hi"}]}`), Operator: testBridgeOperator(),
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
	if store.getConversationArg.ActorSource != "token" || store.getConversationArg.ActorID != 99 {
		t.Fatalf("GetConversation 管理员=%s/%d，期望 token/99", store.getConversationArg.ActorSource, store.getConversationArg.ActorID)
	}
}

func TestBridge拒绝续写同租户其他管理员会话(t *testing.T) {
	store := newBridgeStore()
	row := testBridgeConversation(502, 7, 42)
	row.ActorID = 777
	store.conversations[conversationKey{tenantID: 7, id: 502}] = row
	bridge := mustBridge(t, store)

	_, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-actor-scope", Model: "gpt-4o", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"),
		Body: []byte(`{"conversation_id":502,"messages":[{"role":"user","content":"hi"}]}`), Operator: testBridgeOperator(),
	})
	if !errors.Is(err, hermes.ErrNotFound) {
		t.Fatalf("跨管理员续聊 err=%v，期望不可枚举的 ErrNotFound", err)
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
				TenantID: 7, UserID: 42, RequestID: "req-conversation-contract", Model: "gpt-4o", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"), Body: []byte(tc.body), Operator: testBridgeOperator(),
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
				TenantID: 7, UserID: 42, RequestID: tc.requestID, Model: "gpt-4o", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"), Body: []byte(tc.body), Operator: testBridgeOperator(),
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

func TestBridge允许租户配置任意兼容模型名(t *testing.T) {
	store := newBridgeStore()
	bridge := mustBridgeWithOptions(t, store)

	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-unknown-model", Model: "unknown-model", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"),
		Body: []byte(`{"messages":[{"role":"user","content":"hi"}]}`), Operator: testBridgeOperator(),
	})
	if err != nil {
		t.Fatalf("外部兼容模型不应被 HUAKAI 内部目录拒绝：%v", err)
	}
	if !store.createdConversation || prepared.ConversationID <= 0 {
		t.Fatalf("合法外部模型请求没有建立会话：created=%v conversation=%d", store.createdConversation, prepared.ConversationID)
	}
}

func TestInternalTokenSignedAndVerifiesSecret(t *testing.T) {
	// 回归:面向 runner 的请求体必须携带一个已签名的 internal token,而非任何 secret 都接受的 bearer 字符串。
	now := time.Unix(1700000000, 0).UTC()
	token, err := SignInternalToken([]byte(testInternalSecret), InternalTokenClaims{
		TenantID: 7, UserID: 42,
		ActorSource: "token", ActorID: 99, ActorRole: "platform_admin",
		RequestID: "req-token", IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("SignInternalToken: %v", err)
	}

	claims, err := VerifyInternalToken(token, []byte(testInternalSecret), now)
	if err != nil {
		t.Fatalf("VerifyInternalToken: %v", err)
	}
	if claims.TenantID != 7 || claims.UserID != 42 || claims.ActorSource != "token" || claims.ActorID != 99 || claims.ActorRole != "platform_admin" || claims.RequestID != "req-token" {
		t.Fatalf("claims=%+v want tenant/user/request", claims)
	}
	if _, err := VerifyInternalToken(token, []byte("wrong-secret-32-bytes-minimum-value"), now); err == nil {
		t.Fatalf("VerifyInternalToken with wrong secret succeeded")
	}
}

func TestActionMessageSendIsValidAuditAction(t *testing.T) {
	// hermes.message.send 必须被当前日志动作允许清单接受。
	store := newBridgeStore()
	svc := hermes.NewService(store)

	err := svc.RecordAudit(context.Background(), hermes.AuditFields{
		TenantID: 7, ActorSource: "token", ActorID: 99, ActorRole: "platform_admin",
		Action: hermes.ActionMessageSend,
		SanitizedArgs: map[string]any{
			"conversation_id": int64(123),
			"message_role":    "assistant",
		},
		Result: hermes.AuditResultSuccess, CorrelationID: "corr-action", RequestID: "req-action",
	})
	if err != nil {
		t.Fatalf("RecordAudit(ActionMessageSend): %v", err)
	}
	if store.auditArg.Action != hermes.ActionMessageSend {
		t.Fatalf("audit action=%q want %q", store.auditArg.Action, hermes.ActionMessageSend)
	}
}

const (
	testInternalSecret = "test-internal-secret-32-byte-value"
)

func testBridgeOperator() SessionOperator {
	return SessionOperator{TenantID: 7, ActorSource: "token", ActorID: 99, Role: "platform_admin"}
}

func testBridgeConversation(id, tenantID, ownerUserID int64) dbhermes.HermesConversation {
	return dbhermes.HermesConversation{
		ID: id, TenantID: tenantID, OwnerUserID: ownerUserID,
		ActorSource: "token", ActorID: 99,
	}
}

func mustBridge(t *testing.T, store *bridgeStore) *Bridge {
	t.Helper()
	return mustBridgeWithOptions(t, store)
}

func mustBridgeWithOptions(t *testing.T, store *bridgeStore, opts ...Option) *Bridge {
	t.Helper()
	allOpts := []Option{
		WithInternalTokenSecret([]byte(testInternalSecret)),
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
	store.conversations[conversationKey{tenantID: 7, id: 77}] = testBridgeConversation(77, 7, 42)
	bridge := mustBridge(t, store)
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-framing", Model: "gpt-4o", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"),
		Body: []byte(`{"conversation_id":77,"messages":[{"role":"user","content":"hi"}]}`), Operator: testBridgeOperator(),
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
	store.conversations[conversationKey{tenantID: 7, id: 77}] = testBridgeConversation(77, 7, 42)
	bridge := mustBridge(t, store)
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-header-firewall", Model: "gpt-4o", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"),
		Body: []byte(`{"conversation_id":77,"messages":[{"role":"user","content":"hi"}]}`), Operator: testBridgeOperator(),
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
	if !ok || row.DeletedAt.Valid || row.OwnerUserID != arg.OwnerUserID || row.ActorSource != arg.ActorSource || row.ActorID != arg.ActorID {
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
		ActorSource: arg.ActorSource, ActorID: arg.ActorID, ActorRole: bridgeStringPtr(arg.ActorRole),
	}
	return id, nil
}

func (s *bridgeStore) GetConversation(_ context.Context, arg dbhermes.GetConversationParams) (dbhermes.HermesConversation, error) {
	s.getConversationArg = arg
	row, ok := s.conversations[conversationKey{tenantID: arg.TenantID, id: arg.ID}]
	if !ok || row.OwnerUserID != arg.OwnerUserID || row.ActorSource != arg.ActorSource || row.ActorID != arg.ActorID {
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
	if !ok || row.DeletedAt.Valid || row.OwnerUserID != arg.OwnerUserID || row.ActorSource != arg.ActorSource || row.ActorID != arg.ActorID {
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
			ID: 1, Ts: arg.Ts, TenantID: arg.TenantID,
			Action: arg.Action, SanitizedArgs: arg.SanitizedArgs, Result: arg.Result,
			CorrelationID: arg.CorrelationID, RequestID: arg.RequestID, LogCategory: arg.LogCategory,
			ActorSource: arg.ActorSource, ActorID: arg.ActorID, ActorRole: bridgeStringPtr(arg.ActorRole),
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
		ID: 1, Ts: arg.Ts, TenantID: arg.TenantID,
		Action: arg.Action, SanitizedArgs: arg.SanitizedArgs, Result: arg.Result,
		CorrelationID: arg.CorrelationID, RequestID: arg.RequestID, LogCategory: arg.LogCategory,
		ActorSource: arg.ActorSource, ActorID: arg.ActorID, ActorRole: bridgeStringPtr(arg.ActorRole),
	}, nil
}

func bridgeStringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
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
