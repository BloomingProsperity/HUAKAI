package gatewayhttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
)

type ChannelHealthAdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type ChannelHealthController interface {
	ManualPause(context.Context, channelhealth.ChannelKey, string, string) (channelhealth.Record, error)
	ManualResume(context.Context, channelhealth.ChannelKey, string, string) (channelhealth.Record, error)
	ForceActive(context.Context, channelhealth.ChannelKey, string, string) (channelhealth.Record, error)
}

type ChannelHealthAdminDeps struct {
	Auth       ChannelHealthAdminAuth
	Controller ChannelHealthController
}

type channelHealthOverrideRequest struct {
	TenantID            int64  `json:"tenant_id"`
	Vendor              string `json:"vendor"`
	AccountCredentialID int64  `json:"account_credential_id"`
	CredentialVersion   int    `json:"credential_version"`
	Reason              string `json:"reason"`
}

func MountChannelHealthAdminRoutes(r chi.Router, d ChannelHealthAdminDeps) {
	r.Post("/{id}/channel-health/pause", newChannelHealthPauseHandler(d))
	r.Post("/{id}/channel-health/resume", newChannelHealthResumeHandler(d))
	r.Post("/{id}/channel-health/force-active", newChannelHealthForceActiveHandler(d))
}

func newChannelHealthPauseHandler(d ChannelHealthAdminDeps) http.HandlerFunc {
	return channelHealthMutationHandler(d, func(ctx context.Context, c ChannelHealthController, key channelhealth.ChannelKey, actorID, reason string) (channelhealth.Record, error) {
		return c.ManualPause(ctx, key, actorID, reason)
	})
}

func newChannelHealthResumeHandler(d ChannelHealthAdminDeps) http.HandlerFunc {
	return channelHealthMutationHandler(d, func(ctx context.Context, c ChannelHealthController, key channelhealth.ChannelKey, actorID, reason string) (channelhealth.Record, error) {
		return c.ManualResume(ctx, key, actorID, reason)
	})
}

func newChannelHealthForceActiveHandler(d ChannelHealthAdminDeps) http.HandlerFunc {
	return channelHealthMutationHandler(d, func(ctx context.Context, c ChannelHealthController, key channelhealth.ChannelKey, actorID, reason string) (channelhealth.Record, error) {
		return c.ForceActive(ctx, key, actorID, reason)
	})
}

func channelHealthMutationHandler(
	d ChannelHealthAdminDeps,
	fn func(context.Context, ChannelHealthController, channelhealth.ChannelKey, string, string) (channelhealth.Record, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveChannelHealthAdmin(w, r, d)
		if !ok {
			return
		}
		providerAccountID, ok := parseAdminPoolID(w, r)
		if !ok {
			return
		}
		var req channelHealthOverrideRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		req.Vendor = strings.TrimSpace(strings.ToLower(req.Vendor))
		req.Reason = strings.TrimSpace(req.Reason)
		key := channelhealth.ChannelKey{
			TenantID:            req.TenantID,
			Vendor:              req.Vendor,
			ProviderAccountID:   providerAccountID,
			AccountCredentialID: req.AccountCredentialID,
			CredentialVersion:   req.CredentialVersion,
		}
		if err := key.Validate(); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_channel_health_subject", err.Error())
			return
		}
		if req.Reason == "" {
			writeJSONError(w, http.StatusBadRequest, "reason_required", "reason is required")
			return
		}
		rec, err := fn(r.Context(), d.Controller, key, strconv.FormatInt(ident.TokenID, 10), req.Reason)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "channel_health_override_failed", err.Error())
			return
		}
		writeAuditJSON(w, http.StatusOK, channelHealthResponse(rec))
	}
}

func resolveChannelHealthAdmin(w http.ResponseWriter, r *http.Request, d ChannelHealthAdminDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Controller == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "channel health admin dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func channelHealthResponse(rec channelhealth.Record) map[string]any {
	resp := map[string]any{
		"tenant_id":             rec.Key.TenantID,
		"provider_account_id":   rec.Key.ProviderAccountID,
		"account_credential_id": rec.Key.AccountCredentialID,
		"credential_version":    rec.Key.CredentialVersion,
		"vendor":                rec.Key.Vendor,
		"channel_id":            rec.Key.StableChannelID(),
		"state":                 rec.State,
		"reason_class":          rec.ReasonClass,
		"policy_version":        rec.PolicyVersion,
		"ramp_stage_pct":        rec.RampStagePct,
		"ramp_failure_count":    rec.RampFailureCount,
	}
	if rec.CooldownUntil != nil {
		resp["cooldown_until"] = rec.CooldownUntil.UTC()
	}
	if rec.RampStartedAt != nil {
		resp["ramp_started_at"] = rec.RampStartedAt.UTC()
	}
	return resp
}
