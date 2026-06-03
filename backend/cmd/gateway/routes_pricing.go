package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcataloghttp"
)

func mountPricingCatalogRoutes(r chi.Router, d *deps) {
	r.Route("/admin/v1/pricing/ratios", func(r chi.Router) {
		pricingcataloghttp.MountPricingRatioRoutes(r, pricingcataloghttp.AdminPricingRatioDeps{
			Auth:  d.adminAuth,
			Store: pricingcatalog.NewPostgresStore(d.pgPool),
		})
	})
}
