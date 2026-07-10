package vertexsa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mintCountServer 起一个假 token 端点,统计实际铸造(HTTP)次数。
func mintCountServer(t *testing.T) (*httptest.Server, *int64, *http.Client) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "ya29.tok", ExpiresIn: 3600, TokenType: "Bearer"})
	}))
	target, _ := url.Parse(srv.URL)
	return srv, &hits, &http.Client{Transport: redirectTransport{target: target}}
}

// TestCacheHitDoesNotRemint 验证:有效期内第二次取用命中缓存,不再铸造(HTTP 次数不变)。
// 变异:把 Cache.Token 里的「未过期直接返回」判据删掉 → 第二次会重铸,hits==2,本测试红。
func TestCacheHitDoesNotRemint(t *testing.T) {
	_, pemStr := testKeyPEM(t)
	srv, hits, hc := mintCountServer(t)
	defer srv.Close()
	c := NewCache(hc)
	sa := ServiceAccount{ClientEmail: "svc@x.iam.gserviceaccount.com", PrivateKeyPEM: pemStr}
	now := time.Now().UTC()

	if _, err := c.Token(context.Background(), sa, now); err != nil {
		t.Fatalf("首次铸造失败: %v", err)
	}
	// 10 分钟后仍在有效期(3600s - skew 5min = 55min 内),应命中缓存。
	if _, err := c.Token(context.Background(), sa, now.Add(10*time.Minute)); err != nil {
		t.Fatalf("第二次取用失败: %v", err)
	}
	if got := atomic.LoadInt64(hits); got != 1 {
		t.Fatalf("缓存命中却重铸: hits=%d want 1", got)
	}
}

// TestCacheRemintsAfterExpiry 验证:超过有效期(含 skew)后重铸(HTTP 次数+1)。
// 变异:把过期判据改成恒 true(总返回缓存)→ hits==1,本测试红。
func TestCacheRemintsAfterExpiry(t *testing.T) {
	_, pemStr := testKeyPEM(t)
	srv, hits, hc := mintCountServer(t)
	defer srv.Close()
	c := NewCache(hc)
	sa := ServiceAccount{ClientEmail: "svc@x.iam.gserviceaccount.com", PrivateKeyPEM: pemStr}
	now := time.Now().UTC()

	if _, err := c.Token(context.Background(), sa, now); err != nil {
		t.Fatalf("首次铸造失败: %v", err)
	}
	// now+3601s 超过首个 token 的 3600s 有效期,须重铸。
	// (第二次 assertion 的 iat/exp 相对真实时间在未来,jwt 默认不校验未来 iat、exp 有效。)
	if _, err := c.Token(context.Background(), sa, now.Add(3601*time.Second)); err != nil {
		t.Fatalf("过期后重铸失败: %v", err)
	}
	if got := atomic.LoadInt64(hits); got != 2 {
		t.Fatalf("过期应重铸: hits=%d want 2", got)
	}
}

// TestCacheSerializesConcurrentMint 验证:同一 SA 的并发首次取用只铸一次(防风暴)。
// 变异:去掉 cacheEntry 的 per-key 串行锁 → 并发各自铸造,hits>1,本测试红。
func TestCacheSerializesConcurrentMint(t *testing.T) {
	_, pemStr := testKeyPEM(t)
	srv, hits, hc := mintCountServer(t)
	defer srv.Close()
	c := NewCache(hc)
	sa := ServiceAccount{ClientEmail: "svc@x.iam.gserviceaccount.com", PrivateKeyPEM: pemStr}
	now := time.Now().UTC()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Token(context.Background(), sa, now)
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt64(hits); got != 1 {
		t.Fatalf("并发首次取用应只铸一次: hits=%d want 1", got)
	}
}
