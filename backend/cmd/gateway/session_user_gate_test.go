// HUAKAI · iKun

// sessionUserGate 适配器语义测试: userauth 状态 → 会话使用期资格的映射边界。
// 只拒 disabled/deleted/软删; locked 是登录门的临时锁, 不杀既有会话
// (否则攻击者可用错误密码触发锁定, DoS 掉正常用户的活跃会话)。

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// gateAuthStore 极小 userauth.Store 桩: 只实现 GetUserByID, 其余走 panic
// (适配器只该碰这一个方法, 碰别的就是语义漂移)。
type gateAuthStore struct {
	userauth.Store
	user userauth.User
	err  error
}

func (s gateAuthStore) GetUserByID(context.Context, int64, int64) (userauth.User, error) {
	return s.user, s.err
}

func TestSessionUserGate_StatusMapping(t *testing.T) {
	lockedUntil := time.Date(2026, 7, 5, 13, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		user     userauth.User
		err      error
		wantErr  error
		wantPass bool
	}{
		{"active 放行", userauth.User{ID: 1, TenantID: 1, Status: userauth.UserStatusActive}, nil, nil, true},
		{"disabled 拒", userauth.User{ID: 1, TenantID: 1, Status: userauth.UserStatusDisabled}, nil, usersession.ErrUserIneligible, false},
		{"deleted 拒", userauth.User{ID: 1, TenantID: 1, Status: userauth.UserStatusDeleted}, nil, usersession.ErrUserIneligible, false},
		{"软删(store 过滤)拒", userauth.User{}, userauth.ErrUserNotFound, usersession.ErrUserIneligible, false},
		// locked/时间锁只守登录门, 既有会话放行 —— 防失败锁定被攻击者用作会话 DoS。
		{"locked 放行", userauth.User{ID: 1, TenantID: 1, Status: userauth.UserStatusLocked}, nil, nil, true},
		{"时间锁放行", userauth.User{ID: 1, TenantID: 1, Status: userauth.UserStatusActive, LockedUntil: &lockedUntil}, nil, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := sessionUserGate{auth: userauth.NewService(gateAuthStore{user: tc.user, err: tc.err})}
			err := gate.CheckSessionUser(context.Background(), 1, 1)
			if tc.wantPass && err != nil {
				t.Fatalf("err=%v, want 放行", err)
			}
			if !tc.wantPass && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
		})
	}

	// 后端瞬时故障: 原样上抛 (fail-closed 由 usersession 侧处理), 不误映射成 ineligible。
	transient := errors.New("db down")
	gate := sessionUserGate{auth: userauth.NewService(gateAuthStore{err: transient})}
	if err := gate.CheckSessionUser(context.Background(), 1, 1); !errors.Is(err, transient) {
		t.Fatalf("backend error err=%v, want 原样上抛", err)
	}
}
