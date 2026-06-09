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
	SetKeyModelAllowlist(context.Context, userkeycontrols.SetKeyModelAllowlistRequest) (userkeycontrols.SetKeyModelAllowlistResult, error)
	GetKeyModelAllowlist(context.Context, int64, int64, int64) (userkeycontrols.KeyModelAllowlistView, error)
	// KEY-016: IP blacklist (parallel to allowlist)
	SetKeyIPBlacklist(context.Context, userkeycontrols.SetKeyIPBlacklistRequest) (userkeycontrols.SetKeyIPBlacklistResult, error)
	GetKeyIPBlacklist(context.Context, int64, int64, int64) (userkeycontrols.KeyIPBlacklistView, error)
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
	r.Put("/{id}/ip-blacklist", newSetIPBlacklistHandler(d))
	r.Get("/{id}/ip-blacklist", newGetIPBlacklistHandler(d))
	r.Put("/{id}/model-allowlist", newSetModelAllowlistHandler(d))
	r.Get("/{id}/model-allowlist", newGetModelAllowlistHandler(d))
}
