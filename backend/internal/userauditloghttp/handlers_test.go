package userauditloghttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userauditlog"
)

func TestListAuditEventsUsesSessionIdentity(t *testing.T) {
	now := time.Date(2026, 6, 7, 10, 30, 0, 0, time.UTC)
	apiKeyID := int64(99)
	store := &stubAuditStore{
		rows: []userauditlog.EventRecord{{
			ID:         1,
			TenantID:   7,
			UserID:     42,
			Action:     userauditlog.ActionIssueAPIKey,
			Outcome:    userauditlog.OutcomeCommitted,
			APIKeyID:   &apiKeyID,
			KeyPrefix:  "hk_live_prefix1",
			Reason:     "ok",
			RequestID:  "req-1",
			OccurredAt: now,
		}},
	}
	mux := mountAuditEventsForTest(store, &sessionauth.SessionIdentity{TenantID: 7, UserID: 42})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/audit-events?tenant_id=999&user_id=888&limit=2&offset=3", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(store.calls) != 1 {
		t.Fatalf("store calls=%d want 1", len(store.calls))
	}
	got := store.calls[0]
	if got.TenantID != 7 || got.UserID != 42 || got.Limit != 2 || got.Offset != 3 {
		t.Fatalf("List request=%+v want session tenant/user and query pagination", got)
	}
	var body auditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v body=%s", err, rec.Body.String())
	}
	if body.Count != 1 || len(body.AuditEvents) != 1 {
		t.Fatalf("body count=%d len=%d want 1/1", body.Count, len(body.AuditEvents))
	}
	item := body.AuditEvents[0]
	if item.Action != userauditlog.ActionIssueAPIKey ||
		item.Outcome != userauditlog.OutcomeCommitted ||
		item.APIKeyID == nil ||
		*item.APIKeyID != apiKeyID ||
		item.KeyPrefix != "hk_live_prefix1" ||
		item.OccurredAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("response item mismatch: %+v", item)
	}
}

func TestListAuditEventsRequiresSession(t *testing.T) {
	store := &stubAuditStore{}
	mux := mountAuditEventsForTest(store, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/audit-events", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s want 401", rec.Code, rec.Body.String())
	}
	if len(store.calls) != 0 {
		t.Fatalf("store should not be called without session; calls=%d", len(store.calls))
	}
}

func TestListAuditEventsRejectsInvalidPagination(t *testing.T) {
	store := &stubAuditStore{}
	mux := mountAuditEventsForTest(store, &sessionauth.SessionIdentity{TenantID: 7, UserID: 42})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/audit-events?limit=0", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if len(store.calls) != 0 {
		t.Fatalf("store should not be called on invalid pagination; calls=%d", len(store.calls))
	}
}

func TestListAuditEventsMapsBackendError(t *testing.T) {
	store := &stubAuditStore{err: userauditlog.ErrBackend}
	mux := mountAuditEventsForTest(store, &sessionauth.SessionIdentity{TenantID: 7, UserID: 42})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/audit-events", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
}

func mountAuditEventsForTest(store AuditEventStore, ident *sessionauth.SessionIdentity) http.Handler {
	r := chi.NewRouter()
	r.Route("/v1/me", func(r chi.Router) {
		if ident != nil {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					next.ServeHTTP(w, req.WithContext(sessionauth.ContextWithSession(req.Context(), *ident)))
				})
			})
		}
		MountRoutes(r, Deps{Store: store})
	})
	return r
}

type stubAuditStore struct {
	calls []userauditlog.ListRequest
	rows  []userauditlog.EventRecord
	err   error
}

func (s *stubAuditStore) List(ctx context.Context, req userauditlog.ListRequest) ([]userauditlog.EventRecord, error) {
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	s.calls = append(s.calls, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.rows, nil
}
