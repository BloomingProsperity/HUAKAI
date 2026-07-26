package proxyadmin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

const proxyAuditTarget = "proxy"

type mutationStore interface {
	Create(context.Context, admindb.CreateProxyParams, MutationAudit) (admindb.CreateProxyRow, error)
	Update(context.Context, admindb.UpdateProxyParams, []string, MutationAudit) (admindb.UpdateProxyRow, error)
	Delete(context.Context, admindb.DeleteProxyIfUnusedParams, MutationAudit) (admindb.DeleteProxyIfUnusedRow, error)
	SetStatus(context.Context, admindb.SetProxyStatusParams, MutationAudit) (int64, error)
}

type postgresMutationStore struct {
	pool *pgxpool.Pool
}

func newPostgresMutationStore(pool *pgxpool.Pool) *postgresMutationStore {
	return &postgresMutationStore{pool: pool}
}

func (s *postgresMutationStore) Create(
	ctx context.Context,
	params admindb.CreateProxyParams,
	audit MutationAudit,
) (admindb.CreateProxyRow, error) {
	if s == nil || s.pool == nil {
		return admindb.CreateProxyRow{}, ErrStoreNotConfigured
	}
	payload, err := json.Marshal(map[string]any{
		"protocol":        params.Protocol,
		"status":          params.Status,
		"grouped":         params.GroupID != nil,
		"auth_configured": params.AuthSecret != nil,
	})
	if err != nil {
		return admindb.CreateProxyRow{}, proxyMutationBackendError("encode create log", err)
	}
	var out admindb.CreateProxyRow
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		row, createErr := q.CreateProxy(ctx, params)
		if createErr != nil {
			return createErr
		}
		if logErr := insertProxyMutationLog(ctx, q, params.TenantID, row.ID, "create_proxy", payload, audit); logErr != nil {
			return logErr
		}
		out = row
		return nil
	})
	return out, err
}

func (s *postgresMutationStore) Update(
	ctx context.Context,
	params admindb.UpdateProxyParams,
	changedFields []string,
	audit MutationAudit,
) (admindb.UpdateProxyRow, error) {
	if s == nil || s.pool == nil {
		return admindb.UpdateProxyRow{}, ErrStoreNotConfigured
	}
	payload, err := json.Marshal(map[string]any{"changed_fields": changedFields})
	if err != nil {
		return admindb.UpdateProxyRow{}, proxyMutationBackendError("encode update log", err)
	}
	var out admindb.UpdateProxyRow
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		row, updateErr := q.UpdateProxy(ctx, params)
		if updateErr != nil {
			return updateErr
		}
		if logErr := insertProxyMutationLog(ctx, q, params.TenantID, row.ID, "update_proxy", payload, audit); logErr != nil {
			return logErr
		}
		out = row
		return nil
	})
	return out, err
}

func (s *postgresMutationStore) Delete(
	ctx context.Context,
	params admindb.DeleteProxyIfUnusedParams,
	audit MutationAudit,
) (admindb.DeleteProxyIfUnusedRow, error) {
	if s == nil || s.pool == nil {
		return admindb.DeleteProxyIfUnusedRow{}, ErrStoreNotConfigured
	}
	var out admindb.DeleteProxyIfUnusedRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		row, deleteErr := q.DeleteProxyIfUnused(ctx, params)
		if deleteErr != nil {
			return deleteErr
		}
		out = row
		if !row.Deleted {
			return nil
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"direct_account_count":         row.DirectAccountCount,
			"default_tenant_count":         row.DefaultTenantCount,
			"group_account_count":          row.GroupAccountCount,
			"group_remaining_active_count": row.GroupRemainingActiveCount,
		})
		if marshalErr != nil {
			return proxyMutationBackendError("encode delete log", marshalErr)
		}
		return insertProxyMutationLog(ctx, q, params.TargetTenantID, row.ID, "delete_proxy", payload, audit)
	})
	return out, err
}

func (s *postgresMutationStore) SetStatus(
	ctx context.Context,
	params admindb.SetProxyStatusParams,
	audit MutationAudit,
) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, ErrStoreNotConfigured
	}
	payload, err := json.Marshal(map[string]any{"status": params.Status})
	if err != nil {
		return 0, proxyMutationBackendError("encode status log", err)
	}
	var affected int64
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		rows, updateErr := q.SetProxyStatus(ctx, params)
		if updateErr != nil {
			return updateErr
		}
		affected = rows
		if rows == 0 {
			return nil
		}
		return insertProxyMutationLog(ctx, q, params.TenantID, params.ID, "set_proxy_status", payload, audit)
	})
	return affected, err
}

func insertProxyMutationLog(
	ctx context.Context,
	q *admindb.Queries,
	tenantID int64,
	proxyID int64,
	action string,
	payload []byte,
	audit MutationAudit,
) error {
	requestID := strings.TrimSpace(audit.RequestID)
	reason := cleanAuditReason(audit.Reason)
	_, err := q.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID:   &tenantID,
		ActorID:    strings.TrimSpace(audit.ActorID),
		ActorRole:  strings.TrimSpace(audit.ActorRole),
		Action:     action,
		TargetType: proxyAuditTarget,
		TargetID:   &proxyID,
		RequestID:  optionalText(requestID),
		Reason:     reason,
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

func cleanAuditReason(reason *string) *string {
	if reason == nil {
		return nil
	}
	value := strings.TrimSpace(*reason)
	if value == "" {
		return nil
	}
	return &value
}

func proxyMutationBackendError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrBackend, operation, err)
}
