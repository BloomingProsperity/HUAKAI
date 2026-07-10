package vertexsa

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// refreshSkew 是提前量:token 剩余不足此值即视为需刷新,避免临界过期。
const refreshSkew = 5 * time.Minute

// Cache 按 SA 身份缓存已铸 token,过期(含 skew)才重铸。
// 同一 key 的并发取用被序列化,避免铸造风暴(N 个并发请求只铸一次)。
// 不缓存 private_key,key 只由 client_email + token_uri + scope 组成。
type Cache struct {
	hc      *http.Client
	skew    time.Duration
	mu      sync.Mutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	mu  sync.Mutex // 序列化同一 SA 的铸造,防风暴
	tok Token
}

// NewCache 建缓存;httpClient 为 nil 时铸造用 http.DefaultClient。
func NewCache(httpClient *http.Client) *Cache {
	return &Cache{hc: httpClient, skew: refreshSkew, entries: make(map[string]*cacheEntry)}
}

func (c *Cache) entryFor(key string) *cacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[key]
	if e == nil {
		e = &cacheEntry{}
		c.entries[key] = e
	}
	return e
}

// cacheKey 按 SA 身份生成缓存键,不含私钥。
func cacheKey(sa ServiceAccount) string {
	return strings.TrimSpace(sa.ClientEmail) + "|" + strings.TrimSpace(sa.TokenURI) + "|" + strings.TrimSpace(sa.Scope)
}

// Token 返回该 SA 的有效 access token:命中且未临近过期则直接返回缓存,
// 否则(含首次/过期/临界)铸新并回填。同一 SA 的并发调用串行,只铸一次。
func (c *Cache) Token(ctx context.Context, sa ServiceAccount, now time.Time) (Token, error) {
	e := c.entryFor(cacheKey(sa))
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.tok.AccessToken != "" && now.Add(c.skew).Before(e.tok.ExpiresAt) {
		return e.tok, nil
	}
	tok, err := Mint(ctx, c.hc, sa, now)
	if err != nil {
		return Token{}, err
	}
	e.tok = tok
	return tok, nil
}
