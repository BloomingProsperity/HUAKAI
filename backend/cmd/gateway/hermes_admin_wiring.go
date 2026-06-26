package main

import (
	"context"

	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesadmin"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// hermesAdminDeps 打包 WAVE H5 每日巡检复用的「现有」只读 store + email sender。
// 其每个字段都是已在 buildGatewayRuntime 中构造好的服务;本接线只是把它们现有的读方法
// 适配成 hermesadmin 的 source 形态 —— 与 H3 诊断工具所封装的是「同一批」底层读操作。
// 它不增加任何新查询逻辑,也不引入任何新 transport。
type hermesAdminDeps struct {
	settings       *platformsettings.Service
	emailSender    *email.AuthSender
	moduleRegistry *moduleregistry.Registry
	credentialStr  *credentialstore.Store
	channelHealth  *channelhealth.Service
	dlqStore       *dlq.Store
	billingQueries *dbbilling.Queries
	logger         *zap.Logger
}

// buildHermesInspectionWorker 解析每日巡检配置,且仅当该功能被显式启用(opt-in)
// 且能解析出 admin 收件人时,才构造定时 worker。对未配置 / 已禁用 / 无收件人的情况
// 返回 nil(worker 不启动)并记录原因 —— 这是一个 fail-safe:未配置的部署绝不会发邮件。
// 调用方对非 nil 的 worker 执行 Start(),并把它存到 runtime 上以便优雅 Stop()。
func buildHermesInspectionWorker(ctx context.Context, d hermesAdminDeps) *hermesadmin.InspectionWorker {
	log := d.logger
	if log == nil {
		log = zap.NewNop()
	}

	cfg, err := hermesadmin.LoadConfig(ctx, d.settings)
	if err != nil {
		log.Warn("hermes daily inspection config invalid; worker not started", zap.Error(err))
		return nil
	}
	if !cfg.Enabled {
		log.Info("hermes daily inspection disabled (opt-in flag off); worker not started")
		return nil
	}
	if cfg.Recipient == "" {
		// 已启用但解析不出收件人:fail-safe —— 打 warn 且「不」启动。
		log.Warn("hermes daily inspection enabled but no admin recipient resolved; worker not started",
			zap.String("recipient_source", cfg.RecipientSource))
		return nil
	}
	if d.emailSender == nil {
		log.Warn("hermes daily inspection enabled but email sender unwired; worker not started")
		return nil
	}

	sources := hermesadmin.Sources{
		Modules: d.moduleRegistry,
	}
	if d.credentialStr != nil {
		sources.RenewStatus = d.credentialStr.ListRenewStatus
	}
	if d.channelHealth != nil {
		sources.ChannelSummary = d.channelHealth.SummarizeChannelHealth
	}
	if d.dlqStore != nil {
		sources.DLQList = d.dlqStore.List
	}
	if d.billingQueries != nil {
		sources.ListUsage = d.billingQueries.ListUsageRecords
	}

	svc := hermesadmin.NewInspectionService(sources, cfg.TenantID, nil)
	worker := hermesadmin.NewInspectionWorker(hermesadmin.InspectionWorkerConfig{
		Service:   svc,
		Sender:    d.emailSender,
		Recipient: cfg.Recipient,
		TenantID:  cfg.TenantID,
		Interval:  cfg.Interval,
		Logger:    log,
	})
	log.Info("hermes daily inspection enabled; worker starting",
		zap.String("recipient_source", cfg.RecipientSource),
		zap.Duration("interval", cfg.Interval),
		zap.Int64("tenant_id", cfg.TenantID))
	return worker
}
