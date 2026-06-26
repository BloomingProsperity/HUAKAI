package hermesops

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// AccountHealthDeps are the read-only dependencies the account_health_diagnose
// tool wraps. All are EXISTING SELECT-only reads:
//   - ProviderAccountHealth wraps admindb.Queries.GetAdminProviderAccountHealth.
//   - ChannelSummary wraps ChannelHealthController.SummarizeChannelHealth (aggregate).
//   - ChannelList wraps ChannelHealthController.ListChannelHealth (per-channel records).
//
// 关于 per-channel 明细的隐私:**有意只用 ListChannelHealth(返回结构化 Record),不碰
// GetChannelHealth**——后者额外返回 AuditEvent 列表、其 Payload 是自由 map 有泄露风险。再由
// channelHealthShape **safe-by-construction 显式投影**:只露 enum/时间戳/ids/计数,**不露任何自由
// 文本字段**(Record 的 ManualPauseReason/RecoveryBlockedReason 等 operator 自填无约束文本一律不投影)。
// 且只折叠**本账号**(account_id 匹配 ProviderAccountID)的通道,故隐私安全 + 账号聚焦。
type AccountHealthDeps struct {
	ProviderAccountHealth func(ctx context.Context, params admindb.GetAdminProviderAccountHealthParams) (admindb.GetAdminProviderAccountHealthRow, error)
	ChannelSummary        func(ctx context.Context, tenantID int64) (channelhealth.ChannelHealthSummary, error)
	// ChannelList 是可选的逐通道读;nil 时本工具退化为只返聚合 summary(向后兼容)。
	ChannelList func(ctx context.Context, tenantID int64, limit, offset int) ([]channelhealth.Record, error)
}

// AccountHealthDiagnoseSpec builds the read-only account_health_diagnose tool.
// It reads one provider account's health row (scoped to the tenant) and the
// tenant's channel-health summary, returning enums / counts / latency buckets
// only — never raw bodies or secrets.
//
// Args: { "account_id": <int> }  (required)
func AccountHealthDiagnoseSpec(deps AccountHealthDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolAccountHealthDiagnose,
		Category:     CategoryDiagnostic,
		Description:  "Read a provider account's health state + the tenant's channel-health summary (states, counts, latency).",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema:  map[string]string{"account_id": "provider account id (positive integer, required)"},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.ProviderAccountHealth == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			accountID, err := ArgInt(req.Args, "account_id")
			if err != nil {
				return ToolResult{}, err
			}

			row, err := deps.ProviderAccountHealth(ctx, admindb.GetAdminProviderAccountHealthParams{
				TenantID: req.TenantID,
				ID:       accountID,
			})
			if err != nil {
				return ToolResult{}, err
			}

			summary := map[string]any{
				"account_id":               row.ID,
				"health_state":             row.HealthState,
				"health_state_until":       tsAny(row.HealthStateUntil),
				"enabled":                  row.Enabled,
				"last_probe_latency_ms":    int32PtrAny(row.LastProbeLatencyMS),
				"session_window_5h_start":  tsAny(row.SessionWindow5hStart),
				"session_window_5h_end":    tsAny(row.SessionWindow5hEnd),
				"session_window_5h_status": deref(row.SessionWindow5hStatus),
				"last_refresh_outcome":     deref(row.LastRefreshOutcome),
				"failure_class":            deref(row.FailureClass),
				"failure_count":            row.FailureCount,
				"last_probe_at":            tsAny(row.LastProbeAt),
				"last_refresh_at":          tsAny(row.LastRefreshAt),
			}

			errorClass := ""
			if row.FailureClass != nil && *row.FailureClass != "" {
				errorClass = *row.FailureClass
			}

			// Fold in the tenant channel-health summary (aggregate states +
			// counts only) when wired. Not fatal if absent.
			if deps.ChannelSummary != nil {
				cs, cerr := deps.ChannelSummary(ctx, req.TenantID)
				if cerr != nil {
					summary["channel_summary_error"] = "channel_summary_read_failed"
				} else {
					summary["channel_summary"] = channelSummaryShape(cs)
				}
			}

			// Fold in THIS account's per-channel detail(按 account_id 过滤)when ChannelList
			// wired — 给出聚合 summary 缺的通道级"为什么"(cooling/disabled/paused + reason)。
			// 用 ListChannelHealth(不含 AuditEvent payload),channelHealthShape 安全投影。
			if deps.ChannelList != nil {
				rows, cerr := deps.ChannelList(ctx, req.TenantID, channelHealthListLimit, 0)
				if cerr != nil {
					summary["channels_error"] = "channel_list_read_failed"
				} else {
					chans := make([]map[string]any, 0)
					for _, r := range rows {
						if r.Key.ProviderAccountID == accountID {
							chans = append(chans, channelHealthShape(r))
						}
					}
					summary["channels"] = chans
				}
			}

			return ToolResult{Summary: summary, ErrorClass: errorClass}, nil
		},
	}
}

// channelSummaryShape projects the aggregate channel-health summary into a
// diagnostic-only map: per-state counts + total + oldest-cooldown timestamp.
func channelSummaryShape(cs channelhealth.ChannelHealthSummary) map[string]any {
	byState := make(map[string]int64, len(cs.ByState))
	for state, count := range cs.ByState {
		byState[string(state)] = count
	}
	out := map[string]any{
		"by_state": byState,
		"total":    cs.Total,
	}
	if cs.OldestCooldownAt != nil {
		out["oldest_cooldown_at"] = cs.OldestCooldownAt.UTC()
	}
	return out
}

func int32PtrAny(v *int32) any {
	if v == nil {
		return nil
	}
	return *v
}

func tsAny(ts pgtype.Timestamptz) any {
	if !ts.Valid {
		return nil
	}
	return ts.Time.UTC()
}

// channelHealthListLimit 是逐通道折叠单次读取上限(账号通道少,够用且防超大读)。
const channelHealthListLimit = 100

// channelHealthShape 把一条 channelhealth.Record 投影成操作诊断字段。**显式列举 + safe-by-construction**:
// 只露 enum/时间戳/ids/计数,绝不 echo 整个 struct,也**绝不露任何自由文本字段**——Record 的
// ManualPauseReason/RecoveryBlockedReason 等是 operator 自填的无 schema 约束文本,可能夹带敏感信息,
// 故**有意不投影**(诊断信号由 state/reason_class/last_signal_class 枚举已充分传达)。防未来给 Record
// 新增字段时自动泄露,加字段必须是受控枚举/数值才显式列入。
func channelHealthShape(r channelhealth.Record) map[string]any {
	return map[string]any{
		"channel_id":          r.Key.ChannelID,
		"vendor":              r.Key.Vendor,
		"provider_account_id": r.Key.ProviderAccountID,
		"credential_version":  r.Key.CredentialVersion,
		"state":               string(r.State),
		"reason_class":        string(r.ReasonClass),
		"score":               r.Score,
		"cooldown_until":      tsPtr(r.CooldownUntil),
		"ramp_stage_pct":      r.RampStagePct,
		"last_signal_class":   string(r.LastSignalClass),
		"last_signal_at":      tsPtr(r.LastSignalAt),
		"ramp_failure_count":  r.RampFailureCount,
		"policy_version":      r.PolicyVersion,
		"last_transition_at":  r.LastTransitionAt.UTC(),
	}
}

// tsPtr 把可空时间戳投影为 UTC time 或 nil(*time.Time 版,区别于收 pgtype 的 tsAny)。
func tsPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}
