package hermeschat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

type txRunner interface {
	RunHermesTx(context.Context, func(hermes.Store) error) error
}

type auditDLQ interface {
	Enqueue(context.Context, legacydlq.Event) (int64, error)
}

type savepointExecutor interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
}

type warningLogger interface {
	Warnf(string, ...any)
}

type stdWarningLogger struct{}

func (stdWarningLogger) Warnf(format string, args ...any) {
	log.Printf(format, args...)
}

type Option func(*Bridge)

func WithInternalTokenSecret(secret []byte) Option {
	return func(b *Bridge) {
		b.internalTokenSecret = append([]byte(nil), secret...)
	}
}

func WithInternalBaseURL(baseURL string) Option {
	return func(b *Bridge) {
		b.internalBaseURL = strings.TrimSpace(baseURL)
	}
}

func WithClock(now func() time.Time) Option {
	return func(b *Bridge) {
		if now != nil {
			b.now = now
		}
	}
}

func WithWarningLogger(logger warningLogger) Option {
	return func(b *Bridge) {
		if logger != nil {
			b.logger = logger
		}
	}
}

func WithAuditDLQ(dlq auditDLQ) Option {
	return func(b *Bridge) {
		b.auditDLQ = dlq
	}
}

type Bridge struct {
	tx                  txRunner
	internalTokenSecret []byte
	internalBaseURL     string
	now                 func() time.Time
	logger              warningLogger
	auditDLQ            auditDLQ
}

func NewBridge(tx txRunner, opts ...Option) (*Bridge, error) {
	b := &Bridge{
		tx: tx, internalBaseURL: DefaultInternalBaseURL,
		now:    func() time.Time { return time.Now().UTC() },
		logger: stdWarningLogger{},
	}
	for _, opt := range opts {
		opt(b)
	}
	if b.tx == nil {
		return nil, fmt.Errorf("%w: hermes transaction runner is required", hermes.ErrMisconfigured)
	}
	if len(b.internalTokenSecret) == 0 {
		return nil, fmt.Errorf("%w: %s is required", hermes.ErrMisconfigured, InternalTokenSecretEnv)
	}
	if strings.TrimSpace(b.internalBaseURL) == "" {
		return nil, fmt.Errorf("%w: %s is required", hermes.ErrMisconfigured, InternalBaseURLEnv)
	}
	return b, nil
}

type Request struct {
	TenantID      int64
	UserID        int64
	RequestID     string
	CorrelationID string
	Body          []byte
}

type PreparedRequest struct {
	TenantID            int64
	UserID              int64
	RequestID           string
	CorrelationID       string
	ConversationID      int64
	CreatedConversation bool
	Body                []byte
}

func (b *Bridge) PrepareRequest(ctx context.Context, req Request) (PreparedRequest, error) {
	if b == nil || b.tx == nil {
		return PreparedRequest{}, hermes.ErrMisconfigured
	}
	if req.TenantID <= 0 || req.UserID <= 0 {
		return PreparedRequest{}, fmt.Errorf("%w: tenant_id and user_id must be positive", hermes.ErrInvalidInput)
	}
	body, err := decodeRequestBody(req.Body)
	if err != nil {
		return PreparedRequest{}, err
	}
	if err := validateChatPayload(body); err != nil {
		return PreparedRequest{}, err
	}
	conversationID, hasConversation, err := requestConversationID(body)
	if err != nil {
		return PreparedRequest{}, err
	}
	now := b.now().UTC()
	requestID, err := requestIDFor(req, now)
	if err != nil {
		return PreparedRequest{}, err
	}
	claims := InternalTokenClaims{
		TenantID: req.TenantID, UserID: req.UserID, RequestID: requestID,
		ExpiresAt: now.Add(InternalTokenTTL),
	}
	if err := validateInternalClaimsForRequest(req, claims); err != nil {
		return PreparedRequest{}, err
	}
	token, err := SignInternalToken(b.internalTokenSecret, claims)
	if err != nil {
		return PreparedRequest{}, err
	}
	created := false
	if hasConversation {
		if err := b.ensureConversationOwner(ctx, req.TenantID, req.UserID, conversationID); err != nil {
			return PreparedRequest{}, err
		}
	} else {
		conversationID, err = b.createConversation(ctx, req.TenantID, req.UserID)
		if err != nil {
			return PreparedRequest{}, err
		}
		created = true
	}
	if err := setJSONField(body, "conversation_id", conversationID); err != nil {
		return PreparedRequest{}, err
	}
	if err := setJSONField(body, "internal_base_url", b.internalBaseURL); err != nil {
		return PreparedRequest{}, err
	}
	if err := setJSONField(body, "internal_token", token); err != nil {
		return PreparedRequest{}, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return PreparedRequest{}, err
	}
	return PreparedRequest{
		TenantID: req.TenantID, UserID: req.UserID,
		RequestID: requestID, CorrelationID: strings.TrimSpace(req.CorrelationID),
		ConversationID: conversationID, CreatedConversation: created, Body: raw,
	}, nil
}

func decodeRequestBody(raw []byte) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("%w: request body is required", hermes.ErrInvalidInput)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		return nil, fmt.Errorf("%w: request body must be a JSON object", hermes.ErrInvalidInput)
	}
	return body, nil
}

func validateChatPayload(body map[string]json.RawMessage) error {
	raw, ok := body["messages"]
	if !ok || len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("%w: messages must be a non-empty array", hermes.ErrInvalidInput)
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return fmt.Errorf("%w: messages must be a non-empty array", hermes.ErrInvalidInput)
	}
	if len(messages) == 0 {
		return fmt.Errorf("%w: messages must be a non-empty array", hermes.ErrInvalidInput)
	}
	for i, msg := range messages {
		if msg == nil {
			return fmt.Errorf("%w: messages[%d] must be an object", hermes.ErrInvalidInput, i)
		}
		if err := validateMessageRole(i, msg["role"]); err != nil {
			return err
		}
		if err := validateMessageContent(i, msg["content"]); err != nil {
			return err
		}
	}
	return nil
}

func validateMessageRole(index int, raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("%w: messages[%d].role is required", hermes.ErrInvalidInput, index)
	}
	var role string
	if err := json.Unmarshal(raw, &role); err != nil || strings.TrimSpace(role) == "" {
		return fmt.Errorf("%w: messages[%d].role must be a non-empty string", hermes.ErrInvalidInput, index)
	}
	return nil
}

func validateMessageContent(index int, raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("%w: messages[%d].content is required", hermes.ErrInvalidInput, index)
	}
	switch trimmed[0] {
	case '"':
		var content string
		if err := json.Unmarshal(trimmed, &content); err != nil || strings.TrimSpace(content) == "" {
			return fmt.Errorf("%w: messages[%d].content must be non-empty", hermes.ErrInvalidInput, index)
		}
	case '[':
		var parts []json.RawMessage
		if err := json.Unmarshal(trimmed, &parts); err != nil || len(parts) == 0 {
			return fmt.Errorf("%w: messages[%d].content must be non-empty", hermes.ErrInvalidInput, index)
		}
		for partIndex, part := range parts {
			part = bytes.TrimSpace(part)
			if len(part) == 0 || bytes.Equal(part, []byte("null")) {
				return fmt.Errorf("%w: messages[%d].content[%d] is required", hermes.ErrInvalidInput, index, partIndex)
			}
		}
	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &object); err != nil || len(object) == 0 {
			return fmt.Errorf("%w: messages[%d].content must be non-empty", hermes.ErrInvalidInput, index)
		}
	default:
		return fmt.Errorf("%w: messages[%d].content must be a string, object, or array", hermes.ErrInvalidInput, index)
	}
	return nil
}

func requestConversationID(body map[string]json.RawMessage) (int64, bool, error) {
	raw, ok := body["conversation_id"]
	if !ok || len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, false, nil
	}
	var id int64
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, false, fmt.Errorf("%w: conversation_id must be an integer", hermes.ErrInvalidInput)
	}
	if id < 0 {
		return 0, false, fmt.Errorf("%w: conversation_id must be non-negative", hermes.ErrInvalidInput)
	}
	if id == 0 {
		return 0, false, nil
	}
	return id, true, nil
}

func requestIDFor(req Request, now time.Time) (string, error) {
	requestID := req.RequestID
	if requestID == "" {
		return fmt.Sprintf("hermes-%d-%d-%d", req.TenantID, req.UserID, now.UTC().UnixNano()), nil
	}
	if strings.TrimSpace(requestID) != requestID {
		return "", fmt.Errorf("%w: request_id contains disallowed whitespace", hermes.ErrInvalidInput)
	}
	for _, r := range requestID {
		if r < 0x21 || r > 0x7e || r == '|' {
			return "", fmt.Errorf("%w: request_id contains disallowed characters", hermes.ErrInvalidInput)
		}
	}
	return requestID, nil
}

func validateInternalClaimsForRequest(req Request, claims InternalTokenClaims) error {
	if claims.TenantID != req.TenantID || claims.UserID != req.UserID {
		return fmt.Errorf("%w: internal token claims do not match request identity", hermes.ErrInvalidInput)
	}
	if claims.TenantID <= 0 || claims.UserID <= 0 || strings.TrimSpace(claims.RequestID) == "" || claims.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: internal token claims are incomplete", hermes.ErrInvalidInput)
	}
	return nil
}

func setJSONField(body map[string]json.RawMessage, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	body[key] = raw
	return nil
}

func (b *Bridge) createConversation(ctx context.Context, tenantID, userID int64) (int64, error) {
	var id int64
	err := b.tx.RunHermesTx(ctx, func(store hermes.Store) error {
		created, err := store.CreateConversation(ctx, dbhermes.CreateConversationParams{
			TenantID: tenantID, OwnerUserID: userID,
		})
		if err != nil {
			return fmt.Errorf("create hermes conversation: %w", err)
		}
		id = created
		return nil
	})
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, fmt.Errorf("%w: conversation id was not returned", hermes.ErrInvalidInput)
	}
	return id, nil
}

func (b *Bridge) ensureConversationOwner(ctx context.Context, tenantID, userID, conversationID int64) error {
	return b.tx.RunHermesTx(ctx, func(store hermes.Store) error {
		row, err := store.GetConversation(ctx, dbhermes.GetConversationParams{ID: conversationID, TenantID: tenantID})
		if errors.Is(err, pgx.ErrNoRows) {
			return hermes.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get hermes conversation: %w", err)
		}
		if row.DeletedAt.Valid {
			return hermes.ErrNotFound
		}
		if row.OwnerUserID != userID {
			return hermes.ErrForbidden
		}
		return nil
	})
}

type streamState struct {
	assistantText strings.Builder
	blocked       bool
}

func (b *Bridge) Stream(ctx context.Context, w http.ResponseWriter, resp *http.Response, prepared PreparedRequest) error {
	if b == nil || w == nil || resp == nil || resp.Body == nil {
		return hermes.ErrMisconfigured
	}
	defer resp.Body.Close()
	copyResponseHeaders(w, resp.Header)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	flush(flusher)

	if prepared.CreatedConversation {
		if err := writeConversationEvent(w, flusher, prepared.ConversationID); err != nil {
			return err
		}
	}
	state := &streamState{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	var block bytes.Buffer
	for scanner.Scan() {
		line := scanner.Bytes()
		block.Write(line)
		block.WriteByte('\n')
		if len(bytes.TrimSpace(line)) == 0 {
			if err := b.handleBlock(ctx, w, flusher, prepared, state, block.Bytes()); err != nil {
				return err
			}
			block.Reset()
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if block.Len() > 0 {
		if err := b.handleBlock(ctx, w, flusher, prepared, state, block.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bridge) handleBlock(ctx context.Context, w io.Writer, flusher http.Flusher, prepared PreparedRequest, state *streamState, raw []byte) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return writeAndFlush(w, flusher, raw)
	}
	if state.blocked {
		return nil
	}
	evt := parseSSE(raw)
	switch evt.name {
	case "conversation":
		return handleConversationEvent(w, flusher, prepared, state, evt.data())
	case "token":
		if delta := tokenDeltaFromData(evt.data()); delta != "" {
			state.assistantText.WriteString(delta)
		}
		return writeAndFlush(w, flusher, raw)
	case "done":
		if err := b.persistDone(ctx, prepared, state, evt.data()); err != nil {
			return writePersistError(w, flusher)
		}
		return writeAndFlush(w, flusher, raw)
	default:
		return writeAndFlush(w, flusher, raw)
	}
}

type sseEvent struct {
	name      string
	dataLines []string
}

func (e sseEvent) data() []byte {
	return []byte(strings.Join(e.dataLines, "\n"))
}

func parseSSE(raw []byte) sseEvent {
	var evt sseEvent
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			evt.name = value
		case "data":
			evt.dataLines = append(evt.dataLines, value)
		}
	}
	return evt
}

func conversationIDFromData(data []byte) int64 {
	var payload struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0
	}
	return payload.ID
}

func handleConversationEvent(w io.Writer, flusher http.Flusher, prepared PreparedRequest, state *streamState, data []byte) error {
	if conversationIDFromData(data) != prepared.ConversationID {
		state.blocked = true
		return writeConversationMismatchError(w, flusher)
	}
	return nil
}

func tokenDeltaFromData(data []byte) string {
	var payload struct {
		Delta string `json:"delta"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	return payload.Delta
}

func totalTokensFromDone(data []byte) *int32 {
	var payload struct {
		TotalTokens int64 `json:"total_tokens"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || payload.TotalTokens < 0 || payload.TotalTokens > math.MaxInt32 {
		return nil
	}
	value := int32(payload.TotalTokens)
	return &value
}

func (b *Bridge) persistDone(ctx context.Context, prepared PreparedRequest, state *streamState, doneData []byte) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("persist hermes message panic: %v", recovered)
		}
	}()
	conversationID := prepared.ConversationID
	if conversationID <= 0 {
		return fmt.Errorf("%w: conversation_id is required", hermes.ErrInvalidInput)
	}
	content, err := json.Marshal(map[string]string{"text": state.assistantText.String()})
	if err != nil {
		return err
	}
	now := b.now().UTC()
	completedAt := pgtype.Timestamptz{Time: now, Valid: true}
	var auditErr error
	err = b.tx.RunHermesTx(ctx, func(store hermes.Store) error {
		_, err := store.AppendMessage(ctx, dbhermes.AppendMessageParams{
			TenantID: prepared.TenantID, ConversationID: conversationID, Role: "assistant",
			Content: content, TokenCount: totalTokensFromDone(doneData), CompletedAt: completedAt,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: conversation is not active", hermes.ErrGone)
			}
			return fmt.Errorf("append hermes message: %w", err)
		}
		rows, err := store.UpdateConversationLastMessageAt(ctx, dbhermes.UpdateConversationLastMessageAtParams{
			Ts: completedAt, ID: conversationID, TenantID: prepared.TenantID,
		})
		if err != nil {
			return fmt.Errorf("touch hermes conversation: %w", err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: conversation is not active", hermes.ErrGone)
		}
		auditErr = b.recordMessageAudit(ctx, store, prepared, conversationID, now)
		return nil
	})
	if err != nil {
		return err
	}
	if auditErr != nil {
		b.warnAuditFailure(ctx, prepared, conversationID, auditErr)
	}
	return nil
}

const messageAuditSavepoint = "hermes_message_audit"

func (b *Bridge) recordMessageAudit(ctx context.Context, store hermes.Store, prepared PreparedRequest, conversationID int64, now time.Time) error {
	return withAuditSavepoint(ctx, store, func() error {
		args := hermes.SanitizeArgs(map[string]any{
			"conversation_id": conversationID,
			"message_role":    "assistant",
		})
		raw, err := json.Marshal(args)
		if err != nil {
			return err
		}
		_, err = store.InsertAuditEvent(ctx, dbhermes.InsertAuditEventParams{
			Ts:       pgtype.Timestamptz{Time: now.UTC(), Valid: true},
			TenantID: prepared.TenantID, ActorUserID: prepared.UserID,
			Action: hermes.ActionMessageSend, SanitizedArgs: raw, Result: hermes.AuditResultSuccess,
			CorrelationID: stringPtr(prepared.CorrelationID), RequestID: stringPtr(prepared.RequestID),
		})
		if err != nil {
			return fmt.Errorf("%w: %w", hermes.ErrAuditRecordFailed, err)
		}
		return nil
	})
}

func withAuditSavepoint(ctx context.Context, store hermes.Store, fn func() error) (err error) {
	exec, ok := store.(savepointExecutor)
	if !ok {
		return runAuditOperation(fn)
	}
	if _, err := exec.Exec(ctx, "SAVEPOINT "+messageAuditSavepoint); err != nil {
		return fmt.Errorf("%w: create audit savepoint: %w", hermes.ErrAuditRecordFailed, err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if rollbackErr := rollbackAuditSavepoint(ctx, exec); rollbackErr != nil {
				err = fmt.Errorf("hermes audit panic: %v; rollback audit savepoint: %w", recovered, rollbackErr)
				return
			}
			err = fmt.Errorf("hermes audit panic: %v", recovered)
		}
	}()
	if err := fn(); err != nil {
		if rollbackErr := rollbackAuditSavepoint(ctx, exec); rollbackErr != nil {
			return fmt.Errorf("%v; rollback audit savepoint: %w", err, rollbackErr)
		}
		return err
	}
	if _, err := exec.Exec(ctx, "RELEASE SAVEPOINT "+messageAuditSavepoint); err != nil {
		return fmt.Errorf("%w: release audit savepoint: %w", hermes.ErrAuditRecordFailed, err)
	}
	return nil
}

func runAuditOperation(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("hermes audit panic: %v", recovered)
		}
	}()
	return fn()
}

func rollbackAuditSavepoint(ctx context.Context, exec savepointExecutor) error {
	_, err := exec.Exec(ctx, "ROLLBACK TO SAVEPOINT "+messageAuditSavepoint)
	return err
}

func (b *Bridge) warnAuditFailure(ctx context.Context, prepared PreparedRequest, conversationID int64, auditErr error) {
	if b.logger != nil {
		b.logger.Warnf("hermes audit message send failed: tenant_id=%d user_id=%d conversation_id=%d request_id=%s err=%v",
			prepared.TenantID, prepared.UserID, conversationID, prepared.RequestID, auditErr)
	}
	if b.auditDLQ == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"action":          hermes.ActionMessageSend,
		"tenant_id":       prepared.TenantID,
		"actor_user_id":   prepared.UserID,
		"conversation_id": conversationID,
		"message_role":    "assistant",
		"result":          hermes.AuditResultSuccess,
		"correlation_id":  prepared.CorrelationID,
		"request_id":      prepared.RequestID,
	})
	keyParts := []string{
		"hermes.message.send", strconv.FormatInt(prepared.TenantID, 10),
		strconv.FormatInt(conversationID, 10), strings.TrimSpace(prepared.RequestID),
	}
	key := strings.Join(keyParts, ":")
	if strings.HasSuffix(key, ":") {
		key += strconv.FormatInt(b.now().UTC().UnixNano(), 10)
	}
	_, err := b.auditDLQ.Enqueue(ctx, legacydlq.Event{
		TenantID: prepared.TenantID, EventKind: legacydlq.EventKindAuditEventReplica,
		Lane: legacydlq.LaneHigh, Payload: payload, FailureReason: auditErr.Error(),
		IdempotencyKey: key, SourceTable: "hermes_audit_events", SourceID: conversationID,
	})
	if err != nil && b.logger != nil {
		b.logger.Warnf("hermes audit message send dlq enqueue failed: tenant_id=%d conversation_id=%d request_id=%s err=%v",
			prepared.TenantID, conversationID, prepared.RequestID, err)
	}
}

func copyResponseHeaders(w http.ResponseWriter, header http.Header) {
	for k, values := range header {
		if bridgeManagedHeader(k) {
			continue
		}
		for _, value := range values {
			w.Header().Add(k, value)
		}
	}
}

func bridgeManagedHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Content-Length", "Transfer-Encoding":
		return true
	default:
		return hopByHopHeader(name)
	}
}

func writeConversationEvent(w io.Writer, flusher http.Flusher, conversationID int64) error {
	return writeAndFlush(w, flusher, []byte(fmt.Sprintf("event: conversation\ndata: {\"id\":%d}\n\n", conversationID)))
}

func writeConversationMismatchError(w io.Writer, flusher http.Flusher) error {
	return writeAndFlush(w, flusher, []byte("event: error\ndata: {\"code\":\"conversation_mismatch\",\"message\":\"runner conversation id mismatch\"}\n\n"))
}

func writePersistError(w io.Writer, flusher http.Flusher) error {
	return writeAndFlush(w, flusher, []byte("event: error\ndata: {\"code\":\"persist_failed\",\"message\":\"message persistence failed\"}\n\n"))
}

func writeAndFlush(w io.Writer, flusher http.Flusher, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	flush(flusher)
	return nil
}

func flush(flusher http.Flusher) {
	if flusher != nil {
		flusher.Flush()
	}
}

func hopByHopHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

func stringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	v := value
	return &v
}
