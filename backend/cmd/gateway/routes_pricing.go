package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcataloghttp"
)

func mountPricingCatalogRoutes(r chi.Router, d *deps) {
	store := d.pricingRatioStore
	if store == nil {
		store = pricingcatalog.NewPostgresStoreWithAuditSigner(d.pgPool, d.auditSigner)
	}
	r.Route("/admin/v1/pricing/ratios", func(r chi.Router) {
		pricingcataloghttp.MountPricingRatioRoutes(r, pricingcataloghttp.AdminPricingRatioDeps{
			Auth:     d.adminAuth,
			Store:    store,
			Resolver: d.pricingRatioResolver,
		})
	})
}
