package channeltemplatestore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

var errNotConfigured = errors.New("channel test template store not configured")

type Audit struct {
	ActorID   string
	ActorRole string
	RequestID string
}

type Store struct {
	base *admindb.Queries
	pool *pgxpool.Pool
}

func New(base *admindb.Queries, pool *pgxpool.Pool) *Store {
	if base == nil && pool != nil {
		base = admindb.New(pool)
	}
	return &Store{base: base, pool: pool}
}

func (s *Store) ListChannelTestTemplatesByTenant(
	ctx context.Context,
	arg admindb.ListChannelTestTemplatesByTenantParams,
) ([]admindb.ChannelTestTemplate, error) {
	if s == nil || s.base == nil {
		return nil, errNotConfigured
	}
	return s.base.ListChannelTestTemplatesByTenant(ctx, arg)
}

func (s *Store) GetChannelTestTemplate(
	ctx context.Context,
	arg admindb.GetChannelTestTemplateParams,
) (admindb.ChannelTestTemplate, error) {
	if s == nil || s.base == nil {
		return admindb.ChannelTestTemplate{}, errNotConfigured
	}
	return s.base.GetChannelTestTemplate(ctx, arg)
}

func (s *Store) CreateChannelTestTemplateWithAudit(
	ctx context.Context,
	arg admindb.CreateChannelTestTemplateParams,
	audit Audit,
) (admindb.ChannelTestTemplate, error) {
	if err := s.validateAudit(audit); err != nil {
		return admindb.ChannelTestTemplate{}, err
	}
	payload, err := logPayload(arg.Method, arg.BodyTemplate, arg.Headers, []string{
		"name", "method", "path", "body_template", "headers",
	})
	if err != nil {
		return admindb.ChannelTestTemplate{}, err
	}
	var out admindb.ChannelTestTemplate
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		row, createErr := q.CreateChannelTestTemplate(ctx, arg)
		if createErr != nil {
			return createErr
		}
		if logErr := insertLog(ctx, q, row.TenantID, row.ID, "create_channel_test_template", payload, audit); logErr != nil {
			return logErr
		}
		out = row
		return nil
	})
	return out, err
}

func (s *Store) UpdateChannelTestTemplateWithAudit(
	ctx context.Context,
	arg admindb.UpdateChannelTestTemplateParams,
	audit Audit,
) (admindb.ChannelTestTemplate, error) {
	if err := s.validateAudit(audit); err != nil {
		return admindb.ChannelTestTemplate{}, err
	}
	payload, err := logPayload(arg.Method, arg.BodyTemplate, arg.Headers, []string{
		"name", "method", "path", "body_template", "headers",
	})
	if err != nil {
		return admindb.ChannelTestTemplate{}, err
	}
	var out admindb.ChannelTestTemplate
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		row, updateErr := q.UpdateChannelTestTemplate(ctx, arg)
		if updateErr != nil {
			return updateErr
		}
		if logErr := insertLog(ctx, q, row.TenantID, row.ID, "update_channel_test_template", payload, audit); logErr != nil {
			return logErr
		}
		out = row
		return nil
	})
	return out, err
}

func (s *Store) DeleteChannelTestTemplateWithAudit(
	ctx context.Context,
	arg admindb.DeleteChannelTestTemplateParams,
	audit Audit,
) (admindb.ChannelTestTemplate, error) {
	if err := s.validateAudit(audit); err != nil {
		return admindb.ChannelTestTemplate{}, err
	}
	var out admindb.ChannelTestTemplate
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		row, deleteErr := q.DeleteChannelTestTemplate(ctx, arg)
		if deleteErr != nil {
			return deleteErr
		}
		payload, marshalErr := logPayload(row.Method, row.BodyTemplate, row.Headers, nil)
		if marshalErr != nil {
			return marshalErr
		}
		if logErr := insertLog(ctx, q, row.TenantID, row.ID, "delete_channel_test_template", payload, audit); logErr != nil {
			return logErr
		}
		out = row
		return nil
	})
	return out, err
}

func (s *Store) validateAudit(audit Audit) error {
	if s == nil || s.pool == nil {
		return errNotConfigured
	}
	if strings.TrimSpace(audit.ActorID) == "" || strings.TrimSpace(audit.ActorRole) == "" {
		return errors.New("channel test template audit actor is required")
	}
	return nil
}

func logPayload(method, body string, headers []byte, changedFields []string) ([]byte, error) {
	var headerMap map[string]json.RawMessage
	if err := json.Unmarshal(headers, &headerMap); err != nil {
		return nil, err
	}
	payload := map[string]any{
		"method":       method,
		"has_body":     strings.TrimSpace(body) != "",
		"header_count": len(headerMap),
	}
	if len(changedFields) > 0 {
		payload["changed_fields"] = changedFields
	}
	return json.Marshal(payload)
}

func insertLog(
	ctx context.Context,
	q *admindb.Queries,
	tenantID, templateID int64,
	action string,
	payload []byte,
	audit Audit,
) error {
	requestID := strings.TrimSpace(audit.RequestID)
	_, err := q.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID:   &tenantID,
		ActorID:    strings.TrimSpace(audit.ActorID),
		ActorRole:  strings.TrimSpace(audit.ActorRole),
		Action:     action,
		TargetType: "channel_test_template",
		TargetID:   &templateID,
		RequestID:  optionalText(requestID),
		Payload:    payload,
	})
	return err
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
