package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

// hermesToolDeps bundles the EXISTING read-only stores the WAVE H3 diagnostic
// tools wrap. Each field is a store/service already constructed in
// buildGatewayRuntime; this wiring only adapts their existing read methods into
// the hermesops tool dependency shape — it adds no new query logic.
type hermesToolDeps struct {
	pool           *pgxpool.Pool
	adminQueries   *admindb.Queries
	billingQueries *dbbilling.Queries
	credentialStr  *credentialstore.Store
	channelHealth  *channelhealth.Service
	dlqStore       *dlq.Store
}

// buildHermesToolRegistry assembles the read-only diagnostic-tool registry by
// wiring each tool's Run to the corresponding EXISTING read function. Every tool
// is read-only; no mutating method is referenced. Returns the registry + the
// tool-call audit inserter (pool-backed). A nil pool yields a registry with the
// tools still registered but failing closed on dependency checks.
func buildHermesToolRegistry(d hermesToolDeps) (*hermesops.Registry, *hermestoolsdb.Queries) {
	reg := hermesops.NewRegistry()

	// credential_diagnose -> credentialworker.DryRunProviderAccountCredential
	// (non-persistent validation) + credentialstore.Store.ListRenewStatus (read).
	credDeps := hermesops.CredentialDiagnoseDeps{
		DryRun:   credentialworker.DryRunProviderAccountCredential,
		Registry: credentialworker.DefaultModeAdapterRegistry(),
	}
	if d.credentialStr != nil {
		credDeps.TestStore = d.credentialStr
		credDeps.RenewStatus = d.credentialStr.ListRenewStatus
	}
	reg.Register(hermesops.CredentialDiagnoseSpec(credDeps))

	// account_health_diagnose -> admindb.GetAdminProviderAccountHealth (read) +
	// channelhealth.Service.SummarizeChannelHealth (aggregate read).
	healthDeps := hermesops.AccountHealthDeps{}
	if d.adminQueries != nil {
		healthDeps.ProviderAccountHealth = d.adminQueries.GetAdminProviderAccountHealth
	}
	if d.channelHealth != nil {
		healthDeps.ChannelSummary = d.channelHealth.SummarizeChannelHealth
	}
	reg.Register(hermesops.AccountHealthDiagnoseSpec(healthDeps))

	// request_diagnose / audit_lookup / log_analyze -> the F-OBS-001 SELECT-only
	// admin reads on billingQueries.
	obsDeps := hermesops.ObservabilityDeps{}
	if d.billingQueries != nil {
		obsDeps.ListUsage = d.billingQueries.ListUsageRecords
		obsDeps.ListClaims = d.billingQueries.ListBillingClaims
		obsDeps.ListAudit = d.billingQueries.ListAuditEvents
	}
	reg.Register(hermesops.RequestDiagnoseSpec(obsDeps))
	reg.Register(hermesops.AuditLookupSpec(obsDeps))
	reg.Register(hermesops.LogAnalyzeSpec(obsDeps))

	// dlq_inspect -> dlq.Store.List (read only; Replay is NOT referenced).
	dlqDeps := hermesops.DLQInspectDeps{}
	if d.dlqStore != nil {
		dlqDeps.List = d.dlqStore.List
	}
	reg.Register(hermesops.DLQInspectSpec(dlqDeps))

	var inserter *hermestoolsdb.Queries
	if d.pool != nil {
		inserter = hermestoolsdb.New(d.pool)
	}
	return reg, inserter
}
