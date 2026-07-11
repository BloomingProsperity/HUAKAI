package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

type recordingSettler struct {
	calls           []billing.SettleRequest
	aborts          []recordedAbort
	cacheHitCommits []int64
}

type recordedAbort struct {
	tenantID            int64
	claimID             int64
	reason              string
	auditRequestID      string
	observedInputTokens int64
	protocolLoss        json.RawMessage
}

func (s *recordingSettler) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	s.calls = append(s.calls, req)
	return &billing.SettleResult{}, nil
}

func (s *recordingSettler) Abort(_ context.Context, tenantID, claimID int64, reason, auditRequestID string, observedInputTokens int64, protocolLoss json.RawMessage) error {
	s.aborts = append(s.aborts, recordedAbort{
		tenantID:            tenantID,
		claimID:             claimID,
		reason:              reason,
		auditRequestID:      auditRequestID,
		observedInputTokens: observedInputTokens,
		protocolLoss:        protocolLoss,
	})
	return nil
}

func (s *recordingSettler) CommitCacheHit(_ context.Context, req billing.SettleRequest) error {
	s.cacheHitCommits = append(s.cacheHitCommits, req.ClaimID)
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

func TestChatCompletionsL2CacheEndpointFamilyIsolation(t *testing.T) {
	enableHCSFDispatchForTest(t)
	store := l2cache.NewMemoryStore(1<<20, time.Minute)
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.ResponseCache = store
	d.Settler = &recordingSettler{}

	body := `{"model":"gpt-4o","stream":false,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	first := invokeHandlerPath(t, d, "/v1/chat/completions", body)
	if first.Code != http.StatusOK {
		t.Fatalf("chat status=%d body=%s", first.Code, first.Body.String())
	}
	if got := first.Header().Get("X-HUAKAI-Cache-L2"); got != "miss" {
		t.Fatalf("chat cache header=%q want miss", got)
	}
	if !strings.Contains(first.Body.String(), `"object":"chat.completion"`) {
		t.Fatalf("chat body=%s want OpenAI chat response shape", first.Body.String())
	}

	second := invokeMessagesHandler(t, d, body)
	if second.Code != http.StatusOK {
		t.Fatalf("messages status=%d body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Cache-L2"); got != "miss" {
		t.Fatalf("messages cache header=%q want miss across endpoint family", got)
	}
	if strings.Contains(second.Body.String(), `"object":"chat.completion"`) {
		t.Fatalf("messages endpoint received OpenAI chat cached body: %s", second.Body.String())
	}
	if !strings.Contains(second.Body.String(), `"type":"message"`) {
		t.Fatalf("messages body=%s want Anthropic message response shape", second.Body.String())
	}
	if dispatcher.calls != 2 {
		t.Fatalf("dispatcher calls=%d want 2 because endpoint families must not share L2 body", dispatcher.calls)
	}
}

func TestChatCompletionsFallbackSuccessDoesNotPoisonPrimaryModelCacheKey(t *testing.T) {
	enableHCSFDispatchForTest(t)
	store := l2cache.NewMemoryStore(1<<20, time.Minute)
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`

	firstDispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusInternalServerError, body: `{"error":"primary failed"}`},
			{successText: "fallback model response"},
		},
	}
	firstDeps := pr5NonStreamDeps(t, newPR5Selector(t, 1201, 1202), &pr5ClaimGate{claimID: 88201}, &recordingSettler{}, firstDispatcher)
	firstDeps.ResponseCache = store
	firstDeps.Router = stubRouter{plan: pr5RoutePlan(
		router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
		router.AttemptPlan{Index: 1, PoolGroupID: 42, UpstreamModelID: "gpt-4o-mini", Reason: "model_fallback"},
	)}

	first := invokeHandlerPath(t, firstDeps, "/v1/chat/completions", body)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s; want fallback success", first.Code, first.Body.String())
	}
	if firstDispatcher.calls != 2 {
		t.Fatalf("first dispatcher calls=%d want 2", firstDispatcher.calls)
	}

	secondDispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{{successText: "fresh primary response"}},
	}
	secondDeps := pr5NonStreamDeps(t, newPR5Selector(t, 1203), &pr5ClaimGate{claimID: 88202}, &recordingSettler{}, secondDispatcher)
	secondDeps.ResponseCache = store
	secondDeps.Router = stubRouter{plan: pr5RoutePlan(
		router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
	)}

	second := invokeHandlerPath(t, secondDeps, "/v1/chat/completions", body)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s; want fresh primary success", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Cache-L2"); got != "miss" {
		t.Fatalf("second cache header=%q want miss; fallback response must not be stored under primary model key", got)
	}
	if secondDispatcher.calls != 1 {
		t.Fatalf("second dispatcher calls=%d want 1; primary model cache key must not be poisoned", secondDispatcher.calls)
	}
	if strings.Contains(second.Body.String(), "fallback model response") {
		t.Fatalf("second body reused fallback response under primary key: %s", second.Body.String())
	}
}

func TestChatCompletionsL2CacheHitRejectsMissingAuditRefBeforeCommit(t *testing.T) {
	// 变异: 删除 CommitCacheHit 前的 validator 时，第二次 cache hit 会写 cache-hit commit，导致 commit calls 变成 1。
	enableHCSFDispatchForTest(t)
	store := l2cache.NewMemoryStore(1<<20, time.Minute)
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`

	firstDeps := clientAdapterDeps(t)
	firstDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	firstDeps.ResponseCache = store
	first := invokeHandlerPath(t, firstDeps, "/v1/chat/completions", body)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}

	settler := &recordingSettler{}
	secondDeps := clientAdapterDeps(t)
	secondDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	secondDeps.ResponseCache = store
	secondDeps.Settler = settler
	secondDeps.AuditRefPolicy = productionAuditRefPolicyForGatewayTest(false)
	second := invokeHandlerPath(t, secondDeps, "/v1/chat/completions", body)
	if second.Code != http.StatusInternalServerError {
		t.Fatalf("second status=%d want 500 body=%s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), clienterr.CodeAuditRefMissing) {
		t.Fatalf("second body=%s want %s", second.Body.String(), clienterr.CodeAuditRefMissing)
	}
	if len(settler.cacheHitCommits) != 0 {
		t.Fatalf("cache-hit commits=%d want 0", len(settler.cacheHitCommits))
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != clienterr.CodeAuditRefMissing {
		t.Fatalf("aborts=%+v want one %s abort", settler.aborts, clienterr.CodeAuditRefMissing)
	}
}

func TestChatCompletionsL2CacheHitAllowsDLQRefBeforeCommit(t *testing.T) {
	// 变异: 删除 Deferred ledger result 到 cache-hit audit event DLQRef 的映射时，第二次 cache hit 会被拒绝且 commit calls 保持 0。
	enableHCSFDispatchForTest(t)
	store := l2cache.NewMemoryStore(1<<20, time.Minute)
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`

	firstDeps := clientAdapterDeps(t)
	firstDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	firstDeps.ResponseCache = store
	first := invokeHandlerPath(t, firstDeps, "/v1/chat/completions", body)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}

	settler := &recordingSettler{}
	secondDeps := clientAdapterDeps(t)
	secondDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	secondDeps.ResponseCache = store
	secondDeps.Settler = settler
	secondDeps.AuditRefPolicy = productionAuditRefPolicyForGatewayTest(false)
	enableDeferredAuditLedgerForGatewayTest(t, &secondDeps, 403)
	second := invokeHandlerPath(t, secondDeps, "/v1/chat/completions", body)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d want 200 body=%s", second.Code, second.Body.String())
	}
	if len(settler.cacheHitCommits) != 1 {
		t.Fatalf("cache-hit commits=%d want 1", len(settler.cacheHitCommits))
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("aborts=%+v want none", settler.aborts)
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

// replayClaimGate 模拟 ClaimGate: Reserve 返回固定 ClaimID; hit=true 时额外置
// IdempotencyHit (模拟同 idempotency-key 重试)。
type replayClaimGate struct {
	claimID int64
	hit     bool
}

func (g replayClaimGate) Reserve(context.Context, billing.ReserveRequest) (*billing.ReserveResult, error) {
	return &billing.ReserveResult{ClaimID: g.claimID, AttemptSeq: 1, IdempotencyHit: g.hit}, nil
}

func invokeWithIdempotencyKey(t *testing.T, deps ChatHandlerDeps, body, idemKey string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewChatCompletionsHandler(deps)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idemKey)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func invokeMessagesHandler(t *testing.T, deps ChatHandlerDeps, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewMessagesHandler(deps)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// AT-4: 带 Idempotency-Key 的重试 → 从持久重放表取回原始响应重放返 200。
func TestChatCompletionsIdempotentHitReplaysFromStore(t *testing.T) {
	enableHCSFDispatchForTest(t)
	replayStore := billing.NewMemoryReplayStore()
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`

	firstDeps := clientAdapterDeps(t)
	firstDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	firstDeps.ClaimGate = replayClaimGate{claimID: 777}
	firstDeps.ReplayStore = replayStore
	first := invokeWithIdempotencyKey(t, firstDeps, body, "idem-key-1")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, 777)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("first response did not record JSON replay")
	}
	if stored.ContentType != idempotencyReplayContentTypeJSON {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeJSON)
	}

	secondDeps := clientAdapterDeps(t)
	secondDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	secondDeps.ClaimGate = replayClaimGate{claimID: 777, hit: true}
	secondDeps.ReplayStore = replayStore
	second := invokeWithIdempotencyKey(t, secondDeps, body, "idem-key-1")
	if second.Code != http.StatusOK {
		t.Fatalf("idempotent retry status=%d want 200 (store replay); body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Idempotency-Hit"); got != "true" {
		t.Fatalf("replay header X-HUAKAI-Idempotency-Hit=%q want true", got)
	}
	if got := second.Header().Get("Content-Type"); !strings.HasPrefix(got, idempotencyReplayContentTypeJSON) {
		t.Fatalf("replay Content-Type=%q want %s", got, idempotencyReplayContentTypeJSON)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay body mismatch:\nfirst=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
}

// AT-5: 幂等命中但持久重放表无记录 → 回 409 replay_without_cache。
func TestChatCompletionsIdempotentHitMissReturns409(t *testing.T) {
	enableHCSFDispatchForTest(t)
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.ClaimGate = replayClaimGate{claimID: 888, hit: true}
	d.ReplayStore = billing.NewMemoryReplayStore() // 空
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	rec := invokeWithIdempotencyKey(t, d, body, "idem-key-miss")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409 (idempotent hit, no stored response); body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "replay_without_cache") {
		t.Fatalf("body=%q want replay_without_cache", rec.Body.String())
	}
}

// tamperL2Store 嵌入真实 MemoryStore: Set 时捕获条目; 一旦 poisonTenant 被设, Get 返回
// 捕获条目但把 TenantID 改成 poisonTenant(模拟 key 被弱化/缓存被污染取到别租户条目)。
type tamperL2Store struct {
	*l2cache.MemoryStore
	captured     l2cache.Entry
	hasCaptured  bool
	poisonTenant int64
}

func (s *tamperL2Store) Set(ctx context.Context, e l2cache.Entry) bool {
	s.captured = e
	s.hasCaptured = true
	return s.MemoryStore.Set(ctx, e)
}

func (s *tamperL2Store) Get(ctx context.Context, key string) (l2cache.Entry, bool) {
	if s.poisonTenant != 0 && s.hasCaptured {
		e := s.captured
		e.TenantID = s.poisonTenant
		return e, true
	}
	return s.MemoryStore.Get(ctx, key)
}

// 守 缓存-P0a(纵深防御): 即使 Get 取到一条 TenantID 与请求租户不符的缓存条目(key 被弱化/
// 污染), 也必须**拒绝 serve**, 走正常上游, 绝不把别租户的响应交付出去。
// 变异: 去掉 serveL2CacheIfAvailable 里的 cached.TenantID!=ident.TenantID 断言 -> 毒化条目
// 被 serve -> dispatcher 仅被调 1 次(第二次命中毒化缓存)-> 断言红。
func TestChatCompletionsL2RejectsTenantMismatchedEntry(t *testing.T) {
	enableHCSFDispatchForTest(t)
	tamper := &tamperL2Store{MemoryStore: l2cache.NewMemoryStore(1<<20, time.Minute)}
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.ResponseCache = tamper

	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	// 首次: miss -> 上游 -> 捕获存入条目
	first := invokeHandlerPath(t, d, "/v1/chat/completions", body)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	if !tamper.hasCaptured {
		t.Fatal("first request did not store an L2 entry")
	}
	// 毒化: 同一条(含合法 envelope)但 TenantID 改成别租户
	tamper.poisonTenant = tamper.captured.TenantID + 99999
	// 二次: Get 返回毒化条目 -> 必须被拒 -> 再次走上游
	second := invokeHandlerPath(t, d, "/v1/chat/completions", body)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if dispatcher.calls != 2 {
		t.Fatalf("dispatcher calls=%d want 2 (tenant-mismatched cache entry must NOT be served; 2nd request must hit upstream)", dispatcher.calls)
	}
	if got := second.Header().Get("X-HUAKAI-Cache-L2"); got == "hit" {
		t.Fatalf("served a tenant-mismatched cache entry (X-HUAKAI-Cache-L2=hit); defense-in-depth tenant assertion missing")
	}
}

// 守 缓存-P0c(principal-scope, 默认 apikey): 同租户不同 api-key 在 apikey scope 下**不共享**缓存
// (堵死红队中危: reseller 同租户跨用户共享响应/探针)。变异: 把 principal 移出 key(退回 tenant)
// -> 第二个 api-key 命中第一个的缓存 -> dispatcher 仅 1 次 -> 红。
func TestChatCompletionsL2ApikeyScopeIsolatesCrossApiKey(t *testing.T) {
	enableHCSFDispatchForTest(t)
	store := l2cache.NewMemoryStore(1<<20, time.Minute)
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.ResponseCache = store
	d.CacheScope = "apikey"
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	d.Auth = stubAuth{identity: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 3}}
	first := invokeHandlerPath(t, d, "/v1/chat/completions", body)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	d.Auth = stubAuth{identity: auth.Identity{TenantID: 7, APIKeyID: 22, UserID: 4}}
	second := invokeHandlerPath(t, d, "/v1/chat/completions", body)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if dispatcher.calls != 2 {
		t.Fatalf("dispatcher calls=%d want 2 (apikey scope: different api-key must NOT share tenant-mate cache)", dispatcher.calls)
	}
	if got := second.Header().Get("X-HUAKAI-Cache-L2"); got == "hit" {
		t.Fatalf("cross-apikey cache hit under apikey scope (header=hit); principal isolation broken")
	}
}

// 守: tenant scope(显式配)下同租户跨 api-key 仍共享(最大命中)—— 确认 HUAKAI_CACHE_L2_SCOPE knob 生效。
func TestChatCompletionsL2TenantScopeSharesAcrossApiKey(t *testing.T) {
	enableHCSFDispatchForTest(t)
	store := l2cache.NewMemoryStore(1<<20, time.Minute)
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.ResponseCache = store
	d.CacheScope = "tenant"
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	d.Auth = stubAuth{identity: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 3}}
	first := invokeHandlerPath(t, d, "/v1/chat/completions", body)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	d.Auth = stubAuth{identity: auth.Identity{TenantID: 7, APIKeyID: 22, UserID: 4}}
	second := invokeHandlerPath(t, d, "/v1/chat/completions", body)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if dispatcher.calls != 1 {
		t.Fatalf("dispatcher calls=%d want 1 (tenant scope: api-keys in same tenant SHARE cache)", dispatcher.calls)
	}
	if got := second.Header().Get("X-HUAKAI-Cache-L2"); got != "hit" {
		t.Fatalf("tenant scope cross-apikey should hit; header=%q", got)
	}
}
