// HUAKAI · iKun

package subscriptionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/workerpulse"
)

type fakeWorkerStatsReader struct {
	stats WorkerStats
}

func (f fakeWorkerStatsReader) ReadWorkerStats(context.Context) WorkerStats {
	return f.stats
}

func TestAdminWorkerStatsRejectsTenantOperator(t *testing.T) {
	h := NewAdminWorkerStatsHandler(AdminWorkerStatsDeps{
		Auth:   fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator}},
		Reader: fakeWorkerStatsReader{stats: sampleWorkerStats()},
	})

	req := httptest.NewRequest(http.MethodGet, "/worker-stats", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminWorkerStatsRejectsUnauthenticated(t *testing.T) {
	h := NewAdminWorkerStatsHandler(AdminWorkerStatsDeps{
		Auth:   fakeAdminAuth{err: admin.ErrAdminUnauthorized},
		Reader: fakeWorkerStatsReader{stats: sampleWorkerStats()},
	})

	req := httptest.NewRequest(http.MethodGet, "/worker-stats", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminWorkerStatsReturnsCountersForPlatformAdmin(t *testing.T) {
	h := NewAdminWorkerStatsHandler(AdminWorkerStatsDeps{
		Auth:      fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		Reader:    fakeWorkerStatsReader{stats: sampleWorkerStats()},
		Pulses:    workerpulse.NewMemoryStore(),
		ReplicaID: "node-b",
	})

	req := httptest.NewRequest(http.MethodGet, "/worker-stats", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got WorkerStats
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rec.Body.String())
	}
	want := sampleWorkerStats()
	if got.Reminder.TickCount != want.Reminder.TickCount ||
		got.Reminder.SentTotal != want.Reminder.SentTotal ||
		got.Reminder.FailedTicks != want.Reminder.FailedTicks ||
		got.Expiry.TickCount != want.Expiry.TickCount ||
		got.Expiry.ExpiredTotal != want.Expiry.ExpiredTotal ||
		got.Expiry.FailedTicks != want.Expiry.FailedTicks ||
		got.PendingReconciliation != want.PendingReconciliation {
		t.Fatalf("worker stats = %+v, want %+v", got, want)
	}
	// B10: 自动续费 money 计数进响应 (此前无读者)。
	if got.AutoRenew.Enabled != want.AutoRenew.Enabled ||
		got.AutoRenew.TickCount != want.AutoRenew.TickCount ||
		got.AutoRenew.RenewedTotal != want.AutoRenew.RenewedTotal ||
		got.AutoRenew.SkippedTotal != want.AutoRenew.SkippedTotal ||
		got.AutoRenew.FailedTicks != want.AutoRenew.FailedTicks {
		t.Fatalf("auto_renew stats = %+v, want %+v (续费 money 指标未暴露)", got.AutoRenew, want.AutoRenew)
	}
	if got.AnsweringReplica != "node-b" {
		t.Fatalf("answering_replica=%q", got.AnsweringReplica)
	}
	if got.Reminder.Cluster.Running {
		t.Fatalf("empty pulse store must not look running: %+v", got.Reminder.Cluster)
	}
}

func TestAdminWorkerStatsShowsRemoteExecutorNotAnsweringReplica(t *testing.T) {
	store := workerpulse.NewMemoryStore()
	now := time.Now().UTC()
	if err := store.Record(context.Background(), workerpulse.Record{
		JobKey: workerpulse.JobSubscriptionExpiry, ReplicaID: "node-a",
		SeenAt: now, SuccessAt: now, Executor: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(context.Background(), workerpulse.Record{
		JobKey: workerpulse.JobSubscriptionExpiry, ReplicaID: "node-b",
		SeenAt: now, Executor: false,
	}); err != nil {
		t.Fatal(err)
	}
	h := NewAdminWorkerStatsHandler(AdminWorkerStatsDeps{
		Auth:      fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		Reader:    fakeWorkerStatsReader{stats: sampleWorkerStats()},
		Pulses:    store,
		ReplicaID: "node-b",
	})
	req := httptest.NewRequest(http.MethodGet, "/worker-stats", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got WorkerStats
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AnsweringReplica != "node-b" || got.Expiry.Cluster.ExecutorReplica != "node-a" || !got.Expiry.Cluster.Running {
		t.Fatalf("answering=%q expiry cluster=%+v", got.AnsweringReplica, got.Expiry.Cluster)
	}
	if got.Expiry.TickCount != 13 {
		t.Fatalf("process counters must stay answering-local: %d", got.Expiry.TickCount)
	}
}

func TestAdminWorkerStatsNilPulsesFailsClosed(t *testing.T) {
	h := NewAdminWorkerStatsHandler(AdminWorkerStatsDeps{
		Auth:   fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		Reader: fakeWorkerStatsReader{stats: sampleWorkerStats()},
	})
	req := httptest.NewRequest(http.MethodGet, "/worker-stats", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminWorkerStatsNilReaderFailsClosed(t *testing.T) {
	h := NewAdminWorkerStatsHandler(AdminWorkerStatsDeps{
		Auth: fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
	})

	req := httptest.NewRequest(http.MethodGet, "/worker-stats", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}

func sampleWorkerStats() WorkerStats {
	return WorkerStats{
		Reminder:  ReminderWorkerStats{TickCount: 11, SentTotal: 7, FailedTicks: 2},
		Expiry:    ExpiryWorkerStats{TickCount: 13, ExpiredTotal: 5, FailedTicks: 3},
		AutoRenew: AutoRenewWorkerStats{Enabled: true, TickCount: 9, RenewedTotal: 4, SkippedTotal: 6, FailedTicks: 1},
		PendingReconciliation: PendingReconciliationWorkerStats{
			UsageRecords: 17,
		},
	}
}
