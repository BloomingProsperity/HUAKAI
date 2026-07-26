package obsdlqhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
)

type fakeAdminAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (f fakeAdminAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if f.err != nil {
		return admin.AdminIdentity{}, f.err
	}
	return f.ident, nil
}

type fakeStore struct {
	listFilter   obsdlq.AdminListFilter
	listCalls    int
	rows         []obsdlq.AdminDeadEvent
	replays      map[string]int
	replayTenant int64
	replayActor  string
}

func (f *fakeStore) ListDead(_ context.Context, filter obsdlq.AdminListFilter) ([]obsdlq.AdminDeadEvent, error) {
	f.listCalls++
	f.listFilter = filter
	return f.rows, nil
}

func (f *fakeStore) ReplayDead(_ context.Context, tenantID int64, id, actor string) (obsdlq.AdminReplayResult, error) {
	f.replayTenant = tenantID
	f.replayActor = actor
	if f.replays == nil {
		f.replays = map[string]int{}
	}
	f.replays[id]++
	if f.replays[id] > 1 {
		return obsdlq.AdminReplayResult{}, obsdlq.ErrReplayConflict
	}
	return obsdlq.AdminReplayResult{DLQEventID: id, OutboxEventID: "outbox-" + id}, nil
}

func TestListPassesFiltersAndReturnsEventTypeAttemptCount(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{rows: []obsdlq.AdminDeadEvent{{
		ID: "dead-1", OutboxEventID: "outbox-1", TenantID: 7, EventType: obsdlq.EventTypeEmailRetry,
		Priority: obsdlq.PriorityCritical, Payload: json.RawMessage(`{"safe":"ok"}`),
		DeadAt: now, DeadReason: "smtp failed", AttemptCount: 5, OutboxStatus: obsdlq.StatusFailedDead,
		CreatedAt: now.Add(-time.Hour), NextRetryAt: now.Add(time.Hour),
	}}}
	h := NewListHandler(Deps{Auth: fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, Store: store})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/obs-dlq?tenant=7&event_type=email.retry&from=2026-07-05T00:00:00Z&to=2026-07-06T00:00:00Z", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if store.listFilter.Limit != 100 || store.listFilter.TenantID == nil || *store.listFilter.TenantID != 7 || store.listFilter.EventType == nil || *store.listFilter.EventType != obsdlq.EventTypeEmailRetry {
		t.Fatalf("filter=%+v, want tenant/event_type/default limit", store.listFilter)
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Items) != 1 {
		t.Fatalf("decode body=%s err=%v", rec.Body.String(), err)
	}
	if body.Items[0]["event_type"] != obsdlq.EventTypeEmailRetry || int(body.Items[0]["attempt_count"].(float64)) != 5 {
		t.Fatalf("item=%v, want joined event_type + attempt_count", body.Items[0])
	}
}

func TestListRejectsLimitOverMaxBeforeStore(t *testing.T) {
	store := &fakeStore{}
	h := NewListHandler(Deps{Auth: fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, Store: store})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/obs-dlq?limit=201", nil))

	if rec.Code != http.StatusBadRequest || store.listCalls != 0 {
		t.Fatalf("status=%d storeCalls=%d body=%s, want 400 before store", rec.Code, store.listCalls, rec.Body.String())
	}
}

func TestListTenantOperatorIsForcedToOwnTenant(t *testing.T) {
	store := &fakeStore{}
	h := NewListHandler(Deps{Auth: fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}}, Store: store})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/obs-dlq", nil))

	if rec.Code != http.StatusOK || store.listCalls != 1 || store.listFilter.TenantID == nil || *store.listFilter.TenantID != 7 {
		t.Fatalf("status=%d storeCalls=%d filter=%+v body=%s", rec.Code, store.listCalls, store.listFilter, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/obs-dlq?tenant=8", nil))
	if rec.Code != http.StatusForbidden || store.listCalls != 1 {
		t.Fatalf("cross-tenant status=%d storeCalls=%d body=%s", rec.Code, store.listCalls, rec.Body.String())
	}
}

func TestReplaySecondCallReturnsConflict(t *testing.T) {
	store := &fakeStore{}
	router := chi.NewRouter()
	router.Post("/admin/v1/obs-dlq/{id}/replay", NewReplayHandler(Deps{
		Auth:             fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 9}},
		Store:            store,
		PlatformTenantID: 7,
	}))

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/admin/v1/obs-dlq/dead-1/replay", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d want 200 body=%s", first.Code, first.Body.String())
	}
	if store.replayTenant != 7 || store.replayActor != "admin_token:9" {
		t.Fatalf("replay tenant/actor=%d/%q want 7/admin_token:9", store.replayTenant, store.replayActor)
	}
	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/admin/v1/obs-dlq/dead-1/replay", nil))
	if second.Code != http.StatusConflict {
		t.Fatalf("second status=%d want 409 body=%s", second.Code, second.Body.String())
	}
}

func TestReplayTenantOperatorUsesOwnTenant(t *testing.T) {
	store := &fakeStore{}
	router := chi.NewRouter()
	router.Post("/admin/v1/obs-dlq/{id}/replay", NewReplayHandler(Deps{
		Auth:  fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 8, TokenID: 3}},
		Store: store,
	}))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/v1/obs-dlq/dead-2/replay", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if store.replayTenant != 8 || store.replayActor != "admin_token:3" {
		t.Fatalf("replay tenant/actor=%d/%q want 8/admin_token:3", store.replayTenant, store.replayActor)
	}
}
