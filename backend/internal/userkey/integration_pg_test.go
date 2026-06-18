//go:build integration_pg

// userkey 服务真 PG 验证:
//   T1 Issue 返明文一次 + List/Get 永不再回明文
//   T2 List 只列 caller 自己的 key (user A 不见 user B 的)
//   T3 Get 别人的 key → ErrNotFound (与"不存在"同响应,防 ID 枚举)
//   T4 Revoke 别人的 key → ErrNotFound + 目标行不变 (其他 user 的 key 不动)
//   T5 Active key cap (MaxActiveKeysPerUser) 命中 → ErrActiveKeyCapHit
//   T6 Revoke 已 revoked → AlreadyRevoked=true (幂等)
//   T7 plaintext 签发后跟 inbound auth.APIKeyResolver 端到端能解析
//
// 判别 fixture 关键:每个测试都种 user A + user B 各 1 个 key,然后从 A 的
// session 调 service 试图操作 B 的 key — 反向期望必须出错。把 service 里
// WHERE user_id = $? 删掉 → 测试集体 red,符合 mutation 自检。

package userkey

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

func openPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

type fixture struct {
	t        *testing.T
	pool     *pgxpool.Pool
	tenantID int64
	userA    int64
	userB    int64
	suffix   string
}

func newFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *fixture {
	t.Helper()
	f := &fixture{t: t, pool: pool, suffix: uuid.NewString()}
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"userkey-tenant-"+f.suffix,
	).Scan(&f.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		f.tenantID, "user-a-"+f.suffix,
	).Scan(&f.userA); err != nil {
		t.Fatalf("seed user A: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		f.tenantID, "user-b-"+f.suffix,
	).Scan(&f.userB); err != nil {
		t.Fatalf("seed user B: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id = $1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id = $1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, f.tenantID)
	})
	return f
}

func newServiceFast(pool *pgxpool.Pool) *Service {
	s := NewService(pool, nil)
	s.bcryptCost = bcrypt.MinCost // 测试加速 (production 用 DefaultCost)
	return s
}

// T1: Issue 返明文一次 + List/Get 不再回明文。
func TestUserKey_Issue_PlaintextOnceOnly(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	f := newFixture(t, ctx, pool)
	svc := newServiceFast(pool)

	res, err := svc.Issue(ctx, IssueRequest{
		TenantID: f.tenantID, UserID: f.userA,
		Name: "t1-key", Environment: EnvLive,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if res.Plaintext == "" {
		t.Fatalf("Issue must return non-empty plaintext")
	}
	if res.KeyPrefix == "" || res.APIKeyID == 0 {
		t.Fatalf("Issue must return prefix + id; got %+v", res)
	}
	plaintext := res.Plaintext

	// 后续 List / Get 路径不应承载 plaintext (KeyDescriptor 结构本身就没字段
	// — 但要锁死字段命名不被未来加 Plaintext 字段)
	list, err := svc.List(ctx, ListRequest{TenantID: f.tenantID, UserID: f.userA})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List want 1 row; got %d", len(list))
	}
	d := list[0]
	if d.APIKeyID != res.APIKeyID {
		t.Fatalf("List id drift: want %d got %d", res.APIKeyID, d.APIKeyID)
	}
	if d.KeyPrefix != res.KeyPrefix {
		t.Fatalf("List prefix drift: want %s got %s", res.KeyPrefix, d.KeyPrefix)
	}
	if d.Status != "active" {
		t.Fatalf("List status: want active got %s", d.Status)
	}
	// DB 行不应该把 plaintext 持久化:直接抓 key_hash + 拿 plaintext bcrypt-compare 对得上,
	// 但 plaintext 本身 grep 不到 (bcrypt hash 不含原文)
	var keyHash string
	if err := pool.QueryRow(ctx, `SELECT key_hash FROM api_keys WHERE id=$1`, res.APIKeyID).Scan(&keyHash); err != nil {
		t.Fatalf("read key_hash: %v", err)
	}
	if keyHash == plaintext {
		t.Fatalf("key_hash MUST be bcrypt hash, not plaintext")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(keyHash), []byte(plaintext)); err != nil {
		t.Fatalf("bcrypt mismatch: %v", err)
	}

	// Get + JSON 序列化必不含 plaintext (
	// "KeyDescriptor 结构没字段" 隐式断言,要真正序列化 + grep)
	gotGet, err := svc.Get(ctx, f.tenantID, f.userA, res.APIKeyID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	getBytes, err := json.Marshal(gotGet)
	if err != nil {
		t.Fatalf("Marshal Get: %v", err)
	}
	if strings.Contains(string(getBytes), plaintext) {
		t.Fatalf("Get descriptor JSON MUST NOT contain plaintext substring; got %s", string(getBytes))
	}
	listBytes, err := json.Marshal(list[0])
	if err != nil {
		t.Fatalf("Marshal list[0]: %v", err)
	}
	if strings.Contains(string(listBytes), plaintext) {
		t.Fatalf("List descriptor JSON MUST NOT contain plaintext substring; got %s", string(listBytes))
	}
}

// T2: List owner 隔离 — user A 不见 user B 的 key。
//
// Mutation 自检:把 service.List 的 user_id = $2 改成 user_id > 0 (或删) → user A
// 会看到 user B 的 key → 本测试 red。
func TestUserKey_List_OwnerIsolation(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	f := newFixture(t, ctx, pool)
	svc := newServiceFast(pool)

	if _, err := svc.Issue(ctx, IssueRequest{TenantID: f.tenantID, UserID: f.userA, Name: "a-key"}); err != nil {
		t.Fatalf("Issue A: %v", err)
	}
	if _, err := svc.Issue(ctx, IssueRequest{TenantID: f.tenantID, UserID: f.userB, Name: "b-key-1"}); err != nil {
		t.Fatalf("Issue B1: %v", err)
	}
	if _, err := svc.Issue(ctx, IssueRequest{TenantID: f.tenantID, UserID: f.userB, Name: "b-key-2"}); err != nil {
		t.Fatalf("Issue B2: %v", err)
	}

	listA, err := svc.List(ctx, ListRequest{TenantID: f.tenantID, UserID: f.userA})
	if err != nil {
		t.Fatalf("List A: %v", err)
	}
	if len(listA) != 1 {
		t.Fatalf("user A should see exactly 1 own key; got %d (B's keys leaked if > 1)", len(listA))
	}
	if listA[0].Name != "a-key" {
		t.Fatalf("user A list name drift: want a-key got %s", listA[0].Name)
	}

	listB, err := svc.List(ctx, ListRequest{TenantID: f.tenantID, UserID: f.userB})
	if err != nil {
		t.Fatalf("List B: %v", err)
	}
	if len(listB) != 2 {
		t.Fatalf("user B should see exactly 2 own keys; got %d", len(listB))
	}
}

// T3: Get 别人的 key → ErrNotFound (不是 ErrAlreadyRevoked / not ok)。
//
// Mutation 自检:把 Get WHERE user_id = $3 删掉 → user A 能拿到 user B 的 key descriptor → red。
func TestUserKey_Get_RejectsForeignOwner(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	f := newFixture(t, ctx, pool)
	svc := newServiceFast(pool)

	resB, err := svc.Issue(ctx, IssueRequest{TenantID: f.tenantID, UserID: f.userB, Name: "b-key"})
	if err != nil {
		t.Fatalf("Issue B: %v", err)
	}

	// A 试图 Get B 的 key by id
	_, err = svc.Get(ctx, f.tenantID, f.userA, resB.APIKeyID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("user A getting user B key: want ErrNotFound; got %v", err)
	}

	// B 自己拿自己的 key 应该 ok (正向保持)
	dB, err := svc.Get(ctx, f.tenantID, f.userB, resB.APIKeyID)
	if err != nil {
		t.Fatalf("Get B own: %v", err)
	}
	if dB.APIKeyID != resB.APIKeyID {
		t.Fatalf("Get B own id drift")
	}
}

// T4: Revoke 别人的 key → ErrNotFound + 目标行**不变**。
//
// 这是真正的 owner-isolation 安全洞测试。Mutation 自检:把 Revoke UPDATE
// WHERE user_id = $4 删掉 → user A 能成功撤掉 user B 的 key → 本测试 red。
func TestUserKey_Revoke_RejectsForeignOwner(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	f := newFixture(t, ctx, pool)
	svc := newServiceFast(pool)

	resB, err := svc.Issue(ctx, IssueRequest{TenantID: f.tenantID, UserID: f.userB, Name: "b-key"})
	if err != nil {
		t.Fatalf("Issue B: %v", err)
	}

	// A 试图 Revoke B 的 key
	_, err = svc.Revoke(ctx, RevokeRequest{
		TenantID: f.tenantID, UserID: f.userA, APIKeyID: resB.APIKeyID, Reason: "malicious",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("user A revoking user B key: want ErrNotFound; got %v", err)
	}

	// 验:B 的 key 状态在 DB 里**没变**
	var statusAfter string
	if err := pool.QueryRow(ctx, `SELECT status FROM api_keys WHERE id=$1`, resB.APIKeyID).Scan(&statusAfter); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if statusAfter != "active" {
		t.Fatalf("B's key MUST stay active after A's foreign-revoke attempt; got %s", statusAfter)
	}

	// B 自己撤自己的应该 ok (正向保持)
	out, err := svc.Revoke(ctx, RevokeRequest{
		TenantID: f.tenantID, UserID: f.userB, APIKeyID: resB.APIKeyID, Reason: "rotation",
	})
	if err != nil {
		t.Fatalf("B revoke own: %v", err)
	}
	if out.AlreadyRevoked {
		t.Fatalf("first-time revoke must not be AlreadyRevoked")
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM api_keys WHERE id=$1`, resB.APIKeyID).Scan(&statusAfter); err != nil {
		t.Fatalf("read status post-revoke: %v", err)
	}
	if statusAfter != "revoked" {
		t.Fatalf("B's own revoke must flip to revoked; got %s", statusAfter)
	}
}

// T5: Active key cap (MaxActiveKeysPerUser) 命中 → ErrActiveKeyCapHit。
//
// 跑 MaxActiveKeysPerUser+1 次,第 N+1 次必 cap。Mutation 自检:删 cap check
// → 第 N+1 次不会 cap → red。
func TestUserKey_ActiveKeyCap(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	f := newFixture(t, ctx, pool)
	svc := newServiceFast(pool)

	for i := 0; i < MaxActiveKeysPerUser; i++ {
		if _, err := svc.Issue(ctx, IssueRequest{
			TenantID: f.tenantID, UserID: f.userA,
			Name: "fill", Environment: EnvLive,
		}); err != nil {
			t.Fatalf("fill iter %d: %v", i, err)
		}
	}
	_, err := svc.Issue(ctx, IssueRequest{
		TenantID: f.tenantID, UserID: f.userA, Name: "overflow",
	})
	if !errors.Is(err, ErrActiveKeyCapHit) {
		t.Fatalf("issuing past cap: want ErrActiveKeyCapHit; got %v", err)
	}

	// 撤掉一个后又能签发 (cap 数 active,不数 revoked) — 正向保持
	listA, _ := svc.List(ctx, ListRequest{TenantID: f.tenantID, UserID: f.userA, Limit: MaxActiveKeysPerUser})
	if _, err := svc.Revoke(ctx, RevokeRequest{
		TenantID: f.tenantID, UserID: f.userA, APIKeyID: listA[0].APIKeyID, Reason: "rotate",
	}); err != nil {
		t.Fatalf("revoke for slot: %v", err)
	}
	if _, err := svc.Issue(ctx, IssueRequest{
		TenantID: f.tenantID, UserID: f.userA, Name: "post-rotate",
	}); err != nil {
		t.Fatalf("issue after rotate: %v", err)
	}
}

// T6: Revoke idempotent — 已 revoked 再 Revoke → AlreadyRevoked=true,无 error。
func TestUserKey_Revoke_Idempotent(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	f := newFixture(t, ctx, pool)
	svc := newServiceFast(pool)

	res, err := svc.Issue(ctx, IssueRequest{TenantID: f.tenantID, UserID: f.userA, Name: "t6"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := svc.Revoke(ctx, RevokeRequest{
		TenantID: f.tenantID, UserID: f.userA, APIKeyID: res.APIKeyID,
	}); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	out, err := svc.Revoke(ctx, RevokeRequest{
		TenantID: f.tenantID, UserID: f.userA, APIKeyID: res.APIKeyID,
	})
	if err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	if !out.AlreadyRevoked {
		t.Fatalf("second revoke must be AlreadyRevoked=true")
	}
}

// T7: 签发的 plaintext 直接喂 inbound auth.APIKeyResolver → 能解析到正确 (tenant, user)。
//
// 端到端打通 user-self-service 签发 + inbound 校验路径,防 prefix/hash 算法漂移。
func TestUserKey_Issue_EndToEndInboundAuth(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	f := newFixture(t, ctx, pool)
	svc := newServiceFast(pool)

	res, err := svc.Issue(ctx, IssueRequest{TenantID: f.tenantID, UserID: f.userA, Name: "e2e"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	resolver := auth.NewAPIKeyResolver(dbauth.New(pool))
	req := newAuthHTTPReq(t, res.Plaintext)
	ident, err := resolver.Resolve(ctx, req)
	if err != nil {
		t.Fatalf("Resolve plaintext via inbound resolver: %v", err)
	}
	if ident.TenantID != f.tenantID || ident.UserID != f.userA || ident.APIKeyID != res.APIKeyID {
		t.Fatalf("Resolve identity drift: want (%d, %d, %d) got %+v",
			f.tenantID, f.userA, res.APIKeyID, ident)
	}
}

// T8: Revoke 后 plaintext 用 inbound auth 解析 → ErrUnauthorized;status 真切换。
func TestUserKey_Revoke_BlocksInboundAuth(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	f := newFixture(t, ctx, pool)
	svc := newServiceFast(pool)

	res, err := svc.Issue(ctx, IssueRequest{TenantID: f.tenantID, UserID: f.userA, Name: "t8"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	resolver := auth.NewAPIKeyResolver(dbauth.New(pool))
	if _, err := resolver.Resolve(ctx, newAuthHTTPReq(t, res.Plaintext)); err != nil {
		t.Fatalf("Resolve pre-revoke: %v", err)
	}
	if _, err := svc.Revoke(ctx, RevokeRequest{
		TenantID: f.tenantID, UserID: f.userA, APIKeyID: res.APIKeyID, Reason: "test",
	}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := resolver.Resolve(ctx, newAuthHTTPReq(t, res.Plaintext)); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("Resolve post-revoke: want ErrUnauthorized; got %v", err)
	}
}

// T9: Issue 拒过期时刻;tenant inactive 时拒签 (实测 update tenant.status='disabled')。
func TestUserKey_Issue_RejectsBadInputs(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	f := newFixture(t, ctx, pool)
	svc := newServiceFast(pool)

	past := time.Now().Add(-time.Hour)
	if _, err := svc.Issue(ctx, IssueRequest{
		TenantID: f.tenantID, UserID: f.userA, Name: "past", ExpiresAt: &past,
	}); !errors.Is(err, ErrInvalidExpiry) {
		t.Fatalf("past expires: want ErrInvalidExpiry; got %v", err)
	}
	if _, err := svc.Issue(ctx, IssueRequest{
		TenantID: f.tenantID, UserID: f.userA, Name: "",
	}); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("empty name: want ErrInvalidName; got %v", err)
	}
	if _, err := svc.Issue(ctx, IssueRequest{
		TenantID: f.tenantID, UserID: f.userA, Name: "x", Environment: "admin",
	}); !errors.Is(err, ErrInvalidEnv) {
		t.Fatalf("admin env: want ErrInvalidEnv; got %v", err)
	}

	// disable tenant → 应该拿不到 row
	if _, err := pool.Exec(ctx, `UPDATE tenants SET status='disabled' WHERE id=$1`, f.tenantID); err != nil {
		t.Fatalf("disable tenant: %v", err)
	}
	if _, err := svc.Issue(ctx, IssueRequest{
		TenantID: f.tenantID, UserID: f.userA, Name: "post-disable",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled tenant: want ErrNotFound; got %v", err)
	}
}

// T9b: Patch expiry 三态 (set / clear / unchanged) + reject-past delta, 经 Get readback 判别。
// set 未来 → Get 反映; clear ("") → Get 为 nil; past → ErrInvalidExpiry 且有效期不变;
// no-op → 保留当前有效期 (不静默清除)。
// mutation 自检: 删 Patch 的 expires_at 子句 → set 后 Get 仍 nil → red;
//   把 reject-past 校验删掉 → past Patch 成功把 key 设成过期 → ErrInvalidExpiry 断言 red;
//   no-op 误走 UPDATE 清掉 expires_at → 末尾 Get 为 nil → red。
func TestUserKey_PatchExpiry_TriStateAndRejectPast(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	f := newFixture(t, ctx, pool)
	svc := newServiceFast(pool)

	res, err := svc.Issue(ctx, IssueRequest{TenantID: f.tenantID, UserID: f.userA, Name: "k"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	id := res.APIKeyID

	// SET future → PatchResult + Get both reflect it.
	future := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	pr, err := svc.Patch(ctx, PatchRequest{TenantID: f.tenantID, UserID: f.userA, APIKeyID: id, ExpiresAt: &future})
	if err != nil {
		t.Fatalf("Patch set: %v", err)
	}
	if pr.ExpiresAt == nil || !pr.ExpiresAt.Equal(future) {
		t.Fatalf("set: PatchResult.ExpiresAt=%v want %v", pr.ExpiresAt, future)
	}
	if got, err := svc.Get(ctx, f.tenantID, f.userA, id); err != nil {
		t.Fatalf("Get after set: %v", err)
	} else if got.ExpiresAt == nil || !got.ExpiresAt.Equal(future) {
		t.Fatalf("set: Get.ExpiresAt=%v want %v", got.ExpiresAt, future)
	}

	// CLEAR (ClearExpiry) → PatchResult + Get show nil.
	pr, err = svc.Patch(ctx, PatchRequest{TenantID: f.tenantID, UserID: f.userA, APIKeyID: id, ClearExpiry: true})
	if err != nil {
		t.Fatalf("Patch clear: %v", err)
	}
	if pr.ExpiresAt != nil {
		t.Fatalf("clear: PatchResult.ExpiresAt=%v want nil", pr.ExpiresAt)
	}
	if got, err := svc.Get(ctx, f.tenantID, f.userA, id); err != nil {
		t.Fatalf("Get after clear: %v", err)
	} else if got.ExpiresAt != nil {
		t.Fatalf("clear: Get.ExpiresAt=%v want nil", got.ExpiresAt)
	}

	// Re-set a future deadline so the past/no-op cases have a value to protect.
	if _, err := svc.Patch(ctx, PatchRequest{TenantID: f.tenantID, UserID: f.userA, APIKeyID: id, ExpiresAt: &future}); err != nil {
		t.Fatalf("Patch re-set: %v", err)
	}

	// PAST on set → ErrInvalidExpiry, and the deadline is NOT changed (still future).
	past := time.Now().Add(-time.Hour)
	if _, err := svc.Patch(ctx, PatchRequest{TenantID: f.tenantID, UserID: f.userA, APIKeyID: id, ExpiresAt: &past}); !errors.Is(err, ErrInvalidExpiry) {
		t.Fatalf("past: want ErrInvalidExpiry; got %v", err)
	}
	if got, err := svc.Get(ctx, f.tenantID, f.userA, id); err != nil {
		t.Fatalf("Get after rejected past: %v", err)
	} else if got.ExpiresAt == nil || !got.ExpiresAt.Equal(future) {
		t.Fatalf("past rejected but deadline changed: Get.ExpiresAt=%v want %v", got.ExpiresAt, future)
	}

	// NO-OP (all nil) → returns current state and preserves the deadline.
	pr, err = svc.Patch(ctx, PatchRequest{TenantID: f.tenantID, UserID: f.userA, APIKeyID: id})
	if err != nil {
		t.Fatalf("Patch no-op: %v", err)
	}
	if pr.ExpiresAt == nil || !pr.ExpiresAt.Equal(future) {
		t.Fatalf("no-op: PatchResult.ExpiresAt=%v want %v (preserved)", pr.ExpiresAt, future)
	}
	if got, err := svc.Get(ctx, f.tenantID, f.userA, id); err != nil {
		t.Fatalf("Get after no-op: %v", err)
	} else if got.ExpiresAt == nil || !got.ExpiresAt.Equal(future) {
		t.Fatalf("no-op changed the deadline: Get.ExpiresAt=%v want %v", got.ExpiresAt, future)
	}

	// COMBINED name + expires_at in ONE patch. Single-field cases above only ever exercise
	// the expires_at placeholder at argIdx==1; here name = $1 pushes expires_at to $2 with
	// WHERE at $3/$4/$5 — locking the dynamic-UPDATE argIdx arithmetic that a mis-index
	// would silently break (a mis-placed deadline bricks/un-bricks a key at auth time).
	rename := "renamed"
	future2 := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
	pr, err = svc.Patch(ctx, PatchRequest{TenantID: f.tenantID, UserID: f.userA, APIKeyID: id, Name: &rename, ExpiresAt: &future2})
	if err != nil {
		t.Fatalf("Patch name+expires: %v", err)
	}
	if pr.Name != "renamed" || pr.ExpiresAt == nil || !pr.ExpiresAt.Equal(future2) {
		t.Fatalf("name+expires: PatchResult name=%q expires=%v want renamed/%v", pr.Name, pr.ExpiresAt, future2)
	}
	if got, err := svc.Get(ctx, f.tenantID, f.userA, id); err != nil {
		t.Fatalf("Get after name+expires: %v", err)
	} else if got.Name != "renamed" || got.ExpiresAt == nil || !got.ExpiresAt.Equal(future2) {
		t.Fatalf("name+expires: Get name=%q expires=%v want renamed/%v", got.Name, got.ExpiresAt, future2)
	}

	// COMBINED name + status + expires_at. The status clause emits an extra revoked_at CASE
	// arg, pushing expires_at to the highest index ($4, WHERE $5/$6/$7) — exercises the full
	// argIdx path. status="active" keeps the key usable (revoked_at CASE -> ELSE no change).
	future3 := time.Now().Add(96 * time.Hour).UTC().Truncate(time.Second)
	status := "active"
	pr, err = svc.Patch(ctx, PatchRequest{TenantID: f.tenantID, UserID: f.userA, APIKeyID: id, Name: &rename, Status: &status, ExpiresAt: &future3})
	if err != nil {
		t.Fatalf("Patch name+status+expires: %v", err)
	}
	if pr.ExpiresAt == nil || !pr.ExpiresAt.Equal(future3) {
		t.Fatalf("name+status+expires: PatchResult.ExpiresAt=%v want %v", pr.ExpiresAt, future3)
	}
	if got, err := svc.Get(ctx, f.tenantID, f.userA, id); err != nil {
		t.Fatalf("Get after name+status+expires: %v", err)
	} else if got.ExpiresAt == nil || !got.ExpiresAt.Equal(future3) || got.Status != "active" {
		t.Fatalf("name+status+expires: Get expires=%v status=%q want %v/active", got.ExpiresAt, got.Status, future3)
	}
}

// T10: cap 竞态防御 — 并发 N+5 个 Issue,**永远不**能让 active 数超过 MaxActiveKeysPerUser。
//
// advisory lock 把 cap 检查序列化;只 t.Parallel + serial issuance
// 测不到这个 race;必须真起并发 goroutine。Mutation 自检:删 pg_advisory_xact_lock 行 →
// 多 worker 同时读到 active < cap → 同时 INSERT → 最终 DB 里 active > cap → 测试 red。
func TestUserKey_ActiveKeyCap_RaceLockedDown(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	f := newFixture(t, ctx, pool)
	svc := newServiceFast(pool)

	const goroutines = MaxActiveKeysPerUser + 5
	var wg sync.WaitGroup
	wg.Add(goroutines)
	successes := make(chan int64, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			res, err := svc.Issue(ctx, IssueRequest{
				TenantID: f.tenantID, UserID: f.userA,
				Name: "race-fill", Environment: EnvLive,
			})
			if err == nil {
				successes <- res.APIKeyID
			} else if !errors.Is(err, ErrActiveKeyCapHit) {
				t.Errorf("worker %d unexpected err: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	close(successes)
	count := 0
	for range successes {
		count++
	}
	if count > MaxActiveKeysPerUser {
		t.Fatalf("race lock failed: %d successes exceeds cap %d", count, MaxActiveKeysPerUser)
	}
	// 真 DB 读:active 数也必须 ≤ cap
	var dbActive int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE tenant_id=$1 AND user_id=$2 AND status='active' AND deleted_at IS NULL`,
		f.tenantID, f.userA).Scan(&dbActive); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if dbActive > MaxActiveKeysPerUser {
		t.Fatalf("race lock failed: DB has %d active keys, cap %d", dbActive, MaxActiveKeysPerUser)
	}
}

// T11: 失活 tenant — List/Get/Revoke 必须 fail-closed (返空 / ErrNotFound)。
//
// stale session (tenant 已 disabled 但 token 还没过期)
// 不应该让用户管 key。Mutation 自检:删 List/Get/Revoke 的 JOIN tenants/users → 失活
// tenant 仍能 List 出 key / Get 拿到行 / Revoke 成功 → 测试 red。
func TestUserKey_StaleParent_FailClosed(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	f := newFixture(t, ctx, pool)
	svc := newServiceFast(pool)

	res, err := svc.Issue(ctx, IssueRequest{TenantID: f.tenantID, UserID: f.userA, Name: "stale-tenant"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// 正向保持:active 时能查到
	if list, err := svc.List(ctx, ListRequest{TenantID: f.tenantID, UserID: f.userA}); err != nil || len(list) != 1 {
		t.Fatalf("active baseline list: err=%v len=%d", err, len(list))
	}

	// disable tenant → fail-closed
	if _, err := pool.Exec(ctx, `UPDATE tenants SET status='disabled' WHERE id=$1`, f.tenantID); err != nil {
		t.Fatalf("disable tenant: %v", err)
	}
	if list, err := svc.List(ctx, ListRequest{TenantID: f.tenantID, UserID: f.userA}); err != nil || len(list) != 0 {
		t.Fatalf("disabled-tenant List MUST return empty; err=%v len=%d", err, len(list))
	}
	if _, err := svc.Get(ctx, f.tenantID, f.userA, res.APIKeyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled-tenant Get: want ErrNotFound; got %v", err)
	}
	if _, err := svc.Revoke(ctx, RevokeRequest{
		TenantID: f.tenantID, UserID: f.userA, APIKeyID: res.APIKeyID,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled-tenant Revoke: want ErrNotFound; got %v", err)
	}

	// 还原 tenant → 再 disable user → 仍 fail-closed
	if _, err := pool.Exec(ctx, `UPDATE tenants SET status='active' WHERE id=$1`, f.tenantID); err != nil {
		t.Fatalf("re-enable tenant: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='disabled' WHERE id=$1`, f.userA); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if list, err := svc.List(ctx, ListRequest{TenantID: f.tenantID, UserID: f.userA}); err != nil || len(list) != 0 {
		t.Fatalf("disabled-user List MUST return empty; err=%v len=%d", err, len(list))
	}
	if _, err := svc.Get(ctx, f.tenantID, f.userA, res.APIKeyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled-user Get: want ErrNotFound; got %v", err)
	}
}

// newAuthHTTPReq 构造一个 Authorization: Bearer <plaintext> 的 *http.Request 供 resolver。
func newAuthHTTPReq(t *testing.T, plaintext string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+plaintext)
	return r
}
