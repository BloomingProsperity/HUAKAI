package hermesops

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// AccountHealthDeps are the read-only dependencies the account_health_diagnose
// tool wraps. Both are EXISTING SELECT-only reads:
//   - ProviderAccountHealth wraps admindb.Queries.GetAdminProviderAccountHealth.
//   - ChannelSummary wraps ChannelHealthController.SummarizeChannelHealth.
//
// The per-channel GetChannelHealth read returns an AuditEvent list whose Payload
// is a free-form map; rather than thread it through and risk leaking, this tool
// uses the AGGREGATE Summarize read (states + counts only) as its channel view,
// which is privacy-safe by construction.
type AccountHealthDeps struct {
	ProviderAccountHealth func(ctx context.Context, params admindb.GetAdminProviderAccountHealthParams) (admindb.GetAdminProviderAccountHealthRow, error)
	ChannelSummary        func(ctx context.Context, tenantID int64) (channelhealth.ChannelHealthSummary, error)
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
				"enabled":                  row.Enabled,
				"last_probe_latency_ms":    int32PtrAny(row.LastProbeLatencyMS),
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
