package hermes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

const (
	defaultPageLimit int32 = 50
	maxPageLimit     int32 = 200
)

func (s *Service) ListConversationsByOwner(ctx context.Context, tenantID, ownerUserID int64, limit, offset int32) ([]Conversation, error) {
	if s == nil || s.store == nil {
		return nil, ErrMisconfigured
	}
	if err := validateTenantUser(tenantID, ownerUserID); err != nil {
		return nil, err
	}
	limit, offset, err := normalizePagination(limit, offset)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListConversationsByOwner(ctx, dbhermes.ListConversationsByOwnerParams{
		TenantID: tenantID, OwnerUserID: ownerUserID, PageLimit: limit, PageOffset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list hermes conversations: %w", err)
	}
	return conversationsFromRows(rows), nil
}

func (s *Service) GetConversation(ctx context.Context, tenantID, conversationID, ownerUserID int64) (Conversation, error) {
	if s == nil || s.store == nil {
		return Conversation{}, ErrMisconfigured
	}
	if err := validateTenantUser(tenantID, ownerUserID); err != nil {
		return Conversation{}, err
	}
	row, err := getConversationWithStore(ctx, s.store, tenantID, conversationID)
	if err != nil {
		return Conversation{}, err
	}
	if row.OwnerUserID != ownerUserID {
		return Conversation{}, ErrNotFound
	}
	if row.DeletedAt.Valid {
		return Conversation{}, ErrGone
	}
	return conversationFromRow(row), nil
}

func (s *Service) SoftDeleteConversationWithAudit(ctx context.Context, tenantID, ownerUserID, conversationID int64, audit AuditFields) error {
	if s == nil || s.store == nil {
		return ErrMisconfigured
	}
	if err := validateTenantUser(tenantID, ownerUserID); err != nil {
		return err
	}
	if conversationID <= 0 {
		return fmt.Errorf("%w: conversation_id must be positive", ErrInvalidInput)
	}
	return s.withTx(ctx, func(store Store) error {
		row, err := getConversationWithStore(ctx, store, tenantID, conversationID)
		if err != nil {
			return err
		}
		if row.OwnerUserID != ownerUserID {
			return ErrNotFound
		}
		if !row.DeletedAt.Valid {
			rows, err := store.SoftDeleteConversation(ctx, dbhermes.SoftDeleteConversationParams{
				ID: conversationID, TenantID: tenantID,
			})
			if err != nil {
				return fmt.Errorf("soft delete hermes conversation: %w", err)
			}
			if rows == 0 {
				return ErrNotFound
			}
		}
		audit.TenantID = tenantID
		audit.ActorUserID = ownerUserID
		audit.Action = ActionConversationDelete
		audit.Result = AuditResultSuccess
		return recordAuditWithStore(ctx, store, audit)
	})
}

func (s *Service) ListMessagesByConversation(ctx context.Context, tenantID, conversationID, ownerUserID int64, limit, offset int32) ([]Message, error) {
	if s == nil || s.store == nil {
		return nil, ErrMisconfigured
	}
	if err := validateTenantUser(tenantID, ownerUserID); err != nil {
		return nil, err
	}
	if conversationID <= 0 {
		return nil, fmt.Errorf("%w: conversation_id must be positive", ErrInvalidInput)
	}
	limit, offset, err := normalizePagination(limit, offset)
	if err != nil {
		return nil, err
	}
	row, err := getConversationWithStore(ctx, s.store, tenantID, conversationID)
	if err != nil {
		return nil, err
	}
	if row.OwnerUserID != ownerUserID {
		return nil, ErrNotFound
	}
	if row.DeletedAt.Valid {
		return nil, ErrGone
	}
	rows, err := s.store.ListMessagesByConversation(ctx, dbhermes.ListMessagesByConversationParams{
		TenantID: tenantID, ConversationID: conversationID, OwnerUserID: ownerUserID,
		PageLimit: limit, PageOffset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list hermes messages: %w", err)
	}
	messages, err := s.messagesFromRows(ctx, rows)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func getConversationWithStore(ctx context.Context, store Store, tenantID, conversationID int64) (dbhermes.HermesConversation, error) {
	if store == nil {
		return dbhermes.HermesConversation{}, ErrMisconfigured
	}
	if tenantID <= 0 || conversationID <= 0 {
		return dbhermes.HermesConversation{}, fmt.Errorf("%w: tenant_id and conversation_id must be positive", ErrInvalidInput)
	}
	row, err := store.GetConversation(ctx, dbhermes.GetConversationParams{ID: conversationID, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbhermes.HermesConversation{}, ErrNotFound
	}
	if err != nil {
		return dbhermes.HermesConversation{}, fmt.Errorf("get hermes conversation: %w", err)
	}
	return row, nil
}

func normalizePagination(limit, offset int32) (int32, int32, error) {
	if limit <= 0 {
		limit = defaultPageLimit
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	if offset < 0 {
		return 0, 0, fmt.Errorf("%w: offset must be non-negative", ErrInvalidInput)
	}
	return limit, offset, nil
}

func conversationsFromRows(rows []dbhermes.HermesConversation) []Conversation {
	out := make([]Conversation, 0, len(rows))
	for _, row := range rows {
		out = append(out, conversationFromRow(row))
	}
	return out
}

func conversationFromRow(row dbhermes.HermesConversation) Conversation {
	return Conversation{
		ID: row.ID, TenantID: row.TenantID, OwnerUserID: row.OwnerUserID, Title: row.Title,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		LastMessageAt: pgTimePtr(row.LastMessageAt), DeletedAt: pgTimePtr(row.DeletedAt),
	}
}

func (s *Service) messagesFromRows(ctx context.Context, rows []dbhermes.ListMessagesByConversationRow) ([]Message, error) {
	out := make([]Message, 0, len(rows))
	for _, row := range rows {
		message, err := s.messageFromRow(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, nil
}

func (s *Service) messageFromRow(ctx context.Context, row dbhermes.ListMessagesByConversationRow) (Message, error) {
	content := row.Content
	if len(row.ContentCiphertext) > 0 {
		if s == nil || s.messageContentKeys == nil {
			return Message{}, fmt.Errorf("%w: hermes message content key provider is required", ErrMisconfigured)
		}
		plain, err := DecodeMessageContent(ctx, s.messageContentKeys, row.TenantID, row.ConversationID, row.ContentCiphertext)
		if err != nil {
			return Message{}, fmt.Errorf("decrypt hermes message content: %w", err)
		}
		content = plain
	}
	return Message{
		ID: row.ID, TenantID: row.TenantID, ConversationID: row.ConversationID,
		Role: row.Role, Content: content, TokenCount: row.TokenCount,
		CompletedAt: pgTimePtr(row.CompletedAt), CreatedAt: row.CreatedAt.Time,
	}, nil
}

func pgTimePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}
