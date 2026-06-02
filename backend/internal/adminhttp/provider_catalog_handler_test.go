package adminhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestProviderCatalogHandlerRequiresAdminAuth(t *testing.T) {
	queries := newProviderCatalogQueriesStub()
	rec := invokeProviderCatalog(t, AdminProviderCatalogDeps{
		Auth:    apiKeyAuthStub{err: admin.ErrAdminUnauthorized},
		Queries: queries,
	}, "/admin/v1/providers?tenant_id=7")

	assertProviderCatalogStatus(t, rec, http.StatusUnauthorized)
	if queries.listCalls != 0 {
		t.Fatalf("unauthorized request touched provider query: calls=%d", queries.listCalls)
	}
}

func TestProviderCatalogTenantScopeIsEnforced(t *testing.T) {
	t.Run("tenant operator cross tenant returns 403 before query", func(t *testing.T) {
		queries := newProviderCatalogQueriesStub()
		rec := invokeProviderCatalog(t, AdminProviderCatalogDeps{
			Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
			Queries: queries,
		}, "/admin/v1/providers?tenant_id=8")

		assertProviderCatalogStatus(t, rec, http.StatusForbidden)
		if queries.listCalls != 0 {
			t.Fatalf("cross-tenant request touched provider query: calls=%d arg=%+v", queries.listCalls, queries.lastArg)
		}
	})

	t.Run("platform admin tenant_id filters rows", func(t *testing.T) {
		queries := newProviderCatalogQueriesStub()
		rec := invokeProviderCatalog(t, AdminProviderCatalogDeps{
			Auth:    apiKeyAuthStub{ident: platformAdmin()},
			Queries: queries,
		}, "/admin/v1/providers?tenant_id=8")

		assertProviderCatalogStatus(t, rec, http.StatusOK)
		var body providerCatalogListResponse
		decodeProviderCatalogBody(t, rec, &body)
		if body.Object != "admin_providers_list" || len(body.Items) != 1 || body.Items[0].Code != "openai" {
			t.Fatalf("platform admin response was not scoped to tenant 8: %+v", body)
		}
		if queries.lastArg.TenantID != 8 {
			t.Fatalf("provider query tenant=%d want 8", queries.lastArg.TenantID)
		}
	})

	t.Run("platform admin must pass tenant_id", func(t *testing.T) {
		queries := newProviderCatalogQueriesStub()
		rec := invokeProviderCatalog(t, AdminProviderCatalogDeps{
			Auth:    apiKeyAuthStub{ident: platformAdmin()},
			Queries: queries,
		}, "/admin/v1/providers")

		assertProviderCatalogStatus(t, rec, http.StatusBadRequest)
		if queries.listCalls != 0 {
			t.Fatalf("missing tenant_id touched provider query: calls=%d", queries.listCalls)
		}
	})
}

func TestProviderCatalogResponseWhitelistAndPagination(t *testing.T) {
	t.Run("response contains only safe provider fields", func(t *testing.T) {
		queries := newProviderCatalogQueriesStub()
		rec := invokeProviderCatalog(t, AdminProviderCatalogDeps{
			Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
			Queries: queries,
		}, "/admin/v1/providers")

		assertProviderCatalogStatus(t, rec, http.StatusOK)
		raw := rec.Body.String()
		for _, forbidden := range []string{"credential", "credentials", "secret", "key_hash", "tenant_id"} {
			if strings.Contains(strings.ToLower(raw), forbidden) {
				t.Fatalf("provider catalog leaked forbidden field %q: %s", forbidden, raw)
			}
		}

		var body struct {
			Object string           `json:"object"`
			Items  []map[string]any `json:"items"`
			Limit  int32            `json:"limit"`
			Offset int32            `json:"offset"`
		}
		decodeProviderCatalogBody(t, rec, &body)
		if body.Object != "admin_providers_list" || body.Limit != 50 || body.Offset != 0 || len(body.Items) != 2 {
			t.Fatalf("provider catalog envelope mismatch: %+v", body)
		}
		allowed := map[string]bool{
			"id": true, "code": true, "display_name": true,
			"upstream_protocol": true, "enabled": true, "created_at": true,
		}
		for key := range body.Items[0] {
			if !allowed[key] {
				t.Fatalf("provider catalog item exposed non-whitelisted field %q in %+v", key, body.Items[0])
			}
		}
	})

	t.Run("limit range is strict and offset changes page", func(t *testing.T) {
		for _, target := range []string{"/admin/v1/providers?limit=0", "/admin/v1/providers?limit=501"} {
			queries := newProviderCatalogQueriesStub()
			rec := invokeProviderCatalog(t, AdminProviderCatalogDeps{
				Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
				Queries: queries,
			}, target)
			assertProviderCatalogStatus(t, rec, http.StatusBadRequest)
			if queries.listCalls != 0 {
				t.Fatalf("invalid limit %q touched provider query: calls=%d", target, queries.listCalls)
			}
		}

		queries := newProviderCatalogQueriesStub()
		rec := invokeProviderCatalog(t, AdminProviderCatalogDeps{
			Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
			Queries: queries,
		}, "/admin/v1/providers?limit=1&offset=1")

		assertProviderCatalogStatus(t, rec, http.StatusOK)
		var body providerCatalogListResponse
		decodeProviderCatalogBody(t, rec, &body)
		if body.Limit != 1 || body.Offset != 1 || len(body.Items) != 1 || body.Items[0].Code != "bedrock" {
			t.Fatalf("provider pagination did not return second tenant row: %+v", body)
		}
		if queries.lastArg.PageLimit != 1 || queries.lastArg.PageOffset != 1 {
			t.Fatalf("provider pagination params mismatch: %+v", queries.lastArg)
		}
	})
}

type providerCatalogQueriesStub struct {
	rowsByTenant map[int64][]admindb.ListAdminProvidersByTenantRow
	err          error
	lastArg      admindb.ListAdminProvidersByTenantParams
	listCalls    int
}

func newProviderCatalogQueriesStub() *providerCatalogQueriesStub {
	created := pgTimestamp(time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC))
	return &providerCatalogQueriesStub{rowsByTenant: map[int64][]admindb.ListAdminProvidersByTenantRow{
		7: {
			{ID: 701, Code: "anthropic", DisplayName: "Anthropic", UpstreamProtocol: "anthropic_messages", Enabled: true, CreatedAt: created},
			{ID: 702, Code: "bedrock", DisplayName: "Bedrock", UpstreamProtocol: "bedrock", Enabled: false, CreatedAt: created},
		},
		8: {
			{ID: 801, Code: "openai", DisplayName: "OpenAI", UpstreamProtocol: "openai_chat", Enabled: true, CreatedAt: created},
		},
	}}
}

func (s *providerCatalogQueriesStub) ListAdminProvidersByTenant(_ context.Context, arg admindb.ListAdminProvidersByTenantParams) ([]admindb.ListAdminProvidersByTenantRow, error) {
	s.listCalls++
	s.lastArg = arg
	if s.err != nil {
		return nil, s.err
	}
	rows := append([]admindb.ListAdminProvidersByTenantRow(nil), s.rowsByTenant[arg.TenantID]...)
	return sliceProviderCatalogRows(rows, arg.PageLimit, arg.PageOffset), nil
}

func sliceProviderCatalogRows(rows []admindb.ListAdminProvidersByTenantRow, limit, offset int32) []admindb.ListAdminProvidersByTenantRow {
	start := int(offset)
	if start > len(rows) {
		start = len(rows)
	}
	end := start + int(limit)
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end]
}

func invokeProviderCatalog(t *testing.T, deps AdminProviderCatalogDeps, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/providers", func(r chi.Router) {
		MountProviderCatalogRoutes(r, deps)
	})
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeProviderCatalogBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode provider catalog body: %v body=%s", err, strings.TrimSpace(rec.Body.String()))
	}
}

func assertProviderCatalogStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, want, strings.TrimSpace(rec.Body.String()))
	}
}
