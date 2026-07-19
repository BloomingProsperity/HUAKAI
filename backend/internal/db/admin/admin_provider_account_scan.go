package admin

type adminProviderAccountScanner interface {
	Scan(dest ...any) error
}

func scanAdminProviderAccount(row adminProviderAccountScanner, i *AdminProviderAccountRow) error {
	return row.Scan(adminProviderAccountDestinations(i)...)
}

func scanAdminProviderAccountWithSubscription(row adminProviderAccountScanner, i *AdminProviderAccountRow) error {
	destinations := adminProviderAccountDestinations(i)
	destinations = append(destinations,
		&i.SubscriptionVendor,
		&i.SubscriptionPlan,
		&i.SubscriptionRawPlan,
		&i.SubscriptionScope,
		&i.SubscriptionSubjectRef,
		&i.SubscriptionWorkspaceRef,
		&i.SubscriptionSource,
		&i.SubscriptionTrust,
		&i.SubscriptionVerification,
		&i.SubscriptionStatus,
		&i.SubscriptionMappingVersion,
		&i.SubscriptionErrorClass,
		&i.SubscriptionFirstObservedAt,
		&i.SubscriptionObservedAt,
		&i.SubscriptionChangedAt,
		&i.QuotaFacts,
	)
	return row.Scan(destinations...)
}

func adminProviderAccountDestinations(i *AdminProviderAccountRow) []any {
	return []any{
		&i.ID,
		&i.TenantID,
		&i.ProviderID,
		&i.ChannelID,
		&i.Name,
		&i.AccountType,
		&i.Enabled,
		&i.ExpiresAt,
		&i.RPMLimit,
		&i.TPMLimit,
		&i.WindowCostLimitCents,
		&i.MaxSessions,
		&i.DisableCooling,
		&i.RefreshLeadSeconds,
		&i.TLSFingerprintRotate,
		&i.HealthState,
		&i.CredentialState,
		&i.CapConcurrency,
		&i.InFlightCount,
		&i.Priority,
		&i.StaticWeight,
		&i.UpstreamCostRatio,
		&i.ProbeModel,
		&i.Tags,
		&i.Extra,
		&i.LastDispatchAt,
		&i.LastProbeLatencyMS,
		&i.LastProbeAt,
		&i.LastRequestObservedAt,
		&i.QuotaSnapshotObservedAt,
		&i.QuotaSnapshotSource,
		&i.QuotaSnapshotOutcome,
		&i.QuotaSnapshotErrorClass,
		&i.SessionWindow5hStart,
		&i.SessionWindow5hEnd,
		&i.SessionWindow5hStatus,
		&i.SessionWindow5hUtilization,
		&i.SessionWindow7dStart,
		&i.SessionWindow7dEnd,
		&i.SessionWindow7dStatus,
		&i.SessionWindow7dUtilization,
		&i.ModelAllowList,
		&i.CapabilityFlags,
		&i.RateLimitedAt,
		&i.RateLimitResetAt,
		&i.RateLimitReason,
		&i.OverloadUntil,
		&i.TempUnschedulableUntil,
		&i.TokenVersion,
		&i.LastRefreshAt,
		&i.LastRefreshOutcome,
		&i.OAuthEndpointHealth,
		&i.CustomErrorCodesEnabled,
		&i.CustomErrorCodes,
		&i.PoolMode,
		&i.TempUnschedulableEnabled,
		&i.TempUnschedulableRules,
		&i.ProxyID,
		&i.ProxyGroupID,
		&i.CreatedAt,
		&i.UpdatedAt,
	}
}
