//go:build integration_pg

package tenantadmin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

type registrationPath struct {
	name            string
	wantSocialLinks int
	create          func(context.Context, *userauth.Service, int64, string) (userauth.User, error)
}

func TestRegistrationPathsSerializeWithTenantDisable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openTenantAdminPool(t, ctx)
	platformTenantID := seedTenantAdminPlatform(t, ctx, pool)
	t.Cleanup(func() { cleanupTenantAdminTenant(pool, platformTenantID) })
	tenantService := NewService(pool, platformTenantID)

	for _, path := range registrationLifecyclePaths() {
		t.Run(path.name, func(t *testing.T) {
			suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
			tenantID := seedLifecycleRegistrationTenant(t, ctx, pool, "register-lock-"+suffix)
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), `DELETE FROM outbox_events WHERE tenant_id=$1`, tenantID)
				_, _ = pool.Exec(context.Background(), `DELETE FROM social_identity_links WHERE tenant_id=$1`, tenantID)
				cleanupTenantAdminTenant(pool, tenantID)
			})

			registrationTx, err := pool.BeginTx(ctx, pgx.TxOptions{})
			if err != nil {
				t.Fatalf("开始注册事务：%v", err)
			}
			defer func() { _ = registrationTx.Rollback(context.Background()) }()
			authService := lifecycleRegistrationService(userauth.NewPostgresStore(registrationTx))
			email := "register-lock-" + suffix + "@example.test"
			user, err := path.create(ctx, authService, tenantID, email)
			if err != nil {
				t.Fatalf("执行真实注册路径：%v", err)
			}

			disabled := make(chan error, 1)
			go func() {
				_, disableErr := tenantService.SetStatus(ctx, StatusInput{
					TenantID: tenantID, Status: StatusDisabled, ExpectedVersion: 1,
					Audit: tenantAdminAudit("并发停用注册中的租户"),
				})
				disabled <- disableErr
			}()
			select {
			case disableErr := <-disabled:
				_ = registrationTx.Rollback(ctx)
				t.Fatalf("注册事务未提交时停用越过生命周期锁：%v", disableErr)
			case <-time.After(200 * time.Millisecond):
			}

			if err := registrationTx.Commit(ctx); err != nil {
				t.Fatalf("提交注册事务：%v", err)
			}
			select {
			case disableErr := <-disabled:
				if disableErr != nil {
					t.Fatalf("注册提交后停用租户：%v", disableErr)
				}
			case <-ctx.Done():
				t.Fatalf("注册提交后停用仍阻塞：%v", ctx.Err())
			}
			assertLifecycleRegistrationFacts(
				t, ctx, pool, tenantID, user.ID, email,
				StatusDisabled, 1, path.wantSocialLinks,
			)
		})
	}
}

func TestRegistrationPathsRejectAfterTenantDisableWithoutFacts(t *testing.T) {
	ctx := context.Background()
	pool := openTenantAdminPool(t, ctx)
	platformTenantID := seedTenantAdminPlatform(t, ctx, pool)
	t.Cleanup(func() { cleanupTenantAdminTenant(pool, platformTenantID) })
	tenantService := NewService(pool, platformTenantID)

	for _, path := range registrationLifecyclePaths() {
		t.Run(path.name, func(t *testing.T) {
			suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
			tenantID := seedLifecycleRegistrationTenant(t, ctx, pool, "disabled-register-"+suffix)
			t.Cleanup(func() { cleanupTenantAdminTenant(pool, tenantID) })
			if _, err := tenantService.SetStatus(ctx, StatusInput{
				TenantID: tenantID, Status: StatusDisabled, ExpectedVersion: 1,
				Audit: tenantAdminAudit("注册前停用租户"),
			}); err != nil {
				t.Fatalf("注册前停用租户：%v", err)
			}

			email := "disabled-register-" + suffix + "@example.test"
			_, err := path.create(ctx, lifecycleRegistrationService(userauth.NewPostgresStore(pool)), tenantID, email)
			if !errors.Is(err, userauth.ErrRegistrationDisabled) {
				t.Fatalf("停用后注册错误=%v，期望 ErrRegistrationDisabled", err)
			}
			assertLifecycleRegistrationFacts(
				t, ctx, pool, tenantID, 0, email,
				StatusDisabled, 0, 0,
			)
		})
	}
}

func registrationLifecyclePaths() []registrationPath {
	return []registrationPath{
		{
			name: "密码注册",
			create: func(ctx context.Context, service *userauth.Service, tenantID int64, email string) (userauth.User, error) {
				result, err := service.Register(ctx, userauth.RegisterInput{
					TenantID: tenantID, Email: email, Password: "StrongPass!2026",
				})
				return result.User, err
			},
		},
		{
			name:            "社交身份首次注册",
			wantSocialLinks: 1,
			create: func(ctx context.Context, service *userauth.Service, tenantID int64, email string) (userauth.User, error) {
				return service.ApplyVerifiedSocialIdentity(ctx, tenantID, userauth.VerifiedIdentity{
					Provider: userauth.SocialProviderGoogle, Subject: "subject-" + email,
					Email: email, EmailVerified: true, DisplayName: "社交注册用户",
				})
			},
		},
		{
			name:            "社交身份补全邮箱",
			wantSocialLinks: 1,
			create: func(ctx context.Context, service *userauth.Service, tenantID int64, email string) (userauth.User, error) {
				return service.CompleteSocialSignupWithVerifiedEmail(ctx, tenantID, userauth.VerifiedIdentity{
					Provider: userauth.SocialProviderTelegram, Subject: "subject-" + email,
					DisplayName: "补全邮箱用户",
				}, email)
			},
		},
	}
}

func lifecycleRegistrationService(store userauth.Store) *userauth.Service {
	service := userauth.NewService(store)
	service.RequireVerified = false
	service.SocialSignup = true
	service.PasswordPolicy = userauth.PasswordPolicy{
		MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16,
	}
	service.SignupRewards = userauth.SignupRewardConfig{SignupBonusCents: 125}
	return service
}

func seedLifecycleRegistrationTenant(t *testing.T, ctx context.Context, tx interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, name string) int64 {
	t.Helper()
	var tenantID int64
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&tenantID); err != nil {
		t.Fatalf("创建注册生命周期租户：%v", err)
	}
	return tenantID
}

func assertLifecycleRegistrationFacts(
	t *testing.T,
	ctx context.Context,
	tx interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	tenantID, userID int64,
	email, wantTenantStatus string,
	wantFacts, wantSocialLinks int,
) {
	t.Helper()
	var status string
	var users, rewards, socialLinks, inviteBindings, verificationTokens, sessionFamilies int
	if err := tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id=$1`, tenantID).Scan(&status); err != nil {
		t.Fatalf("读取租户状态：%v", err)
	}
	if err := tx.QueryRow(ctx,
		`SELECT count(*)::int FROM users WHERE tenant_id=$1 AND lower(email)=lower($2)`,
		tenantID, email,
	).Scan(&users); err != nil {
		t.Fatalf("读取注册用户：%v", err)
	}
	if err := tx.QueryRow(ctx, `
SELECT count(*)::int
FROM outbox_events
WHERE tenant_id=$1 AND payload->>'user_id'=$2 AND event_type=$3`,
		tenantID, fmt.Sprint(userID), obsdlq.EventTypeSignupReward,
	).Scan(&rewards); err != nil {
		t.Fatalf("读取注册奖励事实：%v", err)
	}
	for _, query := range []struct {
		name string
		sql  string
		dst  *int
	}{
		{"社交身份", `SELECT count(*)::int FROM social_identity_links WHERE tenant_id=$1`, &socialLinks},
		{"邀请绑定", `SELECT count(*)::int FROM invite_bindings WHERE tenant_id=$1`, &inviteBindings},
		{"邮箱验证", `SELECT count(*)::int FROM email_verification_tokens WHERE tenant_id=$1`, &verificationTokens},
		{"会话族", `SELECT count(*)::int FROM session_families WHERE tenant_id=$1`, &sessionFamilies},
	} {
		if err := tx.QueryRow(ctx, query.sql, tenantID).Scan(query.dst); err != nil {
			t.Fatalf("读取%s事实：%v", query.name, err)
		}
	}
	if status != wantTenantStatus || users != wantFacts || rewards != wantFacts ||
		socialLinks != wantSocialLinks || inviteBindings != 0 ||
		verificationTokens != 0 || sessionFamilies != 0 {
		t.Fatalf(
			"租户/用户/奖励/社交/邀请/验证/会话=%s/%d/%d/%d/%d/%d/%d，期望 %s/%d/%d/%d/0/0/0",
			status, users, rewards, socialLinks, inviteBindings, verificationTokens, sessionFamilies,
			wantTenantStatus, wantFacts, wantFacts, wantSocialLinks,
		)
	}
}
