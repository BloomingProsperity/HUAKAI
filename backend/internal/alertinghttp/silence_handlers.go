package alertinghttp

import (
	"net/http"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
)

type silenceCreateRequest struct {
	TenantID int64     `json:"tenant_id,omitempty"`
	RuleID   *int64    `json:"rule_id,omitempty"`
	Reason   string    `json:"reason"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Platform string    `json:"platform,omitempty"`
	GroupID  string    `json:"group_id,omitempty"`
	Region   string    `json:"region,omitempty"`
}

type silenceResponse struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	RuleID    *int64 `json:"rule_id,omitempty"`
	Reason    string `json:"reason"`
	StartsAt  string `json:"starts_at"`
	EndsAt    string `json:"ends_at"`
	Platform  string `json:"platform,omitempty"`
	GroupID   string `json:"group_id,omitempty"`
	Region    string `json:"region,omitempty"`
	CreatedAt string `json:"created_at"`
}

type silenceListResponse struct {
	Object string            `json:"object"`
	Items  []silenceResponse `json:"items"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
}

func newSilenceCreateHandler(deps AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		var body silenceCreateRequest
		if !decodeRequest(w, r, &body) {
			return
		}
		tenantID, ok := tenantFromValue(w, ident, body.TenantID)
		if !ok {
			return
		}
		silence, err := deps.Service.CreateSilence(r.Context(), alerting.CreateSilenceInput{
			TenantID: tenantID,
			RuleID:   body.RuleID,
			Reason:   body.Reason,
			StartsAt: body.StartsAt,
			EndsAt:   body.EndsAt,
			Platform: body.Platform,
			GroupID:  body.GroupID,
			Region:   body.Region,
		})
		if err != nil {
			writeAlertingError(w, err, "alert_silence_create_failed")
			return
		}
		writeJSON(w, http.StatusCreated, silenceFromValue(silence))
	}
}

func newSilenceListHandler(deps AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		tenantID, ok := tenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		limit, offset, ok := parsePage(w, r)
		if !ok {
			return
		}
		silences, err := deps.Service.ListSilences(r.Context(), alerting.ListSilencesInput{
			TenantID: tenantID,
			Limit:    limit,
			Offset:   offset,
		})
		if err != nil {
			writeAlertingError(w, err, "alert_silence_list_failed")
			return
		}
		writeJSON(w, http.StatusOK, silenceListResponse{
			Object: "alert_silences_list",
			Items:  silenceResponses(silences),
			Limit:  limit,
			Offset: offset,
		})
	}
}

func newSilenceDeleteHandler(deps AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		tenantID, ok := tenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r, "silence_id")
		if !ok {
			return
		}
		if err := deps.Service.DeleteSilence(r.Context(), tenantID, id); err != nil {
			writeAlertingError(w, err, "alert_silence_delete_failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func silenceResponses(silences []alerting.AlertSilence) []silenceResponse {
	out := make([]silenceResponse, 0, len(silences))
	for _, silence := range silences {
		out = append(out, silenceFromValue(silence))
	}
	return out
}

func silenceFromValue(silence alerting.AlertSilence) silenceResponse {
	return silenceResponse{
		ID:        silence.ID,
		TenantID:  silence.TenantID,
		RuleID:    int64Ptr(silence.RuleID),
		Reason:    silence.Reason,
		StartsAt:  formatTime(silence.StartsAt),
		EndsAt:    formatTime(silence.EndsAt),
		Platform:  silence.Platform,
		GroupID:   silence.GroupID,
		Region:    silence.Region,
		CreatedAt: formatTime(silence.CreatedAt),
	}
}

func int64Ptr(in *int64) *int64 {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}
