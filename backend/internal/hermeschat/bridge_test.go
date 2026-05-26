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
	var runnerBody map[string]any
	if err := json.Unmarshal(prepared.Body, &runnerBody); err != nil {
		t.Fatalf("runner body json: %v", err)
	}
	if runnerBody["conversation_id"] != float64(1001) || runnerBody["internal_base_url"] != testInternalBaseURL {
		t.Fatalf("runner body conversation/base=%v/%v want 1001/%s", runnerBody["conversation_id"], runnerBody["internal_base_url"], testInternalBaseURL)
	}
	if token, _ := runnerBody["internal_token"].(string); token == "" {
		t.Fatalf("internal_token missing from runner body: %+v", runnerBody)
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

func TestBridgeAuditFailureWarnsDLQAndDoesNotBlockDone(t *testing.T) {
	// Regression: audit failure is a W4 durable-recovery path, not a user-visible chat failure.
	store := newBridgeStore()
	store.conversations[conversationKey{tenantID: 7, id: 77}] = dbhermes.HermesConversation{ID: 77, TenantID: 7, OwnerUserID: 42}
	store.auditErr = errors.New("audit insert failed")
	store.abortTxOnAuditFailure = true
	logger := &warnRecorder{}
	dlq := &dlqRecorder{id: 313}
	bridge := mustBridgeWithOptions(t, store, WithWarningLogger(logger), WithAuditDLQ(dlq))
	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-audit-fail",
		Body: []byte(`{"conversation_id":77,"messages":[{"role":"user","content":"hi"}]}`),
	})
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
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

	if !strings.Contains(rec.Body.String(), "event: done") {
		t.Fatalf("body=%q want done despite audit failure", rec.Body.String())
	}
	if store.committedTxs == 0 {
		t.Fatalf("committed txs=%d want message transaction committed", store.committedTxs)
	}
	if store.auditSavepointRollbacks != 1 {
		t.Fatalf("audit savepoint rollbacks=%d want 1 rollback after audit failure", store.auditSavepointRollbacks)
	}
	if len(store.appended) != 1 {
		t.Fatalf("persisted messages=%d want 1", len(store.appended))
	}
	if len(logger.messages) != 1 || !strings.Contains(logger.messages[0], "hermes audit") {
		t.Fatalf("warn logs=%v want one hermes audit warning", logger.messages)
	}
	if len(dlq.events) != 1 {
		t.Fatalf("dlq events=%d want 1", len(dlq.events))
	}
	ev := dlq.events[0]
	if ev.EventKind != legacydlq.EventKindAuditEventReplica || ev.TenantID != 7 || ev.IdempotencyKey == "" {
		t.Fatalf("dlq event=%+v want audit_event_replica tenant 7 with idempotency key", ev)
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

	touchedConversationID int64
	auditArg              dbhermes.InsertAuditEventParams
	auditErr              error
	auditPanic            bool
	abortTxOnAuditFailure bool
	txAborted             bool
	committedTxs          int

	auditSavepointActive    bool
	auditSavepointRollbacks int
}

func newBridgeStore() *bridgeStore {
	return &bridgeStore{conversations: make(map[conversationKey]dbhermes.HermesConversation)}
}

func (s *bridgeStore) RunHermesTx(ctx context.Context, fn func(hermes.Store) error) error {
	s.txAborted = false
	err := fn(s)
	if err != nil {
		return err
	}
	if s.txAborted {
		return fmt.Errorf("commit hermes tx: current transaction is aborted")
	}
	s.committedTxs++
	return nil
}

func (s *bridgeStore) AppendMessage(_ context.Context, arg dbhermes.AppendMessageParams) (int64, error) {
	if s.appendPanic {
		panic("append message panic")
	}
	if s.appendErr != nil {
		return 0, s.appendErr
	}
	s.appended = append(s.appended, arg)
	return int64(len(s.appended)), nil
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

func (s *bridgeStore) UpdateConversationLastMessageAt(_ context.Context, arg dbhermes.UpdateConversationLastMessageAtParams) (int64, error) {
	s.touchedConversationID = arg.ID
	if _, ok := s.conversations[conversationKey{tenantID: arg.TenantID, id: arg.ID}]; !ok {
		return 0, nil
	}
	return 1, nil
}

func (s *bridgeStore) InsertAuditEvent(_ context.Context, arg dbhermes.InsertAuditEventParams) (dbhermes.HermesAuditEvent, error) {
	if s.auditPanic {
		if s.abortTxOnAuditFailure {
			s.txAborted = true
		}
		panic("audit panic")
	}
	s.auditArg = arg
	if s.auditErr != nil {
		if s.abortTxOnAuditFailure {
			s.txAborted = true
		}
		return dbhermes.HermesAuditEvent{}, s.auditErr
	}
	return dbhermes.HermesAuditEvent{
		ID: 1, Ts: arg.Ts, TenantID: arg.TenantID, ActorUserID: arg.ActorUserID,
		Action: arg.Action, SanitizedArgs: arg.SanitizedArgs, Result: arg.Result,
		CorrelationID: arg.CorrelationID, RequestID: arg.RequestID,
	}, nil
}

func (s *bridgeStore) Exec(_ context.Context, sql string, _ ...interface{}) (pgconn.CommandTag, error) {
	switch strings.ToUpper(strings.TrimSpace(sql)) {
	case "SAVEPOINT HERMES_MESSAGE_AUDIT":
		s.auditSavepointActive = true
	case "ROLLBACK TO SAVEPOINT HERMES_MESSAGE_AUDIT":
		if !s.auditSavepointActive {
			return pgconn.CommandTag{}, fmt.Errorf("audit savepoint is not active")
		}
		s.txAborted = false
		s.auditSavepointRollbacks++
	case "RELEASE SAVEPOINT HERMES_MESSAGE_AUDIT":
		if !s.auditSavepointActive {
			return pgconn.CommandTag{}, fmt.Errorf("audit savepoint is not active")
		}
		s.auditSavepointActive = false
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected sql: %s", sql)
	}
	return pgconn.CommandTag{}, nil
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
