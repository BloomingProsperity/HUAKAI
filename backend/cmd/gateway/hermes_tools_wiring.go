package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quotaadmin"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
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
	// modelRegistry exposes the EXISTING read-only ResolveModel the model_resolve_diagnose
	// tool wraps (alias -> canonical/pool-binding routing). Nil => the tool fails closed.
	modelRegistry *registry.PostgresRegistry
}

// buildHermesToolRegistry assembles the read-only diagnostic-tool registry by
// wiring each tool's Run to the corresponding EXISTING read function. Every tool
// is read-only; no mutating method is referenced. Returns the registry + the
// tool-call audit inserter (pool-backed). A nil pool yields a registry with the
// tools still registered but failing closed on dependency checks.
//
// mutateOpts are the additive S2 orchestrator guards (concurrency cap + tx
// deadline). With no options the orchestrator is byte-for-byte the legacy
// unbounded behavior.
func buildHermesToolRegistry(d hermesToolDeps, mutateOpts ...hermesops.MutateOption) (*hermesops.Registry, *hermestoolsdb.Queries, *hermesops.MutateOrchestrator) {
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
	// channelhealth.Service.SummarizeChannelHealth (aggregate read) +
	// channelhealth.Service.ListChannelHealth (本账号逐通道明细,只读 Record 无 payload)。
	healthDeps := hermesops.AccountHealthDeps{}
	if d.adminQueries != nil {
		healthDeps.ProviderAccountHealth = d.adminQueries.GetAdminProviderAccountHealth
	}
	if d.channelHealth != nil {
		healthDeps.ChannelSummary = d.channelHealth.SummarizeChannelHealth
		healthDeps.ChannelList = d.channelHealth.ListChannelHealth
	}
	reg.Register(hermesops.AccountHealthDiagnoseSpec(healthDeps))

	// channel_health_list -> channelhealth.Service.ListChannelHealth(整租户逐通道明细,只读 Record 无 payload)。
	// 0152 迁移已把 channel_health_list 加进 hermes_tool_calls.tool_name CHECK。
	chListDeps := hermesops.ChannelHealthListDeps{}
	if d.channelHealth != nil {
		chListDeps.List = d.channelHealth.ListChannelHealth
	}
	reg.Register(hermesops.ChannelHealthListSpec(chListDeps))

	// model_resolve_diagnose -> registry.PostgresRegistry.ResolveModel(只读解析,
	// REPEATABLE READ + read-only TX,不写任何状态)。0153 迁移已把 model_resolve_diagnose 加进
	// hermes_tool_calls.tool_name CHECK。投影 safe-by-construction,绝不露 binding 上的
	// SystemPrompt/SensitiveWords/ParamOverride 等自由文本(见 hermesops.modelResolveShape)。
	mrDeps := hermesops.ModelResolveDiagnoseDeps{}
	if d.modelRegistry != nil {
		mrDeps.Resolve = d.modelRegistry.ResolveModel
	}
	reg.Register(hermesops.ModelResolveDiagnoseSpec(mrDeps))

	// pool_list -> dbbilling.Queries.ListPools(按租户 SELECT-only,SQL 含 deleted_at IS NULL 只返活跃池)。
	// 0154 迁移已把 pool_list 加进 hermes_tool_calls.tool_name CHECK。PoolGroup 全结构化配置无自由文本/PII,
	// poolShape 仍显式列举投影。
	poolDeps := hermesops.PoolListDeps{}
	if d.billingQueries != nil {
		poolDeps.List = d.billingQueries.ListPools
	}
	reg.Register(hermesops.PoolListSpec(poolDeps))

	// provider_account_list -> admindb.Queries.ListAdminProviderAccounts(按 tenant_id SELECT-only,
	// SQL 含 deleted_at IS NULL 只返活跃账号)。0155 迁移已把 provider_account_list 加进
	// hermes_tool_calls.tool_name CHECK。providerAccountShape 投影绝不露 Extra/RateLimitReason/Tags 值/
	// ProxyGroupID,且本行根本不含凭证 token 明文。
	paDeps := hermesops.ProviderAccountListDeps{}
	if d.adminQueries != nil {
		paDeps.List = d.adminQueries.ListAdminProviderAccounts
	}
	reg.Register(hermesops.ProviderAccountListSpec(paDeps))

	// quota_policy_list -> dbquota.Queries.ListQuotaPoliciesForAdmin(按 tenant_id SELECT-only)。
	// 就地用 d.pool 构造 dbquota 查询器(同 routes.go 的 NewQuotaPolicyStoreAdapter,无状态、读路径)。
	// 0156 迁移已把 quota_policy_list 加进 hermes_tool_calls.tool_name CHECK。
	quotaDeps := hermesops.QuotaPolicyListDeps{}
	if d.pool != nil {
		quotaDeps.List = dbquota.New(d.pool).ListQuotaPoliciesForAdmin
	}
	reg.Register(hermesops.QuotaPolicyListSpec(quotaDeps))

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
	// target record within the tenant by a direct tenant-scoped by-id read, so
	// Resolve can preview + re-check tenant ownership even for records older than
	// any bounded List window.
	dlqReplayDeps := hermesops.DLQReplayDeps{}
	if d.dlqStore != nil {
		dlqReplayDeps.Lookup = dlqLookupByID(d.dlqStore.GetByID)
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
		mutator = hermesops.NewMutateOrchestrator(d.pool, mutateOpts...)
	}
	return reg, inserter, mutator
}

// dlqLookupByID adapts the tenant-scoped dlq by-id read into the hermesops
// Lookup shape (id, tenantID order). It is read-only target resolution for the
// dlq_replay preview/confirm path. A record that does not exist for the tenant
// (including a wrong-tenant id) maps the store's ErrNotFound to
// hermesops.ErrTargetResolution so the HTTP layer returns 404 rather than 5xx.
func dlqLookupByID(getByID func(ctx context.Context, tenantID, id int64) (dlq.Record, error)) func(ctx context.Context, id, tenantID int64) (dlq.Record, error) {
	return func(ctx context.Context, id, tenantID int64) (dlq.Record, error) {
		rec, err := getByID(ctx, tenantID, id)
		if errors.Is(err, dlq.ErrNotFound) {
			return dlq.Record{}, hermesops.ErrTargetResolution
		}
		if err != nil {
			return dlq.Record{}, err
		}
		return rec, nil
	}
}
