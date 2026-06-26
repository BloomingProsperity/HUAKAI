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

// fakeHealthSource 是供单元测试使用的可控 SystemHealthSource。
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

// TestSystemHealthAggregate:一个组件不健康 -> 顶层状态反映为 degraded。
// 变异:若 deriveTopLevel 无论组件如何都永远返回 "healthy",
// 顶层状态断言就会失败 -> 变红。
func TestSystemHealthAggregate(t *testing.T) {
	src := &fakeHealthSource{
		chTotal:     10,
		chUnhealthy: 3, // 3 个 channel 不健康 -> degraded
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
	// 顶层状态必须反映 channel_health 处于 degraded。
	if resp.Status != TopLevelStatusDegraded {
		t.Errorf("top-level status=%q want %q; MUTATION: always-healthy impl hides degraded state",
			resp.Status, TopLevelStatusDegraded)
	}
	// 全部 4 个组件都必须存在。
	if len(resp.Components) != 4 {
		t.Fatalf("components=%d want 4 (database, channel_health, dlq, alerting)", len(resp.Components))
	}
	// channel_health 必须为 degraded。
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

// TestSystemHealthAllHealthy:所有组件健康 -> 顶层状态 healthy。
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

// TestSystemHealthDBFailure:DB ping 失败 -> 顶层状态 unhealthy。
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

// TestSystemHealthRuntimeSnapshot 守护实时运行时资源快照:200 响应必须携带一个
// 已填充的 runtime 块,该块在请求时从 Go runtime 读取。
// 变异:从 handler 删除 `Runtime: collectRuntimeInfo()` -> runtime 块变成
// 零值(go_version=""、num_goroutine=0、heap_alloc_bytes=0)-> 下面三个
// 断言变红。这些 fixture 使用了零值无法满足的实时运行时不变量(以 go 为前缀的版本号、
// >=1 个 goroutine、>0 的 heap)。
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

// TestSystemHealthNilSource:nil source -> 503 service unavailable。
func TestSystemHealthNilSource(t *testing.T) {
	h := NewSystemHealthHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/system/health", nil)
	h(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 for nil source", rec.Code)
	}
}
