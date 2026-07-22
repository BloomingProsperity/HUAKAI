package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	runtimeconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	dbmoderation "github.com/BloomingProsperity/HUAKAI/internal/db/moderation"
	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quotaadmin"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

// hermesToolDeps 汇集 Hermes 工具复用的现有存储和服务。
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
	// dlqService 暴露 dlq_replay 复用的 Replay 变更操作
	//(重新 claim + 重新投递,以幂等键标识)。Nil => 该工具在依赖检查处
	// fail closed。
	dlqService *dlq.Service
	// modelRegistry 暴露 model_resolve_diagnose 工具所封装的、已存在的只读 ResolveModel
	//(alias -> canonical/pool-binding 路由)。Nil => 该工具 fail closed。
	modelRegistry *registry.PostgresRegistry
	vendorOAuth   runtimeconfig.VendorOAuthConfigs
}

// buildHermesToolRegistry 把现有查询和管理写路径注册为唯一工具目录，并返回工具日志
// 写入器与改动编排器。依赖缺失时工具仍注册，但调用会失败关闭。
func buildHermesToolRegistry(d hermesToolDeps, mutateOpts ...hermesops.MutateOption) (*hermesops.Registry, *hermestoolsdb.Queries, *hermesops.MutateOrchestrator, error) {
	reg := hermesops.NewRegistry()

	// credential_diagnose -> credentialworker.DryRunProviderAccountCredential
	//（非持久化校验）+ credentialstore.Store.ListByAccount（精确账号读取）。
	credDeps := hermesops.CredentialDiagnoseDeps{
		DryRun:   credentialworker.DryRunProviderAccountCredential,
		Registry: credentialworker.DefaultModeAdapterRegistryWithRuntimeOAuth(d.vendorOAuth),
	}
	if d.credentialStr != nil {
		credDeps.TestStore = d.credentialStr
		credDeps.ListByAccount = d.credentialStr.ListByAccount
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
		healthDeps.ChannelListByAccount = d.channelHealth.ListChannelHealthByProviderAccount
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

	// 告警规则启停复用事务内写路径；Resolve 读取当前状态并复检租户归属。
	alertRuleMutDeps := hermesops.AlertRuleMutationDeps{}
	if d.pool != nil {
		alertRuleMutDeps.GetRule = alerting.NewPostgresStore(d.pool).GetRule
		alertRuleMutDeps.SetEnabledInTx = alerting.NewPostgresStore(d.pool).SetRuleEnabledInTx
	}
	reg.Register(hermesops.AlertRuleEnableSpec(alertRuleMutDeps))
	reg.Register(hermesops.AlertRuleDisableSpec(alertRuleMutDeps))

	// 内容审核关键词启停复用现有读取和事务内写路径，并在预览阶段复检租户归属。
	moderationKwMutDeps := hermesops.ModerationKeywordMutationDeps{}
	if d.pool != nil {
		moderationKwMutDeps.GetKeyword = func(ctx context.Context, tenantID, id int64) (moderation.KeywordRule, error) {
			row, err := dbmoderation.New(d.pool).GetModerationKeyword(ctx, dbmoderation.GetModerationKeywordParams{TenantID: tenantID, ID: id})
			if err != nil {
				return moderation.KeywordRule{}, err
			}
			// 把生成码的 Row 适配成 moderation.KeywordRule;Resolve 只用到 TenantID/Keyword/
			// ReasonCode/Enabled(时间戳 CreatedAt/UpdatedAt 预览不用,留零值即可)。
			return moderation.KeywordRule{
				ID:         row.ID,
				TenantID:   row.TenantID,
				Keyword:    row.Keyword,
				ReasonCode: row.ReasonCode,
				Enabled:    row.Enabled,
			}, nil
		}
		moderationKwMutDeps.SetEnabledInTx = func(ctx context.Context, tx pgx.Tx, tenantID, id int64, enabled bool) error {
			// 用 orchestrator 的 tx 构造查询器,使翻转与审计行在同一事务内原子提交;
			// 租户 scope 第三处绑死在 SetModerationKeywordEnabled 的 SQL WHERE tenant_id。
			_, err := dbmoderation.New(tx).SetModerationKeywordEnabled(ctx, dbmoderation.SetModerationKeywordEnabledParams{TenantID: tenantID, ID: id, Enabled: enabled})
			return err
		}
	}
	reg.Register(hermesops.ModerationKeywordEnableSpec(moderationKwMutDeps))
	reg.Register(hermesops.ModerationKeywordDisableSpec(moderationKwMutDeps))

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

	// 改动型工具复用现有管理操作，只能通过人工确认与编排器执行。

	// account_pause / account_resume 在编排器事务内修改账号 enabled 状态。
	// enabled 是调度器的权威来源，健康状态的人工覆盖由独立工具维护，避免跨事务假联动。
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
		renewDeps.RotateTx = func(ctx context.Context, tx pgx.Tx, in credentialstore.RotateCredentialInput) (credentialstore.CredentialMetadata, error) {
			return d.credentialStr.WithDB(tx).Rotate(ctx, in)
		}
	}
	reg.Register(hermesops.RenewTriggerSpec(renewDeps))

	var inserter *hermestoolsdb.Queries
	var mutator *hermesops.MutateOrchestrator
	if d.pool != nil {
		inserter = hermestoolsdb.New(d.pool)
		mutateOpts = append(mutateOpts, hermesops.WithMutationRecoveryJournal(hermesrecovery.NewStore(d.pool)))
		mutator = hermesops.NewMutateOrchestrator(d.pool, mutateOpts...)
	}
	if err := reg.Validate(); err != nil {
		return nil, nil, nil, fmt.Errorf("校验 Hermes 工具注册表：%w", err)
	}
	return reg, inserter, mutator, nil
}

// dlqLookupByID 把限定在租户内的、按 id 的 dlq 读适配成 hermesops
// 的 Lookup 形状(参数顺序为 id、tenantID)。它是 dlq_replay 预览/确认
// 路径中的只读目标解析。对该租户而言不存在的记录
// (包括错误租户的 id)会把 store 的 ErrNotFound 映射为
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
