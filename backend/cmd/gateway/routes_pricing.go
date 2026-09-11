package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/modelrate"
	"github.com/BloomingProsperity/HUAKAI/internal/modelratehttp"
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
	modelStore := d.modelRateStore
	if modelStore == nil && d.pgPool != nil {
		modelStore = modelrate.NewPostgresStore(d.pgPool, d.auditSigner)
	}
	version := ""
	if d.cfg != nil {
		version = d.cfg.BillingPolicyVersion
	}
	r.Route("/admin/v1/pricing/models", func(r chi.Router) {
		modelratehttp.MountRoutes(r, modelratehttp.Deps{
			Auth:                 d.adminAuth,
			Store:                modelStore,
			Official:             d.rateTableSource,
			BillingPolicyVersion: version,
		})
	})
}
