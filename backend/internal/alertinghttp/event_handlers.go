package alertinghttp

import (
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
)

type eventResponse struct {
	ID            int64   `json:"id"`
	TenantID      int64   `json:"tenant_id"`
	RuleID        int64   `json:"rule_id"`
	State         string  `json:"state"`
	ObservedValue float64 `json:"observed_value"`
	FiredAt       string  `json:"fired_at"`
	ResolvedAt    *string `json:"resolved_at,omitempty"`
}

type eventListResponse struct {
	Object string          `json:"object"`
	Items  []eventResponse `json:"items"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

func newEventListHandler(deps AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		tenantID, ok := tenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		ruleID, ok := parseOptionalRuleID(w, r)
		if !ok {
			return
		}
		state := alerting.EventState(strings.TrimSpace(r.URL.Query().Get("state")))
		limit, offset, ok := parsePage(w, r)
		if !ok {
			return
		}
		events, err := deps.Service.ListEvents(r.Context(), alerting.ListEventsInput{
			TenantID: tenantID,
			RuleID:   ruleID,
			State:    state,
			Limit:    limit,
			Offset:   offset,
		})
		if err != nil {
			writeAlertingError(w, err, "alert_event_list_failed")
			return
		}
		writeJSON(w, http.StatusOK, eventListResponse{
			Object: "alert_events_list",
			Items:  eventResponses(events),
			Limit:  limit,
			Offset: offset,
		})
	}
}

func eventResponses(events []alerting.AlertEvent) []eventResponse {
	out := make([]eventResponse, 0, len(events))
	for _, event := range events {
		out = append(out, eventFromValue(event))
	}
	return out
}

func eventFromValue(event alerting.AlertEvent) eventResponse {
	return eventResponse{
		ID:            event.ID,
		TenantID:      event.TenantID,
		RuleID:        event.RuleID,
		State:         string(event.State),
		ObservedValue: event.ObservedValue,
		FiredAt:       formatTime(event.FiredAt),
		ResolvedAt:    formatTimePtr(event.ResolvedAt),
	}
}
