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
	Model         string
	ModelBaseURL  string
	ModelAPIKey   []byte
	Body          []byte
	// Operator 携带开启聊天的真实管理员身份，并被签入短时内部令牌。
	Operator SessionOperator
}

type PreparedRequest struct {
	TenantID            int64
	UserID              int64
	ActorSource         string
	ActorID             int64
	ActorRole           string
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
	model := strings.TrimSpace(req.Model)
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
		TenantID: req.TenantID, UserID: req.UserID,
		ActorSource: req.Operator.ActorSource, ActorID: req.Operator.ActorID, ActorRole: req.Operator.Role,
		RequestID: requestID,
		IssuedAt:  now, ExpiresAt: now.Add(InternalTokenTTL),
	}
	if err := validateInternalClaimsForRequest(req, claims); err != nil {
		return PreparedRequest{}, err
	}
	mcpClaims := claims
	mcpClaims.Purpose = InternalTokenPurposeMCP
	mcpToken, err := SignInternalToken(b.internalTokenSecret, mcpClaims)
	if err != nil {
		return PreparedRequest{}, err
	}
	modelBaseURL, err := hermes.NormalizeExternalBaseURL(req.ModelBaseURL)
	if err != nil {
		return PreparedRequest{}, fmt.Errorf("%w: model_base_url invalid", hermes.ErrInvalidInput)
	}
	if len(bytes.TrimSpace(req.ModelAPIKey)) == 0 {
		return PreparedRequest{}, fmt.Errorf("%w: model_api_key is required", hermes.ErrInvalidInput)
	}
	created := false
	if hasConversation {
		if err := b.ensureConversationOwner(ctx, req.TenantID, req.UserID, conversationID, req.Operator); err != nil {
			return PreparedRequest{}, err
		}
	} else {
		conversationID, err = b.createConversation(ctx, req.TenantID, req.UserID, req.Operator)
		if err != nil {
			return PreparedRequest{}, err
		}
		created = true
	}
	if err := setJSONField(body, "conversation_id", conversationID); err != nil {
		return PreparedRequest{}, err
	}
	if err := setJSONField(body, "mcp_token", mcpToken); err != nil {
		return PreparedRequest{}, err
	}
	if err := setJSONField(body, "model_base_url", modelBaseURL); err != nil {
		return PreparedRequest{}, err
	}
	if err := setJSONField(body, "model_api_key", string(req.ModelAPIKey)); err != nil {
		return PreparedRequest{}, err
	}
	if err := setJSONField(body, "internal_token_expires_at", claims.ExpiresAt.Unix()); err != nil {
		return PreparedRequest{}, err
	}
	if err := setJSONField(body, "model", model); err != nil {
		return PreparedRequest{}, err
	}
	delete(body, "context_window")

	raw, err := json.Marshal(body)
	if err != nil {
		return PreparedRequest{}, err
	}
	return PreparedRequest{
		TenantID: req.TenantID, UserID: req.UserID,
		ActorSource: req.Operator.ActorSource, ActorID: req.Operator.ActorID, ActorRole: req.Operator.Role,
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
	if claims.ActorSource != req.Operator.ActorSource || claims.ActorID != req.Operator.ActorID || claims.ActorRole != req.Operator.Role {
		return fmt.Errorf("%w: internal token claims do not match administrator identity", hermes.ErrInvalidInput)
	}
	if claims.TenantID <= 0 || claims.UserID <= 0 || claims.ActorID <= 0 || strings.TrimSpace(claims.RequestID) == "" || claims.ExpiresAt.IsZero() {
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

func (b *Bridge) createConversation(ctx context.Context, tenantID, userID int64, operator SessionOperator) (int64, error) {
	var id int64
	err := b.tx.RunHermesTx(ctx, func(store hermes.Store) error {
		created, err := store.CreateConversation(ctx, dbhermes.CreateConversationParams{
			TenantID: tenantID, OwnerUserID: userID,
			ActorSource: operator.ActorSource, ActorID: operator.ActorID, ActorRole: operator.Role,
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

func (b *Bridge) ensureConversationOwner(ctx context.Context, tenantID, userID, conversationID int64, operator SessionOperator) error {
	return b.tx.RunHermesTx(ctx, func(store hermes.Store) error {
		row, err := store.GetConversation(ctx, dbhermes.GetConversationParams{
			ID: conversationID, TenantID: tenantID, OwnerUserID: userID,
			ActorSource: operator.ActorSource, ActorID: operator.ActorID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return hermes.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get hermes conversation: %w", err)
		}
		if row.DeletedAt.Valid {
			return hermes.ErrNotFound
		}
		if row.OwnerUserID != userID || row.ActorSource != operator.ActorSource || row.ActorID != operator.ActorID {
			return hermes.ErrForbidden
		}
		return nil
	})
}
