// HUAKAI · iKun

package main

import (
	"context"
	"expvar"

	"go.uber.org/zap"

	poolrouter "github.com/BloomingProsperity/HUAKAI/internal/pool/router"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionenforce"
)

// groupPolicyFailOpenTotal 暴露订阅分组路由 gate 因 routes repo 不可用 / 查询失败而
// fail-open 放行的累计次数, 经 expvar /debug/vars 暴露给运维。持续增长表示 routes DB 异常。
var groupPolicyFailOpenTotal = expvar.NewInt("group_policy_fail_open_total")

// groupPolicyFailClosedTotal 保留给明确硬拒路径的累计次数。transient repo 问题应走
// fail-open metric, 避免把库抖动扩大成付费用户误拒。
var groupPolicyFailClosedTotal = expvar.NewInt("group_policy_fail_closed_total")

// newGroupPolicyFailOpenObserver 构造 fail-open 观测钩子: 累计 metric + 打 WARN 日志。
func newGroupPolicyFailOpenObserver(logger *zap.Logger) subscriptionenforce.FailOpenObserver {
	return func(_ context.Context, req poolrouter.SelectionRequest, err error) {
		groupPolicyFailOpenTotal.Add(1)
		if logger != nil {
			logger.Warn("订阅分组路由 gate fail-open 放行 (routes repo 不可用或查询失败)",
				zap.Int64("tenant_id", req.TenantID),
				zap.String("user_group", req.UserGroup),
				zap.String("model", req.RequestedModel),
				zap.Error(err),
			)
		}
	}
}

// newGroupPolicyFailClosedObserver 构造明确硬拒观测钩子: 累计 metric + WARN。
func newGroupPolicyFailClosedObserver(logger *zap.Logger) subscriptionenforce.FailClosedObserver {
	return func(_ context.Context, req poolrouter.SelectionRequest, err error) {
		groupPolicyFailClosedTotal.Add(1)
		if logger != nil {
			logger.Warn("订阅分组路由 gate fail-closed 拒绝 (明确硬拒)",
				zap.Int64("tenant_id", req.TenantID),
				zap.String("user_group", req.UserGroup),
				zap.String("model", req.RequestedModel),
				zap.Error(err),
			)
		}
	}
}
