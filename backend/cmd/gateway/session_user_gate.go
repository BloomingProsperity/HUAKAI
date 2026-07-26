// HUAKAI · iKun

package main

import (
	"context"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// sessionUserGate 把 userauth 的租户和账号状态映射成会话使用期资格。
// GetProfile 的生产查询已联表限定 active 租户；用户 disabled/deleted 同样即时断会话。
// locked 是登录失败的临时锁，只守登录门，不杀既有会话，避免错误密码攻击变成会话拒绝服务。
type sessionUserGate struct {
	auth *userauth.Service
}

func (g sessionUserGate) CheckSessionUser(ctx context.Context, tenantID, userID int64) error {
	user, err := g.auth.GetProfile(ctx, tenantID, userID)
	if err != nil {
		if errors.Is(err, userauth.ErrUserNotFound) {
			// 软删用户被 store 过滤 → 视同删除, 拒。
			return usersession.ErrUserIneligible
		}
		return err
	}
	switch user.Status {
	case userauth.UserStatusDisabled, userauth.UserStatusDeleted:
		return usersession.ErrUserIneligible
	}
	return nil
}
