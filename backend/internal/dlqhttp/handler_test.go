package dlqhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

type dlqAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (a dlqAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if a.err != nil {
		return admin.AdminIdentity{}, a.err
	}
	return a.ident, nil
}

type dlqDepsStub struct {
	auth             dlqAuthStub
	store            *dlqStoreStub
	platformTenantID int64
}

func (d dlqDepsStub) AdminDLQAuth() AdminDLQAuth      { return d.auth }
func (d dlqDepsStub) AdminDLQStore() AdminDLQStore    { return d.store }
func (d dlqDepsStub) AdminDLQPlatformTenantID() int64 { return d.platformTenantID }

type dlqStoreStub struct {
	filter       dlq.ListFilter
	replayTenant int64
	replayID     int64
}

func (s *dlqStoreStub) List(_ context.Context, f dlq.ListFilter) ([]dlq.Record, error) {
	s.filter = f
	return []dlq.Record{{
		ID:             9,
		TenantID:       7,
		EventKind:      dlq.EventKindUsageRecord,
		Lane:           dlq.LaneHigh,
		Status:         dlq.StatusOperatorReview,
		Payload:        []byte(`{"ok":true}`),
		FailureReason:  "test",
		FailureAt:      time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC),
		NextRetryAt:    time.Date(2026, 5, 15, 0, 0, 1, 0, time.UTC),
		ReplicaStatus:  dlq.ReplicaStatusNone,
		ReplicaTarget:  "primary",
		IdempotencyKey: "usage_record:7:9",
		SourceTable:    "usage_records",
	}}, nil
}

func (s *dlqStoreStub) Replay(_ context.Context, tenantID, id int64, _ string) (*dlq.Record, error) {
	s.replayTenant = tenantID
	s.replayID = id
	return &dlq.Record{
		ID:             id,
		TenantID:       7,
		EventKind:      dlq.EventKindUsageRecord,
		Lane:           dlq.LaneHigh,
		Status:         dlq.StatusDelivered,
		Payload:        []byte(`{}`),
		FailureReason:  "test",
		FailureAt:      time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC),
		NextRetryAt:    time.Date(2026, 5, 15, 0, 0, 1, 0, time.UTC),
		ReplicaStatus:  dlq.ReplicaStatusNone,
		ReplicaTarget:  "primary",
		IdempotencyKey: "usage_record:7:9",
		SourceTable:    "usage_records",
	}, nil
}

func TestAdminDLQListTenantOperatorIsForcedToOwnTenant(t *testing.T) {
	store := &dlqStoreStub{}
	deps := dlqDepsStub{
		auth:  dlqAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		store: store,
	}
	rec := invokeDLQ(deps, http.MethodGet, "/admin/v1/dlq/usage_record", "")
	assertStatus(t, rec, http.StatusOK)
	if store.filter.TenantID == nil || *store.filter.TenantID != 7 {
		t.Fatalf("tenant filter=%v want 7", store.filter.TenantID)
	}

	rec = invokeDLQ(deps, http.MethodGet, "/admin/v1/dlq/usage_record?tenant_id=8", "")
	assertStatus(t, rec, http.StatusForbidden)
}

func TestAdminDLQListFiltersHandler(t *testing.T) {
	store := &dlqStoreStub{}
	deps := dlqDepsStub{
		auth:             dlqAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		store:            store,
		platformTenantID: 7,
	}
	rec := invokeDLQ(deps, http.MethodGet, "/admin/v1/dlq/billing_event_replica?status=operator_review&limit=25", "")
	assertStatus(t, rec, http.StatusOK)
	if store.filter.EventKind != dlq.EventKindBillingEventReplica || store.filter.Status != dlq.StatusOperatorReview || store.filter.Limit != 25 {
		t.Fatalf("filter mismatch: %+v", store.filter)
	}
}

func TestAdminDLQReplayUsesID(t *testing.T) {
	store := &dlqStoreStub{}
	deps := dlqDepsStub{
		auth:             dlqAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		store:            store,
		platformTenantID: 7,
	}
	rec := invokeDLQ(deps, http.MethodPost, "/admin/v1/dlq/42/replay", "")
	assertStatus(t, rec, http.StatusOK)
	if store.replayTenant != 7 || store.replayID != 42 {
		t.Fatalf("replay tenant/id=%d/%d want 7/42", store.replayTenant, store.replayID)
	}
}

func TestAdminDLQReplayTenantOperatorUsesOwnTenant(t *testing.T) {
	store := &dlqStoreStub{}
	deps := dlqDepsStub{
		auth:  dlqAuthStub{ident: admin.AdminIdentity{TokenID: 2, Role: admin.RoleTenantOperator, ScopeTenantID: 8}},
		store: store,
	}
	rec := invokeDLQ(deps, http.MethodPost, "/admin/v1/dlq/42/replay", "")
	assertStatus(t, rec, http.StatusOK)
	if store.replayTenant != 8 || store.replayID != 42 {
		t.Fatalf("replay tenant/id=%d/%d want 8/42", store.replayTenant, store.replayID)
	}
}

func invokeDLQ(deps dlqDepsStub, method, target, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Get("/admin/v1/dlq/{handler}", NewAdminDLQListHandler(deps))
	r.Post("/admin/v1/dlq/{id}/replay", NewAdminDLQReplayHandler(deps))
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("状态=%d，期望=%d，响应=%s", rec.Code, want, rec.Body.String())
	}
}
