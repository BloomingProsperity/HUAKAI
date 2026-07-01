// Package adminstepup 把既有 passkeyhttp step-up verifier 适配到 admin session 通道。
//
// 复用同一套密码(argon2id 常时比较)/2FA 校验原语(不重复实现,避免第二份易漂移的验证逻辑),
// 仅把底层错误 sentinel 翻译成 admin.ErrAdminStepUp*,供 adminsessionauth 解析器与已 import admin
// 的 handler 按来源统一映射状态码(403/401/429/503)。
//
// role 制单登录 P3:session 通道对被标注 SessionStepUp 的写端点要求新鲜的密码/2FA 证明。
// token 通道豁免(programmatic 持有即授权),故本适配器只在 session 源被调用。
package adminstepup

import (
	"context"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/passkeyhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/twofa"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// innerVerifier 抽象既有 verifier,便于单测注入各错误分支(生产为 *passkeyhttp.LocalStepUpVerifier)。
type innerVerifier interface {
	VerifyStepUp(context.Context, int64, int64, passkeyhttp.StepUpProof) error
}

// Verifier 适配层:结构上满足 adminsessionauth.StepUpVerifier(password/code 两串入参)。
type Verifier struct {
	inner innerVerifier
}

// New 用既有 passkey step-up 原语构造(users + 2FA verifier 与 passkey 通道同源,见 routes.go:716)。
func New(users passkeyhttp.UserStore, twoFactor passkeyhttp.TwoFactorVerifier) *Verifier {
	return &Verifier{inner: passkeyhttp.NewLocalStepUpVerifier(users, twoFactor)}
}

// VerifyStepUp 校验 step-up 证明。成功返 nil;失败翻译成 admin.ErrAdminStepUp{Required,Invalid,Locked}
// 或 admin.ErrAdminBackend(未配置/后端故障/未知一律 503,不泄露底层细节,反枚举)。
func (v *Verifier) VerifyStepUp(ctx context.Context, tenantID, userID int64, password, twoFactorCode string) error {
	if v == nil || v.inner == nil {
		return admin.ErrAdminBackend
	}
	err := v.inner.VerifyStepUp(ctx, tenantID, userID, passkeyhttp.StepUpProof{
		Password: password, TwoFactorCode: twoFactorCode,
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, passkeyhttp.ErrStepUpRequired):
		return admin.ErrAdminStepUpRequired
	case errors.Is(err, passkeyhttp.ErrStepUpInvalid),
		errors.Is(err, userauth.ErrInvalidCredentials),
		errors.Is(err, twofa.ErrInvalidCode),
		errors.Is(err, twofa.ErrCodeReused):
		return admin.ErrAdminStepUpInvalid
	case errors.Is(err, twofa.ErrLocked):
		return admin.ErrAdminStepUpLocked
	default:
		return admin.ErrAdminBackend
	}
}
