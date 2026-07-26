package adminpoolhttp

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/accountquota"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountadvanced"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountsubscription"
	"github.com/BloomingProsperity/HUAKAI/internal/quotawindowview"
)

func providerAccountDTO(row admindb.AdminProviderAccountRow) (providerAccountResponse, error) {
	return providerAccountDTOAt(row, time.Now().UTC())
}

func providerAccountDTOAt(row admindb.AdminProviderAccountRow, now time.Time) (providerAccountResponse, error) {
	subscription, systemLabels := accountsubscription.Build(row)
	quotaFacts, err := accountquota.ParseView(row.QuotaFacts, now)
	if err != nil {
		return providerAccountResponse{}, err
	}
	todayStats := providerAccountTodayStatsDTO(row)
	return providerAccountResponse{
		ID: row.ID, TenantID: row.TenantID, ProviderID: row.ProviderID, ChannelID: row.ChannelID,
		Name: row.Name, AccountType: row.AccountType, Enabled: row.Enabled, ExpiresAt: pgTimePtr(row.ExpiresAt),
		HealthState: row.HealthState, CredentialState: row.CredentialState,
		CapConcurrency: row.CapConcurrency, InFlightCount: row.InFlightCount, Priority: row.Priority,
		StaticWeight: row.StaticWeight, UpstreamCostRatio: row.UpstreamCostRatio,
		ProbeModel: row.ProbeModel, Tags: nonNilStringSlice(row.Tags),
		Subscription: subscription, SystemLabels: systemLabels,
		Extra:          jsonObjectOrEmpty(row.Extra),
		LastDispatchAt: pgTimePtr(row.LastDispatchAt), LastProbeLatencyMS: row.LastProbeLatencyMS,
		LastProbeAt:           pgTimePtr(row.LastProbeAt),
		LastRequestObservedAt: pgTimePtr(row.LastRequestObservedAt),
		ObservationSource:     "request_completion_event",
		TodayStats:            todayStats,
		QuotaWindows: quotawindowview.FromPostgres(quotawindowview.PostgresSnapshot{
			ObservedAt: row.QuotaSnapshotObservedAt, Source: row.QuotaSnapshotSource,
			Outcome: row.QuotaSnapshotOutcome, ErrorClass: row.QuotaSnapshotErrorClass,
			FiveHourStart: row.SessionWindow5hStart, FiveHourEnd: row.SessionWindow5hEnd,
			FiveHourUtilization: row.SessionWindow5hUtilization,
			SevenDayStart:       row.SessionWindow7dStart, SevenDayEnd: row.SessionWindow7dEnd,
			SevenDayUtilization: row.SessionWindow7dUtilization,
		}, now),
		QuotaFacts:      quotaFacts,
		ModelAllowList:  nonNilStringSlice(row.ModelAllowList),
		CapabilityFlags: nonNilStringSlice(row.CapabilityFlags), RateLimitedAt: pgTimePtr(row.RateLimitedAt),
		RateLimitResetAt: pgTimePtr(row.RateLimitResetAt), RateLimitReason: row.RateLimitReason,
		OverloadUntil: pgTimePtr(row.OverloadUntil), TempUnschedulableUntil: pgTimePtr(row.TempUnschedulableUntil),
		TokenVersion: row.TokenVersion, LastRefreshAt: pgTimePtr(row.LastRefreshAt),
		LastRefreshOutcome: row.LastRefreshOutcome, OAuthEndpointHealth: row.OAuthEndpointHealth,
		RPMLimit: row.RPMLimit, TPMLimit: row.TPMLimit, WindowCostLimitCents: row.WindowCostLimitCents,
		MaxSessions: row.MaxSessions, DisableCooling: row.DisableCooling, RefreshLeadSeconds: row.RefreshLeadSeconds,
		TLSFingerprintRotate:    row.TLSFingerprintRotate,
		CustomErrorCodesEnabled: row.CustomErrorCodesEnabled, CustomErrorCodes: nonNilInt32Slice(row.CustomErrorCodes),
		PoolMode: row.PoolMode, TempUnschedulableEnabled: row.TempUnschedulableEnabled,
		TempUnschedulableRules: detailRulesOrNil(row.TempUnschedulableRules),
		ProxyBinding:           accountadvanced.BindingFromColumns(row.ProxyID, row.ProxyGroupID),
		CreatedAt:              pgTimePtr(row.CreatedAt), UpdatedAt: pgTimePtr(row.UpdatedAt),
	}, nil
}

func providerAccountTodayStatsDTO(row admindb.AdminProviderAccountRow) *providerAccountTodayStats {
	if !row.TodayStatsWindowStart.Valid || !row.TodayStatsObservedAt.Valid {
		return nil
	}
	failureRate := 0.0
	if row.TodayRequestCount > 0 {
		failureRate = float64(row.TodayFailureCount) / float64(row.TodayRequestCount) * 100
	}
	return &providerAccountTodayStats{
		WindowStart:        row.TodayStatsWindowStart.Time.UTC(),
		ObservedAt:         row.TodayStatsObservedAt.Time.UTC(),
		RequestCount:       row.TodayRequestCount,
		SuccessCount:       row.TodaySuccessCount,
		FailureCount:       row.TodayFailureCount,
		FailureRatePercent: failureRate,
		TTFTP95MS:          row.TodayTTFTP95MS,
	}
}

func providerAccountDetailDTO(row admindb.AdminProviderAccountRow) (providerAccountResponse, error) {
	response, err := providerAccountDTO(row)
	if err != nil {
		return providerAccountResponse{}, err
	}
	response.TempUnschedulableRules = jsonArrayOrEmpty(row.TempUnschedulableRules)
	return response, nil
}

// detailRulesOrNil 把空规则置 nil，让列表摘要省略详情字段。
func detailRulesOrNil(raw []byte) json.RawMessage {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "[]" || s == "null" {
		return nil
	}
	return json.RawMessage(raw)
}

func jsonArrayOrEmpty(raw []byte) json.RawMessage {
	var values []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil || values == nil {
		return json.RawMessage(`[]`)
	}
	return json.RawMessage(append([]byte(nil), raw...))
}

func pgTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

func nonNilStringSlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func nonNilInt32Slice(in []int32) []int32 {
	if in == nil {
		return []int32{}
	}
	return in
}

func cleanOptionalString(in *string) *string {
	if in == nil {
		return nil
	}
	out := strings.TrimSpace(*in)
	return &out
}

func cleanStringList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func jsonRawObject(raw json.RawMessage) bool {
	var obj map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &obj) == nil && obj != nil
}

func normalizedProviderAccountExtra(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

func jsonObjectOrEmpty(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(raw)
}
