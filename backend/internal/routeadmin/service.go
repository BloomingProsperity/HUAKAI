// HUAKAI · iKun

package routeadmin

import (
	"context"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// Service 是 routes 管理的外观: 入参规范化 + 校验 + 调 store + 审计。
type Service struct {
	store Store
	audit AuditSink // 可 nil
}

// NewService 构造管理服务。audit 为 nil 时不记审计。
func NewService(store Store, audit AuditSink) *Service {
	return &Service{store: store, audit: audit}
}

// Create 规范化并校验入参后创建一条 route。校验顺序保证非法输入绝不落库:
// 必填(tenant/name/user_group/pool_group)→ match_priority 非负 → model_pattern 形态。
func (s *Service) Create(ctx context.Context, in CreateInput) (Route, error) {
	if s == nil || s.store == nil {
		return Route{}, ErrStoreNotConfigured
	}
	in.Name = strings.TrimSpace(in.Name)
	in.UserGroupMatch = strings.TrimSpace(in.UserGroupMatch)
	in.ModelPatternMatch = strings.TrimSpace(in.ModelPatternMatch)
	if in.TenantID <= 0 || in.Name == "" || in.UserGroupMatch == "" || in.PoolGroupID <= 0 {
		return Route{}, ErrInvalidInput
	}
	if in.MatchPriority != nil && *in.MatchPriority < 0 {
		return Route{}, ErrInvalidInput
	}
	if err := ValidateModelPattern(in.ModelPatternMatch); err != nil {
		return Route{}, err
	}
	log := normalizeMutationLog(MutationLog{
		ActorID: in.ActorID, ActorRole: in.ActorRole, RequestID: in.RequestID, LegacyAdminID: in.AdminID,
	})
	var (
		r   Route
		err error
	)
	if atomic, ok := s.store.(AtomicStore); ok {
		r, err = atomic.CreateWithLog(ctx, in, log)
	} else {
		r, err = s.store.Create(ctx, in)
	}
	if err != nil {
		return Route{}, err
	}
	if _, atomic := s.store.(AtomicStore); !atomic && s.audit != nil {
		s.audit.RouteCreated(ctx, r, in.AdminID)
	}
	return r, nil
}

// Update 规范化并校验入参后全替换一条 route 的可编辑字段(PUT 语义)。
// 校验顺序与 Create 一致, 保证非法输入绝不落库: 必填(tenant/id/name/user_group/pool_group)
// → match_priority 非负 → model_pattern 形态。adminID 仅用于审计归属。
func (s *Service) Update(ctx context.Context, in UpdateInput) (Route, error) {
	if s == nil || s.store == nil {
		return Route{}, ErrStoreNotConfigured
	}
	in.Name = strings.TrimSpace(in.Name)
	in.UserGroupMatch = strings.TrimSpace(in.UserGroupMatch)
	in.ModelPatternMatch = strings.TrimSpace(in.ModelPatternMatch)
	if in.TenantID <= 0 || in.ID <= 0 || in.Name == "" || in.UserGroupMatch == "" || in.PoolGroupID <= 0 {
		return Route{}, ErrInvalidInput
	}
	if in.MatchPriority != nil && *in.MatchPriority < 0 {
		return Route{}, ErrInvalidInput
	}
	if err := ValidateModelPattern(in.ModelPatternMatch); err != nil {
		return Route{}, err
	}
	log := normalizeMutationLog(MutationLog{
		ActorID: in.ActorID, ActorRole: in.ActorRole, RequestID: in.RequestID, LegacyAdminID: in.AdminID,
	})
	var (
		r   Route
		err error
	)
	if atomic, ok := s.store.(AtomicStore); ok {
		r, err = atomic.UpdateWithLog(ctx, in, log)
	} else {
		r, err = s.store.Update(ctx, in)
	}
	if err != nil {
		return Route{}, err
	}
	if _, atomic := s.store.(AtomicStore); !atomic && s.audit != nil {
		s.audit.RouteUpdated(ctx, r, in.AdminID)
	}
	return r, nil
}

// SetEnabled 翻转一条 route 的 enabled 闸(独立窄动作, 非 PUT 全替换)。adminID 仅用于审计归属。
// 停用即把该路由从分组路由生效集移除(热路径 gate 过滤 enabled=true), 但不软删 —— 可后续再启用。
// 审计复用 RouteUpdated: 传入的是 store 返回的更新后快照, 其 Enabled 字段已反映新值。
func (s *Service) SetEnabled(ctx context.Context, tenantID, id int64, enabled bool, adminID int64) (Route, error) {
	return s.SetEnabledWithActor(ctx, tenantID, id, enabled, MutationLog{LegacyAdminID: adminID})
}

// SetEnabledWithActor 使用可追责的会话或令牌身份执行启停；PostgreSQL 下变更与日志同事务。
func (s *Service) SetEnabledWithActor(ctx context.Context, tenantID, id int64, enabled bool, log MutationLog) (Route, error) {
	if s == nil || s.store == nil {
		return Route{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || id <= 0 {
		return Route{}, ErrInvalidInput
	}
	log = normalizeMutationLog(log)
	var (
		r   Route
		err error
	)
	if atomic, ok := s.store.(AtomicStore); ok {
		r, err = atomic.SetEnabledWithLog(ctx, tenantID, id, enabled, log)
	} else {
		r, err = s.store.SetEnabled(ctx, tenantID, id, enabled)
	}
	if err != nil {
		return Route{}, err
	}
	if _, atomic := s.store.(AtomicStore); !atomic && s.audit != nil {
		s.audit.RouteUpdated(ctx, r, log.LegacyAdminID)
	}
	return r, nil
}

// List 返回该租户未软删的全部 route。
func (s *Service) List(ctx context.Context, tenantID int64) ([]Route, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if tenantID <= 0 {
		return nil, ErrInvalidInput
	}
	return s.store.List(ctx, tenantID)
}

// Get 取该租户下单条 route(不存在返 ErrRouteNotFound)。
func (s *Service) Get(ctx context.Context, tenantID, id int64) (Route, error) {
	if s == nil || s.store == nil {
		return Route{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || id <= 0 {
		return Route{}, ErrInvalidInput
	}
	return s.store.Get(ctx, tenantID, id)
}

// Delete 软删一条 route(从分组路由生效集移除); adminID 仅用于审计归属。
func (s *Service) Delete(ctx context.Context, tenantID, id, adminID int64) (Route, error) {
	return s.DeleteWithActor(ctx, tenantID, id, MutationLog{LegacyAdminID: adminID})
}

// DeleteWithActor 使用可追责身份软删路由；PostgreSQL 下删除与日志同事务。
func (s *Service) DeleteWithActor(ctx context.Context, tenantID, id int64, log MutationLog) (Route, error) {
	if s == nil || s.store == nil {
		return Route{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || id <= 0 {
		return Route{}, ErrInvalidInput
	}
	log = normalizeMutationLog(log)
	var (
		r   Route
		err error
	)
	if atomic, ok := s.store.(AtomicStore); ok {
		r, err = atomic.SoftDeleteWithLog(ctx, tenantID, id, log)
	} else {
		r, err = s.store.SoftDelete(ctx, tenantID, id)
	}
	if err != nil {
		return Route{}, err
	}
	if _, atomic := s.store.(AtomicStore); !atomic && s.audit != nil {
		s.audit.RouteDeleted(ctx, r, log.LegacyAdminID)
	}
	return r, nil
}

func normalizeMutationLog(log MutationLog) MutationLog {
	log.ActorID = strings.TrimSpace(log.ActorID)
	log.ActorRole = strings.TrimSpace(log.ActorRole)
	log.RequestID = strings.TrimSpace(log.RequestID)
	if log.ActorID == "" && log.LegacyAdminID > 0 {
		log.ActorID = "admin_token:" + strconv.FormatInt(log.LegacyAdminID, 10)
	}
	if log.ActorRole == "" && log.LegacyAdminID > 0 {
		log.ActorRole = admin.RolePlatformAdmin
	}
	return log
}
