package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/userkeycontrols"
	"github.com/BloomingProsperity/HUAKAI/internal/userkeycontrolshttp"
)

// mountUserKeyControlsRoutes 期望 r 已被限定到现有受 session 保护的用户
// API key 路由组内的 /v1/api-keys 之下。
func mountUserKeyControlsRoutes(r chi.Router, d *deps) {
	var controlsSvc userkeycontrolshttp.ControlsService
	if d != nil {
		controlsSvc = userkeycontrols.NewKeyControlService(d.pgPool, nil, userkeycontrols.WithProgressReader(quota.NewPostgresStore(d.pgPool)))
	}
	userkeycontrolshttp.MountRoutes(r, userkeycontrolshttp.Deps{Service: controlsSvc})
}
