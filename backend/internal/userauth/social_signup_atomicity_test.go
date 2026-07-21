package userauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestSocialSignupRollsBackOnLinkFailure 钉死「建用户 + 绑社交身份」的注册事务原子性:
// 当 LinkSocialIdentity 中途失败时,先建出的用户必须随事务整体回滚,不得留下孤儿用户占住邮箱
// (否则该邮箱此后既登不进、也无法重新注册)。
//
// 判别性(变异刀):把 social_login.go 两处的 withStoreTx 包裹去掉、还原成
// s.Store.CreateUser + s.Store.LinkSocialIdentity 两次裸写 → CreateUser 已落库、link 失败后
// 孤儿残留 → GetUserByEmail 拿得到用户 → 两个子用例都转红。
func TestSocialSignupRollsBackOnLinkFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)

	// applyVerifiedSocialIdentity 的全新用户分支(OAuth 自带已验证邮箱)。
	t.Run("apply_verified_social_identity_new_user", func(t *testing.T) {
		store := newMemoryAuthStore(now)
		store.failLink = true // CreateUser 成功、LinkSocialIdentity 注入失败
		svc := NewService(store)
		svc.SocialSignup = true

		const email = "orphan-oauth@example.test"
		_, err := svc.applyVerifiedSocialIdentity(ctx, 1, VerifiedIdentity{
			Provider:      SocialProviderGoogle,
			Subject:       "google-sub-orphan",
			Email:         email,
			EmailVerified: true,
			DisplayName:   "Orphan",
		})
		if err == nil {
			t.Fatal("link 注入失败后应返回 err,却返回 nil")
		}
		if _, gerr := store.GetUserByEmail(ctx, 1, email); !errors.Is(gerr, ErrUserNotFound) {
			t.Fatalf("link 失败后用户未回滚(GetUserByEmail err=%v);孤儿残留 + 邮箱被占", gerr)
		}
	})

	// CompleteSocialSignupWithVerifiedEmail 的补邮箱建号分支(无邮箱社交源)。
	t.Run("complete_social_signup_with_verified_email", func(t *testing.T) {
		store := newMemoryAuthStore(now)
		store.failLink = true
		svc := NewService(store)
		svc.SocialSignup = true

		const email = "orphan-fill@example.test"
		_, err := svc.CompleteSocialSignupWithVerifiedEmail(ctx, 1, VerifiedIdentity{
			Provider:      SocialProviderQQ,
			Subject:       "qq-orphan",
			Email:         SyntheticOAuthEmail(SocialProviderQQ, "qq-orphan"),
			EmailVerified: false,
		}, email)
		if err == nil {
			t.Fatal("link 注入失败后应返回 err,却返回 nil")
		}
		if _, gerr := store.GetUserByEmail(ctx, 1, email); !errors.Is(gerr, ErrUserNotFound) {
			t.Fatalf("link 失败后用户未回滚(GetUserByEmail err=%v);孤儿残留 + 邮箱被占", gerr)
		}
	})
}
