// metrics_test.go — U6-C 测试: per-identity 请求计数 + expvar 暴露行为。
package clientid

import (
	"encoding/json"
	"expvar"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestIncrementRequestCount_Counts(t *testing.T) {
	resetMetricsForTesting()
	defer resetMetricsForTesting()

	IncrementRequestCount(IdentityCursor)
	IncrementRequestCount(IdentityCursor)
	IncrementRequestCount(IdentityClaudeCode)

	if got := RequestCount(IdentityCursor); got != 2 {
		t.Errorf("Cursor count=%d want 2", got)
	}
	if got := RequestCount(IdentityClaudeCode); got != 1 {
		t.Errorf("ClaudeCode count=%d want 1", got)
	}
	if got := RequestCount(IdentityCody); got != 0 {
		t.Errorf("Cody count=%d want 0 (未触发)", got)
	}
}

func TestIncrementRequestCount_AllKnownIdentitiesPreInitialized(t *testing.T) {
	resetMetricsForTesting()
	defer resetMetricsForTesting()

	// initCounters 应预初始化所有 known identity 为 0；这保证
	// /debug/vars 列出全集（即便某 identity 当前 0 流量）。
	for _, id := range allKnownIdentities() {
		if RequestCount(id) != 0 {
			t.Errorf("identity %q 未预初始化为 0", id)
		}
	}
}

func TestExpvarPublished_DebugVarsExposesCounter(t *testing.T) {
	resetMetricsForTesting()
	defer resetMetricsForTesting()

	IncrementRequestCount(IdentityCursor)
	IncrementRequestCount(IdentityCursor)
	IncrementRequestCount(IdentityCody)

	// 通过 stdlib expvar Handler 暴露 metrics，模拟 admin /debug/vars 路径
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/vars", nil)
	expvar.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expvar handler status=%d", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("expvar JSON parse err=%v body=%s", err, rec.Body.String())
	}
	cidMap, ok := payload["clientid_request_count"].(map[string]any)
	if !ok {
		t.Fatalf("/debug/vars 缺 clientid_request_count map: %v", payload)
	}
	// 注: expvar JSON 把 int64 序列为 float64
	if cidMap["cursor"].(float64) != 2 {
		t.Errorf("cursor=%v want 2", cidMap["cursor"])
	}
	if cidMap["cody"].(float64) != 1 {
		t.Errorf("cody=%v want 1", cidMap["cody"])
	}
}

func TestIncrementRequestCount_Concurrent(t *testing.T) {
	resetMetricsForTesting()
	defer resetMetricsForTesting()

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			IncrementRequestCount(IdentityCursor)
		}()
	}
	wg.Wait()
	if got := RequestCount(IdentityCursor); got != n {
		t.Errorf("并发 100 次后 count=%d want %d", got, n)
	}
}

// TestMiddleware_IncrementsMetrics middleware 应自动累计 per-identity 计数。
// 用 nil logger 避免 zap 噪音。
func TestMiddleware_IncrementsMetrics(t *testing.T) {
	resetMetricsForTesting()
	defer resetMetricsForTesting()

	mw := Middleware(nil)
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	wrapped := mw(final)

	// F3 BLOCKING fix: 包含一个 unrecognized UA case，让 IdentityUnknown
	// 走真实 increment 路径，避免 vacuously-true 的 0 断言（sonnet 抓到的
	// 测试逻辑漏洞——middleware 永远 increment 任意 id 包括 Unknown）。
	cases := []struct {
		ua string
	}{
		{"Cursor/0.42"},                            // → IdentityCursor
		{"Cursor/0.42"},                            // → IdentityCursor (再 1 次)
		{"claude-cli/1.0"},                         // → IdentityClaudeCode
		{"curl/7.81"},                              // → IdentityCurlScript
		{"Mozilla/5.0 some random unknown ua"},    // → IdentityUnknown (走 fallback)
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("User-Agent", c.ua)
		wrapped.ServeHTTP(httptest.NewRecorder(), req)
	}

	if got := RequestCount(IdentityCursor); got != 2 {
		t.Errorf("Cursor middleware count=%d want 2", got)
	}
	if got := RequestCount(IdentityClaudeCode); got != 1 {
		t.Errorf("ClaudeCode middleware count=%d want 1", got)
	}
	if got := RequestCount(IdentityCurlScript); got != 1 {
		t.Errorf("CurlScript middleware count=%d want 1", got)
	}
	if got := RequestCount(IdentityUnknown); got != 1 {
		t.Errorf("Unknown middleware count=%d want 1 (Mozilla UA → unknown)", got)
	}
}
