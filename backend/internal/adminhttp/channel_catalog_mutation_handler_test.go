package adminhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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
	lastCreateAudit                       admindb.InsertAdminAuditEventParams
	lastUpdateAudit                       admindb.InsertAdminAuditEventParams
}

func (s *channelCatalogStoreStub) ListAdminChannelsByTenant(context.Context, admindb.ListAdminChannelsByTenantParams) ([]admindb.ListAdminChannelsByTenantRow, error) {
	return nil, nil
}

func (s *channelCatalogStoreStub) GetAdminChannel(context.Context, admindb.GetAdminChannelParams) (admindb.GetAdminChannelRow, error) {
	return admindb.GetAdminChannelRow{}, nil
}

func (s *channelCatalogStoreStub) CreateChannelCatalogWithAudit(_ context.Context, arg channelCatalogCreateParams, audit admindb.InsertAdminAuditEventParams) (channelCatalogItem, error) {
	s.createCalls++
	s.lastCreate = arg
	s.lastCreateAudit = audit
	if s.createErr != nil {
		return channelCatalogItem{}, s.createErr
	}
	return s.createItem, nil
}

func (s *channelCatalogStoreStub) UpdateChannelCatalogWithAudit(_ context.Context, arg channelCatalogUpdateParams, audit admindb.InsertAdminAuditEventParams) (channelCatalogItem, error) {
	s.updateCalls++
	s.lastUpdate = arg
	s.lastUpdateAudit = audit
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
		ID: 901, PoolGroupID: 70, Name: "primary", FailoverStatusCodes: []int32{401, 429},
		BodyParamStrips: []string{"drop_create"}, ParamOverride: json.RawMessage(`{"temperature":0.25,"metadata":{"source":"create"}}`),
		SensitiveWords: []string{"word_create"}, Enabled: true,
	}}
	rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
		Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, http.MethodPost, "/admin/v1/channels",
		`{"pool_group_id":70,"name":"primary","enabled":true,"body_param_strips":["drop_create"],"param_override":{"temperature":0.25,"metadata":{"source":"create"}},"sensitive_words":["word_create"]}`)
	assertChannelCatalogStatus(t, rec, http.StatusCreated)
	if store.createCalls != 1 || store.lastCreate.TenantID != 7 || store.lastCreate.PoolGroupID != 70 || store.lastCreate.Name != "primary" {
		t.Fatalf("create args wrong: %+v", store.lastCreate)
	}
	// 省略时应用 failover codes 的默认值
	if len(store.lastCreate.FailoverStatusCodes) != 4 {
		t.Fatalf("omitted failover codes should default to 4 entries: %v", store.lastCreate.FailoverStatusCodes)
	}
	if !reflect.DeepEqual(store.lastCreate.BodyParamStrips, []string{"drop_create"}) ||
		!reflect.DeepEqual(store.lastCreate.SensitiveWords, []string{"word_create"}) {
		t.Fatalf("create 三门数组未精确传给 store:%+v", store.lastCreate)
	}
	var storedOverride map[string]any
	if err := json.Unmarshal(store.lastCreate.ParamOverride, &storedOverride); err != nil || storedOverride["temperature"] != 0.25 {
		t.Fatalf("create param_override 未精确传给 store:%s err=%v", store.lastCreate.ParamOverride, err)
	}
	var item channelCatalogItem
	decodeChannelCatalogBody(t, rec, &item)
	if item.ID != 901 || item.Name != "primary" ||
		!reflect.DeepEqual(item.BodyParamStrips, []string{"drop_create"}) ||
		!reflect.DeepEqual(item.SensitiveWords, []string{"word_create"}) {
		t.Fatalf("response item wrong: %+v", item)
	}
	var responseOverride map[string]any
	if err := json.Unmarshal(item.ParamOverride, &responseOverride); err != nil || !reflect.DeepEqual(responseOverride, storedOverride) {
		t.Fatalf("create param_override 回显不一致:got=%s want=%s err=%v", item.ParamOverride, store.lastCreate.ParamOverride, err)
	}
	audit := decodeChannelCatalogAuditPayload(t, store.lastCreateAudit.Payload)
	if !reflect.DeepEqual(audit["body_param_strips"], []any{"drop_create"}) ||
		!reflect.DeepEqual(audit["sensitive_words"], []any{"word_create"}) {
		t.Fatalf("create 审计未记录三门数组:%v", audit)
	}
	auditOverride, ok := audit["param_override"].(map[string]any)
	if !ok || auditOverride["temperature"] != 0.25 {
		t.Fatalf("create 审计未记录覆盖对象:%v", audit["param_override"])
	}
}

// 老客户端仍可携带 failover_status_codes；删掉 DTO 字段或映射后，
// 下方精确透传断言会转红。
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
	if len(store.lastCreate.BodyParamStrips) != 0 || string(store.lastCreate.ParamOverride) != `{}` ||
		len(store.lastCreate.SensitiveWords) != 0 {
		t.Fatalf("create 省略三门未使用透传缺省值:arg=%+v", store.lastCreate)
	}
}

func TestChannelCatalogCreateValidation(t *testing.T) {
	cases := map[string]string{
		"missing name":                    `{"pool_group_id":70,"enabled":true}`,
		"missing pool_group":              `{"name":"x","enabled":true}`,
		"zero pool_group":                 `{"pool_group_id":0,"name":"x","enabled":true}`,
		"missing enabled":                 `{"pool_group_id":70,"name":"x"}`,
		"bad failover code":               `{"pool_group_id":70,"name":"x","enabled":true,"failover_status_codes":[99]}`,
		"malformed json":                  `{"pool_group_id":70,"name":"x","enabled":true`,
		"body strips object":              `{"pool_group_id":70,"name":"x","enabled":true,"body_param_strips":{"bad":true}}`,
		"body strips non-string item":     `{"pool_group_id":70,"name":"x","enabled":true,"body_param_strips":["ok",7]}`,
		"body strips empty item":          `{"pool_group_id":70,"name":"x","enabled":true,"body_param_strips":[" "]}`,
		"param override array":            `{"pool_group_id":70,"name":"x","enabled":true,"param_override":[1]}`,
		"param override scalar":           `{"pool_group_id":70,"name":"x","enabled":true,"param_override":"bad"}`,
		"param override null":             `{"pool_group_id":70,"name":"x","enabled":true,"param_override":null}`,
		"param override empty key":        `{"pool_group_id":70,"name":"x","enabled":true,"param_override":{" ":1}}`,
		"sensitive words object":          `{"pool_group_id":70,"name":"x","enabled":true,"sensitive_words":{"bad":true}}`,
		"sensitive words non-string item": `{"pool_group_id":70,"name":"x","enabled":true,"sensitive_words":["ok",false]}`,
		"sensitive words null":            `{"pool_group_id":70,"name":"x","enabled":true,"sensitive_words":null}`,
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
		store := &channelCatalogStoreStub{updateItem: channelCatalogItem{
			ID: 5, PoolGroupID: 70, Name: "renamed",
			BodyParamStrips: []string{"keep-strip"}, ParamOverride: json.RawMessage(`{"keep":true}`),
			SensitiveWords: []string{"new-word"}, Enabled: false,
		}}
		rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
			Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
			Store: store,
		}, http.MethodPut, "/admin/v1/channels/5", `{"pool_group_id":70,"name":"renamed","enabled":false,"sensitive_words":["new-word"]}`)
		assertChannelCatalogStatus(t, rec, http.StatusOK)
		if store.updateCalls != 1 || store.lastUpdate.ID != 5 || store.lastUpdate.TenantID != 7 {
			t.Fatalf("update args wrong: %+v", store.lastUpdate)
		}
		if store.lastUpdate.SetBodyParamStrips || store.lastUpdate.SetParamOverride ||
			!store.lastUpdate.SetSensitiveWords || !reflect.DeepEqual(store.lastUpdate.SensitiveWords, []string{"new-word"}) {
			t.Fatalf("update presence 错误:%+v", store.lastUpdate)
		}
		audit := decodeChannelCatalogAuditPayload(t, store.lastUpdateAudit.Payload)
		if audit["set_body_param_strips"] != false || audit["set_param_override"] != false ||
			audit["set_sensitive_words"] != true || !reflect.DeepEqual(audit["sensitive_words"], []any{"new-word"}) {
			t.Fatalf("update 审计未区分省略与提交:%v", audit)
		}
		var item channelCatalogItem
		decodeChannelCatalogBody(t, rec, &item)
		if !reflect.DeepEqual(item.BodyParamStrips, []string{"keep-strip"}) ||
			!reflect.DeepEqual(item.SensitiveWords, []string{"new-word"}) || string(item.ParamOverride) != `{"keep":true}` {
			t.Fatalf("update 回显未保留另外两门:%+v", item)
		}
	})
	t.Run("explicit empty values clear all three gates", func(t *testing.T) {
		store := &channelCatalogStoreStub{updateItem: channelCatalogItem{
			ID: 6, PoolGroupID: 70, Name: "cleared", BodyParamStrips: []string{},
			ParamOverride: json.RawMessage(`{}`), SensitiveWords: []string{}, Enabled: true,
		}}
		rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
			Auth: apiKeyAuthStub{ident: tenantOperator(7)}, Store: store,
		}, http.MethodPut, "/admin/v1/channels/6",
			`{"pool_group_id":70,"name":"cleared","enabled":true,"body_param_strips":[],"param_override":{},"sensitive_words":[]}`)
		assertChannelCatalogStatus(t, rec, http.StatusOK)
		if !store.lastUpdate.SetBodyParamStrips || !store.lastUpdate.SetParamOverride || !store.lastUpdate.SetSensitiveWords {
			t.Fatalf("显式清空必须保留三门 presence:%+v", store.lastUpdate)
		}
		if len(store.lastUpdate.BodyParamStrips) != 0 || string(store.lastUpdate.ParamOverride) != `{}` || len(store.lastUpdate.SensitiveWords) != 0 {
			t.Fatalf("显式清空值错误:%+v", store.lastUpdate)
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
	t.Run("invalid rewrite gate 400 before store", func(t *testing.T) {
		store := &channelCatalogStoreStub{}
		rec := invokeChannelCatalogMutation(t, AdminChannelCatalogDeps{
			Auth: apiKeyAuthStub{ident: tenantOperator(7)}, Store: store,
		}, http.MethodPut, "/admin/v1/channels/5",
			`{"pool_group_id":70,"name":"x","enabled":true,"param_override":[]}`)
		assertChannelCatalogStatus(t, rec, http.StatusBadRequest)
		if store.updateCalls != 0 {
			t.Fatalf("非法三门值触达 update store:calls=%d", store.updateCalls)
		}
	})
}

func decodeChannelCatalogAuditPayload(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("解析渠道审计 payload: %v body=%s", err, raw)
	}
	return payload
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
