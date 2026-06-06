package alertinghttp

import (
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
)

type ruleCreateRequest struct {
	TenantID      int64   `json:"tenant_id,omitempty"`
	Name          string  `json:"name"`
	Metric        string  `json:"metric"`
	Comparator    string  `json:"comparator"`
	Threshold     float64 `json:"threshold"`
	Severity      string  `json:"severity"`
	WindowSeconds int32   `json:"window_seconds"`
	Enabled       *bool   `json:"enabled,omitempty"`
}

type ruleUpdateRequest struct {
	Name          *string  `json:"name,omitempty"`
	Metric        *string  `json:"metric,omitempty"`
	Comparator    *string  `json:"comparator,omitempty"`
	Threshold     *float64 `json:"threshold,omitempty"`
	Severity      *string  `json:"severity,omitempty"`
	WindowSeconds *int32   `json:"window_seconds,omitempty"`
	Enabled       *bool    `json:"enabled,omitempty"`
}

type ruleResponse struct {
	ID            int64   `json:"id"`
	TenantID      int64   `json:"tenant_id"`
	Name          string  `json:"name"`
	Metric        string  `json:"metric"`
	Comparator    string  `json:"comparator"`
	Threshold     float64 `json:"threshold"`
	Severity      string  `json:"severity"`
	WindowSeconds int32   `json:"window_seconds"`
	Enabled       bool    `json:"enabled"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

type ruleListResponse struct {
	Object string         `json:"object"`
	Items  []ruleResponse `json:"items"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

func newRuleCreateHandler(deps AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		var body ruleCreateRequest
		if !decodeRequest(w, r, &body) {
			return
		}
		tenantID, ok := tenantFromValue(w, ident, body.TenantID)
		if !ok {
			return
		}
		rule, err := deps.Service.CreateRule(r.Context(), alerting.CreateRuleInput{
			TenantID:      tenantID,
			Name:          body.Name,
			Metric:        body.Metric,
			Comparator:    alerting.Comparator(body.Comparator),
			Threshold:     body.Threshold,
			Severity:      alerting.Severity(body.Severity),
			WindowSeconds: body.WindowSeconds,
			Enabled:       body.Enabled,
		})
		if err != nil {
			writeAlertingError(w, err, "alert_rule_create_failed")
			return
		}
		writeJSON(w, http.StatusCreated, ruleFromValue(rule))
	}
}

func newRuleListHandler(deps AdminDeps) http.HandlerFunc {
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
		rules, err := deps.Service.ListRules(r.Context(), alerting.ListRulesInput{
			TenantID: tenantID,
			Limit:    limit,
			Offset:   offset,
		})
		if err != nil {
			writeAlertingError(w, err, "alert_rule_list_failed")
			return
		}
		writeJSON(w, http.StatusOK, ruleListResponse{
			Object: "alert_rules_list",
			Items:  ruleResponses(rules),
			Limit:  limit,
			Offset: offset,
		})
	}
}

func newRuleGetHandler(deps AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		tenantID, ok := tenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r, "rule_id")
		if !ok {
			return
		}
		rule, err := deps.Service.GetRule(r.Context(), tenantID, id)
		if err != nil {
			writeAlertingError(w, err, "alert_rule_get_failed")
			return
		}
		writeJSON(w, http.StatusOK, ruleFromValue(rule))
	}
}

func newRuleUpdateHandler(deps AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		tenantID, ok := tenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r, "rule_id")
		if !ok {
			return
		}
		var body ruleUpdateRequest
		if !decodeRequest(w, r, &body) {
			return
		}
		updated, err := deps.Service.UpdateRule(r.Context(), updateInputFromRequest(tenantID, id, body))
		if err != nil {
			writeAlertingError(w, err, "alert_rule_update_failed")
			return
		}
		writeJSON(w, http.StatusOK, ruleFromValue(updated))
	}
}

func newRuleDeleteHandler(deps AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		tenantID, ok := tenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r, "rule_id")
		if !ok {
			return
		}
		if err := deps.Service.DeleteRule(r.Context(), tenantID, id); err != nil {
			writeAlertingError(w, err, "alert_rule_delete_failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func updateInputFromRequest(tenantID, id int64, body ruleUpdateRequest) alerting.UpdateRuleInput {
	in := alerting.UpdateRuleInput{
		TenantID:      tenantID,
		ID:            id,
		Name:          body.Name,
		Metric:        body.Metric,
		Threshold:     body.Threshold,
		WindowSeconds: body.WindowSeconds,
		Enabled:       body.Enabled,
	}
	if body.Comparator != nil {
		comparator := alerting.Comparator(*body.Comparator)
		in.Comparator = &comparator
	}
	if body.Severity != nil {
		severity := alerting.Severity(*body.Severity)
		in.Severity = &severity
	}
	return in
}

func ruleResponses(rules []alerting.AlertRule) []ruleResponse {
	out := make([]ruleResponse, 0, len(rules))
	for _, rule := range rules {
		out = append(out, ruleFromValue(rule))
	}
	return out
}

func ruleFromValue(rule alerting.AlertRule) ruleResponse {
	return ruleResponse{
		ID:            rule.ID,
		TenantID:      rule.TenantID,
		Name:          rule.Name,
		Metric:        rule.Metric,
		Comparator:    string(rule.Comparator),
		Threshold:     rule.Threshold,
		Severity:      string(rule.Severity),
		WindowSeconds: rule.WindowSeconds,
		Enabled:       rule.Enabled,
		CreatedAt:     formatTime(rule.CreatedAt),
		UpdatedAt:     formatTime(rule.UpdatedAt),
	}
}
