//go:build integration_pg

package userauth

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/signupreward"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

func TestPGRegisterCommitsUserAndRewardExpectationTogether(t *testing.T) {
	ctx := context.Background()
	pool := openUserAuthProfilePool(t, ctx)
	t.Cleanup(pool.Close)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedUserAuthProfileTenant(t, ctx, pool, "signup-reward-"+suffix)
	t.Cleanup(func() { cleanupUserAuthProfileTenant(t, ctx, pool, tenantID) })

	svc := NewService(NewPostgresStore(pool))
	svc.RequireVerified = false
	svc.PasswordPolicy = cheapPasswordPolicy()
	svc.SignupRewards = SignupRewardConfig{SignupBonusCents: 125}
	result, err := svc.Register(ctx, RegisterInput{
		TenantID: tenantID, Email: "reward-" + suffix + "@example.test", Password: "secret12",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	eventID := fmt.Sprintf("signup-reward-%d-%d-signup_bonus", tenantID, result.User.ID)
	var storedTenant, storedUser, amount int64
	var kind, status string
	err = pool.QueryRow(ctx, `
SELECT (payload->>'tenant_id')::bigint,
       (payload->>'user_id')::bigint,
       payload->>'reward_kind',
       (payload->>'amount_cents')::bigint,
       status
FROM outbox_events
WHERE id = $1 AND tenant_id = $2`,
		eventID, tenantID,
	).Scan(&storedTenant, &storedUser, &kind, &amount, &status)
	if err != nil {
		t.Fatalf("read reward expectation: %v", err)
	}
	if storedTenant != tenantID || storedUser != result.User.ID ||
		kind != "signup_bonus" || amount != 125 || status != "pending" {
		t.Fatalf("reward expectation=%d/%d/%s/%d/%s",
			storedTenant, storedUser, kind, amount, status)
	}
}

func TestPGRegisterCommitsInviteVerificationRewardsAndSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := openUserAuthProfilePool(t, ctx)
	t.Cleanup(pool.Close)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedUserAuthProfileTenant(t, ctx, pool, "signup-full-"+suffix)
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM session_tokens WHERE tenant_id=$1`,
			`DELETE FROM refresh_tokens WHERE tenant_id=$1`,
			`DELETE FROM session_families WHERE tenant_id=$1`,
			`DELETE FROM outbox_events WHERE tenant_id=$1`,
			`DELETE FROM email_verification_tokens WHERE tenant_id=$1`,
			`DELETE FROM invite_bindings WHERE tenant_id=$1`,
			`DELETE FROM invite_codes WHERE tenant_id=$1`,
		} {
			if _, err := pool.Exec(context.Background(), query, tenantID); err != nil {
				t.Errorf("清理注册全链事实 %q: %v", query, err)
			}
		}
		cleanupUserAuthProfileTenant(t, context.Background(), pool, tenantID)
	})

	rawInvite, inviteHash, err := GenerateInviteCode()
	if err != nil {
		t.Fatalf("生成邀请码: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO invite_codes (code, tenant_id, max_uses, status)
VALUES ($1, $2, 1, 'active')`, inviteHash, tenantID); err != nil {
		t.Fatalf("写入邀请码: %v", err)
	}

	svc := NewService(NewPostgresStore(pool))
	svc.RequireVerified = true
	svc.RegistrationMode = RegistrationModeInviteRequired
	svc.PasswordPolicy = cheapPasswordPolicy()
	svc.SignupRewards = SignupRewardConfig{
		SignupBonusCents:   125,
		InviteeRewardCents: 75,
	}
	email := "full-" + suffix + "@example.test"
	result, err := svc.Register(ctx, RegisterInput{
		TenantID: tenantID, Email: email, Password: "secret12", InviteCode: rawInvite,
	})
	if err != nil {
		t.Fatalf("完整注册: %v", err)
	}
	if result.User.Status != UserStatusPendingVerification || result.User.EmailVerified ||
		result.User.InviteCodeUsed != inviteHash || result.VerificationToken == "" {
		t.Fatalf("注册结果未进入待验证邀请态: %+v token_empty=%v", result.User, result.VerificationToken == "")
	}

	var usedCount, bindings, activeVerifications int
	if err := pool.QueryRow(ctx, `SELECT used_count FROM invite_codes WHERE code=$1`, inviteHash).Scan(&usedCount); err != nil {
		t.Fatalf("读取邀请码使用数: %v", err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM invite_bindings
WHERE tenant_id=$1 AND user_id=$2 AND invite_code=$3`,
		tenantID, result.User.ID, inviteHash,
	).Scan(&bindings); err != nil {
		t.Fatalf("读取邀请绑定: %v", err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM email_verification_tokens
WHERE tenant_id=$1 AND user_id=$2 AND token_hash=$3 AND consumed_at IS NULL`,
		tenantID, result.User.ID, HashToken(result.VerificationToken),
	).Scan(&activeVerifications); err != nil {
		t.Fatalf("读取邮箱验证事实: %v", err)
	}
	if usedCount != 1 || bindings != 1 || activeVerifications != 1 {
		t.Fatalf("邀请码/绑定/验证事实=%d/%d/%d，期望 1/1/1", usedCount, bindings, activeVerifications)
	}

	expectedRewards := map[string]int64{
		string(signupreward.KindSignupBonus):   125,
		string(signupreward.KindInviteeReward): 75,
	}
	rows, err := pool.Query(ctx, `
SELECT payload->>'reward_kind', (payload->>'amount_cents')::bigint
FROM outbox_events
WHERE tenant_id=$1 AND payload->>'user_id'=$2 AND status='pending'`,
		tenantID, fmt.Sprint(result.User.ID),
	)
	if err != nil {
		t.Fatalf("读取注册奖励事实: %v", err)
	}
	defer rows.Close()
	actualRewards := map[string]int64{}
	for rows.Next() {
		var kind string
		var amount int64
		if err := rows.Scan(&kind, &amount); err != nil {
			t.Fatalf("扫描注册奖励事实: %v", err)
		}
		actualRewards[kind] = amount
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历注册奖励事实: %v", err)
	}
	if len(actualRewards) != len(expectedRewards) {
		t.Fatalf("注册奖励事实=%v，期望 %v", actualRewards, expectedRewards)
	}
	for kind, amount := range expectedRewards {
		if actualRewards[kind] != amount {
			t.Fatalf("注册奖励 %s=%d，期望 %d；全部事实=%v", kind, actualRewards[kind], amount, actualRewards)
		}
	}

	verified, err := svc.VerifyEmail(ctx, tenantID, result.VerificationToken)
	if err != nil {
		t.Fatalf("验证邮箱: %v", err)
	}
	if verified.Status != UserStatusActive || !verified.EmailVerified {
		t.Fatalf("验证后用户未激活: %+v", verified)
	}
	authenticated, err := svc.Authenticate(ctx, LoginInput{
		TenantID: tenantID, Email: email, Password: "secret12",
	})
	if err != nil {
		t.Fatalf("验证后登录: %v", err)
	}

	sessionSvc := usersession.NewService(usersession.NewPostgresStore(pool))
	sessionSvc.SigningKey = []byte(strings.Repeat("s", 32))
	issued, err := sessionSvc.Create(ctx, usersession.CreateInput{
		TenantID: tenantID, UserID: authenticated.ID, AuthVersion: authenticated.PasswordVersion,
	})
	if err != nil {
		t.Fatalf("登录后签发会话: %v", err)
	}
	if issued.SessionToken == "" || issued.RefreshToken == "" || issued.Family.ID == "" {
		t.Fatalf("会话令牌或家族为空: %+v", issued)
	}
	var families, refreshTokens, sessionTokens int
	for _, check := range []struct {
		query string
		args  []any
		dst   *int
	}{
		{
			`SELECT count(*)::int FROM session_families WHERE tenant_id=$1 AND user_id=$2`,
			[]any{tenantID, authenticated.ID},
			&families,
		},
		{
			`SELECT count(*)::int FROM refresh_tokens WHERE tenant_id=$1 AND family_id=$2::uuid`,
			[]any{tenantID, issued.Family.ID},
			&refreshTokens,
		},
		{
			`SELECT count(*)::int FROM session_tokens WHERE tenant_id=$1 AND family_id=$2::uuid`,
			[]any{tenantID, issued.Family.ID},
			&sessionTokens,
		},
	} {
		if err := pool.QueryRow(ctx, check.query, check.args...).Scan(check.dst); err != nil {
			t.Fatalf("读取会话最终事实: %v", err)
		}
	}
	if families != 1 || refreshTokens != 1 || sessionTokens != 1 {
		t.Fatalf("会话族/刷新/访问令牌=%d/%d/%d，期望 1/1/1", families, refreshTokens, sessionTokens)
	}
}
