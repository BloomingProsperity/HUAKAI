// HUAKAI · iKun

package main

import (
	"context"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// sessionUserGate 把 userauth 的账号状态映射成会话使用期资格 (usersession.UserGate)。
// 只拒 disabled/deleted(封禁与删除必须即时断会话); locked 是登录失败的临时锁, 只守登录门,
// 不杀既有会话 —— 否则攻击者可用错误密码触发锁定, DoS 掉正常用户的活跃会话。
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
