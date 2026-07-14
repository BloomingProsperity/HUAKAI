package adminhttp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// channelCatalogStoreStub 是供变更处理器单元测试使用的 adminChannelCatalogStore 假实现:
// 它在不接数据库的情况下记录调用并返回可配置的错误。
// SQL 层面的租户隔离 / 唯一约束由集成测试来覆盖。
type channelCatalogStoreStub struct {
	createErr, updateErr, deleteErr       error
	createItem, updateItem, deleteItem    channelCatalogItem
	createCalls, updateCalls, deleteCalls int
	lastCreate                            channelCatalogCreateParams
	lastUpdate                            channelCatalogUpdateParams
	lastDelete                            channelCatalogDeleteParams
}

func (s *channelCatalogStoreStub) ListAdminChannelsByTenant(context.Context, admindb.ListAdminChannelsByTenantParams) ([]admindb.ListAdminChannelsByTenantRow, error) {
	return nil, nil
}

func (s *channelCatalogStoreStub) CreateChannelCatalogWithAudit(_ context.Context, arg channelCatalogCreateParams, _ admindb.InsertAdminAuditEventParams) (channelCatalogItem, error) {
	s.createCalls++
	s.lastCreate = arg
	if s.createErr != nil {
		return channelCatalogItem{}, s.createErr
	}
	return s.createItem, nil
}

func (s *channelCatalogStoreStub) UpdateChannelCatalogWithAudit(_ context.Context, arg channelCatalogUpdateParams, _ admindb.InsertAdminAuditEventParams) (channelCatalogItem, error) {
	s.updateCalls++
	s.lastUpdate = arg
	if s.updateErr != nil {
		return channelCatalogItem{}, s.updateErr
	}
	return s.updateItem, nil
}

func (s *channelCatalogStoreStub) DeleteChannelCatalogWithAudit(_ context.Context, arg channelCatalogDeleteParams, _ admindb.InsertAdminAuditEventParams) (channelCatalogItem, error) {
	s.deleteCalls++
	s.lastDelete = arg
	if s.deleteErr != nil {
		return channelCatalogItem{}, s.deleteErr
	}
	return s.deleteItem, nil
}

func invokeChannelCatalogMutation(t *testing.T, deps AdminChannelCatalogDeps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/channels", func(r chi.Router) { MountChannelCatalogRoutes(r, deps) })
	var rdr *bytes.Reader
	if body == "" {
		rdr = bytes.NewReader(nil)
	} else {
		rdr = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, target, rdr)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestChannelCatalogCreateRequiresAdminAuth(t *testing.T) {
	store := &channelCatalogStoreStub{}
	rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
		Auth:  apiKeyAuthStub{err: admin.ErrAdminUnauthorized},
		Store: store,
	}, http.MethodPost, "/admin/v1/channels?tenant_id=7",
		`{"pool_group_id":70,"name":"x","enabled":true}`)
	assertChannelCatalogStatus(t, rec, http.StatusUnauthorized)
	if store.createCalls != 0 {
		t.Fatalf("unauthorized create touched store: calls=%d", store.createCalls)
	}
}

// 变异:去掉 resolveChannelCatalogMutationAdmin 中的租户作用域门控,
// 会让一个 tenant operator 能去变更另一个租户 -> 此处「在触达 store 之前返回 403」的检查会变红。
func TestChannelCatalogCreateCrossTenantForbidden(t *testing.T) {
	store := &channelCatalogStoreStub{}
	rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
		Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, http.MethodPost, "/admin/v1/channels?tenant_id=8",
		`{"pool_group_id":80,"name":"x","enabled":true}`)
	assertChannelCatalogStatus(t, rec, http.StatusForbidden)
	if store.createCalls != 0 {
		t.Fatalf("cross-tenant create touched store: calls=%d arg=%+v", store.createCalls, store.lastCreate)
	}
}

func TestChannelCatalogCreateHappyPath(t *testing.T) {
	store := &channelCatalogStoreStub{createItem: channelCatalogItem{
		ID: 901, PoolGroupID: 70, Name: "primary", FailoverStatusCodes: []int32{401, 429}, Enabled: true,
	}}
	rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
		Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, http.MethodPost, "/admin/v1/channels",
		`{"pool_group_id":70,"name":"primary","enabled":true}`)
	assertChannelCatalogStatus(t, rec, http.StatusCreated)
	if store.createCalls != 1 || store.lastCreate.TenantID != 7 || store.lastCreate.PoolGroupID != 70 || store.lastCreate.Name != "primary" {
		t.Fatalf("create args wrong: %+v", store.lastCreate)
	}
	// 省略时应用 failover codes 的默认值
	if len(store.lastCreate.FailoverStatusCodes) != 4 {
		t.Fatalf("omitted failover codes should default to 4 entries: %v", store.lastCreate.FailoverStatusCodes)
	}
	var item channelCatalogItem
	decodeChannelCatalogBody(t, rec, &item)
	if item.ID != 901 || item.Name != "primary" {
		t.Fatalf("response item wrong: %+v", item)
	}
}

// 老客户端仍可携带 failover_status_codes；删掉 DTO 字段后严格 JSON 解码会返回 400。
func TestChannelCatalogCreateAcceptsLegacyFailoverStatusCodes(t *testing.T) {
	store := &channelCatalogStoreStub{createItem: channelCatalogItem{
		ID: 902, PoolGroupID: 70, Name: "legacy", FailoverStatusCodes: []int32{418, 529}, Enabled: true,
	}}
	rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
		Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, http.MethodPost, "/admin/v1/channels",
		`{"pool_group_id":70,"name":"legacy","enabled":true,"failover_status_codes":[418,529]}`)
	assertChannelCatalogStatus(t, rec, http.StatusCreated)
	if store.createCalls != 1 || len(store.lastCreate.FailoverStatusCodes) != 2 ||
		store.lastCreate.FailoverStatusCodes[0] != 418 || store.lastCreate.FailoverStatusCodes[1] != 529 {
		t.Fatalf("旧字段未兼容透传:calls=%d arg=%+v", store.createCalls, store.lastCreate)
	}
	if store.lastCreate.Name != "legacy" || !store.lastCreate.Enabled {
		t.Fatalf("现存有效字段传播错:arg=%+v", store.lastCreate)
	}
}

func TestChannelCatalogCreateValidation(t *testing.T) {
	cases := map[string]string{
		"missing name":       `{"pool_group_id":70,"enabled":true}`,
		"missing pool_group": `{"name":"x","enabled":true}`,
		"zero pool_group":    `{"pool_group_id":0,"name":"x","enabled":true}`,
		"missing enabled":    `{"pool_group_id":70,"name":"x"}`,
		"bad failover code":  `{"pool_group_id":70,"name":"x","enabled":true,"failover_status_codes":[99]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			store := &channelCatalogStoreStub{}
			rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
				Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
				Store: store,
			}, http.MethodPost, "/admin/v1/channels", body)
			assertChannelCatalogStatus(t, rec, http.StatusBadRequest)
			if store.createCalls != 0 {
				t.Fatalf("invalid create touched store: calls=%d", store.createCalls)
			}
		})
	}
}

func TestChannelCatalogCreateConflictAndPoolNotFound(t *testing.T) {
	t.Run("name conflict 409", func(t *testing.T) {
		store := &channelCatalogStoreStub{createErr: errChannelCatalogNameConflict}
		rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
			Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
			Store: store,
		}, http.MethodPost, "/admin/v1/channels", `{"pool_group_id":70,"name":"dup","enabled":true}`)
		assertChannelCatalogStatus(t, rec, http.StatusConflict)
	})
	t.Run("pool not found 400", func(t *testing.T) {
		store := &channelCatalogStoreStub{createErr: errChannelCatalogPoolNotFound}
		rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
			Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
			Store: store,
		}, http.MethodPost, "/admin/v1/channels", `{"pool_group_id":999,"name":"x","enabled":true}`)
		assertChannelCatalogStatus(t, rec, http.StatusBadRequest)
	})
}

func TestChannelCatalogUpdate(t *testing.T) {
	t.Run("happy 200", func(t *testing.T) {
		store := &channelCatalogStoreStub{updateItem: channelCatalogItem{ID: 5, PoolGroupID: 70, Name: "renamed", Enabled: false}}
		rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
			Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
			Store: store,
		}, http.MethodPut, "/admin/v1/channels/5", `{"pool_group_id":70,"name":"renamed","enabled":false}`)
		assertChannelCatalogStatus(t, rec, http.StatusOK)
		if store.updateCalls != 1 || store.lastUpdate.ID != 5 || store.lastUpdate.TenantID != 7 {
			t.Fatalf("update args wrong: %+v", store.lastUpdate)
		}
	})
	t.Run("not found 404", func(t *testing.T) {
		store := &channelCatalogStoreStub{updateErr: errChannelCatalogNotFound}
		rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
			Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
			Store: store,
		}, http.MethodPut, "/admin/v1/channels/404", `{"pool_group_id":70,"name":"x","enabled":true}`)
		assertChannelCatalogStatus(t, rec, http.StatusNotFound)
	})
	t.Run("invalid id 400", func(t *testing.T) {
		store := &channelCatalogStoreStub{}
		rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
			Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
			Store: store,
		}, http.MethodPut, "/admin/v1/channels/not-a-number", `{"pool_group_id":70,"name":"x","enabled":true}`)
		assertChannelCatalogStatus(t, rec, http.StatusBadRequest)
		if store.updateCalls != 0 {
			t.Fatalf("invalid id touched store: calls=%d", store.updateCalls)
		}
	})
}

func TestChannelCatalogDelete(t *testing.T) {
	t.Run("happy 200", func(t *testing.T) {
		store := &channelCatalogStoreStub{deleteItem: channelCatalogItem{ID: 9}}
		rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
			Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
			Store: store,
		}, http.MethodDelete, "/admin/v1/channels/9", "")
		assertChannelCatalogStatus(t, rec, http.StatusOK)
		var body channelCatalogDeleteResponse
		decodeChannelCatalogBody(t, rec, &body)
		if body.Object != "admin_channel_deleted" || body.ID != 9 || !body.Deleted {
			t.Fatalf("delete response wrong: %+v", body)
		}
		if store.lastDelete.TenantID != 7 || store.lastDelete.ID != 9 {
			t.Fatalf("delete args wrong: %+v", store.lastDelete)
		}
	})
	t.Run("not found 404", func(t *testing.T) {
		store := &channelCatalogStoreStub{deleteErr: errChannelCatalogNotFound}
		rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
			Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
			Store: store,
		}, http.MethodDelete, "/admin/v1/channels/404", "")
		assertChannelCatalogStatus(t, rec, http.StatusNotFound)
	})
}
