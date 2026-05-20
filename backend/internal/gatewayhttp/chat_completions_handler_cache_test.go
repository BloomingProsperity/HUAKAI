package gatewayhttp

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

type recordingSettler struct {
	calls           []billing.SettleRequest
	cacheHitCommits []int64
}

func (s *recordingSettler) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	s.calls = append(s.calls, req)
	return &billing.SettleResult{}, nil
}

func (s *recordingSettler) Abort(context.Context, int64, int64, string, string) error {
	return nil
}

func (s *recordingSettler) CommitCacheHit(_ context.Context, _ int64, claimID int64, _ string) error {
	s.cacheHitCommits = append(s.cacheHitCommits, claimID)
	return nil
}

func (s *recordingSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return &billing.RefundResult{}, nil
}

func TestChatCompletionsL2CacheHitReturnsCachedWithoutUpstreamCall(t *testing.T) {
	enableHCSFDispatchForTest(t)
	store := l2cache.NewMemoryStore(1<<20, time.Minute)
	dispatcher := &mockCanonicalBufferedDispatcher{}
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.ResponseCache = store
	d.Settler = settler

	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	first := invokeHandlerPath(t, d, "/v1/chat/completions", body)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	if got := first.Header().Get("X-HUAKAI-Cache-L2"); got != "miss" {
		t.Fatalf("first cache header=%q want miss", got)
	}
	second := invokeHandlerPath(t, d, "/v1/chat/completions", body)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Cache-L2"); got != "hit" {
		t.Fatalf("second cache header=%q want hit", got)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("dispatcher calls=%d want 1", dispatcher.calls)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("cached body mismatch:\nfirst=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
	// cache 命中走 Settler.CommitCacheHit (零成本 committed), 不走 Settle 也
	// 不走 Abort: 首次 miss 记 1 个 Settle, 第二次 hit 记 1 个 CommitCacheHit。
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1 (仅首次 commit)", len(settler.calls))
	}
	if len(settler.cacheHitCommits) != 1 {
		t.Fatalf("cache-hit commit calls=%d want 1 (命中走 CommitCacheHit 零成本结清)", len(settler.cacheHitCommits))
	}
}

func TestChatCompletionsL2CacheHitSkipsPoolCapacity(t *testing.T) {
	enableHCSFDispatchForTest(t)
	store := l2cache.NewMemoryStore(1<<20, time.Minute)

	firstDeps := clientAdapterDeps(t)
	firstDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	firstDeps.ResponseCache = store
	first := invokeHandlerPath(t, firstDeps, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}

	selector := &cacheHitFailingSelector{}
	secondDeps := clientAdapterDeps(t)
	secondDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	secondDeps.ResponseCache = store
	secondDeps.Selector = selector
	second := invokeHandlerPath(t, secondDeps, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s; want cache hit despite pool saturation", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Cache-L2"); got != "hit" {
		t.Fatalf("second cache header=%q want hit", got)
	}
	if selector.calls != 0 {
		t.Fatalf("selector calls=%d want 0 on cache hit", selector.calls)
	}
}

func TestChatCompletionsL2CacheTenantIsolation(t *testing.T) {
	enableHCSFDispatchForTest(t)
	store := l2cache.NewMemoryStore(1<<20, time.Minute)
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.ResponseCache = store
	d.Settler = &recordingSettler{}

	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	first := invokeHandlerPath(t, d, "/v1/chat/completions", body)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	d.Auth = stubAuth{identity: auth.Identity{TenantID: 8, APIKeyID: 11, UserID: 3}}
	second := invokeHandlerPath(t, d, "/v1/chat/completions", body)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Cache-L2"); got != "miss" {
		t.Fatalf("tenant 8 cache header=%q want miss", got)
	}
	if dispatcher.calls != 2 {
		t.Fatalf("dispatcher calls=%d want 2 for tenant-isolated miss", dispatcher.calls)
	}
}

type cacheHitFailingSelector struct {
	calls int
}

func (s *cacheHitFailingSelector) Select(context.Context, pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.calls++
	return nil, pool.ErrNoSlotAvailable
}

func TestChatCompletionsL2CacheStreamSkipsLookup(t *testing.T) {
	store := l2cache.NewMemoryStore(1<<20, time.Minute)
	d := minimalDeps()
	d.ResponseCache = store
	rec := invokeHandler(t, d, strings.Replace(validBody(), `"stream":true`, `"stream":true`, 1))
	if rec.Header().Get("X-HUAKAI-Cache-L2") == "hit" {
		t.Fatal("stream request must not hit L2 cache")
	}
}
