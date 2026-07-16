package gatewayhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
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
	auth  dlqAuthStub
	store *dlqStoreStub
}

func (d dlqDepsStub) AdminDLQAuth() AdminDLQAuth   { return d.auth }
func (d dlqDepsStub) AdminDLQStore() AdminDLQStore { return d.store }

type dlqStoreStub struct {
	filter   dlq.ListFilter
	replayID int64
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

func (s *dlqStoreStub) Replay(_ context.Context, id int64, _ string) (*dlq.Record, error) {
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

func TestAdminDLQListRequiresPlatformAdmin(t *testing.T) {
	deps := dlqDepsStub{
		auth:  dlqAuthStub{ident: admintest.TenantOperator(1, 7)},
		store: &dlqStoreStub{},
	}
	rec := invokeDLQ(deps, http.MethodGet, "/admin/v1/dlq/usage_record", "")
	assertStatus(t, rec, http.StatusForbidden)
}

func TestAdminDLQListFiltersHandler(t *testing.T) {
	store := &dlqStoreStub{}
	deps := dlqDepsStub{
		auth:  dlqAuthStub{ident: admintest.Platform(1)},
		store: store,
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
		auth:  dlqAuthStub{ident: admintest.Platform(1)},
		store: store,
	}
	rec := invokeDLQ(deps, http.MethodPost, "/admin/v1/dlq/42/replay", "")
	assertStatus(t, rec, http.StatusOK)
	if store.replayID != 42 {
		t.Fatalf("replayID=%d want 42", store.replayID)
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
