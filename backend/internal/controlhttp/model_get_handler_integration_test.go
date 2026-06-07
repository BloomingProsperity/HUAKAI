//go:build integration_pg

package controlhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

func TestIntegrationPGModelGetReturnsSeededModelAndModelNotFound(t *testing.T) {
	ctx := context.Background()
	pool := openControlHTTPIntegrationPool(t, ctx)
	tenantID, cleanup := seedModelGetIntegrationRows(t, ctx, pool, "gpt-x")
	t.Cleanup(cleanup)

	authn := &modelListAuthStub{ident: auth.Identity{TenantID: tenantID, APIKeyID: 11, UserID: 23}}
	catalog := registry.NewPostgresRegistry(pool, nil)
	handler := NewModelGetHandler(ModelListDeps{Auth: authn, Catalog: catalog})
	r := chi.NewRouter()
	r.Get("/v1/models/{model}", handler)

	okRec := httptest.NewRecorder()
	okReq := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-x", nil)
	r.ServeHTTP(okRec, okReq)

	if okRec.Code != http.StatusOK {
		t.Fatalf("GET /v1/models/gpt-x status=%d body=%s want 200", okRec.Code, okRec.Body.String())
	}
	var okBody struct {
		ID     string `json:"id"`
		Object string `json:"object"`
	}
	if err := json.Unmarshal(okRec.Body.Bytes(), &okBody); err != nil {
		t.Fatalf("decode ok body: %v body=%s", err, okRec.Body.String())
	}
	if okBody.ID != "gpt-x" || okBody.Object != "model" {
		t.Fatalf("ok body=%+v want id gpt-x object model", okBody)
	}

	missRec := httptest.NewRecorder()
	missReq := httptest.NewRequest(http.MethodGet, "/v1/models/nope", nil)
	r.ServeHTTP(missRec, missReq)

	if missRec.Code != http.StatusNotFound {
		t.Fatalf("GET /v1/models/nope status=%d body=%s want 404", missRec.Code, missRec.Body.String())
	}
	assertModelListErrorCode(t, missRec.Body.Bytes(), "model_not_found")
}

func TestAliasBulkImport(t *testing.T) {
	ctx := context.Background()
	pool := openControlHTTPIntegrationPool(t, ctx)
	tenantID, modelID, cleanup := seedAliasBulkImportRows(t, ctx, pool)
	t.Cleanup(cleanup)

	catalog := registry.NewPostgresRegistry(pool, nil)
	adminRouter := chi.NewRouter()
	adminRouter.Post("/v1/admin/models/aliases/bulk-import",
		NewAdminModelAliasBulkImportHandler(AdminModelAliasesDeps{Store: catalog}))
	importBody := `{"aliases":[` +
		`{"tenant_id":` + jsonInt(tenantID) + `,"model_id":` + jsonInt(modelID) + `,"alias":"bulk-one"},` +
		`{"tenant_id":` + jsonInt(tenantID) + `,"model_id":` + jsonInt(modelID) + `,"alias":"bulk-two"}` +
		`]}`
	importRec := httptest.NewRecorder()
	importReq := httptest.NewRequest(http.MethodPost, "/v1/admin/models/aliases/bulk-import", strings.NewReader(importBody))
	importReq.Header.Set("Content-Type", "application/json")
	adminRouter.ServeHTTP(importRec, importReq)
	if importRec.Code != http.StatusOK {
		t.Fatalf("bulk import status=%d body=%s want 200", importRec.Code, importRec.Body.String())
	}

	authn := &modelListAuthStub{ident: auth.Identity{TenantID: tenantID, APIKeyID: 11, UserID: 23}}
	getRouter := chi.NewRouter()
	getRouter.Get("/v1/models/{model}", NewModelGetHandler(ModelListDeps{Auth: authn, Catalog: catalog}))
	for _, alias := range []string{"bulk-one", "bulk-two"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/models/"+alias, nil)
		getRouter.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET alias %q status=%d body=%s want 200; MUTATION: importing only first row leaves second alias missing", alias, rec.Code, rec.Body.String())
		}
	}
}

func openControlHTTPIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedAliasBulkImportRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, int64, func()) {
	t.Helper()
	suffix := uuid.NewString()
	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"alias-import-tenant-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "alias-import-pool-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool_group: %v", err)
	}
	var modelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO models (tenant_id, scope, canonical_id, protocol_family,
		                     default_provider_model_id, default_context_window,
		                     default_request_timeout_ms, pricing_class, status)
		 VALUES ($1, 'tenant', $2, 'openai_chat', $3, 128000, 60000, 'standard', 'active')
		 RETURNING id`,
		tenantID, "openai/alias-import-"+suffix, "alias-import-"+suffix,
	).Scan(&modelID); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO model_pool_bindings
		   (tenant_id, model_id, pool_group_id, priority, weight, selection_mode,
		    fallback_class, enabled, reason)
		 VALUES ($1, $2, $3, 100, 1, 'strict_priority', 'normal', true, 'primary')`,
		tenantID, modelID, poolGroupID,
	); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	return tenantID, modelID, func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM model_pool_bindings WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM model_aliases WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM models WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}
}

func jsonInt(v int64) string {
	return strconv.FormatInt(v, 10)
}

func seedModelGetIntegrationRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, alias string) (int64, func()) {
	t.Helper()
	suffix := uuid.NewString()
	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"model-get-tenant-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "model-get-pool-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool_group: %v", err)
	}
	var modelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO models (tenant_id, scope, canonical_id, protocol_family,
		                     default_provider_model_id, default_context_window,
		                     default_request_timeout_ms, pricing_class, status)
		 VALUES ($1, 'tenant', $2, 'openai_chat', $3, 128000, 60000, 'standard', 'active')
		 RETURNING id`,
		tenantID, "openai/"+alias+"-"+suffix, alias,
	).Scan(&modelID); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO model_aliases
		   (tenant_id, scope, model_id, public_alias_normalized, public_alias_display, status)
		 VALUES ($1, 'tenant', $2, $3, $4, 'active')`,
		tenantID, modelID, alias, alias,
	); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO model_pool_bindings
		   (tenant_id, model_id, pool_group_id, priority, weight, selection_mode,
		    fallback_class, enabled, reason)
		 VALUES ($1, $2, $3, 100, 1, 'strict_priority', 'normal', true, 'primary')`,
		tenantID, modelID, poolGroupID,
	); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	return tenantID, func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM model_pool_bindings WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM model_aliases WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM models WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}
}
