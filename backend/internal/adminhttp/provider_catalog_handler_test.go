package adminhttp

import (
	"bytes"
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

// Mutation: remove the admin role check or allow unknown roles to mutate; this
// test must turn red with a 2xx/4xx-other response or a store mutation call.
func TestProviderCatalogAdminAuthRequired(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		target string
		body   any
	}{
		{
			name:   "create",
			method: http.MethodPost,
			target: "/admin/v1/providers",
			body: map[string]any{
				"code": "mistral", "display_name": "Mistral",
				"upstream_protocol": "openai_chat", "enabled": true,
			},
		},
		{
			name:   "update",
			method: http.MethodPut,
			target: "/admin/v1/providers/mistral?tenant_id=7",
			body: map[string]any{
				"display_name": "Mistral", "upstream_protocol": "openai_chat", "enabled": true,
			},
		},
		{name: "delete", method: http.MethodDelete, target: "/admin/v1/providers/mistral?tenant_id=7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries := newProviderCatalogQueriesStub()
			rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
				Auth:  apiKeyAuthStub{ident: admin.AdminIdentity{TokenID: 99, Role: "catalog_viewer"}},
				Store: queries,
			}, tc.method, tc.target, tc.body)

			assertProviderCatalogStatus(t, rec, http.StatusForbidden)
			if queries.createCalls != 0 || queries.updateCalls != 0 || queries.deleteCalls != 0 {
				t.Fatalf("non-admin mutation touched store: create=%d update=%d delete=%d",
					queries.createCalls, queries.updateCalls, queries.deleteCalls)
			}
		})
	}
}

// Mutation: drop tenant_id from create uniqueness/scope handling; same-tenant
// duplicate must map to 409, while the same code under another tenant must stay
// a valid create path instead of colliding globally.
func TestCreateProvider_TenantScopedUnique(t *testing.T) {
	t.Run("tenant operator create uses scope tenant and writes audit", func(t *testing.T) {
		queries := newProviderCatalogQueriesStub()
		rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
			Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
			Store: queries,
		}, http.MethodPost, "/admin/v1/providers", map[string]any{
			"code": "mistral", "display_name": "Mistral",
			"upstream_protocol": "openai_chat", "enabled": true,
		})

		assertProviderCatalogStatus(t, rec, http.StatusCreated)
		var body providerCatalogItem
		decodeProviderCatalogBody(t, rec, &body)
		if body.Code != "mistral" || body.DisplayName != "Mistral" || body.UpstreamProtocol != "openai_chat" || !body.Enabled {
			t.Fatalf("created provider body=%+v", body)
		}
		if queries.createCalls != 1 || queries.createArg.TenantID != 7 || queries.createArg.Code != "mistral" {
			t.Fatalf("create arg=%+v calls=%d, want tenant 7 code mistral", queries.createArg, queries.createCalls)
		}
		if queries.createAudit.Action != "create_provider" ||
			queries.createAudit.TargetType != "provider" ||
			queries.createAudit.TenantID == nil || *queries.createAudit.TenantID != 7 ||
			queries.createAudit.ActorID != "12" ||
			!strings.Contains(string(queries.createAudit.Payload), `"code":"mistral"`) {
			t.Fatalf("create audit=%+v payload=%s", queries.createAudit, string(queries.createAudit.Payload))
		}
	})

	t.Run("duplicate code in same tenant is conflict", func(t *testing.T) {
		queries := newProviderCatalogQueriesStub()
		queries.createErr = errProviderCatalogCodeConflict
		rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
			Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
			Store: queries,
		}, http.MethodPost, "/admin/v1/providers", map[string]any{
			"code": "mistral", "display_name": "Mistral duplicate",
			"upstream_protocol": "openai_chat", "enabled": true,
		})

		assertProviderCatalogStatus(t, rec, http.StatusConflict)
		if queries.createCalls != 1 || queries.createArg.TenantID != 7 {
			t.Fatalf("duplicate path arg=%+v calls=%d", queries.createArg, queries.createCalls)
		}
	})

	t.Run("same code in different tenant reaches store with different tenant", func(t *testing.T) {
		queries := newProviderCatalogQueriesStub()
		rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
			Auth:  apiKeyAuthStub{ident: tenantOperator(8)},
			Store: queries,
		}, http.MethodPost, "/admin/v1/providers", map[string]any{
			"code": "mistral", "display_name": "Mistral tenant B",
			"upstream_protocol": "openai_chat", "enabled": true,
		})

		assertProviderCatalogStatus(t, rec, http.StatusCreated)
		if queries.createArg.TenantID != 8 || queries.createArg.Code != "mistral" {
			t.Fatalf("cross-tenant create arg=%+v, want tenant 8 same code", queries.createArg)
		}
	})
}

// Mutation: accept empty code/display name or an unknown upstream protocol; this
// test must turn red by observing a store call for invalid catalog data.
func TestCreateProviderValidatesRequiredFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{
			name: "empty code",
			body: map[string]any{"code": " ", "display_name": "Mistral", "upstream_protocol": "openai_chat", "enabled": true},
		},
		{
			name: "empty display name",
			body: map[string]any{"code": "mistral", "display_name": " ", "upstream_protocol": "openai_chat", "enabled": true},
		},
		{
			name: "unknown upstream protocol",
			body: map[string]any{"code": "mistral", "display_name": "Mistral", "upstream_protocol": "unknown_proto", "enabled": true},
		},
		{
			name: "enabled omitted",
			body: map[string]any{"code": "mistral", "display_name": "Mistral", "upstream_protocol": "openai_chat"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries := newProviderCatalogQueriesStub()
			rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
				Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
				Store: queries,
			}, http.MethodPost, "/admin/v1/providers", tc.body)
			assertProviderCatalogStatus(t, rec, http.StatusBadRequest)
			if queries.createCalls != 0 {
				t.Fatalf("invalid create body touched store: arg=%+v", queries.createArg)
			}
		})
	}
}

// Mutation: drop tenant checks before mutation; tenant operator 7 would mutate
// tenant 8 and this test must turn red by observing a store call.
func TestProviderCrud_TenantIsolation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		target string
		body   any
	}{
		{
			name:   "create",
			method: http.MethodPost,
			target: "/admin/v1/providers?tenant_id=8",
			body: map[string]any{
				"code": "mistral", "display_name": "Mistral",
				"upstream_protocol": "openai_chat", "enabled": true,
			},
		},
		{
			name:   "update",
			method: http.MethodPut,
			target: "/admin/v1/providers/mistral?tenant_id=8",
			body: map[string]any{
				"display_name": "Mistral", "upstream_protocol": "openai_chat", "enabled": true,
			},
		},
		{name: "delete", method: http.MethodDelete, target: "/admin/v1/providers/mistral?tenant_id=8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries := newProviderCatalogQueriesStub()
			rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
				Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
				Store: queries,
			}, tc.method, tc.target, tc.body)

			assertProviderCatalogStatus(t, rec, http.StatusForbidden)
			if queries.createCalls != 0 || queries.updateCalls != 0 || queries.deleteCalls != 0 {
				t.Fatalf("cross-tenant mutation touched store: create=%d update=%d delete=%d",
					queries.createCalls, queries.updateCalls, queries.deleteCalls)
			}
		})
	}
}

// Mutation: ignore the `{code}` path scope or treat no updated row as success;
// the not-found branch must stay 404 and successful updates must reflect the
// requested display_name/enabled values.
func TestUpdateProvider(t *testing.T) {
	t.Run("updates display protocol and enabled", func(t *testing.T) {
		queries := newProviderCatalogQueriesStub()
		rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
			Auth:  apiKeyAuthStub{ident: platformAdmin()},
			Store: queries,
		}, http.MethodPut, "/admin/v1/providers/mistral?tenant_id=7", map[string]any{
			"display_name": "Mistral Updated", "upstream_protocol": "openai_responses", "enabled": false,
		})

		assertProviderCatalogStatus(t, rec, http.StatusOK)
		var body providerCatalogItem
		decodeProviderCatalogBody(t, rec, &body)
		if body.Code != "mistral" || body.DisplayName != "Mistral Updated" || body.UpstreamProtocol != "openai_responses" || body.Enabled {
			t.Fatalf("updated provider body=%+v", body)
		}
		if queries.updateCalls != 1 || queries.updateArg.TenantID != 7 || queries.updateArg.Code != "mistral" ||
			queries.updateArg.DisplayName != "Mistral Updated" || queries.updateArg.Enabled {
			t.Fatalf("update arg=%+v calls=%d", queries.updateArg, queries.updateCalls)
		}
		if queries.updateAudit.Action != "update_provider" ||
			!strings.Contains(string(queries.updateAudit.Payload), `"enabled":false`) {
			t.Fatalf("update audit=%+v payload=%s", queries.updateAudit, string(queries.updateAudit.Payload))
		}
	})

	t.Run("non-existent provider returns 404", func(t *testing.T) {
		queries := newProviderCatalogQueriesStub()
		queries.updateErr = errProviderCatalogNotFound
		rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
			Auth:  apiKeyAuthStub{ident: platformAdmin()},
			Store: queries,
		}, http.MethodPut, "/admin/v1/providers/missing?tenant_id=7", map[string]any{
			"display_name": "Missing", "upstream_protocol": "openai_chat", "enabled": true,
		})

		assertProviderCatalogStatus(t, rec, http.StatusNotFound)
	})
}

// Mutation: hard-delete or soft-delete providers that still have active
// provider_accounts; this test must turn red by returning success instead of
// 409 for the active-account guard.
func TestDeleteProvider_GuardOrSoftDelete(t *testing.T) {
	t.Run("soft deletes provider when no active accounts reference it", func(t *testing.T) {
		queries := newProviderCatalogQueriesStub()
		rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
			Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
			Store: queries,
		}, http.MethodDelete, "/admin/v1/providers/mistral", nil)

		assertProviderCatalogStatus(t, rec, http.StatusOK)
		var body providerCatalogDeleteResponse
		decodeProviderCatalogBody(t, rec, &body)
		if body.Code != "mistral" || !body.Deleted {
			t.Fatalf("delete body=%+v", body)
		}
		if queries.deleteCalls != 1 || queries.deleteArg.TenantID != 7 || queries.deleteArg.Code != "mistral" {
			t.Fatalf("delete arg=%+v calls=%d", queries.deleteArg, queries.deleteCalls)
		}
		if queries.deleteAudit.Action != "delete_provider" ||
			!strings.Contains(string(queries.deleteAudit.Payload), `"deleted":true`) {
			t.Fatalf("delete audit=%+v payload=%s", queries.deleteAudit, string(queries.deleteAudit.Payload))
		}
	})

	t.Run("active provider accounts are guarded", func(t *testing.T) {
		queries := newProviderCatalogQueriesStub()
		queries.deleteErr = errProviderCatalogActiveAccounts
		rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
			Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
			Store: queries,
		}, http.MethodDelete, "/admin/v1/providers/mistral", nil)

		assertProviderCatalogStatus(t, rec, http.StatusConflict)
		if queries.deleteCalls != 1 {
			t.Fatalf("delete calls=%d want guard evaluation call", queries.deleteCalls)
		}
	})
}

type providerCatalogQueriesStub struct {
	rowsByTenant map[int64][]admindb.ListAdminProvidersByTenantRow
	err          error
	lastArg      admindb.ListAdminProvidersByTenantParams
	listCalls    int
	createArg    providerCatalogCreateParams
	createAudit  admindb.InsertAdminAuditEventParams
	createResult providerCatalogItem
	createErr    error
	createCalls  int
	updateArg    providerCatalogUpdateParams
	updateAudit  admindb.InsertAdminAuditEventParams
	updateResult providerCatalogItem
	updateErr    error
	updateCalls  int
	deleteArg    providerCatalogDeleteParams
	deleteAudit  admindb.InsertAdminAuditEventParams
	deleteResult providerCatalogItem
	deleteErr    error
	deleteCalls  int
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

func (s *providerCatalogQueriesStub) CreateProviderCatalogWithAudit(_ context.Context, arg providerCatalogCreateParams, audit admindb.InsertAdminAuditEventParams) (providerCatalogItem, error) {
	s.createCalls++
	s.createArg = arg
	s.createAudit = audit
	if s.createErr != nil {
		return providerCatalogItem{}, s.createErr
	}
	if s.createResult.ID == 0 {
		s.createResult = providerCatalogItem{
			ID: arg.TenantID*100 + 3, Code: arg.Code, DisplayName: arg.DisplayName,
			UpstreamProtocol: arg.UpstreamProtocol, Enabled: arg.Enabled,
			CreatedAt: "2026-06-02T08:00:00Z",
		}
	}
	return s.createResult, nil
}

func (s *providerCatalogQueriesStub) UpdateProviderCatalogWithAudit(_ context.Context, arg providerCatalogUpdateParams, audit admindb.InsertAdminAuditEventParams) (providerCatalogItem, error) {
	s.updateCalls++
	s.updateArg = arg
	s.updateAudit = audit
	if s.updateErr != nil {
		return providerCatalogItem{}, s.updateErr
	}
	if s.updateResult.ID == 0 {
		s.updateResult = providerCatalogItem{
			ID: arg.TenantID*100 + 3, Code: arg.Code, DisplayName: arg.DisplayName,
			UpstreamProtocol: arg.UpstreamProtocol, Enabled: arg.Enabled,
			CreatedAt: "2026-06-02T08:00:00Z",
		}
	}
	return s.updateResult, nil
}

func (s *providerCatalogQueriesStub) DeleteProviderCatalogWithAudit(_ context.Context, arg providerCatalogDeleteParams, audit admindb.InsertAdminAuditEventParams) (providerCatalogItem, error) {
	s.deleteCalls++
	s.deleteArg = arg
	s.deleteAudit = audit
	if s.deleteErr != nil {
		return providerCatalogItem{}, s.deleteErr
	}
	if s.deleteResult.ID == 0 {
		s.deleteResult = providerCatalogItem{
			ID: arg.TenantID*100 + 3, Code: arg.Code, DisplayName: "Mistral",
			UpstreamProtocol: "openai_chat", Enabled: false,
			CreatedAt: "2026-06-02T08:00:00Z",
		}
	}
	return s.deleteResult, nil
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
	return invokeProviderCatalogRequest(t, deps, http.MethodGet, target, nil)
}

func invokeProviderCatalogRequest(t *testing.T, deps AdminProviderCatalogDeps, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/providers", func(r chi.Router) {
		MountProviderCatalogRoutes(r, deps)
	})
	req := httptest.NewRequest(method, target, providerCatalogRequestBody(t, body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func providerCatalogRequestBody(t *testing.T, body any) *bytes.Reader {
	t.Helper()
	switch v := body.(type) {
	case nil:
		return bytes.NewReader(nil)
	case string:
		return bytes.NewReader([]byte(v))
	case []byte:
		return bytes.NewReader(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		return bytes.NewReader(raw)
	}
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
