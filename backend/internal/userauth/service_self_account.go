package userauth

import (
	"context"
	"errors"
	"strings"
	"time"
)

// service_self_account.go 承载已登录用户自助账户管理(改密 / 软删)的 service 编排。
// 身份(tenantID/userID)由调用方从已认证 session 注入,service 不信任何 body 字段。

// ErrLastAdmin 表示删号被「末位 admin 保护」拒绝:本人是 tenant 内最后一个 role='admin'。
// 使用「未软删 admin 计数」保护租户的最后一个管理入口,多 admin 场景下仍可删除非末位 admin。
// store 层在同一事务内原子判定后返回此错误。
var ErrLastAdmin = errors.New("userauth: cannot delete the last admin")

type ownPasswordUpdateStore interface {
	UpdateOwnPassword(ctx context.Context, tenantID, userID int64, passwordHash string) (User, error)
}

type ownPasswordSecurityUpdateStore interface {
	UpdateOwnPasswordAndRevokeOthers(
		ctx context.Context,
		tenantID, userID int64,
		passwordHash string,
		expectedPasswordVersion int,
		currentFamilyID string,
		now time.Time,
	) (User, int64, error)
}

type selfDeleteStore interface {
	SoftDeleteUser(ctx context.Context, tenantID, userID int64, now time.Time) (User, error)
}

type selfDeleteSecurityStore interface {
	SoftDeleteUserAndRevokeSessions(
		ctx context.Context,
		tenantID, userID int64,
		now time.Time,
	) (User, int64, error)
}

// ChangeOwnPassword 校验已认证用户的旧口令后改成新口令。
//   - 旧密错 → ErrInvalidCredentials(handler 映射成 401 invalid_old_password;generic 与 reset 一致)。
//   - 校旧密走 VerifyPassword(常量时间 subtle.ConstantTimeCompare),旧/新明文绝不进日志 / 响应 / 审计 payload(CMB-5)。
//   - 新密走既有口令策略 HashPassword(空 / 不合策略 → ErrInvalidInput,handler 映射 400 invalid_password)。
//   - 持久层 UpdateOwnPassword bump password_version,失效在途 reset token。
//
// 注意:本方法仅供不承载浏览器会话的内部调用；HTTP 自助改密入口必须调用
// ChangeOwnPasswordAndRevokeOthers，保证口令与会话失效处在同一事务。
func (s *Service) ChangeOwnPassword(ctx context.Context, tenantID, userID int64, oldPassword, newPassword string) (User, error) {
	_, newHash, err := s.prepareOwnPasswordChange(ctx, tenantID, userID, oldPassword, newPassword)
	if err != nil {
		return User{}, err
	}
	updater, ok := s.Store.(ownPasswordUpdateStore)
	if !ok {
		return User{}, ErrStoreNotConfigured
	}
	return updater.UpdateOwnPassword(ctx, tenantID, userID, newHash)
}

// ChangeOwnPasswordAndRevokeOthers 在一个数据库事务里修改口令并撤销除当前会话族外的所有会话。
// 会话创建与本操作共用用户级事务锁，因此不会在改密窗口签发漏网会话。
func (s *Service) ChangeOwnPasswordAndRevokeOthers(
	ctx context.Context,
	tenantID, userID int64,
	oldPassword, newPassword, currentFamilyID string,
) (User, int64, error) {
	user, newHash, err := s.prepareOwnPasswordChange(ctx, tenantID, userID, oldPassword, newPassword)
	if err != nil {
		return User{}, 0, err
	}
	updater, ok := s.Store.(ownPasswordSecurityUpdateStore)
	if !ok {
		return User{}, 0, ErrStoreNotConfigured
	}
	return updater.UpdateOwnPasswordAndRevokeOthers(
		ctx,
		tenantID,
		userID,
		newHash,
		user.PasswordVersion,
		currentFamilyID,
		s.now(),
	)
}

func (s *Service) prepareOwnPasswordChange(
	ctx context.Context,
	tenantID, userID int64,
	oldPassword, newPassword string,
) (User, string, error) {
	if s == nil || s.Store == nil {
		return User{}, "", ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return User{}, "", ErrInvalidInput
	}
	if strings.TrimSpace(oldPassword) == "" || strings.TrimSpace(newPassword) == "" {
		return User{}, "", ErrInvalidInput
	}
	user, err := s.Store.GetUserByID(ctx, tenantID, userID)
	if err != nil {
		return User{}, "", err
	}
	if user.PasswordHash == "" {
		// social-only 账号无本地口令可校验 → 不放行自助改密(与登录侧无本地口令语义一致)。
		return User{}, "", ErrInvalidCredentials
	}
	verified, verifyErr := verifyPasswordFn(user.PasswordHash, oldPassword)
	if verifyErr != nil || !verified {
		return User{}, "", ErrInvalidCredentials
	}
	newHash, err := HashPassword(newPassword, s.PasswordPolicy)
	if err != nil {
		return User{}, "", err
	}
	return user, newHash, nil
}

// SoftDeleteSelf 软删已认证用户自己。末位 admin 保护在 store 层同一事务内原子判定:
// 本人是 tenant 内末位未软删 admin → store 返 ErrLastAdmin(handler 映射 409)。
//   - 否则 store 软删(status='deleted' + deleted_at=NOW())并同 Tx revoke 本人 api_key。
//   - 撤销本人全部 session 由 handler 在软删成功后显式调 usersession.Revoke(UserID 全撤路径)完成。
//   - 不退款 / 不清 user_balances(本切片范围外,删号余额处置策略另列切片决定)。
func (s *Service) SoftDeleteSelf(ctx context.Context, tenantID, userID int64) (User, error) {
	if s == nil || s.Store == nil {
		return User{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return User{}, ErrInvalidInput
	}
	deleter, ok := s.Store.(selfDeleteStore)
	if !ok {
		return User{}, ErrStoreNotConfigured
	}
	return deleter.SoftDeleteUser(ctx, tenantID, userID, s.now())
}

// SoftDeleteSelfAndRevokeSessions 在一个事务里完成末位管理员保护、账号软删、
// API Key 失效和全部会话撤销。
func (s *Service) SoftDeleteSelfAndRevokeSessions(
	ctx context.Context,
	tenantID, userID int64,
) (User, int64, error) {
	if s == nil || s.Store == nil {
		return User{}, 0, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return User{}, 0, ErrInvalidInput
	}
	deleter, ok := s.Store.(selfDeleteSecurityStore)
	if !ok {
		return User{}, 0, ErrStoreNotConfigured
	}
	return deleter.SoftDeleteUserAndRevokeSessions(ctx, tenantID, userID, s.now())
}
