package alertinghttp

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
)

type Service interface {
	CreateRule(context.Context, alerting.CreateRuleInput) (alerting.AlertRule, error)
	UpdateRule(context.Context, alerting.UpdateRuleInput) (alerting.AlertRule, error)
	DeleteRule(context.Context, int64, int64) error
	GetRule(context.Context, int64, int64) (alerting.AlertRule, error)
	ListRules(context.Context, alerting.ListRulesInput) ([]alerting.AlertRule, error)
	ListEvents(context.Context, alerting.ListEventsInput) ([]alerting.AlertEvent, error)
	ManualResolveEvent(context.Context, int64, int64) (alerting.AlertEvent, error)
	CreateSilence(context.Context, alerting.CreateSilenceInput) (alerting.AlertSilence, error)
	DeleteSilence(context.Context, int64, int64) error
	ListSilences(context.Context, alerting.ListSilencesInput) ([]alerting.AlertSilence, error)
}

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminDeps struct {
	Auth    AdminAuth
	Service Service
}

func MountAdminRoutes(r chi.Router, deps AdminDeps) {
	r.Get("/v1/admin/alert-rules", newRuleListHandler(deps))
	r.Post("/v1/admin/alert-rules", newRuleCreateHandler(deps))
	r.Get("/v1/admin/alert-rules/{id}", newRuleGetHandler(deps))
	r.Put("/v1/admin/alert-rules/{id}", newRuleUpdateHandler(deps))
	r.Delete("/v1/admin/alert-rules/{id}", newRuleDeleteHandler(deps))
	r.Get("/v1/admin/alert-events", newEventListHandler(deps))
	r.Post("/v1/admin/alert-events/{id}/manual-resolve", newEventManualResolveHandler(deps))
	r.Get("/v1/admin/alert-silences", newSilenceListHandler(deps))
	r.Post("/v1/admin/alert-silences", newSilenceCreateHandler(deps))
	r.Delete("/v1/admin/alert-silences/{id}", newSilenceDeleteHandler(deps))
}
