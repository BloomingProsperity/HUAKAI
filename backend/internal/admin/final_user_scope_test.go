package admin

import (
	"errors"
	"testing"
)

// 该矩阵锁定三身份的终端用户管理边界。变异：
// 把 platform_admin 分支改回无条件放行，会让“下级租户”用例由拒绝变成功。
func TestCanManageFinalUsersForTenant(t *testing.T) {
	const platformTenantID int64 = 7
	tests := []struct {
		name     string
		identity AdminIdentity
		target   int64
		platform int64
		wantErr  error
	}{
		{
			name:     "部署者管理平台租户",
			identity: AdminIdentity{Role: RolePlatformAdmin},
			target:   platformTenantID,
			platform: platformTenantID,
		},
		{
			name:     "部署者不得管理下级租户用户",
			identity: AdminIdentity{Role: RolePlatformAdmin},
			target:   8,
			platform: platformTenantID,
			wantErr:  ErrAdminForbidden,
		},
		{
			name:     "部署者作用域未接线时拒绝",
			identity: AdminIdentity{Role: RolePlatformAdmin},
			target:   platformTenantID,
			platform: 0,
			wantErr:  ErrAdminBackend,
		},
		{
			name:     "租户管理员管理本租户",
			identity: AdminIdentity{Role: RoleTenantOperator, ScopeTenantID: 8},
			target:   8,
			platform: platformTenantID,
		},
		{
			name:     "租户管理员不得跨租户",
			identity: AdminIdentity{Role: RoleTenantOperator, ScopeTenantID: 8},
			target:   9,
			platform: platformTenantID,
			wantErr:  ErrAdminForbidden,
		},
		{
			name:     "普通角色不得进入管理面",
			identity: AdminIdentity{Role: "user"},
			target:   platformTenantID,
			platform: platformTenantID,
			wantErr:  ErrAdminUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.identity.CanManageFinalUsersForTenant(tc.target, tc.platform)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("意外拒绝：%v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("错误=%v，期望 %v", err, tc.wantErr)
			}
		})
	}
}
