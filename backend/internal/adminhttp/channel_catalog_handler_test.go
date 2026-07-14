package adminhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
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
			"failover_status_codes": true, "body_param_strips": true,
			"param_override": true, "sensitive_words": true,
			"enabled": true, "created_at": true,
		}
		for key := range body.Items[0] {
			if !allowed[key] {
				t.Fatalf("channel catalog item exposed non-whitelisted field %q in %+v", key, body.Items[0])
			}
		}
		if got := body.Items[0]["body_param_strips"]; fmt.Sprint(got) != "[drop_create]" {
			t.Fatalf("body_param_strips 回显错误:%v", got)
		}
		if got := body.Items[0]["sensitive_words"]; fmt.Sprint(got) != "[word_create]" {
			t.Fatalf("sensitive_words 回显错误:%v", got)
		}
		override, ok := body.Items[0]["param_override"].(map[string]any)
		if !ok || override["temperature"] != 0.25 {
			t.Fatalf("param_override 回显错误:%v", body.Items[0]["param_override"])
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

func TestChannelCatalogGet精确回显三门并处理不存在(t *testing.T) {
	t.Run("existing channel", func(t *testing.T) {
		queries := newChannelCatalogQueriesStub()
		rec := invokeChannelCatalog(t, AdminChannelCatalogDeps{
			Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
			Queries: queries,
		}, "/admin/v1/channels/701")

		assertChannelCatalogStatus(t, rec, http.StatusOK)
		var item channelCatalogItem
		decodeChannelCatalogBody(t, rec, &item)
		if item.ID != 701 || item.Name != "tenant-7-primary" ||
			!reflect.DeepEqual(item.BodyParamStrips, []string{"drop_create"}) ||
			!reflect.DeepEqual(item.SensitiveWords, []string{"word_create"}) {
			t.Fatalf("get 未精确回显渠道三门:item=%+v", item)
		}
		var override map[string]any
		if err := json.Unmarshal(item.ParamOverride, &override); err != nil || override["temperature"] != 0.25 {
			t.Fatalf("get param_override 回显错误:body=%s err=%v", item.ParamOverride, err)
		}
		if queries.getCalls != 1 || queries.lastGetArg.TenantID != 7 || queries.lastGetArg.ID != 701 {
			t.Fatalf("get 查询作用域错误:calls=%d arg=%+v", queries.getCalls, queries.lastGetArg)
		}
	})

	t.Run("missing channel", func(t *testing.T) {
		queries := newChannelCatalogQueriesStub()
		rec := invokeChannelCatalog(t, AdminChannelCatalogDeps{
			Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
			Queries: queries,
		}, "/admin/v1/channels/999")

		assertChannelCatalogStatus(t, rec, http.StatusNotFound)
		if queries.getCalls != 1 {
			t.Fatalf("不存在渠道应精确查询一次:calls=%d", queries.getCalls)
		}
	})
}

type channelCatalogQueriesStub struct {
	rowsByTenant map[int64][]admindb.ListAdminChannelsByTenantRow
	err          error
	lastArg      admindb.ListAdminChannelsByTenantParams
	lastGetArg   admindb.GetAdminChannelParams
	listCalls    int
	getCalls     int
}

func newChannelCatalogQueriesStub() *channelCatalogQueriesStub {
	created := pgTimestamp(time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC))
	return &channelCatalogQueriesStub{rowsByTenant: map[int64][]admindb.ListAdminChannelsByTenantRow{
		7: {
			{ID: 701, PoolGroupID: 70, Name: "tenant-7-primary", FailoverStatusCodes: []int32{401, 429}, BodyParamStrips: []string{"drop_create"}, ParamOverride: []byte(`{"temperature":0.25}`), SensitiveWords: []string{"word_create"}, Enabled: true, CreatedAt: created},
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

func (s *channelCatalogQueriesStub) GetAdminChannel(_ context.Context, arg admindb.GetAdminChannelParams) (admindb.GetAdminChannelRow, error) {
	s.getCalls++
	s.lastGetArg = arg
	if s.err != nil {
		return admindb.GetAdminChannelRow{}, s.err
	}
	for _, row := range s.rowsByTenant[arg.TenantID] {
		if row.ID == arg.ID {
			return admindb.GetAdminChannelRow{
				ID: row.ID, PoolGroupID: row.PoolGroupID, Name: row.Name,
				FailoverStatusCodes: row.FailoverStatusCodes,
				BodyParamStrips:     row.BodyParamStrips, ParamOverride: row.ParamOverride,
				SensitiveWords: row.SensitiveWords, Enabled: row.Enabled, CreatedAt: row.CreatedAt,
			}, nil
		}
	}
	return admindb.GetAdminChannelRow{}, pgx.ErrNoRows
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
	r.Get("/admin/v1/channels", NewChannelCatalogListHandler(deps))
	r.Get("/admin/v1/channels/{id}", NewChannelCatalogGetHandler(deps))
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
