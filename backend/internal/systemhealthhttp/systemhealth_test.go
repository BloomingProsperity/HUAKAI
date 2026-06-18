package systemhealthhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeHealthSource is a controllable SystemHealthSource for unit tests.
type fakeHealthSource struct {
	chTotal     int64
	chUnhealthy int64
	chErr       error
	dlqDepth    int64
	dlqErr      error
	firingCount int64
	firingErr   error
	dbErr       error
}

func (f *fakeHealthSource) ChannelHealthSummary(_ context.Context) (int64, int64, error) {
	return f.chTotal, f.chUnhealthy, f.chErr
}
func (f *fakeHealthSource) DLQPendingDepth(_ context.Context) (int64, error) {
	return f.dlqDepth, f.dlqErr
}
func (f *fakeHealthSource) AlertingFiringCount(_ context.Context) (int64, error) {
	return f.firingCount, f.firingErr
}
func (f *fakeHealthSource) DBPing(_ context.Context) error {
	return f.dbErr
}

// TestSystemHealthAggregate: one unhealthy component -> top-level reflects degraded.
// MUTATION: if deriveTopLevel always returns "healthy" regardless of components,
// the top-level status assertion fails -> RED.
func TestSystemHealthAggregate(t *testing.T) {
	src := &fakeHealthSource{
		chTotal:     10,
		chUnhealthy: 3, // 3 channels not healthy -> degraded
	}
	h := NewSystemHealthHandler(src)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/system/health", nil)
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	// Top-level must reflect that channel_health is degraded.
	if resp.Status != TopLevelStatusDegraded {
		t.Errorf("top-level status=%q want %q; MUTATION: always-healthy impl hides degraded state",
			resp.Status, TopLevelStatusDegraded)
	}
	// All 4 components must be present.
	if len(resp.Components) != 4 {
		t.Fatalf("components=%d want 4 (database, channel_health, dlq, alerting)", len(resp.Components))
	}
	// channel_health must be degraded.
	found := false
	for _, c := range resp.Components {
		if c.Name == "channel_health" {
			found = true
			if c.Status != ComponentStatusDegraded {
				t.Errorf("channel_health status=%q want degraded", c.Status)
			}
		}
	}
	if !found {
		t.Error("channel_health component missing from response")
	}
}

// TestSystemHealthAllHealthy: all components healthy -> top-level healthy.
func TestSystemHealthAllHealthy(t *testing.T) {
	src := &fakeHealthSource{chTotal: 5}
	h := NewSystemHealthHandler(src)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/system/health", nil)
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != TopLevelStatusHealthy {
		t.Errorf("status=%q want healthy", resp.Status)
	}
}

// TestSystemHealthDBFailure: DB ping fails -> top-level unhealthy.
func TestSystemHealthDBFailure(t *testing.T) {
	src := &fakeHealthSource{dbErr: errors.New("connection refused")}
	h := NewSystemHealthHandler(src)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/system/health", nil)
	h(rec, req)

	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != TopLevelStatusUnhealthy {
		t.Errorf("status=%q want unhealthy when DB fails", resp.Status)
	}
}

// TestSystemHealthRuntimeSnapshot guards the live runtime resource snapshot: the 200
// response must carry a populated runtime block read from the Go runtime at request time.
// MUTATION: drop `Runtime: collectRuntimeInfo()` from the handler -> the runtime block is
// the zero value (go_version="", num_goroutine=0, heap_alloc_bytes=0) -> the three
// assertions below go RED. Fixtures use live runtime invariants (go-prefixed version,
// >=1 goroutine, >0 heap) that the zero value cannot satisfy.
func TestSystemHealthRuntimeSnapshot(t *testing.T) {
	h := NewSystemHealthHandler(&fakeHealthSource{chTotal: 1})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/system/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp.Runtime.GoVersion, "go") {
		t.Errorf("runtime.go_version=%q want a go… version (handler omitted the runtime snapshot?)", resp.Runtime.GoVersion)
	}
	if resp.Runtime.NumGoroutine < 1 {
		t.Errorf("runtime.num_goroutine=%d want >=1 (the serving goroutine alone)", resp.Runtime.NumGoroutine)
	}
	if resp.Runtime.HeapAllocBytes == 0 {
		t.Errorf("runtime.heap_alloc_bytes=0 want >0 (live heap)")
	}
	if resp.Runtime.UptimeSeconds < 0 {
		t.Errorf("runtime.uptime_seconds=%d want >=0", resp.Runtime.UptimeSeconds)
	}
}

// TestSystemHealthNilSource: nil source -> 503 service unavailable.
func TestSystemHealthNilSource(t *testing.T) {
	h := NewSystemHealthHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/system/health", nil)
	h(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 for nil source", rec.Code)
	}
}
