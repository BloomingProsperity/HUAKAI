// HUAKAI · iKun

package main

import (
	"context"
	"expvar"

	"go.uber.org/zap"

	poolrouter "github.com/BloomingProsperity/HUAKAI/internal/pool/router"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionenforce"
)

// groupPolicyFailOpenTotal 暴露订阅分组路由 gate 因 routes 查询失败而 fail-open 放行的累计
// 次数, 经 expvar /debug/vars 暴露给运维。R4: 持续 fail-open = routes DB 异常, 此时被限档
// 用户会越档放行, 该计数须可被告警监控 (仅 debug 日志不足以告警)。
var groupPolicyFailOpenTotal = expvar.NewInt("group_policy_fail_open_total")

// newGroupPolicyFailOpenObserver 构造 fail-open 观测钩子: 累计 metric + 打 WARN 日志
// (含 tenant/user_group/model/err, 便于定位是哪个租户档位的 routes 查询在抖动)。
func newGroupPolicyFailOpenObserver(logger *zap.Logger) subscriptionenforce.FailOpenObserver {
	return func(_ context.Context, req poolrouter.SelectionRequest, err error) {
		groupPolicyFailOpenTotal.Add(1)
		if logger != nil {
			logger.Warn("订阅分组路由 gate fail-open 放行 (routes 查询失败)",
				zap.Int64("tenant_id", req.TenantID),
				zap.String("user_group", req.UserGroup),
				zap.String("model", req.RequestedModel),
				zap.Error(err),
			)
		}
	}
}
