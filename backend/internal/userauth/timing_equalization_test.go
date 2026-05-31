// HUAKAI · iKun

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestAuthenticate_EqualWorkOnUserMiss 守 S2-048: 密码登录不得通过响应时延泄露某邮箱是否已注册。
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
	if _, err := svc.Register(ctx, RegisterInput{TenantID: 1, Email: "real@example.test", Password: "secret"}); err != nil {
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
	// 会与「不存在(已跑 dummy)」时延不同, 暴露该邮箱是已注册的 social 账号(S2-048 R1 codex 抓的漏)。
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
