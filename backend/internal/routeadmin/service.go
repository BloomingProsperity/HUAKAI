// HUAKAI · iKun

package routeadmin

import (
	"context"
	"strings"
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
	r, err := s.store.Create(ctx, in)
	if err != nil {
		return Route{}, err
	}
	if s.audit != nil {
		s.audit.RouteCreated(ctx, r, in.AdminID)
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
	if s == nil || s.store == nil {
		return Route{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || id <= 0 {
		return Route{}, ErrInvalidInput
	}
	r, err := s.store.SoftDelete(ctx, tenantID, id)
	if err != nil {
		return Route{}, err
	}
	if s.audit != nil {
		s.audit.RouteDeleted(ctx, r, adminID)
	}
	return r, nil
}
