package hermeschat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

type Request struct {
	TenantID      int64
	UserID        int64
	RequestID     string
	CorrelationID string
	Body          []byte
	// Operator carries the admin actor that opened the chat (role + admin token
	// id) so the conversational READ-ONLY tool loop (WAVE H3b) can enforce the
	// SAME RBAC role floor + tenant scope and record the SAME operator attribution
	// as the explicit H3 tool-execute endpoint. It is bound to the session's
	// request_id so the runner's mid-conversation tool callbacks resolve to this
	// operator. Zero-value (no role / no token id) => the session is not bound and
	// the conversational tool loop is unavailable for that chat (fail closed).
	Operator SessionOperator
}

type PreparedRequest struct {
	TenantID            int64
	UserID              int64
	RequestID           string
	CorrelationID       string
	ConversationID      int64
	CreatedConversation bool
	Body                []byte
	// BoundOperator is true when PrepareRequest registered a WAVE H3b session
	// binding for this request_id. The caller (startChat) releases it after the
	// stream finishes so a binding does not outlive its session.
	BoundOperator bool
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

	// WAVE H3b: bind the session operator to this request_id and inject the
	// read-only tool catalog. Both are gated on a usable operator (role + admin
	// token id) AND the bindings store being wired — if either is absent the chat
	// proceeds WITHOUT a tool loop (fail closed: the internal endpoint rejects an
	// unbound session). The binding's expiry tracks the internal_token's expiry so
	// the two die together.
	boundOperator := false
	if b.sessionBindings != nil && req.Operator.Role != "" && req.Operator.AdminActorTokenID > 0 {
		op := req.Operator
		op.TenantID = req.TenantID
		op.ActorUserID = req.UserID
		op.ExpiresAt = claims.ExpiresAt
		b.sessionBindings.Bind(requestID, op)
		boundOperator = true
		// Inject the catalog ONLY for a bound (admin) session — the LLM should not
		// be told about tools it cannot call.
		if b.toolCatalog != nil {
			catalog := b.toolCatalog.ReadOnlyToolCatalog()
			if catalog != nil {
				if err := setJSONField(body, "tool_catalog", catalog); err != nil {
					b.sessionBindings.Release(requestID)
					return PreparedRequest{}, err
				}
			}
		}
	}

	raw, err := json.Marshal(body)
	if err != nil {
		if boundOperator {
			b.sessionBindings.Release(requestID)
		}
		return PreparedRequest{}, err
	}
	return PreparedRequest{
		TenantID: req.TenantID, UserID: req.UserID,
		RequestID: requestID, CorrelationID: strings.TrimSpace(req.CorrelationID),
		ConversationID: conversationID, CreatedConversation: created, Body: raw,
		BoundOperator: boundOperator,
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
