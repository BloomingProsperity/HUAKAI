package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/adminhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/adminuserhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/announcementhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/audiohttp"
	"github.com/BloomingProsperity/HUAKAI/internal/auditexporthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/captcha"
	"github.com/BloomingProsperity/HUAKAI/internal/checkinhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/completionshttp"
	"github.com/BloomingProsperity/HUAKAI/internal/controlhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/embeddingshttp"
	"github.com/BloomingProsperity/HUAKAI/internal/engineembeddingsalias"
	"github.com/BloomingProsperity/HUAKAI/internal/exporthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/geminihttp"
	"github.com/BloomingProsperity/HUAKAI/internal/healthhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeshttp"
	"github.com/BloomingProsperity/HUAKAI/internal/imageshttp"
	"github.com/BloomingProsperity/HUAKAI/internal/invoicehttp"
	"github.com/BloomingProsperity/HUAKAI/internal/mediataskhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/meexporthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/mequotahttp"
	"github.com/BloomingProsperity/HUAKAI/internal/meusagehttp"
	"github.com/BloomingProsperity/HUAKAI/internal/mjclient"
	"github.com/BloomingProsperity/HUAKAI/internal/passkeyhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/paymenthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingpublichttp"
	"github.com/BloomingProsperity/HUAKAI/internal/publicrankinghttp"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/referralhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/rerankhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/responsescompacthttp"
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

// mountRoutes wires the HTTP routes per docs/openapi/openapi.yaml.
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
	geminiV1BetaHandler := geminihttp.NewGenerateContentHandler(geminihttp.NewDeps(chatHandlerDeps(d), modelListHandler))
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
		auditexporthttp.MountRoutes(r, auditexporthttp.Deps{
			Ledger:   auditExportLedgerFrom(d.auditLedger),
			Registry: d.auditPubkeyRegistry,
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
		r.Route("/passkey", func(r chi.Router) {
			passkeyhttp.MountLoginRoutes(r, passkeyHandlerDeps(d))
		})
		// GET /v1/auth/me 需已认证 session(同块的 login/register 等不需要), 故用 per-route session 中间件,
		// 不另起 /v1/auth Route 组(chi 同前缀重复 Mount 会 panic)。
		controlhttp.MountAuthMeRoutes(r.With(auth.SessionMiddleware(d.userSessions, d.clientIPResolver)), controlhttp.AuthMeDeps{
			Resolver:    d.panelAuthResolver,
			Profiles:    d.userAuth,
			SocialLinks: d.userAuth,
			Sessions:    d.userSessions,
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
		gatewayhttp.MountVoucherUserRoutes(r, gatewayhttp.VoucherUserDeps{Service: d.voucherService, ClientIPResolver: d.clientIPResolver})
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
		r.With(hermeshttp.APIKeyMiddleware(d.inboundAuth)).
			Mount("/v1/hermes", hermeshttp.NewRouterWithDeps(hermeshttp.RouterDeps{
				Service:        d.hermesService,
				Runner:         d.hermesRunner,
				Bridge:         d.hermesChatBridge,
				HeaderSettings: d.platformSettings,
			}))
	}
	r.Post("/internal/runner/bootstrap", d.handleRunnerBootstrap)
	r.Post("/internal/runner/refresh", d.handleRunnerRefresh)
	r.Get("/internal/keys", d.handleRunnerKeys)
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
			captchaTurnstileSecret(),
			&http.Client{Timeout: 10 * time.Second},
		),
		LoginThrottle:     d.loginThrottle,
		TwoFactor:         d.twoFactor,
		TwoFactorSettings: d.platformSettings,
		TelegramBotToken:  strings.TrimSpace(os.Getenv("HUAKAI_TELEGRAM_LOGIN_BOT_TOKEN")),
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
		Auth:                   d.inboundAuth,
		Registry:               d.modelRegistry,
		Router:                 d.routePlanner,
		ClaimGate:              d.claimGate,
		QuotaReserver:          d.quotaReserver,
		RateTables:             d.rateTableSource,
		PricingRatioResolver:   d.pricingRatioResolver,
		CacheOverrideStore:     d.cacheOverrideStore,
		Selector:               d.selector,
		CredentialVault:        d.credentialVault,
		Dispatcher:             d.dispatcher,
		Forwarder:              d.forwarder,
		ResponseCache:          d.responseCache,
		CacheScope:             d.cacheScope,
		Settler:                d.settler,
		ReplayStore:            d.replayStore,
		BillingPolicyResolver:  d.billingPolicyResolver,
		CompletionBus:          d.completionBus,
		AuditRefPolicy:         d.auditRefPolicy,
		AuditLedger:            d.auditLedger,
		AuditLedgerDLQ:         d.dlqService,
		ModerationScreener:     moderationScreener(d),
		SettleRecoveryDLQ:      d.dlqService,
		Signer:                 d.auditSigner,
		ChannelHealth:          d.channelHealth,
		ModelCooldowns:         d.modelCooldowns,
		RateService:            d.upstreamRate,
		RetryBudget:            d.retryBudget,
		CredentialHotRefresher: d.credentialScheduler,
		ModelFallbackSettings:  d.platformSettings,
		BillingPolicyVersion:   d.cfg.BillingPolicyVersion,
		RequestClass:           d.cfg.RequestClass,
		ClientIPResolver:       d.clientIPResolver,
		SessionCapRegistry:     d.sessionCapRegistry,
		RecentReqRing:          d.recentReqRing,
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
		BillingPolicyResolver: d.billingPolicyResolver,
		BillingPolicyVersion:  d.cfg.BillingPolicyVersion,
		RequestClass:          d.cfg.RequestClass,
		ClientIPResolver:      d.clientIPResolver,
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
		BillingPolicyResolver: d.billingPolicyResolver,
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
	mountPlatformSettingsRoutes(r, d)
	mountUsageAdminRoutes(r, d)
	mountSystemHealthRoutes(r, d) // ADMIN-042
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
	r.Route("/admin/v1/api-keys", func(r chi.Router) {
		adminhttp.MountAPIKeyRoutes(r, adminhttp.AdminAPIKeysDeps{
			Auth:    d.adminAuth,
			Issuer:  d.adminIssuer,
			Revoker: d.adminRevoker,
			Queries: d.adminQueries,
		})
	})
	adminUserDeps := adminuserhttp.Deps{
		Auth:             d.adminAuth,
		Store:            d.adminQueries,
		SocialLinks:      d.userAuth,
		UnlockAudit:      adminuserhttp.NewPostgresUnlockAuditStore(d.pgPool),
		TwoFADisabler:    d.twoFactor,
		PasskeyResetter:  d.passkeys,
		UserGroupSetter:  adminuserhttp.NewPostgresUserGroupStore(d.pgPool),
		UserRemarkSetter: adminuserhttp.NewPostgresUserRemarkStore(d.pgPool),
		Unlocker:         d.userAuth,
		Audit:            d.adminQueries,
	}
	r.Get("/admin/v1/users", adminuserhttp.NewListHandler(adminUserDeps))
	r.Route("/admin/v1/users", func(r chi.Router) {
		adminuserhttp.MountRoutes(r, adminUserDeps)
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
	r.Get("/admin/v1/channels", adminhttp.NewChannelCatalogListHandler(adminhttp.AdminChannelCatalogDeps{
		Auth:    d.adminAuth,
		Queries: d.adminQueries,
	}))
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
			Tester:   adminhttp.NewProviderAccountCredentialTester(d.credentialStore, credentialworker.DefaultModeAdapterRegistry()),
		})
		adminhttp.MountProviderAccountHealthRoutes(r, adminhttp.ProviderAccountHealthDeps{
			Auth:          d.adminAuth,
			Store:         d.adminQueries,
			RecentReqRing: d.recentReqRing,
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
		gatewayhttp.MountAdminCredentialAcquisitionRoutes(r, gatewayhttp.AdminCredentialAcquisitionDeps{
			Auth:              d.adminAuth,
			Sessions:          d.credentialAcqStore,
			Credentials:       d.credentialStore,
			CredentialAudit:   d.credentialStore,
			AuditStore:        d.adminQueries,
			Exchangers:        d.credentialExchangers,
			BootstrapShortTTL: d.cfg.CredentialAcqBootstrapShortTTL,
			BootstrapLongTTL:  d.cfg.CredentialAcqBootstrapLongTTL,
		})
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
		gatewayhttp.MountAdminCredentialAcquisitionHelperRoutes(r, gatewayhttp.AdminCredentialAcquisitionDeps{
			Auth:              d.adminAuth,
			Sessions:          d.credentialAcqStore,
			Credentials:       d.credentialStore,
			CredentialAudit:   d.credentialStore,
			AuditStore:        d.adminQueries,
			Exchangers:        d.credentialExchangers,
			BootstrapShortTTL: d.cfg.CredentialAcqBootstrapShortTTL,
			BootstrapLongTTL:  d.cfg.CredentialAcqBootstrapLongTTL,
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
		Orders:   d.paymentService, // OPS-005: order CSV export (read-only)
		Refunds:  d.paymentService, // OPS-005: refund CSV export (read-only)
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
	mountModerationAdminRoutes(r, d)
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
