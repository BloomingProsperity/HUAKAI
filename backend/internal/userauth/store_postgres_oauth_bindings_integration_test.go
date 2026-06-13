//go:build integration_pg

package userauth

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// 列表只返回「该 tenant+user」的绑定,绝不串到同租户另一用户。
// discriminating fixture: 同一 tenant 下用户 A 绑 google、用户 B 绑 github;查 A 必须只得 google,
// 且 B 的 github subject 绝不出现在 A 的结果里。
// MUTATION: ListSocialIdentityLinks 的 WHERE 漏掉 user_id(只按 tenant 过滤)→ A 的结果会含 B 的
// github → 断言 len==1 且 provider==google 变红。
func TestPGListSocialIdentityLinksScopesToUser(t *testing.T) {
	ctx := context.Background()
	pool := openUserAuthProfilePool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedUserAuthProfileTenant(t, ctx, pool, "list-bindings-"+suffix)
	t.Cleanup(func() { cleanupUserAuthProfileTenant(t, ctx, pool, tenantID) })

	userA, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "list-a-" + suffix + "@example.test", DisplayName: "User A",
		PasswordHash: "argon2id-test-hash", EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser A: %v", err)
	}
	userB, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "list-b-" + suffix + "@example.test", DisplayName: "User B",
		PasswordHash: "argon2id-test-hash", EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser B: %v", err)
	}
	if _, err := store.LinkSocialIdentity(ctx, tenantID, userA.ID, SocialProviderGoogle, "google-a-"+suffix); err != nil {
		t.Fatalf("LinkSocialIdentity A: %v", err)
	}
	if _, err := store.LinkSocialIdentity(ctx, tenantID, userB.ID, SocialProviderGitHub, "github-b-"+suffix); err != nil {
		t.Fatalf("LinkSocialIdentity B: %v", err)
	}

	linksA, err := store.ListSocialIdentityLinks(ctx, tenantID, userA.ID)
	if err != nil {
		t.Fatalf("ListSocialIdentityLinks A: %v", err)
	}
	if len(linksA) != 1 {
		t.Fatalf("user A links len=%d want 1 (cross-user leak would surface B's github); got=%+v", len(linksA), linksA)
	}
	if linksA[0].Provider != SocialProviderGoogle {
		t.Fatalf("user A provider=%q want google", linksA[0].Provider)
	}
	if linksA[0].Subject != "google-a-"+suffix {
		t.Fatalf("store returns raw subject; got %q want google-a-%s (masking is service-layer, not store)", linksA[0].Subject, suffix)
	}
	if linksA[0].LinkedAt.IsZero() {
		t.Fatalf("linked_at zero; want created_at populated")
	}

	linksB, err := store.ListSocialIdentityLinks(ctx, tenantID, userB.ID)
	if err != nil {
		t.Fatalf("ListSocialIdentityLinks B: %v", err)
	}
	if len(linksB) != 1 || linksB[0].Provider != SocialProviderGitHub {
		t.Fatalf("user B links=%+v want exactly github", linksB)
	}
}

// service 端到端:列表返回脱敏 subject;解绑后该 provider 从列表消失。
// MUTATION: service 跳过 maskSocialSubject → subject 原文 → 红;UnlinkSocialIdentity 不真正删行 →
// 解绑后列表仍含该 provider → 红。
func TestPGServiceListThenUnbindRemovesProvider(t *testing.T) {
	ctx := context.Background()
	pool := openUserAuthProfilePool(t, ctx)
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	svc := NewService(store)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedUserAuthProfileTenant(t, ctx, pool, "list-unbind-"+suffix)
	t.Cleanup(func() { cleanupUserAuthProfileTenant(t, ctx, pool, tenantID) })

	user, err := store.CreateUser(ctx, CreateUserParams{
		TenantID: tenantID, Email: "list-unbind-" + suffix + "@example.test", DisplayName: "Password Backed",
		PasswordHash: "argon2id-test-hash", EmailVerified: true, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	rawSubject := "github-subject-" + suffix
	if _, err := store.LinkSocialIdentity(ctx, tenantID, user.ID, SocialProviderGitHub, rawSubject); err != nil {
		t.Fatalf("LinkSocialIdentity: %v", err)
	}

	links, err := svc.ListSocialIdentityLinks(ctx, tenantID, user.ID)
	if err != nil {
		t.Fatalf("svc.ListSocialIdentityLinks: %v", err)
	}
	if len(links) != 1 || links[0].Provider != SocialProviderGitHub {
		t.Fatalf("links=%+v want exactly github", links)
	}
	if links[0].Subject == rawSubject {
		t.Fatalf("subject not masked at service layer: %q", links[0].Subject)
	}

	// 该用户有密码,解绑唯一社交绑定应被允许(末位保护只在无密码时触发)。
	unlinked, err := svc.UnlinkSocialIdentity(ctx, tenantID, user.ID, SocialProviderGitHub)
	if err != nil {
		t.Fatalf("UnlinkSocialIdentity: %v", err)
	}
	if !unlinked {
		t.Fatal("unlinked=false want true")
	}

	after, err := svc.ListSocialIdentityLinks(ctx, tenantID, user.ID)
	if err != nil {
		t.Fatalf("svc.ListSocialIdentityLinks after unbind: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("links after unbind=%+v want empty (provider removed)", after)
	}
}
