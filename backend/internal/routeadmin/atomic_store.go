// HUAKAI · iKun

package routeadmin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

const routeRuleTargetType = "route_rule"

// CreateWithLog 把路由创建和分类操作日志放进同一事务。
func (s *PostgresStore) CreateWithLog(ctx context.Context, in CreateInput, log MutationLog) (Route, error) {
	if s == nil || s.pool == nil {
		return Route{}, ErrStoreNotConfigured
	}
	if err := validateMutationLog(log); err != nil {
		return Route{}, err
	}
	var out Route
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		txStore := &PostgresStore{db: tx}
		route, err := txStore.Create(ctx, in)
		if err != nil {
			return err
		}
		if err := appendRouteMutationLog(ctx, tx, route, log, "create_route_rule"); err != nil {
			return err
		}
		out = route
		return nil
	})
	if err != nil {
		return Route{}, err
	}
	return out, nil
}

// UpdateWithLog 把路由全量更新和分类操作日志放进同一事务。
func (s *PostgresStore) UpdateWithLog(ctx context.Context, in UpdateInput, log MutationLog) (Route, error) {
	if s == nil || s.pool == nil {
		return Route{}, ErrStoreNotConfigured
	}
	if err := validateMutationLog(log); err != nil {
		return Route{}, err
	}
	var out Route
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		txStore := &PostgresStore{db: tx}
		route, err := txStore.Update(ctx, in)
		if err != nil {
			return err
		}
		if err := appendRouteMutationLog(ctx, tx, route, log, "update_route_rule"); err != nil {
			return err
		}
		out = route
		return nil
	})
	if err != nil {
		return Route{}, err
	}
	return out, nil
}

// SetEnabledWithLog 把路由启停和分类操作日志放进同一事务。
func (s *PostgresStore) SetEnabledWithLog(
	ctx context.Context,
	tenantID, id int64,
	enabled bool,
	log MutationLog,
) (Route, error) {
	if s == nil || s.pool == nil {
		return Route{}, ErrStoreNotConfigured
	}
	if err := validateMutationLog(log); err != nil {
		return Route{}, err
	}
	var out Route
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		txStore := &PostgresStore{db: tx}
		route, err := txStore.SetEnabled(ctx, tenantID, id, enabled)
		if err != nil {
			return err
		}
		if err := appendRouteMutationLog(ctx, tx, route, log, "update_route_rule"); err != nil {
			return err
		}
		out = route
		return nil
	})
	if err != nil {
		return Route{}, err
	}
	return out, nil
}

// SoftDeleteWithLog 把路由软删和分类操作日志放进同一事务。
func (s *PostgresStore) SoftDeleteWithLog(
	ctx context.Context,
	tenantID, id int64,
	log MutationLog,
) (Route, error) {
	if s == nil || s.pool == nil {
		return Route{}, ErrStoreNotConfigured
	}
	if err := validateMutationLog(log); err != nil {
		return Route{}, err
	}
	var out Route
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		txStore := &PostgresStore{db: tx}
		route, err := txStore.SoftDelete(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if err := appendRouteMutationLog(ctx, tx, route, log, "delete_route_rule"); err != nil {
			return err
		}
		out = route
		return nil
	})
	if err != nil {
		return Route{}, err
	}
	return out, nil
}

func appendRouteMutationLog(
	ctx context.Context,
	tx pgx.Tx,
	route Route,
	log MutationLog,
	action string,
) error {
	payload, err := json.Marshal(map[string]any{
		"name":                route.Name,
		"user_group_match":    route.UserGroupMatch,
		"model_pattern_match": route.ModelPatternMatch,
		"pool_group_id":       route.PoolGroupID,
		"match_priority":      route.MatchPriority,
		"enabled":             route.Enabled,
	})
	if err != nil {
		return fmt.Errorf("routeadmin: encode operation log: %w", err)
	}
	var requestID *string
	if log.RequestID != "" {
		requestID = &log.RequestID
	}
	_, err = admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID:   &route.TenantID,
		ActorID:    log.ActorID,
		ActorRole:  log.ActorRole,
		Action:     action,
		TargetType: routeRuleTargetType,
		TargetID:   &route.ID,
		RequestID:  requestID,
		Payload:    payload,
	})
	if err != nil {
		return fmt.Errorf("routeadmin: append operation log: %w", err)
	}
	return nil
}

func validateMutationLog(log MutationLog) error {
	source, rawID, ok := strings.Cut(log.ActorID, ":")
	numericID, parseErr := strconv.ParseInt(rawID, 10, 64)
	if !ok || (source != "admin_token" && source != "admin_user") || parseErr != nil || numericID <= 0 {
		return fmt.Errorf("%w: operation actor is required", ErrInvalidInput)
	}
	if log.ActorRole != admin.RolePlatformAdmin && log.ActorRole != admin.RoleTenantOperator {
		return fmt.Errorf("%w: operation actor role is invalid", ErrInvalidInput)
	}
	return nil
}
