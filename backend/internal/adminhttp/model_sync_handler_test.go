package adminhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
)

func TestModelSyncHandlerRequiresPlatformAdminBeforeService(t *testing.T) {
	svc := &modelSyncServiceStub{}
	rec := invokeModelSync(t, AdminModelSyncDeps{
		Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
		Service: svc,
	}, "")

	assertModelSyncStatus(t, rec, http.StatusForbidden)
	if svc.calls != 0 {
		t.Fatalf("tenant operator triggered global model sync: calls=%d", svc.calls)
	}
}

func TestModelSyncHandlerTriggersSyncForPlatformAdmin(t *testing.T) {
	completed := time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC)
	svc := &modelSyncServiceStub{result: modelsync.SyncResult{
		CompletedAt:   completed,
		TotalAdded:    2,
		TotalUpdated:  1,
		TotalDisabled: 1,
		Results: []modelsync.ApplyResult{
			{Vendor: modelsync.VendorOpenAI, Added: 1},
			{Vendor: modelsync.VendorGemini, Added: 1, Updated: 1, Disabled: 1},
		},
	}}
	rec := invokeModelSync(t, AdminModelSyncDeps{
		Auth:    apiKeyAuthStub{ident: platformAdmin()},
		Service: svc,
	}, `{"reason":"manual refresh"}`)

	assertModelSyncStatus(t, rec, http.StatusOK)
	if svc.calls != 1 || svc.lastReason != "manual refresh" || svc.lastActor != "admin_token:11" {
		t.Fatalf("sync call mismatch calls=%d reason=%q actor=%q", svc.calls, svc.lastReason, svc.lastActor)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"object":"admin_model_sync_result"`,
		`"total_added":2`,
		`"total_updated":1`,
		`"total_disabled":1`,
		`"vendor":"openai"`,
		`"completed_at":"2026-06-02T08:00:00Z"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("response body missing %s: %s", want, body)
		}
	}
}

func TestModelSyncHandlerFailureIsServiceUnavailable(t *testing.T) {
	svc := &modelSyncServiceStub{err: errors.New("fetch failed")}
	rec := invokeModelSync(t, AdminModelSyncDeps{
		Auth:    apiKeyAuthStub{ident: platformAdmin()},
		Service: svc,
	}, "")

	assertModelSyncStatus(t, rec, http.StatusServiceUnavailable)
	if !strings.Contains(rec.Body.String(), "model_sync_failed") {
		t.Fatalf("body=%s want model_sync_failed", rec.Body.String())
	}
}

func TestModelSyncHandlerRejectsOverlongReason(t *testing.T) {
	svc := &modelSyncServiceStub{}
	rec := invokeModelSync(t, AdminModelSyncDeps{
		Auth:    apiKeyAuthStub{ident: platformAdmin()},
		Service: svc,
	}, `{"reason":"`+strings.Repeat("x", 201)+`"}`)

	assertModelSyncStatus(t, rec, http.StatusBadRequest)
	if svc.calls != 0 {
		t.Fatalf("overlong reason triggered sync: calls=%d", svc.calls)
	}
}

type modelSyncServiceStub struct {
	result     modelsync.SyncResult
	err        error
	calls      int
	lastReason string
	lastActor  string
}

func (s *modelSyncServiceStub) SyncWithActor(ctx context.Context, reason, actor string) (modelsync.SyncResult, error) {
	s.calls++
	s.lastReason = reason
	s.lastActor = actor
	if s.err != nil {
		return modelsync.SyncResult{}, s.err
	}
	return s.result, nil
}

func invokeModelSync(t *testing.T, deps AdminModelSyncDeps, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/model-sync", func(r chi.Router) {
		MountModelSyncRoutes(r, deps)
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/model-sync", strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func assertModelSyncStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, want, strings.TrimSpace(rec.Body.String()))
	}
}

var _ = admin.RolePlatformAdmin
