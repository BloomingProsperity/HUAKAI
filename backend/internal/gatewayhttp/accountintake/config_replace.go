package accountintake

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func normalizedExtra(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte(`{}`)
	}
	return append([]byte(nil), raw...)
}

func normalizedRules(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte(`[]`)
	}
	return append([]byte(nil), raw...)
}

func nullableTimestamp(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func replaceAccountConfiguration(ctx context.Context, tx pgx.Tx, plan PlanInput, in ExecuteInput, accountID int64, proxyID *int64) error {
	var updatedAt time.Time
	var providerID, channelID int64
	var accountType string
	if err := tx.QueryRow(ctx, `SELECT provider_id, channel_id, account_type, updated_at
FROM provider_accounts
WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL
FOR UPDATE`, accountID, plan.TenantID).Scan(&providerID, &channelID, &accountType, &updatedAt); err != nil {
		return err
	}
	if providerID != plan.Account.ProviderID || channelID != plan.Account.ChannelID || accountType != plan.Account.AccountType {
		return ErrExecutionStale
	}
	if in.ExpectedAccountUpdatedAt == nil || !updatedAt.Equal(in.ExpectedAccountUpdatedAt.UTC()) {
		return ErrExecutionStale
	}
	_, err := admindb.New(tx).UpdateAdminProviderAccount(ctx, admindb.UpdateAdminProviderAccountParams{
		Enabled: plan.Account.Enabled, Priority: plan.Account.Priority,
		CapConcurrency: plan.Account.CapConcurrency, StaticWeight: plan.Account.StaticWeight,
		SetUpstreamCostRatio: true, UpstreamCostRatio: plan.Account.UpstreamCostRatio,
		RPMLimit: plan.Account.RPMLimit, TPMLimit: plan.Account.TPMLimit,
		WindowCostLimitCents: plan.Account.WindowCostLimitCents, MaxSessions: plan.Account.MaxSessions,
		DisableCooling:        plan.Account.DisableCooling,
		SetRefreshLeadSeconds: true, RefreshLeadSeconds: plan.Account.RefreshLeadSeconds,
		SetExpiresAt: true, ExpiresAt: nullableTimestamp(plan.Account.ExpiresAt),
		TLSFingerprintRotate: plan.Account.TLSFingerprintRotate,
		SetProbeModel:        true, ProbeModel: plan.Account.ProbeModel,
		SetTags: true, Tags: plan.Account.Tags,
		SetExtra: true, Extra: normalizedExtra(plan.Account.Extra),
		SetModelAllowList: true, ModelAllowList: plan.Account.ModelAllowList,
		SetCapabilityFlags: true, CapabilityFlags: plan.Account.CapabilityFlags,
		CustomErrorCodesEnabled: plan.Account.CustomErrorCodesEnabled,
		SetCustomErrorCodes:     true, CustomErrorCodes: plan.Account.CustomErrorCodes,
		PoolMode:                  plan.Account.PoolMode,
		TempUnschedulableEnabled:  plan.Account.TempUnschedulableEnabled,
		SetTempUnschedulableRules: true, TempUnschedulableRulesJSON: normalizedRules(plan.Account.TempUnschedulableRules),
		SetProxyID: true, ProxyID: proxyID,
		ActorID: optionalString(strings.TrimSpace(in.ActorID)), ID: accountID, TenantID: plan.TenantID,
	})
	return err
}
