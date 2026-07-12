package adminhttp

import (
	"encoding/json"
	"net/http"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// 账号池健康聚合(B9 运维巡检):GET /admin/v1/provider-accounts/health-summary。
// 跨整个租户池按 health_state 计数(非分页),供管理端一眼看清问题账号分布。
// 只读、不含钱字段;租户 scope 复用 provider_account_health 的解析(tenant_operator 限本租户)。

// providerAccountHealthSummaryResponse 是聚合响应。states 按健康态计数,
// disabled 单列(enabled=false 的账号数),needs_attention=非 operational 或被停用的账号数。
type providerAccountHealthSummaryResponse struct {
	Total          int64                             `json:"total"`
	Enabled        int64                             `json:"enabled"`
	Disabled       int64                             `json:"disabled"`
	NeedsAttention int64                             `json:"needs_attention"`
	States         []providerAccountHealthStateCount `json:"states"`
}

type providerAccountHealthStateCount struct {
	HealthState string `json:"health_state"`
	Count       int64  `json:"count"`
}

func newProviderAccountHealthSummaryHandler(d ProviderAccountHealthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, tenantID, ok := resolveProviderAccountHealthTenant(w, r, d)
		if !ok {
			return
		}
		rows, err := d.Store.SummarizeProviderAccountHealth(r.Context(), tenantID)
		if err != nil {
			writeProviderAccountHealthReadError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(buildHealthSummary(rows))
	}
}

// buildHealthSummary 把 (health_state, enabled) 明细聚合成总览。
// healthy 视为健康;其余健康态(throttled/cooldown/revoked)或被停用(enabled=false)计入 needs_attention。
// health_state 枚举以 provider_accounts CHECK 约束为准:healthy/throttled/revoked/cooldown。
func buildHealthSummary(rows []admindb.SummarizeProviderAccountHealthRow) providerAccountHealthSummaryResponse {
	byState := map[string]int64{}
	var total, enabled, disabled, needs int64
	for _, r := range rows {
		total += r.N
		byState[r.HealthState] += r.N
		if r.Enabled {
			enabled += r.N
		} else {
			disabled += r.N
		}
		// 非 healthy 或被停用都需要关注(停用的 healthy 也算)。
		if r.HealthState != "healthy" || !r.Enabled {
			needs += r.N
		}
	}
	states := make([]providerAccountHealthStateCount, 0, len(byState))
	// 固定顺序输出已知健康态,未知态追加在后(稳定、便于前端渲染)。
	for _, s := range []string{"healthy", "throttled", "cooldown", "revoked"} {
		if n, has := byState[s]; has {
			states = append(states, providerAccountHealthStateCount{HealthState: s, Count: n})
			delete(byState, s)
		}
	}
	for s, n := range byState {
		states = append(states, providerAccountHealthStateCount{HealthState: s, Count: n})
	}
	return providerAccountHealthSummaryResponse{
		Total: total, Enabled: enabled, Disabled: disabled, NeedsAttention: needs, States: states,
	}
}
