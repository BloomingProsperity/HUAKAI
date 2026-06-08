package adminhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestChannelTestTemplateHandlersCRUD(t *testing.T) {
	store := newChannelTestTemplateStoreStub()
	deps := AdminChannelTestTemplateDeps{
		Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
		Store: store,
	}

	create := invokeChannelTestTemplates(t, deps, http.MethodPost, "/admin/v1/channel-test-templates", map[string]any{
		"name":          "quota check",
		"method":        "POST",
		"path":          "/v1/chat/completions",
		"body_template": `{"model":"health-check"}`,
		"headers":       map[string]string{"X-Test-Mode": "true"},
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var created channelTestTemplateItem
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == 0 || created.TenantID != 7 || created.Name != "quota check" {
		t.Fatalf("created=%+v", created)
	}

	list := invokeChannelTestTemplates(t, deps, http.MethodGet, "/admin/v1/channel-test-templates", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	var listed channelTestTemplateListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Fatalf("listed=%+v want created ID %d", listed, created.ID)
	}

	update := invokeChannelTestTemplates(t, deps, http.MethodPut, "/admin/v1/channel-test-templates/1", map[string]any{
		"name":          "updated quota check",
		"method":        "GET",
		"path":          "/v1/models",
		"body_template": "",
		"headers":       map[string]string{"X-Test-Mode": "updated"},
	})
	if update.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", update.Code, update.Body.String())
	}
	got := invokeChannelTestTemplates(t, deps, http.MethodGet, "/admin/v1/channel-test-templates/1", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", got.Code, got.Body.String())
	}
	var fetched channelTestTemplateItem
	if err := json.Unmarshal(got.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if fetched.Name != "updated quota check" || fetched.Method != "GET" || fetched.Path != "/v1/models" {
		t.Fatalf("fetched=%+v", fetched)
	}

	deleted := invokeChannelTestTemplates(t, deps, http.MethodDelete, "/admin/v1/channel-test-templates/1", nil)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	list = invokeChannelTestTemplates(t, deps, http.MethodGet, "/admin/v1/channel-test-templates", nil)
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list after delete: %v", err)
	}
	if len(listed.Items) != 0 {
		t.Fatalf("listed after delete=%+v want empty", listed.Items)
	}
}

func TestChannelTestTemplateRejectsCredentialHeaders(t *testing.T) {
	store := newChannelTestTemplateStoreStub()
	deps := AdminChannelTestTemplateDeps{
		Auth:  apiKeyAuthStub{ident: tenantOperator(7)},
		Store: store,
	}

	rec := invokeChannelTestTemplates(t, deps, http.MethodPost, "/admin/v1/channel-test-templates", map[string]any{
		"name":          "unsafe",
		"method":        "POST",
		"path":          "/v1/chat/completions",
		"body_template": `{}`,
		"headers":       map[string]string{"Authorization": "Bearer should-not-store"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if len(store.rows) != 0 {
		t.Fatalf("credential-bearing header was persisted: %+v", store.rows)
	}
}

func invokeChannelTestTemplates(t *testing.T, deps AdminChannelTestTemplateDeps, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/channel-test-templates", func(r chi.Router) {
		MountChannelTestTemplateRoutes(r, deps)
	})
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reader)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

type channelTestTemplateStoreStub struct {
	rows   map[int64]admindb.ChannelTestTemplate
	nextID int64
}

func newChannelTestTemplateStoreStub() *channelTestTemplateStoreStub {
	return &channelTestTemplateStoreStub{rows: map[int64]admindb.ChannelTestTemplate{}, nextID: 1}
}

func (s *channelTestTemplateStoreStub) CreateChannelTestTemplate(_ context.Context, arg admindb.CreateChannelTestTemplateParams) (admindb.ChannelTestTemplate, error) {
	row := admindb.ChannelTestTemplate{
		ID: s.nextID, TenantID: arg.TenantID, Name: arg.Name, Method: arg.Method,
		Path: arg.Path, BodyTemplate: arg.BodyTemplate, Headers: append([]byte(nil), arg.Headers...),
		CreatedAt: pgtype.Timestamptz{Time: time.Unix(1700000000, 0).UTC(), Valid: true},
	}
	s.nextID++
	s.rows[row.ID] = row
	return row, nil
}

func (s *channelTestTemplateStoreStub) ListChannelTestTemplatesByTenant(_ context.Context, arg admindb.ListChannelTestTemplatesByTenantParams) ([]admindb.ChannelTestTemplate, error) {
	out := make([]admindb.ChannelTestTemplate, 0, len(s.rows))
	for _, row := range s.rows {
		if row.TenantID == arg.TenantID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *channelTestTemplateStoreStub) GetChannelTestTemplate(_ context.Context, arg admindb.GetChannelTestTemplateParams) (admindb.ChannelTestTemplate, error) {
	row, ok := s.rows[arg.ID]
	if !ok || row.TenantID != arg.TenantID {
		return admindb.ChannelTestTemplate{}, pgx.ErrNoRows
	}
	return row, nil
}

func (s *channelTestTemplateStoreStub) UpdateChannelTestTemplate(_ context.Context, arg admindb.UpdateChannelTestTemplateParams) (admindb.ChannelTestTemplate, error) {
	row, ok := s.rows[arg.ID]
	if !ok || row.TenantID != arg.TenantID {
		return admindb.ChannelTestTemplate{}, pgx.ErrNoRows
	}
	row.Name = arg.Name
	row.Method = arg.Method
	row.Path = arg.Path
	row.BodyTemplate = arg.BodyTemplate
	row.Headers = append([]byte(nil), arg.Headers...)
	s.rows[arg.ID] = row
	return row, nil
}

func (s *channelTestTemplateStoreStub) DeleteChannelTestTemplate(_ context.Context, arg admindb.DeleteChannelTestTemplateParams) (admindb.ChannelTestTemplate, error) {
	row, ok := s.rows[arg.ID]
	if !ok || row.TenantID != arg.TenantID {
		return admindb.ChannelTestTemplate{}, pgx.ErrNoRows
	}
	delete(s.rows, arg.ID)
	return row, nil
}

func TestChannelTestTemplateHeadersRoundTrip(t *testing.T) {
	store := newChannelTestTemplateStoreStub()
	row, err := store.CreateChannelTestTemplate(context.Background(), admindb.CreateChannelTestTemplateParams{
		TenantID: 7, Name: "headers", Method: "GET", Path: "/v1/models", Headers: []byte(`{"X-Test-Mode":"true"}`),
	})
	if err != nil {
		t.Fatalf("CreateChannelTestTemplate: %v", err)
	}
	if !reflect.DeepEqual(row.Headers, []byte(`{"X-Test-Mode":"true"}`)) {
		t.Fatalf("headers=%s", row.Headers)
	}
}
