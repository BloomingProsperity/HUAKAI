//go:build integration_pg

package userauth

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

// TestPGTelegramBindThenLoginEndToEnd 在真实 Postgres 上端到端验证「先绑定后登录」(Owner 选的 B 方案)
// 的完整逻辑链路,全部走真 PostgresStore + Service,而非内存桩:
//
//	① 已登录用户用 LinkVerifiedSocialIdentity 绑定 telegram 身份(EmailVerified 恒 false)→ 真写 social_identity_links;
//	② 同一 telegram 身份再走 ApplyVerifiedSocialIdentity(登录)→ 凭既有绑定直接登录到本人(reorder 的价值);
//	③ 接管保护:第二个用户绑同一 subject → ErrSocialIdentityAlreadyBound,且原绑定不被改动;
//	④ 未绑定的 telegram 身份登录 → 仍被邮箱门拦成 ErrOAuthPendingEmailRequired,不凭空建号。
//
// MUTATION 对照:把 applyVerifiedSocialIdentity 的既有绑定查询挪回邮箱门之后(旧顺序)→ 步骤②对已绑定的
// telegram(EmailVerified:false)会先撞邮箱门返回 pending,本测试②变红——正是「先绑定后登录」要修的死路。
func TestPGTelegramBindThenLoginEndToEnd(t *testing.T) {
	ctx := context.Background()
	pool := openUserAuthProfilePool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	svc := NewService(store)
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedUserAuthProfileTenant(t, ctx, pool, "tg-bind-login-"+suffix)
	t.Cleanup(func() { cleanupUserAuthProfileTenant(t, ctx, pool, tenantID) })

	alice, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "tg-alice-" + suffix + "@example.test", DisplayName: "Alice",
		PasswordHash: "argon2id-test-hash", EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser alice: %v", err)
	}
	bob, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "tg-bob-" + suffix + "@example.test", DisplayName: "Bob",
		PasswordHash: "argon2id-test-hash", EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}

	subject := "tg-" + suffix
	tgIdentity := VerifiedIdentity{
		Provider:      SocialProviderTelegram,
		Subject:       subject,
		Email:         SyntheticOAuthEmail(SocialProviderTelegram, subject),
		EmailVerified: false, // telegram widget 永远不证明邮箱所有权
	}

	// ① alice 绑定 telegram(真写 DB)。
	if _, err := svc.LinkVerifiedSocialIdentity(ctx, tenantID, alice.ID, tgIdentity); err != nil {
		t.Fatalf("① alice 绑定 telegram 应成功: %v", err)
	}

	// ② 凭既有绑定登录:同一 telegram 身份走登录路径,应直接登录到 alice(而非 pending-email 死路)。
	loggedIn, err := svc.ApplyVerifiedSocialIdentity(ctx, tenantID, tgIdentity)
	if err != nil {
		t.Fatalf("② 已绑定 telegram 登录应成功,得 err=%v", err)
	}
	if loggedIn.ID != alice.ID {
		t.Fatalf("② 登录返回 user=%d,应为已绑定的 alice=%d", loggedIn.ID, alice.ID)
	}

	// ③ 接管保护:bob 试图绑 alice 已占用的同一 subject → 拒,且原绑定仍归 alice。
	if _, err := svc.LinkVerifiedSocialIdentity(ctx, tenantID, bob.ID, tgIdentity); !errors.Is(err, ErrSocialIdentityAlreadyBound) {
		t.Fatalf("③ bob 抢绑他人已占身份 err=%v,应 ErrSocialIdentityAlreadyBound", err)
	}
	owner, err := store.GetUserBySocialIdentity(ctx, tenantID, SocialProviderTelegram, subject)
	if err != nil {
		t.Fatalf("③ 抢绑后查身份归属: %v", err)
	}
	if owner.ID != alice.ID {
		t.Fatalf("③ 接管被拒后身份应仍归 alice=%d,实归 %d", alice.ID, owner.ID)
	}

	// ④ 未绑定的 telegram 身份(不同 subject)登录 → 仍 pending,不凭空建号。
	unbound := VerifiedIdentity{
		Provider: SocialProviderTelegram, Subject: "tg-unbound-" + suffix,
		Email: SyntheticOAuthEmail(SocialProviderTelegram, "tg-unbound-"+suffix), EmailVerified: false,
	}
	if _, err := svc.ApplyVerifiedSocialIdentity(ctx, tenantID, unbound); !errors.Is(err, ErrOAuthPendingEmailRequired) {
		t.Fatalf("④ 未绑定 telegram 登录 err=%v,应 ErrOAuthPendingEmailRequired", err)
	}
}
