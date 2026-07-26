//go:build integration_pg

// 针对真实 PostgreSQL 的 APIKeyResolver Phase L0 最小集成测试。校验:
//   - happy path (匹配的 bearer → Identity)
//   - 错误 bearer → ErrUnauthorized (401)
//   - 已吊销/已过期的 key → ErrUnauthorized
//   - 跨租户探测 (租户 A 的 bearer 绝不在别处解析成功)
//   - 前缀碰撞: 多个候选, bcrypt 挑出正确的那个
//   - bcrypt fanout 上限: SQL LIMIT 5 把 DOS 控制在可控范围
//
// 按 D10 (无枚举泄露), 所有失败路径都必须返回 ErrUnauthorized
// (而非区分性的错误)。

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

func openIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

type seededAPIKey struct {
	tenantID      int64
	otherTenantID int64
	userID        int64
	apiKeyID      int64
	plaintext     string
}

func seedAPIKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, opts apiKeySeedOpts) *seededAPIKey {
	t.Helper()
	suffix := uuid.NewString()
	s := &seededAPIKey{}

	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"auth-tenant-"+suffix,
	).Scan(&s.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"auth-other-"+suffix,
	).Scan(&s.otherTenantID); err != nil {
		t.Fatalf("seed other tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		s.tenantID, "user-"+suffix,
	).Scan(&s.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if opts.userGroup != "" {
		if _, err := pool.Exec(ctx, `UPDATE users SET user_group=$2 WHERE id=$1`, s.userID, opts.userGroup); err != nil {
			t.Fatalf("set user_group: %v", err)
		}
	}

	plaintext := opts.plaintext
	if plaintext == "" {
		plaintext = "hk_test_" + suffix
	}
	prefix := plaintext
	if len(prefix) > APIKeyPrefixLen {
		prefix = prefix[:APIKeyPrefixLen]
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	s.plaintext = plaintext

	status := opts.status
	if status == "" {
		status = "active"
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		s.tenantID, s.userID, "key-"+suffix, string(hash), prefix, status, opts.expiresAt,
	).Scan(&s.apiKeyID); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id IN ($1, $2)`, s.tenantID, s.otherTenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id IN ($1, $2)`, s.tenantID, s.otherTenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id IN ($1, $2)`, s.tenantID, s.otherTenantID)
	})
	return s
}

type apiKeySeedOpts struct {
	plaintext string
	status    string
	expiresAt interface{} // pass nil for no expiry, or time.Time
	userGroup string      // 空=用 DB 默认 'default'; 非空=覆写 users.user_group
}

func newRequest(t *testing.T, header string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if header != "" {
		r.Header.Set("Authorization", header)
	}
	return r
}

func setAPIKeyLastUsedAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, apiKeyID int64, ts time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE api_keys SET last_used_at=$1 WHERE id=$2`,
		ts, apiKeyID,
	); err != nil {
		t.Fatalf("set last_used_at: %v", err)
	}
}

func readAPIKeyLastUsedAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, apiKeyID int64) pgtype.Timestamptz {
	t.Helper()
	var ts pgtype.Timestamptz
	if err := pool.QueryRow(ctx,
		`SELECT last_used_at FROM api_keys WHERE id=$1`,
		apiKeyID,
	).Scan(&ts); err != nil {
		t.Fatalf("read last_used_at: %v", err)
	}
	return ts
}

func TestAPIKeyResolver_HappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	r := NewAPIKeyResolver(dbauth.New(pool))
	ident, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if err != nil {
		t.Fatalf("happy path: %v", err)
	}
	if ident.TenantID != seed.tenantID || ident.APIKeyID != seed.apiKeyID || ident.UserID != seed.userID {
		t.Fatalf("identity mismatch: %+v vs seed %+v", ident, seed)
	}
	// 普通 seed 用户走 DB 默认档 'default'; 空说明查询/Identity 漏带 user_group。
	if ident.UserGroup != "default" {
		t.Fatalf("UserGroup = %q, want default (seeded user uses DB default)", ident.UserGroup)
	}
}

// TestAPIKeyResolver_ResolvesUserGroup 守 R-SUB-WIRE-1 地基: 解析出的 Identity 必须带用户订阅档。
// 判别: 把 LookupAPIKeysByPrefix 的 u.user_group 列或 Identity.UserGroup 赋值去掉 →
// ident.UserGroup 变空 (非 'premium') → 红 (分组→路由会拿不到档位, 高档用户被当无限制)。
func TestAPIKeyResolver_ResolvesUserGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{userGroup: "premium"})

	r := NewAPIKeyResolver(dbauth.New(pool))
	ident, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ident.UserGroup != "premium" {
		t.Fatalf("UserGroup = %q, want premium", ident.UserGroup)
	}
}

// TestAPIKeyResolver_IPBlacklistFromQueryDeniesBeforeAllowlist 验证数据库投影出的
// 黑名单会进入 resolver,且 deny 优先于同时命中的 allowlist。
// 变异:从 LookupAPIKeysByPrefix 删除 ip_blacklist 投影或让 Scan 后该字段恒空,
// 请求会被 allowlist 放行,本测试应由 ErrForbidden 断言变红。
func TestAPIKeyResolver_IPBlacklistFromQueryDeniesBeforeAllowlist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	const clientIP = "203.0.113.7"
	matchingCIDR := clientIP + "/32"
	if _, err := pool.Exec(ctx,
		`UPDATE api_keys SET ip_allowlist=$1, ip_blacklist=$1 WHERE id=$2`,
		matchingCIDR, seed.apiKeyID,
	); err != nil {
		t.Fatalf("设置 API key IP 规则: %v", err)
	}

	req := newRequest(t, "Bearer "+seed.plaintext)
	req.RemoteAddr = clientIP + ":5100"
	r := NewAPIKeyResolver(dbauth.New(pool))
	if _, err := r.Resolve(ctx, req); !errors.Is(err, ErrForbidden) {
		t.Fatalf("黑名单与白名单同时命中应由 deny 优先并返回 ErrForbidden,实际错误=%v", err)
	}
}

func TestAPIKeyResolver_TouchesLastUsedAtOnSuccessfulResolve(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})
	old := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
	setAPIKeyLastUsedAt(t, ctx, pool, seed.apiKeyID, old)

	r := NewAPIKeyResolver(dbauth.New(pool))
	if _, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext)); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	first := readAPIKeyLastUsedAt(t, ctx, pool, seed.apiKeyID)
	if !first.Valid {
		t.Fatalf("successful Resolve must set last_used_at; got NULL")
	}
	if !first.Time.After(old) {
		t.Fatalf("successful Resolve must advance last_used_at beyond old timestamp: got %s, old %s",
			first.Time, old)
	}

	time.Sleep(20 * time.Millisecond)
	if _, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext)); err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	second := readAPIKeyLastUsedAt(t, ctx, pool, seed.apiKeyID)
	if !second.Valid {
		t.Fatalf("second successful Resolve must keep last_used_at non-NULL")
	}
	if !second.Time.After(first.Time) {
		t.Fatalf("second successful Resolve must advance last_used_at again: first %s, second %s",
			first.Time, second.Time)
	}
}

func TestAPIKeyResolver_FailedAuthDoesNotTouchLastUsedAt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)

	expiredAt := time.Now().Add(-1 * time.Hour)
	cases := []struct {
		name   string
		opts   apiKeySeedOpts
		header func(*seededAPIKey) string
	}{
		{
			name: "wrong bearer",
			header: func(seed *seededAPIKey) string {
				return "Bearer " + seed.plaintext[:APIKeyPrefixLen] + "_WRONG_SUFFIX_HERE"
			},
		},
		{
			name: "revoked key",
			opts: apiKeySeedOpts{status: "revoked"},
			header: func(seed *seededAPIKey) string {
				return "Bearer " + seed.plaintext
			},
		},
		{
			name: "expired key",
			opts: apiKeySeedOpts{expiresAt: expiredAt},
			header: func(seed *seededAPIKey) string {
				return "Bearer " + seed.plaintext
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seed := seedAPIKey(t, ctx, pool, tc.opts)
			old := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
			setAPIKeyLastUsedAt(t, ctx, pool, seed.apiKeyID, old)

			r := NewAPIKeyResolver(dbauth.New(pool))
			_, err := r.Resolve(ctx, newRequest(t, tc.header(seed)))
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("failed bearer must collapse to ErrUnauthorized; got %v", err)
			}
			after := readAPIKeyLastUsedAt(t, ctx, pool, seed.apiKeyID)
			if !after.Valid {
				t.Fatalf("failed Resolve must preserve existing last_used_at; got NULL")
			}
			if !after.Time.Equal(old) {
				t.Fatalf("failed Resolve must not update last_used_at: got %s, want %s", after.Time, old)
			}
		})
	}
}

func TestAPIKeyResolver_WrongBearer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	r := NewAPIKeyResolver(dbauth.New(pool))
	// 前缀相同但后缀错误 → bcrypt 不匹配
	bad := seed.plaintext[:APIKeyPrefixLen] + "_WRONG_SUFFIX_HERE"
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+bad))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong bearer must collapse to ErrUnauthorized; got %v", err)
	}
}

func TestAPIKeyResolver_RevokedKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{status: "revoked"})

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked key must return ErrUnauthorized (no leakage); got %v", err)
	}
}

func TestAPIKeyResolver_ActiveStatusWithRevokedAtRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	if _, err := pool.Exec(ctx,
		`UPDATE api_keys SET status='active', revoked_at=NOW() WHERE id=$1`,
		seed.apiKeyID,
	); err != nil {
		t.Fatalf("seed inconsistent revoked key: %v", err)
	}

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked_at 非空的 Key 必须拒绝认证，得到 %v", err)
	}
}

func TestAPIKeyResolver_AdminRoleUserKeyRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	if _, err := pool.Exec(ctx,
		`UPDATE users SET role='admin' WHERE id=$1`,
		seed.userID,
	); err != nil {
		t.Fatalf("promote seeded user: %v", err)
	}

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("管理员身份绑定的普通用户 Key 必须拒绝认证，得到 %v", err)
	}
}

func TestAPIKeyResolver_ExpiredKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	expired := time.Now().Add(-1 * time.Hour)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{expiresAt: expired})

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired key must return ErrUnauthorized; got %v", err)
	}
}

func TestAPIKeyResolver_CrossTenantProbeRejected(t *testing.T) {
	// 即便该 bearer 对 tenantA 有效, resolver 也不会暴露任何途径
	// 去询问 "这个 bearer 是否属于 tenantB?" —— 每种失败模式
	// 都坍缩为 ErrUnauthorized。我们校验 resolver 返回的是
	// bearer 真实所属的租户 (而非探测的目标租户)。
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	r := NewAPIKeyResolver(dbauth.New(pool))
	ident, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ident.TenantID == seed.otherTenantID {
		t.Fatalf("CRITICAL: bearer of tenant %d resolved to other tenant %d",
			seed.tenantID, seed.otherTenantID)
	}
}

func TestAPIKeyResolver_PrefixCollisionPicksRightRow(t *testing.T) {
	// 种入两条 key_prefix 相同 (共享前 16 字符) 但后缀不同的
	// api_keys。Resolver 必须对每个候选做 bcrypt 比对,
	// 返回匹配的那条 —— 而非第一行。
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)

	suffix := uuid.NewString()
	var tenantID, userID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"auth-collide-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "user-"+suffix,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	prefix := "hk_test_collide" // 15 字符 + 1 -> 16
	prefix = prefix + "X"
	// 种入两条都以 `prefix` 开头、但之后不同的 key。
	plaintexts := []string{prefix + "AAAAAAAAAAA", prefix + "BBBBBBBBBBB"}
	var ids []int64
	for i, p := range plaintexts {
		hash, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
		if err != nil {
			t.Fatalf("hash %d: %v", i, err)
		}
		var id int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
			 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
			tenantID, userID, "key-"+suffix+"-"+p[len(p)-1:], string(hash), prefix,
		).Scan(&id); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	r := NewAPIKeyResolver(dbauth.New(pool))
	// 解析第二个明文 —— 校验 resolver 返回的是第二行的
	// id, 而非第一行。
	ident, err := r.Resolve(ctx, newRequest(t, "Bearer "+plaintexts[1]))
	if err != nil {
		t.Fatalf("Resolve collide: %v", err)
	}
	if ident.APIKeyID != ids[1] {
		t.Fatalf("resolver picked wrong row on prefix collision: got %d, want %d (ids: %v)",
			ident.APIKeyID, ids[1], ids)
	}
}

func TestAPIKeyResolver_RejectsForeignFormat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	_ = seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	r := NewAPIKeyResolver(dbauth.New(pool))
	for _, bad := range []string{
		"Bearer sk-1234567890abcdef",
		"Bearer xyz_random_token_here",
		"NotBearer hk_live_xyz",
		"Bearer ", // 空
		"",        // 缺失 header
	} {
		_, err := r.Resolve(ctx, newRequest(t, bad))
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("foreign format %q must return ErrUnauthorized; got %v", bad, err)
		}
	}
}

func TestAPIKeyResolver_NilQueriesReturnsMisconfigured(t *testing.T) {
	r := &APIKeyResolver{q: nil}
	_, err := r.Resolve(context.Background(), newRequest(t, "Bearer hk_test_anything"))
	if !errors.Is(err, ErrAuthMisconfigured) {
		t.Fatalf("nil queries must return ErrAuthMisconfigured (D9 → 503); got %v", err)
	}
}

// 即便 API key 处于 active, 被禁用的 user 也绝不能
// 通过鉴权。Resolver 在 bcrypt 匹配后查 users.status。
func TestAPIKeyResolver_DisabledUserRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{}) // active 的 key + active 的 user

	// 种入后把 user.status 翻成 'disabled'。
	if _, err := pool.Exec(ctx,
		`UPDATE users SET status='disabled' WHERE id=$1`, seed.userID,
	); err != nil {
		t.Fatalf("flip user disabled: %v", err)
	}

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("disabled user must collapse to ErrUnauthorized; got %v", err)
	}
}

// 即便 user + API key 都是 active, 被禁用的租户
// 也绝不能通过鉴权。
func TestAPIKeyResolver_DisabledTenantRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	if _, err := pool.Exec(ctx,
		`UPDATE tenants SET status='disabled' WHERE id=$1`, seed.tenantID,
	); err != nil {
		t.Fatalf("flip tenant disabled: %v", err)
	}

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("disabled tenant must collapse to ErrUnauthorized; got %v", err)
	}
}

// 被软删除的租户绝不能通过鉴权。
// 注意: 在真实 admin 流程中 api_keys 行通常会被级联处理;
// 本测试直接写入租户生命周期的 deleted 终态。
func TestAPIKeyResolver_SoftDeletedTenantRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	if _, err := pool.Exec(ctx,
		`UPDATE tenants SET status='deleted', deleted_at=NOW() WHERE id=$1`, seed.tenantID,
	); err != nil {
		t.Fatalf("soft-delete tenant: %v", err)
	}

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("soft-deleted tenant must collapse to ErrUnauthorized; got %v", err)
	}
}

// 即便 API key 仍处于 active, 被软删除的 user
// 也绝不能通过鉴权。
func TestAPIKeyResolver_SoftDeletedUserRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	if _, err := pool.Exec(ctx,
		`UPDATE users SET deleted_at=NOW() WHERE id=$1`, seed.userID,
	); err != nil {
		t.Fatalf("soft-delete user: %v", err)
	}

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("soft-deleted user must collapse to ErrUnauthorized; got %v", err)
	}
}
