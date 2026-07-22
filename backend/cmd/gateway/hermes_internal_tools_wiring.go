package main

import (
	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeschat"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesconfirm"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
	"go.uber.org/zap"
)

// buildHermesMCPHandler 构造官方 Hermes 使用的唯一工具入口。工具目录、参数校验、角色、租户、
// 提议、日志都由 HUAKAI 注册表和短时内部令牌决定。
func buildHermesMCPHandler(secret []byte, reg *hermesops.Registry, inserter *hermestoolsdb.Queries, enabled bool, confirmStore hermesconfirm.Store, proposalEnabled bool) *hermeschat.MCPHandler {
	if reg == nil || len(secret) == 0 {
		return nil
	}
	var calls hermesops.ToolCallInserter
	if inserter != nil {
		calls = inserter
	}
	return hermeschat.NewMCPHandler(secret, reg, calls, confirmStore, nil, enabled, proposalEnabled)
}

func effectiveHermesProposeEnabled(mutatingEnabled, proposeEnabled bool, logger *zap.Logger) bool {
	if !proposeEnabled {
		return false
	}
	if mutatingEnabled {
		return true
	}
	if logger != nil {
		logger.Warn("Hermes 提议能力已关闭，因为改动型工具总开关未启用",
			zap.String("propose_knob", hermesLLMProposeEnabledEnv+"=true"),
			zap.String("mutating_knob", hermesMutatingEnabledEnv+"=false"))
	}
	return false
}
