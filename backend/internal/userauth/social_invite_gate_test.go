// HUAKAI · iKun

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestApplyVerifiedSocialIdentity_HonorsInviteGate 守 S2-114: 社交 OAuth 首次注册必须和密码
// Register 受同一邀请闸约束。此前 applyVerifiedSocialIdentity 的新用户分支只看 SocialSignup,
// 不看 InviteRequired —— 租户开启邀请注册时, 任意 Google/GitHub 账号能绕过 invite 闸直接开户。
//
// 三个判别 case 共同把行为钉死(单独一个都不够):
//
//	(A) 全新身份 + InviteRequired=true  → 必须 ErrInviteRequired 且**不得**建用户。
//	    mutation: 删 social_login.go 新用户分支里的 `if s.InviteRequired { return ErrInviteRequired }`
//	    → err=nil + 用户被建出 → A 红(漏洞复活)。
//	(B) 全新身份 + InviteRequired=false → 必须成功建出 active 用户。
//	    防"修过头": 若把社交注册改成无条件拒绝 → B 红。
//	(C) 既有用户(邮箱已存在)+ InviteRequired=true → 必须放行(走 link 路径), 不得被闸挡。
//	    防"闸装错位置": 若把 InviteRequired 检查放到既有用户 link 路径之前 → C 误返 ErrInviteRequired → 红。
func TestApplyVerifiedSocialIdentity_HonorsInviteGate(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)

	newIdentity := func(email string) VerifiedIdentity {
		return VerifiedIdentity{
			Provider:      SocialProviderGoogle,
			Subject:       "google-sub-" + email,
			Email:         email,
			EmailVerified: true,
			DisplayName:   "Social User",
		}
	}

	// (A) 全新身份 + 要求邀请 → 拒绝, 且不建用户。
	t.Run("new_signup_blocked_when_invite_required", func(t *testing.T) {
		store := newMemoryAuthStore(now)
		svc := NewService(store)
		svc.SocialSignup = true
		svc.InviteRequired = true

		const email = "newcomer@example.test"
		_, err := svc.applyVerifiedSocialIdentity(ctx, 1, newIdentity(email))
		if !errors.Is(err, ErrInviteRequired) {
			t.Fatalf("social signup under InviteRequired returned %v, want ErrInviteRequired (invite gate bypassed)", err)
		}
		// 关键判别: 被拒后绝不能留下账号 —— 否则攻击者照样开了户。
		if _, gerr := store.GetUserByEmail(ctx, 1, email); !errors.Is(gerr, ErrUserNotFound) {
			t.Fatalf("rejected social signup still persisted a user (GetUserByEmail err=%v); invite gate leaked an account", gerr)
		}
	})

	// (B) 全新身份 + 不要求邀请 → 正常建出 active 用户(确认闸不过度拦截)。
	t.Run("new_signup_allowed_when_invite_not_required", func(t *testing.T) {
		store := newMemoryAuthStore(now)
		svc := NewService(store)
		svc.SocialSignup = true
		svc.InviteRequired = false

		const email = "opensignup@example.test"
		user, err := svc.applyVerifiedSocialIdentity(ctx, 1, newIdentity(email))
		if err != nil {
			t.Fatalf("social signup without invite requirement failed: %v (gate must not block when invites are off)", err)
		}
		if user.Status != UserStatusActive {
			t.Fatalf("social signup user status = %q, want %q", user.Status, UserStatusActive)
		}
		if user.SocialLoginProvider != SocialProviderGoogle {
			t.Fatalf("social signup provider = %q, want %q", user.SocialLoginProvider, SocialProviderGoogle)
		}
		if _, gerr := store.GetUserByEmail(ctx, 1, email); gerr != nil {
			t.Fatalf("social signup user not persisted: %v", gerr)
		}
	})

	// (C) 既有用户(邮箱已注册)+ 要求邀请 → 社交登录/绑定仍放行(闸只管新注册, 不管既有用户登录)。
	t.Run("existing_user_login_not_blocked_by_invite_gate", func(t *testing.T) {
		store := newMemoryAuthStore(now)
		svc := NewService(store)
		svc.SocialSignup = true
		svc.InviteRequired = true

		const email = "veteran@example.test"
		seeded, err := store.CreateUser(ctx, CreateUserParams{
			TenantID: 1, Email: email, EmailVerified: true, Status: UserStatusActive,
		})
		if err != nil {
			t.Fatalf("seed existing user: %v", err)
		}

		user, err := svc.applyVerifiedSocialIdentity(ctx, 1, newIdentity(email))
		if err != nil {
			t.Fatalf("existing-user social login under InviteRequired returned %v, want success (gate must scope to new signups only)", err)
		}
		if user.ID != seeded.ID {
			t.Fatalf("existing-user social login created a NEW user (id=%d) instead of linking the seeded one (id=%d)", user.ID, seeded.ID)
		}
	})

	// (D) 已绑定社交身份(GetUserBySocialIdentity 命中)+ 要求邀请 → 复登仍放行。这条覆盖的是与 C
	// 不同的分支(social_login.go:153 已绑定身份分支, C 覆盖的是 :161 邮箱 link 分支), 强化分支级回归。
	// mutation: 若把 InviteRequired 闸放到 GetUserBySocialIdentity 命中分支之前 → 已绑定用户复登误返
	// ErrInviteRequired → 红。
	t.Run("already_linked_identity_relogin_not_blocked", func(t *testing.T) {
		store := newMemoryAuthStore(now)
		svc := NewService(store)
		svc.SocialSignup = true

		const email = "linked@example.test"
		identity := newIdentity(email)

		// 先在不要求邀请时完成首次注册 + 绑定。
		svc.InviteRequired = false
		first, err := svc.applyVerifiedSocialIdentity(ctx, 1, identity)
		if err != nil {
			t.Fatalf("seed linked social user: %v", err)
		}

		// 随后租户开启邀请注册; 同一已绑定身份复登必须仍走 social-identity 命中分支放行。
		svc.InviteRequired = true
		again, err := svc.applyVerifiedSocialIdentity(ctx, 1, identity)
		if err != nil {
			t.Fatalf("already-linked relogin under InviteRequired returned %v, want success (gate must not touch existing linked identities)", err)
		}
		if again.ID != first.ID {
			t.Fatalf("already-linked relogin returned id=%d, want the originally linked id=%d", again.ID, first.ID)
		}
	})
}
