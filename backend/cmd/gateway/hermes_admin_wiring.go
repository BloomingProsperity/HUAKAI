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

// hermesAdminDeps bundles the EXISTING read-only stores + email sender the WAVE
// H5 daily inspection reuses. Every field is a service already constructed in
// buildGatewayRuntime; this wiring only adapts their existing read methods into
// the hermesadmin source shape — the SAME underlying reads the H3 diagnostic
// tools wrap. It adds no new query logic and no new transport.
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

// buildHermesInspectionWorker resolves the daily-inspection config and, only when
// the feature is opt-in enabled AND an admin recipient resolves, constructs the
// scheduled worker. It returns nil (worker not started) for the unconfigured /
// disabled / no-recipient cases, logging the reason — a fail-safe so an
// unconfigured deployment never emails. The caller Start()s a non-nil worker and
// stores it on the runtime for graceful Stop().
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
		// Enabled but no recipient resolves: fail-safe — warn and do NOT start.
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
