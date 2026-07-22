package main

import (
	"context"
	"strconv"

	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
	obsoutbox "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/opsinspection"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/workerlease"
)

// opsInspectionDeps 打包每日巡检复用的现有只读服务和邮件发送器。
type opsInspectionDeps struct {
	platformTenantID int64
	settings         *platformsettings.Service
	emailSender      *email.AuthSender
	moduleRegistry   *moduleregistry.Registry
	credentialStr    *credentialstore.Store
	channelHealth    *channelhealth.Service
	dlqStore         *dlq.Store
	obsDLQStore      *obsoutbox.PostgresOutbox
	billingQueries   *dbbilling.Queries
	logger           *zap.Logger
	leaderLease      workerlease.SessionProvider
	windowClaims     workerlease.WindowClaimFactory
}

// buildOpsInspectionWorker 只在显式开启、平台租户有效且管理员收件人可解析时构造任务。
func buildOpsInspectionWorker(ctx context.Context, d opsInspectionDeps) *opsinspection.InspectionWorker {
	log := d.logger
	if log == nil {
		log = zap.NewNop()
	}

	cfg, err := opsinspection.LoadConfig(ctx, d.settings)
	if err != nil {
		log.Warn("每日运维巡检配置无效，任务未启动", zap.String("error_class", privacy.ErrorClassFor(ctx, err)))
		return nil
	}
	if !cfg.Enabled {
		log.Info("每日运维巡检未开启")
		return nil
	}
	if d.platformTenantID <= 0 {
		log.Warn("每日运维巡检缺少平台租户，任务未启动")
		return nil
	}
	if cfg.Recipient == "" {
		log.Warn("每日运维巡检未解析出管理员收件人，任务未启动",
			zap.String("recipient_source", cfg.RecipientSource))
		return nil
	}
	if d.emailSender == nil {
		log.Warn("每日运维巡检邮件发送器未接线，任务未启动")
		return nil
	}

	sources := opsinspection.Sources{
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
	if d.obsDLQStore != nil {
		sources.ObsDLQList = d.obsDLQStore.ListDead
	}
	if d.billingQueries != nil {
		sources.ListUsage = d.billingQueries.ListUsageRecords
	}

	svc := opsinspection.NewInspectionService(sources, d.platformTenantID, nil)
	worker := opsinspection.NewInspectionWorker(opsinspection.InspectionWorkerConfig{
		Service:     svc,
		Sender:      d.emailSender,
		Recipient:   cfg.Recipient,
		TenantID:    d.platformTenantID,
		Interval:    cfg.Interval,
		Logger:      log,
		LeaderLease: d.leaderLease,
		WindowClaim: buildOpsInspectionWindowClaim(d.windowClaims, d.platformTenantID),
	})
	log.Info("每日运维巡检任务启动",
		zap.String("recipient_source", cfg.RecipientSource),
		zap.Duration("interval", cfg.Interval),
		zap.Int64("tenant_id", d.platformTenantID))
	return worker
}

func buildOpsInspectionWindowClaim(factory workerlease.WindowClaimFactory, tenantID int64) workerlease.WindowClaimer {
	if factory == nil {
		return nil
	}
	return factory.For("ops_inspection", strconv.FormatInt(tenantID, 10))
}
