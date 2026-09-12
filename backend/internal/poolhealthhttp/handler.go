// Package poolhealthhttp 提供租户作用域的按账号池健康投影只读接口:
// GET /admin/v1/pools/health-summary。
//
// 投影按池给出账号的调度健康分栏(schedulable / degraded / cooling_down / unavailable)、占比、
// 最早恢复时刻与池级状态,全部在服务端一次算完;不倒行给浏览器求和,不探测上游,不改任何状态。
// 分栏只纳入与请求无关的账号级谓词与两层健康门(选号候选查询的账号/渠道/厂商/凭据谓词、
// 进程内健康 FSM 门、auth 降级车道),按模型/协议/能力/限流/额度/容量的请求维门不在投影内;
// 因此 schedulable 表示"健康层放行",不表示某个具体请求一定会选中它。
package poolhealthhttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminhttpcore"
	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// Auth 从请求派生部署者或租户管理员身份;鉴权失败一律拒,防跨租户读。
type Auth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// Store 是投影需要的三条只读查询:租户存在性门、按池聚合、未挂池账号计数。
type Store interface {
	GetPoolHealthTenant(context.Context, int64) (int64, error)
	SummarizeProviderAccountHealthByPool(context.Context, int64) ([]admindb.SummarizeProviderAccountHealthByPoolRow, error)
	CountUnpooledProviderAccounts(context.Context, int64) (int64, error)
}

// AuthCooldownReader 暴露 auth 降级车道的只读快照;nil 表示车道未接线(不叠加)。
type AuthCooldownReader interface {
	Snapshot(int64, time.Time) authcooldown.Snapshot
}

// Deps 是接口依赖。Store 为空表示网关未配置数据库真相,接口 fail-closed 返回 503。
type Deps struct {
	Auth         Auth
	Store        Store
	AuthCooldown AuthCooldownReader
	Now          func() time.Time
}

// PoolHealth 是单个池的投影行。四个计数互斥且相加等于 total_accounts;
// 响应只含池标识与计数/占比,不含账号 id、凭据元数据或上游地址。
type PoolHealth struct {
	PoolGroupID          int64   `json:"pool_group_id"`
	PoolName             string  `json:"pool_name"`
	PoolEnabled          bool    `json:"pool_enabled"`
	Status               string  `json:"status"`
	TotalAccounts        int64   `json:"total_accounts"`
	SchedulableAccounts  int64   `json:"schedulable_accounts"`
	DegradedAccounts     int64   `json:"degraded_accounts"`
	CoolingDownAccounts  int64   `json:"cooling_down_accounts"`
	UnavailableAccounts  int64   `json:"unavailable_accounts"`
	AuthCooldownAccounts int64   `json:"auth_cooldown_accounts"`
	SchedulableRatio     string  `json:"schedulable_ratio"`
	DegradedRatio        string  `json:"degraded_ratio"`
	CoolingDownRatio     string  `json:"cooling_down_ratio"`
	UnavailableRatio     string  `json:"unavailable_ratio"`
	EarliestRecoveryAt   *string `json:"earliest_recovery_at"`
}

// PoolHealthTotals 是跨池合计,供与租户级健康汇总对账:
// totals.total_accounts + unpooled_accounts == /provider-accounts/health-summary 的 total。
type PoolHealthTotals struct {
	Pools                int64 `json:"pools"`
	TotalAccounts        int64 `json:"total_accounts"`
	SchedulableAccounts  int64 `json:"schedulable_accounts"`
	DegradedAccounts     int64 `json:"degraded_accounts"`
	CoolingDownAccounts  int64 `json:"cooling_down_accounts"`
	UnavailableAccounts  int64 `json:"unavailable_accounts"`
	AuthCooldownAccounts int64 `json:"auth_cooldown_accounts"`
}

// Response 是接口响应体。
type Response struct {
	TenantID         int64            `json:"tenant_id"`
	GeneratedAt      string           `json:"generated_at"`
	Pools            []PoolHealth     `json:"pools"`
	Totals           PoolHealthTotals `json:"totals"`
	UnpooledAccounts int64            `json:"unpooled_accounts"`
}

// 池级状态:人工停用优先于子单元事实;全部子单元都不可调度才抬升为整体不可用。
const (
	PoolStatusDisabled    = "disabled"
	PoolStatusEmpty       = "empty"
	PoolStatusUnavailable = "unavailable"
	PoolStatusDegraded    = "degraded"
	PoolStatusHealthy     = "healthy"
)

// NewHandler 构造 GET /admin/v1/pools/health-summary。
func NewHandler(d Deps) http.HandlerFunc {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil || d.Auth == nil {
			adminhttpcore.WriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "pool health projection is not configured")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			adminhttpcore.WriteTenantScopeAuthError(w, err)
			return
		}
		tenantID, ok := adminhttpcore.ResolveTenantScopeQuery(w, r, ident)
		if !ok {
			return
		}
		ctx := r.Context()
		if _, err := d.Store.GetPoolHealthTenant(ctx, tenantID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				adminhttpcore.WriteJSONError(w, http.StatusNotFound, "tenant_not_found", "tenant does not exist")
				return
			}
			writeQueryError(w)
			return
		}
		rows, err := d.Store.SummarizeProviderAccountHealthByPool(ctx, tenantID)
		if err != nil {
			writeQueryError(w)
			return
		}
		unpooled, err := d.Store.CountUnpooledProviderAccounts(ctx, tenantID)
		if err != nil {
			writeQueryError(w)
			return
		}
		at := now().UTC()
		resp := Response{
			TenantID:         tenantID,
			GeneratedAt:      at.Format(time.RFC3339),
			Pools:            make([]PoolHealth, 0, len(rows)),
			UnpooledAccounts: unpooled,
		}
		for _, row := range rows {
			pool := projectPool(row, d.AuthCooldown, at)
			resp.Pools = append(resp.Pools, pool)
			resp.Totals.Pools++
			resp.Totals.TotalAccounts += pool.TotalAccounts
			resp.Totals.SchedulableAccounts += pool.SchedulableAccounts
			resp.Totals.DegradedAccounts += pool.DegradedAccounts
			resp.Totals.CoolingDownAccounts += pool.CoolingDownAccounts
			resp.Totals.UnavailableAccounts += pool.UnavailableAccounts
			resp.Totals.AuthCooldownAccounts += pool.AuthCooldownAccounts
		}
		adminhttpcore.WriteJSON(w, http.StatusOK, resp)
	}
}

func writeQueryError(w http.ResponseWriter) {
	// 真相不可用就明确 503,不返回空投影冒充"全健康"。
	adminhttpcore.WriteJSONError(w, http.StatusServiceUnavailable, "pool_health_query_failed", "pool health projection is temporarily unavailable")
}

// projectPool 把数据库层分栏与进程内 auth 降级车道叠加成最终投影,判定顺序与选号门一致:
//   - 数据库层 schedulable/degraded 的账号:auth 硬禁 → unavailable;auth 临时冷却且未被
//     disable_cooling 豁免 → cooling_down,恢复时刻取 AuthUntil;
//   - 数据库层已在 cooling_down 的账号:auth 硬禁 → 抬升为 unavailable(需人工恢复,不能再显示为到点自愈),
//     并从恢复时刻里剔除;auth 临时冷却 → 仍是 cooling_down,恢复时刻取两层截止的较晚者;
//   - earliest_recovery_at 是叠加后所有 cooling_down 账号已知恢复时刻的最小值,未知不参与。
//
// 车道未接线(lane==nil)时,投影就是数据库层的分栏。
func projectPool(row admindb.SummarizeProviderAccountHealthByPoolRow, lane AuthCooldownReader, now time.Time) PoolHealth {
	pool := PoolHealth{
		PoolGroupID:         row.PoolGroupID,
		PoolName:            row.PoolName,
		PoolEnabled:         row.PoolEnabled,
		TotalAccounts:       row.TotalAccounts,
		SchedulableAccounts: row.SchedulableAccounts,
		DegradedAccounts:    row.DegradedAccounts,
		CoolingDownAccounts: row.CoolingDownAccounts,
		UnavailableAccounts: row.UnavailableAccounts,
	}
	var recovery *time.Time
	noteRecovery := func(t *time.Time) {
		if t == nil {
			return
		}
		if recovery == nil || t.Before(*recovery) {
			u := t.UTC()
			recovery = &u
		}
	}
	snapshot := func(id int64) authcooldown.Snapshot {
		if lane == nil {
			return authcooldown.Snapshot{Eligible: true}
		}
		return lane.Snapshot(id, now)
	}
	exempt := make(map[int64]struct{}, len(row.CoolingExemptIds))
	for _, id := range row.CoolingExemptIds {
		exempt[id] = struct{}{}
	}
	// 数据库层放行的账号只可能被 auth 车道往下降。
	overlayEligible := func(ids []int64, from *int64) {
		for _, id := range ids {
			snap := snapshot(id)
			switch {
			case snap.HardDisabled:
				*from--
				pool.UnavailableAccounts++
				pool.AuthCooldownAccounts++
			case !snap.Eligible:
				if _, ok := exempt[id]; ok {
					continue
				}
				*from--
				pool.CoolingDownAccounts++
				pool.AuthCooldownAccounts++
				noteRecovery(snap.AuthUntil)
			}
		}
	}
	overlayEligible(row.SchedulableIds, &pool.SchedulableAccounts)
	overlayEligible(row.DegradedIds, &pool.DegradedAccounts)
	// 数据库层已冷却的账号:硬禁抬升为 unavailable,否则按两层截止的较晚者登记恢复时刻。
	for i, id := range row.CoolingIds {
		snap := snapshot(id)
		if snap.HardDisabled {
			pool.CoolingDownAccounts--
			pool.UnavailableAccounts++
			pool.AuthCooldownAccounts++
			continue
		}
		var dbRecovery *time.Time
		if i < len(row.CoolingRecoveryAt) && row.CoolingRecoveryAt[i].Valid {
			t := row.CoolingRecoveryAt[i].Time
			dbRecovery = &t
		}
		if _, ok := exempt[id]; !snap.Eligible && !ok {
			pool.AuthCooldownAccounts++
			if snap.AuthUntil != nil && dbRecovery != nil && snap.AuthUntil.After(*dbRecovery) {
				dbRecovery = snap.AuthUntil
			}
		}
		noteRecovery(dbRecovery)
	}
	pool.SchedulableRatio = formatRatio(pool.SchedulableAccounts, pool.TotalAccounts)
	pool.DegradedRatio = formatRatio(pool.DegradedAccounts, pool.TotalAccounts)
	pool.CoolingDownRatio = formatRatio(pool.CoolingDownAccounts, pool.TotalAccounts)
	pool.UnavailableRatio = formatRatio(pool.UnavailableAccounts, pool.TotalAccounts)
	if recovery != nil && pool.CoolingDownAccounts > 0 {
		s := recovery.Format(time.RFC3339)
		pool.EarliestRecoveryAt = &s
	}
	pool.Status = poolStatus(pool)
	return pool
}

// poolStatus 推导池级状态:disabled(人工停用)> empty(无账号)> unavailable(无一可调度)
// > healthy(全部完全可调度)> degraded(其余,含部分冷却/不可用/降级)。
func poolStatus(p PoolHealth) string {
	switch {
	case !p.PoolEnabled:
		return PoolStatusDisabled
	case p.TotalAccounts == 0:
		return PoolStatusEmpty
	case p.SchedulableAccounts+p.DegradedAccounts == 0:
		return PoolStatusUnavailable
	case p.SchedulableAccounts == p.TotalAccounts:
		return PoolStatusHealthy
	default:
		return PoolStatusDegraded
	}
}

// formatRatio 输出四位小数字符串;分母为 0 时给 "0.0000",不产生 NaN。
func formatRatio(n, total int64) string {
	if total <= 0 {
		return "0.0000"
	}
	return strconv.FormatFloat(float64(n)/float64(total), 'f', 4, 64)
}
