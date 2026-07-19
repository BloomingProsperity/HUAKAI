package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// b6BackendErrStore 包一个真实 MemoryStore(让 Create 正常铸出有效 token),
// 但强制 LookupSessionToken 返回一个瞬时后端故障(模拟 PG 连接断 / 查询中途
// ctx 取消 / 连接池耗尽)。该错误既非 pgx.ErrNoRows 也非 ErrTokenNotFound。
type b6BackendErrStore struct {
	*usersession.MemoryStore
	lookupErr error
}

func (s *b6BackendErrStore) LookupSessionToken(context.Context, []byte) (usersession.SessionRecord, error) {
	return usersession.SessionRecord{}, s.lookupErr
}

// TestSessionMiddleware_B6_BackendErrorMapsTo503 是 B6 [S3] 的判别测试。
//
// 正确行为: 当 session-store 在校验期间发生瞬时后端故障(非 ErrNoRows 的裸错误),
// 持有【有效 token】的合法用户应收到 HTTP 503(基础设施暂时不可用),而【不是】
// 401 session_token_invalid。后者会诱导客户端丢弃有效 token / 强制重登 / refresh 风暴,
// 且与同仓 api_key_resolver 的 ErrAuthBackend->503 约定不一致。
//
// 缺陷代码路径: PostgresStore.LookupSessionToken 只把 pgx.ErrNoRows 映射为
// ErrTokenNotFound,其余后端错误裸上抛;Service.Validate 原样透传;
// SessionMiddleware 的 switch 只特判 ErrSigningKeyMissing/ErrTokenExpired,
// 其余落 default -> 401。故本测试在修复前应为 RED(拿到 401),修复后 GREEN(503)。
func TestSessionMiddleware_B6_BackendErrorMapsTo503(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)

	backend := errors.New("dial tcp 10.0.0.5:5432: connect: connection refused")
	store := &b6BackendErrStore{MemoryStore: usersession.NewMemoryStore(), lookupErr: backend}

	svc := usersession.NewService(store)
	svc.Now = func() time.Time { return now }
	svc.SessionTTL = time.Minute
	svc.RefreshTTL = time.Hour
	// 32 字节签名密钥,足以通过 signPayload/verifyPayload 的长度门槛。
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte('a' + (i % 26))
	}
	svc.SigningKey = key

	// 铸出一个真正有效的 session token(Create 走内嵌 MemoryStore,不碰被改写的 lookup)。
	issued, err := svc.Create(ctx, usersession.CreateInput{TenantID: 1, UserID: 42, IP: "10.1.2.3", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if issued.SessionToken == "" {
		t.Fatalf("empty session token")
	}

	// 前置校验: 这个有效 token 在健康 store 下应当能过(排除 token 本身无效的干扰)。
	if _, err := svc.Validate(ctx, issued.SessionToken, "10.1.2.3", "Chrome/1"); !errors.Is(err, backend) && err == nil {
		// 走到这里说明 lookup 没被打断——不应发生,因为我们强制其失败。
		t.Fatalf("expected lookup to fail with backend error, got nil")
	}

	handlerHit := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerHit = true
		w.WriteHeader(http.StatusOK)
	})
	h := SessionMiddleware(svc, nil)(next)

	req := httptest.NewRequest(http.MethodGet, "/api/whatever", nil)
	req.Header.Set("Authorization", "Bearer "+issued.SessionToken)
	req.RemoteAddr = "10.1.2.3:5555"
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if handlerHit {
		t.Fatalf("next handler must not run when validation errors")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("backend outage during validation: status=%d body=%s; want 503 (must NOT collapse a valid-token holder into 401 during infra outage)", rec.Code, rec.Body.String())
	}
}
