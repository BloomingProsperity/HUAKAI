package userkeycontrolshttp

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/userkeycontrols"
)

type ControlsService interface {
	SetKeyQuota(context.Context, userkeycontrols.SetKeyQuotaRequest) (userkeycontrols.SetKeyQuotaResult, error)
	GetKeyQuota(context.Context, int64, int64, int64) (userkeycontrols.KeyQuotaView, error)
	SetKeyGroup(context.Context, userkeycontrols.SetKeyGroupRequest) (userkeycontrols.SetKeyGroupResult, error)
	GetKeyGroup(context.Context, int64, int64, int64) (userkeycontrols.KeyGroupView, error)
	SetKeyIPAllowlist(context.Context, userkeycontrols.SetKeyIPAllowlistRequest) (userkeycontrols.SetKeyIPAllowlistResult, error)
	GetKeyIPAllowlist(context.Context, int64, int64, int64) (userkeycontrols.KeyIPAllowlistView, error)
}

type Deps struct {
	Service ControlsService
}

func MountRoutes(r chi.Router, d Deps) {
	r.Put("/{id}/quota", newSetQuotaHandler(d))
	r.Get("/{id}/quota", newGetQuotaHandler(d))
	r.Put("/{id}/group", newSetGroupHandler(d))
	r.Get("/{id}/group", newGetGroupHandler(d))
	r.Put("/{id}/ip-allowlist", newSetIPAllowlistHandler(d))
	r.Get("/{id}/ip-allowlist", newGetIPAllowlistHandler(d))
}
