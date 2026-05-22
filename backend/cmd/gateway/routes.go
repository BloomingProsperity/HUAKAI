package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/adminhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
)

func (d *deps) AdminObservabilityAuth() gatewayhttp.AdminObservabilityAuth {
	return d.adminAuth
}

func (d *deps) AdminObservabilityStore() gatewayhttp.AdminObservabilityStore {
	return d.billingQueries
}

func (d *deps) AdminDLQAuth() gatewayhttp.AdminDLQAuth {
	return d.adminAuth
}

func (d *deps) AdminDLQStore() gatewayhttp.AdminDLQStore {
	return d.dlqService
}

// mountRoutes wires the HTTP routes per docs/openapi/openapi.yaml.
func mountRoutes(r chi.Router, d *deps, logger *zap.Logger) {
	r.Post("/v1/chat/completions", gatewayhttp.NewChatCompletionsHandler(chatHandlerDeps(d)))
	r.Post("/v1/responses", gatewayhttp.NewResponsesHandler(chatHandlerDeps(d)))
	r.Post("/v1/messages", gatewayhttp.NewMessagesHandler(chatHandlerDeps(d)))

	auditVerifyDeps := gatewayhttp.AuditVerifyStaticDeps{Ledger: d.auditLedger, Registry: d.auditPubkeyRegistry}
	auditPubkeyDeps := gatewayhttp.AuditPubkeyDeps{Signer: d.auditSigner, Registry: d.auditPubkeyRegistry}
	r.Route("/v1/audit", func(r chi.Router) {
		r.Get("/pubkey", gatewayhttp.NewAuditPubkeyHandler(auditPubkeyDeps))
		r.Get("/pubkeys", gatewayhttp.NewAuditPubkeysHandler(auditPubkeyDeps))
		r.Get("/pubkey/{fingerprint_hex}", gatewayhttp.NewAuditPubkeyByFingerprintHandler(auditPubkeyDeps))
		r.Get("/verify", gatewayhttp.NewAuditVerifyHandler(auditVerifyDeps))
		r.Post("/verify", gatewayhttp.NewAuditVerifyHandler(auditVerifyDeps))
		r.Get("/merkle-tree.json", gatewayhttp.NewAuditMerkleTreeHandler(auditVerifyDeps))
	})

	receiptDeps := gatewayhttp.CostReceiptHandlerDeps{
		Receipts:        d.receiptStore,
		DerivedReceipts: d.receiptFormatter,
		MismatchRefunds: d.refundQueue,
		RateTables:      d.rateTableSource,
		Signer:          d.auditSigner,
		PubkeyRegistry:  d.auditPubkeyRegistry,
	}
	r.Route("/v1/receipts", func(r chi.Router) {
		r.With(auth.SessionMiddleware(d.userSessions)).Get("/{request_id}", gatewayhttp.NewCostReceiptGetHandler(receiptDeps))
		r.Post("/{request_id}", http.NotFound)
		r.With(auth.SessionMiddleware(d.userSessions)).Post("/{request_id}/verify", gatewayhttp.NewCostReceiptVerifyHandler(receiptDeps))
		r.With(auth.SessionMiddleware(d.userSessions)).Get("/{request_id_host}/{request_id_tail}", gatewayhttp.NewCostReceiptGetHandler(receiptDeps))
		r.Post("/{request_id_host}/{request_id_tail}", http.NotFound)
		r.With(auth.SessionMiddleware(d.userSessions)).Post("/{request_id_host}/{request_id_tail}/verify", gatewayhttp.NewCostReceiptVerifyHandler(receiptDeps))
	})
	r.Get("/v1/pricing/rate-table", gatewayhttp.NewPricingRateTableHandler(receiptDeps))
	r.Get("/v1/pricing/snapshots", gatewayhttp.NewPricingSnapshotsHandler(receiptDeps))
	r.Get("/v1/pricing/snapshots/{snapshot_id}", gatewayhttp.NewPricingSnapshotHandler(receiptDeps))

	r.Route("/v1/auth", func(r chi.Router) {
		gatewayhttp.MountAuthRoutes(r, gatewayhttp.AuthHandlerDeps{
			Auth:        d.userAuth,
			Sessions:    d.userSessions,
			EmailSender: d.authEmailSender,
			AdminAuth:   d.adminAuth,
		})
	})

	r.Route("/v1/sessions", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions))
		gatewayhttp.MountSessionRoutes(r, gatewayhttp.SessionHandlerDeps{Sessions: d.userSessions})
	})
	r.Route("/v1/users/me/vouchers", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions))
		gatewayhttp.MountVoucherUserRoutes(r, gatewayhttp.VoucherUserDeps{Service: d.voucherService})
	})
	r.With(auth.SessionMiddleware(d.userSessions)).Post("/v1/invitations", gatewayhttp.NewInvitationCreateHandler(gatewayhttp.InvitationDeps{
		Service: d.invitationService,
	}))

	mountAdminRoutes(r, d)
	logger.Info("routes mounted")
}

func chatHandlerDeps(d *deps) gatewayhttp.ChatHandlerDeps {
	return gatewayhttp.ChatHandlerDeps{
		Auth:                  d.inboundAuth,
		Registry:              d.modelRegistry,
		Router:                d.routePlanner,
		ClaimGate:             d.claimGate,
		RateTables:            d.rateTableSource,
		Selector:              d.selector,
		CredentialVault:       d.credentialVault,
		Dispatcher:            d.dispatcher,
		Forwarder:             d.forwarder,
		ResponseCache:         d.responseCache,
		Settler:               d.settler,
		ReplayStore:           d.replayStore,
		BillingPolicyResolver: d.billingPolicyResolver,
		CompletionBus:         d.completionBus,
		AuditLedger:           d.auditLedger,
		AuditLedgerDLQ:        d.dlqService,
		Signer:                d.auditSigner,
		ChannelHealth:         d.channelHealth,
		BillingPolicyVersion:  d.cfg.BillingPolicyVersion,
		RequestClass:          d.cfg.RequestClass,
	}
}

func mountAdminRoutes(r chi.Router, d *deps) {
	r.Route("/v1/admin/email", func(r chi.Router) {
		gatewayhttp.MountAdminEmailSettingsRoutes(r, gatewayhttp.AdminEmailSettingsDeps{
			Auth:  d.adminAuth,
			Store: d.emailSettings,
			Keys:  d.credentialKeys,
		})
	})
	r.Route("/admin/v1/api-keys", func(r chi.Router) {
		adminhttp.MountAPIKeyRoutes(r, adminhttp.AdminAPIKeysDeps{
			Auth:    d.adminAuth,
			Issuer:  d.adminIssuer,
			Revoker: d.adminRevoker,
			Queries: d.adminQueries,
		})
	})

	mountProviderAccountAdminRoutes := func(r chi.Router) {
		gatewayhttp.MountAdminPoolAccountRoutes(r, gatewayhttp.AdminPoolAccountDeps{
			Auth:          d.adminAuth,
			Store:         d.adminQueries,
			Credentials:   d.credentialStore,
			ChannelHealth: d.channelHealth,
		})
		gatewayhttp.MountAdminCredentialRoutes(r, gatewayhttp.AdminCredentialDeps{
			Auth:        d.adminAuth,
			Credentials: d.credentialStore,
			AuditStore:  d.adminQueries,
		})
		gatewayhttp.MountAdminCredentialAcquisitionRoutes(r, gatewayhttp.AdminCredentialAcquisitionDeps{
			Auth:            d.adminAuth,
			Sessions:        d.credentialAcqStore,
			Credentials:     d.credentialStore,
			CredentialAudit: d.credentialStore,
			AuditStore:      d.adminQueries,
		})
		gatewayhttp.MountChannelHealthAdminRoutes(r, gatewayhttp.ChannelHealthAdminDeps{
			Auth:       d.adminAuth,
			Controller: d.channelHealth,
		})
	}
	r.Route("/admin/v1/provider-accounts", mountProviderAccountAdminRoutes)
	r.Route("/v1/admin/provider-accounts", mountProviderAccountAdminRoutes)
	r.Route("/v1/admin/channel-health", func(r chi.Router) {
		gatewayhttp.MountChannelHealthReadAdminRoutes(r, gatewayhttp.ChannelHealthAdminDeps{
			Auth:       d.adminAuth,
			Controller: d.channelHealth,
		})
	})
	r.Route("/v1/admin/pool-accounts", mountProviderAccountAdminRoutes)

	r.Route("/admin/v1/credentials", func(r chi.Router) {
		gatewayhttp.MountAdminCredentialRenewStatusRoutes(r, gatewayhttp.AdminCredentialDeps{
			Auth:        d.adminAuth,
			Credentials: d.credentialStore,
			AuditStore:  d.adminQueries,
		})
		gatewayhttp.MountAdminCredentialAcquisitionHelperRoutes(r, gatewayhttp.AdminCredentialAcquisitionDeps{
			Auth:            d.adminAuth,
			Sessions:        d.credentialAcqStore,
			Credentials:     d.credentialStore,
			CredentialAudit: d.credentialStore,
			AuditStore:      d.adminQueries,
		})
	})
	r.Route("/admin/v1/pools", func(r chi.Router) {
		r.Mount("/", gatewayhttp.NewAdminPoolsHandler(gatewayhttp.AdminPoolsDeps{
			Auth:  d.adminAuth,
			Store: gatewayhttp.NewAdminPoolsStoreAdapter(d.billingQueries, d.adminQueries),
		}))
	})
	r.Route("/admin/v1/billing", func(r chi.Router) {
		gatewayhttp.MountAdminBillingSettingsRoutes(r, gatewayhttp.AdminBillingSettingsDeps{
			Auth:          d.adminAuth,
			Store:         d.billingPolicyStore,
			TenantChecker: d.adminQueries,
			AuditUpdater:  d.billingAuditUpdater,
		})
	})
	r.Route("/v1/admin/vouchers", func(r chi.Router) {
		gatewayhttp.MountVoucherAdminRoutes(r, gatewayhttp.VoucherAdminDeps{
			Auth:    d.adminAuth,
			Service: d.voucherService,
		})
	})
	r.Get("/admin/v1/usage", gatewayhttp.NewUsageHandler(d))
	r.Get("/admin/v1/billing/claims", gatewayhttp.NewClaimsHandler(d))
	r.Get("/admin/v1/audit-events", gatewayhttp.NewAuditEventsHandler(d))
	r.Get("/admin/v1/dlq/{handler}", gatewayhttp.NewAdminDLQListHandler(d))
	r.Post("/admin/v1/dlq/{id}/replay", gatewayhttp.NewAdminDLQReplayHandler(d))
	r.Post("/admin/v1/usage-record-dlq/{id}/replay", gatewayhttp.NewAdminDLQReplayHandler(d))
	r.Route("/admin/v1/cache/l2", func(r chi.Router) {
		gatewayhttp.MountAdminL2CacheRoutes(r, gatewayhttp.AdminL2CacheDeps{
			Auth:  d.adminAuth,
			Store: d.responseCache,
		})
	})
}
