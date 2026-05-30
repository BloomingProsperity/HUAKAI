package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/adminhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeshttp"
	"github.com/BloomingProsperity/HUAKAI/internal/paymenthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/routeadminhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/trusthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/userkeyhttp"
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
	r.Get("/.well-known/huakai-pubkey.json", trusthttp.NewWellKnownHandler(trusthttp.WellKnownDeps{Signer: d.auditSigner, Registry: d.auditPubkeyRegistry}))
	r.Post("/v1/trust/verify", trusthttp.NewVerifyHandler(trusthttp.VerifyDeps{Signer: d.auditSigner, Registry: d.auditPubkeyRegistry}).ServeHTTP)
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
	r.Route("/v1/users/me/payments", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions))
		paymenthttp.MountPaymentUserRoutes(r, paymenthttp.UserDeps{Service: d.paymentService})
	})
	r.Route("/v1/users/me/subscriptions", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions))
		subscriptionhttp.MountSubscriptionUserRoutes(r, subscriptionhttp.UserDeps{Service: d.subscriptionService})
	})
	// 公开支付回调端点 (P2a): 无 session/admin 中间件, 信任靠验签; 复用 d.paymentService。
	paymenthttp.MountPaymentWebhookRoutes(r, paymenthttp.WebhookDeps{Service: d.paymentService})
	r.Route("/v1/api-keys", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions))
		userkeyhttp.MountUserAPIKeyRoutes(r, userkeyhttp.Deps{Service: d.userKeyService})
	})
	if d.hermesService != nil && d.hermesRunner != nil {
		r.With(hermeshttp.APIKeyMiddleware(d.inboundAuth)).
			Mount("/v1/hermes", hermeshttp.NewRouter(d.hermesService, d.hermesRunner, d.hermesChatBridge))
	}
	r.Post("/internal/runner/bootstrap", d.handleRunnerBootstrap)
	r.Post("/internal/runner/refresh", d.handleRunnerRefresh)
	r.Get("/internal/keys", d.handleRunnerKeys)
	r.With(auth.SessionMiddleware(d.userSessions)).Post("/v1/invitations", gatewayhttp.NewInvitationCreateHandler(gatewayhttp.InvitationDeps{
		Service: d.invitationService,
	}))

	mountAdminRoutes(r, d)
	logger.Info("routes mounted")
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
		AuditRefPolicy:        d.auditRefPolicy,
		AuditLedger:           d.auditLedger,
		AuditLedgerDLQ:        d.dlqService,
		SettleRecoveryDLQ:     d.dlqService,
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
			Exchangers:      d.credentialExchangers,
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
			Exchangers:      d.credentialExchangers,
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
	r.Route("/v1/admin/vouchers", func(r chi.Router) {
		gatewayhttp.MountVoucherAdminRoutes(r, gatewayhttp.VoucherAdminDeps{
			Auth:    d.adminAuth,
			Service: d.voucherService,
		})
	})
	r.Route("/v1/admin/payments", func(r chi.Router) {
		paymenthttp.MountPaymentAdminRoutes(r, paymenthttp.AdminDeps{
			Auth:    d.adminAuth,
			Service: d.paymentService,
		})
	})
	r.Route("/v1/admin/subscriptions", func(r chi.Router) {
		subscriptionhttp.MountSubscriptionAdminRoutes(r, subscriptionhttp.AdminDeps{
			Auth:           d.adminAuth,
			Service:        d.subscriptionService,
			VoucherService: d.voucherService,
		})
	})
	r.Route("/v1/admin/routes", func(r chi.Router) {
		routeadminhttp.MountRouteAdminRoutes(r, routeadminhttp.AdminDeps{
			Auth:    d.adminAuth,
			Service: d.routeAdminService,
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
