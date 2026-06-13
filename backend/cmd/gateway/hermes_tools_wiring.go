package main

import (
	"context"

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
	// dlqService exposes the EXISTING Replay mutation (re-claim + re-deliver,
	// idempotency-keyed) the WAVE H4 dlq_replay tool wraps. Nil => the tool fails
	// closed on its dependency check.
	dlqService *dlq.Service
}

// buildHermesToolRegistry assembles the read-only diagnostic-tool registry by
// wiring each tool's Run to the corresponding EXISTING read function. Every tool
// is read-only; no mutating method is referenced. Returns the registry + the
// tool-call audit inserter (pool-backed). A nil pool yields a registry with the
// tools still registered but failing closed on dependency checks.
func buildHermesToolRegistry(d hermesToolDeps) (*hermesops.Registry, *hermestoolsdb.Queries, *hermesops.MutateOrchestrator) {
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

	// ---- WAVE H4 MUTATING tools ----------------------------------------------
	// Each wraps an EXISTING mutation behind the 5-layer safety contract. They are
	// registered with Mutating=true so the read-only dispatch refuses them; they
	// run only through the confirm-gated mutate path + the orchestrator.

	// account_pause / account_resume -> UpdateProviderAccountEnabled (tx-bound by
	// the orchestrator). Resolve reads via GetAdminProviderAccount. The
	// channelhealth manual-override coordination needs a credential-scoped
	// ChannelKey (vendor + credential id + version) that the account row alone
	// does not carry, so Coordinate is left nil this wave: enabled=false is the
	// dispatcher's source of truth and effects the pause; channel-health
	// coordination from the account id is a documented follow-up.
	accountDeps := hermesops.AccountMutationDeps{}
	if d.adminQueries != nil {
		accountDeps.GetAccount = d.adminQueries.GetAdminProviderAccount
	}
	reg.Register(hermesops.AccountPauseSpec(accountDeps))
	reg.Register(hermesops.AccountResumeSpec(accountDeps))

	// dlq_replay -> dlq.Service.Replay (platform_admin only). Lookup finds the
	// target record within the tenant from the existing List read (there is no
	// single-id read), so Resolve can preview + re-check tenant ownership.
	dlqReplayDeps := hermesops.DLQReplayDeps{}
	if d.dlqStore != nil {
		dlqReplayDeps.Lookup = dlqLookupByID(d.dlqStore.List)
	}
	if d.dlqService != nil {
		dlqReplayDeps.Replay = d.dlqService.Replay
	}
	reg.Register(hermesops.DLQReplaySpec(dlqReplayDeps))

	// renew_trigger -> credentialstore.Store.Rotate (atomically supersedes the
	// prior version). Resolve reads the credential metadata via ListByAccount.
	renewDeps := hermesops.RenewTriggerDeps{}
	if d.credentialStr != nil {
		renewDeps.ListByAccount = d.credentialStr.ListByAccount
		renewDeps.Rotate = d.credentialStr.Rotate
	}
	reg.Register(hermesops.RenewTriggerSpec(renewDeps))

	var inserter *hermestoolsdb.Queries
	var mutator *hermesops.MutateOrchestrator
	if d.pool != nil {
		inserter = hermestoolsdb.New(d.pool)
		mutator = hermesops.NewMutateOrchestrator(d.pool)
	}
	return reg, inserter, mutator
}

// dlqLookupByID adapts the tenant-scoped dlq List read into a single-record
// lookup by id (there is no by-id read in the store). It lists the tenant's
// records and returns the one matching id. The List read drops the raw payload
// shape we surface anyway (Resolve only previews kind/lane/status), so this is a
// read-only target resolution.
func dlqLookupByID(list func(ctx context.Context, f dlq.ListFilter) ([]dlq.Record, error)) func(ctx context.Context, id, tenantID int64) (dlq.Record, error) {
	return func(ctx context.Context, id, tenantID int64) (dlq.Record, error) {
		tenant := tenantID
		rows, err := list(ctx, dlq.ListFilter{TenantID: &tenant, Limit: 500})
		if err != nil {
			return dlq.Record{}, err
		}
		for i := range rows {
			if rows[i].ID == id {
				return rows[i], nil
			}
		}
		return dlq.Record{}, hermesops.ErrTargetResolution
	}
}
