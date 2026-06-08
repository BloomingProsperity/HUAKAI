package anthropic

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// CLAUDEHDR-02:真实 Claude Code 每个会话带一个稳定的 X-Claude-Code-Session-Id,
// 每次请求带一个新的 x-client-request-id。两者缺失都是 relay 的 tell。HUAKAI 在
// 此层只有 Account.AccountID(无 inbound apiKey),故按 accountID 缓存稳定 session
// id(TTL 1h,过期重生),accountID<=0 每次新发。

const claudeSessionTTL = time.Hour

var (
	claudeSessionMu    sync.Mutex
	claudeSessionCache = map[int64]claudeSessionEntry{}
	claudeSessionOnce  sync.Once
)

type claudeSessionEntry struct {
	id     string
	expire time.Time
}

// newUUID4 生成随机 RFC-4122 v4 UUID(crypto/rand,不引外部依赖)。
func newUUID4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// cachedSessionID 返回该账号稳定的 Claude Code session id(同账号在 TTL 内不变)。
// accountID<=0 -> 每次新发(无法绑定到稳定账号)。
func cachedSessionID(accountID int64) string {
	if accountID <= 0 {
		return newUUID4()
	}
	claudeSessionOnce.Do(startClaudeSessionCleanup)
	now := time.Now()
	claudeSessionMu.Lock()
	defer claudeSessionMu.Unlock()
	if e, ok := claudeSessionCache[accountID]; ok && e.expire.After(now) {
		e.expire = now.Add(claudeSessionTTL)
		claudeSessionCache[accountID] = e
		return e.id
	}
	id := newUUID4()
	claudeSessionCache[accountID] = claudeSessionEntry{id: id, expire: now.Add(claudeSessionTTL)}
	return id
}

func startClaudeSessionCleanup() {
	go func() {
		t := time.NewTicker(claudeSessionTTL)
		defer t.Stop()
		for range t.C {
			now := time.Now()
			claudeSessionMu.Lock()
			for k, e := range claudeSessionCache {
				if !e.expire.After(now) {
					delete(claudeSessionCache, k)
				}
			}
			claudeSessionMu.Unlock()
		}
	}()
}

// applyClaudeSessionHeaders 设置稳定 session id + 每请求新 client-request-id。
func applyClaudeSessionHeaders(h http.Header, accountID int64) {
	h.Set("X-Claude-Code-Session-Id", cachedSessionID(accountID))
	h.Set("X-Client-Request-Id", newUUID4())
}
