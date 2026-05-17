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
	"github.com/BloomingProsperity/HUAKAI/internal/redact"
)

const (
	defaultChannelHealthListLimit = 50
	maxChannelHealthListLimit     = 200
)

type ChannelHealthAdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type ChannelHealthController interface {
	ListChannelHealth(context.Context, int64, int, int) ([]channelhealth.ChannelHealthState, error)
	GetChannelHealth(context.Context, int64, string) (channelhealth.ChannelHealthState, []channelhealth.AuditEvent, error)
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

func MountChannelHealthReadAdminRoutes(r chi.Router, d ChannelHealthAdminDeps) {
	r.Get("/", newChannelHealthListHandler(d))
	r.Get("/{channel_id}", newChannelHealthDetailHandler(d))
}

func newChannelHealthListHandler(d ChannelHealthAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveChannelHealthAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQueryInt(w, r, "tenant_id")
		if !ok {
			return
		}
		limit, offset, ok := parseChannelHealthPagination(w, r)
		if !ok {
			return
		}
		items, err := d.Controller.ListChannelHealth(r.Context(), tenantID, limit, offset)
		if err != nil {
			writeChannelHealthReadError(w, err, "channel_health_list_failed")
			return
		}
		resp := make([]map[string]any, 0, len(items))
		for _, item := range items {
			resp = append(resp, channelHealthResponse(item))
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{
			"items":  resp,
			"limit":  limit,
			"offset": offset,
		})
	}
}

func newChannelHealthDetailHandler(d ChannelHealthAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveChannelHealthAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQueryInt(w, r, "tenant_id")
		if !ok {
			return
		}
		channelID := strings.TrimSpace(chi.URLParam(r, "channel_id"))
		if channelID == "" {
			writeJSONError(w, http.StatusBadRequest, "invalid_channel_id", "channel_id is required")
			return
		}
		state, events, err := d.Controller.GetChannelHealth(r.Context(), tenantID, channelID)
		if err != nil {
			writeChannelHealthReadError(w, err, "channel_health_get_failed")
			return
		}
		auditEvents := make([]map[string]any, 0, len(events))
		for _, ev := range events {
			auditEvents = append(auditEvents, channelHealthAuditEventResponse(ev))
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{
			"state":        channelHealthResponse(state),
			"audit_events": auditEvents,
		})
	}
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

func parseChannelHealthPagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit := defaultChannelHealthListLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > maxChannelHealthListLimit {
			writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
			return 0, 0, false
		}
		limit = n
	}
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_offset", "offset must be non-negative")
			return 0, 0, false
		}
		offset = n
	}
	return limit, offset, true
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
		"score":                 rec.Score,
		"reason_class":          rec.ReasonClass,
		"confidence_tier":       rec.Confidence,
		"policy_version":        rec.PolicyVersion,
		"ramp_stage_pct":        rec.RampStagePct,
		"ramp_failure_count":    rec.RampFailureCount,
	}
	if !rec.StateEnteredAt.IsZero() {
		resp["state_entered_at"] = rec.StateEnteredAt.UTC()
	}
	if !rec.LastTransitionAt.IsZero() {
		resp["last_transition_at"] = rec.LastTransitionAt.UTC()
	}
	if rec.LastSignalClass != "" {
		resp["last_signal_class"] = rec.LastSignalClass
	}
	if rec.LastSignalAt != nil {
		resp["last_signal_at"] = rec.LastSignalAt.UTC()
	}
	if !rec.UpdatedAt.IsZero() {
		resp["updated_at"] = rec.UpdatedAt.UTC()
	}
	if rec.CooldownUntil != nil {
		resp["cooldown_until"] = rec.CooldownUntil.UTC()
	}
	if rec.RampStartedAt != nil {
		resp["ramp_started_at"] = rec.RampStartedAt.UTC()
	}
	return resp
}

func channelHealthAuditEventResponse(ev channelhealth.AuditEvent) map[string]any {
	resp := map[string]any{
		"event_type":            ev.Type,
		"tenant_id":             ev.Key.TenantID,
		"channel_id":            ev.Key.StableChannelID(),
		"vendor":                ev.Key.Vendor,
		"provider_account_id":   ev.Key.ProviderAccountID,
		"account_credential_id": ev.Key.AccountCredentialID,
		"credential_version":    ev.Key.CredentialVersion,
		"new_state":             ev.NewState,
		"reason_class":          ev.ReasonClass,
		"policy_version":        ev.PolicyVersion,
		"payload":               redact.RedactForAudience(ev.Payload, redact.AudienceInternal),
	}
	if ev.PreviousState != "" {
		resp["previous_state"] = ev.PreviousState
	}
	if ev.RequestID != "" {
		resp["request_id"] = ev.RequestID
	}
	if ev.ActorID != "" {
		resp["actor_id"] = ev.ActorID
	}
	if !ev.OccurredAt.IsZero() {
		resp["occurred_at"] = ev.OccurredAt.UTC()
	}
	return resp
}

func writeChannelHealthReadError(w http.ResponseWriter, err error, code string) {
	if errors.Is(err, channelhealth.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "channel_health_not_found", "channel health state not found")
		return
	}
	writeJSONError(w, http.StatusServiceUnavailable, code, err.Error())
}
