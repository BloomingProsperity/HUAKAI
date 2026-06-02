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

func TestChannelCatalogHandlerRequiresAdminAuth(t *testing.T) {
	queries := newChannelCatalogQueriesStub()
	rec := invokeChannelCatalog(t, AdminChannelCatalogDeps{
		Auth:    apiKeyAuthStub{err: admin.ErrAdminUnauthorized},
		Queries: queries,
	}, "/admin/v1/channels?tenant_id=7")

	assertChannelCatalogStatus(t, rec, http.StatusUnauthorized)
	if queries.listCalls != 0 {
		t.Fatalf("unauthorized request touched channel query: calls=%d", queries.listCalls)
	}
}

func TestChannelCatalogTenantScopeIsEnforced(t *testing.T) {
	t.Run("tenant operator cross tenant returns 403 before query", func(t *testing.T) {
		queries := newChannelCatalogQueriesStub()
		rec := invokeChannelCatalog(t, AdminChannelCatalogDeps{
			Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
			Queries: queries,
		}, "/admin/v1/channels?tenant_id=8")

		assertChannelCatalogStatus(t, rec, http.StatusForbidden)
		if queries.listCalls != 0 {
			t.Fatalf("cross-tenant request touched channel query: calls=%d arg=%+v", queries.listCalls, queries.lastArg)
		}
	})

	t.Run("platform admin tenant_id filters rows", func(t *testing.T) {
		queries := newChannelCatalogQueriesStub()
		rec := invokeChannelCatalog(t, AdminChannelCatalogDeps{
			Auth:    apiKeyAuthStub{ident: platformAdmin()},
			Queries: queries,
		}, "/admin/v1/channels?tenant_id=8")

		assertChannelCatalogStatus(t, rec, http.StatusOK)
		var body channelCatalogListResponse
		decodeChannelCatalogBody(t, rec, &body)
		if body.Object != "admin_channels_list" || len(body.Items) != 1 || body.Items[0].Name != "tenant-8-primary" {
			t.Fatalf("platform admin response was not scoped to tenant 8: %+v", body)
		}
		if queries.lastArg.TenantID != 8 {
			t.Fatalf("channel query tenant=%d want 8", queries.lastArg.TenantID)
		}
	})

	t.Run("platform admin must pass tenant_id", func(t *testing.T) {
		queries := newChannelCatalogQueriesStub()
		rec := invokeChannelCatalog(t, AdminChannelCatalogDeps{
			Auth:    apiKeyAuthStub{ident: platformAdmin()},
			Queries: queries,
		}, "/admin/v1/channels")

		assertChannelCatalogStatus(t, rec, http.StatusBadRequest)
		if queries.listCalls != 0 {
			t.Fatalf("missing tenant_id touched channel query: calls=%d", queries.listCalls)
		}
	})
}

func TestChannelCatalogResponseWhitelistAndPagination(t *testing.T) {
	t.Run("response contains only safe channel fields", func(t *testing.T) {
		queries := newChannelCatalogQueriesStub()
		rec := invokeChannelCatalog(t, AdminChannelCatalogDeps{
			Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
			Queries: queries,
		}, "/admin/v1/channels")

		assertChannelCatalogStatus(t, rec, http.StatusOK)
		raw := rec.Body.String()
		for _, forbidden := range []string{"credential", "credentials", "secret", "key_hash", "tenant_id"} {
			if strings.Contains(strings.ToLower(raw), forbidden) {
				t.Fatalf("channel catalog leaked forbidden field %q: %s", forbidden, raw)
			}
		}

		var body struct {
			Object string           `json:"object"`
			Items  []map[string]any `json:"items"`
			Limit  int32            `json:"limit"`
			Offset int32            `json:"offset"`
		}
		decodeChannelCatalogBody(t, rec, &body)
		if body.Object != "admin_channels_list" || body.Limit != 50 || body.Offset != 0 || len(body.Items) != 2 {
			t.Fatalf("channel catalog envelope mismatch: %+v", body)
		}
		allowed := map[string]bool{
			"id": true, "pool_group_id": true, "name": true,
			"failover_status_codes": true, "enabled": true, "created_at": true,
		}
		for key := range body.Items[0] {
			if !allowed[key] {
				t.Fatalf("channel catalog item exposed non-whitelisted field %q in %+v", key, body.Items[0])
			}
		}
	})

	t.Run("limit range is strict and offset changes page", func(t *testing.T) {
		for _, target := range []string{"/admin/v1/channels?limit=0", "/admin/v1/channels?limit=501"} {
			queries := newChannelCatalogQueriesStub()
			rec := invokeChannelCatalog(t, AdminChannelCatalogDeps{
				Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
				Queries: queries,
			}, target)
			assertChannelCatalogStatus(t, rec, http.StatusBadRequest)
			if queries.listCalls != 0 {
				t.Fatalf("invalid limit %q touched channel query: calls=%d", target, queries.listCalls)
			}
		}

		queries := newChannelCatalogQueriesStub()
		rec := invokeChannelCatalog(t, AdminChannelCatalogDeps{
			Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
			Queries: queries,
		}, "/admin/v1/channels?limit=1&offset=1")

		assertChannelCatalogStatus(t, rec, http.StatusOK)
		var body channelCatalogListResponse
		decodeChannelCatalogBody(t, rec, &body)
		if body.Limit != 1 || body.Offset != 1 || len(body.Items) != 1 || body.Items[0].Name != "tenant-7-fallback" {
			t.Fatalf("channel pagination did not return second tenant row: %+v", body)
		}
		if queries.lastArg.PageLimit != 1 || queries.lastArg.PageOffset != 1 {
			t.Fatalf("channel pagination params mismatch: %+v", queries.lastArg)
		}
	})
}

type channelCatalogQueriesStub struct {
	rowsByTenant map[int64][]admindb.ListAdminChannelsByTenantRow
	err          error
	lastArg      admindb.ListAdminChannelsByTenantParams
	listCalls    int
}

func newChannelCatalogQueriesStub() *channelCatalogQueriesStub {
	created := pgTimestamp(time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC))
	return &channelCatalogQueriesStub{rowsByTenant: map[int64][]admindb.ListAdminChannelsByTenantRow{
		7: {
			{ID: 701, PoolGroupID: 70, Name: "tenant-7-primary", FailoverStatusCodes: []int32{401, 429}, Enabled: true, CreatedAt: created},
			{ID: 702, PoolGroupID: 70, Name: "tenant-7-fallback", FailoverStatusCodes: []int32{500, 529}, Enabled: false, CreatedAt: created},
		},
		8: {
			{ID: 801, PoolGroupID: 80, Name: "tenant-8-primary", FailoverStatusCodes: []int32{403, 429}, Enabled: true, CreatedAt: created},
		},
	}}
}

func (s *channelCatalogQueriesStub) ListAdminChannelsByTenant(_ context.Context, arg admindb.ListAdminChannelsByTenantParams) ([]admindb.ListAdminChannelsByTenantRow, error) {
	s.listCalls++
	s.lastArg = arg
	if s.err != nil {
		return nil, s.err
	}
	rows := append([]admindb.ListAdminChannelsByTenantRow(nil), s.rowsByTenant[arg.TenantID]...)
	return sliceChannelCatalogRows(rows, arg.PageLimit, arg.PageOffset), nil
}

func sliceChannelCatalogRows(rows []admindb.ListAdminChannelsByTenantRow, limit, offset int32) []admindb.ListAdminChannelsByTenantRow {
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

func invokeChannelCatalog(t *testing.T, deps AdminChannelCatalogDeps, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/channels", func(r chi.Router) {
		MountChannelCatalogRoutes(r, deps)
	})
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeChannelCatalogBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode channel catalog body: %v body=%s", err, strings.TrimSpace(rec.Body.String()))
	}
}

func assertChannelCatalogStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, want, strings.TrimSpace(rec.Body.String()))
	}
}
