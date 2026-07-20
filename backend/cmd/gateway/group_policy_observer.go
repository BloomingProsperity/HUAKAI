// HUAKAI · iKun

package main

import (
	"context"
	"expvar"

	"go.uber.org/zap"

	poolrouter "github.com/BloomingProsperity/HUAKAI/internal/pool/router"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionenforce"
)

// groupPolicyFailClosedTotal 统计策略真相不可用而停止选号的次数。
var groupPolicyFailClosedTotal = expvar.NewInt("group_policy_fail_closed_total")

// newGroupPolicyFailClosedObserver 构造策略真相不可用的 metric 与 WARN 钩子。
func newGroupPolicyFailClosedObserver(logger *zap.Logger) subscriptionenforce.FailClosedObserver {
	return func(_ context.Context, req poolrouter.SelectionRequest, err error) {
		groupPolicyFailClosedTotal.Add(1)
		if logger != nil {
			logger.Warn("订阅分组路由策略不可用，已停止选号",
				zap.Int64("tenant_id", req.TenantID),
				zap.String("user_group", req.UserGroup),
				zap.String("model", req.RequestedModel),
				zap.Error(err),
			)
		}
	}
}
