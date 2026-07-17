package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/accountfphttp"
	"github.com/BloomingProsperity/HUAKAI/internal/adminhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/adminquotahttp"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/adminuserhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/announcementhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/audiohttp"
	"github.com/BloomingProsperity/HUAKAI/internal/auditexporthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/billingreconhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/captcha"
	"github.com/BloomingProsperity/HUAKAI/internal/checkinhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/completionshttp"
	"github.com/BloomingProsperity/HUAKAI/internal/controlhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialprojecthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/embeddingshttp"
	"github.com/BloomingProsperity/HUAKAI/internal/engineembeddingsalias"
	"github.com/BloomingProsperity/HUAKAI/internal/exporthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintakehttp"
	"github.com/BloomingProsperity/HUAKAI/internal/geminihttp"
	"github.com/BloomingProsperity/HUAKAI/internal/healthhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeshttp"
	"github.com/BloomingProsperity/HUAKAI/internal/imageshttp"
	"github.com/BloomingProsperity/HUAKAI/internal/invoicehttp"
	"github.com/BloomingProsperity/HUAKAI/internal/mediataskhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/meexporthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/megroupshttp"
	"github.com/BloomingProsperity/HUAKAI/internal/mequotahttp"
	"github.com/BloomingProsperity/HUAKAI/internal/meusagehttp"
	"github.com/BloomingProsperity/HUAKAI/internal/mjclient"
	"github.com/BloomingProsperity/HUAKAI/internal/modelbindingadminhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/oauthpendinghttp"
	"github.com/BloomingProsperity/HUAKAI/internal/obsdlqhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/orphanreconcilehttp"
	"github.com/BloomingProsperity/HUAKAI/internal/passkeyhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/paymenthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingpublichttp"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyadmin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyadminhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/publicrankinghttp"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/referralhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/rerankhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/responsescompacthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/sunoclient"
	"github.com/BloomingProsperity/HUAKAI/internal/tlsfpadmin"
	"github.com/BloomingProsperity/HUAKAI/internal/tlsfphttp"
	"github.com/BloomingProsperity/HUAKAI/internal/trusthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/usageanalyticshttp"
	"github.com/BloomingProsperity/HUAKAI/internal/userauditloghttp"
	"github.com/BloomingProsperity/HUAKAI/internal/userkeyhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/videoclient"
	"github.com/BloomingProsperity/HUAKAI/internal/voucherhttp"
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

func credentialAcquisitionRouteDeps(d *deps) gatewayhttp.AdminCredentialAcquisitionDeps {
	return gatewayhttp.AdminCredentialAcquisitionDeps{
		Auth: d.adminAuth, Sessions: d.credentialAcqStore,
		Credentials: d.credentialStore, CredentialAudit: d.credentialStore,
		AuditStore: d.adminQueries, Exchangers: d.credentialExchangers,
		ProjectEnricher:   d.projectEnricher,
		BootstrapShortTTL: d.cfg.CredentialAcqBootstrapShortTTL,
		BootstrapLongTTL:  d.cfg.CredentialAcqBootstrapLongTTL,
	}
}

func credentialProjectRouteDeps(d *deps) credentialprojecthttp.Deps {
	return credentialprojecthttp.Deps{
		Auth: d.adminAuth, Store: d.credentialStore,
		Enricher: d.projectEnricher, Audit: d.adminQueries,
	}
}

func credentialModeAdapterRegistry(d *deps) *credentialworker.ModeAdapterRegistry {
	if d == nil || d.cfg == nil {
		return credentialworker.DefaultModeAdapterRegistry()
	}
	return credentialworker.DefaultModeAdapterRegistryWithRuntimeOAuth(d.cfg.VendorOAuth)
}

// mountRoutes 按 docs/openapi/openapi.yaml 接线 HTTP 路由。
func mountRoutes(r chi.Router, d *deps, logger *zap.Logger) {
	liveness := healthhttp.NewLivenessHandler()
	r.Method(http.MethodGet, "/healthz", liveness)
	r.Method(http.MethodHead, "/healthz", liveness)

	r.Post("/v1/chat/completions", gatewayhttp.NewChatCompletionsHandler(chatHandlerDeps(d)))
	r.Post("/v1/completions", completionshttp.NewCompletionsHandler(completionsHandlerDeps(d)))
	r.Post("/v1/embeddings", embeddingshttp.NewEmbeddingsHandler(embeddingsHandlerDeps(d)))
	r.Post("/engines/{model}/embeddings", engineembeddingsalias.NewHandler(embeddingshttp.NewEmbeddingsHandler(embeddingsHandlerDeps(d))))
	r.Post("/v1/rerank", rerankhttp.NewRerankHandler(rerankHandlerDeps(d)))
	r.Post("/v1/images/generations", imageshttp.NewGenerationsHandler(imageHandlerDeps(d)))
	r.Post("/v1/images/edits", imageshttp.NewEditsHandler(imageHandlerDeps(d)))
	r.Post("/v1/images/variations", imageshttp.NewVariationsHandler(imageHandlerDeps(d)))
	r.Post("/v1/audio/speech", audiohttp.NewSpeechHandler(audioHandlerDeps(d)))
	r.Post("/v1/audio/transcriptions", audiohttp.NewTranscriptionHandler(audioHandlerDeps(d)))
	r.Post("/v1/audio/translations", audiohttp.NewTranslationHandler(audioHandlerDeps(d)))
	r.Post("/v1/responses", gatewayhttp.NewResponsesHandler(chatHandlerDeps(d)))
	r.Post("/v1/responses/compact", responsescompacthttp.NewCompactHandler(gatewayhttp.NewResponsesHandler(chatHandlerDeps(d)), "/v1/responses"))
	r.Post("/backend-api/codex/responses", gatewayhttp.NewResponsesHandler(chatHandlerDeps(d)))
	r.Post("/backend-api/codex/responses/compact", responsescompacthttp.NewCompactHandler(gatewayhttp.NewResponsesHandler(chatHandlerDeps(d)), "/backend-api/codex/responses"))
	r.Post("/v1/messages", gatewayhttp.NewMessagesHandler(chatHandlerDeps(d)))
	r.Post("/v1/messages/count_tokens", completionshttp.NewCountTokensHandler(completionsHandlerDeps(d)))
	r.Get("/v1/realtime", handleRealtimeRoadmap)
	modelListHandler := controlhttp.NewModelListHandler(controlhttp.ModelListDeps{
		Auth:    d.inboundAuth,
		Catalog: d.modelRegistry,
		Pricing: d.rateTableSource,
	})
	r.Get("/v1/models", modelListHandler)
	r.Get("/v1/models/{model}", controlhttp.NewModelGetHandler(controlhttp.ModelListDeps{
		Auth:    d.inboundAuth,
		Catalog: d.modelRegistry,
		Pricing: d.rateTableSource,
	}))
	geminiV1BetaHandler := geminihttp.NewGenerateContentHandler(geminihttp.NewDeps(
		chatHandlerDeps(d),
		modelListHandler,
		embeddingshttp.NewEmbeddingsHandler(embeddingsHandlerDeps(d)),
		d.upstreamFeedback,
		d.retryBudget,
	))
	r.Get("/v1beta/models", geminiV1BetaHandler.ServeHTTP)
	r.Post("/v1beta/models/{rest:.*}", geminiV1BetaHandler.ServeHTTP)
	r.Get("/v1beta/models/{rest:.*}", geminiV1BetaHandler.ServeHTTP)
	r.Get("/v1/me/usage", meusagehttp.NewHandler(meusagehttp.Deps{
		Auth:  d.inboundAuth,
		Store: d.billingQueries,
	}))
	r.Get("/v1/generation", meusagehttp.NewGenerationHandler(meusagehttp.GenerationDeps{
		Auth:  d.inboundAuth,
		Store: d.billingQueries,
	}))
	r.Get("/v1/me/analytics/time-series", usageanalyticshttp.NewTimeSeriesHandler(usageanalyticshttp.Deps{
		Auth:  d.inboundAuth,
		Store: d.billingQueries,
	}))

	auditVerifyDeps := gatewayhttp.AuditVerifyStaticDeps{Ledger: d.auditLedger, Registry: d.auditPubkeyRegistry}
	auditPubkeyDeps := gatewayhttp.AuditPubkeyDeps{Signer: d.auditSigner, Registry: d.auditPubkeyRegistry}
	r.Get("/.well-known/huakai-pubkey.json", trusthttp.NewWellKnownHandler(trusthttp.WellKnownDeps{Signer: d.auditSigner, Registry: d.auditPubkeyRegistry}))
	r.Post("/v1/trust/verify", trusthttp.NewVerifyHandler(trusthttp.VerifyDeps{Signer: d.auditSigner, Registry: d.auditPubkeyRegistry}).ServeHTTP)
	r.Route("/v1/audit", func(r chi.Router) {
		r.Get("/pubkey", gatewayhttp.NewAuditPubkeyHandler(auditPubkeyDeps))
		r.Get("/pubkeys", gatewayhttp.NewAuditPubkeysHandler(auditPubkeyDeps))
		r.Get("/pubkey/{fingerprint_hex}", gatewayhttp.NewAuditPubkeyByFingerprintHandler(auditPubkeyDeps))
		r.Get("/verify", gatewayhttp.NewAuditVerifyHandler(auditVerifyDeps))
		r.Post("/verify", gatewayhttp.NewAuditVerifyHandler(auditVerifyDeps))
		r.Get("/merkle-tree.json", gatewayhttp.NewAuditMerkleTreeHandler(auditVerifyDeps))
		// 审计导出/证明会按租户范围返回整条审计链,必须认证并绑定到认证身份的租户;
		// pubkey/verify/merkle 保持公开(trust-chain 单负载验证)。处理器内部还会从认证
		// 上下文派生 tenant_scope_ref 并失败闭合,中间件与处理器双层堵住跨租户 IDOR。
		r.Group(func(r chi.Router) {
			r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))
			auditexporthttp.MountRoutes(r, auditexporthttp.Deps{
				Ledger:   auditExportLedgerFrom(d.auditLedger),
				Registry: d.auditPubkeyRegistry,
			})
		})
	})

	receiptDeps := gatewayhttp.CostReceiptHandlerDeps{
		Receipts:        d.receiptStore,
		DerivedReceipts: d.receiptFormatter,
		MismatchRefunds: d.refundQueue,
		RateTables:      d.rateTableSource,
		Signer:          d.auditSigner,
		PubkeyRegistry:  d.auditPubkeyRegistry,
	}
	disputeUserDeps := controlhttp.DisputeUserDeps{
		Receipts: d.receiptStore,
		Store:    d.disputeStore,
	}
	r.Route("/v1/receipts", func(r chi.Router) {
		r.With(auth.SessionMiddleware(d.userSessions, d.clientIPResolver)).Get("/{request_id}", gatewayhttp.NewCostReceiptGetHandler(receiptDeps))
		r.Post("/{request_id}", http.NotFound)
		r.With(auth.SessionMiddleware(d.userSessions, d.clientIPResolver)).Post("/{request_id}/disputes", controlhttp.NewCreateDisputeHandler(disputeUserDeps))
		r.With(auth.SessionMiddleware(d.userSessions, d.clientIPResolver)).Post("/{request_id}/verify", gatewayhttp.NewCostReceiptVerifyHandler(receiptDeps))
		r.With(auth.SessionMiddleware(d.userSessions, d.clientIPResolver)).Get("/{request_id_host}/{request_id_tail}", gatewayhttp.NewCostReceiptGetHandler(receiptDeps))
		r.Post("/{request_id_host}/{request_id_tail}", http.NotFound)
		r.With(auth.SessionMiddleware(d.userSessions, d.clientIPResolver)).Post("/{request_id_host}/{request_id_tail}/disputes", controlhttp.NewCreateDisputeHandler(disputeUserDeps))
		r.With(auth.SessionMiddleware(d.userSessions, d.clientIPResolver)).Post("/{request_id_host}/{request_id_tail}/verify", gatewayhttp.NewCostReceiptVerifyHandler(receiptDeps))
	})
	r.With(auth.SessionMiddleware(d.userSessions, d.clientIPResolver)).Get("/v1/me/disputes", controlhttp.NewListUserDisputesHandler(disputeUserDeps))
	r.Route("/v1/me", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))
		r.Get("/quota", mequotahttp.NewHandler(mequotahttp.Deps{
			Auth:  mequotahttp.SessionResolver{},
			Store: quota.NewPostgresStore(d.pgPool),
		}))
		// 会话级用量明细:跨当前用户全部 key 的逐请求日志(session 鉴权,按 user_id 收敛)。
		// 区别于顶层 /v1/me/usage(API-key 鉴权、单 key 维度)。
		r.Get("/usage-records", meusagehttp.NewSessionHandler(d.billingQueries))
		meexporthttp.MountRoutes(r, meexporthttp.Deps{Store: d.billingQueries})
		checkinhttp.MountRoutes(r, checkinhttp.Deps{Service: d.checkinService})
		userauditloghttp.MountRoutes(r, userauditloghttp.Deps{Store: d.userAuditStore})
		invoicehttp.MountRoutes(r, invoicehttp.Deps{Orders: d.paymentService})
		r.Get("/keys/{id}/usage-summary", usageanalyticshttp.NewKeyUsageSummaryHandler(usageanalyticshttp.KeyUsageSummaryDeps{
			Keys:  d.userKeyService,
			Store: d.billingQueries,
		}))
		r.Get("/invitations", gatewayhttp.NewInvitationSummaryHandler(gatewayhttp.InvitationDeps{
			Service: d.invitationService,
		}))
		r.Get("/invitation-code", gatewayhttp.NewMyReferralCodeHandler(gatewayhttp.InvitationDeps{
			Service: d.invitationService,
		}))
		meGroupsRatios := megroupshttp.RatioLister(d.pricingRatioStore)
		if meGroupsRatios == nil {
			meGroupsRatios = pricingcatalog.NewPostgresStore(d.pgPool)
		}
		r.Get("/groups", megroupshttp.NewHandler(megroupshttp.Deps{
			Auth:       megroupshttp.SessionResolver{},
			UserGroups: megroupshttp.NewPostgresUserGroupReader(d.pgPool),
			RoutesRepo: subscriptionenforce.NewPostgresRoutesRepo(d.pgPool),
			Ratios:     meGroupsRatios,
			Pools:      megroupshttp.NewPostgresPoolNameLister(d.pgPool),
		}))
		r.Get("/referrals", referralhttp.NewUserReferralsHandler(referralhttp.Deps{
			Service: d.invitationService,
		}))
		r.Get("/referrals/rewards", referralhttp.NewUserReferralRewardsHandler(referralhttp.Deps{
			Service: d.invitationService,
		}))
		r.Get("/voucher-redemptions", voucherhttp.NewRedemptionHistoryHandler(voucherhttp.Deps{
			Service: d.voucherService,
		}))
	})
	r.Get("/v1/pricing/rate-table", gatewayhttp.NewPricingRateTableHandler(receiptDeps))
	r.Get("/v1/pricing/page", pricingpublichttp.NewHandler(pricingpublichttp.Deps{
		Catalog: d.modelRegistry,
		Pricing: d.rateTableSource,
	}))
	var publicRankingsStore publicrankinghttp.Store
	if d.billingQueries != nil {
		publicRankingsStore = d.billingQueries
	}
	r.Get("/v1/public/rankings", publicrankinghttp.NewHandler(publicrankinghttp.Deps{
		Store: publicRankingsStore,
	}))
	mountSiteConfigRoute(r, d)
	r.Get("/v1/pricing/snapshots", gatewayhttp.NewPricingSnapshotsHandler(receiptDeps))
	r.Get("/v1/pricing/snapshots/{snapshot_id}", gatewayhttp.NewPricingSnapshotHandler(receiptDeps))
	announcementhttp.MountUserRoutes(r, announcementhttp.UserDeps{
		Service:          d.announcementService,
		Sessions:         d.userSessions,
		ClientIPResolver: d.clientIPResolver,
	})

	r.Route("/v1/auth", func(r chi.Router) {
		mountInviteValidateRoutes(r, d)
		gatewayhttp.MountAuthRoutes(r, authHandlerDeps(d, logger))
		// 「社交登录无邮箱→补邮箱建号」两端点(独立包,不膨胀 gatewayhttp)。
		oauthpendinghttp.MountRoutes(r, oauthPendingDeps(d, logger))
		r.Route("/passkey", func(r chi.Router) {
			passkeyhttp.MountLoginRoutes(r, passkeyHandlerDeps(d))
		})
		// GET /v1/auth/me 需已认证 session(同块的 login/register 等不需要), 故用 per-route session 中间件,
		// 不另起 /v1/auth Route 组(chi 同前缀重复 Mount 会 panic)。
		controlhttp.MountAuthMeRoutes(r.With(auth.SessionMiddleware(d.userSessions, d.clientIPResolver)), controlhttp.AuthMeDeps{
			Resolver:       d.panelAuthResolver,
			Profiles:       d.userAuth,
			SocialLinks:    d.userAuth,
			Sessions:       d.userSessions,
			SelfAccount:    d.userAuth,
			SessionsOthers: d.userSessions,
		})
		r.Route("/2fa", func(r chi.Router) {
			r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))
			controlhttp.MountTwoFARoutes(r, controlhttp.TwoFADeps{Service: d.twoFactor, Settings: d.platformSettings, Sessions: d.userSessions})
		})
	})
	r.Route("/v1/me/passkeys", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))
		passkeyhttp.MountUserRoutes(r, passkeyHandlerDeps(d))
	})

	r.Route("/v1/sessions", func(r chi.Router) {
		sessionDeps := sessionHandlerDeps(d, logger)
		gatewayhttp.MountSessionRefreshRoute(r, sessionDeps)
		r.Group(func(r chi.Router) {
			r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))
			gatewayhttp.MountSessionProtectedRoutes(r, sessionDeps)
		})
	})
	r.Route("/v1/users/me/vouchers", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))
		gatewayhttp.MountVoucherUserRoutes(r, gatewayhttp.VoucherUserDeps{Service: d.voucherService, ClientIPResolver: d.clientIPResolver, PlatformSettings: d.platformSettings})
	})
	r.Route("/v1/users/me/oauth-bindings", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))
		// 子路由相对挂在该组下;tenant/user 由 session 身份注入,handler 绝不取自 path/query。
		controlhttp.MountOAuthBindingsRoutes(r, controlhttp.OAuthBindingsDeps{
			Bindings:    d.userAuth,
			SocialLinks: d.userAuth,
			// telegram 绑定腿(「先绑定后登录」):已登录用户绑定自己的 telegram 身份。
			// bot token 与登录端点同源(env),空则绑定端点降级为 503。
			TelegramBinder:           d.userAuth,
			TelegramBotTokenResolver: telegramBotTokenResolver(d),
		})
	})
	paymentDeps := paymenthttp.Deps{Service: d.paymentService, Providers: d.paymentProviders}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))
		paymenthttp.MountUserRoutes(r, paymentDeps)
	})
	paymenthttp.MountWebhookRoutes(r, paymentDeps)
	r.Route("/v1/users/me/payments", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))
		paymenthttp.MountPaymentUserRoutes(r, paymenthttp.UserDeps{
			Service:          d.paymentService,
			ClientIPResolver: d.clientIPResolver,
			RefundRequests:   d.paymentRefundRequests,
		})
	})
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))
		mediataskhttp.MountRoutes(r, mediataskhttp.Deps{Service: d.mediaTaskService})
		mjclient.MountRoutes(r, d.mediaTaskService)
		sunoclient.MountRoutes(r, d.mediaTaskService)
		videoclient.MountRoutes(r, d.mediaTaskService)
	})
	r.Route("/v1/users/me/subscriptions", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))
		subscriptionhttp.MountSubscriptionUserRoutes(r, subscriptionhttp.UserDeps{
			Service:    d.subscriptionService,
			Quota:      quota.NewPostgresStore(d.pgPool),
			Payment:    d.paymentService,
			TradeNoGen: paymenthttp.ExternalTradeNoForTenant,
		})
	})
	// 公开支付回调端点 (P2a): 无 session/admin 中间件, 信任靠验签; 复用 d.paymentService。
	paymenthttp.MountPaymentWebhookRoutes(r, paymenthttp.WebhookDeps{Service: d.paymentService})
	r.Route("/v1/api-keys", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))
		userkeyhttp.MountUserAPIKeyRoutes(r, userkeyhttp.Deps{Service: d.userKeyService})
		mountUserKeyControlsRoutes(r, d)
	})
	if d.hermesService != nil && d.hermesRunner != nil {
		// WAVE H1:Hermes 被重新定位为面向 admin/operator 的运维助手。
		// 当 HUAKAI_HERMES_ADMIN_ONLY 为 true(默认)时,挂在
		// admin-token 中间件之后(admin_tokens 鉴权 + 租户作用域强制)。
		// 为 false 时,旧的终端用户 customer-key 路径被逐字保留,
		// 以便干净回滚。
		hermesAuth := hermeshttp.APIKeyMiddleware(d.inboundAuth)
		if d.hermesAdminOnly {
			hermesAuth = hermeshttp.AdminAuthMiddleware(d.adminAuth)
		}
		hermesRouterDeps := hermeshttp.RouterDeps{
			Service:        d.hermesService,
			Runner:         d.hermesRunner,
			Bridge:         d.hermesChatBridge,
			HeaderSettings: d.platformSettings,
			// 注入共享 confirm Cache 单例(同一实例后续也会注入 hermeschat 提议侧)。
			ConfirmCache: d.hermesConfirmCache,
			// KNOB A:运行时 mutating-tool 总开关。handler 在 mutating 分支顶端
			// 强制它(覆盖 preview 与 confirm);下方的双保险在它关闭时
			// 还会扣住 orchestrator 不接线。
			MutatingEnabled: d.hermesMutatingEnabled,
		}
		// WAVE H3 只读运维脊柱。仅在 admin-only 重定位生效时才挂上
		// tool/context 依赖——旧的终端用户 customer-key 路径绝不得暴露
		// 运维诊断。任一依赖为 nil 时,handler 也会 fail closed(503)。
		if d.hermesAdminOnly {
			if d.hermesToolRegistry != nil {
				hermesRouterDeps.Tools = d.hermesToolRegistry
			}
			if d.hermesToolCalls != nil {
				hermesRouterDeps.ToolCalls = d.hermesToolCalls
			}
			if d.hermesModuleSource != nil {
				hermesRouterDeps.ContextSource = d.hermesModuleSource
			}
			// WAVE H4 mutating-tool orchestrator(原子审计 + advisory-lock)。
			// KNOB A 双保险:当 mutating 总开关关闭时,orchestrator 不被接线,
			// 因此即便 handler 的标志检查被绕过,直接走 confirm 路径也会
			// 503(mutator 为 nil)而非真的去改动。
			if d.hermesMutator != nil && d.hermesMutatingEnabled {
				hermesRouterDeps.Mutator = d.hermesMutator
				// S2 (c):按 operator-token 的限流器随 mutator 一起挂载
				//(同样的 admin-only + mutating-enabled 门控)。limiter 为 nil/禁用
				// 即旧的无界行为。
				hermesRouterDeps.MutateGuard = &hermeshttp.MutateGuardDeps{
					RateLimiter: d.hermesMutateRateLimiter,
				}
			}
		}
		r.With(hermesAuth).
			Mount("/v1/hermes", hermeshttp.NewRouterWithDeps(hermesRouterDeps))
	}
	r.Post("/internal/runner/bootstrap", d.handleRunnerBootstrap)
	r.Post("/internal/runner/refresh", d.handleRunnerRefresh)
	r.Get("/internal/keys", d.handleRunnerKeys)
	// WAVE H3b:runner 在对话中途的只读 tool-execute 回调。它由 session 的
	// internal_token 鉴权(在 handler 内部校验)——与 runner 在这个内部
	// 监听器上做 LLM completions 所用的是同一个 HMAC——并且只用绑定 operator 的
	// 作用域派发只读工具。仅在该 handler 已接线时(admin-only + chat bridge)挂载。
	if d.hermesInternalToolHandler != nil {
		r.Method(http.MethodPost, "/internal/hermes/tool-execute", d.hermesInternalToolHandler)
	}
	r.With(auth.SessionMiddleware(d.userSessions, d.clientIPResolver)).Post("/v1/invitations", gatewayhttp.NewInvitationCreateHandler(gatewayhttp.InvitationDeps{
		Service: d.invitationService,
	}))

	mountAdminRoutes(r, d)
	logger.Info("routes mounted")
}

func auditExportLedgerFrom(ledger any) auditexporthttp.Ledger {
	out, _ := ledger.(auditexporthttp.Ledger)
	return out
}

func handleRealtimeRoadmap(w http.ResponseWriter, _ *http.Request) {
	writeGatewayJSONError(w, http.StatusNotImplemented, "realtime_not_available",
		"Realtime WebSocket runtime is a Phase 9+ mandatory roadmap item; use /v1/responses or /v1/chat/completions until F-RT-001 is released.")
}

func writeGatewayJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}

func (d *deps) handleRunnerBootstrap(w http.ResponseWriter, r *http.Request) {
	if d == nil || d.hermesBootstrapIssuer == nil {
		writeInternalError(w, http.StatusServiceUnavailable, "hermes_bootstrap_unavailable")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeInternalError(w, http.StatusBadRequest, "invalid_body")
		return
	}
	if !hermes.VerifyRunnerHMACRequest(r, body, d.hermesRunnerSharedSecret, time.Now().UTC()) {
		writeInternalError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, actorUserID, ok := runnerSignedIdentity(r)
	if !ok {
		writeInternalError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req struct {
		RunnerID    string `json:"runner_id"`
		TenantID    int64  `json:"tenant_id"`
		ActorUserID int64  `json:"actor_user_id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeInternalError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	token, err := d.hermesBootstrapIssuer.IssueBootstrapJWT(r.Context(), req.RunnerID)
	if err != nil {
		writeInternalError(w, http.StatusBadRequest, "jwt_issue_failed")
		return
	}
	if d.hermesService != nil {
		kid, _ := hermes.KIDFromToken(token)
		err := d.hermesService.RecordAudit(r.Context(), tenantID, actorUserID, hermes.ActionProfileRotate, map[string]any{
			"jwt_action": "issue",
			"runner_id":  req.RunnerID,
			"kid":        kid,
		}, hermes.AuditResultSuccess, r.Header.Get("X-Correlation-ID"), r.Header.Get("X-Request-ID"))
		if err != nil {
			writeInternalError(w, http.StatusServiceUnavailable, "audit_failed")
			return
		}
	}
	writeInternalJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (d *deps) handleRunnerRefresh(w http.ResponseWriter, r *http.Request) {
	if d == nil || d.hermesBootstrapIssuer == nil {
		writeInternalError(w, http.StatusServiceUnavailable, "hermes_bootstrap_unavailable")
		return
	}
	var req struct {
		Token       string `json:"token"`
		TenantID    int64  `json:"tenant_id"`
		ActorUserID int64  `json:"actor_user_id"`
	}
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeInternalError(w, http.StatusBadRequest, "invalid_body")
			return
		}
		if len(strings.TrimSpace(string(body))) > 0 {
			_ = json.Unmarshal(body, &req)
		}
	}
	if !hermes.VerifyRunnerHMACRequest(r, body, d.hermesRunnerSharedSecret, time.Now().UTC()) {
		writeInternalError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, actorUserID, ok := runnerSignedIdentity(r)
	if !ok {
		writeInternalError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	token := bearerToken(r.Header.Get(hermes.HeaderAuthorization))
	if token == "" {
		token = strings.TrimSpace(req.Token)
	}
	if token == "" {
		writeInternalError(w, http.StatusUnauthorized, "missing_token")
		return
	}
	refreshed, err := d.hermesBootstrapIssuer.RefreshJWT(r.Context(), token)
	if err != nil {
		writeInternalError(w, http.StatusUnauthorized, "invalid_token")
		return
	}
	if d.hermesService != nil {
		kid, _ := hermes.KIDFromToken(refreshed)
		err := d.hermesService.RecordAudit(r.Context(), tenantID, actorUserID, hermes.ActionProfileRotate, map[string]any{
			"jwt_action": "refresh",
			"kid":        kid,
		}, hermes.AuditResultSuccess, r.Header.Get("X-Correlation-ID"), r.Header.Get("X-Request-ID"))
		if err != nil {
			writeInternalError(w, http.StatusServiceUnavailable, "audit_failed")
			return
		}
	}
	writeInternalJSON(w, http.StatusOK, map[string]string{"token": refreshed})
}

func (d *deps) handleRunnerKeys(w http.ResponseWriter, r *http.Request) {
	if d == nil || d.hermesKeyStore == nil {
		writeInternalError(w, http.StatusServiceUnavailable, "hermes_keys_unavailable")
		return
	}
	if !hermes.VerifyRunnerHMACRequest(r, nil, d.hermesRunnerSharedSecret, time.Now().UTC()) {
		writeInternalError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	keys, err := d.hermesKeyStore.GetActiveKeys(r.Context())
	if err != nil {
		writeInternalError(w, http.StatusServiceUnavailable, "keys_failed")
		return
	}
	type keyResponse struct {
		Kid          string `json:"kid"`
		Alg          string `json:"alg"`
		PublicKeyPEM string `json:"public_key_pem"`
	}
	out := make([]keyResponse, 0, len(keys))
	for _, key := range keys {
		out = append(out, keyResponse{Kid: key.Kid, Alg: key.Alg, PublicKeyPEM: key.PublicKeyPEM})
	}
	writeInternalJSON(w, http.StatusOK, map[string]any{"keys": out})
}

func runnerSignedIdentity(r *http.Request) (int64, int64, bool) {
	if r == nil {
		return 0, 0, false
	}
	tenantID, ok := positiveInt64Header(r, hermes.HeaderTenant)
	if !ok {
		return 0, 0, false
	}
	actorUserID, ok := positiveInt64Header(r, hermes.HeaderUser)
	if !ok {
		return 0, 0, false
	}
	return tenantID, actorUserID, true
}

func positiveInt64Header(r *http.Request, header string) (int64, bool) {
	raw := strings.TrimSpace(r.Header.Get(header))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if len(header) < len("Bearer ") || !strings.EqualFold(header[:len("Bearer ")], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(header[len("Bearer "):])
}

func writeInternalJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeInternalError(w http.ResponseWriter, status int, code string) {
	writeInternalJSON(w, status, map[string]string{"error": code})
}

// authHandlerDeps 装配 /v1/auth handlers 的依赖。关键: EventSink 接生产
// zap sink(newAuthEventSink), 否则登录失败/注册/重置/OAuth/social 等安全
// 事件在 recordAuthEvent 处因 sink==nil 被静默丢弃。
// telegramBotTokenResolver 请求期解析 telegram bot token:后台设置 KeyTelegramBotToken 优先(settings-first),
// 空则回退 env HUAKAI_TELEGRAM_LOGIN_BOT_TOKEN(back-compat)。使运营在管理台配/换 token 即生效、不重部署。
func telegramBotTokenResolver(d *deps) func(context.Context) string {
	return func(ctx context.Context) string {
		if d.platformSettings != nil {
			if s, err := d.platformSettings.Get(ctx, platformsettings.KeyTelegramBotToken); err == nil {
				if v := strings.TrimSpace(s.Value); v != "" {
					return v
				}
			}
		}
		return strings.TrimSpace(os.Getenv("HUAKAI_TELEGRAM_LOGIN_BOT_TOKEN"))
	}
}

// captchaSecretResolver 请求期解析 captcha secret:后台设置 KeyCaptchaSecret 优先(settings-first),
// 空则回退 env HUAKAI_CAPTCHA_TURNSTILE_SECRET(back-compat)。使运营在管理台配/换 secret 即生效、不重部署。
func captchaSecretResolver(d *deps) func(context.Context) string {
	return func(ctx context.Context) string {
		if d.platformSettings != nil {
			if s, err := d.platformSettings.Get(ctx, platformsettings.KeyCaptchaSecret); err == nil {
				if v := strings.TrimSpace(s.Value); v != "" {
					return v
				}
			}
		}
		return captchaTurnstileSecret()
	}
}

func authHandlerDeps(d *deps, logger *zap.Logger) gatewayhttp.AuthHandlerDeps {
	return gatewayhttp.AuthHandlerDeps{
		Auth:             d.userAuth,
		Sessions:         d.userSessions,
		EmailSender:      d.authEmailSender,
		EmailSendLimiter: d.emailSendLimit,
		AdminAuth:        d.adminAuth,
		EventSink:        newAuthEventSink(logger),
		ClientIPResolver: d.clientIPResolver,
		Captcha: captcha.NewVerifier(
			d.platformSettings,
			captchaSecretResolver(d),
			&http.Client{Timeout: 10 * time.Second},
		),
		LoginThrottle:            d.loginThrottle,
		TwoFactor:                d.twoFactor,
		TwoFactorSettings:        d.platformSettings,
		TelegramBotTokenResolver: telegramBotTokenResolver(d),
		OAuthPendingKey:          oauthPendingKey(d),
	}
}

// oauthPendingKey 从会话签名密钥域分隔派生「社交登录补邮箱」流程专用密钥。会话服务/密钥缺失时返 nil
// (补邮箱流程随之停用、端点返 503),不 panic。
func oauthPendingKey(d *deps) []byte {
	if d == nil || d.userSessions == nil {
		return nil
	}
	return oauthpendinghttp.DeriveKey(d.userSessions.SigningKey)
}

// oauthPendingDeps 装配「补邮箱建号」独立包的依赖。EmailSender 用类型断言取具体 *email.AuthSender
// (它实现 SendOAuthEmailCode);断言失败(如 Noop 装配)则不发码。事件经 gatewayhttp 的 AuthEventSink 记录。
func oauthPendingDeps(d *deps, logger *zap.Logger) oauthpendinghttp.Deps {
	var emailSender oauthpendinghttp.EmailCodeSender
	if s, ok := d.authEmailSender.(oauthpendinghttp.EmailCodeSender); ok {
		emailSender = s
	}
	sink := newAuthEventSink(logger)
	return oauthpendinghttp.Deps{
		Auth:        d.userAuth,
		Sessions:    d.userSessions,
		EmailSender: emailSender,
		ClientIP:    d.clientIPResolver,
		Key:         oauthPendingKey(d),
		RecordEvent: func(ctx context.Context, eventType string, tenantID, userID int64, provider, outcome, reasonClass, ip, userAgent string) {
			sink.RecordAuthEvent(ctx, gatewayhttp.AuthEvent{
				EventType: eventType, TenantID: tenantID, UserID: userID,
				IP: ip, UserAgent: userAgent, Provider: provider,
				Outcome: outcome, ReasonClass: reasonClass, AuthMethod: provider,
			})
		},
	}
}

// sessionHandlerDeps 装配 /v1/sessions handlers 的依赖, 同样接生产 EventSink
// 以记录 session_refresh_failed / session_refreshed 安全事件。
func sessionHandlerDeps(d *deps, logger *zap.Logger) gatewayhttp.SessionHandlerDeps {
	return gatewayhttp.SessionHandlerDeps{
		Sessions:         d.userSessions,
		EventSink:        newAuthEventSink(logger),
		ClientIPResolver: d.clientIPResolver,
	}
}

func passkeyHandlerDeps(d *deps) passkeyhttp.Deps {
	if d == nil {
		return passkeyhttp.Deps{}
	}
	var users passkeyhttp.UserStore
	if d.userAuth != nil {
		users = d.userAuth.Store
	}
	return passkeyhttp.Deps{
		Passkeys:         d.passkeys,
		Sessions:         d.userSessions,
		Users:            users,
		StepUp:           passkeyhttp.NewLocalStepUpVerifier(users, d.twoFactor),
		ClientIPResolver: d.clientIPResolver,
	}
}

func chatHandlerDeps(d *deps) gatewayhttp.ChatHandlerDeps {
	return gatewayhttp.ChatHandlerDeps{
		Auth:                    d.inboundAuth,
		Registry:                d.modelRegistry,
		Router:                  d.routePlanner,
		ClaimGate:               d.claimGate,
		QuotaReserver:           d.quotaReserver,
		RateTables:              d.rateTableSource,
		PricingRatioResolver:    d.pricingRatioResolver,
		CacheOverrideStore:      d.cacheOverrideStore,
		Selector:                d.selector,
		QueueWaiter:             d.queueWaiter,
		CredentialVault:         d.credentialVault,
		Dispatcher:              d.dispatcher,
		Forwarder:               d.forwarder,
		ResponseCache:           d.responseCache,
		CacheScope:              d.cacheScope,
		Settler:                 d.settler,
		SettlementIntents:       d.settlementIntents,
		SettlementIntentEnabled: d.cfg.SettlementIntentEnabled,
		ReplayStore:             d.replayStore,
		BillingPolicyResolver:   d.billingPolicyResolver,
		CompletionBus:           d.completionBus,
		AuditRefPolicy:          d.auditRefPolicy,
		AuditLedger:             d.auditLedger,
		AuditLedgerDLQ:          d.dlqService,
		ModerationScreener:      moderationScreener(d),
		SettleRecoveryDLQ:       d.dlqService,
		Signer:                  d.auditSigner,
		ChannelHealth:           d.channelHealth,
		ModelCooldowns:          d.modelCooldowns,
		RateService:             d.upstreamRate,
		RetryBudget:             d.retryBudget,
		CredentialHotRefresher:  d.credentialScheduler,
		AuthCooldown:            d.authCooldown,
		ModelFallbackSettings:   d.platformSettings,
		// 非流式 keepalive 间隔:默认 0=关(不改现有行为)。反代(Cloudflare)前建议设
		// HUAKAI_NONSTREAM_KEEPALIVE_INTERVAL=85s,避开 ~100s 空闲超时掐断长 buffered 响应(图片生成等)。
		NonStreamKeepAliveInterval: streamDurationEnv("HUAKAI_NONSTREAM_KEEPALIVE_INTERVAL", 0),
		// 平台设置读取(止漏装配):此前从不赋值 → 热路径恒 nil → warmup_intercept 与
		// codex_client_access.* 全部键落库后运行时永不被读(死开关)。两族键默认均为
		// 关/等价现行为,接上不翻转任何默认行为,仅让运维显式配置真正生效。
		PlatformSettings:     d.platformSettings,
		BillingPolicyVersion: d.cfg.BillingPolicyVersion,
		RequestClass:         d.cfg.RequestClass,
		ClientIPResolver:     d.clientIPResolver,
		SessionCapRegistry:   d.sessionCapRegistry,
		RecentReqRing:        d.recentReqRing,
		// 工具调用附加费价表来源(NAPI-BILLING-01 止漏装配)。之前此字段从不赋值 →
		// 生产恒 nil → 工具调用加 $0 漏钱;现按 HUAKAI_TOOL_SURCHARGE_ENABLED 接入
		// platformSource(默认开,计费默认翻转,Owner 已授权)。
		ToolPricingTable: d.toolPriceSource,
	}
}

func embeddingsHandlerDeps(d *deps) embeddingshttp.Deps {
	return embeddingshttp.Deps{
		Auth:                  d.inboundAuth,
		Registry:              d.modelRegistry,
		Router:                d.routePlanner,
		ClaimGate:             d.claimGate,
		QuotaReserver:         d.quotaReserver,
		RateTables:            d.rateTableSource,
		PricingRatioResolver:  d.pricingRatioResolver,
		Selector:              d.selector,
		CredentialVault:       d.credentialVault,
		Dispatcher:            d.dispatcher,
		Settler:               d.settler,
		BillingPolicyResolver: d.billingPolicyResolver,
		BillingPolicyVersion:  d.cfg.BillingPolicyVersion,
		RequestClass:          d.cfg.RequestClass,
		Feedback:              d.upstreamFeedback,
		RetryBudget:           d.retryBudget,
	}
}

func completionsHandlerDeps(d *deps) completionshttp.Deps {
	return completionshttp.Deps{
		Auth:                  d.inboundAuth,
		Registry:              d.modelRegistry,
		Router:                d.routePlanner,
		ClaimGate:             d.claimGate,
		QuotaReserver:         d.quotaReserver,
		RateTables:            d.rateTableSource,
		PricingRatioResolver:  d.pricingRatioResolver,
		Selector:              d.selector,
		CredentialVault:       d.credentialVault,
		Dispatcher:            d.dispatcher,
		Settler:               d.settler,
		BillingPolicyResolver: d.billingPolicyResolver,
		BillingPolicyVersion:  d.cfg.BillingPolicyVersion,
		RequestClass:          d.cfg.RequestClass,
		Feedback:              d.upstreamFeedback,
		RetryBudget:           d.retryBudget,
		// 流式交付后 settle 失败的 durable 兜底队列，与 chat 路径同一注入(S1-2/S1-3)。
		SettleRecoveryDLQ: d.dlqService,
	}
}

func rerankHandlerDeps(d *deps) rerankhttp.Deps {
	return rerankhttp.Deps{
		Auth:                  d.inboundAuth,
		Registry:              d.modelRegistry,
		Router:                d.routePlanner,
		ClaimGate:             d.claimGate,
		QuotaReserver:         d.quotaReserver,
		RateTables:            d.rateTableSource,
		PricingRatioResolver:  d.pricingRatioResolver,
		Selector:              d.selector,
		CredentialVault:       d.credentialVault,
		Dispatcher:            d.dispatcher,
		Settler:               d.settler,
		BillingPolicyResolver: d.billingPolicyResolver,
		BillingPolicyVersion:  d.cfg.BillingPolicyVersion,
		RequestClass:          d.cfg.RequestClass,
		Feedback:              d.upstreamFeedback,
		RetryBudget:           d.retryBudget,
	}
}

func imageHandlerDeps(d *deps) imageshttp.Deps {
	return imageshttp.Deps{
		Auth:                  d.inboundAuth,
		Registry:              d.modelRegistry,
		Router:                d.routePlanner,
		ClaimGate:             d.claimGate,
		QuotaReserver:         d.quotaReserver,
		RateTables:            d.rateTableSource,
		PricingRatioResolver:  d.pricingRatioResolver,
		Selector:              d.selector,
		CredentialVault:       d.credentialVault,
		Dispatcher:            d.dispatcher,
		Settler:               d.settler,
		SettleRecoveryDLQ:     d.dlqService,
		BillingPolicyResolver: d.billingPolicyResolver,
		BillingPolicyVersion:  d.cfg.BillingPolicyVersion,
		RequestClass:          d.cfg.RequestClass,
		ClientIPResolver:      d.clientIPResolver,
		Feedback:              d.upstreamFeedback,
		RetryBudget:           d.retryBudget,
		// 图片生成强制 buffered、可达数十秒;反代前设 HUAKAI_NONSTREAM_KEEPALIVE_INTERVAL 保活。默认 0=关。
		NonStreamKeepAliveInterval: streamDurationEnv("HUAKAI_NONSTREAM_KEEPALIVE_INTERVAL", 0),
	}
}

func audioHandlerDeps(d *deps) audiohttp.Deps {
	return audiohttp.Deps{
		Auth:                  d.inboundAuth,
		Registry:              d.modelRegistry,
		Router:                d.routePlanner,
		ClaimGate:             d.claimGate,
		QuotaReserver:         d.quotaReserver,
		RateTables:            d.rateTableSource,
		PricingRatioResolver:  d.pricingRatioResolver,
		Selector:              d.selector,
		CredentialVault:       d.credentialVault,
		Dispatcher:            d.dispatcher,
		Settler:               d.settler,
		SettleRecoveryDLQ:     d.dlqService,
		BillingPolicyResolver: d.billingPolicyResolver,
		BillingPolicyVersion:  d.cfg.BillingPolicyVersion,
		RequestClass:          d.cfg.RequestClass,
		Feedback:              d.upstreamFeedback,
		RetryBudget:           d.retryBudget,
	}
}

func adminUserRouteDeps(d *deps) adminuserhttp.Deps {
	return adminuserhttp.Deps{
		Auth:             d.adminAuth,
		Store:            d.adminQueries,
		UsageStore:       d.billingQueries,
		SocialLinks:      d.userAuth,
		UnlockAudit:      adminuserhttp.NewPostgresUnlockAuditStore(d.pgPool),
		TwoFADisabler:    d.twoFactor,
		PasskeyResetter:  d.passkeys,
		UserGroupSetter:  adminuserhttp.NewPostgresUserGroupStore(d.pgPool),
		UserRemarkSetter: adminuserhttp.NewPostgresUserRemarkStore(d.pgPool),
		UserStatusSetter: adminuserhttp.NewPostgresUserStatusStore(d.pgPool),
		UserCreator:      adminuserhttp.NewPostgresUserCreateStore(d.pgPool),
		UserSoftDeleter:  adminuserhttp.NewPostgresUserSoftDeleteStore(d.pgPool),
		SessionRevoker:   d.userSessions,
		Unlocker:         d.userAuth,
		Audit:            d.adminQueries,
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
	mountPlatformSettingsRoutes(r, d)
	mountUsageAdminRoutes(r, d)
	mountSystemHealthRoutes(r, d) // ADMIN-042
	// 运行日志查询/清理/采集健康(platform_admin;handler 内部自解析鉴权)。
	r.Route("/v1/admin/ops", func(r chi.Router) {
		gatewayhttp.MountAdminRuntimeLogRoutes(r, gatewayhttp.AdminRuntimeLogsDeps{
			Auth:  d.adminAuth,
			Store: d.runtimeLogStore,
			Sink:  d.logSink,
			Audit: d.adminQueries,
		})
	})
	mountBackupRoutes(r, d)         // 只读备份 manifest(platform_admin)
	mountModuleRegistryRoutes(r, d) // WAVE H2 模块知识脊柱
	var adminResolver adminIdentityResolver
	if d.adminAuth != nil {
		adminResolver = d.adminAuth
	}
	r.Method(http.MethodPut, "/v1/admin/models/{id}/capabilities",
		adminGate(adminResolver, controlhttp.NewAdminCapabilitiesHandler(controlhttp.AdminCapabilitiesDeps{
			Store: d.modelRegistry,
		})))
	modelAliasDeps := controlhttp.AdminModelAliasesDeps{Store: d.modelRegistry}
	r.Method(http.MethodPost, "/v1/admin/models/aliases/bulk-import",
		adminGate(adminResolver, controlhttp.NewAdminModelAliasBulkImportHandler(modelAliasDeps)))
	r.Method(http.MethodGet, "/v1/admin/models/{id}/capability-bindings",
		adminGate(adminResolver, controlhttp.NewAdminModelCapabilityBindingsHandler(modelAliasDeps)))
	r.Method(http.MethodPut, "/v1/admin/models/{id}/capability-bindings",
		adminGate(adminResolver, controlhttp.NewAdminModelCapabilityBindingUpsertHandler(modelAliasDeps)))
	// 租户目录继承策略(inherit_global_catalog)admin 写面 — platform_admin only(经 adminGate), tenant 取 query。
	tenantPolicyDeps := controlhttp.AdminTenantPolicyDeps{Store: d.modelRegistry}
	r.Method(http.MethodGet, "/v1/admin/model-registry-policy",
		adminGate(adminResolver, controlhttp.NewAdminTenantPolicyGetHandler(tenantPolicyDeps)))
	r.Method(http.MethodPut, "/v1/admin/model-registry-policy",
		adminGate(adminResolver, controlhttp.NewAdminTenantPolicySetHandler(tenantPolicyDeps)))
	r.Route("/admin/v1/api-keys", func(r chi.Router) {
		adminhttp.MountAPIKeyRoutes(r, adminhttp.AdminAPIKeysDeps{
			Auth:    d.adminAuth,
			Issuer:  d.adminIssuer,
			Revoker: d.adminRevoker,
			Queries: d.adminQueries,
		})
	})
	// admin token(运维凭证)签发 / 列举 / 吊销:支持临时/一次性 token
	//(可选 expires_at)。高权操作,issuer 内部做 platform_admin-only 的
	// fail-closed RBAC;明文 bearer 仅在签发响应里返一次。
	r.Route("/admin/v1/admin-tokens", func(r chi.Router) {
		adminhttp.MountAdminTokenRoutes(r, adminhttp.AdminTokensDeps{
			Auth:   d.adminAuth,
			Issuer: d.adminTokenIssuer,
		})
	})
	adminUserDeps := adminUserRouteDeps(d)
	r.Get("/admin/v1/users", adminuserhttp.NewListHandler(adminUserDeps))
	r.Route("/admin/v1/users", func(r chi.Router) {
		adminuserhttp.MountRoutes(r, adminUserDeps)
	})
	// 出站代理池 admin 面(F-FP-POOL):在无密钥的 proxyadmin.Service 之上提供
	// list/create/update/delete/set-status。经共享 admin gate 做租户作用域;
	// auth_secret 只写,绝不向外投影。
	r.Route("/admin/v1/proxies", func(r chi.Router) {
		proxyadminhttp.MountRoutes(r, proxyadminhttp.Deps{
			Auth:    d.adminAuth,
			Service: proxyadmin.New(d.adminQueries, d.credentialKeys),
			Prober:  buildProxyProber(d),
		})
	})
	// Model -> pool 绑定 admin 面:补上之前的死写路径缺口
	//(列 + resolver 早已存在,但没有 admin CRUD)。顶层资源,双角色
	// gate,snapshot.version 由 registry.PostgresRegistry 在 Tx 内自增。
	r.Route("/admin/v1/model-pool-bindings", func(r chi.Router) {
		modelbindingadminhttp.MountRoutes(r, modelbindingadminhttp.Deps{
			Auth:    d.adminAuth,
			Service: registry.NewPostgresRegistry(d.pgPool, nil),
		})
	})
	r.Get("/admin/v1/account-modes", adminhttp.NewAccountModeListHandler(adminhttp.AdminAccountModesDeps{
		Auth: d.adminAuth,
	}))
	providerCatalogDeps := adminhttp.AdminProviderCatalogDeps{
		Auth:  d.adminAuth,
		Store: adminhttp.NewProviderCatalogStoreAdapter(d.adminQueries, d.pgPool),
	}
	r.Get("/admin/v1/providers", adminhttp.NewProviderCatalogListHandler(providerCatalogDeps))
	r.Post("/admin/v1/providers", adminhttp.NewProviderCatalogCreateHandler(providerCatalogDeps))
	r.Put("/admin/v1/providers/{code}", adminhttp.NewProviderCatalogUpdateHandler(providerCatalogDeps))
	r.Delete("/admin/v1/providers/{code}", adminhttp.NewProviderCatalogDeleteHandler(providerCatalogDeps))
	channelCatalogDeps := adminhttp.AdminChannelCatalogDeps{
		Auth:    d.adminAuth,
		Queries: d.adminQueries,
		Store:   adminhttp.NewChannelCatalogStoreAdapter(d.adminQueries, d.pgPool),
	}
	r.Get("/admin/v1/channels", adminhttp.NewChannelCatalogListHandler(channelCatalogDeps))
	r.Post("/admin/v1/channels", adminhttp.NewChannelCatalogCreateHandler(channelCatalogDeps))
	r.Put("/admin/v1/channels/{id}", adminhttp.NewChannelCatalogUpdateHandler(channelCatalogDeps))
	r.Delete("/admin/v1/channels/{id}", adminhttp.NewChannelCatalogDeleteHandler(channelCatalogDeps))
	quotaPolicyDeps := adminquotahttp.Deps{
		Auth:  d.adminAuth,
		Store: adminquotahttp.NewQuotaPolicyStoreAdapter(d.pgPool),
	}
	// role 制单登录:集合级 + /{id} 端点内联挂载迁入包函数(create/update 挂 SessionSafe、delete 留 token-only),
	// 路径不变(仍规范无尾斜杠),仅把写分级注解与路由定义收拢到一处。
	adminquotahttp.MountQuotaPolicyRoutes(r, quotaPolicyDeps)
	// 孤儿对账闭环 admin 面:只读列表(可视化) + 显式手动对账动作。复用既有 admin 鉴权
	// (d.adminAuth)。追扣走既有 billing settle、Manual-First、幂等防双扣(详见 orphanreconcilehttp)。
	if d.mediaTaskStore != nil {
		orphanDeps := orphanreconcilehttp.Deps{Auth: d.adminAuth, Store: d.mediaTaskStore}
		r.Get("/admin/v1/media-task-orphans", orphanreconcilehttp.NewListHandler(orphanDeps))
		r.Post("/admin/v1/media-task-orphans/{id}/reconcile", orphanreconcilehttp.NewReconcileHandler(orphanDeps))
	}
	channelTestTemplateDeps := adminhttp.AdminChannelTestTemplateDeps{
		Auth:  d.adminAuth,
		Store: d.adminQueries,
	}
	r.Get("/admin/v1/channel-test-templates", adminhttp.NewChannelTestTemplateListHandler(channelTestTemplateDeps))
	r.Post("/admin/v1/channel-test-templates", adminhttp.NewChannelTestTemplateCreateHandler(channelTestTemplateDeps))
	r.Get("/admin/v1/channel-test-templates/{id}", adminhttp.NewChannelTestTemplateGetHandler(channelTestTemplateDeps))
	r.Put("/admin/v1/channel-test-templates/{id}", adminhttp.NewChannelTestTemplateUpdateHandler(channelTestTemplateDeps))
	r.Delete("/admin/v1/channel-test-templates/{id}", adminhttp.NewChannelTestTemplateDeleteHandler(channelTestTemplateDeps))

	mountProviderAccountAdminRoutes := func(r chi.Router) {
		gatewayhttp.MountAdminPoolAccountRoutes(r, gatewayhttp.AdminPoolAccountDeps{
			Auth:          d.adminAuth,
			Store:         gatewayhttp.NewAdminPoolAccountStoreAdapter(d.adminQueries, d.pgPool),
			Credentials:   d.credentialStore,
			ChannelHealth: d.channelHealth,
		})
		adminhttp.MountProviderAccountTestRoutes(r, adminhttp.ProviderAccountTestDeps{
			Auth:     d.adminAuth,
			Accounts: d.adminQueries,
			Tester:   adminhttp.NewProviderAccountCredentialTester(d.credentialStore, credentialModeAdapterRegistry(d)),
		})
		// 账号 TLS 指纹 profile 绑定/解绑(独立包 accountfphttp,§13 不塞进 god 包 gatewayhttp)。
		accountfphttp.MountRoutes(r, accountfphttp.Deps{Auth: d.adminAuth, Store: d.adminQueries})
		adminhttp.MountProviderAccountHealthRoutes(r, adminhttp.ProviderAccountHealthDeps{
			Auth:          d.adminAuth,
			Store:         d.adminQueries,
			RecentReqRing: d.recentReqRing,
		})
		adminhttp.MountProviderAccountRecentRequestsRoutes(r, adminhttp.ProviderAccountRecentRequestsDeps{
			Auth:     d.adminAuth,
			Accounts: d.adminQueries,
			Requests: d.billingQueries,
		})
		adminhttp.MountProviderAccountBulkRoutes(r, adminhttp.ProviderAccountBulkDeps{
			Auth:  d.adminAuth,
			Store: d.adminQueries,
		})
		adminhttp.MountProviderAccountUpstreamModelsRoutes(r, adminhttp.UpstreamModelsDeps{
			Auth:     d.adminAuth,
			Accounts: d.adminQueries,
			Creds:    d.credentialStore,
		})
		gatewayhttp.MountAdminCredentialRoutes(r, gatewayhttp.AdminCredentialDeps{
			Auth:        d.adminAuth,
			Credentials: d.credentialStore,
			AuditStore:  d.adminQueries,
		})
		credentialprojecthttp.MountRoutes(r, credentialProjectRouteDeps(d))
		gatewayhttp.MountAdminCredentialAcquisitionRoutes(r, credentialAcquisitionRouteDeps(d))
		gatewayhttp.MountChannelHealthAdminRoutes(r, gatewayhttp.ChannelHealthAdminDeps{
			Auth:       d.adminAuth,
			Controller: d.channelHealth,
		})
	}
	r.Route("/admin/v1/provider-accounts", mountProviderAccountAdminRoutes)
	r.Route("/v1/admin/provider-accounts", mountProviderAccountAdminRoutes)
	r.Route("/admin/v1", func(r chi.Router) {
		adminhttp.MountVersionRoutes(r, adminhttp.VersionDeps{Auth: d.adminAuth})
		adminhttp.MountLogLevelRoutes(r, adminhttp.LogLevelDeps{Auth: d.adminAuth})
	})
	r.Route("/v1/admin", func(r chi.Router) {
		adminhttp.MountVersionRoutes(r, adminhttp.VersionDeps{Auth: d.adminAuth})
		adminhttp.MountLogLevelRoutes(r, adminhttp.LogLevelDeps{Auth: d.adminAuth})
	})
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
		gatewayhttp.MountAdminCredentialAcquisitionHelperRoutes(r, credentialAcquisitionRouteDeps(d))
		accountintakehttp.Mount(r, accountintakehttp.Deps{
			Auth:    d.adminAuth,
			Service: accountintake.NewService(d.pgPool, d.credentialStore, d.channelHealth),
		})
	})
	r.Route("/admin/v1/pools", func(r chi.Router) {
		r.Mount("/", gatewayhttp.NewAdminPoolsHandler(gatewayhttp.AdminPoolsDeps{
			Auth:  d.adminAuth,
			Store: gatewayhttp.NewAdminPoolsStoreAdapter(d.billingQueries, d.adminQueries, d.pgPool),
		}))
	})
	r.Route("/admin/v1/billing", func(r chi.Router) {
		gatewayhttp.MountAdminBillingSettingsRoutes(r, gatewayhttp.AdminBillingSettingsDeps{
			Auth:          d.adminAuth,
			Store:         d.billingPolicyStore,
			TenantChecker: d.adminQueries,
			AuditUpdater:  d.billingAuditUpdater,
		})
		repriceService := billing.NewPostgresRepriceService(d.pgPool, d.rateTableSource, d.pricingRatioResolver, d.cfg.BillingPolicyVersion)
		r.With(adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)).Post("/reprice", billingreconhttp.NewHandler(billingreconhttp.Deps{
			Auth:    d.adminAuth,
			Service: repriceService,
		}))
	})
	mountPricingCatalogRoutes(r, d)
	r.Route("/admin/v1/balances", func(r chi.Router) {
		adminhttp.MountBalanceCreditRoutes(r, adminhttp.AdminBalanceCreditDeps{
			Auth:    d.adminAuth,
			Service: d.paymentService,
		})
	})
	r.Route("/admin/v1/model-sync", func(r chi.Router) {
		adminhttp.MountModelSyncRoutes(r, adminhttp.AdminModelSyncDeps{
			Auth:    d.adminAuth,
			Service: d.modelSync,
		})
	})
	r.Route("/v1/admin/vouchers", func(r chi.Router) {
		gatewayhttp.MountVoucherAdminRoutes(r, gatewayhttp.VoucherAdminDeps{
			Auth:    d.adminAuth,
			Service: d.voucherService,
		})
	})
	exporthttp.MountRoutes(r, exporthttp.Deps{
		Auth:     d.adminAuth,
		Payments: d.paymentService,
		Usage:    d.billingQueries,
		Orders:   d.paymentService, // OPS-005:订单 CSV 导出(只读)
		Refunds:  d.paymentService, // OPS-005:退款 CSV 导出(只读)
	})
	r.Route("/v1/admin/payments", func(r chi.Router) {
		paymenthttp.MountPaymentAdminRoutes(r, paymenthttp.AdminDeps{
			Auth:           d.adminAuth,
			Service:        d.paymentService,
			ProviderConfig: paymentProviderConfigRouteService(d),
			RefundRequests: d.paymentRefundRequests,
		})
	})
	r.Route("/v1/admin/cache-price-overrides", func(r chi.Router) {
		paymenthttp.MountCacheOverrideAdminRoutes(r, paymenthttp.CacheOverrideAdminDeps{
			Auth:  d.adminAuth,
			Store: d.cacheOverrideStore,
		})
	})
	r.Route("/v1/admin/subscriptions", func(r chi.Router) {
		subscriptionhttp.MountSubscriptionAdminRoutes(r, subscriptionhttp.AdminDeps{
			Auth:           d.adminAuth,
			Service:        d.subscriptionService,
			VoucherService: d.voucherService,
		})
	})
	r.Get("/v1/admin/referrals", referralhttp.NewAdminReferralsHandler(referralhttp.Deps{
		Service:   d.invitationService,
		AdminAuth: d.adminAuth,
	}))
	r.Get("/v1/admin/referrals/rewards", referralhttp.NewAdminReferralRewardsHandler(referralhttp.Deps{
		Service:   d.invitationService,
		AdminAuth: d.adminAuth,
	}))
	r.Get("/v1/admin/referrals/overview", referralhttp.NewAdminReferralOverviewHandler(referralhttp.Deps{
		Service:   d.invitationService,
		AdminAuth: d.adminAuth,
	}))
	disputeAdminDeps := controlhttp.DisputeAdminDeps{
		Auth:  d.adminAuth,
		Store: d.disputeStore,
	}
	adminListDisputesHandler := controlhttp.NewAdminListDisputesHandler(disputeAdminDeps)
	r.Get("/v1/admin/disputes", adminListDisputesHandler)
	r.Route("/v1/admin/disputes", func(r chi.Router) {
		r.Get("/", adminListDisputesHandler)
		r.Post("/{id}/resolve", controlhttp.NewAdminResolveDisputeHandler(disputeAdminDeps))
	})
	mountNotificationRoutes(r, d)
	announcementhttp.MountAdminRoutes(r, announcementhttp.AdminDeps{
		Auth:    d.adminAuth,
		Service: d.announcementService,
	})
	r.Route("/v1/admin/routes", func(r chi.Router) {
		controlhttp.MountRouteAdminRoutes(r, controlhttp.RouteAdminDeps{
			Auth:    d.adminAuth,
			Service: d.routeAdminService,
		})
	})
	r.Route("/v1/admin/tls-fingerprint-profiles", func(r chi.Router) {
		tlsfphttp.MountTLSFPAdminRoutes(r, tlsfphttp.AdminDeps{
			Auth:    d.adminAuth,
			Service: tlsfpadmin.New(d.adminQueries),
		})
	})
	mountAlertingAdminRoutes(r, d)
	mountRiskAdminRoutes(r, d)
	mountModerationAdminRoutes(r, d)
	r.Get("/admin/v1/usage", gatewayhttp.NewUsageHandler(d))
	r.Get("/admin/v1/billing/claims", gatewayhttp.NewClaimsHandler(d))
	r.Get("/admin/v1/audit-events", gatewayhttp.NewAuditEventsHandler(d))
	r.Get("/admin/v1/dlq/{handler}", gatewayhttp.NewAdminDLQListHandler(d))
	r.Post("/admin/v1/dlq/{id}/replay", gatewayhttp.NewAdminDLQReplayHandler(d))
	r.Post("/admin/v1/usage-record-dlq/{id}/replay", gatewayhttp.NewAdminDLQReplayHandler(d))
	obsDLQDeps := obsdlqhttp.Deps{Auth: d.adminAuth, Store: d.obsDLQAdminStore}
	r.Get("/admin/v1/obs-dlq", obsdlqhttp.NewListHandler(obsDLQDeps))
	r.Post("/admin/v1/obs-dlq/{id}/replay", obsdlqhttp.NewReplayHandler(obsDLQDeps))
	r.Route("/admin/v1/cache/l2", func(r chi.Router) {
		gatewayhttp.MountAdminL2CacheRoutes(r, gatewayhttp.AdminL2CacheDeps{
			Auth:  d.adminAuth,
			Store: d.responseCache,
		})
	})
}
