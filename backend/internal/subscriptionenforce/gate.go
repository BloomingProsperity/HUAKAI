// HUAKAI · iKun

// Package subscriptionenforce 实现订阅运行时强制的路由侧 gate (R-SUB-WIRE-1)。
// GroupPolicyGate 按 routes 表把调用者订阅档 (user_group) 限制到其允许的 pool_group;
// 与 pool 选择链路其余 gate 同形 (实现 poolrouter.Gate), 由 selector 装配后在选号时生效。
package subscriptionenforce

import (
	"context"
	"strings"

	poolrouter "github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

// RoutesRepo 提供按 (租户, 用户组) 读取启用路由规则的只读能力。
type RoutesRepo interface {
	// AllowedPoolGroups 返回该租户该用户组下、model 命中 model_pattern_match 的
	// 启用路由所指向的 pool_group_id 集合。无匹配规则时返回空集 (调用方据此放行)。
	AllowedPoolGroups(ctx context.Context, tenantID int64, userGroup, model string) (map[int64]struct{}, error)
}

// GroupPolicyGate 实现 poolrouter.Gate, 在 pool 选择时按订阅档限制可用 pool_group。
//
// 语义 (保守、向后兼容):
//   - user_group 空 → 放行 (无档 = 无限制, 老链路 / 未接线)。
//   - repo 未注入 → 放行 (休眠安全: 字段已加但未配 repo 时不改行为)。
//   - 该租户/档/模型无配置路由 (允许集为空) → 放行 (未启用分组路由, 直通)。
//   - 有配置 → 仅当候选所在 PoolGroupID 在允许集内才放行, 否则拒 (group_policy)。
//   - repo 出错 → fail-open 放行: 路由档位是 entitlement 而非钱/安全闸,
//     瞬时 DB 错不应拒掉付费用户的请求 (与配额闸 S3 的 fail-closed 区别对待)。
type GroupPolicyGate struct {
	repo RoutesRepo
}

// NewGroupPolicyGate 构造按 routes 限档的 gate。repo 为 nil 时 gate 恒放行。
func NewGroupPolicyGate(repo RoutesRepo) GroupPolicyGate {
	return GroupPolicyGate{repo: repo}
}

// Allow 实现 poolrouter.Gate。决策只依赖 SelectionRequest (账号参数不参与,
// 因为同一 Select 内所有候选账号共享同一 PoolGroupID, 判定对全池一致)。
func (g GroupPolicyGate) Allow(ctx context.Context, _ *poolrouter.AccountSnapshot, req poolrouter.SelectionRequest) (bool, poolrouter.GateFailureReason, error) {
	if req.UserGroup == "" || g.repo == nil {
		return true, "", nil
	}
	allowed, err := g.repo.AllowedPoolGroups(ctx, req.TenantID, req.UserGroup, req.RequestedModel)
	if err != nil {
		// fail-open: 路由档位非钱非安全闸; 瞬时 DB 错放行优于拒付费用户。
		return true, "", nil
	}
	if len(allowed) == 0 {
		return true, "", nil
	}
	if _, ok := allowed[req.PoolGroupID]; ok {
		return true, "", nil
	}
	return false, poolrouter.GateFailureGroupPolicy, nil
}

// ModelPatternMatches 判定 routes.model_pattern_match 是否命中具体 model。
// 支持: '*' 或空 = 全匹配; 'prefix*' = 前缀匹配; 否则精确相等。
func ModelPatternMatches(pattern, model string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(model, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == model
}
