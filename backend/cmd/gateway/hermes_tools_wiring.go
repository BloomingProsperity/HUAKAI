package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
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

// hermesToolDeps 打包了 WAVE H3 诊断工具所封装的、已存在的只读存储。
// 每个字段都是已在 buildGatewayRuntime 中构造好的 store/service;本接线
// 只是把它们已有的读方法适配成 hermesops 工具依赖的形状——
// 不新增任何查询逻辑。
type hermesToolDeps struct {
	pool           *pgxpool.Pool
	adminQueries   *admindb.Queries
	billingQueries *dbbilling.Queries
	credentialStr  *credentialstore.Store
	channelHealth  *channelhealth.Service
	dlqStore       *dlq.Store
	// dlqService 暴露 WAVE H4 dlq_replay 工具所封装的、已存在的 Replay 变更操作
	//(重新 claim + 重新投递,以幂等键标识)。Nil => 该工具在依赖检查处
	// fail closed。
	dlqService *dlq.Service
	// modelRegistry 暴露 model_resolve_diagnose 工具所封装的、已存在的只读 ResolveModel
	//(alias -> canonical/pool-binding 路由)。Nil => 该工具 fail closed。
	modelRegistry *registry.PostgresRegistry
}

// buildHermesToolRegistry 组装只读诊断工具的 registry,做法是把每个工具的 Run
// 接到对应的、已存在的读函数上。每个工具都是只读的;不引用任何变更方法。
// 返回 registry + 工具调用的审计 inserter(由 pool 支撑)。pool 为 nil 时,
// 返回的 registry 中工具仍会注册,但在依赖检查处 fail closed。
//
// mutateOpts 是附加式的 S2 orchestrator 守卫(并发上限 + tx 超时)。
// 不传任何选项时,orchestrator 在字节层面等同于旧版的无界行为。
func buildHermesToolRegistry(d hermesToolDeps, mutateOpts ...hermesops.MutateOption) (*hermesops.Registry, *hermestoolsdb.Queries, *hermesops.MutateOrchestrator) {
	reg := hermesops.NewRegistry()

	// credential_diagnose -> credentialworker.DryRunProviderAccountCredential
	//(非持久化的校验)+ credentialstore.Store.ListRenewStatus(读)。
	credDeps := hermesops.CredentialDiagnoseDeps{
		DryRun:   credentialworker.DryRunProviderAccountCredential,
		Registry: credentialworker.DefaultModeAdapterRegistry(),
	}
	if d.credentialStr != nil {
		credDeps.TestStore = d.credentialStr
		credDeps.RenewStatus = d.credentialStr.ListRenewStatus
	}
	reg.Register(hermesops.CredentialDiagnoseSpec(credDeps))

	// account_health_diagnose -> admindb.GetAdminProviderAccountHealth(读)+
	// channelhealth.Service.SummarizeChannelHealth(聚合读)+
	// channelhealth.Service.ListChannelHealth(本账号逐通道明细,只读 Record 无 payload)。
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

	// alert_rule_list -> alerting.PostgresStore.ListRules(按 tenant_id SELECT-only)。就地用 d.pool 构造
	// 只读 store(同 routes_alerting.go 的 NewPostgresStore 用法;读路径无需 deliverer/scheduler)。
	// 0157 迁移已把 alert_rule_list 加进 hermes_tool_calls.tool_name CHECK。
	alertDeps := hermesops.AlertRuleListDeps{}
	if d.pool != nil {
		alertDeps.List = alerting.NewPostgresStore(d.pool).ListRules
	}
	reg.Register(hermesops.AlertRuleListSpec(alertDeps))

	// alert_event_list -> alerting.PostgresStore.ListEvents(按 tenant_id SELECT-only,可按 state 过滤)。
	// 0158 迁移已把 alert_event_list 加进 hermes_tool_calls.tool_name CHECK。Dimensions 同 Filters 来源。
	alertEvtDeps := hermesops.AlertEventListDeps{}
	if d.pool != nil {
		alertEvtDeps.List = alerting.NewPostgresStore(d.pool).ListEvents
	}
	reg.Register(hermesops.AlertEventListSpec(alertEvtDeps))

	// provider_catalog_list / channel_catalog_list -> admindb.Queries 的按租户目录读(SELECT-only,
	// SQL 含 tenant_id 过滤 + deleted_at IS NULL)。0159 迁移已把两名加进 hermes_tool_calls.tool_name CHECK。
	provCatDeps := hermesops.ProviderCatalogListDeps{}
	chanCatDeps := hermesops.ChannelCatalogListDeps{}
	if d.adminQueries != nil {
		provCatDeps.List = d.adminQueries.ListAdminProvidersByTenant
		chanCatDeps.List = d.adminQueries.ListAdminChannelsByTenant
	}
	reg.Register(hermesops.ProviderCatalogListSpec(provCatDeps))
	reg.Register(hermesops.ChannelCatalogListSpec(chanCatDeps))

	// request_diagnose / audit_lookup / log_analyze -> billingQueries 上
	// F-OBS-001 的仅 SELECT 管理读。
	obsDeps := hermesops.ObservabilityDeps{}
	if d.billingQueries != nil {
		obsDeps.ListUsage = d.billingQueries.ListUsageRecords
		obsDeps.ListClaims = d.billingQueries.ListBillingClaims
		obsDeps.ListAudit = d.billingQueries.ListAuditEvents
	}
	reg.Register(hermesops.RequestDiagnoseSpec(obsDeps))
	reg.Register(hermesops.AuditLookupSpec(obsDeps))
	reg.Register(hermesops.LogAnalyzeSpec(obsDeps))

	// dlq_inspect -> dlq.Store.List(只读;不引用 Replay)。
	dlqDeps := hermesops.DLQInspectDeps{}
	if d.dlqStore != nil {
		dlqDeps.List = d.dlqStore.List
	}
	reg.Register(hermesops.DLQInspectSpec(dlqDeps))

	// ---- WAVE H4 MUTATING 工具 ----------------------------------------------
	// 每个工具都把一个已存在的变更操作封装在 5 层安全契约之后。它们以
	// Mutating=true 注册,因此只读 dispatch 会拒绝它们;它们只通过
	// confirm 把关的 mutate 路径 + orchestrator 运行。

	// account_pause / account_resume -> UpdateProviderAccountEnabled(由
	// orchestrator 绑定到 tx)。Resolve 通过 GetAdminProviderAccount 读取。
	// channelhealth 的手动覆盖协调需要一个凭证维度的
	// ChannelKey(vendor + credential id + version),而单凭账号行
	// 并不携带它,因此本波次中 Coordinate 留空(nil):enabled=false 才是
	// dispatcher 的事实来源,它生效暂停;从账号 id 出发的 channel-health
	// 协调是一项有记录在案的后续事项。
	accountDeps := hermesops.AccountMutationDeps{}
	if d.adminQueries != nil {
		accountDeps.GetAccount = d.adminQueries.GetAdminProviderAccount
	}
	reg.Register(hermesops.AccountPauseSpec(accountDeps))
	reg.Register(hermesops.AccountResumeSpec(accountDeps))

	// dlq_replay -> dlq.Service.Replay(仅 platform_admin)。Lookup 通过一次
	// 直接的、限定在租户内的按 id 读,在该租户内找到目标记录,从而
	// Resolve 即便对那些早于任何有界 List 窗口的记录,也能预览 +
	// 重新校验租户归属。
	dlqReplayDeps := hermesops.DLQReplayDeps{}
	if d.dlqStore != nil {
		dlqReplayDeps.Lookup = dlqLookupByID(d.dlqStore.GetByID)
	}
	if d.dlqService != nil {
		dlqReplayDeps.Replay = d.dlqService.Replay
	}
	reg.Register(hermesops.DLQReplaySpec(dlqReplayDeps))

	// renew_trigger -> credentialstore.Store.Rotate(原子地取代上一版本)。
	// Resolve 通过 ListByAccount 读取凭证元数据。
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

// dlqLookupByID 把限定在租户内的、按 id 的 dlq 读适配成 hermesops
// 的 Lookup 形状(参数顺序为 id、tenantID)。它是 dlq_replay 预览/确认
// 路径中的只读目标解析。对该租户而言不存在的记录
//(包括错误租户的 id)会把 store 的 ErrNotFound 映射为
// hermesops.ErrTargetResolution,从而让 HTTP 层返回 404 而非 5xx。
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
