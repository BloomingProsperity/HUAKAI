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
	// Operator 携带开启聊天的 admin actor(role + admin token id),使会话式只读工具
	// 循环(WAVE H3b)能强制执行与显式 H3 tool-execute 端点相同的 RBAC role 下限 +
	// tenant 范围,并记录相同的 operator 归属。它被绑定到会话的 request_id,使 runner
	// 在对话中途的工具回调能解析到此 operator。零值(无 role / 无 token id)=> 会话未
	// 绑定,该聊天无法使用会话式工具循环(fail closed)。
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
	// BoundOperator 为 true 表示 PrepareRequest 已为此 request_id 注册了一个 WAVE H3b
	// 会话绑定。调用方(startChat)会在流结束后释放它,使绑定不会比其会话存活更久。
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

	// WAVE H3b:将会话 operator 绑定到此 request_id 并注入只读工具目录。两者都以一个可用的
	// operator(role + admin token id)以及已接线的绑定存储为前提——若任一缺失,聊天将在
	// 没有工具循环的情况下继续(fail closed:内部端点会拒绝未绑定的会话)。绑定的过期时间
	// 跟随 internal_token 的过期时间,使两者同生共死。
	boundOperator := false
	if b.sessionBindings != nil && req.Operator.Role != "" && req.Operator.AdminActorTokenID > 0 {
		op := req.Operator
		op.TenantID = req.TenantID
		op.ActorUserID = req.UserID
		op.ExpiresAt = claims.ExpiresAt
		// fail-closed:若此 request_id 已被【另一个】operator 占用(客户端可影响 request_id,
		// 见 Bind 注释),Bind 会作废该键并返回 false。此时不注入工具目录、不置 boundOperator——
		// 该会话回落到无工具的普通聊天,startChat 也不会去 Release 一个不属于它的绑定。
		if b.sessionBindings.Bind(requestID, op) {
			boundOperator = true
			// 仅为已绑定的(admin)会话注入目录——不应让 LLM 知道它无法调用的工具。KNOB B:
			// 当会话式工具循环在运行时被禁用时,完全跳过注入,使 LLM 被告知没有任何工具
			//(内部 tool-execute 端点同样被闸门关闭)。
			if b.toolLoopEnabled && b.toolCatalog != nil {
				catalog := b.toolCatalog.ToolCatalog()
				if catalog != nil {
					if err := setJSONField(body, "tool_catalog", catalog); err != nil {
						b.sessionBindings.Release(requestID)
						return PreparedRequest{}, err
					}
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
