// HUAKAI · iKun

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestAuthenticate_EqualWorkOnUserMiss 验证 密码登录不得通过响应时延泄露某邮箱是否已注册。
// 修复让「邮箱不存在」也跑一次等价 argon2 校验。这里用**确定性**方式断言(不用易抖的 wall-clock):
// 经 verifyPasswordFn hook 计口令校验次数 —— 不存在的邮箱必须仍恰好触发 1 次校验, 且被校验的 hash
// 必须是真实可解析的 argon2id(确保是真 argon2 等工, 而非空串/no-op 的快速失败)。
//
// mutation: 删掉 ErrUserNotFound 分支里的 dummy verifyPasswordFn 调用 → miss 路径 0 次校验 →
// calls==0 → 本测红(存在性/时序侧信道复活)。
func TestAuthenticate_EqualWorkOnUserMiss(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	store := newMemoryAuthStore(now)
	svc := NewService(store)
	svc.RequireVerified = false
	svc.PasswordPolicy = PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	svc.Now = func() time.Time { return now }

	// 注册一个真实用户, 作为「存在但口令错」的对照组。
	if _, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "real@example.test", Password: "secret12"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	orig := verifyPasswordFn
	t.Cleanup(func() { verifyPasswordFn = orig })
	var calls int
	var lastHash string
	verifyPasswordFn = func(encoded, password string) (bool, error) {
		calls++
		lastHash = encoded
		return orig(encoded, password)
	}

	// (A) 不存在的邮箱: 必须仍跑一次 argon2 校验(等工), 返回 ErrInvalidCredentials。
	calls, lastHash = 0, ""
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "ghost@example.test", Password: "whatever"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("missing-email auth error = %v, want ErrInvalidCredentials", err)
	}
	if calls != 1 {
		t.Fatalf("missing-email login ran %d password verifications, want exactly 1 (timing-equalization argon2 on miss); 0 = existence/timing oracle", calls)
	}
	// 被校验的必须是真实可解析的 argon2id hash(确保是真 argon2 等工, 不是 no-op/空串快速失败)。
	if _, perr := VerifyPassword(lastHash, "x"); perr != nil {
		t.Fatalf("miss-path verified against a non-argon2id hash (%q): work not equivalent, timing still leaks; parse err=%v", lastHash, perr)
	}

	// (B) 存在但口令错: 也恰好一次校验 —— 与 (A) 等工(同样 1 次 argon2)。
	calls = 0
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "real@example.test", Password: "wrong"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong-password auth error = %v, want ErrInvalidCredentials", err)
	}
	if calls != 1 {
		t.Fatalf("existing-user wrong-password ran %d verifications, want 1 (must equal the miss path)", calls)
	}

	// (C) 存在但无本地口令(social-only): 密码登录也必须跑一次等价 argon2 —— 否则其「快速返回」
	// 会与「不存在(已跑 dummy)」时延不同, 暴露该邮箱是已注册的 social 账号。
	// mutation: 删掉 PasswordHash=="" 分支里的 dummy verify → calls==0 → 红。
	if _, err := svc.applyVerifiedSocialIdentity(ctx, 1, VerifiedIdentity{
		Provider: SocialProviderGoogle, Subject: "g-sub", Email: "social@example.test", EmailVerified: true,
	}); err != nil {
		t.Fatalf("create social-only user: %v", err)
	}
	calls, lastHash = 0, ""
	if _, err := svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "social@example.test", Password: "whatever"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("social-only password login error = %v, want ErrInvalidCredentials", err)
	}
	if calls != 1 {
		t.Fatalf("social-only (passwordless) login ran %d verifications, want 1 (equal work; 0 = leaks the email is a registered social account)", calls)
	}
	// social-only 分支也必须用与 miss/wrong-password 完全等价的成本(同一常量 hash),否则其 argon2 耗时
	// 不同仍可区分。断言等于常量是最强判别 —— mutation: 把该分支 dummy 换成空串 / 换成另一只低成本
	// 合法 argon2id → lastHash != timingEqualizationHash → 红(仅断言可解析抓不住低成本掺水)。
	if lastHash != timingEqualizationHash {
		t.Fatalf("social-only path verified against %q, want the canonical timingEqualizationHash; mismatched cost re-opens the timing oracle", lastHash)
	}
}

// TestAuthenticate_EqualWorkOnAccountStateBranches 验证 disabled/locked/
// reset / unverified 这些「因账号状态失败」的分支此前在 argon2 之前 early-return, 比「口令错」(跑
// argon2)快得多 → 泄露「该邮箱存在且处于某状态」(时序枚举侧信道)。修复让每条状态分支返回前也跑
// 一次等价 argon2(用用户真实 hash, 成本与口令校验一致)。本测断言每条恰好 1 次校验且仍返回各自
// typed error(供 handler 审计;对外 generic 由 handler 层并入, 见 gatewayhttp 测)。
//
// mutation: 删掉任一状态分支的 equalizeLoginWork 调用 → 该 case calls==0 → 红(时序 oracle 复活)。
func TestAuthenticate_EqualWorkOnAccountStateBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	cheap := PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}

	cases := []struct {
		name          string
		status        UserStatus
		emailVerified bool
		wantErr       error
	}{
		{"disabled", UserStatusDisabled, true, ErrUserDisabled},
		{"reset_required", UserStatusResetRequired, true, ErrPasswordResetRequired},
		{"locked", UserStatusLocked, true, ErrUserLocked},
		{"unverified", UserStatusActive, false, ErrEmailUnverified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryAuthStore(now)
			svc := NewService(store)
			svc.PasswordPolicy = cheap
			svc.RequireVerified = true // 让 unverified 分支可触发;有口令的 active 用户需先验证
			svc.Now = func() time.Time { return now }

			hash, err := HashPassword("secret12", cheap)
			if err != nil {
				t.Fatalf("hash: %v", err)
			}
			if _, err := store.CreateUser(ctx, CreateUserParams{
				TenantID: 1, Email: "u@example.test", PasswordHash: hash,
				EmailVerified: tc.emailVerified, Status: tc.status,
			}); err != nil {
				t.Fatalf("create user: %v", err)
			}

			orig := verifyPasswordFn
			t.Cleanup(func() { verifyPasswordFn = orig })
			var calls int
			verifyPasswordFn = func(encoded, password string) (bool, error) {
				calls++
				return orig(encoded, password)
			}

			calls = 0
			_, err = svc.Authenticate(ctx, LoginInput{TenantID: 1, Email: "u@example.test", Password: "secret-wrong-or-right"})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("auth error = %v, want %v", err, tc.wantErr)
			}
			if calls != 1 {
				t.Fatalf("%s branch ran %d argon2 verifications, want exactly 1 (equal work with wrong-password path); 0 = timing oracle leaks account state", tc.name, calls)
			}
		})
	}
}
