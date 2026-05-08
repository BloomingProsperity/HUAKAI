// cachemetrics_test.go — vendor prompt cache 计数 + expvar 暴露测试。
package cachemetrics

import (
	"encoding/json"
	"expvar"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestObserve_Counts(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	Observe(100, 50) // request 1: 100 写入, 50 读
	Observe(0, 200)  // request 2: 0 写入 (cache hit), 200 读
	Observe(80, 0)   // request 3: 80 写入 (first-time prefix), 0 读

	creation, read, req := Snapshot()
	if creation != 180 {
		t.Errorf("creation=%d want 180", creation)
	}
	if read != 250 {
		t.Errorf("read=%d want 250", read)
	}
	if req != 3 {
		t.Errorf("requests=%d want 3", req)
	}
}

func TestObserve_ZeroInputDoesNotIncrement(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	// vendor 未启用 caching → cache fields = 0/0 → 不应膨胀分母
	Observe(0, 0)
	creation, read, req := Snapshot()
	if creation != 0 || read != 0 || req != 0 {
		t.Errorf("0/0 不应增 counter; 得 c=%d r=%d req=%d", creation, read, req)
	}
}

func TestExpvarPublished(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	Observe(50, 100)
	Observe(0, 200)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/vars", nil)
	expvar.Handler().ServeHTTP(rec, req)

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("expvar parse err=%v", err)
	}
	cm, ok := payload["cache_token_count"].(map[string]any)
	if !ok {
		t.Fatalf("缺 cache_token_count map: %v", payload)
	}
	if cm["creation_total"].(float64) != 50 {
		t.Errorf("creation_total=%v want 50", cm["creation_total"])
	}
	if cm["read_total"].(float64) != 300 {
		t.Errorf("read_total=%v want 300", cm["read_total"])
	}
	if cm["request_count"].(float64) != 2 {
		t.Errorf("request_count=%v want 2", cm["request_count"])
	}
}

func TestObserve_Concurrent(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			Observe(10, 5)
		}()
	}
	wg.Wait()
	c, r, req := Snapshot()
	if c != 1000 || r != 500 || req != 100 {
		t.Errorf("并发 100 次后 c=%d r=%d req=%d want 1000/500/100", c, r, req)
	}
}
