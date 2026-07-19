package admin

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// InsertProviderAccountParams 是管理账号创建链使用的稳定参数合同。
type InsertProviderAccountParams struct {
	TenantID                   int64
	ProviderID                 int64
	ChannelID                  int64
	Name                       string
	AccountType                string
	Enabled                    *bool
	ExpiresAt                  pgtype.Timestamptz
	Credentials                []byte
	CapConcurrency             *int32
	CapQueueSticky             *int32
	CapQueueFallback           *int32
	Priority                   *int32
	StaticWeight               *int32
	UpstreamCostRatio          *float64
	ProbeModel                 *string
	Tags                       []string
	Extra                      []byte
	ModelAllowList             []string
	CapabilityFlags            []string
	RPMLimit                   *int64
	TPMLimit                   *int64
	WindowCostLimitCents       *int64
	MaxSessions                *int32
	DisableCooling             *bool
	RefreshLeadSeconds         *int32
	TLSFingerprintRotate       *bool
	CustomErrorCodesEnabled    *bool
	CustomErrorCodes           []int32
	PoolMode                   *bool
	TempUnschedulableEnabled   *bool
	TempUnschedulableRulesJSON []byte
	ProxyID                    *int64
	ProxyGroupID               *string
	ActorID                    *string
}

func (q *Queries) InsertProviderAccount(ctx context.Context, arg InsertProviderAccountParams) (int64, error) {
	return q.InsertProviderAccountRaw(ctx, InsertProviderAccountRawParams{
		TenantID: arg.TenantID, ProviderID: arg.ProviderID, ChannelID: arg.ChannelID,
		Name: arg.Name, AccountType: arg.AccountType, Enabled: arg.Enabled, ExpiresAt: arg.ExpiresAt,
		Credentials: arg.Credentials, CapConcurrency: arg.CapConcurrency, CapQueueSticky: arg.CapQueueSticky,
		CapQueueFallback: arg.CapQueueFallback, Priority: arg.Priority, StaticWeight: arg.StaticWeight,
		UpstreamCostRatio: arg.UpstreamCostRatio, ProbeModel: arg.ProbeModel, Tags: arg.Tags, Extra: arg.Extra,
		ModelAllowList: arg.ModelAllowList, CapabilityFlags: arg.CapabilityFlags, RpmLimit: arg.RPMLimit,
		TpmLimit: arg.TPMLimit, WindowCostLimitCents: arg.WindowCostLimitCents, MaxSessions: arg.MaxSessions,
		DisableCooling: arg.DisableCooling, RefreshLeadSeconds: arg.RefreshLeadSeconds,
		TlsFingerprintRotate: arg.TLSFingerprintRotate, CustomErrorCodesEnabled: arg.CustomErrorCodesEnabled,
		CustomErrorCodes: arg.CustomErrorCodes, PoolMode: arg.PoolMode,
		TempUnschedulableEnabled: arg.TempUnschedulableEnabled,
		TempUnschedulableRules:   arg.TempUnschedulableRulesJSON, ProxyID: arg.ProxyID,
		ProxyGroupID: arg.ProxyGroupID, ActorID: arg.ActorID,
	})
}

// UpdateAdminProviderAccountParams 是管理账号更新链使用的稳定参数合同。
type UpdateAdminProviderAccountParams struct {
	Enabled                    *bool
	Priority                   *int32
	CapConcurrency             *int32
	StaticWeight               *int32
	SetUpstreamCostRatio       bool
	UpstreamCostRatio          *float64
	RPMLimit                   *int64
	TPMLimit                   *int64
	WindowCostLimitCents       *int64
	MaxSessions                *int32
	DisableCooling             *bool
	SetRefreshLeadSeconds      bool
	RefreshLeadSeconds         *int32
	SetExpiresAt               bool
	ExpiresAt                  pgtype.Timestamptz
	TLSFingerprintRotate       *bool
	SetProbeModel              bool
	ProbeModel                 *string
	SetTags                    bool
	Tags                       []string
	SetExtra                   bool
	Extra                      []byte
	SetModelAllowList          bool
	ModelAllowList             []string
	SetCapabilityFlags         bool
	CapabilityFlags            []string
	CustomErrorCodesEnabled    *bool
	SetCustomErrorCodes        bool
	CustomErrorCodes           []int32
	PoolMode                   *bool
	TempUnschedulableEnabled   *bool
	SetTempUnschedulableRules  bool
	TempUnschedulableRulesJSON []byte
	SetProxyID                 bool
	ProxyID                    *int64
	SetProxyGroupID            bool
	ProxyGroupID               *string
	ActorID                    *string
	ID                         int64
	TenantID                   int64
}

func (q *Queries) UpdateAdminProviderAccount(ctx context.Context, arg UpdateAdminProviderAccountParams) (AdminProviderAccountRow, error) {
	_, err := q.UpdateAdminProviderAccountRaw(ctx, UpdateAdminProviderAccountRawParams{
		Enabled: arg.Enabled, Priority: arg.Priority, CapConcurrency: arg.CapConcurrency,
		StaticWeight: arg.StaticWeight, SetUpstreamCostRatio: arg.SetUpstreamCostRatio,
		UpstreamCostRatio: arg.UpstreamCostRatio, RpmLimit: arg.RPMLimit, TpmLimit: arg.TPMLimit,
		WindowCostLimitCents: arg.WindowCostLimitCents, MaxSessions: arg.MaxSessions,
		DisableCooling: arg.DisableCooling, SetRefreshLeadSeconds: arg.SetRefreshLeadSeconds,
		RefreshLeadSeconds: arg.RefreshLeadSeconds, SetExpiresAt: arg.SetExpiresAt, ExpiresAt: arg.ExpiresAt,
		TlsFingerprintRotate: arg.TLSFingerprintRotate, SetProbeModel: arg.SetProbeModel,
		ProbeModel: arg.ProbeModel, SetTags: arg.SetTags, Tags: arg.Tags, SetExtra: arg.SetExtra,
		Extra: arg.Extra, SetModelAllowList: arg.SetModelAllowList, ModelAllowList: arg.ModelAllowList,
		SetCapabilityFlags: arg.SetCapabilityFlags, CapabilityFlags: arg.CapabilityFlags,
		CustomErrorCodesEnabled: arg.CustomErrorCodesEnabled, SetCustomErrorCodes: arg.SetCustomErrorCodes,
		CustomErrorCodes: arg.CustomErrorCodes, PoolMode: arg.PoolMode,
		TempUnschedulableEnabled:  arg.TempUnschedulableEnabled,
		SetTempUnschedulableRules: arg.SetTempUnschedulableRules,
		TempUnschedulableRules:    arg.TempUnschedulableRulesJSON, SetProxyID: arg.SetProxyID,
		ProxyID: arg.ProxyID, SetProxyGroupID: arg.SetProxyGroupID, ProxyGroupID: arg.ProxyGroupID,
		ActorID: arg.ActorID, ID: arg.ID, TenantID: arg.TenantID,
	})
	if err != nil {
		return AdminProviderAccountRow{}, err
	}
	return q.GetAdminProviderAccount(ctx, GetAdminProviderAccountParams{ID: arg.ID, TenantID: arg.TenantID})
}
